package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectOrganizationAndPrivateTempDirectory(t *testing.T) {
	if got := ProjectOrganization(); got != "apimgr" {
		t.Fatalf("ProjectOrganization() = %q, want apimgr", got)
	}
	directory, cleanup, err := NewProjectTempDir("citylist")
	if err != nil {
		t.Fatalf("NewProjectTempDir(): %v", err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("temporary directory mode = %04o, want 0700", info.Mode().Perm())
	}
	if filepath.Dir(filepath.Dir(directory)) != os.TempDir() || filepath.Base(filepath.Dir(directory)) != "apimgr" {
		t.Errorf("temporary directory %q is not under the project organization temp root", directory)
	}
	if !strings.HasPrefix(filepath.Base(directory), "citylist-") {
		t.Errorf("temporary directory %q lacks the internal name prefix", directory)
	}
	cleanup()
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Errorf("temporary directory remains after cleanup: %v", err)
	}
}

func TestNewProjectTempDirRejectsEmptyInternalName(t *testing.T) {
	if _, cleanup, err := NewProjectTempDir(" "); err == nil {
		cleanup()
		t.Fatal("NewProjectTempDir accepted an empty internal name")
	}
}
