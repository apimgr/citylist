package server

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/database"
)

func TestProjectPGPKeyLifecycleAndRotation(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.FQDN = "citylist.test"
	cfg.Server.Contact.Security.Email = "security@citylist.test"
	application := testServerWithConfig(t, cfg)
	manager, err := NewPGPMaintenance(t.TempDir(), application.cfg, application.store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	generated, err := manager.GenerateProjectPGPKeypair(ctx)
	if err != nil {
		t.Fatalf("GenerateProjectPGPKeypair() error: %v", err)
	}
	if len(generated.Fingerprint) != 40 {
		t.Fatalf("generated fingerprint length = %d, want 40", len(generated.Fingerprint))
	}
	private, err := manager.ExportProjectPGPPrivate(ctx)
	if err != nil {
		t.Fatalf("ExportProjectPGPPrivate() error: %v", err)
	}
	imported, err := manager.ImportProjectPGPPrivate(ctx, private, false)
	if err != nil {
		t.Fatalf("ImportProjectPGPPrivate() error: %v", err)
	}
	if imported.Fingerprint != generated.Fingerprint {
		t.Fatalf("imported fingerprint = %q, want %q", imported.Fingerprint, generated.Fingerprint)
	}

	corruptReport := []byte("not an armored PGP message")
	if err := application.store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO security_reports (tracking_id, id_window, severity, component, body_enc, body_alg, token_hash) VALUES (?, ?, ?, ?, ?, ?, ?)`, "sec_corrupt_test", 1, "low", "test", corruptReport, "pgp", strings.Repeat("b", 64))
		return err
	}); err != nil {
		t.Fatalf("insert corrupt report: %v", err)
	}
	if _, _, err := manager.RotateProjectPGPKeypair(ctx); err == nil {
		t.Fatal("rotation succeeded despite an invalid retained report")
	}
	stillActive, err := manager.LoadProjectPGPKeypair(ctx)
	if err != nil || stillActive.Fingerprint != generated.Fingerprint {
		t.Fatalf("failed rotation changed active key: %v, %v", stillActive, err)
	}
	oldPath := filepath.Join(manager.configDir, "security", "pgp.priv.old.asc.enc")
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed rotation left a staged prior private key: %v", err)
	}
	if err := application.store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM security_reports WHERE tracking_id = ?`, "sec_corrupt_test")
		return err
	}); err != nil {
		t.Fatalf("remove corrupt test report: %v", err)
	}

	body := []byte("confidential security report")
	encrypted, err := encryptPGPMessage(generated.Entity, body, generated.Entity)
	if err != nil {
		t.Fatalf("encrypt report for rotation: %v", err)
	}
	if err := application.store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO security_reports (tracking_id, id_window, severity, component, body_enc, body_alg, token_hash) VALUES (?, ?, ?, ?, ?, ?, ?)`, "sec_rotation_test", 1, "low", "test", encrypted, "pgp", strings.Repeat("a", 64))
		return err
	}); err != nil {
		t.Fatalf("insert encrypted report: %v", err)
	}

	rotated, _, err := manager.RotateProjectPGPKeypair(ctx)
	if err != nil {
		t.Fatalf("RotateProjectPGPKeypair() error: %v", err)
	}
	if rotated.Fingerprint == generated.Fingerprint {
		t.Fatal("rotation retained the original fingerprint")
	}
	loaded, err := manager.LoadProjectPGPKeypair(ctx)
	if err != nil || loaded.Fingerprint != rotated.Fingerprint {
		t.Fatalf("LoadProjectPGPKeypair() = %v, %v; want fingerprint %q", loaded, err, rotated.Fingerprint)
	}
	var rotatedBody []byte
	if err := application.store.Read(ctx, func(ctx context.Context, reader database.Reader) error {
		return reader.QueryRowContext(ctx, `SELECT body_enc FROM security_reports WHERE tracking_id = ?`, "sec_rotation_test").Scan(&rotatedBody)
	}); err != nil {
		t.Fatalf("read rotated report: %v", err)
	}
	decrypted, err := decryptProjectPGPMessage(loaded.Entity, rotatedBody)
	if err != nil || string(decrypted) != string(body) {
		t.Fatalf("rotated report decryption = %q, %v", decrypted, err)
	}

}

func TestProjectPGPEncryptSignAndResearcherKeyValidation(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.FQDN = "citylist.test"
	cfg.Server.Contact.Security.Email = "security@citylist.test"
	application := testServerWithConfig(t, cfg)
	manager, err := NewPGPMaintenance(t.TempDir(), application.cfg, application.store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key, err := manager.GenerateProjectPGPKeypair(ctx)
	if err != nil {
		t.Fatal(err)
	}
	message := []byte("private report fixture")
	encrypted, err := manager.EncryptForProjectPGP(ctx, message)
	if err != nil {
		t.Fatalf("EncryptForProjectPGP() error: %v", err)
	}
	decrypted, err := decryptProjectPGPMessage(key.Entity, encrypted)
	if err != nil || string(decrypted) != string(message) {
		t.Fatalf("project message decryption = %q, %v", decrypted, err)
	}
	signature, err := manager.SignWithProjectPGP(ctx, message)
	if err != nil || len(signature) == 0 {
		t.Fatalf("SignWithProjectPGP() returned %d bytes, %v", len(signature), err)
	}
	if _, err := manager.SignWithProjectPGP(ctx, nil); err == nil {
		t.Fatal("SignWithProjectPGP() accepted an empty message")
	}
	if err := ValidateResearcherPGPKey(ctx, string(key.PublicArmor)); err != nil {
		t.Fatalf("ValidateResearcherPGPKey() rejected the generated public key: %v", err)
	}
	if err := ValidateResearcherPGPKey(ctx, "http://keys.example.net/key.asc"); err == nil {
		t.Fatal("ValidateResearcherPGPKey() accepted an HTTP key URL")
	}
	if err := ValidateResearcherPGPKey(ctx, "https://user@example.net/key.asc"); err == nil {
		t.Fatal("ValidateResearcherPGPKey() accepted a URL with user information")
	}
	if _, err := manager.EncryptForResearcherPGP(ctx, string(key.PublicArmor), message); err != nil {
		t.Fatalf("EncryptForResearcherPGP() error: %v", err)
	}
	if err := ValidateResearcherPGPKey(ctx, "not an armored key"); err == nil {
		t.Fatal("ValidateResearcherPGPKey() accepted malformed key data")
	}
}

func TestProjectPGPDeleteAndExpiredOldKeyCleanup(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.FQDN = "citylist.test"
	cfg.Server.Contact.Security.Email = "security@citylist.test"
	application := testServerWithConfig(t, cfg)
	manager, err := NewPGPMaintenance(t.TempDir(), application.cfg, application.store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := manager.GenerateProjectPGPKeypair(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.RotateProjectPGPKeypair(ctx); err != nil {
		t.Fatalf("RotateProjectPGPKeypair() error: %v", err)
	}
	oldPath := filepath.Join(manager.configDir, "security", "pgp.priv.old.asc.enc")
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("rotation did not retain the prior private key: %v", err)
	}
	if err := manager.CleanupExpiredOldProjectPGPKey(ctx); err != nil {
		t.Fatalf("early old-key cleanup: %v", err)
	}
	if err := application.store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE project_pgp_key SET old_key_expires_at = ? WHERE id = 1`, time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.CleanupExpiredOldProjectPGPKey(ctx); err != nil {
		t.Fatalf("expired old-key cleanup: %v", err)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired old private key remains: %v", err)
	}
	var expiry sql.NullString
	if err := application.store.Read(ctx, func(ctx context.Context, reader database.Reader) error {
		return reader.QueryRowContext(ctx, `SELECT old_key_expires_at FROM project_pgp_key WHERE id = 1`).Scan(&expiry)
	}); err != nil {
		t.Fatal(err)
	}
	if expiry.Valid {
		t.Fatalf("expired old-key metadata was not cleared: %q", expiry.String)
	}
	if err := manager.DeleteProjectPGPKeypair(ctx); err != nil {
		t.Fatalf("DeleteProjectPGPKeypair() error: %v", err)
	}
	for _, path := range []string{filepath.Join(manager.configDir, "security", "pgp.priv.asc.enc"), filepath.Join(manager.configDir, "security", "pgp.pub.asc")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("deleted key file %s still exists: %v", path, err)
		}
	}
	var revoked bool
	if err := application.store.Read(ctx, func(ctx context.Context, reader database.Reader) error {
		return reader.QueryRowContext(ctx, `SELECT revoked FROM project_pgp_key WHERE id = 1`).Scan(&revoked)
	}); err != nil {
		t.Fatal(err)
	}
	if !revoked {
		t.Fatal("deleted project key metadata was not retained as revoked")
	}
}

func TestSafePGPAddressesRejectLocalAndNonRoutableRanges(t *testing.T) {
	for _, value := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		if !safePublicPGPAddress(net.ParseIP(value)) {
			t.Errorf("safePublicPGPAddress(%q) rejected a public IP", value)
		}
	}
	for _, value := range []string{"127.0.0.1", "10.0.0.1", "169.254.1.1", "100.64.0.1", "::1", "::"} {
		if safePublicPGPAddress(net.ParseIP(value)) {
			t.Errorf("safePublicPGPAddress(%q) accepted a non-public IP", value)
		}
	}
	if _, err := safePGPDialContext(context.Background(), "tcp", "127.0.0.1:443"); err == nil {
		t.Fatal("safePGPDialContext() accepted a loopback destination")
	}
}
