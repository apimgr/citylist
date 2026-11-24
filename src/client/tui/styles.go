package tui

import (
	"charm.land/lipgloss/v2"
	"github.com/apimgr/citylist/src/common/theme"
)

// TUIStyles maps semantic interface roles to the shared ANSI palette.
type TUIStyles struct {
	Base     lipgloss.Style
	Title    lipgloss.Style
	Selected lipgloss.Style
	Error    lipgloss.Style
	Success  lipgloss.Style
	Warning  lipgloss.Style
	Muted    lipgloss.Style
	Border   lipgloss.Style
}

// StylesFromTerminalPalette builds terminal-safe styles without literal RGB colors.
func StylesFromTerminalPalette(p theme.TerminalPalette) TUIStyles {
	return TUIStyles{
		Base:     lipgloss.NewStyle().Foreground(lipgloss.Color(p.Foreground)),
		Title:    lipgloss.NewStyle().Foreground(lipgloss.Color(p.Primary)).Bold(true),
		Selected: lipgloss.NewStyle().Reverse(true),
		Error:    lipgloss.NewStyle().Foreground(lipgloss.Color(p.Error)),
		Success:  lipgloss.NewStyle().Foreground(lipgloss.Color(p.Success)),
		Warning:  lipgloss.NewStyle().Foreground(lipgloss.Color(p.Warning)),
		Muted:    lipgloss.NewStyle().Foreground(lipgloss.Color(p.Muted)),
		Border:   lipgloss.NewStyle().BorderForeground(lipgloss.Color(p.Border)),
	}
}
