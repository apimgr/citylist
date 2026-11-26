package service

import (
	"errors"
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

func TestServiceTypeString(t *testing.T) {
	tests := []struct {
		service ServiceType
		want    string
	}{
		{ServiceUnknown, "unknown"},
		{ServiceSystemd, "systemd"},
		{ServiceOpenRC, "OpenRC"},
		{ServiceRunit, "runit"},
		{ServiceS6, "s6"},
		{ServiceSysV, "SysVinit"},
		{ServiceLaunchd, "launchd"},
		{ServiceWindows, "Windows Service"},
		{ServiceBSDRC, "rc.d"},
		{ServiceType(99), "unknown"},
	}
	for _, test := range tests {
		if got := test.service.String(); got != test.want {
			t.Errorf("ServiceType(%d).String() = %q, want %q", test.service, got, test.want)
		}
	}
}

func TestServiceStatusPIDParsing(t *testing.T) {
	for _, test := range []struct {
		name   string
		output string
		want   string
	}{
		{name: "runit", output: "run: /var/service/citylist: (pid 1234) 2s", want: "1234"},
		{name: "windows", output: "        PID                : 4321\n", want: "4321"},
		{name: "missing", output: "service is stopped", want: ""},
	} {
		if got := processPID(test.output); got != test.want {
			t.Errorf("processPID(%s) = %q, want %q", test.name, got, test.want)
		}
	}
	if got := launchdPID("PID\tStatus\tLabel\n1234\t0\tio.github.apimgr.citylist\n", "io.github.apimgr.citylist"); got != "1234" {
		t.Errorf("launchdPID() = %q, want 1234", got)
	}
}

func TestServiceLanguageNormalizationAndFallback(t *testing.T) {
	tests := []struct {
		name     string
		language string
		lcAll    string
		lang     string
		want     string
	}{
		{name: "normalizes explicit locale", language: " FR_fr.UTF-8@euro ", want: "fr"},
		{name: "uses LC_ALL", lcAll: "es_MX.UTF-8", lang: "de_DE", want: "es"},
		{name: "uses LANG when LC_ALL is empty", lang: "de_DE.UTF-8", want: "de"},
		{name: "unsupported language falls back", language: "zh_CN", want: "en"},
		{name: "empty environment falls back", want: "en"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("LC_ALL", test.lcAll)
			t.Setenv("LANG", test.lang)
			if got := serviceLanguage(test.language); got != test.want {
				t.Errorf("serviceLanguage(%q) = %q, want %q", test.language, got, test.want)
			}
		})
	}
}

func TestServiceErrorPreservesCause(t *testing.T) {
	cause := errors.New("underlying failure")
	err := serviceError("en", "service.errors.create_directory", map[string]string{"path": "/tmp/citylist"}, cause)
	if !errors.Is(err, cause) {
		t.Fatalf("serviceError() = %v; errors.Is does not preserve cause", err)
	}
	if !strings.Contains(err.Error(), "/tmp/citylist") || !strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("serviceError() = %q; want translated context and cause", err)
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

func TestNormalizeOptionsRequiresRuntimeDirectories(t *testing.T) {
	if _, err := normalizeOptions(Options{}); err == nil {
		t.Fatal("normalizeOptions accepted missing runtime directories")
	}
}

func TestNormalizeOptionsDefaultsAndBinaryPath(t *testing.T) {
	configDir := t.TempDir()
	dataDir := t.TempDir()
	logsDir := t.TempDir()
	options, err := normalizeOptions(Options{ConfigDir: configDir, DataDir: dataDir, LogsDir: logsDir})
	if err != nil {
		t.Fatal(err)
	}
	if options.Name != "citylist" || options.Org != "apimgr" {
		t.Fatalf("default identity = %s/%s", options.Org, options.Name)
	}
	if options.CacheDir != filepath.Join(dataDir, "cache") || options.BackupDir != filepath.Join(dataDir, "backup") || options.DatabaseDir != filepath.Join(dataDir, "db") {
		t.Fatalf("default runtime directories = cache:%s backup:%s database:%s", options.CacheDir, options.BackupDir, options.DatabaseDir)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if options.BinaryPath != binary {
		t.Fatalf("default binary path = %q, want %q", options.BinaryPath, binary)
	}
}

func TestServiceFilePathsMatchPlatformContracts(t *testing.T) {
	options := Options{Name: "citylist", Org: "apimgr"}
	for _, test := range []struct {
		manager ServiceType
		want    string
	}{{ServiceSystemd, "/etc/systemd/system/citylist.service"},
		{ServiceOpenRC, "/etc/init.d/citylist"},
		{ServiceSysV, "/etc/init.d/citylist"},
		{ServiceRunit, "/etc/sv/citylist"},
		{ServiceS6, "/etc/s6/sv/citylist"},
		{ServiceLaunchd, "/Library/LaunchDaemons/io.github.apimgr.citylist.plist"},
		{ServiceBSDRC, "/usr/local/etc/rc.d/citylist"},
		{ServiceWindows, "citylist"}} {
		got, err := serviceFilePath(options, test.manager)
		if err != nil || got != test.want {
			t.Errorf("serviceFilePath(%s) = %q, %v; want %q", test.manager, got, err, test.want)
		}
	}
	if _, err := serviceFilePath(options, ServiceUnknown); err == nil {
		t.Fatal("serviceFilePath accepted an unknown manager")
	}

	options.UserMode = true
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := serviceFilePath(options, ServiceLaunchd)
	want := filepath.Join(home, "Library", "LaunchAgents", "io.github.apimgr.citylist.plist")
	if err != nil || got != want {
		t.Fatalf("user launchd path = %q, %v; want %q", got, err, want)
	}
}

func TestEnsureDirectoriesUsesUserModePermissions(t *testing.T) {
	root := t.TempDir()
	options := Options{
		Name: "citylist", Org: "apimgr", UserMode: true,
		ConfigDir: filepath.Join(root, "config"), DataDir: filepath.Join(root, "data"),
		CacheDir: filepath.Join(root, "cache"), LogsDir: filepath.Join(root, "logs"),
		BackupDir: filepath.Join(root, "backup"),
	}
	if err := ensureDirectories(options); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{options.ConfigDir, options.DataDir, options.CacheDir, options.LogsDir, options.BackupDir} {
		info, err := os.Stat(directory)
		if err != nil {
			t.Errorf("stat %q: %v", directory, err)
			continue
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
			t.Errorf("directory %q mode = %04o, want 0700", directory, info.Mode().Perm())
		}
	}
}

func TestLaunchdPlistUsesStableLabelAndEscapesValues(t *testing.T) {
	options := Options{
		Name: "citylist", Org: "apimgr", BinaryPath: "/opt/city&list",
		ConfigDir: "/etc/apimgr/citylist", DataDir: "/var/lib/apimgr/citylist",
		LogsDir: "/var/log/apimgr/citylist",
	}
	content := launchdPlist(options)
	for _, value := range []string{
		"<string>io.github.apimgr.citylist</string>",
		"/opt/city&amp;list",
		"/etc/apimgr/citylist",
		"/var/lib/apimgr/citylist",
		"/var/log/apimgr/citylist/server.log",
		"/var/log/apimgr/citylist/error.log",
	} {
		if !strings.Contains(content, value) {
			t.Errorf("launchd plist omitted %q", value)
		}
	}
	if strings.Contains(content, "<string>apimgr.citylist</string>") {
		t.Fatal("launchd plist used the non-canonical service label")
	}
}

func TestWriteExecutableCreatesExecutableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service")
	content := []byte("#!/bin/sh\nexit 0\n")
	if err := writeExecutable(path, content); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("service content = %q, want %q", got, content)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0755 {
			t.Fatalf("service mode = %04o, want 0755", info.Mode().Perm())
		}
	}
}

func TestXMLValuesAreEscaped(t *testing.T) {
	if got, want := xml(`<script a="x">Tom & 'Jerry'</script>`), "&lt;script a=&quot;x&quot;&gt;Tom &amp; &apos;Jerry&apos;&lt;/script&gt;"; got != want {
		t.Fatalf("xml() = %q, want %q", got, want)
	}
}

func TestFindS6ScanDirectory(t *testing.T) {
	root := t.TempDir()
	proc := filepath.Join(root, "proc")
	scanDir := filepath.Join(root, "service-scan")
	process := filepath.Join(proc, "1234")
	if err := os.MkdirAll(process, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(process, "comm"), []byte("s6-svscan\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(process, "cmdline"), []byte("s6-svscan\x00"+scanDir+"\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, running := findS6ScanDirectory(proc); !running || got != scanDir {
		t.Fatalf("findS6ScanDirectory() = %q, %t; want %q, true", got, running, scanDir)
	}
	if err := os.Remove(filepath.Join(process, "cmdline")); err != nil {
		t.Fatal(err)
	}
	if got, running := findS6ScanDirectory(proc); !running || got != "/run/service" {
		t.Fatalf("findS6ScanDirectory() without cmdline = %q, %t; want /run/service, true", got, running)
	}
	if got, running := findS6ScanDirectory(filepath.Join(root, "missing-proc")); running || got != "/run/service" {
		t.Fatalf("findS6ScanDirectory() without proc = %q, %t; want /run/service, false", got, running)
	}
}

func TestCreateAndEnableS6Definition(t *testing.T) {
	root := t.TempDir()
	servicePath := filepath.Join(root, "definition", "citylist")
	scanDir := filepath.Join(root, "scan")
	options := Options{
		Name: "citylist", BinaryPath: "/usr/local/bin/citylist",
		ConfigDir: "/etc/apimgr/citylist", DataDir: "/var/lib/apimgr/citylist",
		LogsDir: "/var/log/apimgr/citylist",
	}
	if err := createS6Definition(options, servicePath); err != nil {
		t.Fatal(err)
	}
	runScript, err := os.ReadFile(filepath.Join(servicePath, "run"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(runScript), `exec "/usr/local/bin/citylist"`) {
		t.Fatalf("s6 run script = %q; missing exec of configured binary", runScript)
	}
	logScript, err := os.ReadFile(filepath.Join(servicePath, "log", "run"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logScript), `s6-log n10 s1000000 "/var/log/apimgr/citylist"`) {
		t.Fatalf("s6 log script = %q; missing configured log directory", logScript)
	}
	linkPath := filepath.Join(scanDir, options.Name)
	if err := enableS6Service(servicePath, linkPath); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(linkPath)
	if err != nil || target != servicePath {
		t.Fatalf("s6 service link target = %q, %v; want %q", target, err, servicePath)
	}
	if err := enableS6Service(servicePath, linkPath); err != nil {
		t.Fatalf("enabling the same service twice should be idempotent: %v", err)
	}
	other := filepath.Join(root, "other")
	if err := os.MkdirAll(other, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(linkPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, linkPath); err != nil {
		t.Fatal(err)
	}
	if err := enableS6Service(servicePath, linkPath); err == nil {
		t.Fatal("enabling an existing unrelated link succeeded")
	}
}

func TestIsNumeric(t *testing.T) {
	for value, want := range map[string]bool{"": false, "1234": true, "12a4": false, "-12": false} {
		if got := isNumeric(value); got != want {
			t.Errorf("isNumeric(%q) = %t, want %t", value, got, want)
		}
	}
}

func TestServiceInstallerTemplatesUseConfiguredPathsAndCommands(t *testing.T) {
	for _, test := range []struct {
		name      string
		manager   ServiceType
		userMode  bool
		install   func(Options, string) error
		commands  []string
		fragments []string
	}{
		{name: "systemd", manager: ServiceSystemd, install: installSystemd, commands: []string{"systemctl daemon-reload", "systemctl enable citylist", "systemctl start citylist"}, fragments: []string{"ExecStart=/tmp/citylist --config", "Documentation=https://apimgr.github.io/citylist", "ProtectSystem=true", "ProtectHome=yes", "AmbientCapabilities=CAP_NET_BIND_SERVICE", "CapabilityBoundingSet=CAP_CHOWN CAP_DAC_OVERRIDE CAP_FOWNER CAP_NET_BIND_SERVICE CAP_SETGID CAP_SETUID", "ExecStartPre=+/usr/bin/install -d -m 0755", "WantedBy=multi-user.target", "ReadWritePaths=-"}},
		{name: "systemd-user", manager: ServiceSystemd, userMode: true, install: installSystemd, commands: []string{"systemctl --user daemon-reload", "systemctl --user enable citylist", "systemctl --user start citylist"}, fragments: []string{"ProtectHome=no", "ExecStartPre=/usr/bin/install -d -m 0700", "WantedBy=default.target"}},
		{name: "openrc", manager: ServiceOpenRC, install: installOpenRC, commands: []string{"rc-update add citylist default", "rc-service citylist start"}, fragments: []string{"#!/sbin/openrc-run", "command_background=true", "pidfile=\"/var/run/apimgr-citylist.pid\"", "checkpath -d -m 0755", "start-stop-daemon --stop --quiet --pidfile", `if [ "$executable" != "$command" ]`, `if [ "$state" = "Z" ]`}},
		{name: "sysv", manager: ServiceSysV, install: installSysV, commands: []string{"update-rc.d citylist defaults", "install -d", "chown -R", "start-stop-daemon --start"}, fragments: []string{"### BEGIN INIT INFO", "DAEMON_USER=citylist", "PIDFILE=/var/run/apimgr-citylist.pid", "start-stop-daemon --start"}},
		{name: "launchd", manager: ServiceLaunchd, install: installLaunchd, commands: []string{"launchctl load"}, fragments: []string{"io.github.apimgr.citylist", "/tmp/citylist"}},
		{name: "windows", manager: ServiceWindows, install: func(options Options, _ string) error { return installWindows(options) }, commands: []string{"icacls.exe", "NT SERVICE\\citylist:(OI)(CI)F", "sc.exe create citylist", "sc.exe config citylist", "sc.exe start citylist"}, fragments: nil},
		{name: "bsd", manager: ServiceBSDRC, install: installBSD, commands: []string{"sysrc citylist_enable=YES", "service citylist start"}, fragments: []string{"# PROVIDE: citylist", "name=\"citylist\"", "rcvar=\"citylist_enable\"", "pidfile=\"/var/run/apimgr-citylist.pid\""}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			binDir := filepath.Join(root, "bin")
			if err := os.MkdirAll(binDir, 0755); err != nil {
				t.Fatal(err)
			}
			calls := filepath.Join(root, "calls.log")
			t.Setenv("SERVICE_TEST_CALLS", calls)
			commandNames := map[string]bool{"systemctl": true, "rc-update": true, "rc-service": true, "update-rc.d": true, "install": true, "chown": true, "start-stop-daemon": true, "launchctl": true, "sc.exe": true, "icacls.exe": true, "sysrc": true, "service": true, "dirname": true}
			for name := range commandNames {
				script := "#!/bin/sh\nprintf '%s\\n' \"$0 $*\" >> \"$SERVICE_TEST_CALLS\"\n"
				switch name {
				case "install":
					script += "/usr/bin/install \"$@\"\n"
				case "dirname":
					script += "printf '%s\\n' \"${1%/*}\"\n"
				}
				if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", binDir)
			options := Options{
				Name: "citylist", Org: "apimgr", BinaryPath: filepath.Join(root, "citylist"),
				ConfigDir: filepath.Join(root, "config"), DataDir: filepath.Join(root, "data"),
				DatabaseDir: filepath.Join(root, "database"), CacheDir: filepath.Join(root, "cache"), LogsDir: filepath.Join(root, "logs"),
				BackupDir: filepath.Join(root, "backup"),
				UserMode:  test.userMode,
			}
			servicePath := filepath.Join(root, "service")
			if err := test.install(options, servicePath); err != nil {
				t.Fatalf("install %s: %v", test.manager, err)
			}
			if test.manager != ServiceWindows {
				data, err := os.ReadFile(servicePath)
				if err != nil {
					t.Fatalf("read %s definition: %v", test.manager, err)
				}
				for _, fragment := range test.fragments {
					fragment = strings.ReplaceAll(fragment, "/tmp/citylist", options.BinaryPath)
					if !strings.Contains(string(data), fragment) {
						t.Errorf("%s definition omitted %q", test.manager, fragment)
					}
				}
				if test.manager == ServiceSystemd {
					for _, directory := range []string{options.ConfigDir, options.DataDir, options.DatabaseDir, options.CacheDir, options.LogsDir, filepath.Join("/var/run", options.Org, options.Name), options.BackupDir} {
						if !strings.Contains(string(data), "ReadWritePaths=-"+directory) {
							t.Errorf("systemd definition omitted writable path %q", directory)
						}
					}
					if test.userMode && (strings.Contains(string(data), "ProtectHome=yes") || strings.Contains(string(data), "ExecStartPre=+") || strings.Contains(string(data), "AmbientCapabilities=") || strings.Contains(string(data), "ReadWritePaths=-/usr/local/bin")) {
						t.Errorf("user service contains system-only hardening or update settings: %s", data)
					}
				}
				if test.manager == ServiceSysV && strings.Contains(string(data), `chown -R "$DAEMON_USER:$DAEMON_USER" "$(dirname "$PIDFILE")"`) {
					t.Error("SysV definition recursively changes ownership of the supervisor PID directory")
				}
			}
			callsData, err := os.ReadFile(calls)
			if err != nil {
				t.Fatalf("read %s command log: %v", test.manager, err)
			}
			for _, command := range test.commands {
				if !strings.Contains(string(callsData), command) {
					t.Errorf("%s did not run %q: %s", test.manager, command, callsData)
				}
			}
		})
	}
}
