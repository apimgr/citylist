package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/server"
)

func maintenanceSecretCommand(args []string, configDir, dataDir, configPath string, cfg *config.Config) error {
	if err := authorizeTokenMaintenance(cfg); err != nil {
		return err
	}
	if len(args) != 2 || args[0] != "rotate" {
		return errors.New("usage: citylist --maintenance secret rotate {installation_secret|encryption_key}")
	}
	name := strings.TrimSpace(args[1])
	if name != "installation_secret" && name != "encryption_key" {
		return fmt.Errorf("unsupported secret rotation %q", name)
	}
	phrase := "ROTATE " + strings.ToUpper(strings.ReplaceAll(name, "_", " "))
	if err := requirePGPConfirmation("Type "+phrase+" to continue", phrase); err != nil {
		return err
	}
	store, err := openConfiguredDatabase(cfg, dataDir)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err = store.EnsureSchema(ctx); err != nil {
		return err
	}
	switch name {
	case "installation_secret":
		err = server.RotateInstallationSecret(ctx, configDir, cfg, store)
	case "encryption_key":
		err = server.RotateEncryptionKey(ctx, configPath, cfg, store)
	}
	if err != nil {
		return err
	}
	event := "security." + name + "_rotated"
	if err = writeAuditEvent(event, "warn", "success", "cli", maintenanceOperatorIP(), map[string]string{"type": "configuration", "id": "server.security." + name}, map[string]any{"reason": "operator initiated rotation"}); err != nil {
		return err
	}
	if err = server.AcknowledgeSecretRotation(ctx, store, name); err != nil {
		return err
	}
	_, err = fmt.Fprintf(os.Stdout, "Rotated %s successfully.\n", name)
	return err
}
