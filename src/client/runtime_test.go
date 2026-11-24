package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestClientRuntimePathsUseStableProjectIdentifiers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix XDG path contract")
	}
	oldName, oldOrg := InternalName, InternalOrg
	InternalName, InternalOrg = "stable-name", "stable-org"
	t.Cleanup(func() { InternalName, InternalOrg = oldName, oldOrg })

	root := t.TempDir()
	home := filepath.Join(root, "home")
	configRoot := filepath.Join(root, "config")
	dataRoot := filepath.Join(root, "data")
	cacheRoot := filepath.Join(root, "cache")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("XDG_DATA_HOME", dataRoot)
	t.Setenv("XDG_CACHE_HOME", cacheRoot)

	configPath, err := cliConfigPath("cli.yml")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(configRoot, InternalOrg, InternalName, "cli.yml"); configPath != want {
		t.Fatalf("config path = %q, want %q", configPath, want)
	}
	if err := ensureCLIDirectories(configPath); err != nil {
		t.Fatal(err)
	}
	wantDirs := []string{
		filepath.Dir(configPath),
		filepath.Join(dataRoot, InternalOrg, InternalName),
		filepath.Join(cacheRoot, InternalOrg, InternalName),
		filepath.Join(home, ".local", "log", InternalOrg, InternalName),
	}
	for _, dir := range wantDirs {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("runtime directory %s: %v", dir, err)
		}
		if info.Mode().Perm() != 0700 {
			t.Errorf("runtime directory %s mode = %04o, want 0700", dir, info.Mode().Perm())
		}
	}
	if got, want := cliCacheDirectory(), filepath.Join(cacheRoot, InternalOrg, InternalName); got != want {
		t.Errorf("cache path = %q, want %q", got, want)
	}
	if got, want := cliLogDirectory(), filepath.Join(home, ".local", "log", InternalOrg, InternalName); got != want {
		t.Errorf("log path = %q, want %q", got, want)
	}
}
