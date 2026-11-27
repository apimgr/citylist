package main

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestParseTorVanityStart(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantPrefix  string
		wantWorkers int
		wantErr     bool
	}{
		{name: "flags", args: []string{"--prefix", "abc", "--workers", "3"}, wantPrefix: "abc", wantWorkers: 3},
		{name: "positional prefix", args: []string{"abc"}, wantPrefix: "abc"},
		{name: "missing prefix", args: []string{"--workers", "2"}, wantErr: true},
		{name: "invalid worker count", args: []string{"--prefix", "abc", "--workers", "many"}, wantErr: true},
		{name: "unknown option", args: []string{"--prefix", "abc", "--other"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prefix, workers, err := parseTorVanityStart(test.args)
			if (err != nil) != test.wantErr {
				t.Fatalf("parseTorVanityStart(%v) error = %v, wantErr %v", test.args, err, test.wantErr)
			}
			if err == nil && (prefix != test.wantPrefix || workers != test.wantWorkers) {
				t.Fatalf("parseTorVanityStart(%v) = (%q, %d), want (%q, %d)", test.args, prefix, workers, test.wantPrefix, test.wantWorkers)
			}
		})
	}
}

func TestRequestTorControl(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/server/tor/restart" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		_, _ = w.Write([]byte(`{"ok":true,"data":{"restarted":true}}`))
	}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	response, err := requestTorControl(strconv.Itoa(port), http.MethodPost, "/server/tor/restart", map[string]bool{"confirm": true})
	if err != nil {
		t.Fatal(err)
	}
	if !response.OK || string(response.Data) != `{"restarted":true}` {
		t.Fatalf("response = %#v", response)
	}
}

func TestRequestTorControlRejectsInvalidAndFailedResponses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantError  string
	}{
		{name: "invalid JSON", statusCode: http.StatusOK, body: `{`, wantError: "invalid Tor maintenance response"},
		{name: "HTTP error", statusCode: http.StatusServiceUnavailable, body: `{"ok":false,"error":"SERVER_ERROR","message":"unavailable"}`, wantError: "unavailable"},
		{name: "failed envelope", statusCode: http.StatusOK, body: `{"ok":false,"error":"SERVER_ERROR","message":"operation failed"}`, wantError: "operation failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.statusCode)
				if _, err := fmt.Fprint(w, test.body); err != nil {
					t.Errorf("write control response: %v", err)
				}
			}))
			defer server.Close()
			port := strconv.Itoa(server.Listener.Addr().(*net.TCPAddr).Port)
			if _, err := requestTorControl(port, http.MethodGet, "/server/tor/status", nil); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("requestTorControl() error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestReadHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"mode":"production","uptime":"5m"}`)
	}))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	health, err := readHealth(strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	if health.Mode != "production" || health.Uptime != "5m" {
		t.Fatalf("health = %+v", health)
	}
}

func TestReadHealthRejectsFailedAndIncompleteResponses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "unhealthy status", statusCode: http.StatusServiceUnavailable, body: `{}`},
		{name: "invalid JSON", statusCode: http.StatusOK, body: `{`},
		{name: "missing mode", statusCode: http.StatusOK, body: `{"uptime":"5m"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.statusCode)
				if _, err := fmt.Fprint(w, test.body); err != nil {
					t.Errorf("write health response: %v", err)
				}
			}))
			defer server.Close()
			port := strconv.Itoa(server.Listener.Addr().(*net.TCPAddr).Port)
			if _, err := readHealth(port); err == nil {
				t.Fatal("readHealth() error = nil")
			}
		})
	}
}

func TestMaintenanceTorStatusFallsBackWhenServerIsStopped(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("the stopped-server maintenance fallback requires operator authorization")
	}
	if err := maintenanceTor([]string{"status"}, "", t.TempDir(), "0", nil); err != nil {
		t.Fatal(err)
	}
}

func TestMaintenanceTorValidationFallsBackWhenServerIsStopped(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("Tor maintenance validation requires operator authorization")
	}
	configDir, dataDir := t.TempDir(), t.TempDir()
	torConfigDir := filepath.Join(configDir, "tor")
	if err := os.MkdirAll(torConfigDir, 0700); err != nil {
		t.Fatal(err)
	}
	torConfigPath := filepath.Join(torConfigDir, "torrc")
	if err := os.WriteFile(torConfigPath, []byte("DataDirectory /private/tor\nHiddenServiceDir /private/site\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := maintenanceTor([]string{"validate"}, configDir, dataDir, "0", nil); err != nil {
		t.Fatal(err)
	}
}

func TestMaintenanceTorActionsUseTheControlServer(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("Tor maintenance actions require operator authorization")
	}
	expectedMethods := map[string]string{
		"/server/tor/status":       http.MethodGet,
		"/server/tor/validate":     http.MethodPost,
		"/server/tor/restart":      http.MethodPost,
		"/server/tor/regenerate":   http.MethodPost,
		"/server/tor/vanity/start": http.MethodPost,
		"/server/tor/vanity/stop":  http.MethodPost,
		"/server/tor/vanity/apply": http.MethodPost,
		"/server/tor/import-keys":  http.MethodPost,
	}
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if method, ok := expectedMethods[r.URL.Path]; !ok || method != r.Method {
			t.Errorf("unexpected control request %s %s", r.Method, r.URL.Path)
		}
		if _, err := fmt.Fprint(w, `{"ok":true,"data":{"complete":true}}`); err != nil {
			t.Errorf("write control response: %v", err)
		}
	}))
	defer controlServer.Close()
	port := strconv.Itoa(controlServer.Listener.Addr().(*net.TCPAddr).Port)

	originalStdout := os.Stdout
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = devNull
	t.Cleanup(func() {
		os.Stdout = originalStdout
		_ = devNull.Close()
	})

	tests := []struct {
		name string
		args []string
	}{
		{name: "status", args: []string{"status"}},
		{name: "validate", args: []string{"validate"}},
		{name: "restart", args: []string{"restart"}},
		{name: "regenerate", args: []string{"regenerate", "--yes"}},
		{name: "vanity start", args: []string{"vanity", "start", "--prefix", "abc", "--workers", "2"}},
		{name: "vanity stop", args: []string{"vanity", "stop"}},
		{name: "vanity apply", args: []string{"vanity", "apply", "candidate.onion", "--yes"}},
		{name: "import keys", args: []string{"import-keys", t.TempDir(), "--yes"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := maintenanceTor(test.args, "", t.TempDir(), port, nil); err != nil {
				t.Fatalf("maintenanceTor(%v): %v", test.args, err)
			}
		})
	}
}

func TestTorMaintenanceHelpers(t *testing.T) {
	if os.Geteuid() == 0 {
		if err := authorizeTorMaintenance(nil); err != nil {
			t.Fatalf("root authorization: %v", err)
		}
	}
	if err := confirmTorAction("RESTART TOR", []string{"--yes"}); err != nil {
		t.Fatalf("--yes confirmation: %v", err)
	}
	if got, rest := firstNonFlag([]string{"--yes", "candidate", "--force"}); got != "candidate" || len(rest) != 2 || rest[0] != "--yes" || rest[1] != "--force" {
		t.Fatalf("firstNonFlag = %q, %v", got, rest)
	}
}

func TestTorMaintenanceRejectsInvalidCommands(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("Tor maintenance commands require operator authorization")
	}
	tests := [][]string{
		nil,
		{"vanity"},
		{"unknown"},
		{"import-keys", "relative/path", "--yes"},
		{"vanity", "start", "--workers", "invalid"},
		{"regenerate"},
	}
	for _, args := range tests {
		if err := maintenanceTor(args, "", t.TempDir(), "0", nil); err == nil {
			t.Errorf("maintenanceTor(%v) error = nil", args)
		}
	}
}

func TestServerListenerReachable(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	if !serverListenerReachable(port) {
		t.Fatal("serverListenerReachable() = false for a listening loopback port")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if serverListenerReachable("0") {
		t.Fatal("serverListenerReachable() = true for port zero")
	}
}
