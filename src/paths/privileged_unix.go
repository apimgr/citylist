//go:build !windows

package paths

import (
	"os"
	"path/filepath"
)

func isWindowsAdmin() bool { return false }

func windowsProgramData() string {
	if value := os.Getenv("ProgramData"); value != "" {
		return value
	}
	return `C:\ProgramData`
}

func windowsAppData(home string) string {
	if value := os.Getenv("APPDATA"); value != "" {
		return value
	}
	return filepath.Join(home, `AppData\Roaming`)
}

func windowsLocalAppData(home string) string {
	if value := os.Getenv("LOCALAPPDATA"); value != "" {
		return value
	}
	return filepath.Join(home, `AppData\Local`)
}
