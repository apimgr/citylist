package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/server"
)

func TestProbeDataDirectoryWrite(t *testing.T) {
	if err := probeDataDirectoryWrite(t.TempDir()); err != nil {
		t.Fatalf("probeDataDirectoryWrite() error: %v", err)
	}
}

func TestNotifyMaintenanceTransition(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.Maintenance.Notify.OnEnter = true
	cfg.Server.Maintenance.Notify.OnExit = true
	state := &server.Maintenance{Reason: "database", Since: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), SelfHealing: server.SelfHealingInfo{Attempts: 2}}
	for _, test := range []struct {
		name       string
		transition server.MaintenanceTransition
		wantEvent  string
	}{
		{name: "entered", transition: server.MaintenanceTransition{Entered: true, State: state}, wantEvent: "maintenance_entered"},
		{name: "recovered", transition: server.MaintenanceTransition{Recovered: true, State: state}, wantEvent: "maintenance_exited"},
		{name: "exhausted", transition: server.MaintenanceTransition{Exhausted: true, State: state}, wantEvent: "self_healing_exhausted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			notified := false
			notify := func(_ context.Context, event string, details map[string]string) error {
				notified = event == test.wantEvent && details["reason"] == "database" && details["attempts"] == "2"
				return nil
			}
			if err := notifyMaintenanceTransition(context.Background(), cfg, notify, test.transition); err != nil {
				t.Fatalf("notifyMaintenanceTransition() error: %v", err)
			}
			if !notified {
				t.Fatalf("maintenance event was not sent with expected details")
			}
		})
	}
	if err := notifyMaintenanceTransition(context.Background(), cfg, nil, server.MaintenanceTransition{}); err != nil {
		t.Fatalf("empty maintenance transition error: %v", err)
	}
}

func TestRemoveExpiredProjectTempDirs(t *testing.T) {
	base := t.TempDir()
	oldDir, err := os.MkdirTemp(base, internalName+"-maintenance-test-")
	if err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(oldDir, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "artifact"), []byte("temporary data"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(oldDir) }()
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldDir, old, old); err != nil {
		t.Fatal(err)
	}
	removed, freed, err := removeExpiredProjectTempDirsAt(base, internalName, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("removeExpiredProjectTempDirs() error: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed %d project temp directories, want 1", removed)
	}
	if freed != int64(len("temporary data")) {
		t.Fatalf("freed bytes = %d, want %d", freed, len("temporary data"))
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("expired temp directory still exists or stat failed: %v", err)
	}
}

func TestRemoveExpiredLogsPreservesAuditHistoryAndReportsBytes(t *testing.T) {
	logsDir := t.TempDir()
	old := time.Now().Add(-48 * time.Hour)
	for name, content := range map[string]string{"server.log.1": "old server log", "audit.log.1": "retained audit history"} {
		path := filepath.Join(logsDir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	removed, freed, err := removeExpiredLogs(logsDir, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("removeExpiredLogs() error: %v", err)
	}
	if removed != 1 || freed != int64(len("old server log")) {
		t.Fatalf("removed=%d freed=%d, want 1 and %d", removed, freed, len("old server log"))
	}
	if _, err := os.Stat(filepath.Join(logsDir, "audit.log.1")); err != nil {
		t.Fatalf("audit rotation was removed: %v", err)
	}
}
