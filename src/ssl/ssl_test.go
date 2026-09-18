package ssl

import (
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
	domain := "example.com"

	certPath := filepath.Join(tmpDir, domain+".crt")
	keyPath := filepath.Join(tmpDir, domain+".key")
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

func TestFindManualCertsFullchainFormat(t *testing.T) {
	tmpDir := t.TempDir()
	domain := "example.com"

	domainDir := filepath.Join(tmpDir, domain)
	if err := os.MkdirAll(domainDir, 0755); err != nil {
		t.Fatalf("failed to create domain dir: %v", err)
	}
	certPath := filepath.Join(domainDir, "fullchain.pem")
	keyPath := filepath.Join(domainDir, "privkey.pem")
	writeSelfSignedCert(t, certPath, keyPath)

	m := NewManager(Config{Enabled: true, CertPath: tmpDir})
	cert, key := m.findManualCerts([]string{domain})
	if cert != certPath || key != keyPath {
		t.Errorf("findManualCerts() = (%q, %q), want (%q, %q)", cert, key, certPath, keyPath)
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
	defer certOut.Close()
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
	defer keyOut.Close()
	if err := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}); err != nil {
		t.Fatalf("failed to write key pem: %v", err)
	}
}
