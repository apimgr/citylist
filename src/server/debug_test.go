package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/mode"
)

func TestDebugRoutesRequireExplicitFlagAndOperatorToken(t *testing.T) {
	mode.Set("production")
	mode.SetDebugEnabled(false)
	t.Cleanup(func() {
		mode.Set("production")
		mode.SetDebugEnabled(false)
	})
	if response := doRequest(t, testServer(t), http.MethodGet, "/debug/config", nil); response.Code != http.StatusNotFound {
		t.Fatalf("debug route without flag returned %d, want 404", response.Code)
	}

	mode.SetDebugEnabled(true)
	cfg := config.DefaultConfig()
	cfg.Server.Token = "operator-debug-test-token"
	s := testServerWithConfig(t, cfg)
	if response := doRequest(t, s, http.MethodGet, "/debug/config", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated debug route returned %d, want 401", response.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/debug/config", nil)
	request.Header.Set("Authorization", "Bearer "+cfg.Server.Token)
	response := httptest.NewRecorder()
	s.Router().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated debug config returned %d, want 200: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), cfg.Server.Token) || !strings.Contains(response.Body.String(), "********") {
		t.Fatalf("debug config did not redact the operator token: %s", response.Body.String())
	}
	if alias := doRequest(t, s, http.MethodGet, "/api/v1/debug/config", nil); alias.Code != http.StatusNotFound {
		t.Fatalf("versioned debug alias returned %d, want 404", alias.Code)
	}
}

func TestDebugCmdlineOnlyReturnsBinaryName(t *testing.T) {
	mode.SetDebugEnabled(true)
	t.Cleanup(func() { mode.SetDebugEnabled(false) })
	cfg := config.DefaultConfig()
	cfg.Server.Token = "operator-debug-test-token"
	s := testServerWithConfig(t, cfg)
	request := httptest.NewRequest(http.MethodGet, "/debug/pprof/cmdline", nil)
	request.Header.Set("Authorization", "Bearer "+cfg.Server.Token)
	response := httptest.NewRecorder()
	s.Router().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated cmdline returned %d: %s", response.Code, response.Body.String())
	}
	if got := strings.TrimSpace(response.Body.String()); got == "" || strings.Contains(got, " ") || strings.Contains(got, "operator-debug-test-token") {
		t.Fatalf("cmdline response contains arguments or is empty: %q", got)
	}
}
