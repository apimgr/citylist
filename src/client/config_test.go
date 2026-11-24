package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCLIConfigPathAndPermissions(t *testing.T) {
	root := clientTestDir(t)
	t.Setenv("XDG_CONFIG_HOME", root)
	path, err := cliConfigPath("dev")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "apimgr", "citylist", "dev.yml")
	if path != want {
		t.Fatalf("config path = %q, want %q", path, want)
	}
	cfg := defaultCLIConfig()
	cfg.Server.Primary = "https://example.test"
	cfg.Auth.Token = "own_0123456789ABCDEFGHIJKLMNOPQRSTUV"
	if err := saveCLIConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadCLIConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Server.Primary != cfg.Server.Primary || loaded.Auth.Token != cfg.Auth.Token {
		t.Fatalf("loaded config does not preserve configured values")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("config mode = %04o, want 0600", info.Mode().Perm())
		}
		info, err = os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0700 {
			t.Errorf("config directory mode = %04o, want 0700", info.Mode().Perm())
		}
	}
}

func TestCLIURLAndTokenValidation(t *testing.T) {
	if !validServerURL("https://city.example/path") {
		t.Fatal("valid HTTPS URL rejected")
	}
	if validServerURL("ftp://city.example") {
		t.Fatal("non-HTTP URL accepted")
	}
	if !validCLIToken("own_0123456789ABCDEFGHIJKLMNOPQRSTUV") {
		t.Fatal("valid owner token rejected")
	}
	if validCLIToken("invalid") {
		t.Fatal("invalid token accepted")
	}
}

func TestDefaultCLIConfigUsesTableOutput(t *testing.T) {
	if got := defaultCLIConfig().Output.Format; got != "table" {
		t.Fatalf("default output format = %q, want table", got)
	}
}

func TestSaveServerOnlyFillsEmptyOrInvalidConfig(t *testing.T) {
	path := filepath.Join(clientTestDir(t), "cli.yml")
	cfg := defaultCLIConfig()
	if err := saveServerIfEmptyOrInvalid(path, &cfg, "https://first.example"); err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Primary != "https://first.example" {
		t.Fatalf("saved server = %q", cfg.Server.Primary)
	}
	if err := saveServerIfEmptyOrInvalid(path, &cfg, "https://temporary.example"); err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Primary != "https://first.example" {
		t.Fatalf("valid configured server was replaced: %q", cfg.Server.Primary)
	}
}

func TestCLIConfigPathSupportsProfilesAndExplicitPaths(t *testing.T) {
	root := clientTestDir(t)
	t.Setenv("XDG_CONFIG_HOME", root)
	configDirectory := filepath.Join(root, "apimgr", "citylist")
	if err := os.MkdirAll(configDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "stage.yaml"), []byte("server:\n  primary: https://stage.example\n"), 0600); err != nil {
		t.Fatal(err)
	}
	profilePath, err := cliConfigPath("stage")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(configDirectory, "stage.yaml"); profilePath != want {
		t.Fatalf("profile path = %q, want %q", profilePath, want)
	}

	explicitPath := filepath.Join(root, "external", "production.yml")
	resolved, err := cliConfigPath(explicitPath)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != explicitPath {
		t.Fatalf("explicit path = %q, want %q", resolved, explicitPath)
	}

	newProfile, err := cliConfigPath("new-profile")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(configDirectory, "new-profile.yml"); newProfile != want {
		t.Fatalf("new profile path = %q, want %q", newProfile, want)
	}
}
