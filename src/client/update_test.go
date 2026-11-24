package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCLIServerURL(t *testing.T) {
	tests := []struct {
		name, base, route, want string
		wantErr                 bool
	}{
		{name: "root", base: "https://example.net", route: "/api/autodiscover", want: "https://example.net/api/autodiscover"},
		{name: "path and query", base: "https://example.net/base/?old=yes#part", route: "/cli/file", want: "https://example.net/base/cli/file"},
		{name: "missing host", base: "https:///no-host", route: "/api", wantErr: true},
		{name: "userinfo", base: "https://user@example.net", route: "/api", wantErr: true},
		{name: "unsupported scheme", base: "file:///tmp/server", route: "/api", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := cliServerURL(test.base, test.route)
			if (err != nil) != test.wantErr {
				t.Fatalf("cliServerURL() error = %v, wantErr %t", err, test.wantErr)
			}
			if err == nil && got != test.want {
				t.Errorf("cliServerURL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestValidateCLIDownloadURL(t *testing.T) {
	tests := []struct {
		name, source string
		wantErr      bool
	}{
		{name: "https", source: "https://downloads.example.net/citylist-cli"},
		{name: "localhost http", source: "http://localhost:8080/cli"},
		{name: "loopback http", source: "http://127.0.0.1:8080/cli"},
		{name: "IPv6 loopback http", source: "http://[::1]:8080/cli"},
		{name: "external http", source: "http://downloads.example.net/cli", wantErr: true},
		{name: "userinfo", source: "https://user@example.net/cli", wantErr: true},
		{name: "invalid", source: "://invalid", wantErr: true},
		{name: "unsupported", source: "ftp://example.net/cli", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateCLIDownloadURL(test.source)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateCLIDownloadURL() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}

func TestValidSHA256(t *testing.T) {
	if !validSHA256(strings.Repeat("a", 64)) {
		t.Fatal("valid SHA-256 was rejected")
	}
	for _, value := range []string{"", "abc", strings.Repeat("z", 64)} {
		if validSHA256(value) {
			t.Errorf("invalid SHA-256 %q was accepted", value)
		}
	}
}

func TestRestartCLIArgs(t *testing.T) {
	got := restartCLIArgs([]string{"--server", "https://example.net", "--update", "yes", "city", "1", "--update=check"})
	want := []string{"--server", "https://example.net", "city", "1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("restartCLIArgs() = %v, want %v", got, want)
	}
}

func TestCheckCLIUpdateResponses(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		supported  bool
		wantErr    bool
		wantSHA256 string
	}{
		{name: "published", status: http.StatusOK, body: `{"cli_versions":{"linux-amd64":{"version":"1.2.3","sha256":"abc"}}}`, supported: true, wantSHA256: "abc"},
		{name: "not found", status: http.StatusNotFound},
		{name: "method not allowed", status: http.StatusMethodNotAllowed},
		{name: "server error", status: http.StatusInternalServerError, wantErr: true},
		{name: "invalid JSON", status: http.StatusOK, body: "not-json", supported: true, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/autodiscover" || r.Method != http.MethodGet {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Errorf("authorization = %q", r.Header.Get("Authorization"))
				}
				w.WriteHeader(test.status)
				_, _ = fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			info, supported, err := checkCLIUpdate(context.Background(), server.URL, "test-token", "1.0.0", time.Second)
			if (err != nil) != test.wantErr || supported != test.supported {
				t.Fatalf("checkCLIUpdate() supported=%t err=%v", supported, err)
			}
			if test.wantSHA256 != "" && (info == nil || info.CLIVersions["linux-amd64"].SHA256 != test.wantSHA256) {
				t.Fatalf("checkCLIUpdate() info = %#v", info)
			}
		})
	}
}

func TestDownloadAndReplaceCLIRejectsHTTPAndChecksum(t *testing.T) {
	t.Run("non-success response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()
		if err := downloadAndReplaceCLI(server.URL, strings.Repeat("a", 64), "", time.Second); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
			t.Fatalf("downloadAndReplaceCLI() error = %v, want HTTP 503", err)
		}
	})
	t.Run("checksum mismatch", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		previousLogger := activeCLILogger
		activeCLILogger = nil
		t.Cleanup(func() { activeCLILogger = previousLogger })
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, "not-the-expected-binary")
		}))
		defer server.Close()
		if err := downloadAndReplaceCLI(server.URL, strings.Repeat("a", 64), "", time.Second); err == nil || !strings.Contains(err.Error(), "checksum verification failed") {
			t.Fatalf("downloadAndReplaceCLI() error = %v, want checksum failure", err)
		}
		if _, err := os.Stat(filepath.Join(home, ".local", "log", "apimgr", "citylist", "cli.log")); err != nil {
			t.Fatalf("checksum failure audit log was not written under temporary HOME: %v", err)
		}
	})
}

func TestMaybeUpdateCLIStatusAndPromptPaths(t *testing.T) {
	t.Run("invalid update mode", func(t *testing.T) {
		var output, errorOutput strings.Builder
		stop, code := maybeUpdateCLI([]string{"--update", "later"}, strings.NewReader(""), &output, &errorOutput, "", "", "1.0.0", time.Second, cliConfig{})
		if !stop || code != 2 || !strings.Contains(errorOutput.String(), "must be check or yes") {
			t.Fatalf("maybeUpdateCLI() = (%t, %d), error %q", stop, code, errorOutput.String())
		}
	})
	t.Run("update requires configured server", func(t *testing.T) {
		var output, errorOutput strings.Builder
		stop, code := maybeUpdateCLI([]string{"--update=check"}, strings.NewReader(""), &output, &errorOutput, "", "", "1.0.0", time.Second, cliConfig{})
		if !stop || code != 2 || !strings.Contains(errorOutput.String(), "configure a server") {
			t.Fatalf("maybeUpdateCLI() = (%t, %d), error %q", stop, code, errorOutput.String())
		}
	})
	t.Run("unsupported server", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		defer server.Close()
		var output, errorOutput strings.Builder
		stop, code := maybeUpdateCLI([]string{"--update", "check"}, strings.NewReader(""), &output, &errorOutput, server.URL, "", "1.0.0", time.Second, cliConfig{})
		if !stop || code != 0 || !strings.Contains(output.String(), "does not publish") {
			t.Fatalf("maybeUpdateCLI() = (%t, %d), output %q, error %q", stop, code, output.String(), errorOutput.String())
		}
	})
	t.Run("available update check", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprintf(w, `{"cli_versions":{"%s":{"version":"2.0.0","sha256":"%s"}}}`, runtime.GOOS+"-"+runtime.GOARCH, strings.Repeat("a", 64))
		}))
		defer server.Close()
		var output, errorOutput strings.Builder
		stop, code := maybeUpdateCLI([]string{"--update=check"}, strings.NewReader(""), &output, &errorOutput, server.URL, "", "1.0.0", time.Second, cliConfig{})
		if !stop || code != 0 || !strings.Contains(output.String(), "CLI update available: 1.0.0 → 2.0.0") {
			t.Fatalf("maybeUpdateCLI() = (%t, %d), output %q, error %q", stop, code, output.String(), errorOutput.String())
		}
	})
	t.Run("noninteractive update requires opt-in", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprintf(w, `{"cli_versions":{"%s":{"version":"2.0.0","sha256":"%s"}}}`, runtime.GOOS+"-"+runtime.GOARCH, strings.Repeat("a", 64))
		}))
		defer server.Close()
		var output, errorOutput strings.Builder
		stop, code := maybeUpdateCLI([]string{"--update=yes"}, strings.NewReader(""), &output, &errorOutput, server.URL, "", "1.0.0", time.Second, cliConfig{})
		if !stop || code != 1 || !strings.Contains(errorOutput.String(), "require update.auto: true") {
			t.Fatalf("maybeUpdateCLI() = (%t, %d), output %q, error %q", stop, code, output.String(), errorOutput.String())
		}
	})
}
