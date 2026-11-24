package theme

import "testing"

func TestDetectTerminalTheme(t *testing.T) {
	for _, test := range []struct{ value, want string }{
		{"", "dark"}, {"15;0", "dark"}, {"0;7", "light"}, {"0;15", "light"},
	} {
		t.Setenv("COLORFGBG", test.value)
		if got := DetectTerminalTheme(); got != test.want {
			t.Errorf("DetectTerminalTheme(%q) = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestColorEnabledPrecedence(t *testing.T) {
	force := true
	configured := &ColorConfig{Color: false, ColorSet: true}
	if !ColorEnabled(&force, configured) {
		t.Fatal("explicit force color did not take precedence")
	}
	if ColorEnabled(nil, configured) {
		t.Fatal("explicit config color did not take precedence")
	}
	t.Setenv("NO_COLOR", "1")
	if ColorEnabled(nil) {
		t.Fatal("NO_COLOR should disable color")
	}
}
