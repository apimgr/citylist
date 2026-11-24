package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/citylist/src/config"
)

func TestAccessLogSuppressesSuccessfulHealthAndKeepsFailures(t *testing.T) {
	dir := t.TempDir()
	if err := ConfigureAccessLog(dir, config.LogsConfig{}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		accessOutput.Lock()
		if accessOutput.file != nil {
			_ = accessOutput.file.Close()
		}
		accessOutput.file = nil
		accessOutput.Unlock()
	}()
	s := &Server{cfg: &config.Config{Server: config.ServerConfig{APIVersion: "v1"}}}
	for _, tc := range []struct {
		status int
		want   bool
	}{{http.StatusOK, false}, {http.StatusServiceUnavailable, true}} {
		handler := s.accessLogMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status) }))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/server/healthz", nil))
		info, err := os.Stat(filepath.Join(dir, "access.log"))
		if err != nil {
			t.Fatal(err)
		}
		if (info.Size() > 0) != tc.want {
			t.Fatalf("health status %d produced log size %d", tc.status, info.Size())
		}
		if !tc.want {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "access.log"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "503") {
			t.Fatalf("failure status missing from access log: %s", data)
		}
	}
}

func TestAccessLogEscapesControlCharacters(t *testing.T) {
	dir := t.TempDir()
	if err := ConfigureAccessLog(dir, config.LogsConfig{}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		accessOutput.Lock()
		if accessOutput.file != nil {
			_ = accessOutput.file.Close()
		}
		accessOutput.file = nil
		accessOutput.Unlock()
	}()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("User-Agent", "agent\nforged")
	writeAccessLog(r, http.StatusOK, 1, 0)
	data, err := os.ReadFile(filepath.Join(dir, "access.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "\n") != 1 {
		t.Fatalf("expected one raw log line, got %q", data)
	}
}

func TestAccessLogRedactsSensitiveQueryValues(t *testing.T) {
	dir := t.TempDir()
	settings := config.LogsConfig{Defaults: config.LogDefaults{Custom: "{query}"}, Access: config.LogKindConfig{Format: "custom"}}
	if err := ConfigureAccessLog(dir, settings); err != nil {
		t.Fatal(err)
	}
	defer func() {
		accessOutput.Lock()
		if accessOutput.file != nil {
			_ = accessOutput.file.Close()
		}
		accessOutput.file = nil
		accessOutput.Unlock()
	}()
	r := httptest.NewRequest(http.MethodGet, "/?token=private-value&city=Boston", nil)
	writeAccessLog(r, http.StatusOK, 1, 0)
	data, err := os.ReadFile(filepath.Join(dir, "access.log"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "private-value") || !strings.Contains(got, "token=%5Bredacted%5D") || !strings.Contains(got, "city=Boston") {
		t.Fatalf("access log query redaction failed: %s", got)
	}
}
