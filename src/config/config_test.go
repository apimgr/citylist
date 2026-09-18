package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Server.Port != "8080" {
		t.Errorf("Server.Port = %q, want 8080", cfg.Server.Port)
	}
	if cfg.Server.Mode != "production" {
		t.Errorf("Server.Mode = %q, want production", cfg.Server.Mode)
	}
	if cfg.WebUI.Theme != "dark" {
		t.Errorf("WebUI.Theme = %q, want dark", cfg.WebUI.Theme)
	}
	if !cfg.WebUI.Notifications.Enabled {
		t.Error("expected Notifications.Enabled to be true")
	}
	if len(cfg.WebRobots.Allow) == 0 {
		t.Error("expected WebRobots.Allow to be non-empty")
	}
	if cfg.WebSecurity.CORS != "*" {
		t.Errorf("WebSecurity.CORS = %q, want *", cfg.WebSecurity.CORS)
	}
}

func TestSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.yml")

	cfg := DefaultConfig()
	cfg.Server.Port = "9090"
	cfg.WebUI.Theme = "light"

	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded.Server.Port != "9090" {
		t.Errorf("loaded Server.Port = %q, want 9090", loaded.Server.Port)
	}
	if loaded.WebUI.Theme != "light" {
		t.Errorf("loaded WebUI.Theme = %q, want light", loaded.WebUI.Theme)
	}
}

func TestLoadCreatesDefaultWhenMissing(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "missing.yml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Server.Port != "8080" {
		t.Errorf("expected default port 8080, got %q", cfg.Server.Port)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected Load() to create the config file: %v", err)
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "bad.yml")

	if err := os.WriteFile(path, []byte("not: valid: yaml: [broken"), 0644); err != nil {
		t.Fatalf("failed to write test fixture: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("expected error loading invalid YAML, got nil")
	}
}

func TestMigrateYamlToYml(t *testing.T) {
	tmpDir := t.TempDir()
	ymlPath := filepath.Join(tmpDir, "config.yml")
	yamlPath := filepath.Join(tmpDir, "config.yaml")

	if err := os.WriteFile(yamlPath, []byte("server:\n  port: \"1234\"\n"), 0644); err != nil {
		t.Fatalf("failed to write test fixture: %v", err)
	}

	if err := migrateYamlToYml(ymlPath); err != nil {
		t.Fatalf("migrateYamlToYml() error: %v", err)
	}

	if _, err := os.Stat(ymlPath); err != nil {
		t.Errorf("expected %q to exist after migration: %v", ymlPath, err)
	}
	if _, err := os.Stat(yamlPath); !os.IsNotExist(err) {
		t.Errorf("expected old .yaml file to be removed, stat err = %v", err)
	}
}

func TestMigrateYamlToYmlNoOldFile(t *testing.T) {
	tmpDir := t.TempDir()
	ymlPath := filepath.Join(tmpDir, "config.yml")

	if err := migrateYamlToYml(ymlPath); err != nil {
		t.Fatalf("migrateYamlToYml() error when no old file exists: %v", err)
	}
}

func TestMigrateYamlToYmlNonYmlPath(t *testing.T) {
	if err := migrateYamlToYml("/some/path/config.json"); err != nil {
		t.Fatalf("migrateYamlToYml() should be a no-op for non-.yml paths: %v", err)
	}
}
