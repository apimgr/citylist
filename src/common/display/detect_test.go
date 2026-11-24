package display

import (
	"runtime"
	"testing"
)

func TestDisplayModeDetection(t *testing.T) {
	tests := []struct {
		name string
		env  DisplayEnv
		want DisplayMode
	}{
		{name: "headless", env: DisplayEnv{}, want: DisplayModeHeadless},
		{name: "dumb terminal", env: DisplayEnv{IsTerminal: true, TerminalType: "dumb", HasDisplay: true}, want: DisplayModeCLI},
		{name: "local GUI", env: DisplayEnv{HasDisplay: true}, want: DisplayModeGUI},
		{name: "remote terminal", env: DisplayEnv{HasDisplay: true, IsTerminal: true, IsSSH: true}, want: DisplayModeTUI},
		{name: "remote display without terminal", env: DisplayEnv{HasDisplay: true, IsSSH: true}, want: DisplayModeCLI},
		{name: "interactive terminal", env: DisplayEnv{IsTerminal: true}, want: DisplayModeTUI},
		{name: "mosh display", env: DisplayEnv{HasDisplay: true, IsTerminal: true, IsMosh: true}, want: DisplayModeTUI},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.env.autoDetectDisplayMode(); got != test.want {
				t.Fatalf("autoDetectDisplayMode() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestDisplayFlagsAndAliases(t *testing.T) {
	for _, test := range []struct {
		mode DisplayMode
		gui  bool
		tui  bool
		cli  bool
		head bool
	}{
		{mode: DisplayModeGUI, gui: true},
		{mode: DisplayModeTUI, tui: true},
		{mode: DisplayModeCLI, cli: true},
		{mode: DisplayModeHeadless, head: true},
	} {
		env := DisplayEnv{Mode: test.mode, TerminalType: "dumb"}
		if env.IsDumbTerminal() != true || env.IsGUI() != test.gui || env.IsTUI() != test.tui || env.IsCLI() != test.cli || env.IsHeadless() != test.head {
			t.Errorf("mode %v predicates returned unexpected values", test.mode)
		}
		if env.IsAutoDetectDisplayModeGUI() != test.gui || env.IsAutoDetectDisplayModeTUI() != test.tui || env.IsAutoDetectDisplayModeCLI() != test.cli || env.IsAutoDetectDisplayModeHeadless() != test.head {
			t.Errorf("mode %v aliases returned unexpected values", test.mode)
		}
	}
	normalTerminal := DisplayEnv{TerminalType: "xterm"}
	if normalTerminal.IsDumbTerminal() {
		t.Fatal("normal terminal identified as dumb")
	}
}

func TestDetectPlatformDisplay(t *testing.T) {
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	env := DisplayEnv{}
	env.detectPlatformDisplay()
	switch runtime.GOOS {
	case "windows":
		if !env.HasDisplay || env.DisplayType != "windows" {
			t.Fatalf("Windows display = %+v", env)
		}
	case "darwin":
		if !env.HasDisplay || env.DisplayType != "macos" {
			t.Fatalf("macOS display = %+v", env)
		}
	case "linux":
		if env.HasDisplay || env.DisplayType != "none" {
			t.Fatalf("headless Linux display = %+v", env)
		}
		t.Setenv("DISPLAY", ":1")
		env.detectPlatformDisplay()
		if !env.HasDisplay || env.DisplayType != "x11" {
			t.Fatalf("X11 display = %+v", env)
		}
		t.Setenv("WAYLAND_DISPLAY", "wayland-0")
		env.detectPlatformDisplay()
		if !env.HasDisplay || env.DisplayType != "wayland" {
			t.Fatalf("Wayland display = %+v", env)
		}
	default:
		if env.DisplayType != "none" {
			t.Fatalf("unsupported platform display = %+v", env)
		}
	}
}

func TestDetectDisplayEnvProducesAValidMode(t *testing.T) {
	t.Setenv("SSH_CLIENT", "")
	t.Setenv("SSH_TTY", "")
	t.Setenv("MOSH", "")
	t.Setenv("STY", "")
	t.Setenv("TMUX", "")
	env := DetectDisplayEnv()
	if env.Mode < DisplayModeHeadless || env.Mode > DisplayModeGUI {
		t.Fatalf("DetectDisplayEnv() returned invalid mode: %+v", env)
	}
}
