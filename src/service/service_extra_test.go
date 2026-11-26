package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestServiceFilePathUserModeRejectsUnsupportedManagers(t *testing.T) {
	options := Options{Name: "citylist", UserMode: true}
	if _, err := serviceFilePath(options, ServiceRunit); err == nil {
		t.Fatal("user mode accepted runit, want unsupported-manager error")
	}
}

func TestBinaryDestinationUserMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix HOME destination contract")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	options := Options{Name: "citylist", Org: "apimgr", UserMode: true}
	want := filepath.Join(home, ".local", "bin", "citylist")
	if got := binaryDestination(options); got != want {
		t.Fatalf("binaryDestination() = %q, want %q", got, want)
	}
}

func TestEnsureDirectoriesSystemModeUsesPublicPermissions(t *testing.T) {
	root := t.TempDir()
	options := Options{
		ConfigDir: filepath.Join(root, "config"), DataDir: filepath.Join(root, "data"),
		DatabaseDir: filepath.Join(root, "database"), CacheDir: filepath.Join(root, "cache"),
		LogsDir: filepath.Join(root, "logs"), BackupDir: filepath.Join(root, "backup"),
	}
	if err := ensureDirectories(options); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are unavailable")
	}
	for _, path := range []string{options.ConfigDir, options.DataDir, options.DatabaseDir, options.CacheDir, options.LogsDir, options.BackupDir} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got&0700 != 0700 || got&^0755 != 0 {
			t.Errorf("mode for %s = %04o, want owner access without permissions beyond 0755", path, got)
		}
	}
}

func TestWriteExecutableReportsFileErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing", "service")
	if err := writeExecutable(missing, []byte("#!/bin/sh\n")); err == nil {
		t.Fatal("writeExecutable() succeeded with a missing parent directory")
	}
}

func TestRunPreservesCommandOutputAndStatus(t *testing.T) {
	binDir := t.TempDir()
	command := filepath.Join(binDir, "fixture-command")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nprintf 'fixture output\\n'\nexit 7\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	err := run("fixture-command", "argument")
	if err == nil {
		t.Fatal("run() succeeded for a command returning status 7")
	}
	for _, fragment := range []string{"fixture-command argument", "exit status 7", "fixture output"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("run() error %q does not contain %q", err, fragment)
		}
	}
}

func TestCommandOutputContains(t *testing.T) {
	binDir := t.TempDir()
	command := filepath.Join(binDir, "fixture-output")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nprintf 'citylist active\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	if !commandOutputContains("fixture-output", nil, "active") {
		t.Fatal("commandOutputContains() did not find matching output")
	}
	if commandOutputContains("fixture-output", nil, "missing") {
		t.Fatal("commandOutputContains() found absent text")
	}
	if commandOutputContains("not-installed", nil, "anything") {
		t.Fatal("commandOutputContains() accepted a missing command")
	}
}

func TestCopyBinaryRejectsUnsafeDestinations(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.WriteFile(source, []byte("binary"), 0700); err != nil {
		t.Fatal(err)
	}
	directoryDestination := filepath.Join(root, "directory")
	if err := os.Mkdir(directoryDestination, 0700); err != nil {
		t.Fatal(err)
	}
	if err := copyBinary(source, directoryDestination); err == nil {
		t.Fatal("copyBinary() replaced a directory destination")
	}
	if runtime.GOOS != "windows" {
		target := filepath.Join(root, "target")
		if err := os.WriteFile(target, []byte("preserve"), 0600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if err := copyBinary(source, link); err == nil {
			t.Fatal("copyBinary() followed a symlink destination")
		}
		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "preserve" {
			t.Fatalf("symlink target content = %q; want preserved content", data)
		}
	}
}

func TestEnableS6ServiceRejectsRegularFileAtLinkPath(t *testing.T) {
	root := t.TempDir()
	servicePath := filepath.Join(root, "definition")
	if err := os.Mkdir(servicePath, 0700); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(root, "scan", "citylist")
	if err := os.MkdirAll(filepath.Dir(linkPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(linkPath, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := enableS6Service(servicePath, linkPath); err == nil {
		t.Fatal("enableS6Service() accepted a regular file at its link path")
	}
}

func TestFindS6ScanDirectorySkipsUnrelatedEntries(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	for name, comm := range map[string]string{"not-a-pid": "s6-svscan", "123": "other-process"} {
		process := filepath.Join(proc, name)
		if err := os.MkdirAll(process, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(process, "comm"), []byte(comm+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if got, running := findS6ScanDirectory(proc); running || got != "/run/service" {
		t.Fatalf("findS6ScanDirectory() = %q, %t; want fallback and false", got, running)
	}
}

func TestGetPlatformUIDRejectsEmptyRange(t *testing.T) {
	if _, err := getPlatformUID(1, 2); err == nil {
		t.Fatal("getPlatformUID() accepted an empty range")
	}
}

func TestUninstallRequiresConfirmationIOAndConsent(t *testing.T) {
	root := t.TempDir()
	options := Options{
		Name: "citylist", Org: "apimgr",
		ConfigDir: filepath.Join(root, "config"), DataDir: filepath.Join(root, "data"),
		LogsDir: filepath.Join(root, "logs"),
	}
	if err := UninstallWithOptions(options, nil, &strings.Builder{}); err == nil {
		t.Fatal("UninstallWithOptions() accepted missing confirmation input")
	}
	if err := UninstallWithOptions(options, strings.NewReader("n\n"), &strings.Builder{}); err == nil || !strings.Contains(err.Error(), "cancel") {
		t.Fatalf("UninstallWithOptions() error = %v, want cancellation", err)
	}
	for _, path := range []string{options.ConfigDir, options.DataDir, options.LogsDir} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("unconfirmed uninstall changed %s: stat error = %v", path, err)
		}
	}
}

func TestUserModeUninstallDoesNotDeleteSystemAccount(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd user-service behavior is Linux-specific")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	serviceDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(serviceDir, 0700); err != nil {
		t.Fatal(err)
	}
	servicePath := filepath.Join(serviceDir, "citylist.service")
	if err := os.WriteFile(servicePath, []byte("[Service]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	callLog := filepath.Join(root, "calls")
	for _, name := range []string{"systemctl", "userdel", "groupdel", "deluser", "delgroup"} {
		script := "#!/bin/sh\nprintf '%s\\n' \"$0 $*\" >> \"" + callLog + "\"\n"
		if name == "systemctl" {
			script += "exit 0\n"
		} else {
			script += "exit 99\n"
		}
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir)
	options := Options{
		Name: "citylist", Org: "apimgr", UserMode: true,
		ConfigDir: filepath.Join(root, "config"), DataDir: filepath.Join(root, "data"),
		CacheDir: filepath.Join(root, "cache"), LogsDir: filepath.Join(root, "logs"),
		BackupDir: filepath.Join(root, "backup"),
	}
	for _, path := range []string{options.ConfigDir, options.DataDir, options.CacheDir, options.LogsDir, options.BackupDir} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := uninstallWithManager(options, strings.NewReader("yes\n"), &strings.Builder{}, ServiceSystemd); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(servicePath); !os.IsNotExist(err) {
		t.Errorf("user service definition remains after uninstall: %v", err)
	}
	if data, err := os.ReadFile(callLog); err == nil {
		calls := string(data)
		if strings.Contains(calls, "userdel") || strings.Contains(calls, "groupdel") || strings.Contains(calls, "deluser") || strings.Contains(calls, "delgroup") {
			t.Errorf("user-mode uninstall invoked system account deletion: %s", calls)
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestUninstallSkipsGroupAlreadyRemovedWithServiceUser(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("system account deletion behavior is Linux-specific")
	}
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	callLog := filepath.Join(root, "calls")
	for _, name := range []string{"userdel", "groupdel"} {
		script := "#!/bin/sh\nprintf '%s\\n' \"" + name + "\" >> \"" + callLog + "\"\nexit 0\n"
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir)
	options := Options{
		Name: "citylist-uninstall-group-test", Org: "apimgr",
		ConfigDir: filepath.Join(root, "config"), DataDir: filepath.Join(root, "data"),
		DatabaseDir: filepath.Join(root, "database"), CacheDir: filepath.Join(root, "cache"),
		LogsDir: filepath.Join(root, "logs"), BackupDir: filepath.Join(root, "backup"),
	}
	if err := uninstallWithManager(options, strings.NewReader("yes\n"), &strings.Builder{}, ServiceSystemd); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if string(calls) != "userdel\n" {
		t.Fatalf("account deletion calls = %q, want only userdel when the group is already absent", calls)
	}
}

func TestUserModeSystemdServiceLifecycle(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd user-service behavior is Linux-specific")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	systemctlLog := filepath.Join(root, "systemctl.log")
	systemctl := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + systemctlLog + "\"\ncase \"$*\" in *' show citylist '*) printf 'active\\nenabled\\n4242\\n' ;; esac\n"
	if err := os.WriteFile(filepath.Join(binDir, "systemctl"), []byte(systemctl), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"userdel", "groupdel", "deluser", "delgroup"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir)
	options := Options{
		Name: "citylist", Org: "apimgr", UserMode: true,
		ConfigDir: filepath.Join(root, "config"), DataDir: filepath.Join(root, "data"),
		DatabaseDir: filepath.Join(root, "database"), CacheDir: filepath.Join(root, "cache"),
		LogsDir: filepath.Join(root, "logs"), BackupDir: filepath.Join(root, "backup"),
		BinaryPath: filepath.Join(root, "citylist-source"),
	}
	if err := os.WriteFile(options.BinaryPath, []byte("fixture binary"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := installWithManager(options, ServiceSystemd); err != nil {
		t.Fatal(err)
	}
	servicePath := filepath.Join(home, ".config", "systemd", "user", "citylist.service")
	unit, err := os.ReadFile(servicePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"ProtectHome=no", "WantedBy=default.target"} {
		if !strings.Contains(string(unit), fragment) {
			t.Errorf("user unit is missing %q", fragment)
		}
	}
	state, err := stateWithManager(options, ServiceSystemd)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Installed || !state.Running || !state.Enabled || state.PID != "4242" {
		t.Errorf("user service state = %+v, want installed, running, enabled with PID 4242", state)
	}
	if err := disableWithManager(options, ServiceSystemd); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(servicePath); err != nil {
		t.Errorf("disable removed the service unit: %v", err)
	}
	if err := uninstallWithManager(options, strings.NewReader("yes\n"), &strings.Builder{}, ServiceSystemd); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(servicePath); !os.IsNotExist(err) {
		t.Errorf("uninstall left the service unit: %v", err)
	}
	if _, err := os.Stat(binaryDestination(options)); err != nil {
		t.Errorf("uninstall removed the installed binary: %v", err)
	}
	for _, directory := range []string{options.ConfigDir, options.DataDir, options.DatabaseDir, options.CacheDir, options.LogsDir, options.BackupDir} {
		if _, err := os.Stat(directory); !os.IsNotExist(err) {
			t.Errorf("uninstall left runtime directory %s: %v", directory, err)
		}
	}
	calls, err := os.ReadFile(systemctlLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
		if !strings.HasPrefix(line, "--user ") {
			t.Errorf("systemd user command omitted --user: %q", line)
		}
	}
}
