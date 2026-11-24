package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnforcePrivatePathsSecuresExistingTrees(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	dataDir := filepath.Join(root, "data")
	privateDirs := []string{
		filepath.Join(configDir, "yaml.d"),
		filepath.Join(configDir, "assets"),
		filepath.Join(configDir, "ssl"),
		filepath.Join(configDir, "tor"),
		filepath.Join(configDir, "i2p"),
		filepath.Join(dataDir, "security"),
		filepath.Join(dataDir, "tor", "site"),
		filepath.Join(dataDir, "i2p", "site"),
	}
	privateFiles := make([]string, 0, len(privateDirs)+2)
	for _, directory := range privateDirs {
		if err := os.MkdirAll(directory, 0755); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(directory, "fixture")
		if err := os.WriteFile(file, []byte("private\n"), 0644); err != nil {
			t.Fatal(err)
		}
		privateFiles = append(privateFiles, file)
	}
	for _, file := range []string{filepath.Join(configDir, "server.yml"), filepath.Join(configDir, "secrets.yml")} {
		if err := os.WriteFile(file, []byte("server: {}\n"), 0644); err != nil {
			t.Fatal(err)
		}
		privateFiles = append(privateFiles, file)
	}

	if err := EnforcePrivatePaths(configDir, dataDir); err != nil {
		t.Fatal(err)
	}
	for _, directory := range privateDirs {
		info, err := os.Stat(directory)
		if err != nil {
			t.Fatalf("stat %s: %v", directory, err)
		}
		if got := info.Mode().Perm(); got != 0700 {
			t.Errorf("directory mode for %s = %04o, want 0700", directory, got)
		}
	}
	for _, file := range privateFiles {
		info, err := os.Stat(file)
		if err != nil {
			t.Fatalf("stat %s: %v", file, err)
		}
		if got := info.Mode().Perm(); got != 0600 {
			t.Errorf("file mode for %s = %04o, want 0600", file, got)
		}
	}
}

func TestEnforcePrivatePathsCreatesMissingDirectories(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	dataDir := filepath.Join(root, "data")
	privateDirs := []string{
		filepath.Join(configDir, "yaml.d"),
		filepath.Join(configDir, "assets"),
		filepath.Join(configDir, "ssl"),
		filepath.Join(configDir, "tor"),
		filepath.Join(configDir, "i2p"),
		filepath.Join(dataDir, "security"),
		filepath.Join(dataDir, "tor"),
		filepath.Join(dataDir, "i2p"),
	}
	if err := EnforcePrivatePaths(configDir, dataDir); err != nil {
		t.Fatal(err)
	}
	for _, directory := range privateDirs {
		info, err := os.Stat(directory)
		if err != nil {
			t.Fatalf("stat %s: %v", directory, err)
		}
		if got := info.Mode().Perm(); got != 0700 {
			t.Errorf("directory mode for %s = %04o, want 0700", directory, got)
		}
	}
}

func TestEnforcePrivatePathsRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(configDir, "assets")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if err := EnforcePrivatePaths(configDir, dataDir); err == nil {
		t.Fatal("EnforcePrivatePaths accepted a private directory symlink")
	}
	info, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Fatalf("symlink target mode = %04o, want unchanged 0755", info.Mode().Perm())
	}
}
