package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/server"
)

func TestMaintenanceHelpRequested(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "long flag", args: []string{"--maintenance", "--help"}, want: true},
		{name: "short flag", args: []string{"--maintenance", "-h"}, want: true},
		{name: "help word", args: []string{"--maintenance", "help"}, want: true},
		{name: "action", args: []string{"--maintenance", "backup"}},
		{name: "unrelated help", args: []string{"--help", "--maintenance", "backup"}},
		{name: "missing value", args: []string{"--maintenance"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := maintenanceHelpRequested(test.args); got != test.want {
				t.Fatalf("maintenanceHelpRequested(%v) = %t, want %t", test.args, got, test.want)
			}
		})
	}
}

func TestSubmitPGPKeyUsesProviderProtocolAndClassifiesRetries(t *testing.T) {
	for _, test := range []struct {
		name        string
		path        string
		status      int
		contentType string
		retryable   bool
	}{
		{name: "VKS JSON", path: "/vks/v1/upload", status: http.StatusOK, contentType: "application/json"},
		{name: "HKP form", path: "/pks/add", status: http.StatusOK, contentType: "application/x-www-form-urlencoded"},
		{name: "rate limited retry", path: "/pks/add", status: http.StatusTooManyRequests, contentType: "application/x-www-form-urlencoded", retryable: true},
		{name: "client error final", path: "/pks/add", status: http.StatusBadRequest, contentType: "application/x-www-form-urlencoded"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Content-Type"); got != test.contentType {
					t.Errorf("Content-Type = %q, want %q", got, test.contentType)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read request body: %v", err)
				}
				if test.path == "/vks/v1/upload" {
					var request map[string]string
					if err := json.Unmarshal(body, &request); err != nil || request["keytext"] != "armored-key" {
						t.Errorf("VKS request body = %s, error = %v", body, err)
					}
				} else if values, err := url.ParseQuery(string(body)); err != nil || values.Get("keytext") != "armored-key" {
					t.Errorf("HKP request body = %s, error = %v", body, err)
				}
				w.WriteHeader(test.status)
				if test.path == "/vks/v1/upload" && test.status == http.StatusOK {
					_, _ = io.WriteString(w, `{"key_fpr":"ABC123","status":"published"}`)
				}
			}))
			defer server.Close()
			err := submitPGPKeyWithClient(context.Background(), server.URL+test.path, []byte("armored-key"), server.Client())
			if test.status == http.StatusOK {
				if err != nil {
					t.Fatalf("submitPGPKeyWithClient() error = %v", err)
				}
				return
			}
			var publishErr pgpPublishError
			if err == nil || !errors.As(err, &publishErr) || publishErr.retryable != test.retryable {
				t.Fatalf("submitPGPKeyWithClient() error = %v, retryable = %t; want %t", err, publishErr.retryable, test.retryable)
			}
		})
	}
}

func TestPGPKeyserverRetryStateSurvivesCallsAndStopsFinalFailures(t *testing.T) {
	ctx := context.Background()
	store := openMaintenanceTokenTestStore(t)
	key := &server.ProjectPGPKey{Fingerprint: "ABCD1234", PublicArmor: []byte("armored-key"), CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(365 * 24 * time.Hour)}
	if err := savePGPMetadata(ctx, store, key, time.Time{}, time.Time{}, false); err != nil {
		t.Fatal(err)
	}
	root := mainProjectTempDir(t)
	configDir, logsDir := filepath.Join(root, "config"), filepath.Join(root, "logs")
	cfg := config.DefaultConfig()
	cfg.Web.Security.Keyservers = []string{"https://keys.example.net/submit"}
	calls := 0
	temporaryFailure := func(context.Context, string, []byte) error {
		calls++
		return pgpPublishError{err: errors.New("temporary network failure"), retryable: true}
	}
	if err := publishPGPKeyWithSubmitter(ctx, configDir, logsDir, cfg, store, key, temporaryFailure); err == nil {
		t.Fatal("temporary publication failure was not returned to the caller")
	}
	statePath := filepath.Join(configDir, "security", "keyservers.state")
	readState := func(path string) map[string]pgpPublishState {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		state := map[string]pgpPublishState{}
		if err := json.Unmarshal(data, &state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	state := readState(statePath)
	retry := state[cfg.Web.Security.Keyservers[0]]
	next, err := time.Parse(time.RFC3339Nano, retry.NextAttemptAt)
	if err != nil || retry.Attempts != 1 || retry.Fingerprint != key.Fingerprint || !next.After(time.Now()) {
		t.Fatalf("persisted retry state = %+v, parse error = %v", retry, err)
	}
	if err := publishPGPKeyWithSubmitter(ctx, configDir, logsDir, cfg, store, key, temporaryFailure); err != nil {
		t.Fatalf("not-yet-due retry returned an error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("not-yet-due retry made %d submit calls, want 1", calls)
	}
	retry.NextAttemptAt = time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	state[cfg.Web.Security.Keyservers[0]] = retry
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAtomicPrivateFile(statePath, data); err != nil {
		t.Fatal(err)
	}
	if err := publishPGPKeyWithSubmitter(ctx, configDir, logsDir, cfg, store, key, func(context.Context, string, []byte) error {
		calls++
		return nil
	}); err != nil {
		t.Fatalf("due retry failed: %v", err)
	}
	state = readState(statePath)
	if calls != 2 || state[cfg.Web.Security.Keyservers[0]].PublishedAt == "" {
		t.Fatalf("successful retry calls=%d state=%+v", calls, state[cfg.Web.Security.Keyservers[0]])
	}

	finalConfig := filepath.Join(root, "final-config")
	finalCalls := 0
	finalFailure := func(context.Context, string, []byte) error {
		finalCalls++
		return pgpPublishError{err: errors.New("HTTP 400"), retryable: false}
	}
	if err := publishPGPKeyWithSubmitter(ctx, finalConfig, logsDir, cfg, store, key, finalFailure); err == nil {
		t.Fatal("permanent publication failure was not reported")
	}
	if err := publishPGPKeyWithSubmitter(ctx, finalConfig, logsDir, cfg, store, key, finalFailure); err != nil {
		t.Fatalf("persisted permanent failure was retried: %v", err)
	}
	finalState := readState(filepath.Join(finalConfig, "security", "keyservers.state"))
	if finalCalls != 1 || !finalState[cfg.Web.Security.Keyservers[0]].FinalFailure {
		t.Fatalf("permanent failure calls=%d state=%+v", finalCalls, finalState[cfg.Web.Security.Keyservers[0]])
	}
}

func TestCommandHelp(t *testing.T) {
	if got, ok := commandHelp([]string{"--service", "--help"}, "service", "update"); !ok || got != "service" {
		t.Fatalf("commandHelp() = (%q, %t), want service help", got, ok)
	}
	if got, ok := commandHelp([]string{"--service", "backup"}, "service", "update"); ok || got != "" {
		t.Fatalf("commandHelp() for an action = (%q, %t), want no match", got, ok)
	}
}

func TestResolveDatabasePath(t *testing.T) {
	dataDir := filepath.Join(mainProjectTempDir(t), "data")
	t.Setenv("DATABASE_DIR", filepath.Join(dataDir, "db"))
	tests := []struct {
		name       string
		configured string
		want       string
		wantErr    bool
	}{
		{name: "default", want: filepath.Join(dataDir, "db", "server.db")},
		{name: "relative path", configured: "custom/server.db", want: filepath.Join(dataDir, "custom", "server.db")},
		{name: "data placeholder", configured: "{data_dir}/custom.db", want: filepath.Join(dataDir, "custom.db")},
		{name: "absolute path", configured: filepath.Join(dataDir, "absolute.db"), want: filepath.Join(dataDir, "absolute.db")},
		{name: "file URL", configured: "file:/var/lib/citylist/server.db", want: "/var/lib/citylist/server.db"},
		{name: "query rejected", configured: "file:server.db?mode=ro", wantErr: true},
		{name: "empty file URL rejected", configured: "file:", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveDatabasePath(test.configured, dataDir)
			if (err != nil) != test.wantErr {
				t.Fatalf("resolveDatabasePath() error = %v, wantErr %t", err, test.wantErr)
			}
			if err == nil && got != test.want {
				t.Fatalf("resolveDatabasePath() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestMaintenanceOperatorIP(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "192.0.2.10 54321 192.0.2.20 22")
	if got := maintenanceOperatorIP(); got != "192.0.2.10" {
		t.Fatalf("maintenanceOperatorIP() = %q, want SSH peer", got)
	}
	for _, invalid := range []string{"", "invalid 1 2 3", "192.0.2.10 1 2", "not-an-ip 1 2 3"} {
		t.Setenv("SSH_CONNECTION", invalid)
		if got := maintenanceOperatorIP(); got != "127.0.0.1" {
			t.Fatalf("maintenanceOperatorIP() with %q = %q, want loopback", invalid, got)
		}
	}
}

func TestPGPKeyserverEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		want       string
		wantErr    bool
	}{
		{name: "openpgp host", configured: "keys.openpgp.org", want: "https://keys.openpgp.org/vks/v1/upload"},
		{name: "ubuntu host", configured: "keyserver.ubuntu.com", want: "https://keyserver.ubuntu.com/pks/add"},
		{name: "explicit custom endpoint", configured: "https://keys.example.net/submit", want: "https://keys.example.net/submit"},
		{name: "unsupported host without endpoint", configured: "keys.example.net", wantErr: true},
		{name: "insecure scheme", configured: "http://keys.openpgp.org/vks/v1/upload", wantErr: true},
		{name: "user info", configured: "https://user@keys.openpgp.org/vks/v1/upload", wantErr: true},
		{name: "query", configured: "https://keys.openpgp.org/vks/v1/upload?token=x", wantErr: true},
		{name: "invalid value", configured: "://invalid", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := pgpKeyserverEndpoint(test.configured)
			if (err != nil) != test.wantErr {
				t.Fatalf("pgpKeyserverEndpoint() error = %v, wantErr %t", err, test.wantErr)
			}
			if err == nil && got != test.want {
				t.Fatalf("pgpKeyserverEndpoint() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestReadPGPImport(t *testing.T) {
	directory := mainProjectTempDir(t)
	path := filepath.Join(directory, "private.asc")
	contents := []byte("private key fixture")
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readPGPImport(path)
	if err != nil || string(got) != string(contents) {
		t.Fatalf("readPGPImport() = %q, %v", got, err)
	}
	if _, err := readPGPImport(filepath.Join(directory, "missing.asc")); err == nil {
		t.Fatal("readPGPImport() accepted a missing file")
	}
	largePath := filepath.Join(directory, "large.asc")
	if err := os.WriteFile(largePath, make([]byte, 16<<10+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPGPImport(largePath); err == nil {
		t.Fatal("readPGPImport() accepted a file larger than 16 KiB")
	}
	linkPath := filepath.Join(directory, "linked.asc")
	if err := os.Symlink(path, linkPath); err == nil {
		if _, err := readPGPImport(linkPath); err == nil {
			t.Fatal("readPGPImport() accepted a symlink")
		}
	}
}

func TestWriteAtomicPrivateFile(t *testing.T) {
	path := filepath.Join(mainProjectTempDir(t), "security", "state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	for _, contents := range [][]byte{[]byte("first"), []byte("second")} {
		if err := writeAtomicPrivateFile(path, contents); err != nil {
			t.Fatalf("writeAtomicPrivateFile() error: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(contents) {
			t.Fatalf("stored contents = %q, %v; want %q", got, err, contents)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("stored mode = %v, %v; want 0600", info, err)
		}
	}
}

func TestPGPAuditAndExportRateLimit(t *testing.T) {
	logsDir := filepath.Join(mainProjectTempDir(t), "logs")
	if err := enforcePGPExportRateLimit(logsDir, "operator-a"); err != nil {
		t.Fatalf("empty audit log rate check: %v", err)
	}
	entry := maintenancePGPAuditEntry{
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		Event:      "security.private_key_exported",
		Operator:   "operator-a",
		OperatorIP: "192.0.2.10",
		Reason:     "verified backup",
		Success:    true,
	}
	if err := writePGPAudit(logsDir, entry); err != nil {
		t.Fatalf("writePGPAudit() error: %v", err)
	}
	logPath := filepath.Join(logsDir, "audit.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	var stored maintenancePGPAuditEntry
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("audit entry is not JSON: %v", err)
	}
	if stored.Event != entry.Event || stored.OperatorIP != entry.OperatorIP || !strings.Contains(string(data), "verified backup") {
		t.Fatalf("stored audit entry = %+v", stored)
	}
	info, err := os.Stat(logPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("audit log mode = %v, %v; want 0600", info, err)
	}
	if err := enforcePGPExportRateLimit(logsDir, "operator-a"); err == nil {
		t.Fatal("recent export was not rate limited")
	}
	if err := enforcePGPExportRateLimit(logsDir, "operator-b"); err != nil {
		t.Fatalf("different operator was rate limited: %v", err)
	}
	old := entry
	old.Timestamp = time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano)
	oldData, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, append(oldData, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if err := enforcePGPExportRateLimit(logsDir, "operator-a"); err != nil {
		t.Fatalf("expired export was not ignored: %v", err)
	}
}

func TestPGPPrivateExportAndMaintenanceAuthorization(t *testing.T) {
	directory := mainProjectTempDir(t)
	path := filepath.Join(directory, "private.asc")
	if err := writePrivatePGPExport(path, []byte("private key")); err != nil {
		t.Fatalf("writePrivatePGPExport() error: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "private key" {
		t.Fatalf("export contents = %q, %v", contents, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("export mode = %v, %v; want 0600", info, err)
	}
	if err := writePrivatePGPExport(path, []byte("replacement")); err == nil {
		t.Fatal("private export overwrote an existing file")
	}
	if err := writePrivatePGPExport(filepath.Join(directory, "empty.asc"), nil); err == nil {
		t.Fatal("empty private export was accepted")
	}

	operator, err := authorizePGPMaintenance("generate", nil, nil)
	if os.Geteuid() == 0 {
		if err != nil || operator != "uid:0" {
			t.Fatalf("root authorization = %q, %v; want uid:0", operator, err)
		}
	} else if err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("non-interactive authorization error = %v, want terminal requirement", err)
	}
	if err := requirePGPConfirmation("confirm", "DELETE"); err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("non-interactive confirmation error = %v, want terminal requirement", err)
	}
}

func TestPGPMaintenanceUsageAndReasonValidation(t *testing.T) {
	if err := maintenancePGP(nil, "", "", "", "", nil); err == nil {
		t.Fatal("PGP maintenance accepted a nil config")
	}
	if err := maintenancePGP(nil, "", "", "", "", config.DefaultConfig()); err == nil {
		t.Fatal("PGP maintenance accepted a missing action")
	}
	if err := maintenancePGP([]string{"help"}, "", "", "", "", config.DefaultConfig()); err != nil {
		t.Fatalf("PGP help error: %v", err)
	}

	previous := maintenanceInput
	t.Cleanup(func() { maintenanceInput = previous })
	for _, test := range []struct {
		name string
		text string
		want string
		fail bool
	}{
		{name: "trimmed reason", text: "  emergency key recovery  \n", want: "emergency key recovery"},
		{name: "empty reason", text: " \n", fail: true},
		{name: "oversized reason", text: strings.Repeat("x", 2001), fail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			maintenanceInput = bufio.NewReader(strings.NewReader(test.text))
			got, err := promptPGPReason()
			if (err != nil) != test.fail || got != test.want {
				t.Fatalf("promptPGPReason() = %q, %v; want %q, failure=%t", got, err, test.want, test.fail)
			}
		})
	}
}
