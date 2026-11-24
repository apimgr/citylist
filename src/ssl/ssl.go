package ssl

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v5/challenge/tlsalpn01"
)

// Config holds SSL/TLS configuration
type Config struct {
	Enabled     bool
	CertPath    string
	Cert        string
	Key         string
	MinVersion  string
	Staging     bool
	LetsEncrypt LetsEncryptConfig
}

// LetsEncryptConfig holds Let's Encrypt settings
type LetsEncryptConfig struct {
	Enabled         bool
	Email           string
	Challenge       string // http-01, tls-alpn-01, dns-01
	Staging         bool
	DNSProviderType string
	DNSCredentials  map[string]string
	DNSProviderKey  string
	RFC2136Server   string
	RFC2136Name     string
	RFC2136Algo     string
}

// Manager handles SSL/TLS certificates
type Manager struct {
	config        Config
	mu            sync.RWMutex
	issueMu       sync.Mutex
	domains       []string
	challenges    map[string]string
	tlsChallenges map[string]*tls.Certificate
}

// NewManager creates a new SSL manager
func NewManager(cfg Config) *Manager {
	return &Manager{config: cfg, challenges: make(map[string]string), tlsChallenges: make(map[string]*tls.Certificate)}
}

// GetTLSConfig returns TLS configuration for the server
func (m *Manager) GetTLSConfig(domains []string) (*tls.Config, error) {
	m.mu.Lock()
	m.domains = append([]string(nil), domains...)
	m.mu.Unlock()

	if !m.config.Enabled {
		return nil, nil
	}

	if m.config.LetsEncrypt.Enabled {
		switch ParseChallenge(m.config.LetsEncrypt.Challenge) {
		case "dns-01":
			if err := m.validateDNSProvider(); err != nil {
				return nil, err
			}
		case "http-01", "tls-alpn-01":
		default:
			return nil, fmt.Errorf("unsupported ACME challenge %q", m.config.LetsEncrypt.Challenge)
		}
		return &tls.Config{MinVersion: m.minTLSVersion(), NextProtos: []string{tlsalpn01.ACMETLS1Protocol, "h2", "http/1.1"}, GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			return m.getACMECertificate(hello.Context(), hello.ServerName, domains, hello.SupportedProtos)
		}}, nil
	}
	cert, key := m.findConfiguredCerts(domains)
	if cert != "" && key != "" {
		return m.loadTLSCertificate(cert, key, domains)
	}

	return nil, fmt.Errorf("no certificates available and Let's Encrypt not enabled")
}

func (m *Manager) loadTLSCertificate(certPath, keyPath string, domains []string) (*tls.Config, error) {
	tlsCert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load certificate: %w", err)
	}
	if len(tlsCert.Certificate) == 0 {
		return nil, errors.New("certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}
	if !time.Now().Before(leaf.NotAfter) {
		return nil, errors.New("certificate is expired")
	}
	if len(domains) > 0 {
		if err := leaf.VerifyHostname(domains[0]); err != nil {
			return nil, fmt.Errorf("certificate does not match configured FQDN: %w", err)
		}
	}
	minVersion := uint16(tls.VersionTLS12)
	if strings.EqualFold(strings.TrimSpace(m.config.MinVersion), "TLS1.3") {
		minVersion = tls.VersionTLS13
	}
	log.Printf("Using certificate: %s", certPath)
	return &tls.Config{Certificates: []tls.Certificate{tlsCert}, MinVersion: minVersion}, nil
}

// GetHTTPHandler returns HTTP handler for ACME challenges
func (m *Manager) GetHTTPHandler(fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.handleHTTPChallenge(w, r) {
			return
		}
		fallback.ServeHTTP(w, r)
	})
}

func (m *Manager) handleHTTPChallenge(w http.ResponseWriter, r *http.Request) bool {
	if ParseChallenge(m.config.LetsEncrypt.Challenge) != "http-01" || r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") {
		return false
	}
	token := strings.TrimPrefix(r.URL.Path, "/.well-known/acme-challenge/")
	if token == "" || strings.Contains(token, "/") {
		http.NotFound(w, r)
		return true
	}
	m.mu.RLock()
	keyAuth, ok := m.challenges[token]
	m.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return true
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(keyAuth))
	return true
}

func (m *Manager) Present(ctx context.Context, domain, token, keyAuth string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch ParseChallenge(m.config.LetsEncrypt.Challenge) {
	case "http-01":
		m.mu.Lock()
		m.challenges[token] = keyAuth
		m.mu.Unlock()
	case "tls-alpn-01":
		certificate, err := tlsalpn01.ChallengeCert(domain, keyAuth)
		if err != nil {
			return fmt.Errorf("create TLS-ALPN challenge certificate: %w", err)
		}
		m.mu.Lock()
		m.tlsChallenges[domain] = certificate
		m.mu.Unlock()
	}
	return nil
}

func (m *Manager) CleanUp(ctx context.Context, domain, token, keyAuth string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.challenges, token)
	delete(m.tlsChallenges, domain)
	m.mu.Unlock()
	return nil
}

func (m *Manager) minTLSVersion() uint16 {
	if strings.EqualFold(strings.TrimSpace(m.config.MinVersion), "TLS1.3") {
		return tls.VersionTLS13
	}
	return tls.VersionTLS12
}

// findExistingCerts looks for existing certificates (e.g., from certbot)
func (m *Manager) findExistingCerts(domains []string) (certPath, keyPath string) {
	return m.findConfiguredCerts(domains)
}

func (m *Manager) findConfiguredCerts(domains []string) (certPath, keyPath string) {
	if m.config.Cert != "" || m.config.Key != "" {
		if m.config.Cert != "" && m.config.Key != "" && fileExists(m.config.Cert) && fileExists(m.config.Key) {
			return m.config.Cert, m.config.Key
		}
		return "", ""
	}
	for _, path := range []string{filepath.Join("/etc/letsencrypt/live", "domain"), filepath.Join("/etc/letsencrypt/live", firstDomain(domains))} {
		if cert, key, ok := certificatePair(path, "fullchain.pem", "privkey.pem"); ok {
			return cert, key
		}
	}
	for _, domain := range domains {
		if cert, key, ok := certificatePair(filepath.Join(m.config.CertPath, "ssl", "letsencrypt", domain), "fullchain.pem", "privkey.pem"); ok {
			return cert, key
		}
	}
	for _, domain := range domains {
		if cert, key, ok := certificatePair(filepath.Join(m.config.CertPath, "ssl", "local", domain), "cert.pem", "key.pem"); ok {
			return cert, key
		}
	}
	return "", ""
}

func firstDomain(domains []string) string {
	if len(domains) == 0 {
		return ""
	}
	return domains[0]
}

func certificatePair(directory, certName, keyName string) (string, string, bool) {
	cert, key := filepath.Join(directory, certName), filepath.Join(directory, keyName)
	return cert, key, fileExists(cert) && fileExists(key)
}

// findManualCerts looks for manually placed certificates
func (m *Manager) findManualCerts(domains []string) (certPath, keyPath string) {
	return m.findConfiguredCerts(domains)
}

// ChallengeServer handles ACME HTTP-01 challenges
type ChallengeServer struct {
	tokens map[string]string
	mu     sync.RWMutex
}

// NewChallengeServer creates a challenge server
func NewChallengeServer() *ChallengeServer {
	return &ChallengeServer{
		tokens: make(map[string]string),
	}
}

// SetToken sets a challenge token
func (cs *ChallengeServer) SetToken(token, auth string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.tokens[token] = auth
}

// ClearToken removes a challenge token
func (cs *ChallengeServer) ClearToken(token string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	delete(cs.tokens, token)
}

// ServeHTTP handles ACME challenge requests
func (cs *ChallengeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") {
		return false
	}

	token := strings.TrimPrefix(r.URL.Path, "/.well-known/acme-challenge/")
	cs.mu.RLock()
	auth, ok := cs.tokens[token]
	cs.mu.RUnlock()

	if !ok {
		http.NotFound(w, r)
		return true
	}

	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(auth))
	return true
}

// ParseChallenge parses challenge type from string
func ParseChallenge(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "http-01", "http01", "http":
		return "http-01"
	case "tls-alpn-01", "tlsalpn01", "tls-alpn", "tls":
		return "tls-alpn-01"
	case "dns-01", "dns01", "dns":
		return "dns-01"
	default:
		return "http-01"
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
