//go:build gui && linux

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
	value, err := exec.CommandContext(ctx, "gsettings", "get", "org.gnome.desktop.interface", "color-scheme").Output()
	if err == nil {
		return strings.Contains(strings.ToLower(string(value)), "dark")
	}
	return theme.DetectTerminalTheme() == "dark"
}
