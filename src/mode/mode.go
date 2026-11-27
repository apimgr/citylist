package mode

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
)

// appName is the application name
const appName = "citylist"

// Mode represents an application mode
type Mode string

const (
	// Production mode - optimized for security, performance, and stability
	Production Mode = "production"
	// Development mode - optimized for debugging and rapid iteration
	Development Mode = "development"
	// Debug mode adds diagnostic detail without disabling security checks.
	Debug Mode = "debug"
)

var (
	// currentMode holds the current application mode
	currentMode  Mode = Production
	debugEnabled bool
	// mu protects currentMode from concurrent access
	mu sync.RWMutex
)

// ParseMode parses a mode string and returns the corresponding Mode constant.
// Accepts: "dev", "development", "prod", "production"
// Returns Production mode if the input is invalid.
func ParseMode(s string) Mode {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "dev", "devel", "development":
		return Development
	case "debug":
		return Debug
	case "prod", "production":
		return Production
	default:
		return Production
	}
}

// Set sets the current application mode
func Set(mode string) {
	mu.Lock()
	defer mu.Unlock()
	currentMode = ParseMode(mode)
}

// Get returns the current application mode
func Get() Mode {
	mu.RLock()
	defer mu.RUnlock()
	return currentMode
}

// IsDevelopment returns true if the current mode is development
func IsDevelopment() bool {
	return Get() == Development
}

// IsDebugMode reports whether debug operating mode is selected.
func IsDebugMode() bool { return Get() == Debug }

// IsDebugEnabled reports whether diagnostic endpoints and verbose logging are enabled.
func IsDebugEnabled() bool {
	mu.RLock()
	defer mu.RUnlock()
	return debugEnabled
}

// SetDebugEnabled controls diagnostics independently of operating mode.
func SetDebugEnabled(enabled bool) {
	mu.Lock()
	debugEnabled = enabled
	mu.Unlock()
}

// IsProduction returns true if the current mode is production
func IsProduction() bool {
	return Get() == Production
}

// GetErrorDetail returns error details based on the current mode.
// In development mode, returns full error details.
// In production mode, returns a generic error message.
func GetErrorDetail(err error) string {
	if err == nil {
		return ""
	}

	if IsDevelopment() || IsDebugMode() {
		// Development mode: return full error details
		return fmt.Sprintf("%+v", err)
	}

	// Production mode: return generic error message
	return "An internal error occurred. Please contact the administrator."
}

// ShouldShowDebugEndpoints returns true if debug endpoints should be enabled
// Debug endpoints include /debug/pprof/*, /debug/vars
func ShouldShowDebugEndpoints() bool {
	return IsDebugEnabled()
}

// GetCacheHeaders returns appropriate cache headers for static files based on mode.
// Development: no-cache headers to ensure fresh content
// Production: appropriate cache headers for performance
func GetCacheHeaders() map[string]string {
	if IsDevelopment() || IsDebugMode() {
		// Development: disable caching
		return map[string]string{
			"Cache-Control": "no-cache, no-store, must-revalidate",
			"Pragma":        "no-cache",
			"Expires":       "0",
		}
	}

	// Production: enable caching (1 year for static assets)
	return map[string]string{
		"Cache-Control": "public, max-age=31536000, immutable",
	}
}

// ApplyCacheHeaders applies the appropriate cache headers to an http.ResponseWriter
func ApplyCacheHeaders(w http.ResponseWriter) {
	headers := GetCacheHeaders()
	for key, value := range headers {
		w.Header().Set(key, value)
	}
}

// Initialize sets up the mode based on priority:
// 1. --mode CLI flag (highest priority) - handled by caller
// 2. MODE environment variable
// 3. Default: production
//
// This function should be called during application startup.
// The mode from CLI flag should be passed as the parameter.
// If cliMode is empty, it will check the MODE environment variable.
func Initialize(cliMode string) {
	InitializeWithSettings(cliMode, "", false, false)
}

// InitializeWithSettings resolves mode and debug state using CLI, environment,
// config, and default precedence.
func InitializeWithSettings(cliMode, configMode string, debugFlagSet, debugFlag bool) {
	mu.Lock()
	defer mu.Unlock()

	if cliMode != "" {
		currentMode = ParseMode(cliMode)
	} else if envMode := os.Getenv("MODE"); envMode != "" {
		currentMode = ParseMode(envMode)
	} else if configMode != "" {
		currentMode = ParseMode(configMode)
	} else {
		currentMode = Production
	}

	if debugFlagSet {
		debugEnabled = debugFlag
	} else if envDebug, ok := os.LookupEnv("DEBUG"); ok && strings.TrimSpace(envDebug) != "" {
		debugEnabled = truthy(envDebug)
	} else {
		debugEnabled = currentMode == Debug
	}
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// String returns the string representation of the mode
func (m Mode) String() string {
	return string(m)
}

// GetLogLevel returns the appropriate log level for the current mode
func GetLogLevel() string {
	if IsDevelopment() || IsDebugMode() {
		return "debug"
	}
	return "info"
}

// ShouldCacheTemplates returns true if templates should be cached
func ShouldCacheTemplates() bool {
	return IsProduction() && !IsDebugMode()
}

// ShouldEnableAutoReload returns true if auto-reload should be enabled
func ShouldEnableAutoReload() bool {
	return IsDevelopment() || IsDebugMode()
}

// ShouldEnableProfiling returns true if profiling endpoints should be available
func ShouldEnableProfiling() bool {
	return IsDebugEnabled()
}

// GetPanicRecoveryBehavior returns a description of how panics should be handled
func GetPanicRecoveryBehavior() string {
	if IsDevelopment() || IsDebugMode() {
		return "verbose" // Full stack trace in response
	}
	return "graceful" // Log error, return 500, continue serving
}

// GetStartupMessage returns the appropriate startup message for the current mode
func GetStartupMessage(version, address string) string {
	icon := "🔒"
	if IsDevelopment() || IsDebugMode() {
		icon = "🔧"
	}
	debugSuffix := ""
	if IsDebugEnabled() {
		debugSuffix = " [debugging]"
	}
	return fmt.Sprintf("%s Running in mode: %s%s\n%s v%s\nListening on: %s", icon, Get(), debugSuffix, appName, version, address)
}
