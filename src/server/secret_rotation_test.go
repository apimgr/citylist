package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/database"
)

func openSecretRotationTestStore(t *testing.T) *database.Store {
	t.Helper()
	store, err := database.OpenSQLite(filepath.Join(t.TempDir(), "server.db"), database.DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err = store.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestRotateEncryptionKeyReencryptsTokenAndContactData(t *testing.T) {
	ctx := context.Background()
	store := openSecretRotationTestStore(t)
	if err := InitializeInstallationSecret(ctx, store); err != nil {
		t.Fatal(err)
	}
	old := make([]byte, 32)
	if _, err := rand.Read(old); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(old)
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "server.yml")
	if err := config.ReplaceEncryptionKey(configPath, old); err != nil {
		t.Fatal(err)
	}
	token, err := sealOwnerToken(old, []byte("own_sensitive_test_token"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := sealSecret(old, []byte("private report body"), []byte("citylist-security-report-v1"))
	if err != nil {
		t.Fatal(err)
	}
	contact, err := sealSecret(old, []byte("person@example.test"), []byte("citylist-security-report-v1"))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Write(ctx, func(tx *sql.Tx) error {
		if _, e := tx.Exec(`INSERT INTO owners(id) VALUES('owner-secret-rotation')`); e != nil {
			return e
		}
		if _, e := tx.Exec(`INSERT INTO api_tokens(id,owner_id,kind,token_hash,token_prefix,token_last4,token_enc) VALUES('0190a13f-7c00-7000-8000-000000000001','owner-secret-rotation','primary','secret-hash','secret-prefix','1234',?)`, token); e != nil {
			return e
		}
		_, e := tx.Exec(`INSERT INTO security_reports(tracking_id,id_window,severity,component,body_enc,body_alg,contact_enc,token_hash) VALUES('tracking-rotation',1,'low','test',?,'aes-256-gcm',?,'hash')`, body, contact)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if err = RotateEncryptionKey(ctx, configPath, cfg, store); err != nil {
		t.Fatal(err)
	}
	current, _ := base64.StdEncoding.DecodeString(cfg.Server.Security.EncryptionKey)
	if equalBytes(current, old) {
		t.Fatal("encryption key did not change")
	}
	if err = store.Read(ctx, func(ctx context.Context, r database.Reader) error {
		var encryptedToken string
		if e := r.QueryRowContext(ctx, `SELECT token_enc FROM api_tokens WHERE owner_id='owner-secret-rotation'`).Scan(&encryptedToken); e != nil {
			return e
		}
		plain, e := openOwnerToken(current, encryptedToken)
		if e != nil {
			return e
		}
		if string(plain) != "own_sensitive_test_token" {
			t.Errorf("decrypted token=%q", plain)
		}
		var encryptedBody, encryptedContact []byte
		if e = r.QueryRowContext(ctx, `SELECT body_enc,contact_enc FROM security_reports WHERE tracking_id='tracking-rotation'`).Scan(&encryptedBody, &encryptedContact); e != nil {
			return e
		}
		plain, e = openSecret(current, encryptedBody, []byte("citylist-security-report-v1"))
		if e != nil {
			return e
		}
		if string(plain) != "private report body" {
			t.Errorf("decrypted body=%q", plain)
		}
		plain, e = openSecret(current, encryptedContact, []byte("citylist-security-report-v1"))
		if e != nil {
			return e
		}
		if string(plain) != "person@example.test" {
			t.Errorf("decrypted contact=%q", plain)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = store.Read(context.Background(), func(ctx context.Context, r database.Reader) error {
		var until string
		return r.QueryRowContext(ctx, `SELECT value FROM app_secrets WHERE name='encryption_key_previous_until'`).Scan(&until)
	}); err != nil {
		t.Fatal(err)
	}
	var audited string
	if err = ResumeSecretRotationsWithAudit(ctx, configPath, configDir, cfg, store, func(event string) error { audited = event; return nil }); err != nil {
		t.Fatal(err)
	}
	if audited != "security.encryption_key_rotated" {
		t.Fatalf("recovery audit event=%q", audited)
	}
	if exists, err := secretExists(ctx, store, secretKeyRotationPending); err != nil || exists {
		t.Fatalf("pending encryption marker exists=%t err=%v", exists, err)
	}
}

func TestRotateInstallationSecretRetainsGraceValueAndRecovers(t *testing.T) {
	ctx := context.Background()
	store := openSecretRotationTestStore(t)
	old := make([]byte, 32)
	if _, err := rand.Read(old); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		_, e := tx.Exec(`INSERT INTO app_secrets(name,value,updated_at) VALUES('installation_secret',?,?)`, base64.StdEncoding.EncodeToString(old), now)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	encryptionKey := make([]byte, 32)
	if _, err := rand.Read(encryptionKey); err != nil {
		t.Fatal(err)
	}
	cfg.Server.Security.EncryptionKey = base64.StdEncoding.EncodeToString(encryptionKey)
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "server.yml")
	if err := config.ReplaceEncryptionKey(configPath, encryptionKey); err != nil {
		t.Fatal(err)
	}
	if err := RotateInstallationSecret(ctx, configDir, cfg, store); err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: cfg, store: store}
	candidates, err := s.installationSecretCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || !equalBytes(candidates[1], old) {
		t.Fatalf("installation secret grace candidates=%d or prior mismatch", len(candidates))
	}
	if err = ResumeSecretRotations(ctx, configPath, configDir, cfg, store); err != nil {
		t.Fatal(err)
	}
	if exists, err := secretExists(ctx, store, installationSecretRotationPending); err != nil || !exists {
		t.Fatalf("pending marker should remain until audit is durable: exists=%t err=%v", exists, err)
	}
	audited := ""
	if err = ResumeSecretRotationsWithAudit(ctx, configPath, configDir, cfg, store, func(event string) error { audited = event; return nil }); err != nil {
		t.Fatal(err)
	}
	if audited != "security.installation_secret_rotated" {
		t.Fatalf("recovery audit event=%q", audited)
	}
	if exists, err := secretExists(ctx, store, installationSecretRotationPending); err != nil || exists {
		t.Fatalf("pending installation marker exists=%t err=%v", exists, err)
	}
}
