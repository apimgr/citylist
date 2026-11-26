package theme

import (
	"os"
	"strings"

	"golang.org/x/term"
)

// DetectTerminalTheme follows common terminal foreground/background reporting.
func DetectTerminalTheme() string {
	parts := strings.Split(os.Getenv("COLORFGBG"), ";")
	if len(parts) > 0 {
		background := strings.TrimSpace(parts[len(parts)-1])
		if background == "7" || background == "15" {
			return "light"
		}
		if background != "" {
			return "dark"
		}
	}
	return "dark"
}

type ThemePalette struct {
	Background      string `json:"background"`
	Foreground      string `json:"foreground"`
	Primary         string `json:"primary"`
	Secondary       string `json:"secondary"`
	Accent          string `json:"accent"`
	Success         string `json:"success"`
	Warning         string `json:"warning"`
	Error           string `json:"error"`
	Info            string `json:"info"`
	Surface         string `json:"surface"`
	SurfaceAlt      string `json:"surface_alt"`
	BackgroundHover string `json:"background_hover"`
	Border          string `json:"border"`
	BorderHover     string `json:"border_hover"`
	Muted           string `json:"muted"`
	OnError         string `json:"on_error"`
}

type TerminalPalette struct {
	Foreground string `json:"foreground"`
	Muted      string `json:"muted"`
	Primary    string `json:"primary"`
	Success    string `json:"success"`
	Warning    string `json:"warning"`
	Error      string `json:"error"`
	Info       string `json:"info"`
	Border     string `json:"border"`
}

var ThemePaletteDark = ThemePalette{
	Background: "#282a36", Foreground: "#f8f8f2",
	Primary: "#bd93f9", Secondary: "#50fa7b", Accent: "#ff79c6",
	Success: "#50fa7b", Warning: "#ffb86c", Error: "#ff5555", Info: "#8be9fd",
	Surface: "#2b2d3a", SurfaceAlt: "#21222c", BackgroundHover: "#343746", Border: "#44475a", BorderHover: "#6272a4", Muted: "#939fcc", OnError: "#ffffff",
}

var ThemePaletteLight = ThemePalette{
	Background: "#ffffff", Foreground: "#1f2328",
	Primary: "#0969da", Secondary: "#1a7f37", Accent: "#8250df",
	Success: "#1a7f37", Warning: "#9a6700", Error: "#d1242f", Info: "#0969da",
	Surface: "#f6f8fa", SurfaceAlt: "#eff2f5", BackgroundHover: "#eff2f5", Border: "#d1d9e0", BorderHover: "#818b98", Muted: "#59636e", OnError: "#ffffff",
}

var TerminalPaletteDark = TerminalPalette{
	Foreground: "15", Muted: "7", Primary: "13",
	Success: "10", Warning: "11", Error: "9", Info: "12", Border: "13",
}

var TerminalPaletteLight = TerminalPalette{
	Foreground: "0", Muted: "8", Primary: "4",
	Success: "2", Warning: "3", Error: "1", Info: "4", Border: "4",
}

type ColorConfig struct {
	Color    bool
	ColorSet bool
}

func ColorEnabled(forceColor *bool, configs ...*ColorConfig) bool {
	if forceColor != nil {
		return *forceColor
	}
	if len(configs) > 0 && configs[0] != nil && configs[0].ColorSet {
		return configs[0].Color
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}
