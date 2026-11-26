package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

const (
	defaultReadTimeout  = 5 * time.Second
	defaultWriteTimeout = 10 * time.Second
	maxWriteAttempts    = 5
)

// Store owns the read-only and single-writer SQLite pools for one database.
type Store struct {
	writeDB *sql.DB
	readDB  *sql.DB
	remote  bool
	cache   *localCache
}

// OpenLibSQL opens bounded read and single-writer pools for a remote libSQL URL.
func OpenLibSQL(rawURL, token string, pool PoolConfig) (*Store, error) {
	if pool.MaxOpen < 1 {
		pool.MaxOpen = 25
	}
	if pool.MaxIdle < 0 {
		return nil, errors.New("database pool max_idle must not be negative")
	}
	maxLifetime, err := parseDuration(pool.MaxLifetime, 5*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("invalid database pool max_lifetime: %w", err)
	}
	maxIdleTime, err := parseDuration(pool.MaxIdleTime, time.Minute)
	if err != nil {
		return nil, fmt.Errorf("invalid database pool max_idle_time: %w", err)
	}
	writeDB, err := openLibSQLPool(rawURL, token, 1, 1, maxLifetime, maxIdleTime)
	if err != nil {
		return nil, err
	}
	readDB, err := openLibSQLPool(rawURL, token, pool.MaxOpen, pool.MaxIdle, maxLifetime, maxIdleTime)
	if err != nil {
		_ = writeDB.Close()
		return nil, err
	}
	return &Store{writeDB: writeDB, readDB: readDB, remote: true}, nil
}

// Reader exposes query operations without exposing write-capable handles.
type Reader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// PoolConfig controls the bounded reader pool.
type PoolConfig struct {
	MaxOpen     int
	MaxIdle     int
	MaxLifetime string
	MaxIdleTime string
}

// DefaultPoolConfig returns the database pool defaults from AI.md PART 10.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{MaxOpen: 25, MaxIdle: 5, MaxLifetime: "5m", MaxIdleTime: "1m"}
}

// OpenSQLite opens a local SQLite database with separate read and write pools.
func OpenSQLite(path string, pool PoolConfig) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is required")
	}
	if pool.MaxOpen < 1 {
		pool.MaxOpen = 25
	}
	if pool.MaxIdle < 0 {
		return nil, errors.New("database pool max_idle must not be negative")
	}
	maxLifetime, err := parseDuration(pool.MaxLifetime, 5*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("invalid database pool max_lifetime: %w", err)
	}
	maxIdleTime, err := parseDuration(pool.MaxIdleTime, time.Minute)
	if err != nil {
		return nil, fmt.Errorf("invalid database pool max_idle_time: %w", err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	if err := prepareDatabaseFile(absPath); err != nil {
		return nil, err
	}

	writeDSN := sqliteDSN(absPath, false)
	writeDB, err := sql.Open("sqlite", writeDSN)
	if err != nil {
		return nil, fmt.Errorf("open SQLite writer: %w", err)
	}
	writeDB.SetMaxOpenConns(1)
	writeDB.SetMaxIdleConns(1)
	writeDB.SetConnMaxLifetime(0)

	readDB, err := sql.Open("sqlite", sqliteDSN(absPath, true))
	if err != nil {
		_ = writeDB.Close()
		return nil, fmt.Errorf("open SQLite reader: %w", err)
	}
	readDB.SetMaxOpenConns(pool.MaxOpen)
	readDB.SetMaxIdleConns(min(pool.MaxOpen, pool.MaxIdle))
	readDB.SetConnMaxLifetime(maxLifetime)
	readDB.SetConnMaxIdleTime(maxIdleTime)

	store := &Store{writeDB: writeDB, readDB: readDB}
	if err := store.ping(); err != nil {
		_ = readDB.Close()
		_ = writeDB.Close()
		return nil, err
	}
	if err := store.verifyWAL(); err != nil {
		_ = readDB.Close()
		_ = writeDB.Close()
		return nil, err
	}
	return store, nil
}

func parseDuration(value string, fallback time.Duration) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < 0 {
		return 0, fmt.Errorf("must be a non-negative duration")
	}
	return duration, nil
}

func prepareDatabaseFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("secure database directory: %w", err)
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		file, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if createErr != nil && !os.IsExist(createErr) {
			return fmt.Errorf("create database file: %w", createErr)
		}
		if createErr == nil {
			if closeErr := file.Close(); closeErr != nil {
				return fmt.Errorf("close new database file: %w", closeErr)
			}
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect database file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("database path must be a regular file")
	}
	if err := os.Chmod(path, 0600); err != nil {
		return fmt.Errorf("secure database file: %w", err)
	}
	return nil
}

func sqliteDSN(path string, readOnly bool) string {
	fileURL := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	query := url.Values{}
	query.Add("_pragma", "busy_timeout(10000)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(NORMAL)")
	query.Add("_pragma", "foreign_keys(1)")
	if readOnly {
		query.Add("_pragma", "query_only(1)")
	}
	fileURL.RawQuery = query.Encode()
	if !readOnly {
		return fileURL.String() + "&_txlock=immediate"
	}
	return fileURL.String()
}

func (s *Store) ping() error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultReadTimeout)
	defer cancel()
	if err := s.writeDB.PingContext(ctx); err != nil {
		return fmt.Errorf("database writer ping failed: %w", err)
	}
	if err := s.readDB.PingContext(ctx); err != nil {
		return fmt.Errorf("database reader ping failed: %w", err)
	}
	return nil
}

func (s *Store) verifyWAL() error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultReadTimeout)
	defer cancel()
	var mode string
	if err := s.writeDB.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("check SQLite journal mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("SQLite database refused WAL mode: %s", mode)
	}
	return nil
}

// Read runs a bounded read-only operation against the reader pool.
func (s *Store) Read(ctx context.Context, fn func(context.Context, Reader) error) error {
	if fn == nil {
		return errors.New("database read callback is required")
	}
	ctx, cancel := context.WithTimeout(ctx, defaultReadTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.cache != nil {
		if s.cache.usable(time.Now()) {
			if err := fn(ctx, s.cache.store.readDB); err == nil {
				return nil
			}
		}
		err := fn(ctx, s.readDB)
		if err == nil || !s.cache.offlineMode || !isRemoteUnavailable(err) || !s.cache.hasSnapshot() {
			return err
		}
		return fn(ctx, s.cache.store.readDB)
	}
	return fn(ctx, s.readDB)
}

// HasColumn reports whether an application table contains a column.
func (s *Store) HasColumn(ctx context.Context, table, column string) (bool, error) {
	if !simpleIdentifier(table) || !simpleIdentifier(column) {
		return false, errors.New("database table and column names must be simple identifiers")
	}
	if s.remote {
		table = DatabaseObjectName(table, "libsql")
	}
	found := false
	err := s.Read(ctx, func(ctx context.Context, reader Reader) (retErr error) {
		rows, err := reader.QueryContext(ctx, "PRAGMA table_info("+table+")")
		if err != nil {
			return err
		}
		defer func() { retErr = errors.Join(retErr, rows.Close()) }()
		for rows.Next() {
			var ordinal, notNull, primaryKey int
			var name, columnType string
			var defaultValue interface{}
			if err := rows.Scan(&ordinal, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				return err
			}
			if name == column {
				found = true
			}
		}
		return rows.Err()
	})
	return found, err
}

func simpleIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character != '_' && (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

// Write runs a bounded write transaction and retries SQLite busy errors.
func (s *Store) Write(ctx context.Context, fn func(*sql.Tx) error) error {
	if fn == nil {
		return errors.New("database write callback is required")
	}
	if s.cache != nil {
		s.cache.mu.RLock()
		offline := s.cache.offline
		s.cache.mu.RUnlock()
		if offline {
			return errors.New("database is in offline read-only cache mode")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, defaultWriteTimeout)
	defer cancel()
	var err error
	for attempt := 0; attempt < maxWriteAttempts; attempt++ {
		started := time.Now()
		err = s.writeOnce(ctx, fn)
		waited := time.Since(started)
		if waited > time.Second {
			slog.Warn("db.busy", "wait_ms", waited.Milliseconds(), "caller", "database.Write")
		}
		if s.remote || !isBusy(err) {
			if err != nil && s.cache != nil && s.cache.offlineMode && isRemoteUnavailable(err) && s.cache.hasSnapshot() {
				s.cache.mu.Lock()
				s.cache.offline = true
				s.cache.mu.Unlock()
			}
			if err == nil && s.cache != nil {
				if syncErr := s.cache.sync(ctx, s); syncErr != nil {
					var rowErr *cacheRowSyncError
					var cleanupErr error
					if errors.As(syncErr, &rowErr) {
						cleanupErr = s.cache.deleteRow(ctx, rowErr.table, rowErr.primaryKey, rowErr.keyValues)
						if cleanupErr != nil {
							s.cache.setDirty(true)
						}
					} else {
						cleanupErr = s.cache.clear(ctx)
						if cleanupErr == nil {
							s.cache.setStale()
						} else {
							s.cache.setDirty(true)
						}
					}
					slog.Warn("database cache update failed after remote write", "error", syncErr, "cache_cleanup_error", cleanupErr)
				}
			}
			return err
		}
		if attempt == maxWriteAttempts-1 {
			break
		}
		wait := time.Duration(25<<attempt)*time.Millisecond + jitter(attempt)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

// Snapshot creates a consistent SQLite snapshot through the serialized write
// handle. Remote databases cannot be snapshotted through this local file API.
func (s *Store) Snapshot(ctx context.Context, destination string) error {
	if s == nil {
		return errors.New("SQLite snapshot requires a local database")
	}
	if s.remote {
		if s.cache == nil {
			return errors.New("remote database has no local cache snapshot")
		}
		return s.cache.store.Snapshot(ctx, destination)
	}
	if s.writeDB == nil {
		return errors.New("SQLite snapshot requires a local database")
	}
	if strings.TrimSpace(destination) == "" {
		return errors.New("SQLite snapshot destination is required")
	}
	destination, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve SQLite snapshot destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return fmt.Errorf("create SQLite snapshot directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(destination), 0700); err != nil {
		return fmt.Errorf("secure SQLite snapshot directory: %w", err)
	}
	if _, err := os.Lstat(destination); err == nil {
		return errors.New("SQLite snapshot destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect SQLite snapshot destination: %w", err)
	}
	quotedPath := strings.ReplaceAll(destination, "'", "''")
	if _, err := s.writeDB.ExecContext(ctx, "VACUUM INTO '"+quotedPath+"'"); err != nil {
		return fmt.Errorf("create SQLite online snapshot: %w", err)
	}
	info, err := os.Lstat(destination)
	if err != nil {
		return fmt.Errorf("inspect SQLite snapshot: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("SQLite snapshot is not a regular file")
	}
	if err := os.Chmod(destination, 0600); err != nil {
		return fmt.Errorf("secure SQLite snapshot: %w", err)
	}
	return nil
}

// HasLocalSnapshot reports whether this store has a local SQLite database that can be backed up.
func (s *Store) HasLocalSnapshot() bool {
	return s != nil && (!s.remote || s.cache != nil)
}

func (s *Store) writeOnce(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func jitter(attempt int) time.Duration {
	limit := time.Duration(25<<attempt) * time.Millisecond
	return time.Duration(rand.Int63n(int64(limit)))
}

func isBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToUpper(err.Error())
	return strings.Contains(message, "SQLITE_BUSY") || strings.Contains(message, "SQLITE_LOCKED") || strings.Contains(message, "DATABASE IS LOCKED")
}

// EnsureSchema creates the shared application tables and indexes idempotently.
func (s *Store) EnsureSchema(ctx context.Context) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		for _, statement := range schemaStatements {
			if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(statement)), "CREATE INDEX") {
				continue
			}
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply schema statement: %w", err)
			}
		}
		if err := applyCanonicalColumnRenames(ctx, tx, s.remote); err != nil {
			return err
		}
		if err := ensureSchemaColumns(ctx, tx, s.remote); err != nil {
			return err
		}
		for _, statement := range schemaStatements {
			if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(statement)), "CREATE INDEX") {
				continue
			}
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("apply schema index: %w", err)
			}
		}
		return nil
	})
}

type schemaColumnRename struct {
	table string
	old   string
	new   string
}

// Canonical renames are the scheduler column names defined by AI.md PART 10.
var canonicalSchemaRenames = []schemaColumnRename{
	{table: "scheduler_tasks", old: "task_id", new: "id"},
	{table: "scheduler_tasks", old: "task_name", new: "name"},
	{table: "scheduler_history", old: "ran_at", new: "started_at"},
}

// ensureSchemaColumns adds nullable fields introduced after the initial schema.
func ensureSchemaColumns(ctx context.Context, tx *sql.Tx, remote bool) error {
	tokenColumns, err := tableColumnNames(ctx, tx, "api_tokens", remote)
	if err != nil {
		return err
	}
	if !tokenColumns["token_last4"] {
		if _, err := tx.ExecContext(ctx, "ALTER TABLE api_tokens ADD COLUMN token_last4 TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add api_tokens.token_last4: %w", err)
		}
	}
	if err := migrateTokenIDsToUUID(ctx, tx, remote); err != nil {
		return err
	}
	reportColumns, err := tableColumnNames(ctx, tx, "security_reports", remote)
	if err != nil {
		return err
	}
	if !reportColumns["id_window"] {
		if _, err := tx.ExecContext(ctx, "ALTER TABLE security_reports ADD COLUMN id_window BIGINT NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("add security_reports.id_window: %w", err)
		}
	}
	if reportColumns["security_id"] {
		// Older versions persisted the rotating security.txt credential. Preserve
		// reports while replacing it with the report creation window and erasing
		// the credential from the legacy column.
		if _, err := tx.ExecContext(ctx, `UPDATE security_reports SET id_window = CAST(strftime('%s', created_at) AS INTEGER) / 172800 WHERE id_window = 0`); err != nil {
			return fmt.Errorf("backfill security_reports.id_window: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE security_reports SET security_id = '' WHERE security_id <> ''`); err != nil {
			return fmt.Errorf("redact legacy security report ids: %w", err)
		}
	}
	columns, err := tableColumnNames(ctx, tx, "scheduler_history", remote)
	if err != nil {
		return err
	}
	if !columns["finished_at"] {
		if _, err := tx.ExecContext(ctx, "ALTER TABLE scheduler_history ADD COLUMN finished_at INTEGER"); err != nil {
			return fmt.Errorf("add scheduler_history.finished_at: %w", err)
		}
	}
	return nil
}

func migrateTokenIDsToUUID(ctx context.Context, tx *sql.Tx, remote bool) error {
	types := make(map[string]string)
	table := DatabaseObjectName("api_tokens", map[bool]string{true: "libsql", false: "sqlite"}[remote])
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return fmt.Errorf("inspect api_tokens ID schema: %w", err)
	}
	for rows.Next() {
		var ordinal, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&ordinal, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return fmt.Errorf("read api_tokens ID schema: %w", err)
		}
		types[name] = strings.ToUpper(strings.TrimSpace(columnType))
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("read api_tokens ID schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close api_tokens ID schema: %w", err)
	}
	if types["id"] == "TEXT" {
		return nil
	}
	legacyName := DatabaseObjectName("api_tokens_legacy", map[bool]string{true: "libsql", false: "sqlite"}[remote])
	if _, err := tx.ExecContext(ctx, "ALTER TABLE api_tokens RENAME TO "+legacyName); err != nil {
		return fmt.Errorf("rename legacy api_tokens table: %w", err)
	}
	for _, statement := range schemaStatements {
		upper := strings.ToUpper(strings.TrimSpace(statement))
		if strings.HasPrefix(upper, "CREATE TABLE IF NOT EXISTS API_TOKENS (") {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("create UUID api_tokens table: %w", err)
			}
			break
		}
	}
	const query = `SELECT owner_id, kind, name, token_hash, token_prefix, token_last4, token_enc, created_at, rotated_at, expires_at, last_used_at, revoked_at, revoked_reason FROM api_tokens_legacy ORDER BY id`
	legacyRows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("read legacy api_tokens: %w", err)
	}
	columns := []string{"owner_id", "kind", "name", "token_hash", "token_prefix", "token_last4", "token_enc", "created_at", "rotated_at", "expires_at", "last_used_at", "revoked_at", "revoked_reason"}
	placeholders := make([]string, len(columns)+1)
	for i := range placeholders {
		placeholders[i] = "?"
	}
	insert := `INSERT INTO api_tokens (id,` + strings.Join(columns, ",") + `) VALUES (` + strings.Join(placeholders, ",") + `)`
	for legacyRows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(values))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := legacyRows.Scan(destinations...); err != nil {
			_ = legacyRows.Close()
			return fmt.Errorf("scan legacy api token: %w", err)
		}
		id, err := uuid.NewV7()
		if err != nil {
			_ = legacyRows.Close()
			return fmt.Errorf("generate migrated API token UUID: %w", err)
		}
		args := append([]any{id.String()}, values...)
		if _, err := tx.ExecContext(ctx, insert, args...); err != nil {
			_ = legacyRows.Close()
			return fmt.Errorf("copy legacy API token: %w", err)
		}
	}
	if err := legacyRows.Err(); err != nil {
		_ = legacyRows.Close()
		return fmt.Errorf("read legacy api_tokens: %w", err)
	}
	if err := legacyRows.Close(); err != nil {
		return fmt.Errorf("close legacy api_tokens: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DROP TABLE "+legacyName); err != nil {
		return fmt.Errorf("drop legacy api_tokens table: %w", err)
	}
	return nil
}

func applyCanonicalColumnRenames(ctx context.Context, tx *sql.Tx, remote bool) error {
	for _, rename := range canonicalSchemaRenames {
		columns, err := tableColumnNames(ctx, tx, rename.table, remote)
		if err != nil {
			return err
		}
		if !columns[rename.old] || columns[rename.new] {
			continue
		}
		statement := fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s", rename.table, rename.old, rename.new)
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("rename %s.%s to %s: %w", rename.table, rename.old, rename.new, err)
		}
	}
	return nil
}

func tableColumnNames(ctx context.Context, tx *sql.Tx, table string, remote bool) (columns map[string]bool, retErr error) {
	name := table
	if remote {
		name = DatabaseObjectName(table, "libsql")
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info("+name+")")
	if err != nil {
		return nil, fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer func() { retErr = errors.Join(retErr, rows.Close()) }()
	columns = make(map[string]bool)
	for rows.Next() {
		var ordinal, notNull, primaryKey int
		var column, columnType string
		var defaultValue interface{}
		if err := rows.Scan(&ordinal, &column, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, fmt.Errorf("read %s columns: %w", table, err)
		}
		columns[column] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read %s columns: %w", table, err)
	}
	return columns, nil
}

// Close checkpoints the WAL and closes the reader before the writer.
func (s *Store) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultWriteTimeout)
	defer cancel()
	var checkpointErr error
	if !s.remote {
		_, checkpointErr = s.writeDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	}
	var cacheErr error
	if s.cache != nil {
		s.cache.mu.RLock()
		cancel, done := s.cache.cancel, s.cache.done
		s.cache.mu.RUnlock()
		if cancel != nil {
			cancel()
			<-done
		}
		cacheErr = s.cache.store.Close()
	}
	readErr := s.readDB.Close()
	writeErr := s.writeDB.Close()
	return errors.Join(checkpointErr, cacheErr, readErr, writeErr)
}

// PoolStats reports open and in-use database connections across the read and write pools.
func (s *Store) PoolStats() (open, inUse int) {
	if s == nil {
		return 0, 0
	}
	readStats := s.readDB.Stats()
	writeStats := s.writeDB.Stats()
	return readStats.OpenConnections + writeStats.OpenConnections, readStats.InUse + writeStats.InUse
}
