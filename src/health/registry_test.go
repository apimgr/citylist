package health

import (
	"errors"
	"testing"
)

func TestRegisteredChecksExposeCachedPublicStatuses(t *testing.T) {
	failed := true
	Register("test_registry", true, func() (string, error) {
		if failed {
			return "error", errors.New("private diagnostic")
		}
		return "ok", nil
	})
	if got := statusFor("test_registry"); got != "error" {
		t.Fatalf("initial status = %q, want error", got)
	}
	failed = false
	if err := Refresh(); err != nil {
		t.Fatalf("Refresh() error: %v", err)
	}
	if got := statusFor("test_registry"); got != "ok" {
		t.Fatalf("refreshed status = %q, want ok", got)
	}
}

func statusFor(key string) string {
	for _, result := range Results() {
		if result.Key == key {
			return result.Status
		}
	}
	return ""
}
