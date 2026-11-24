package ssl

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewManager(t *testing.T) {
	m := NewManager(Config{Enabled: true})
	if m == nil {
		t.Fatal("NewManager() returned nil")
	}
}

func TestGetTLSConfigDisabled(t *testing.T) {
	m := NewManager(Config{Enabled: false})
	cfg, err := m.GetTLSConfig([]string{"example.com"})
	if err != nil {
		t.Fatalf("GetTLSConfig() error = %v, want nil", err)
	}
	if cfg != nil {
		t.Error("expected nil TLS config when SSL disabled")
	}
}

func TestGetTLSConfigNoCertsNoLetsEncrypt(t *testing.T) {
	tmpDir := t.TempDir()
	m := NewManager(Config{Enabled: true, CertPath: tmpDir})
	_, err := m.GetTLSConfig([]string{"example.com"})
	if err == nil {
		t.Error("expected error when no certs available and Let's Encrypt disabled")
	}
}

func TestGetTLSConfigManualCerts(t *testing.T) {
	tmpDir := t.TempDir()
	domain := "localhost"

	domainDir := filepath.Join(tmpDir, "ssl", "local", domain)
	if err := os.MkdirAll(domainDir, 0700); err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(domainDir, "cert.pem")
	keyPath := filepath.Join(domainDir, "key.pem")
	writeSelfSignedCert(t, certPath, keyPath)

	m := NewManager(Config{Enabled: true, CertPath: tmpDir})
	cfg, err := m.GetTLSConfig([]string{domain})
	if err != nil {
		t.Fatalf("GetTLSConfig() error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil TLS config")
	}
	if len(cfg.Certificates) != 1 {
		t.Errorf("Certificates len = %d, want 1", len(cfg.Certificates))
	}
}

func TestFindManualCertsLocalCertificateFormat(t *testing.T) {
	tmpDir := t.TempDir()
	domain := "example.com"

	domainDir := filepath.Join(tmpDir, "ssl", "local", domain)
	if err := os.MkdirAll(domainDir, 0755); err != nil {
		t.Fatalf("failed to create domain dir: %v", err)
	}
	certPath := filepath.Join(domainDir, "cert.pem")
	keyPath := filepath.Join(domainDir, "key.pem")
	writeSelfSignedCert(t, certPath, keyPath)

	m := NewManager(Config{Enabled: true, CertPath: tmpDir})
	cert, key := m.findManualCerts([]string{domain})
	if cert != certPath || key != keyPath {
		t.Errorf("findManualCerts() = (%q, %q), want (%q, %q)", cert, key, certPath, keyPath)
	}
}

func TestFindConfiguredCertsUsesSpecifiedLookupOrder(t *testing.T) {
	tmpDir := t.TempDir()
	domain := "example.test"
	appPath := filepath.Join(tmpDir, "ssl", "letsencrypt", domain)
	if err := os.MkdirAll(appPath, 0700); err != nil {
		t.Fatal(err)
	}
	cert := filepath.Join(appPath, "fullchain.pem")
	key := filepath.Join(appPath, "privkey.pem")
	writeSelfSignedCert(t, cert, key)

	m := NewManager(Config{Enabled: true, CertPath: tmpDir})
	gotCert, gotKey := m.findConfiguredCerts([]string{domain})
	if gotCert != cert || gotKey != key {
		t.Fatalf("findConfiguredCerts() = (%q, %q), want (%q, %q)", gotCert, gotKey, cert, key)
	}
}

func TestTLSConfigRejectsWrongHostname(t *testing.T) {
	tmpDir := t.TempDir()
	certPath := filepath.Join(tmpDir, "cert.pem")
	keyPath := filepath.Join(tmpDir, "key.pem")
	writeSelfSignedCert(t, certPath, keyPath)
	m := NewManager(Config{Enabled: true, Cert: certPath, Key: keyPath})
	if _, err := m.GetTLSConfig([]string{"example.test"}); err == nil {
		t.Fatal("GetTLSConfig() succeeded with a certificate for the wrong hostname")
	}
}

func TestDNSProviderCredentialsAreRestoredAfterConstruction(t *testing.T) {
	t.Setenv("CF_DNS_API_TOKEN", "existing-environment-value")
	provider, err := newDNSProvider("cloudflare", map[string]string{"CF_DNS_API_TOKEN": "temporary-provider-value"})
	if err != nil {
		t.Fatalf("newDNSProvider() error: %v", err)
	}
	if provider == nil {
		t.Fatal("newDNSProvider() returned nil provider")
	}
	if got := os.Getenv("CF_DNS_API_TOKEN"); got != "existing-environment-value" {
		t.Fatalf("provider credential environment value = %q, want restored value", got)
	}
}

func TestHostMatchesConfiguredWildcard(t *testing.T) {
	if !hostMatchesDomains("api.example.test", []string{"example.test", "*.example.test"}) {
		t.Fatal("wildcard domain did not match a subdomain")
	}
	if hostMatchesDomains("example.test", []string{"*.example.test"}) {
		t.Fatal("wildcard domain matched its apex")
	}
}

func TestHTTP01ChallengeHandlerServesOnlyActiveToken(t *testing.T) {
	manager := NewManager(Config{LetsEncrypt: LetsEncryptConfig{Challenge: "http-01"}})
	if err := manager.Present(context.Background(), "example.test", "challenge-token", "challenge-token.thumbprint"); err != nil {
		t.Fatal(err)
	}
	handler := manager.GetHTTPHandler(http.NotFoundHandler())
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/challenge-token", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "challenge-token.thumbprint" || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("HTTP-01 response = (%d, %q, %q)", recorder.Code, recorder.Body.String(), recorder.Header().Get("Cache-Control"))
	}
	if err := manager.CleanUp(context.Background(), "example.test", "challenge-token", "challenge-token.thumbprint"); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("cleaned HTTP-01 token status = %d, want 404", recorder.Code)
	}
}

func TestLegoAccountKeyHasPrivatePermissionsAndCanBeLoaded(t *testing.T) {
	manager := NewManager(Config{CertPath: t.TempDir(), LetsEncrypt: LetsEncryptConfig{Email: "admin@example.test"}})
	account, path, err := manager.loadOrCreateLegoAccount()
	if err != nil {
		t.Fatal(err)
	}
	if account.key == nil {
		t.Fatal("generated account has no private key")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("ACME account file mode = %04o, want 0600", info.Mode().Perm())
	}
	loaded, loadedPath, err := manager.loadOrCreateLegoAccount()
	if err != nil {
		t.Fatal(err)
	}
	if loadedPath != path || loaded.key == nil || loaded.email != "admin@example.test" {
		t.Fatalf("reloaded account = %+v, path %q", loaded, loadedPath)
	}
}

func TestFindManualCertsNoCertPath(t *testing.T) {
	m := NewManager(Config{Enabled: true})
	cert, key := m.findManualCerts([]string{"example.com"})
	if cert != "" || key != "" {
		t.Errorf("expected empty results with no CertPath, got (%q, %q)", cert, key)
	}
}

func TestFindExistingCertsNotFound(t *testing.T) {
	m := NewManager(Config{Enabled: true})
	cert, key := m.findExistingCerts([]string{"nonexistent-domain-for-test.invalid"})
	if cert != "" || key != "" {
		t.Errorf("expected empty results, got (%q, %q)", cert, key)
	}
}

func TestGetHTTPHandlerFallback(t *testing.T) {
	m := NewManager(Config{Enabled: false})
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	handler := m.GetHTTPHandler(fallback)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusTeapot)
	}
}

func TestChallengeServer(t *testing.T) {
	cs := NewChallengeServer()
	cs.SetToken("tok1", "auth1")

	t.Run("matching token", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/tok1", nil)
		handled := cs.ServeHTTP(rec, req)
		if !handled {
			t.Fatal("expected ServeHTTP to handle challenge path")
		}
		if rec.Body.String() != "auth1" {
			t.Errorf("body = %q, want auth1", rec.Body.String())
		}
	})

	t.Run("unknown token", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/unknown", nil)
		handled := cs.ServeHTTP(rec, req)
		if !handled {
			t.Fatal("expected ServeHTTP to handle challenge path even when unknown")
		}
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("non-challenge path", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		handled := cs.ServeHTTP(rec, req)
		if handled {
			t.Error("expected ServeHTTP to not handle a non-challenge path")
		}
	})

	t.Run("clear token", func(t *testing.T) {
		cs.ClearToken("tok1")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/tok1", nil)
		cs.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("status after clear = %d, want 404", rec.Code)
		}
	})
}

func TestParseChallenge(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"http-01", "http-01"},
		{"http01", "http-01"},
		{"http", "http-01"},
		{"HTTP-01", "http-01"},
		{" http-01 ", "http-01"},
		{"tls-alpn-01", "tls-alpn-01"},
		{"tlsalpn01", "tls-alpn-01"},
		{"tls-alpn", "tls-alpn-01"},
		{"tls", "tls-alpn-01"},
		{"dns-01", "dns-01"},
		{"dns01", "dns-01"},
		{"dns", "dns-01"},
		{"unknown", "http-01"},
		{"", "http-01"},
	}
	for _, tt := range tests {
		if got := ParseChallenge(tt.in); got != tt.want {
			t.Errorf("ParseChallenge(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFileExists(t *testing.T) {
	tmpDir := t.TempDir()
	existing := filepath.Join(tmpDir, "exists.txt")
	if err := os.WriteFile(existing, []byte("data"), 0644); err != nil {
		t.Fatalf("failed to write test fixture: %v", err)
	}

	if !fileExists(existing) {
		t.Error("fileExists() = false for existing file, want true")
	}
	if fileExists(filepath.Join(tmpDir, "missing.txt")) {
		t.Error("fileExists() = true for missing file, want false")
	}
}

// writeSelfSignedCert generates a fresh throwaway EC cert/key pair for tests
// so no static private key material is ever committed to the repository.
func writeSelfSignedCert(t *testing.T, certPath, keyPath string) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate test key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"Test Co"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("failed to create test certificate: %v", err)
	}

	certOut, err := os.Create(certPath)
	if err != nil {
		t.Fatalf("failed to open cert file: %v", err)
	}
	defer func() { _ = certOut.Close() }()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		t.Fatalf("failed to write cert pem: %v", err)
	}

	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("failed to marshal test key: %v", err)
	}

	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatalf("failed to open key file: %v", err)
	}
	defer func() { _ = keyOut.Close() }()
	if err := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}); err != nil {
		t.Fatalf("failed to write key pem: %v", err)
	}
}
