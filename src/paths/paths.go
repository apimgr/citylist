package paths

import (
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

// EnsureDir creates a directory if it doesn't exist
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0755)
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
	// Check environment variable first
	if dir := os.Getenv("BACKUP_DIR"); dir != "" {
		return dir
	}

	current, err := user.Current()
	if err != nil {
		return filepath.Join(".", "backups")
	}

	isRoot := current.Uid == "0"

	switch runtime.GOOS {
	case "linux", "freebsd", "openbsd", "netbsd":
		if isRoot {
			return filepath.Join("/var/backups/apimgr", appName)
		}
		return filepath.Join(current.HomeDir, ".local/state/apimgr", appName, "backups")

	case "darwin":
		appNamePascal := toPascalCase(appName)
		if isRoot {
			return filepath.Join("/Library/Application Support/apimgr", appNamePascal, "backups")
		}
		return filepath.Join(current.HomeDir, "Library/Application Support/apimgr", appNamePascal, "backups")

	case "windows":
		appNamePascal := toPascalCase(appName)
		programData := os.Getenv("ProgramData")
		if programData == "" {
			programData = "C:\\ProgramData"
		}
		return filepath.Join(programData, "apimgr", appNamePascal, "backups")

	default:
		return filepath.Join(".", "backups")
	}
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
