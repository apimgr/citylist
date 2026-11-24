package main

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/citylist/src/database"
	"github.com/google/uuid"
)

func openMaintenanceTokenTestStore(t *testing.T) *database.Store {
	t.Helper()
	store, err := database.OpenSQLite(filepath.Join(t.TempDir(), "server.db"), database.DefaultPoolConfig())
	if err != nil {
		t.Fatalf("OpenSQLite() error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("EnsureSchema() error: %v", err)
	}
	return store
}

func seedMaintenanceToken(t *testing.T, store *database.Store, ownerID, kind, prefix, name string) {
	t.Helper()
	tokenID, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	err = store.Write(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO owners (id) VALUES (?) ON CONFLICT(id) DO NOTHING`, ownerID); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO api_tokens (id, owner_id, kind, name, token_hash, token_prefix, token_last4, token_enc) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, tokenID.String(), ownerID, kind, name, "hash-"+prefix, prefix, "1234", "encrypted-secret-"+prefix)
		return err
	})
	if err != nil {
		t.Fatalf("seed token %s: %v", prefix, err)
	}
}

func TestListMaintenanceTokensDoesNotRevealSecrets(t *testing.T) {
	store := openMaintenanceTokenTestStore(t)
	seedMaintenanceToken(t, store, "owner-a", "primary", "own_primary", "")
	seedMaintenanceToken(t, store, "owner-a", "linked", "own_linked", "automation")

	var output bytes.Buffer
	if err := listMaintenanceTokens(context.Background(), store, &output); err != nil {
		t.Fatalf("listMaintenanceTokens() error: %v", err)
	}
	got := output.String()
	for _, required := range []string{"prefix", "kind", "owner", "name", "created", "last used", "status", "own_primary", "own_linked", "automation"} {
		if !strings.Contains(got, required) {
			t.Errorf("token list missing %q: %s", required, got)
		}
	}
	for _, forbidden := range []string{"encrypted-secret", "hash-own_", "token_enc", "token_hash"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("token list exposed %q: %s", forbidden, got)
		}
	}
}

func TestRevokeMaintenancePrimaryRevokesOwnerTokensAndAudits(t *testing.T) {
	store := openMaintenanceTokenTestStore(t)
	seedMaintenanceToken(t, store, "owner-a", "primary", "own_primary", "")
	seedMaintenanceToken(t, store, "owner-a", "linked", "own_linked", "automation")
	seedMaintenanceToken(t, store, "owner-b", "primary", "other_primary", "")

	var output bytes.Buffer
	if err := revokeMaintenanceToken(context.Background(), store, "own_primary", true, &output); err != nil {
		t.Fatalf("revokeMaintenanceToken() error: %v", err)
	}
	if !strings.Contains(output.String(), "Revoked primary token and 1 linked token(s) for owner owner-a") {
		t.Fatalf("unexpected revoke output: %s", output.String())
	}
	var ownerAActive, ownerBActive, auditCount int
	if err := store.Read(context.Background(), func(ctx context.Context, reader database.Reader) error {
		if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id = 'owner-a' AND revoked_at IS NULL`).Scan(&ownerAActive); err != nil {
			return err
		}
		if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id = 'owner-b' AND revoked_at IS NULL`).Scan(&ownerBActive); err != nil {
			return err
		}
		return reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE action = 'token.revoked' AND target_id = 'owner-a'`).Scan(&auditCount)
	}); err != nil {
		t.Fatal(err)
	}
	if ownerAActive != 0 || ownerBActive != 1 || auditCount != 1 {
		t.Fatalf("active owner A=%d owner B=%d audit events=%d; want 0, 1, 1", ownerAActive, ownerBActive, auditCount)
	}
}

func TestRevokeMaintenanceLinkedTokenOnlyRevokesSelectedToken(t *testing.T) {
	store := openMaintenanceTokenTestStore(t)
	seedMaintenanceToken(t, store, "owner-a", "primary", "own_primary", "")
	seedMaintenanceToken(t, store, "owner-a", "linked", "own_linked", "automation")

	var output bytes.Buffer
	if err := revokeMaintenanceToken(context.Background(), store, "own_linked", true, &output); err != nil {
		t.Fatalf("revokeMaintenanceToken() error: %v", err)
	}
	var active int
	if err := store.Read(context.Background(), func(ctx context.Context, reader database.Reader) error {
		return reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE owner_id = 'owner-a' AND revoked_at IS NULL`).Scan(&active)
	}); err != nil {
		t.Fatal(err)
	}
	if active != 1 || !strings.Contains(output.String(), "Revoked linked token own_linked") {
		t.Fatalf("active tokens=%d, output=%q; want primary active and linked confirmation", active, output.String())
	}
}
