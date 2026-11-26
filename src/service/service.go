package service

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/apimgr/citylist/src/common/i18n"
)

const (
	ServiceUnknown ServiceType = iota
	ServiceSystemd
	ServiceOpenRC
	ServiceRunit
	ServiceS6
	ServiceSysV
	ServiceLaunchd
	ServiceWindows
	ServiceBSDRC
)

type ServiceType int

type Options struct {
	Name        string
	Org         string
	ConfigDir   string
	DataDir     string
	DatabaseDir string
	CacheDir    string
	LogsDir     string
	BackupDir   string
	BinaryPath  string
	UserMode    bool
	Language    string
}

type State struct {
	Installed bool
	Running   bool
	Enabled   bool
	PID       string
	Manager   string
}

func (t ServiceType) String() string {
	switch t {
	case ServiceSystemd:
		return "systemd"
	case ServiceOpenRC:
		return "OpenRC"
	case ServiceRunit:
		return "runit"
	case ServiceS6:
		return "s6"
	case ServiceSysV:
		return "SysVinit"
	case ServiceLaunchd:
		return "launchd"
	case ServiceWindows:
		return "Windows Service"
	case ServiceBSDRC:
		return "rc.d"
	default:
		return "unknown"
	}
}

func normalizeOptions(options Options) (Options, error) {
	options.Language = serviceLanguage(options.Language)
	if options.Name == "" {
		options.Name = "citylist"
	}
	if options.Org == "" {
		options.Org = "apimgr"
	}
	if options.ConfigDir == "" || options.DataDir == "" || options.LogsDir == "" {
		return Options{}, errors.New(serviceMessage(options.Language, "service.errors.runtime_directories_required"))
	}
	if options.CacheDir == "" {
		options.CacheDir = filepath.Join(options.DataDir, "cache")
	}
	if options.DatabaseDir == "" {
		options.DatabaseDir = filepath.Join(options.DataDir, "db")
	}
	if options.BackupDir == "" {
		options.BackupDir = filepath.Join(options.DataDir, "backup")
	}
	if options.BinaryPath == "" {
		binary, err := os.Executable()
		if err != nil {
			return Options{}, serviceError(options.Language, "service.errors.resolve_executable", nil, err)
		}
		options.BinaryPath = binary
	}
	return options, nil
}

func serviceLanguage(value string) string {
	if strings.TrimSpace(value) == "" {
		value = os.Getenv("LC_ALL")
		if value == "" {
			value = os.Getenv("LANG")
		}
	}
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.SplitN(value, ".", 2)[0]
	value = strings.SplitN(value, "@", 2)[0]
	value = strings.SplitN(value, "_", 2)[0]
	if !i18n.IsSupported(value) {
		return "en"
	}
	return value
}

func serviceMessage(language, key string) string {
	return i18n.Translate(serviceLanguage(language), key)
}

func serviceMessageFormat(language, key string, values map[string]string) string {
	return i18n.TranslateFormat(serviceLanguage(language), key, values)
}

func serviceError(language, key string, values map[string]string, cause error) error {
	message := serviceMessageFormat(language, key, values)
	if cause == nil {
		return errors.New(message)
	}
	return fmt.Errorf("%s: %w", message, cause)
}

func DetectServiceManager() ServiceType {
	switch runtime.GOOS {
	case "linux":
		if _, err := os.Stat("/run/systemd/system"); err == nil {
			return ServiceSystemd
		}
		if _, err := os.Stat("/sbin/openrc-run"); err == nil {
			if _, err := os.Stat("/run/openrc"); err == nil {
				return ServiceOpenRC
			}
		}
		if pathExists("/etc/runit") || commandExists("sv") {
			return ServiceRunit
		}
		if commandExists("s6-svc") {
			if _, running := findS6ScanDirectory("/proc"); running {
				return ServiceS6
			}
		}
		if pathExists("/etc/init.d") && (commandExists("update-rc.d") || commandExists("chkconfig")) {
			return ServiceSysV
		}
	case "darwin":
		return ServiceLaunchd
	case "windows":
		return ServiceWindows
	case "freebsd", "openbsd", "netbsd", "dragonfly":
		return ServiceBSDRC
	}
	return ServiceUnknown
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func binaryDestination(options Options) string {
	if options.UserMode {
		if runtime.GOOS == "windows" {
			return filepath.Join(os.Getenv("LOCALAPPDATA"), options.Org, options.Name, options.Name+".exe")
		}
		return filepath.Join(os.Getenv("HOME"), ".local", "bin", options.Name)
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("ProgramFiles"), options.Org, options.Name, options.Name+".exe")
	}
	return filepath.Join("/usr/local/bin", options.Name)
}

func serviceFilePath(options Options, manager ServiceType) (string, error) {
	if options.UserMode {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		switch manager {
		case ServiceSystemd:
			return filepath.Join(home, ".config", "systemd", "user", options.Name+".service"), nil
		case ServiceLaunchd:
			return filepath.Join(home, "Library", "LaunchAgents", launchdLabel(options)+".plist"), nil
		default:
			return "", errors.New(serviceMessageFormat(options.Language, "service.errors.user_mode_unsupported", map[string]string{"manager": manager.String()}))
		}
	}
	switch manager {
	case ServiceSystemd:
		return filepath.Join("/etc/systemd/system", options.Name+".service"), nil
	case ServiceOpenRC, ServiceSysV:
		return filepath.Join("/etc/init.d", options.Name), nil
	case ServiceRunit:
		return filepath.Join("/etc/sv", options.Name), nil
	case ServiceS6:
		return filepath.Join("/etc/s6/sv", options.Name), nil
	case ServiceLaunchd:
		return filepath.Join("/Library/LaunchDaemons", launchdLabel(options)+".plist"), nil
	case ServiceWindows:
		return options.Name, nil
	case ServiceBSDRC:
		return filepath.Join("/usr/local/etc/rc.d", options.Name), nil
	default:
		return "", errors.New(serviceMessage(options.Language, "service.errors.manager_unsupported"))
	}
}

func supervisorPIDPath(options Options) string {
	return filepath.Join("/var/run", options.Org+"-"+options.Name+".pid")
}

func ensureDirectories(options Options) error {
	mode := os.FileMode(0755)
	if options.UserMode {
		mode = 0700
	}
	for _, directory := range []string{options.ConfigDir, options.DataDir, options.DatabaseDir, options.CacheDir, options.LogsDir, options.BackupDir} {
		if directory == "" {
			continue
		}
		if err := os.MkdirAll(directory, mode); err != nil {
			return serviceError(options.Language, "service.errors.create_directory", map[string]string{"path": directory}, err)
		}
	}
	return nil
}

// PrepareSystemRuntime creates the service identity and owned runtime directories before privilege drop.
func PrepareSystemRuntime(raw Options, databaseDir string) error {
	options, err := normalizeOptions(raw)
	if err != nil {
		return err
	}
	if os.Geteuid() != 0 || runtime.GOOS == "windows" {
		return errors.New(serviceMessage(options.Language, "service.errors.privileged_unix_required"))
	}
	if err := ensureServiceAccount(options); err != nil {
		return serviceError(options.Language, "service.errors.prepare_account", nil, err)
	}
	general := []string{
		options.ConfigDir, options.DataDir, filepath.Join(options.DataDir, "db"),
		options.CacheDir, options.LogsDir, options.BackupDir, filepath.Join("/var/run", options.Org, options.Name),
	}
	if databaseDir != "" {
		general = append(general, databaseDir)
	}
	private := []string{filepath.Join(options.DataDir, "security"), filepath.Join(options.ConfigDir, "assets"), filepath.Join(options.ConfigDir, "ssl"), filepath.Join(options.ConfigDir, "tor"), filepath.Join(options.ConfigDir, "i2p"), filepath.Join(options.ConfigDir, "yaml.d"), filepath.Join(options.DataDir, "tor"), filepath.Join(options.DataDir, "i2p")}
	for _, directory := range general {
		if err := os.MkdirAll(directory, 0755); err != nil {
			return serviceError(options.Language, "service.errors.create_directory", map[string]string{"path": directory}, err)
		}
		if err := os.Chmod(directory, 0755); err != nil {
			return serviceError(options.Language, "service.errors.set_permissions", map[string]string{"path": directory}, err)
		}
	}
	for _, directory := range private {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return serviceError(options.Language, "service.errors.create_private_directory", map[string]string{"path": directory}, err)
		}
		if err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			mode := os.FileMode(0600)
			if entry.IsDir() {
				mode = 0700
			}
			return os.Chmod(path, mode)
		}); err != nil {
			return serviceError(options.Language, "service.errors.secure_private_directory", map[string]string{"path": directory}, err)
		}
	}
	for _, file := range []string{filepath.Join(options.ConfigDir, "server.yml"), filepath.Join(options.ConfigDir, "secrets.yml")} {
		if _, err := os.Stat(file); err == nil {
			if err := os.Chmod(file, 0600); err != nil {
				return serviceError(options.Language, "service.errors.secure_private_file", map[string]string{"path": file}, err)
			}
		} else if !os.IsNotExist(err) {
			return serviceError(options.Language, "service.errors.inspect_private_file", map[string]string{"path": file}, err)
		}
	}
	ownedDirectories := append(general, private...)
	for _, directory := range ownedDirectories {
		if err := run("chown", "-R", options.Name+":"+options.Name, directory); err != nil {
			return serviceError(options.Language, "service.errors.set_ownership", map[string]string{"path": directory}, err)
		}
	}
	return nil
}

func Install() error {
	return InstallWithOptions(Options{})
}

func InstallWithOptions(raw Options) error {
	return installWithManager(raw, DetectServiceManager())
}

func installWithManager(raw Options, manager ServiceType) error {
	options, err := normalizeOptions(raw)
	if err != nil {
		return err
	}
	if os.Geteuid() != 0 && runtime.GOOS != "windows" {
		options.UserMode = true
		switch runtime.GOOS {
		case "linux":
			runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
			if runtimeDir == "" {
				return errors.New(serviceMessage(options.Language, "service.errors.user_manager_missing"))
			}
			if _, err := os.Stat(filepath.Join(runtimeDir, "systemd", "private")); err != nil {
				return errors.New(serviceMessage(options.Language, "service.errors.user_manager_missing"))
			}
			manager = ServiceSystemd
		case "darwin":
			manager = ServiceLaunchd
		default:
			return errors.New(serviceMessage(options.Language, "service.errors.user_install_unsupported"))
		}
	}
	if manager == ServiceUnknown {
		return errors.New(serviceMessage(options.Language, "service.errors.manager_not_detected"))
	}
	if err := ensureDirectories(options); err != nil {
		return err
	}
	if !options.UserMode && os.Geteuid() == 0 {
		if err := ensureServiceAccount(options); err != nil {
			return err
		}
		for _, directory := range []string{options.ConfigDir, options.DataDir, options.DatabaseDir, options.CacheDir, options.LogsDir, options.BackupDir} {
			if err := run("chown", "-R", options.Name+":"+options.Name, directory); err != nil {
				return err
			}
		}
	}
	destination := binaryDestination(options)
	if err := copyBinary(options.BinaryPath, destination, options.Language); err != nil {
		return serviceError(options.Language, "service.errors.install_binary", nil, err)
	}
	options.BinaryPath = destination
	servicePath, err := serviceFilePath(options, manager)
	if err != nil {
		return err
	}
	if manager != ServiceWindows {
		if err := os.MkdirAll(filepath.Dir(servicePath), 0755); err != nil {
			return serviceError(options.Language, "service.errors.create_definition_directory", nil, err)
		}
	}
	switch manager {
	case ServiceSystemd:
		err = installSystemd(options, servicePath)
	case ServiceOpenRC:
		err = installOpenRC(options, servicePath)
	case ServiceRunit:
		err = installRunit(options, servicePath)
	case ServiceS6:
		err = installS6(options, servicePath)
	case ServiceSysV:
		err = installSysV(options, servicePath)
	case ServiceLaunchd:
		err = installLaunchd(options, servicePath)
	case ServiceWindows:
		err = installWindows(options)
	case ServiceBSDRC:
		err = installBSD(options, servicePath)
	}
	if err != nil {
		return err
	}
	return nil
}

func installSystemd(options Options, servicePath string) error {
	runtimeDir := filepath.Join("/var/run", options.Org, options.Name)
	writableDirs := []string{options.ConfigDir, options.DataDir, options.DatabaseDir, options.CacheDir, options.LogsDir, runtimeDir, options.BackupDir}
	uniqueDirs := make([]string, 0, len(writableDirs))
	seen := make(map[string]struct{}, len(writableDirs))
	for _, directory := range writableDirs {
		if directory == "" {
			continue
		}
		if _, ok := seen[directory]; ok {
			continue
		}
		seen[directory] = struct{}{}
		uniqueDirs = append(uniqueDirs, directory)
	}
	mode := "0755"
	prestart := "+"
	protectHome := "yes"
	installTarget := "multi-user.target"
	capabilities := "AmbientCapabilities=CAP_NET_BIND_SERVICE\nCapabilityBoundingSet=CAP_CHOWN CAP_DAC_OVERRIDE CAP_FOWNER CAP_NET_BIND_SERVICE CAP_SETGID CAP_SETUID\n"
	if options.UserMode {
		mode = "0700"
		prestart = ""
		protectHome = "no"
		installTarget = "default.target"
		capabilities = ""
	}
	readWritePaths := make([]string, 0, len(uniqueDirs)+1)
	for _, directory := range uniqueDirs {
		readWritePaths = append(readWritePaths, "ReadWritePaths=-"+directory)
	}
	if !options.UserMode {
		readWritePaths = append(readWritePaths, "ReadWritePaths=-/usr/local/bin")
	}
	service := fmt.Sprintf(`[Unit]
Description=%s
Documentation=https://%s.github.io/%s
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s --config %s --data %s --log %s
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal
%sProtectSystem=true
ProtectHome=%s
PrivateTmp=yes
ExecStartPre=%s/usr/bin/install -d -m %s %s
%s

[Install]
WantedBy=%s
`, serviceMessageFormat(options.Language, "service.unit_description", map[string]string{"name": options.Name}), options.Org, options.Name, options.BinaryPath, options.ConfigDir, options.DataDir, options.LogsDir, capabilities, protectHome, prestart, mode, strings.Join(uniqueDirs, " "), strings.Join(readWritePaths, "\n"), installTarget)
	if err := os.WriteFile(servicePath, []byte(service), 0644); err != nil {
		return serviceError(options.Language, "service.errors.write_systemd_unit", nil, err)
	}
	if err := run("systemctl", userSystemctlArgs(options, "daemon-reload")...); err != nil {
		return err
	}
	if options.UserMode {
		if err := run("systemctl", "--user", "enable", options.Name); err != nil {
			return err
		}
		return run("systemctl", "--user", "start", options.Name)
	}
	if err := run("systemctl", "enable", options.Name); err != nil {
		return err
	}
	return run("systemctl", "start", options.Name)
}

func userSystemctlArgs(options Options, args ...string) []string {
	if options.UserMode {
		return append([]string{"--user"}, args...)
	}
	return args
}

func installOpenRC(options Options, servicePath string) error {
	content := fmt.Sprintf(`#!/sbin/openrc-run
name=%q
description=%q
command=%q
command_args=%q
command_background=true
pidfile=%q
output_log=%q
error_log=%q

depend() {
    need net
    after firewall
    use dns logger
}

start_pre() {
    checkpath -d -m 0755 %q
    checkpath -d -m 0755 %q
    checkpath -d -m 0755 -o %s:%s %q
    checkpath -d -m 0755 -o %s:%s %q
}

stop() {
    local pid result state executable i
    pid=""
    if [ -r "$pidfile" ]; then
        pid=$(cat "$pidfile")
    fi
    case "$pid" in
        ''|*[!0-9]*) rm -f "$pidfile"; return 0 ;;
    esac
    if [ ! -e "/proc/$pid" ]; then
        rm -f "$pidfile"
        return 0
    fi
    executable=$(readlink "/proc/$pid/exe" 2>/dev/null)
    if [ "$executable" != "$command" ]; then
        eerror "Refusing to stop unexpected process $pid"
        return 1
    fi
    ebegin "Stopping $name"
    start-stop-daemon --stop --quiet --pidfile "$pidfile" --retry TERM/30/KILL/5
    result=$?
    for i in $(seq 1 35); do
        if [ ! -e "/proc/$pid" ]; then
            rm -f "$pidfile"
            eend 0
            return 0
        fi
        state=$(awk '{print $3}' "/proc/$pid/stat" 2>/dev/null)
        if [ "$state" = "Z" ]; then
            rm -f "$pidfile"
            eend 0
            return 0
        fi
        sleep 1
    done
    if [ ! -e "/proc/$pid" ]; then
        rm -f "$pidfile"
        eend 0
        return 0
    fi
    eend 1
    return 1
}
	`, options.Name, serviceMessageFormat(options.Language, "service.unit_description", map[string]string{"name": options.Name}), options.BinaryPath, fmt.Sprintf("--config %q --data %q --log %q", options.ConfigDir, options.DataDir, options.LogsDir), supervisorPIDPath(options), filepath.Join(options.LogsDir, "server.log"), filepath.Join(options.LogsDir, "error.log"), filepath.Join("/var/run", options.Org), filepath.Join("/var/log", options.Org), options.Name, options.Name, filepath.Join("/var/run", options.Org, options.Name), options.Name, options.Name, filepath.Join("/var/log", options.Org, options.Name))
	if err := writeExecutable(servicePath, []byte(content), options.Language); err != nil {
		return err
	}
	if err := run("rc-update", "add", options.Name, "default"); err != nil {
		return err
	}
	return run("rc-service", options.Name, "start")
}

func installSysV(options Options, servicePath string) error {
	content := fmt.Sprintf(`#!/bin/sh
### BEGIN INIT INFO
# Provides:          %s
# Required-Start:    $network $remote_fs $syslog
# Required-Stop:     $network $remote_fs $syslog
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: %s
### END INIT INFO
NAME=%s
DAEMON=%s
DAEMON_USER=%s
PIDFILE=%s
LOGFILE=%s
case "$1" in
  start) install -d -m 0755 "$(dirname "$PIDFILE")" "$(dirname "$LOGFILE")"; chown -R "$DAEMON_USER:$DAEMON_USER" "$(dirname "$LOGFILE")"; start-stop-daemon --start --quiet --background --make-pidfile --pidfile "$PIDFILE" --exec "$DAEMON" --no-close -- --config %q --data %q --log %q >>"$LOGFILE" 2>&1 ;;
  stop) start-stop-daemon --stop --quiet --pidfile "$PIDFILE" --retry 30; rm -f "$PIDFILE" ;;
  restart) "$0" stop && sleep 1 && "$0" start ;;
  reload) kill -HUP "$(cat "$PIDFILE")" ;;
  status) if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then echo %q; else echo %q; exit 3; fi ;;
  *) echo %q; exit 1 ;;
esac
	`, options.Name, serviceMessageFormat(options.Language, "service.unit_description", map[string]string{"name": options.Name}), options.Name, options.BinaryPath, options.Name, supervisorPIDPath(options), filepath.Join(options.LogsDir, "server.log"), options.ConfigDir, options.DataDir, options.LogsDir,
		serviceMessageFormat(options.Language, "service.script.running", map[string]string{"name": options.Name}),
		serviceMessageFormat(options.Language, "service.script.stopped", map[string]string{"name": options.Name}),
		serviceMessageFormat(options.Language, "service.script.usage", map[string]string{"script": "$0", "commands": "{start|stop|restart|reload|status}"}))
	if err := writeExecutable(servicePath, []byte(content), options.Language); err != nil {
		return err
	}
	if commandExists("update-rc.d") {
		if err := run("update-rc.d", options.Name, "defaults"); err != nil {
			return err
		}
		return run(servicePath, "start")
	}
	if err := run("chkconfig", "--add", options.Name); err != nil {
		return err
	}
	if err := run("chkconfig", options.Name, "on"); err != nil {
		return err
	}
	return run(servicePath, "start")
}

func installRunit(options Options, servicePath string) error {
	logDir := filepath.Join(options.LogsDir, "runit")
	if err := os.MkdirAll(filepath.Join(servicePath, "log"), 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}
	runScript := fmt.Sprintf("#!/bin/sh\nexec %q --config %q --data %q --log %q 2>&1\n", options.BinaryPath, options.ConfigDir, options.DataDir, options.LogsDir)
	if err := writeExecutable(filepath.Join(servicePath, "run"), []byte(runScript), options.Language); err != nil {
		return err
	}
	logScript := fmt.Sprintf("#!/bin/sh\nexec svlogd -tt %q\n", logDir)
	if err := writeExecutable(filepath.Join(servicePath, "log", "run"), []byte(logScript), options.Language); err != nil {
		return err
	}
	linkPath := filepath.Join("/var/service", options.Name)
	if err := os.Symlink(servicePath, linkPath); err != nil && !errors.Is(err, os.ErrExist) {
		return serviceError(options.Language, "service.errors.enable_service", nil, err)
	}
	return run("sv", "up", linkPath)
}

func installS6(options Options, servicePath string) error {
	if err := createS6Definition(options, servicePath); err != nil {
		return err
	}
	scanDir, running := findS6ScanDirectory("/proc")
	if !running {
		return errors.New(serviceMessage(options.Language, "service.errors.s6_not_running"))
	}
	linkPath := filepath.Join(scanDir, options.Name)
	if err := enableS6Service(servicePath, linkPath, options.Language); err != nil {
		return err
	}
	if err := run("s6-svscanctl", "-a", scanDir); err != nil {
		return err
	}
	return run("s6-svc", "-u", linkPath)
}

func createS6Definition(options Options, servicePath string) error {
	if err := os.MkdirAll(filepath.Join(servicePath, "log"), 0755); err != nil {
		return err
	}
	content := fmt.Sprintf("#!/bin/sh\nexec %q --config %q --data %q --log %q 2>&1\n", options.BinaryPath, options.ConfigDir, options.DataDir, options.LogsDir)
	if err := writeExecutable(filepath.Join(servicePath, "run"), []byte(content), options.Language); err != nil {
		return err
	}
	logContent := fmt.Sprintf("#!/bin/sh\nexec s6-log n10 s1000000 %q\n", options.LogsDir)
	return writeExecutable(filepath.Join(servicePath, "log", "run"), []byte(logContent), options.Language)
}

func enableS6Service(servicePath, linkPath string, languages ...string) error {
	language := "en"
	if len(languages) > 0 {
		language = serviceLanguage(languages[0])
	}
	if err := os.MkdirAll(filepath.Dir(linkPath), 0755); err != nil {
		return err
	}
	if target, err := os.Readlink(linkPath); err == nil {
		resolvedTarget, targetErr := filepath.EvalSymlinks(target)
		resolvedService, serviceErr := filepath.EvalSymlinks(servicePath)
		if targetErr == nil && serviceErr == nil && resolvedTarget == resolvedService {
			return nil
		}
		return errors.New(serviceMessageFormat(language, "service.errors.s6_link_conflict", map[string]string{"path": linkPath}))
	} else if !errors.Is(err, os.ErrNotExist) {
		return serviceError(language, "service.errors.inspect_s6_link", nil, err)
	}
	if err := os.Symlink(servicePath, linkPath); err != nil {
		return serviceError(language, "service.errors.enable_service", nil, err)
	}
	return nil
}

func findS6ScanDirectory(procRoot string) (string, bool) {
	fallback := "/run/service"
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return fallback, false
	}
	for _, entry := range entries {
		if !entry.IsDir() || !isNumeric(entry.Name()) {
			continue
		}
		processDir := filepath.Join(procRoot, entry.Name())
		comm, err := os.ReadFile(filepath.Join(processDir, "comm"))
		if err != nil || strings.TrimSpace(string(comm)) != "s6-svscan" {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join(processDir, "cmdline"))
		if err != nil {
			return fallback, true
		}
		args := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
		if len(args) > 1 && args[1] != "" {
			return args[1], true
		}
		return fallback, true
	}
	return fallback, false
}

func isNumeric(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func removeS6ServiceLink(options Options) error {
	scanDir, running := findS6ScanDirectory("/proc")
	if !running {
		return errors.New(serviceMessage(options.Language, "service.errors.s6_not_running"))
	}
	linkPath := filepath.Join(scanDir, options.Name)
	if info, err := os.Lstat(linkPath); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return errors.New(serviceMessageFormat(options.Language, "service.errors.s6_path_not_symlink", map[string]string{"path": linkPath}))
		}
		if err := os.Remove(linkPath); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return run("s6-svscanctl", "-a", scanDir)
}

func s6ServiceLink(options Options) (string, error) {
	scanDir, running := findS6ScanDirectory("/proc")
	if !running {
		return "", errors.New(serviceMessage(options.Language, "service.errors.s6_not_running"))
	}
	linkPath := filepath.Join(scanDir, options.Name)
	if _, err := os.Lstat(linkPath); err != nil {
		return "", serviceError(options.Language, "service.errors.s6_not_enabled", map[string]string{"path": linkPath}, err)
	}
	return linkPath, nil
}

func installLaunchd(options Options, servicePath string) error {
	content := launchdPlist(options)
	if err := os.WriteFile(servicePath, []byte(content), 0644); err != nil {
		return serviceError(options.Language, "service.errors.write_launchd_plist", nil, err)
	}
	if options.UserMode {
		return run("launchctl", "load", servicePath)
	}
	return run("launchctl", "load", servicePath)
}

func launchdPlist(options Options) string {
	programArguments := fmt.Sprintf(`<string>%s</string><string>--config</string><string>%s</string><string>--data</string><string>%s</string><string>--log</string><string>%s</string>`, xml(options.BinaryPath), xml(options.ConfigDir), xml(options.DataDir), xml(options.LogsDir))
	content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>Label</key><string>%s</string><key>ProgramArguments</key><array>%s</array><key>RunAtLoad</key><true/><key>KeepAlive</key><true/><key>StandardOutPath</key><string>%s</string><key>StandardErrorPath</key><string>%s</string></dict></plist>
`, xml(launchdLabel(options)), programArguments, xml(filepath.Join(options.LogsDir, "server.log")), xml(filepath.Join(options.LogsDir, "error.log")))
	return content
}

func launchdLabel(options Options) string {
	return "io.github." + options.Org + "." + options.Name
}

func installWindows(options Options) error {
	if err := ensureDirectories(options); err != nil {
		return err
	}
	binaryPath := options.BinaryPath
	if err := run("sc.exe", "create", options.Name, "binPath=", binaryPath, "start=", "auto", "DisplayName=", options.Name); err != nil {
		return err
	}
	if err := run("sc.exe", "config", options.Name, "obj=", "NT SERVICE\\"+options.Name); err != nil {
		return err
	}
	principal := "NT SERVICE\\" + options.Name + ":(OI)(CI)F"
	for _, directory := range []string{options.ConfigDir, options.DataDir, options.DatabaseDir, options.CacheDir, options.LogsDir, options.BackupDir} {
		if err := run("icacls.exe", directory, "/grant", principal, "/T", "/C"); err != nil {
			return serviceError(options.Language, "service.errors.set_permissions", map[string]string{"path": directory}, err)
		}
	}
	return run("sc.exe", "start", options.Name)
}

func installBSD(options Options, servicePath string) error {
	content := fmt.Sprintf(`#!/bin/sh
# PROVIDE: %s
# REQUIRE: NETWORKING
# KEYWORD: shutdown
. /etc/rc.subr
name=%q
rcvar=%q
pidfile=%q
command=/usr/sbin/daemon
command_args=%q
start_precmd=%s_prestart
%s_prestart() { install -d -m 0755 %q %q; }
load_rc_config $name
run_rc_command "$1"
	`, options.Name, options.Name, options.Name+"_enable", supervisorPIDPath(options), "-P ${pidfile} -r -f "+options.BinaryPath+" -- --config "+options.ConfigDir+" --data "+options.DataDir+" --log "+options.LogsDir, options.Name, options.Name, filepath.Join("/var/run", options.Org, options.Name), options.LogsDir)
	if err := writeExecutable(servicePath, []byte(content), options.Language); err != nil {
		return err
	}
	if commandExists("sysrc") {
		if err := run("sysrc", options.Name+"_enable=YES"); err != nil {
			return err
		}
	}
	return run("service", options.Name, "start")
}

func writeExecutable(path string, data []byte, languages ...string) error {
	language := "en"
	if len(languages) > 0 {
		language = serviceLanguage(languages[0])
	}
	if err := os.WriteFile(path, data, 0755); err != nil {
		return serviceError(language, "service.errors.write_file", map[string]string{"path": path}, err)
	}
	return os.Chmod(path, 0755)
}

func xml(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	value = strings.ReplaceAll(value, `"`, "&quot;")
	return strings.ReplaceAll(value, "'", "&apos;")
}

func run(name string, args ...string) error {
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, message)
	}
	return nil
}

func copyBinary(source, destination string, languages ...string) error {
	language := "en"
	if len(languages) > 0 {
		language = serviceLanguage(languages[0])
	}
	input, err := os.Open(source)
	if err != nil {
		return serviceError(language, "service.errors.open_source_binary", nil, err)
	}
	defer func() { _ = input.Close() }()
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}
	info, err := os.Lstat(destination)
	if err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New(serviceMessage(language, "service.errors.binary_destination_invalid"))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".citylist-binary-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer func() { _ = os.Remove(temporary) }()
	if err := file.Chmod(0755); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := io.Copy(file, input); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, destination)
}

func detectInstalledWithManager(options Options, manager ServiceType) (ServiceType, bool, string, error) {
	path, err := serviceFilePath(options, manager)
	if err != nil {
		return ServiceUnknown, false, "", err
	}
	if manager == ServiceWindows {
		return manager, true, path, nil
	}
	if _, err := os.Lstat(path); err == nil {
		return manager, true, path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return manager, false, path, err
	}
	return manager, false, path, nil
}

func control(options Options, action string) error {
	return controlWithManager(options, action, DetectServiceManager())
}

func controlWithManager(options Options, action string, manager ServiceType) error {
	options, err := normalizeOptions(options)
	if err != nil {
		return err
	}
	manager, installed, servicePath, err := detectInstalledWithManager(options, manager)
	if err != nil {
		return err
	}
	if !installed {
		return errors.New(serviceMessageFormat(options.Language, "service.errors.not_installed", map[string]string{"name": options.Name}))
	}
	switch manager {
	case ServiceSystemd:
		args := []string{}
		if options.UserMode {
			args = append(args, "--user")
		}
		return run("systemctl", append(args, action, options.Name)...)
	case ServiceOpenRC:
		return run("rc-service", options.Name, action)
	case ServiceRunit:
		if action == "reload" {
			action = "hup"
		}
		return run("sv", action, filepath.Join("/var/service", options.Name))
	case ServiceS6:
		flag := map[string]string{"start": "-u", "stop": "-d", "restart": "-r", "reload": "-h"}[action]
		if flag == "" {
			return errors.New(serviceMessageFormat(options.Language, "service.errors.action_unsupported", map[string]string{"action": action, "manager": manager.String()}))
		}
		linkPath, err := s6ServiceLink(options)
		if err != nil {
			return err
		}
		return run("s6-svc", flag, linkPath)
	case ServiceSysV:
		return run(servicePath, action)
	case ServiceLaunchd:
		switch action {
		case "start":
			return run("launchctl", "load", servicePath)
		case "stop":
			return run("launchctl", "unload", servicePath)
		case "restart", "reload":
			if err := run("launchctl", "unload", servicePath); err != nil {
				return err
			}
			return run("launchctl", "load", servicePath)
		}
	case ServiceWindows:
		return run("sc.exe", action, options.Name)
	case ServiceBSDRC:
		return run("service", options.Name, action)
	}
	return errors.New(serviceMessageFormat(options.Language, "service.errors.action_unsupported", map[string]string{"action": action, "manager": manager.String()}))
}

func Start() error                             { return StartWithOptions(Options{}) }
func StartWithOptions(options Options) error   { return control(options, "start") }
func Stop() error                              { return StopWithOptions(Options{}) }
func StopWithOptions(options Options) error    { return control(options, "stop") }
func Restart() error                           { return RestartWithOptions(Options{}) }
func RestartWithOptions(options Options) error { return control(options, "restart") }
func Reload() error                            { return ReloadWithOptions(Options{}) }
func ReloadWithOptions(options Options) error  { return control(options, "reload") }

func DisableWithOptions(options Options) error {
	return disableWithManager(options, DetectServiceManager())
}

func disableWithManager(options Options, manager ServiceType) error {
	options, err := normalizeOptions(options)
	if err != nil {
		return err
	}
	manager, installed, _, err := detectInstalledWithManager(options, manager)
	if err != nil {
		return err
	}
	if !installed {
		return errors.New(serviceMessageFormat(options.Language, "service.errors.not_installed", map[string]string{"name": options.Name}))
	}
	if err := controlWithManager(options, "stop", manager); err != nil {
		return err
	}
	switch manager {
	case ServiceSystemd:
		args := []string{}
		if options.UserMode {
			args = append(args, "--user")
		}
		return run("systemctl", append(args, "disable", options.Name)...)
	case ServiceOpenRC:
		return run("rc-update", "del", options.Name, "default")
	case ServiceRunit:
		return os.Remove(filepath.Join("/var/service", options.Name))
	case ServiceSysV:
		if commandExists("update-rc.d") {
			return run("update-rc.d", "-f", options.Name, "remove")
		}
		return run("chkconfig", "--del", options.Name)
	case ServiceLaunchd:
		return nil
	case ServiceWindows:
		return run("sc.exe", "config", options.Name, "start=", "disabled")
	case ServiceBSDRC:
		return nil
	case ServiceS6:
		return removeS6ServiceLink(options)
	default:
		return errors.New(serviceMessageFormat(options.Language, "service.errors.manager_unsupported_named", map[string]string{"manager": manager.String()}))
	}
}

func StateWithOptions(options Options) (State, error) {
	return stateWithManager(options, DetectServiceManager())
}

func stateWithManager(options Options, manager ServiceType) (State, error) {
	options, err := normalizeOptions(options)
	if err != nil {
		return State{}, err
	}
	manager, installed, servicePath, err := detectInstalledWithManager(options, manager)
	state := State{Installed: installed, Manager: manager.String()}
	if err != nil || !installed {
		return state, err
	}
	var output []byte
	switch manager {
	case ServiceSystemd:
		args := []string{}
		if options.UserMode {
			args = append(args, "--user")
		}
		output, err = exec.Command("systemctl", append(args, "show", options.Name, "--property=ActiveState,UnitFileState,MainPID", "--value")...).Output()
		if err == nil {
			values := strings.Split(strings.TrimSpace(string(output)), "\n")
			if len(values) > 0 {
				state.Running = values[0] == "active"
			}
			if len(values) > 1 {
				state.Enabled = values[1] == "enabled"
			}
			if len(values) > 2 {
				state.PID = values[2]
			}
		}
	case ServiceOpenRC:
		output, err = exec.Command("rc-service", options.Name, "status").CombinedOutput()
		state.Running = err == nil
		state.PID = processPID(string(output))
		state.Enabled = commandOutputContains("rc-update", []string{"show", "default"}, options.Name)
	case ServiceRunit:
		output, err = exec.Command("sv", "status", filepath.Join("/var/service", options.Name)).CombinedOutput()
		state.Running = err == nil && strings.Contains(string(output), "run:")
		state.PID = processPID(string(output))
		state.Enabled = pathExists(filepath.Join("/var/service", options.Name))
	case ServiceS6:
		var linkPath string
		linkPath, err = s6ServiceLink(options)
		if err == nil {
			state.Enabled = true
			output, err = exec.Command("s6-svstat", linkPath).CombinedOutput()
		}
		state.Running = err == nil && strings.Contains(string(output), "up")
		state.PID = processPID(string(output))
	case ServiceSysV:
		output, err = exec.Command(servicePath, "status").CombinedOutput()
		state.Running = err == nil
		state.PID = processPID(string(output))
		state.Enabled = commandOutputContains("chkconfig", []string{"--list", options.Name}, options.Name) || commandOutputContains("update-rc.d", []string{"-n", options.Name, "defaults"}, options.Name)
	case ServiceLaunchd:
		output, err = exec.Command("launchctl", "list").CombinedOutput()
		state.Running = err == nil && strings.Contains(string(output), options.Org+"."+options.Name)
		state.Enabled = state.Running
		state.PID = launchdPID(string(output), launchdLabel(options))
	case ServiceWindows:
		output, err = exec.Command("sc.exe", "queryex", options.Name).CombinedOutput()
		state.Running = err == nil && strings.Contains(string(output), "RUNNING")
		state.PID = processPID(string(output))
		state.Enabled = err == nil && !strings.Contains(string(output), "DISABLED")
	case ServiceBSDRC:
		output, err = exec.Command("service", options.Name, "status").CombinedOutput()
		state.Running = err == nil
		state.PID = processPID(string(output))
		state.Enabled = true
	}
	if err != nil && manager != ServiceOpenRC && manager != ServiceRunit && manager != ServiceSysV && manager != ServiceLaunchd && manager != ServiceWindows && manager != ServiceBSDRC {
		return state, serviceError(options.Language, "service.errors.read_state", map[string]string{"manager": manager.String()}, err)
	}
	return state, nil
}

func processPID(output string) string {
	fields := strings.Fields(output)
	for index, field := range fields {
		if !strings.EqualFold(strings.Trim(field, ":=()"), "pid") {
			continue
		}
		for _, candidate := range fields[index+1:] {
			candidate = strings.Trim(candidate, ":=()")
			if pid, err := strconv.Atoi(candidate); err == nil && pid > 0 {
				return candidate
			}
		}
	}
	return ""
}

func launchdPID(output, label string) string {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[2] != label {
			continue
		}
		if pid, err := strconv.Atoi(fields[0]); err == nil && pid > 0 {
			return fields[0]
		}
	}
	return ""
}

func commandOutputContains(command string, args []string, value string) bool {
	output, err := exec.Command(command, args...).CombinedOutput()
	return err == nil && strings.Contains(string(output), value)
}

func PrintStatus(options Options, output io.Writer) error {
	state, err := StateWithOptions(options)
	if err != nil {
		return err
	}
	language := serviceLanguage(options.Language)
	serviceState := serviceMessage(language, "service.status.stopped")
	if state.Running {
		serviceState = serviceMessage(language, "service.status.running")
	}
	installed := serviceMessage(language, "service.status.not_installed")
	if state.Installed {
		installed = serviceMessage(language, "service.status.installed")
	}
	autostart := serviceMessage(language, "service.status.disabled")
	if state.Enabled {
		autostart = serviceMessage(language, "service.status.enabled")
	}
	if _, err := fmt.Fprintf(output, "%s:    %s\n%s:      %s\n%s: %s\n",
		serviceMessage(language, "service.status.service"), installed,
		serviceMessage(language, "service.status.state"), serviceState,
		serviceMessage(language, "service.status.auto_start"), autostart); err != nil {
		return err
	}
	if state.PID != "" && state.PID != "0" {
		_, err = fmt.Fprintf(output, "%s:        %s\n", serviceMessage(language, "service.status.pid"), state.PID)
	}
	return err
}

func Disable(options ...Options) error {
	value := Options{}
	if len(options) > 0 {
		value = options[0]
	}
	return DisableWithOptions(value)
}

func Uninstall(options ...Options) error {
	value := Options{}
	if len(options) > 0 {
		value = options[0]
	}
	return UninstallWithOptions(value, os.Stdin, os.Stdout)
}

func UninstallWithOptions(raw Options, input io.Reader, output io.Writer) error {
	return uninstallWithManager(raw, input, output, DetectServiceManager())
}

func uninstallWithManager(raw Options, input io.Reader, output io.Writer, manager ServiceType) error {
	options, err := normalizeOptions(raw)
	if err != nil {
		return err
	}
	if input == nil || output == nil {
		return errors.New(serviceMessage(options.Language, "service.errors.confirmation_io_required"))
	}
	if _, err := fmt.Fprint(output, serviceMessage(options.Language, "service.confirm_uninstall")); err != nil {
		return err
	}
	answer, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(answer), "y") && !strings.EqualFold(strings.TrimSpace(answer), "yes") {
		return errors.New(serviceMessage(options.Language, "service.errors.uninstall_cancelled"))
	}
	manager, installed, servicePath, err := detectInstalledWithManager(options, manager)
	if err != nil {
		return err
	}
	if installed {
		if err := controlWithManager(options, "stop", manager); err != nil {
			return err
		}
		switch manager {
		case ServiceSystemd:
			args := []string{}
			if options.UserMode {
				args = append(args, "--user")
			}
			if err := run("systemctl", append(args, "disable", options.Name)...); err != nil {
				return err
			}
		case ServiceOpenRC:
			if err := run("rc-update", "del", options.Name, "default"); err != nil {
				return err
			}
		case ServiceRunit:
			if err := os.Remove(filepath.Join("/var/service", options.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		case ServiceS6:
			if err := removeS6ServiceLink(options); err != nil {
				return err
			}
		case ServiceSysV:
			if commandExists("update-rc.d") {
				if err := run("update-rc.d", "-f", options.Name, "remove"); err != nil {
					return err
				}
			} else if err := run("chkconfig", "--del", options.Name); err != nil {
				return err
			}
		case ServiceLaunchd:
			if err := run("launchctl", "unload", servicePath); err != nil {
				return err
			}
		case ServiceWindows:
			if err := run("sc.exe", "delete", options.Name); err != nil {
				return err
			}
		case ServiceBSDRC:
			if err := run("service", options.Name, "stop"); err != nil {
				return err
			}
		}
		if manager != ServiceWindows {
			if err := os.RemoveAll(servicePath); err != nil {
				return err
			}
		}
		if manager == ServiceSystemd && !options.UserMode {
			if err := run("systemctl", "daemon-reload"); err != nil {
				return err
			}
		}
	}
	for _, directory := range []string{options.ConfigDir, options.DataDir, options.DatabaseDir, options.CacheDir, options.LogsDir, options.BackupDir} {
		if err := os.RemoveAll(directory); err != nil {
			return serviceError(options.Language, "service.errors.remove_data_directory", map[string]string{"path": directory}, err)
		}
	}
	if !options.UserMode {
		pidPath := filepath.Join("/var/run", options.Org, options.Name, options.Name+".pid")
		if err := os.Remove(pidPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if os.Geteuid() == 0 && !options.UserMode {
		switch runtime.GOOS {
		case "linux":
			userCommand, groupCommand := "userdel", "groupdel"
			if !commandExists(userCommand) {
				userCommand = "deluser"
			}
			if !commandExists(groupCommand) {
				groupCommand = "delgroup"
			}
			if commandExists(userCommand) {
				if err := run(userCommand, options.Name); err != nil && !strings.Contains(err.Error(), "does not exist") {
					return err
				}
			}
			if commandExists(groupCommand) {
				if _, lookupErr := user.LookupGroup(options.Name); lookupErr == nil {
					if err := run(groupCommand, options.Name); err != nil && !strings.Contains(err.Error(), "does not exist") {
						return err
					}
				} else {
					var unknownGroup user.UnknownGroupError
					if !errors.As(lookupErr, &unknownGroup) {
						return lookupErr
					}
				}
			}
		case "darwin":
			for _, target := range []string{"/Users/" + options.Name, "/Groups/" + options.Name} {
				if err := run("dscl", ".", "-delete", target); err != nil {
					return err
				}
			}
		case "freebsd", "openbsd", "netbsd", "dragonfly":
			if err := run("pw", "userdel", "-n", options.Name); err != nil {
				return err
			}
			if err := run("pw", "groupdel", "-n", options.Name); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprintln(output, serviceMessageFormat(options.Language, "service.uninstalled", map[string]string{"binary": binaryDestination(options)})); err != nil {
		return err
	}
	return nil
}

func GetBinaryPath() string {
	options, _ := normalizeOptions(Options{Name: "citylist", Org: "apimgr", ConfigDir: "/etc/apimgr/citylist", DataDir: "/var/lib/apimgr/citylist", LogsDir: "/var/log/apimgr/citylist"})
	return binaryDestination(options)
}

func getUIDGID(languages ...string) (int, error) {
	language := "en"
	if len(languages) > 0 {
		language = serviceLanguage(languages[0])
	}
	reserved := map[int]bool{65534: true, 999: true, 998: true, 997: true, 996: true, 995: true, 994: true, 993: true, 992: true, 991: true, 990: true, 989: true, 988: true, 987: true, 986: true, 985: true, 984: true, 983: true, 982: true, 981: true, 980: true}
	for id := 899; id >= 200; id-- {
		if reserved[id] {
			continue
		}
		if _, err := user.LookupId(strconv.Itoa(id)); err == nil {
			continue
		}
		if _, err := user.LookupGroupId(strconv.Itoa(id)); err == nil {
			continue
		}
		return id, nil
	}
	return 0, errors.New(serviceMessage(language, "service.errors.no_available_uid_gid"))
}

func ensureLinuxServiceAccount(options Options) error {
	if os.Geteuid() != 0 {
		return errors.New(serviceMessage(options.Language, "service.errors.root_required_for_account"))
	}
	if account, err := user.Lookup(options.Name); err == nil {
		if _, groupErr := user.LookupGroup(options.Name); groupErr == nil {
			return nil
		}
		if err := createLinuxGroup(options.Name, account.Gid, options.Language); err != nil {
			return err
		}
		return nil
	}
	id, err := getUIDGID(options.Language)
	if err != nil {
		return err
	}
	if err := createLinuxGroup(options.Name, strconv.Itoa(id), options.Language); err != nil {
		return err
	}
	if commandExists("useradd") {
		err = run("useradd", "--system", "--uid", strconv.Itoa(id), "--gid", strconv.Itoa(id), "--home-dir", options.ConfigDir, "--shell", "/usr/sbin/nologin", "--comment", options.Name+" service account", options.Name)
	} else if commandExists("adduser") {
		err = run("adduser", "-S", "-D", "-H", "-u", strconv.Itoa(id), "-G", options.Name, "-h", options.ConfigDir, "-s", "/sbin/nologin", "-g", options.Name+" service account", options.Name)
	} else {
		err = errors.New(serviceMessage(options.Language, "service.errors.user_creation_tool_missing"))
	}
	if err != nil {
		if commandExists("groupdel") {
			_ = run("groupdel", options.Name)
		} else if commandExists("delgroup") {
			_ = run("delgroup", options.Name)
		}
		return err
	}
	return nil
}

func createLinuxGroup(name, gid, language string) error {
	if commandExists("groupadd") {
		return run("groupadd", "--system", "--gid", gid, name)
	}
	if commandExists("addgroup") {
		return run("addgroup", "-S", "-g", gid, name)
	}
	return errors.New(serviceMessage(language, "service.errors.group_creation_tool_missing"))
}

func ensureServiceAccount(options Options) error {
	switch runtime.GOOS {
	case "linux":
		return ensureLinuxServiceAccount(options)
	case "darwin":
		if _, err := user.Lookup(options.Name); err == nil {
			return nil
		}
		id, err := getPlatformUID(399, 200, options.Language)
		if err != nil {
			return err
		}
		commands := [][]string{
			{"dscl", ".", "-create", "/Groups/" + options.Name},
			{"dscl", ".", "-create", "/Groups/" + options.Name, "PrimaryGroupID", strconv.Itoa(id)},
			{"dscl", ".", "-create", "/Groups/" + options.Name, "Password", "*"},
			{"dscl", ".", "-create", "/Users/" + options.Name},
			{"dscl", ".", "-create", "/Users/" + options.Name, "UniqueID", strconv.Itoa(id)},
			{"dscl", ".", "-create", "/Users/" + options.Name, "PrimaryGroupID", strconv.Itoa(id)},
			{"dscl", ".", "-create", "/Users/" + options.Name, "UserShell", "/usr/bin/false"},
			{"dscl", ".", "-create", "/Users/" + options.Name, "RealName", options.Name + " service account"},
			{"dscl", ".", "-create", "/Users/" + options.Name, "NFSHomeDirectory", options.DataDir},
			{"dscl", ".", "-create", "/Users/" + options.Name, "Password", "*"},
			{"dscl", ".", "-create", "/Users/" + options.Name, "IsHidden", "1"},
		}
		for _, command := range commands {
			if err := run(command[0], command[1:]...); err != nil {
				return err
			}
		}
		return nil
	case "freebsd", "openbsd", "netbsd", "dragonfly":
		if _, err := user.Lookup(options.Name); err == nil {
			return nil
		}
		id, err := getPlatformUID(899, 200, options.Language)
		if err != nil {
			return err
		}
		if err := run("pw", "groupadd", "-n", options.Name, "-g", strconv.Itoa(id)); err != nil {
			return err
		}
		return run("pw", "useradd", "-n", options.Name, "-u", strconv.Itoa(id), "-g", strconv.Itoa(id), "-d", options.DataDir, "-s", "/usr/sbin/nologin", "-c", options.Name+" service account")
	default:
		return nil
	}
}

func getPlatformUID(top, bottom int, languages ...string) (int, error) {
	language := "en"
	if len(languages) > 0 {
		language = serviceLanguage(languages[0])
	}
	for id := top; id >= bottom; id-- {
		if _, err := user.LookupId(strconv.Itoa(id)); err == nil {
			continue
		}
		if _, err := user.LookupGroupId(strconv.Itoa(id)); err == nil {
			continue
		}
		return id, nil
	}
	return 0, serviceError(language, "service.errors.no_available_uid_gid_range", map[string]string{"minimum": strconv.Itoa(bottom), "maximum": strconv.Itoa(top)}, nil)
}
