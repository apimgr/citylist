package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLILoggerFiltersEventsAndRotatesPrivateFiles(t *testing.T) {
	path := filepath.Join(clientTestDir(t), "logs", "cli.log")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	logger := &cliLogger{path: path, level: "warn", maxSize: 80, maxFiles: 2}
	logger.Log("debug", "filtered")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("filtered log created a file: %v", err)
	}
	logger.Event("cli.update_completed", map[string]string{"old_version": "1.0.0", "new_version": "1.1.0"})
	logger.Log("error", "first error")
	logger.Log("error", "second error")
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(current), "second error") || strings.Contains(string(current), "filtered") {
		t.Fatalf("current log = %q", current)
	}
	var record map[string]any
	if err := json.Unmarshal(current, &record); err != nil || record["level"] != "error" {
		t.Fatalf("current log record = %v, %v", record, err)
	}
	rotated, err := os.ReadFile(path + ".1")
	if err != nil || !strings.Contains(string(rotated), "first error") {
		t.Fatalf("rotated log = %q, %v", rotated, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("log permissions = %v, %v; want 0600", info, err)
	}
}

func TestCLILoggerWithoutRetentionRemovesOldFile(t *testing.T) {
	path := filepath.Join(clientTestDir(t), "cli.log")
	logger := &cliLogger{path: path, level: "debug", maxSize: 1, maxFiles: 0}
	logger.Log("info", "first")
	logger.Log("info", "second")
	contents, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(contents), "first") || !strings.Contains(string(contents), "second") {
		t.Fatalf("retention-free log = %q, %v", contents, err)
	}
}
