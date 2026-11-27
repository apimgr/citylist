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
	t.Setenv("LOG_DIR", "/custom/logs")
	if got := GetLogsDir("citylist"); got != "/custom/logs" {
		t.Errorf("GetLogsDir() = %q, want /custom/logs", got)
	}
}

func TestGetCacheDirEnvOverride(t *testing.T) {
	t.Setenv("CACHE_DIR", "/custom/cache")
	if got := GetCacheDir("citylist"); got != "/custom/cache" {
		t.Fatalf("GetCacheDir() = %q, want /custom/cache", got)
	}
}

func TestGetBackupDirForOverrides(t *testing.T) {
	t.Setenv("BACKUP_DIR", "/environment/backups")
	if got := GetBackupDirFor("citylist", "/data/citylist", "/flag/backups"); got != "/flag/backups" {
		t.Fatalf("flag override = %q, want /flag/backups", got)
	}
	if got := GetBackupDirFor("citylist", "/data/citylist", ""); got != "/environment/backups" {
		t.Fatalf("environment override = %q, want /environment/backups", got)
	}
}

func TestGetDatabaseDirEnvOverride(t *testing.T) {
	t.Setenv("DATABASE_DIR", "/custom/db")
	if got := GetDatabaseDir("/data/citylist"); got != "/custom/db" {
		t.Errorf("GetDatabaseDir() = %q, want /custom/db", got)
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
	got := GetConfigDir("citylist")
	if got == "" {
		t.Error("GetConfigDir() returned empty string")
	}
}

func TestGetDataDirNoEnv(t *testing.T) {
	t.Setenv("DATA_DIR", "")
	got := GetDataDir("citylist")
	if got == "" {
		t.Error("GetDataDir() returned empty string")
	}
}

func TestGetLogsDirNoEnv(t *testing.T) {
	t.Setenv("LOG_DIR", "")
	got := GetLogsDir("citylist")
	if got == "" {
		t.Error("GetLogsDir() returned empty string")
	}
}

func TestGetBackupDirNoEnv(t *testing.T) {
	t.Setenv("BACKUP_DIR", "")
	got := GetBackupDir("citylist")
	if got == "" {
		t.Error("GetBackupDir() returned empty string")
	}
}

func TestGetDefaultDirs(t *testing.T) {
	t.Setenv("CONFIG_DIR", "/c")
	t.Setenv("DATA_DIR", "/d")
	t.Setenv("LOG_DIR", "/l")

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

func TestEnsurePrivateDirSetsMode(t *testing.T) {
	tmpDir := t.TempDir()
	target := filepath.Join(tmpDir, "private", "config")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatalf("creating test directory: %v", err)
	}
	if err := EnsurePrivateDir(target); err != nil {
		t.Fatalf("EnsurePrivateDir() error: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat private directory: %v", err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("private directory mode = %04o, want 0700", info.Mode().Perm())
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
