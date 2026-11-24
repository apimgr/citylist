package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/database"
)

func TestBackupByteSizeAndRestoreLimits(t *testing.T) {
	tests := []struct {
		input string
		want  int64
		bad   bool
	}{
		{input: "1K", want: 1024},
		{input: "2MiB", want: 2 << 20},
		{input: " 3g ", want: 3 << 30},
		{input: "0", bad: true},
		{input: "-1M", bad: true},
		{input: "nope", bad: true},
	}
	for _, test := range tests {
		got, err := parseBackupByteSize(test.input)
		if (err != nil) != test.bad || (!test.bad && got != test.want) {
			t.Errorf("parseBackupByteSize(%q) = (%d, %v), want (%d, bad=%t)", test.input, got, err, test.want, test.bad)
		}
	}

	cfg := config.DefaultConfig()
	limits := restoreLimits(cfg)
	if limits.MaxArchiveSize != 1<<30 || limits.MaxExpandedSize != 4<<30 || limits.MaxFiles != 10000 || limits.MaxFileSize != 512<<20 || limits.MaxRatio != 100 {
		t.Fatalf("default restore limits = %+v", limits)
	}
	cfg.Server.Restore.MaxArchiveSize = "invalid"
	cfg.Server.Restore.MaxExpandedSize = "8M"
	cfg.Server.Restore.MaxFiles = 0
	limits = restoreLimits(cfg)
	if limits.MaxArchiveSize != 1<<30 || limits.MaxExpandedSize != 8<<20 || limits.MaxFiles != 10000 {
		t.Fatalf("validated restore limits = %+v", limits)
	}
}

func TestBackupPrivateFilesystemHelpers(t *testing.T) {
	root := mainProjectTempDir(t)
	directory := filepath.Join(root, "backups")
	if err := secureBackupDirectory(directory); err != nil {
		t.Fatalf("secureBackupDirectory() error: %v", err)
	}
	info, err := os.Stat(directory)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("backup directory mode = %v, %v; want 0700", info, err)
	}

	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "private", "copy")
	if err := os.WriteFile(source, []byte("private backup data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		t.Fatal(err)
	}
	if err := copyPrivateFile(source, destination); err != nil {
		t.Fatalf("copyPrivateFile() error: %v", err)
	}
	info, err = os.Stat(destination)
	contents, readErr := os.ReadFile(destination)
	if err != nil || readErr != nil || info == nil || info.Mode().Perm() != 0600 || string(contents) != "private backup data" {
		mode := os.FileMode(0)
		if info != nil {
			mode = info.Mode().Perm()
		}
		t.Fatalf("private copy = %q, %v, mode %v; want source contents and 0600", contents, err, mode)
	}
	if err := copyPrivateFile(source, destination); err == nil {
		t.Fatal("copyPrivateFile() overwrote an existing destination")
	}
}

func TestLatestFullBackupDistinguishesFirstRunFromInvalidCandidate(t *testing.T) {
	root := mainProjectTempDir(t)
	directory := filepath.Join(root, "backups")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	if _, _, _, err := latestFullBackup(context.Background(), directory, "", root, cfg); !errors.Is(err, errNoFullBackup) {
		t.Fatalf("empty backup directory error = %v, want errNoFullBackup", err)
	}
	if err := os.WriteFile(filepath.Join(directory, projectName+"_backup_broken.tar.gz"), []byte("not an archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := latestFullBackup(context.Background(), directory, "", root, cfg); err == nil || errors.Is(err, errNoFullBackup) {
		t.Fatalf("invalid candidate error = %v, want a verification error", err)
	}
}

func TestScheduledDailyBackupCreatesVerifiedFullOnFirstRun(t *testing.T) {
	root := mainProjectTempDir(t)
	configDir := filepath.Join(root, "config")
	dataDir := filepath.Join(root, "data")
	backupDir := filepath.Join(root, "backup")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "server.yml")
	if err := config.Save(configPath, config.DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(dataDir, "db", "sqlite", "server.db")
	store, err := database.OpenSQLite(databasePath, database.DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	if err := runScheduledDailyBackup(context.Background(), configPath, configDir, dataDir, backupDir, cfg, store); err != nil {
		t.Fatalf("first daily backup run: %v", err)
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || filepath.Ext(entries[0].Name()) != ".gz" {
		t.Fatalf("first run backup files = %v, want one unencrypted full backup", entries)
	}
	if _, _, _, err := latestFullBackup(context.Background(), backupDir, "", root, cfg); err != nil {
		t.Fatalf("verify newly created full backup as incremental base: %v", err)
	}
	newest, err := newestBackup(backupDir)
	if err != nil || newest != filepath.Join(backupDir, entries[0].Name()) {
		t.Fatalf("newestBackup() = (%q, %v), want the verified full backup", newest, err)
	}
	if count := backupCount(backupDir); count != 1 {
		t.Fatalf("backupCount() = %d, want one full backup", count)
	}
	if err := runScheduledHourlyBackup(context.Background(), configPath, configDir, dataDir, backupDir, cfg, store); err != nil {
		t.Fatalf("hourly incremental backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backupDir, projectName+"-hourly.tar.gz")); err != nil {
		t.Fatalf("hourly incremental was not created: %v", err)
	}
}

func TestScheduledSchedulerRegistersRequiredTasksAndOnlyEnabledOverlayHealth(t *testing.T) {
	root := mainProjectTempDir(t)
	store, err := database.OpenSQLite(filepath.Join(root, "server.db"), database.DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	scheduler, err := newApplicationScheduler(cfg, root, filepath.Join(root, "data"), filepath.Join(root, "logs"), filepath.Join(root, "backup"), store, context.Background(), map[string]func() error{
		"tor_health": func() error { return nil },
		"i2p_health": func() error { return nil },
		"unexpected": func() error { return nil },
	})
	if err != nil {
		t.Fatalf("newApplicationScheduler() error: %v", err)
	}
	registered := map[string]bool{}
	for _, task := range scheduler.GetTasks() {
		registered[task.Name] = true
	}
	for _, name := range []string{"ssl_renewal", "geoip_update", "blocklist_update", "security_cleanup", "keyserver_retry", "cve_update", "update_check", "token_cleanup", "log_rotation", "backup_daily", "backup_hourly", "disk_check", "healthcheck_self", "tor_health", "i2p_health"} {
		if !registered[name] {
			t.Errorf("required task %q was not registered", name)
		}
	}
	if registered["unexpected"] {
		t.Error("scheduler registered an unrecognized health task")
	}
	if err := runScheduledSSLRenewal(root, cfg); err != nil {
		t.Fatalf("disabled SSL renewal should be a no-op: %v", err)
	}
}
