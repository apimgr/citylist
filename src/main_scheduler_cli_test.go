package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/database"
	"github.com/apimgr/citylist/src/scheduler"
)

func TestRunSchedulerCommandUsesConfiguredRuntimeDirectories(t *testing.T) {
	root := mainProjectTempDir(t)
	configDir := filepath.Join(root, "config")
	dataDir := filepath.Join(root, "data")
	logsDir := filepath.Join(root, "logs")
	for key, value := range map[string]string{"CONFIG_DIR": configDir, "DATA_DIR": dataDir, "LOGS_DIR": logsDir} {
		t.Setenv(key, value)
	}
	t.Setenv("DATABASE_DIR", filepath.Join(dataDir, "db"))
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	if err := config.Save(filepath.Join(configDir, "server.yml"), cfg); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runSchedulerCommand([]string{"list"}, &output); err != nil {
		t.Fatalf("runSchedulerCommand(list): %v", err)
	}
	if !strings.Contains(output.String(), "TASK\tENABLED\tSCHEDULE") || !strings.Contains(output.String(), "backup_daily") {
		t.Fatalf("scheduler list output = %q", output.String())
	}
	if _, err := os.Stat(filepath.Join(dataDir, "db", "server.db")); err != nil {
		t.Fatalf("scheduler database was not created in configured data directory: %v", err)
	}
}

func TestRunSchedulerCommandHelpAndMissingConfiguration(t *testing.T) {
	var output bytes.Buffer
	if err := runSchedulerCommand([]string{"--help"}, &output); err != nil || !strings.Contains(output.String(), "Usage:") {
		t.Fatalf("scheduler help = %q, %v", output.String(), err)
	}
	root := mainProjectTempDir(t)
	for key, value := range map[string]string{"CONFIG_DIR": filepath.Join(root, "config"), "DATA_DIR": filepath.Join(root, "data"), "LOGS_DIR": filepath.Join(root, "logs")} {
		t.Setenv(key, value)
	}
	if err := runSchedulerCommand([]string{"list"}, &output); err == nil || !strings.Contains(strings.ToLower(err.Error()), "configuration file not found") {
		t.Fatalf("missing configuration error = %v", err)
	}
}

func TestExecuteSchedulerCommand(t *testing.T) {
	store, err := database.OpenSQLite(filepath.Join(mainProjectTempDir(t), "scheduler.db"), database.DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close scheduler store: %v", err)
		}
	})
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	instance, err := scheduler.NewPersistent(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	runs := 0
	if err := instance.AddScheduledTask("example", "@daily", true, time.UTC, func() error { runs++; return nil }); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer

	if err := executeSchedulerCommand([]string{"list"}, &output, instance, store); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(output.String(), "example") || !strings.Contains(output.String(), "@daily") {
		t.Fatalf("list output = %q", output.String())
	}
	output.Reset()
	if err := executeSchedulerCommand([]string{"show", "example"}, &output, instance, store); err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(output.String(), "TASK: example") {
		t.Fatalf("show output = %q", output.String())
	}
	if err := executeSchedulerCommand([]string{"disable", "example"}, &output, instance, store); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := executeSchedulerCommand([]string{"run", "example"}, &output, instance, store); err == nil {
		t.Fatal("run on disabled task succeeded")
	}
	if err := executeSchedulerCommand([]string{"enable", "example"}, &output, instance, store); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := executeSchedulerCommand([]string{"run", "example"}, &output, instance, store); err != nil {
		t.Fatalf("run: %v", err)
	}
	if runs != 1 {
		t.Fatalf("runs = %d, want 1", runs)
	}
	output.Reset()
	if err := executeSchedulerCommand([]string{"history", "example"}, &output, instance, store); err != nil {
		t.Fatalf("history: %v", err)
	}
	if !strings.Contains(output.String(), "success") {
		t.Fatalf("history output = %q", output.String())
	}
}

func TestExecuteSchedulerCommandRejectsUnknownTaskAndCommand(t *testing.T) {
	store, err := database.OpenSQLite(filepath.Join(mainProjectTempDir(t), "scheduler.db"), database.DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close scheduler store: %v", err)
		}
	})
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	instance, err := scheduler.NewPersistent(store, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.AddScheduledTask("example", "@daily", true, time.UTC, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, command := range [][]string{{"bogus", "example"}, {"show", "missing"}} {
		if err := executeSchedulerCommand(command, &bytes.Buffer{}, instance, store); err == nil {
			t.Errorf("executeSchedulerCommand(%v) succeeded", command)
		}
	}
}
