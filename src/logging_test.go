package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/citylist/src/config"
)

func TestFormatLogFormats(t *testing.T) {
	fields := map[string]any{"time": "2026-01-02T03:04:05Z", "level": "INFO", "msg": "server started"}
	for _, format := range []string{"json", "text", "logfmt", "syslog", "fail2ban"} {
		t.Run(format, func(t *testing.T) {
			line, err := formatLog(fields, format)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(string(line), "\n") {
				t.Fatalf("missing line ending: %q", line)
			}
			if format == "json" {
				var decoded map[string]any
				if err := json.Unmarshal(line, &decoded); err != nil {
					t.Fatalf("invalid JSON line: %v", err)
				}
			}
			if strings.ContainsAny(string(line), "\x1b\r") {
				t.Fatalf("raw log contains a control character: %q", line)
			}
		})
	}
}

func TestFormatSecurityLogFormats(t *testing.T) {
	fields := map[string]any{
		"time": "2026-11-22T12:13:14Z", "level": "WARN",
		"msg": "blocked | request=\nsecond line", "remote_ip": "192.0.2.10",
	}
	tests := []struct {
		format string
		want   []string
	}{
		{format: "cef", want: []string{"CEF:0|Citylist|citylist|1|security|", "blocked \\| request\\= second line", "remote_ip=192.0.2.10"}},
		{format: "syslog", want: []string{"<132>1 2026-11-22T12:13:14Z", "blocked | request= second line"}},
		{format: "fail2ban", want: []string{"2026-11-22T12:13:14Z [security]", "blocked | request= second line"}},
		{format: "text", want: []string{"2026-11-22T12:13:14Z [WARN]", "blocked | request= second line"}},
	}
	for _, test := range tests {
		t.Run(test.format, func(t *testing.T) {
			line, err := formatSecurityLog(fields, test.format)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(string(line), "\n") || strings.ContainsAny(string(line), "\r\x1b") {
				t.Fatalf("security log is not a clean single line: %q", line)
			}
			for _, expected := range test.want {
				if !strings.Contains(string(line), expected) {
					t.Errorf("security log %q does not contain %q", line, expected)
				}
			}
		})
	}
	jsonLine, err := formatSecurityLog(fields, "json")
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(jsonLine, &decoded); err != nil {
		t.Fatalf("security JSON log is invalid: %v", err)
	}
}

func TestSecuritySyslogPriority(t *testing.T) {
	for level, want := range map[string]int{"ERROR": 131, "WARN": 132, "INFO": 134, "DEBUG": 134} {
		if got := securitySyslogPriority(level); got != want {
			t.Errorf("securitySyslogPriority(%q) = %d, want %d", level, got, want)
		}
	}
}

func TestFormatCustomLogSubstitutesAndCleansFields(t *testing.T) {
	fields := map[string]any{
		"time": "2026-11-22T12:13:14Z", "level": "INFO", "msg": "request\ncomplete",
		"remote_ip": "192.0.2.10", "method": "GET", "path": "/cities", "query": "q=New York",
		"status": 200, "bytes": 42, "latency": "12ms", "latency_ms": 12,
		"user_agent": "citylist\rcli", "referer": "https://example.test/", "request_id": "req-1",
		"fqdn": "city.example.test", "protocol": "HTTP/1.1", "tls_version": "TLS1.3",
		"country": "US", "asn": 64500,
	}
	pattern := "{time} {date} {datetime} {remote_ip} {method} {path} {query} {status} {bytes} {latency} {latency_ms} {user_agent} {referer} {request_id} {fqdn} {protocol} {tls_version} {country} {asn} {msg}"
	line, err := formatCustomLog(fields, pattern)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(line), "\n") || strings.ContainsAny(string(line), "\r\x1b") {
		t.Fatalf("custom log is not a clean single line: %q", line)
	}
	for _, expected := range []string{"192.0.2.10", "GET /cities q=New York 200 42 12ms 12", "citylist cli", "req-1", "TLS1.3 US 64500 request complete"} {
		if !strings.Contains(string(line), expected) {
			t.Errorf("custom log %q does not contain %q", line, expected)
		}
	}
}

func TestLogRotationAndRetentionPolicyValidation(t *testing.T) {
	for _, policy := range []string{"never", "daily", "weekly,50MB", "1GB", "monthly"} {
		if !validRotationPolicy(policy) {
			t.Errorf("validRotationPolicy(%q) = false", policy)
		}
	}
	for _, policy := range []string{"", "weekly,0MB", "forever", "daily,unknown"} {
		if validRotationPolicy(policy) {
			t.Errorf("validRotationPolicy(%q) = true", policy)
		}
	}
	for _, policy := range []string{"none", "forever", "3", "30d", "4w", "12m", "weekly:2", "monthly:5"} {
		if !validRetentionPolicy(policy) {
			t.Errorf("validRetentionPolicy(%q) = false", policy)
		}
	}
	for _, policy := range []string{"", "weekly:", "-1d", "2y"} {
		if validRetentionPolicy(policy) {
			t.Errorf("validRetentionPolicy(%q) = true", policy)
		}
	}
}

func TestAuditLogUsesRequiredFieldsAndChecksumChain(t *testing.T) {
	dir := t.TempDir()
	settings := config.DefaultConfig().Server.Logs
	settings.Defaults.Rotate = "never"
	settings.Defaults.Keep = "forever"
	settings.Audit.Rotate = "never"
	settings.Audit.Keep = "forever"
	settings.Audit.Format = "json"
	settings.Audit.Enabled = boolPointer(true)
	if err := configureLogs(dir, "production", settings); err != nil {
		t.Fatal(err)
	}
	defer func() {
		activeLogs.Lock()
		if activeLogs.value != nil {
			activeLogs.value.close()
		}
		activeLogs.value = nil
		activeLogs.Unlock()
	}()
	if err := writeAuditEvent("security.test", "warn", "failure", "cli", "127.0.0.1", map[string]string{"type": "test", "id": "target"}, map[string]any{"reason": "invalid", "token_value": "private"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err = json.Unmarshal(data, &record); err != nil {
		t.Fatalf("decode audit JSONL: %v", err)
	}
	for _, key := range []string{"id", "time", "event", "category", "severity", "actor", "result", "checksum"} {
		if _, ok := record[key]; !ok {
			t.Errorf("audit record omitted %q: %s", key, data)
		}
	}
	if len(record["id"].(string)) != 26 {
		t.Errorf("audit id is not ULID length: %q", record["id"])
	}
	details := record["details"].(map[string]any)
	if details["token_value"] != "[REDACTED]" {
		t.Errorf("sensitive detail was not redacted: %#v", details)
	}
	if err = writeAuditEvent("server.test", "info", "success", "scheduler", "127.0.0.1", nil, nil); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(mustRead(t, filepath.Join(dir, "audit.log")))), "\n")
	if len(lines) != 2 {
		t.Fatalf("audit lines=%d, want 2", len(lines))
	}
}

func TestConfigureLogsClosesPreviousDestinations(t *testing.T) {
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	settings := config.DefaultConfig().Server.Logs
	settings.Defaults.Rotate = "never"
	settings.Defaults.Keep = "forever"
	if err := configureLogs(firstDir, "production", settings); err != nil {
		t.Fatal(err)
	}
	activeLogs.RLock()
	previousFile := activeLogs.value.files["server"].file
	activeLogs.RUnlock()
	if err := configureLogs(secondDir, "production", settings); err != nil {
		t.Fatal(err)
	}
	if _, err := previousFile.Write([]byte("should not be written\n")); err == nil {
		t.Fatal("previous server log destination remained open after reload")
	}
	activeLogs.Lock()
	if activeLogs.value != nil {
		activeLogs.value.close()
	}
	activeLogs.value = nil
	activeLogs.Unlock()
}

func TestLogDestinationRotatesCompressesAndPrunes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		t.Fatal(err)
	}
	destination := &logDestination{file: file, path: path, rotate: "never", keep: "1", opened: time.Now(), maxBytes: 1, compress: true}
	if err = destination.write([]byte("line\n")); err != nil {
		t.Fatal(err)
	}
	_ = destination.file.Close()
	archives, err := filepath.Glob(path + ".*.gz")
	if err != nil {
		t.Fatal(err)
	}
	if len(archives) != 1 {
		t.Fatalf("compressed archives=%d, want 1", len(archives))
	}
}

func boolPointer(value bool) *bool { return &value }
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
