package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestOpenSQLiteEnsuresPrivateFileAndSchema(t *testing.T) {
	dir := projectTempDir(t)
	databasePath := filepath.Join(dir, "db", "server.db")
	store, err := OpenSQLite(databasePath, DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	info, err := os.Stat(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("database mode = %04o, want 0600", got)
	}

	ctx := context.Background()
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("second schema pass: %v", err)
	}
	if err := store.Read(ctx, func(ctx context.Context, db Reader) error {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'audit_log'`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return errors.New("audit_log table was not created")
		}
		columns, err := db.QueryContext(ctx, `PRAGMA table_info(security_reports)`)
		if err != nil {
			return err
		}
		defer columns.Close()
		hasWindow, hasSecurityID := false, false
		for columns.Next() {
			var ordinal, notNull, primaryKey int
			var name, columnType string
			var defaultValue interface{}
			if err := columns.Scan(&ordinal, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				return err
			}
			hasWindow = hasWindow || name == "id_window"
			hasSecurityID = hasSecurityID || name == "security_id"
		}
		if err := columns.Err(); err != nil {
			return err
		}
		if !hasWindow || hasSecurityID {
			return errors.New("security_reports must store the validated window without the rotating security ID")
		}
		legacyID, err := store.HasColumn(ctx, "security_reports", "security_id")
		if err != nil {
			return err
		}
		if legacyID {
			return errors.New("fresh security_reports schema must not include the legacy security_id column")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotCreatesConsistentPrivateSQLiteFile(t *testing.T) {
	root := projectTempDir(t)
	databasePath := filepath.Join(root, "server.db")
	store, err := OpenSQLite(databasePath, DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE snapshot_data (value TEXT NOT NULL)`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO snapshot_data(value) VALUES('preserved')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	snapshotPath := filepath.Join(root, "snapshot'server.db")
	if err := store.Snapshot(ctx, snapshotPath); err != nil {
		t.Fatalf("Snapshot() error: %v", err)
	}
	info, err := os.Stat(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("snapshot mode = %04o, want 0600", info.Mode().Perm())
	}
	snapshot, err := sql.Open("sqlite", snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	var value string
	if err := snapshot.QueryRowContext(ctx, `SELECT value FROM snapshot_data`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "preserved" {
		t.Fatalf("snapshot value = %q, want preserved", value)
	}
	if err := store.Snapshot(ctx, snapshotPath); err == nil {
		t.Fatal("Snapshot() accepted a pre-existing destination")
	}
}

func TestEnsureSchemaAppliesCanonicalSchedulerColumnUpdates(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(projectTempDir(t), "legacy.db"), DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE scheduler_tasks (task_id TEXT PRIMARY KEY, task_name TEXT NOT NULL)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `CREATE TABLE scheduler_history (id INTEGER PRIMARY KEY, task_id TEXT NOT NULL, ran_at INTEGER NOT NULL, status TEXT NOT NULL)`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO scheduler_tasks(task_id,task_name) VALUES('cleanup','Cleanup')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema() legacy upgrade error: %v", err)
	}
	if err := store.Read(ctx, func(ctx context.Context, reader Reader) error {
		var id, name string
		if err := reader.QueryRowContext(ctx, `SELECT id,name FROM scheduler_tasks WHERE id='cleanup'`).Scan(&id, &name); err != nil {
			return err
		}
		if id != "cleanup" || name != "Cleanup" {
			return errors.New("canonical scheduler task data was not preserved")
		}
		rows, err := reader.QueryContext(ctx, `PRAGMA table_info(scheduler_history)`)
		if err != nil {
			return err
		}
		defer rows.Close()
		columns := map[string]bool{}
		for rows.Next() {
			var ordinal, notNull, primaryKey int
			var column, columnType string
			var defaultValue interface{}
			if err := rows.Scan(&ordinal, &column, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				return err
			}
			columns[column] = true
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if !columns["started_at"] || !columns["finished_at"] || columns["ran_at"] {
			return errors.New("canonical scheduler history columns were not applied")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureSchemaMigratesIntegerTokenIDsToUUIDv7(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(projectTempDir(t), "legacy-tokens.db"), DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE owners (id TEXT PRIMARY KEY, created_at INTEGER NOT NULL DEFAULT 1, last_seen_at INTEGER NOT NULL DEFAULT 1)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `CREATE TABLE api_tokens (id INTEGER PRIMARY KEY AUTOINCREMENT, owner_id TEXT NOT NULL REFERENCES owners(id) ON DELETE CASCADE, kind TEXT NOT NULL, name TEXT, token_hash TEXT NOT NULL UNIQUE, token_prefix TEXT NOT NULL, token_enc TEXT NOT NULL, created_at INTEGER NOT NULL DEFAULT 1, rotated_at INTEGER, expires_at INTEGER, last_used_at INTEGER, revoked_at INTEGER, revoked_reason TEXT)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO owners(id) VALUES('legacy-owner')`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO api_tokens(owner_id,kind,name,token_hash,token_prefix,token_enc) VALUES('legacy-owner','linked','migration','hash','own_legacyxx','encrypted')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema() migration error: %v", err)
	}
	var id, ownerID, kind, name, hash, prefix, last4, encrypted string
	if err := store.Read(ctx, func(ctx context.Context, reader Reader) error {
		return reader.QueryRowContext(ctx, `SELECT id,owner_id,kind,name,token_hash,token_prefix,token_last4,token_enc FROM api_tokens`).Scan(&id, &ownerID, &kind, &name, &hash, &prefix, &last4, &encrypted)
	}); err != nil {
		t.Fatal(err)
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.Version() != 7 {
		t.Fatalf("migrated token ID = %q, want UUID v7 (parse error %v)", id, err)
	}
	if ownerID != "legacy-owner" || kind != "linked" || name != "migration" || hash != "hash" || prefix != "own_legacyxx" || last4 != "" || encrypted != "encrypted" {
		t.Fatalf("migrated token fields were not preserved: owner=%q kind=%q name=%q hash=%q prefix=%q last4=%q", ownerID, kind, name, hash, prefix, last4)
	}
}

func TestEnsureSchemaRedactsLegacySecurityIDs(t *testing.T) {
	store, err := OpenSQLite(filepath.Join(projectTempDir(t), "legacy-security.db"), DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	createdAt := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE security_reports (tracking_id TEXT PRIMARY KEY, security_id TEXT NOT NULL, status TEXT NOT NULL, created_at TEXT NOT NULL)`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO security_reports (tracking_id, security_id, status, created_at) VALUES ('legacy-report', '0123456789abcdef', 'received', ?)`, createdAt.Format("2006-01-02 15:04:05"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema() legacy security schema error: %v", err)
	}
	if err := store.Read(ctx, func(ctx context.Context, reader Reader) error {
		var window int64
		var secret string
		if err := reader.QueryRowContext(ctx, `SELECT id_window, security_id FROM security_reports WHERE tracking_id = 'legacy-report'`).Scan(&window, &secret); err != nil {
			return err
		}
		if window != createdAt.Unix()/172800 || secret != "" {
			return fmt.Errorf("legacy security report migration = (window %d, secret %q), want (%d, empty)", window, secret, createdAt.Unix()/172800)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	legacyID, err := store.HasColumn(ctx, "security_reports", "security_id")
	if err != nil {
		t.Fatal(err)
	}
	if !legacyID {
		t.Fatal("legacy security_id column must remain for additive compatibility")
	}
}

func TestRemoteDatabaseObjectMapping(t *testing.T) {
	if got := DatabaseObjectName("audit_log", "libsql"); got != "srv_audit_log" {
		t.Fatalf("remote table name = %q, want srv_audit_log", got)
	}
	if got := DatabaseObjectName("idx_audit_timestamp", "turso"); got != "srv_idx_audit_timestamp" {
		t.Fatalf("remote index name = %q, want srv_idx_audit_timestamp", got)
	}
	if got := DatabaseObjectName("audit_log", "sqlite"); got != "audit_log" {
		t.Fatalf("SQLite table name = %q, want audit_log", got)
	}
	query := `CREATE INDEX idx_audit_timestamp ON audit_log(timestamp) WHERE category = 'audit_log' -- audit_log\n`
	want := `CREATE INDEX srv_idx_audit_timestamp ON srv_audit_log(timestamp) WHERE category = 'audit_log' -- audit_log\n`
	if got := prefixDatabaseSQL(query); got != want {
		t.Fatalf("mapped SQL = %q, want %q", got, want)
	}
}

func TestReaderPoolRejectsWrites(t *testing.T) {
	dir := projectTempDir(t)
	pool := DefaultPoolConfig()
	pool.MaxOpen = 2
	store, err := OpenSQLite(filepath.Join(dir, "server.db"), pool)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if _, err := store.readDB.ExecContext(context.Background(), `CREATE TABLE should_not_exist (id INTEGER)`); err == nil {
		t.Fatal("read pool accepted a schema write")
	}
}

func TestConcurrentSQLiteReadersAndWriters(t *testing.T) {
	dir := projectTempDir(t)
	pool := DefaultPoolConfig()
	pool.MaxOpen = 20
	store, err := OpenSQLite(filepath.Join(dir, "server.db"), pool)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TABLE counter (id INTEGER PRIMARY KEY, value INTEGER NOT NULL)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO counter (id, value) VALUES (1, 0)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errCh := make(chan error, 40)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				err := store.Write(ctx, func(tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, `UPDATE counter SET value = value + 1 WHERE id = 1`)
					return err
				})
				if err != nil && ctx.Err() == nil {
					errCh <- err
					return
				}
			}
		}()
	}
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				err := store.Read(ctx, func(readCtx context.Context, db Reader) error {
					var value int
					return db.QueryRowContext(readCtx, `SELECT value FROM counter WHERE id = 1`).Scan(&value)
				})
				if err != nil && ctx.Err() == nil {
					errCh <- err
					return
				}
			}
		}()
	}
	<-ctx.Done()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent operation: %v", err)
	}
}

func projectTempDir(t *testing.T) string {
	t.Helper()
	root := filepath.Clean(filepath.Join("..", ".."))
	data, err := os.ReadFile(filepath.Join(root, "IDEA.md"))
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(key string) string {
		pattern := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `:\s*([^\s#]+)\s*$`)
		match := pattern.FindSubmatch(data)
		if len(match) != 2 {
			t.Fatalf("IDEA.md is missing %s", key)
		}
		return string(match[1])
	}
	base := filepath.Join(os.TempDir(), lookup("project_org"))
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, lookup("internal_name")+"-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestIsBusyRecognizesSQLiteBusyCodes(t *testing.T) {
	for _, message := range []string{"SQLITE_BUSY: busy", "SQLITE_LOCKED", "database is locked"} {
		if isBusy(errors.New(message)) {
			continue
		}
		t.Errorf("busy message %q was not recognized", message)
	}
	if isBusy(nil) || isBusy(sql.ErrNoRows) {
		t.Fatal("non-busy errors were recognized as busy")
	}
}
