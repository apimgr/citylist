package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetConfigDirEnvOverride(t *testing.T) {
	t.Setenv("CONFIG_DIR", "/custom/config")
	if got := GetConfigDir("citylist"); got != "/custom/config" {
		t.Errorf("GetConfigDir() = %q, want /custom/config", got)
	}
}

func TestGetDataDirEnvOverride(t *testing.T) {
	t.Setenv("DATA_DIR", "/custom/data")
	if got := GetDataDir("citylist"); got != "/custom/data" {
		t.Errorf("GetDataDir() = %q, want /custom/data", got)
	}
}

func TestGetLogsDirEnvOverride(t *testing.T) {
	t.Setenv("LOGS_DIR", "/custom/logs")
	if got := GetLogsDir("citylist"); got != "/custom/logs" {
		t.Errorf("GetLogsDir() = %q, want /custom/logs", got)
	}
}

func TestGetBackupDirEnvOverride(t *testing.T) {
	t.Setenv("BACKUP_DIR", "/custom/backups")
	if got := GetBackupDir("citylist"); got != "/custom/backups" {
		t.Errorf("GetBackupDir() = %q, want /custom/backups", got)
	}
}

func TestGetConfigDirNonDocker(t *testing.T) {
	t.Setenv("CONFIG_DIR", "")
	os.Unsetenv("CONFIG_DIR")
	got := GetConfigDir("citylist")
	if got == "" {
		t.Error("GetConfigDir() returned empty string")
	}
}

func TestGetDataDirNoEnv(t *testing.T) {
	os.Unsetenv("DATA_DIR")
	got := GetDataDir("citylist")
	if got == "" {
		t.Error("GetDataDir() returned empty string")
	}
}

func TestGetLogsDirNoEnv(t *testing.T) {
	os.Unsetenv("LOGS_DIR")
	got := GetLogsDir("citylist")
	if got == "" {
		t.Error("GetLogsDir() returned empty string")
	}
}

func TestGetBackupDirNoEnv(t *testing.T) {
	os.Unsetenv("BACKUP_DIR")
	got := GetBackupDir("citylist")
	if got == "" {
		t.Error("GetBackupDir() returned empty string")
	}
}

func TestGetDefaultDirs(t *testing.T) {
	t.Setenv("CONFIG_DIR", "/c")
	t.Setenv("DATA_DIR", "/d")
	t.Setenv("LOGS_DIR", "/l")

	configDir, dataDir, logsDir := GetDefaultDirs("citylist")
	if configDir != "/c" {
		t.Errorf("configDir = %q, want /c", configDir)
	}
	if dataDir != "/d" {
		t.Errorf("dataDir = %q, want /d", dataDir)
	}
	if logsDir != "/l" {
		t.Errorf("logsDir = %q, want /l", logsDir)
	}
}

func TestEnsureDir(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "nested", "dir")

	if err := EnsureDir(target); err != nil {
		t.Fatalf("EnsureDir() error: %v", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("expected directory to exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected target to be a directory")
	}
}

func TestEnsureDirs(t *testing.T) {
	tmpDir := t.TempDir()
	a := filepath.Join(tmpDir, "a")
	b := filepath.Join(tmpDir, "b")

	if err := EnsureDirs(a, b); err != nil {
		t.Fatalf("EnsureDirs() error: %v", err)
	}

	for _, dir := range []string{a, b} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("expected %q to exist: %v", dir, err)
		}
	}
}

func TestIsDocker(t *testing.T) {
	// Just verify it returns a bool without panicking; actual value depends
	// on the environment the test runs in.
	_ = isDocker()
}

func TestToPascalCase(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"citylist", "Citylist"},
		{"a", "A"},
		{"Already", "Already"},
	}
	for _, tt := range tests {
		if got := toPascalCase(tt.in); got != tt.want {
			t.Errorf("toPascalCase(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
