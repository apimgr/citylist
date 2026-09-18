package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/apimgr/citylist/src/config"
)

func TestCheckHealth(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/health" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		u, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatalf("failed to parse test server URL: %v", err)
		}

		if err := checkHealth(u.Port()); err != nil {
			t.Errorf("checkHealth() error = %v, want nil", err)
		}
	})

	t.Run("bad status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		u, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatalf("failed to parse test server URL: %v", err)
		}

		if err := checkHealth(u.Port()); err == nil {
			t.Error("expected error for non-200 status, got nil")
		}
	})

	t.Run("connection refused", func(t *testing.T) {
		if err := checkHealth("1"); err == nil {
			t.Error("expected error connecting to closed port, got nil")
		}
	})
}

func newTestConfigPath(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "server.yml")
	cfg := config.DefaultConfig()
	if err := config.Save(path, cfg); err != nil {
		t.Fatalf("failed to save test config: %v", err)
	}
	return path
}

func TestSetApplicationMode(t *testing.T) {
	t.Run("valid mode", func(t *testing.T) {
		path := newTestConfigPath(t)
		setApplicationMode("development", path)

		cfg, err := config.Load(path)
		if err != nil {
			t.Fatalf("failed to reload config: %v", err)
		}
		if cfg.Server.Mode != "development" {
			t.Errorf("Server.Mode = %q, want development", cfg.Server.Mode)
		}
	})
}

func TestShowCurrentMode(t *testing.T) {
	path := newTestConfigPath(t)
	showCurrentMode(path)
}

func TestRunInitialSetup(t *testing.T) {
	path := newTestConfigPath(t)
	runInitialSetup(path)
}

func TestMaintenanceUpdate(t *testing.T) {
	maintenanceUpdate()
}

func TestHandleUpdateCommand(t *testing.T) {
	t.Run("check", func(t *testing.T) {
		cfg := config.DefaultConfig()
		handleUpdateCommand("check", cfg)
	})
	t.Run("yes", func(t *testing.T) {
		cfg := config.DefaultConfig()
		handleUpdateCommand("yes", cfg)
	})
	t.Run("branch no args", func(t *testing.T) {
		cfg := config.DefaultConfig()
		handleUpdateCommand("branch", cfg)
	})
}

func TestPrintHelp(t *testing.T) {
	printHelp()
}

func TestRunCommand(t *testing.T) {
	t.Run("existing command", func(t *testing.T) {
		runCommand("true")
	})
	t.Run("missing command", func(t *testing.T) {
		runCommand("citylist-definitely-not-a-real-command")
	})
}

// Exercises the service lifecycle wrapper functions. On linux they shell
// out to systemctl (absent in the test container, so runCommand just logs
// and continues) and installSystemdService writes to the real, absolute
// /etc/systemd/system path rather than the project tree, so this is safe
// inside the disposable Docker test container.
func TestHandleServiceCommand(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("service commands are linux-specific in this test")
	}
	tmpDir := t.TempDir()
	// installSystemdService writes directly to /etc/systemd/system without
	// creating it first, and uses log.Fatalf on failure (which would kill
	// the whole test binary) — ensure the directory exists so the install
	// path succeeds instead of exiting.
	if err := os.MkdirAll("/etc/systemd/system", 0755); err != nil {
		t.Fatalf("failed to prepare /etc/systemd/system: %v", err)
	}
	handleServiceCommand("start", tmpDir)
	handleServiceCommand("stop", tmpDir)
	handleServiceCommand("restart", tmpDir)
	handleServiceCommand("reload", tmpDir)
	handleServiceCommand("status", tmpDir)
	handleServiceCommand("--install", tmpDir)
	handleServiceCommand("--uninstall", tmpDir)
	handleServiceCommand("--disable", tmpDir)
	handleServiceCommand("--help", tmpDir)
}

func TestHandleMaintenanceCommand(t *testing.T) {
	path := newTestConfigPath(t)
	tmpDir := filepath.Dir(path)

	t.Run("update", func(t *testing.T) {
		handleMaintenanceCommand("update", tmpDir, tmpDir, tmpDir, path)
	})
	t.Run("mode", func(t *testing.T) {
		handleMaintenanceCommand("mode", tmpDir, tmpDir, tmpDir, path)
	})
	t.Run("setup", func(t *testing.T) {
		handleMaintenanceCommand("setup", tmpDir, tmpDir, tmpDir, path)
	})
}

func TestMaintenanceBackupAndRestore(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	dataDir := filepath.Join(tmpDir, "data")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("failed to create data dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "server.yml"), []byte("server:\n  port: \"8080\"\n"), 0644); err != nil {
		t.Fatalf("failed to write config fixture: %v", err)
	}

	backupFile := filepath.Join(tmpDir, "backup.tar.gz")
	maintenanceBackup(configDir, dataDir, backupFile)

	if _, err := os.Stat(backupFile); err != nil {
		t.Fatalf("expected backup file to exist: %v", err)
	}
}
