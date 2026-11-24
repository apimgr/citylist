package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/apimgr/citylist/src/config"
)

func TestMaintenanceCommandValidation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("maintenance command authorization requires the root test user")
	}
	cfg := config.DefaultConfig()
	if err := maintenanceTokenCommand(nil, t.TempDir(), cfg); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("maintenanceTokenCommand() error = %v, want usage error", err)
	}
	if err := maintenanceTokenWithStore(context.Background(), []string{"list"}, nil, &bytes.Buffer{}); err == nil {
		t.Fatal("maintenanceTokenWithStore(list, nil) succeeded")
	}
	if err := maintenanceSecretCommand(nil, t.TempDir(), t.TempDir(), "", cfg); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Fatalf("maintenanceSecretCommand() error = %v, want usage error", err)
	}
	if err := maintenanceSecretCommand([]string{"rotate", "unsupported"}, t.TempDir(), t.TempDir(), "", cfg); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported secret rotation error = %v", err)
	}
}
