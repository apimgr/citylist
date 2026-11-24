//go:build gui && darwin

package gui

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/common/theme"
)

func systemThemeIsDark() bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	value, err := exec.CommandContext(ctx, "defaults", "read", "-g", "AppleInterfaceStyle").Output()
	if err == nil && strings.Contains(strings.ToLower(string(value)), "dark") {
		return true
	}
	if err == nil {
		return false
	}
	return theme.DetectTerminalTheme() == "dark"
}
