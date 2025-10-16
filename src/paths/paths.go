package paths

import (
	"os"
	"os/user"
	"path/filepath"
	"runtime"
)

// GetDefaultDirs returns OS-specific default directories for config, data, and logs
func GetDefaultDirs(projectName string) (configDir, dataDir, logsDir string) {
	// Detect if running as root/admin
	isRoot := false
	if runtime.GOOS == "windows" {
		isRoot = os.Getenv("USERDOMAIN") == os.Getenv("COMPUTERNAME")
	} else {
		isRoot = os.Geteuid() == 0
	}

	if isRoot {
		// System-wide paths for privileged users
		switch runtime.GOOS {
		case "windows":
			programData := os.Getenv("ProgramData")
			if programData == "" {
				programData = "C:\\ProgramData"
			}
			configDir = filepath.Join(programData, projectName, "config")
			dataDir = filepath.Join(programData, projectName, "data")
			logsDir = filepath.Join(programData, projectName, "logs")

		case "darwin":
			// macOS system paths
			configDir = filepath.Join("/Library/Application Support", projectName)
			dataDir = filepath.Join("/Library/Application Support", projectName, "data")
			logsDir = filepath.Join("/Library/Logs", projectName)

		default:
			// Linux/BSD system paths
			configDir = filepath.Join("/etc", projectName)
			dataDir = filepath.Join("/var/lib", projectName)
			logsDir = filepath.Join("/var/log", projectName)
		}
	} else {
		// User-specific paths
		var homeDir string
		currentUser, err := user.Current()
		if err == nil {
			homeDir = currentUser.HomeDir
		} else {
			homeDir = os.Getenv("HOME")
		}

		switch runtime.GOOS {
		case "windows":
			appData := os.Getenv("APPDATA")
			if appData == "" {
				appData = filepath.Join(homeDir, "AppData", "Roaming")
			}
			localAppData := os.Getenv("LOCALAPPDATA")
			if localAppData == "" {
				localAppData = filepath.Join(homeDir, "AppData", "Local")
			}

			configDir = filepath.Join(appData, projectName)
			dataDir = filepath.Join(localAppData, projectName)
			logsDir = filepath.Join(localAppData, projectName, "logs")

		case "darwin":
			// macOS user paths
			configDir = filepath.Join(homeDir, "Library", "Application Support", projectName)
			dataDir = filepath.Join(homeDir, "Library", "Application Support", projectName, "data")
			logsDir = filepath.Join(homeDir, "Library", "Logs", projectName)

		default:
			// Linux/BSD user paths (XDG Base Directory)
			xdgConfigHome := os.Getenv("XDG_CONFIG_HOME")
			if xdgConfigHome == "" {
				xdgConfigHome = filepath.Join(homeDir, ".config")
			}
			xdgDataHome := os.Getenv("XDG_DATA_HOME")
			if xdgDataHome == "" {
				xdgDataHome = filepath.Join(homeDir, ".local", "share")
			}
			xdgStateHome := os.Getenv("XDG_STATE_HOME")
			if xdgStateHome == "" {
				xdgStateHome = filepath.Join(homeDir, ".local", "state")
			}

			configDir = filepath.Join(xdgConfigHome, projectName)
			dataDir = filepath.Join(xdgDataHome, projectName)
			logsDir = filepath.Join(xdgStateHome, projectName)
		}
	}

	return configDir, dataDir, logsDir
}

// EnsureDirectories creates the directories if they don't exist
func EnsureDirectories(dirs ...string) error {
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return nil
}
