package display

import (
	"os"
	"runtime"
	"strings"

	"golang.org/x/term"
)

// DisplayMode identifies how an interactive CLI can present itself.
type DisplayMode int

const (
	DisplayModeHeadless DisplayMode = iota
	DisplayModeCLI
	DisplayModeTUI
	DisplayModeGUI
)

// DisplayEnv describes the process terminal and native display environment.
type DisplayEnv struct {
	Mode         DisplayMode
	HasDisplay   bool
	DisplayType  string
	IsTerminal   bool
	IsSSH        bool
	IsMosh       bool
	IsScreen     bool
	TerminalType string
	Cols         int
	Rows         int
}

// DetectDisplayEnv detects terminal, remote-session, and native display state.
func DetectDisplayEnv() DisplayEnv {
	env := DisplayEnv{
		IsTerminal:   term.IsTerminal(int(os.Stdout.Fd())),
		TerminalType: os.Getenv("TERM"),
		IsSSH:        os.Getenv("SSH_CLIENT") != "" || os.Getenv("SSH_TTY") != "",
		IsMosh:       os.Getenv("MOSH") != "" || strings.Contains(strings.ToLower(os.Getenv("TERM")), "mosh"),
		IsScreen:     os.Getenv("STY") != "" || os.Getenv("TMUX") != "",
	}
	if env.IsTerminal {
		env.Cols, env.Rows, _ = term.GetSize(int(os.Stdout.Fd()))
	}
	env.detectPlatformDisplay()
	env.Mode = env.autoDetectDisplayMode()
	return env
}

func (e *DisplayEnv) detectPlatformDisplay() {
	switch runtime.GOOS {
	case "windows":
		e.HasDisplay, e.DisplayType = true, "windows"
	case "darwin":
		e.HasDisplay, e.DisplayType = true, "macos"
	case "linux":
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			e.HasDisplay, e.DisplayType = true, "wayland"
		} else if os.Getenv("DISPLAY") != "" {
			e.HasDisplay, e.DisplayType = true, "x11"
		}
	}
	if !e.HasDisplay {
		e.DisplayType = "none"
	}
}

func (e *DisplayEnv) autoDetectDisplayMode() DisplayMode {
	if !e.IsTerminal && !e.HasDisplay {
		return DisplayModeHeadless
	}
	if e.TerminalType == "dumb" {
		return DisplayModeCLI
	}
	if e.HasDisplay && !e.IsSSH && !e.IsMosh {
		return DisplayModeGUI
	}
	if e.IsTerminal {
		return DisplayModeTUI
	}
	return DisplayModeCLI
}

func (e *DisplayEnv) IsDumbTerminal() bool { return e.TerminalType == "dumb" }

func (e DisplayEnv) IsGUI() bool                           { return e.Mode == DisplayModeGUI }
func (e DisplayEnv) IsTUI() bool                           { return e.Mode == DisplayModeTUI }
func (e DisplayEnv) IsCLI() bool                           { return e.Mode == DisplayModeCLI }
func (e DisplayEnv) IsHeadless() bool                      { return e.Mode == DisplayModeHeadless }
func (e DisplayEnv) IsAutoDetectDisplayModeGUI() bool      { return e.Mode == DisplayModeGUI }
func (e DisplayEnv) IsAutoDetectDisplayModeTUI() bool      { return e.Mode == DisplayModeTUI }
func (e DisplayEnv) IsAutoDetectDisplayModeCLI() bool      { return e.Mode == DisplayModeCLI }
func (e DisplayEnv) IsAutoDetectDisplayModeHeadless() bool { return e.Mode == DisplayModeHeadless }
