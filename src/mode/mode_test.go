package mode

import (
	"errors"
	"net/http/httptest"
	"os"
	"testing"
)

func resetMode(t *testing.T) {
	t.Helper()
	mu.Lock()
	currentMode = Production
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		currentMode = Production
		mu.Unlock()
	})
}

func TestParseMode(t *testing.T) {
	tests := []struct {
		in   string
		want Mode
	}{
		{"dev", Development},
		{"development", Development},
		{"DEVELOPMENT", Development},
		{"  dev  ", Development},
		{"prod", Production},
		{"production", Production},
		{"", Production},
		{"garbage", Production},
	}
	for _, tt := range tests {
		if got := ParseMode(tt.in); got != tt.want {
			t.Errorf("ParseMode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSetAndGet(t *testing.T) {
	resetMode(t)

	Set("development")
	if got := Get(); got != Development {
		t.Errorf("Get() = %q, want %q", got, Development)
	}

	Set("production")
	if got := Get(); got != Production {
		t.Errorf("Get() = %q, want %q", got, Production)
	}
}

func TestIsDevelopmentIsProduction(t *testing.T) {
	resetMode(t)

	Set("development")
	if !IsDevelopment() {
		t.Error("IsDevelopment() = false, want true")
	}
	if IsProduction() {
		t.Error("IsProduction() = true, want false")
	}

	Set("production")
	if IsDevelopment() {
		t.Error("IsDevelopment() = true, want false")
	}
	if !IsProduction() {
		t.Error("IsProduction() = false, want true")
	}
}

func TestGetErrorDetail(t *testing.T) {
	resetMode(t)

	if got := GetErrorDetail(nil); got != "" {
		t.Errorf("GetErrorDetail(nil) = %q, want empty", got)
	}

	testErr := errors.New("boom")

	Set("development")
	if got := GetErrorDetail(testErr); got == "" {
		t.Error("GetErrorDetail() in dev mode should not be empty")
	}

	Set("production")
	got := GetErrorDetail(testErr)
	if got != "An internal error occurred. Please contact the administrator." {
		t.Errorf("GetErrorDetail() in prod mode = %q, want generic message", got)
	}
}

func TestShouldShowDebugEndpoints(t *testing.T) {
	resetMode(t)

	Set("development")
	if !ShouldShowDebugEndpoints() {
		t.Error("expected true in development mode")
	}

	Set("production")
	if ShouldShowDebugEndpoints() {
		t.Error("expected false in production mode")
	}
}

func TestGetCacheHeaders(t *testing.T) {
	resetMode(t)

	Set("development")
	devHeaders := GetCacheHeaders()
	if devHeaders["Cache-Control"] != "no-cache, no-store, must-revalidate" {
		t.Errorf("unexpected dev Cache-Control: %q", devHeaders["Cache-Control"])
	}

	Set("production")
	prodHeaders := GetCacheHeaders()
	if prodHeaders["Cache-Control"] != "public, max-age=31536000, immutable" {
		t.Errorf("unexpected prod Cache-Control: %q", prodHeaders["Cache-Control"])
	}
}

func TestApplyCacheHeaders(t *testing.T) {
	resetMode(t)
	Set("production")

	rec := httptest.NewRecorder()
	ApplyCacheHeaders(rec)

	if rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control not applied correctly: %q", rec.Header().Get("Cache-Control"))
	}
}

func TestInitialize(t *testing.T) {
	resetMode(t)

	t.Run("cli flag takes priority", func(t *testing.T) {
		os.Setenv("MODE", "production")
		defer os.Unsetenv("MODE")

		Initialize("development")
		if Get() != Development {
			t.Errorf("Initialize with CLI flag: got %q, want development", Get())
		}
	})

	t.Run("env var used when no cli flag", func(t *testing.T) {
		os.Setenv("MODE", "development")
		defer os.Unsetenv("MODE")

		Initialize("")
		if Get() != Development {
			t.Errorf("Initialize with env var: got %q, want development", Get())
		}
	})

	t.Run("defaults to production", func(t *testing.T) {
		os.Unsetenv("MODE")

		Initialize("")
		if Get() != Production {
			t.Errorf("Initialize with no input: got %q, want production", Get())
		}
	})
}

func TestModeString(t *testing.T) {
	if Development.String() != "development" {
		t.Errorf("Development.String() = %q, want development", Development.String())
	}
	if Production.String() != "production" {
		t.Errorf("Production.String() = %q, want production", Production.String())
	}
}

func TestGetLogLevel(t *testing.T) {
	resetMode(t)

	Set("development")
	if GetLogLevel() != "debug" {
		t.Errorf("GetLogLevel() in dev = %q, want debug", GetLogLevel())
	}

	Set("production")
	if GetLogLevel() != "info" {
		t.Errorf("GetLogLevel() in prod = %q, want info", GetLogLevel())
	}
}

func TestShouldCacheTemplates(t *testing.T) {
	resetMode(t)

	Set("production")
	if !ShouldCacheTemplates() {
		t.Error("expected true in production")
	}

	Set("development")
	if ShouldCacheTemplates() {
		t.Error("expected false in development")
	}
}

func TestShouldEnableAutoReload(t *testing.T) {
	resetMode(t)

	Set("development")
	if !ShouldEnableAutoReload() {
		t.Error("expected true in development")
	}

	Set("production")
	if ShouldEnableAutoReload() {
		t.Error("expected false in production")
	}
}

func TestShouldEnableProfiling(t *testing.T) {
	resetMode(t)

	Set("development")
	if !ShouldEnableProfiling() {
		t.Error("expected true in development")
	}

	Set("production")
	if ShouldEnableProfiling() {
		t.Error("expected false in production")
	}
}

func TestGetPanicRecoveryBehavior(t *testing.T) {
	resetMode(t)

	Set("development")
	if got := GetPanicRecoveryBehavior(); got != "verbose" {
		t.Errorf("GetPanicRecoveryBehavior() in dev = %q, want verbose", got)
	}

	Set("production")
	if got := GetPanicRecoveryBehavior(); got != "graceful" {
		t.Errorf("GetPanicRecoveryBehavior() in prod = %q, want graceful", got)
	}
}

func TestGetStartupMessage(t *testing.T) {
	resetMode(t)

	Set("development")
	devMsg := GetStartupMessage("1.0.0", "localhost:8080")
	if devMsg == "" {
		t.Error("GetStartupMessage() in dev mode returned empty string")
	}

	Set("production")
	prodMsg := GetStartupMessage("1.0.0", "localhost:8080")
	if prodMsg == "" {
		t.Error("GetStartupMessage() in prod mode returned empty string")
	}

	if devMsg == prodMsg {
		t.Error("expected different startup messages for dev and prod modes")
	}
}
