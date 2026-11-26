package ssl

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v5/acme"
	"github.com/go-acme/lego/v5/certcrypto"
	"github.com/go-acme/lego/v5/certificate"
	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/challenge/tlsalpn01"
	"github.com/go-acme/lego/v5/lego"
	"github.com/go-acme/lego/v5/providers/dns"
	"github.com/go-acme/lego/v5/registration"
)

var dnsEnvironmentMu sync.Mutex

type legoAccount struct {
	email        string
	registration *acme.ExtendedAccount
	key          crypto.Signer
}

func (u *legoAccount) GetEmail() string                       { return u.email }
func (u *legoAccount) GetRegistration() *acme.ExtendedAccount { return u.registration }
func (u *legoAccount) GetPrivateKey() crypto.Signer           { return u.key }

type storedLegoAccount struct {
	Email        string                `json:"email"`
	PrivateKey   string                `json:"private_key"`
	Registration *acme.ExtendedAccount `json:"registration,omitempty"`
}

func newDNSProvider(name string, credentials map[string]string) (challenge.Provider, error) {
	dnsEnvironmentMu.Lock()
	defer dnsEnvironmentMu.Unlock()
	previous := make(map[string]*string, len(credentials))
	defer func() {
		for key, value := range previous {
			if value == nil {
				_ = os.Unsetenv(key)
			} else {
				_ = os.Setenv(key, *value)
			}
		}
	}()
	for key, value := range credentials {
		if old, ok := os.LookupEnv(key); ok {
			oldCopy := old
			previous[key] = &oldCopy
		} else {
			previous[key] = nil
		}
		if err := os.Setenv(key, value); err != nil {
			return nil, fmt.Errorf("set DNS provider credential variable %s: %w", key, err)
		}
	}
	return dns.NewDNSChallengeProviderByName(strings.ToLower(strings.TrimSpace(name)))
}

func (m *Manager) validateDNSProvider() error {
	if !strings.EqualFold(ParseChallenge(m.config.LetsEncrypt.Challenge), "dns-01") {
		return nil
	}
	if strings.TrimSpace(m.config.LetsEncrypt.DNSProviderType) == "" {
		return errors.New("DNS-01 requires server.ssl.letsencrypt.dns_provider")
	}
	if _, err := newDNSProvider(m.config.LetsEncrypt.DNSProviderType, m.config.LetsEncrypt.DNSCredentials); err != nil {
		return redactCredentialError(err, m.config.LetsEncrypt.DNSCredentials)
	}
	return nil
}

func redactCredentialError(err error, credentials map[string]string) error {
	message := err.Error()
	for _, value := range credentials {
		if value != "" {
			message = strings.ReplaceAll(message, value, "[redacted]")
		}
	}
	return errors.New(message)
}

func (m *Manager) obtainCertificate(ctx context.Context, domains []string) error {
	challengeType := ParseChallenge(m.config.LetsEncrypt.Challenge)
	account, accountPath, err := m.loadOrCreateLegoAccount()
	if err != nil {
		return err
	}
	legoConfig := lego.NewConfig(account)
	if m.config.LetsEncrypt.Staging || m.config.Staging {
		legoConfig.CADirURL = lego.DirectoryURLLetsEncryptStaging
	}
	client, err := lego.NewClient(legoConfig)
	if err != nil {
		return fmt.Errorf("create ACME client: %w", err)
	}
	switch challengeType {
	case "http-01":
		err = client.Challenge.SetHTTP01Provider(m)
	case "tls-alpn-01":
		err = client.Challenge.SetTLSALPN01Provider(m)
	case "dns-01":
		var provider challenge.Provider
		provider, err = newDNSProvider(m.config.LetsEncrypt.DNSProviderType, m.config.LetsEncrypt.DNSCredentials)
		if err == nil {
			err = client.Challenge.SetDNS01Provider(provider)
		}
	default:
		err = fmt.Errorf("unsupported ACME challenge %q", challengeType)
	}
	if err != nil {
		return fmt.Errorf("configure %s challenge provider: %w", challengeType, redactCredentialError(err, m.config.LetsEncrypt.DNSCredentials))
	}
	if account.registration == nil {
		registration, err := client.Registration.Register(ctx, registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return fmt.Errorf("register ACME account: %w", redactCredentialError(err, m.config.LetsEncrypt.DNSCredentials))
		}
		account.registration = registration
		if err := saveLegoAccount(accountPath, account); err != nil {
			return err
		}
	}
	certificates, err := client.Certificate.Obtain(ctx, certificate.ObtainRequest{
		Domains: domains,
		KeyType: certcrypto.EC256,
		Bundle:  true,
	})
	if err != nil {
		return fmt.Errorf("obtain certificate: %w", redactCredentialError(err, m.config.LetsEncrypt.DNSCredentials))
	}
	domain := strings.TrimSpace(domains[0])
	certificateDir := filepath.Join(m.config.CertPath, "ssl", "letsencrypt", domain)
	if err := os.MkdirAll(certificateDir, 0700); err != nil {
		return fmt.Errorf("create certificate directory: %w", err)
	}
	if err := writePrivateAtomic(filepath.Join(certificateDir, "privkey.pem"), certificates.PrivateKey); err != nil {
		return fmt.Errorf("write certificate private key: %w", err)
	}
	if err := writePrivateAtomic(filepath.Join(certificateDir, "fullchain.pem"), certificates.Certificate); err != nil {
		return fmt.Errorf("write certificate chain: %w", err)
	}
	return nil
}

func (m *Manager) loadOrCreateLegoAccount() (*legoAccount, string, error) {
	accountName := "account.json"
	if m.config.LetsEncrypt.Staging || m.config.Staging {
		accountName = "account-staging.json"
	}
	accountPath := filepath.Join(m.config.CertPath, "ssl", "letsencrypt", accountName)
	if content, err := os.ReadFile(accountPath); err == nil {
		var stored storedLegoAccount
		if err := json.Unmarshal(content, &stored); err != nil {
			return nil, "", fmt.Errorf("decode ACME account: %w", err)
		}
		block, _ := pem.Decode([]byte(stored.PrivateKey))
		if block == nil {
			return nil, "", errors.New("ACME account private key is invalid")
		}
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, "", fmt.Errorf("parse ACME account private key: %w", err)
		}
		signer, ok := key.(crypto.Signer)
		if !ok {
			return nil, "", errors.New("ACME account key does not implement crypto.Signer")
		}
		return &legoAccount{email: stored.Email, registration: stored.Registration, key: signer}, accountPath, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("read ACME account: %w", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("generate ACME account key: %w", err)
	}
	account := &legoAccount{email: m.config.LetsEncrypt.Email, key: key}
	if err := saveLegoAccount(accountPath, account); err != nil {
		return nil, "", err
	}
	return account, accountPath, nil
}

func saveLegoAccount(path string, account *legoAccount) error {
	privateKey, err := x509.MarshalPKCS8PrivateKey(account.key)
	if err != nil {
		return fmt.Errorf("marshal ACME account key: %w", err)
	}
	content, err := json.Marshal(storedLegoAccount{
		Email: account.email, PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey})),
		Registration: account.registration,
	})
	if err != nil {
		return fmt.Errorf("encode ACME account: %w", err)
	}
	content = append(content, '\n')
	if err := writePrivateAtomic(path, content); err != nil {
		return fmt.Errorf("save ACME account: %w", err)
	}
	return nil
}

func writePrivateAtomic(path string, content []byte) (retErr error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".citylist-acme-*")
	if err != nil {
		return err
	}
	temporaryPath := file.Name()
	defer func() {
		if cleanupErr := os.Remove(temporaryPath); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			retErr = errors.Join(retErr, fmt.Errorf("remove temporary ACME account file: %w", cleanupErr))
		}
	}()
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func (m *Manager) renewCertificateIfDue(ctx context.Context, domains []string) error {
	if len(domains) == 0 || !m.config.LetsEncrypt.Enabled {
		return nil
	}
	if m.config.Cert != "" || m.config.Key != "" {
		return nil
	}
	for _, directory := range []string{
		filepath.Join("/etc/letsencrypt/live", "domain"),
		filepath.Join("/etc/letsencrypt/live", domains[0]),
	} {
		if _, _, ok := certificatePair(directory, "fullchain.pem", "privkey.pem"); ok {
			return nil
		}
	}
	path := filepath.Join(m.config.CertPath, "ssl", "letsencrypt", domains[0], "fullchain.pem")
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		localDir := filepath.Join(m.config.CertPath, "ssl", "local", domains[0])
		if _, _, ok := certificatePair(localDir, "cert.pem", "key.pem"); ok {
			return nil
		}
		return m.obtainCertificate(ctx, domains)
	}
	if err != nil {
		return fmt.Errorf("read managed certificate: %w", err)
	}
	block, _ := pem.Decode(content)
	if block == nil || block.Type != "CERTIFICATE" {
		return errors.New("managed certificate contains no PEM certificate")
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse managed certificate: %w", err)
	}
	if time.Until(parsed.NotAfter) > 7*24*time.Hour {
		return nil
	}
	return m.obtainCertificate(ctx, domains)
}

// RenewManagedCertificate provisions or renews the app-managed certificate when needed.
func (m *Manager) RenewManagedCertificate(ctx context.Context, domains []string) error {
	m.issueMu.Lock()
	defer m.issueMu.Unlock()
	return m.renewCertificateIfDue(ctx, domains)
}

func (m *Manager) getACMECertificate(ctx context.Context, host string, domains, protocols []string) (*tls.Certificate, error) {
	if !hostMatchesDomains(host, domains) {
		return nil, fmt.Errorf("TLS host %q is not an allowed certificate domain", host)
	}
	if containsProtocol(protocols, tlsalpn01.ACMETLS1Protocol) {
		m.mu.RLock()
		certificate := m.tlsChallenges[host]
		m.mu.RUnlock()
		if certificate != nil {
			return certificate, nil
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.issueMu.Lock()
	defer m.issueMu.Unlock()
	if certPath, keyPath := m.findConfiguredCerts(domains); certPath != "" && keyPath != "" {
		appManagedPath := filepath.Join(m.config.CertPath, "ssl", "letsencrypt", domains[0], "fullchain.pem")
		if filepath.Clean(certPath) != filepath.Clean(appManagedPath) {
			staticConfig, err := m.loadTLSCertificate(certPath, keyPath, domains)
			if err != nil {
				return nil, err
			}
			return &staticConfig.Certificates[0], nil
		}
	}
	certificatePath := filepath.Join(m.config.CertPath, "ssl", "letsencrypt", domains[0], "fullchain.pem")
	keyPath := filepath.Join(m.config.CertPath, "ssl", "letsencrypt", domains[0], "privkey.pem")
	if certificate, err := tls.LoadX509KeyPair(certificatePath, keyPath); err == nil && len(certificate.Certificate) > 0 {
		leaf, parseErr := x509.ParseCertificate(certificate.Certificate[0])
		if parseErr == nil && time.Until(leaf.NotAfter) > 7*24*time.Hour && leaf.VerifyHostname(host) == nil {
			certificate.Leaf = leaf
			return &certificate, nil
		}
	}
	requestContext, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := m.obtainCertificate(requestContext, domains); err != nil {
		return nil, err
	}
	certificate, err := tls.LoadX509KeyPair(certificatePath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load issued certificate: %w", err)
	}
	if len(certificate.Certificate) == 0 {
		return nil, errors.New("issued certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse issued certificate: %w", err)
	}
	if err := leaf.VerifyHostname(host); err != nil {
		return nil, fmt.Errorf("issued certificate does not match requested host: %w", err)
	}
	certificate.Leaf = leaf
	return &certificate, nil
}

func containsProtocol(protocols []string, wanted string) bool {
	for _, protocol := range protocols {
		if protocol == wanted {
			return true
		}
	}
	return false
}

func hostMatchesDomains(host string, domains []string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	for _, domain := range domains {
		domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
		if host == domain || strings.HasPrefix(domain, "*.") && strings.HasSuffix(host, strings.TrimPrefix(domain, "*")) {
			return true
		}
	}
	return false
}

var _ registration.User = (*legoAccount)(nil)
