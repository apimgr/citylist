//go:build gui && windows

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
	command := `$v = Get-ItemPropertyValue -Path HKCU:\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize -Name AppsUseLightTheme; Write-Output $v`
	value, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command).Output()
	if err == nil {
		return strings.TrimSpace(string(value)) != "1"
	}
	return theme.DetectTerminalTheme() == "dark"
}
