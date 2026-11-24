package paths

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

// GetConfigDir returns the OS-specific configuration directory
func GetConfigDir(appName string) string {
	// Check environment variable first
	if dir := os.Getenv("CONFIG_DIR"); dir != "" {
		return dir
	}

	// Check if running in Docker
	if isDocker() {
		return "/config"
	}

	current, err := user.Current()
	if err != nil {
		return filepath.Join(".", "config")
	}

	isRoot := current.Uid == "0"

	switch runtime.GOOS {
	case "linux", "freebsd", "openbsd", "netbsd":
		if isRoot {
			return filepath.Join("/etc/apimgr", appName)
		}
		return filepath.Join(current.HomeDir, ".config/apimgr", appName)

	case "darwin":
		appNamePascal := toPascalCase(appName)
		if isRoot {
			return filepath.Join("/Library/Application Support/apimgr", appNamePascal)
		}
		return filepath.Join(current.HomeDir, "Library/Application Support/apimgr", appNamePascal)

	case "windows":
		appNamePascal := toPascalCase(appName)
		programData := os.Getenv("ProgramData")
		if programData == "" {
			programData = "C:\\ProgramData"
		}
		return filepath.Join(programData, "apimgr", appNamePascal)

	default:
		return filepath.Join(".", "config")
	}
}

// GetDataDir returns the OS-specific data directory
func GetDataDir(appName string) string {
	// Check environment variable first
	if dir := os.Getenv("DATA_DIR"); dir != "" {
		return dir
	}

	// Check if running in Docker
	if isDocker() {
		return "/data"
	}

	current, err := user.Current()
	if err != nil {
		return filepath.Join(".", "data")
	}

	isRoot := current.Uid == "0"

	switch runtime.GOOS {
	case "linux", "freebsd", "openbsd", "netbsd":
		if isRoot {
			return filepath.Join("/var/lib/apimgr", appName)
		}
		return filepath.Join(current.HomeDir, ".local/share/apimgr", appName)

	case "darwin":
		appNamePascal := toPascalCase(appName)
		if isRoot {
			return filepath.Join("/Library/Application Support/apimgr", appNamePascal, "data")
		}
		return filepath.Join(current.HomeDir, "Library/Application Support/apimgr", appNamePascal, "data")

	case "windows":
		appNamePascal := toPascalCase(appName)
		programData := os.Getenv("ProgramData")
		if programData == "" {
			programData = "C:\\ProgramData"
		}
		return filepath.Join(programData, "apimgr", appNamePascal, "data")

	default:
		return filepath.Join(".", "data")
	}
}

// GetLogsDir returns the OS-specific logs directory
func GetLogsDir(appName string) string {
	// Check environment variable first
	if dir := os.Getenv("LOGS_DIR"); dir != "" {
		return dir
	}

	// Check if running in Docker
	if isDocker() {
		return "/logs"
	}

	current, err := user.Current()
	if err != nil {
		return filepath.Join(".", "logs")
	}

	isRoot := current.Uid == "0"

	switch runtime.GOOS {
	case "linux", "freebsd", "openbsd", "netbsd":
		if isRoot {
			return filepath.Join("/var/log/apimgr", appName)
		}
		return filepath.Join(current.HomeDir, ".local/state/apimgr", appName)

	case "darwin":
		appNamePascal := toPascalCase(appName)
		if isRoot {
			return filepath.Join("/Library/Logs/apimgr", appNamePascal)
		}
		return filepath.Join(current.HomeDir, "Library/Logs/apimgr", appNamePascal)

	case "windows":
		appNamePascal := toPascalCase(appName)
		programData := os.Getenv("ProgramData")
		if programData == "" {
			programData = "C:\\ProgramData"
		}
		return filepath.Join(programData, "apimgr", appNamePascal, "logs")

	default:
		return filepath.Join(".", "logs")
	}
}

// GetCacheDir returns the OS-specific cache directory, honoring CACHE_DIR.
func GetCacheDir(appName string) string {
	if dir := os.Getenv("CACHE_DIR"); dir != "" {
		return dir
	}
	if isDocker() {
		return filepath.Join("/data", appName, "cache")
	}
	current, err := user.Current()
	if err != nil {
		return filepath.Join(".", "cache")
	}
	if current.Uid == "0" {
		switch runtime.GOOS {
		case "darwin":
			return filepath.Join("/Library/Caches/apimgr", appName)
		case "windows":
			programData := os.Getenv("ProgramData")
			if programData == "" {
				programData = `C:\\ProgramData`
			}
			return filepath.Join(programData, "apimgr", appName, "cache")
		default:
			return filepath.Join("/var/cache/apimgr", appName)
		}
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(current.HomeDir, "Library/Caches/apimgr", appName)
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = filepath.Join(current.HomeDir, "AppData/Local")
		}
		return filepath.Join(base, "apimgr", appName, "cache")
	default:
		return filepath.Join(current.HomeDir, ".cache/apimgr", appName)
	}
}

// GetDatabaseDir returns the SQLite directory from the environment or platform defaults.
func GetDatabaseDir(dataDir string) string {
	if dir := os.Getenv("DATABASE_DIR"); dir != "" {
		return dir
	}
	if isDocker() {
		return "/data/db/sqlite"
	}
	return filepath.Join(dataDir, "db")
}

// EnsureDir creates a directory if it doesn't exist
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0755)
}

// EnsurePrivateDir creates or secures a directory that contains credentials.
func EnsurePrivateDir(path string) error {
	directoryMode := os.FileMode(0700)
	if current, err := user.Current(); err == nil && current.Uid == "0" {
		directoryMode = 0755
	}
	if err := os.MkdirAll(path, directoryMode); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("private path %s is not a directory", path)
	}
	return os.Chmod(path, 0700)
}

// EnsureDirs creates multiple directories
func EnsureDirs(paths ...string) error {
	for _, path := range paths {
		if err := EnsureDir(path); err != nil {
			return err
		}
	}
	return nil
}

// GetDefaultDirs returns config, data, and logs directories for the app
func GetDefaultDirs(appName string) (configDir, dataDir, logsDir string) {
	return GetConfigDir(appName), GetDataDir(appName), GetLogsDir(appName)
}

// GetBackupDir returns the backup directory for the app
func GetBackupDir(appName string) string {
	return GetBackupDirFor(appName, GetDataDir(appName), "")
}

// GetBackupDirFor resolves an explicit, environment, system, or user backup path.
func GetBackupDirFor(appName, dataDir, flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if dir := os.Getenv("BACKUP_DIR"); dir != "" {
		return dir
	}

	current, err := user.Current()
	if err != nil {
		return filepath.Join(".", "backups")
	}

	isRoot := current.Uid == "0"
	if isDocker() {
		return filepath.Join("/data", "backups", appName)
	}
	systemDir := systemBackupDir(appName)
	if isWritable(systemDir) {
		return systemDir
	}
	if isRoot && runtime.GOOS != "windows" {
		return filepath.Join(dataDir, "backup")
	}
	return userBackupDir(appName, current.HomeDir)
}

func systemBackupDir(appName string) string {
	switch runtime.GOOS {
	case "linux", "freebsd", "openbsd", "netbsd":
		if runtime.GOOS == "linux" {
			return filepath.Join("/mnt/Backups", "apimgr", appName)
		}
		return filepath.Join("/var/backups", "apimgr", appName)

	case "darwin":
		return filepath.Join("/Library/Backups", "apimgr", appName)

	case "windows":
		appNamePascal := toPascalCase(appName)
		programData := os.Getenv("ProgramData")
		if programData == "" {
			programData = "C:\\ProgramData"
		}
		return filepath.Join(programData, "Backups", "apimgr", appNamePascal)

	default:
		return filepath.Join(".", "backups")
	}
}

func userBackupDir(appName, home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library/Backups", "apimgr", appName)
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = filepath.Join(home, "AppData/Local")
		}
		return filepath.Join(base, "Backups", "apimgr", appName)
	default:
		return filepath.Join(home, ".local/share/Backups", "apimgr", appName)
	}
}

func isWritable(path string) bool {
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		probe, err := os.CreateTemp(path, ".write-test-*")
		if err != nil {
			return false
		}
		name := probe.Name()
		_ = probe.Close()
		_ = os.Remove(name)
		return true
	}
	parent := filepath.Dir(path)
	info, err = os.Stat(parent)
	return err == nil && info.IsDir() && info.Mode().Perm()&0200 != 0
}

// isDocker checks if running inside a Docker container
func isDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	// Check for cgroup
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		if strings.Contains(string(data), "docker") {
			return true
		}
	}
	return false
}

// toPascalCase converts a string to PascalCase
func toPascalCase(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
