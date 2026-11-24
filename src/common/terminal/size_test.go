package terminal

import "testing"

func TestDetectTerminalSize(t *testing.T) {
	tests := []struct {
		width, height int
		wantWidth     int
		wantHeight    int
		wantMode      SizeMode
	}{
		{0, 0, 80, 24, SizeModeStandard},
		{40, 10, 40, 10, SizeModeMinimal},
		{60, 16, 60, 16, SizeModeCompact},
		{80, 24, 80, 24, SizeModeStandard},
		{120, 40, 120, 40, SizeModeWide},
		{200, 60, 200, 60, SizeModeUltrawide},
		{400, 80, 400, 80, SizeModeMassive},
	}
	for _, test := range tests {
		got := DetectTerminalSize(test.width, test.height)
		if got.Width != test.wantWidth || got.Height != test.wantHeight || got.Mode != test.wantMode {
			t.Errorf("DetectTerminalSize(%d, %d) = %+v", test.width, test.height, got)
		}
	}
}

func TestSizeModeMethods(t *testing.T) {
	for mode, want := range map[SizeMode]string{
		SizeModeMicro: "micro", SizeModeMinimal: "minimal", SizeModeCompact: "compact",
		SizeModeStandard: "standard", SizeModeWide: "wide", SizeModeUltrawide: "ultrawide",
		SizeModeMassive: "massive", SizeMode(99): "unknown",
	} {
		if got := mode.String(); got != want {
			t.Errorf("SizeMode(%d).String() = %q, want %q", mode, got, want)
		}
		if got := mode.MaxTableColumns(); got < 2 {
			t.Errorf("SizeMode(%d).MaxTableColumns() = %d", mode, got)
		}
	}
}

func TestEnvSize(t *testing.T) {
	t.Setenv("CITYLIST_TEST_SIZE", "42")
	if got := envSize("CITYLIST_TEST_SIZE", 80); got != 42 {
		t.Fatalf("envSize() = %d, want 42", got)
	}
	for _, value := range []string{"", "zero", "0", "-1"} {
		t.Setenv("CITYLIST_TEST_SIZE", value)
		if got := envSize("CITYLIST_TEST_SIZE", 80); got != 80 {
			t.Errorf("envSize(%q) = %d, want fallback", value, got)
		}
	}
}
