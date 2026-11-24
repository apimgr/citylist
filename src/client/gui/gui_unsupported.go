//go:build !gui || freebsd || netbsd || openbsd || !(linux || darwin || windows)

package gui

import (
	"errors"
	"time"
)

// Available reports that the native GPU toolkit is not compiled for this platform.
func Available() bool { return false }

// Run reports the platform limitation without attempting native initialization.
func Run(serverURL, token, language, version, themeMode, apiVersion string, timeout time.Duration, retries int, retryDelay time.Duration, saveServer func(string) error) error {
	return errors.New("native GUI is unavailable on this platform; use TUI mode on an interactive terminal")
}
