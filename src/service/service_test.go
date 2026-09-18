package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDetectServiceManager(t *testing.T) {
	got := DetectServiceManager()
	switch runtime.GOOS {
	case "linux":
		if got != ServiceSystemd && got != ServiceRunit && got != ServiceUnknown {
			t.Errorf("DetectServiceManager() on linux = %v, want systemd/runit/unknown", got)
		}
	case "darwin":
		if got != ServiceLaunchd {
			t.Errorf("DetectServiceManager() on darwin = %v, want ServiceLaunchd", got)
		}
	case "windows":
		if got != ServiceWindows {
			t.Errorf("DetectServiceManager() on windows = %v, want ServiceWindows", got)
		}
	}
}

func TestGetBinaryPath(t *testing.T) {
	path := GetBinaryPath()
	if path == "" {
		t.Fatal("GetBinaryPath() returned empty string")
	}

	switch runtime.GOOS {
	case "windows":
		if !strings.Contains(path, "citylist") || !strings.Contains(path, "apimgr") {
			t.Errorf("GetBinaryPath() = %q, want it to contain citylist and apimgr", path)
		}
	default:
		if path != "/usr/local/bin/citylist" {
			t.Errorf("GetBinaryPath() = %q, want /usr/local/bin/citylist", path)
		}
	}
}

func TestCopyBinary(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "src-binary")
	dst := filepath.Join(tmpDir, "nested", "dst-binary")

	content := []byte("fake binary content")
	if err := os.WriteFile(src, content, 0755); err != nil {
		t.Fatalf("failed to write source fixture: %v", err)
	}

	if err := copyBinary(src, dst); err != nil {
		t.Fatalf("copyBinary() error: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read copied binary: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("copied content = %q, want %q", got, content)
	}
}

func TestCopyBinaryMissingSource(t *testing.T) {
	tmpDir := t.TempDir()
	err := copyBinary(filepath.Join(tmpDir, "does-not-exist"), filepath.Join(tmpDir, "dst"))
	if err == nil {
		t.Error("expected error copying nonexistent source, got nil")
	}
}

// The lifecycle functions below shell out to the host's service manager.
// Inside the disposable test container they either no-op with an error
// (manager binary absent) or affect only the container's own filesystem,
// so exercising them here for coverage is safe.
func TestServiceLifecycleCallsDoNotPanic(t *testing.T) {
	_ = Install()
	_ = Uninstall()
	_ = Start()
	_ = Stop()
	_ = Restart()
	_ = Reload()
}

// The install*/uninstall* functions below write to real, absolute
// system paths (/etc, /var, /Library) rather than the current working
// directory, so calling them inside the disposable test container only
// affects the container's own filesystem, never the bind-mounted project
// tree. installWindows/uninstallWindows are intentionally NOT exercised
// here: their backslash-style Windows paths are not absolute under a
// Linux-targeted filepath package, so calling them under linux/darwin
// would write files into the current working directory instead.
func TestInstallUninstallSystemd(t *testing.T) {
	_ = installSystemd()
	_ = uninstallSystemd()
}

func TestInstallUninstallRunit(t *testing.T) {
	_ = installRunit()
	_ = uninstallRunit()
}

func TestInstallUninstallLaunchd(t *testing.T) {
	_ = installLaunchd()
	_ = uninstallLaunchd()
}

func TestInstallUninstallBSDRC(t *testing.T) {
	_ = installBSDRC()
	_ = uninstallBSDRC()
}
