package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

const cacheSyncTimeout = 2 * time.Minute

type localCache struct {
	store       *Store
	maxAge      time.Duration
	syncEvery   time.Duration
	offlineMode bool
	mu          sync.RWMutex
	lastSync    time.Time
	dirty       bool
	offline     bool
	cancel      context.CancelFunc
	done        chan struct{}
}

type cacheRowSyncError struct {
	table      string
	primaryKey []string
	keyValues  []any
	cause      error
}

func (e *cacheRowSyncError) Error() string { return e.cause.Error() }

func (e *cacheRowSyncError) Unwrap() error { return e.cause }

// ConfigureLocalCache opens a private SQLite read replica for a remote libSQL store.
func (s *Store) ConfigureLocalCache(path string, syncEvery, maxAge time.Duration, offlineMode bool) error {
	if s == nil || !s.remote {
		return errors.New("local database cache requires a remote libSQL store")
	}
	if strings.TrimSpace(path) == "" || syncEvery <= 0 || maxAge <= 0 {
		return errors.New("local database cache requires a path and positive sync and max-age intervals")
	}
	cacheStore, err := OpenSQLite(path, DefaultPoolConfig())
	if err != nil {
		return fmt.Errorf("open local database cache: %w", err)
	}
	if err := cacheStore.EnsureSchema(context.Background()); err != nil {
		_ = cacheStore.Close()
		return fmt.Errorf("initialize local database cache schema: %w", err)
	}
	if err := cacheStore.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `CREATE TABLE IF NOT EXISTS citylist_cache_state (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
		return err
	}); err != nil {
		_ = cacheStore.Close()
		return fmt.Errorf("initialize local database cache state: %w", err)
	}
	cache := &localCache{store: cacheStore, maxAge: maxAge, syncEvery: syncEvery, offlineMode: offlineMode, done: make(chan struct{})}
	var synced string
	var dirty string
	_ = cacheStore.Read(context.Background(), func(ctx context.Context, reader Reader) error {
		return reader.QueryRowContext(ctx, `SELECT value FROM citylist_cache_state WHERE key='last_sync'`).Scan(&synced)
	})
	_ = cacheStore.Read(context.Background(), func(ctx context.Context, reader Reader) error {
		return reader.QueryRowContext(ctx, `SELECT value FROM citylist_cache_state WHERE key='dirty'`).Scan(&dirty)
	})
	if timestamp, parseErr := time.Parse(time.RFC3339Nano, synced); parseErr == nil {
		cache.lastSync = timestamp
	}
	cache.dirty = dirty == "1"
	s.cache = cache
	return nil
}

// StartCacheSync performs an initial replica sync and keeps it fresh in the background.
func (s *Store) StartCacheSync(ctx context.Context) error {
	if s == nil || s.cache == nil {
		return nil
	}
	initialCtx, cancelInitial := context.WithTimeout(ctx, cacheSyncTimeout)
	s.dbMu.RLock()
	initialErr := s.cache.sync(initialCtx, s)
	s.dbMu.RUnlock()
	cancelInitial()
	if initialErr != nil {
		if !s.cache.offlineMode || !s.cache.hasSnapshot() {
			return fmt.Errorf("initial database cache sync: %w", initialErr)
		}
		s.cache.mu.Lock()
		s.cache.offline = true
		s.cache.mu.Unlock()
		slog.Warn("remote database unavailable; using existing local read cache", "error", initialErr)
	} else {
		s.cache.mu.Lock()
		s.cache.offline = false
		s.cache.mu.Unlock()
	}
	workerCtx, cancel := context.WithCancel(ctx)
	s.cache.mu.Lock()
	s.cache.cancel = cancel
	s.cache.done = make(chan struct{})
	done := s.cache.done
	s.cache.mu.Unlock()
	go func() {
		defer close(done)
		ticker := time.NewTicker(s.cache.syncEvery)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				syncCtx, stop := context.WithTimeout(workerCtx, cacheSyncTimeout)
				s.dbMu.RLock()
				if err := s.cache.sync(syncCtx, s); err != nil {
					slog.Warn("background database cache sync failed", "error", err)
					if s.cache.offlineMode && isRemoteUnavailable(err) && s.cache.hasSnapshot() {
						s.cache.mu.Lock()
						s.cache.offline = true
						s.cache.mu.Unlock()
					}
				} else {
					s.cache.mu.Lock()
					s.cache.offline = false
					s.cache.mu.Unlock()
				}
				s.dbMu.RUnlock()
				stop()
			}
		}
	}()
	return nil
}

// SyncCache reconciles a local replica once before an operation that needs a current snapshot.
func (s *Store) SyncCache(ctx context.Context) error {
	if s == nil || s.cache == nil {
		return nil
	}
	s.dbMu.RLock()
	defer s.dbMu.RUnlock()
	return s.cache.sync(ctx, s)
}

// RetryRemote reruns schema setup and replica synchronization after an offline start.
func (s *Store) RetryRemote(ctx context.Context) error {
	if s == nil || s.cache == nil || !s.remote {
		return errors.New("remote database cache is not configured")
	}
	cache := s.cache
	cache.mu.Lock()
	wasOffline := cache.offline
	cache.offline = false
	cache.mu.Unlock()
	if err := s.EnsureSchema(ctx); err != nil {
		cache.mu.Lock()
		cache.offline = wasOffline
		cache.mu.Unlock()
		return err
	}
	if err := cache.sync(ctx, s); err != nil {
		cache.mu.Lock()
		cache.offline = wasOffline
		cache.mu.Unlock()
		return err
	}
	cache.mu.Lock()
	cache.offline = false
	cache.mu.Unlock()
	return nil
}

// IsEmpty reports whether every application table in the remote database has no rows.
func (s *Store) IsEmpty(ctx context.Context) (bool, error) {
	if s == nil || !s.remote {
		return false, errors.New("remote database emptiness check requires libSQL")
	}
	empty := true
	err := s.Read(ctx, func(ctx context.Context, reader Reader) error {
		for _, table := range cacheTables() {
			var count int64
			if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "no such table") {
					continue
				}
				return fmt.Errorf("count remote table %s: %w", table, err)
			}
			if count != 0 {
				empty = false
				return nil
			}
		}
		return nil
	})
	return empty, err
}

// SeedFromSQLite copies an extracted server.db into an empty remote database only.
func (s *Store) SeedFromSQLite(ctx context.Context, path string) (bool, error) {
	if s == nil || !s.remote {
		return false, errors.New("remote database seeding requires libSQL")
	}
	source, err := OpenSQLite(path, DefaultPoolConfig())
	if err != nil {
		return false, fmt.Errorf("open restored SQLite database: %w", err)
	}
	defer func() { _ = source.Close() }()
	if err := source.EnsureSchema(ctx); err != nil {
		return false, fmt.Errorf("migrate restored SQLite database: %w", err)
	}
	seeded := false
	err = s.Write(ctx, func(tx *sql.Tx) error {
		for _, table := range cacheTables() {
			var count int64
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
				return fmt.Errorf("count remote table %s: %w", table, err)
			}
			if count != 0 {
				return nil
			}
		}
		for _, table := range cacheTables() {
			columns, err := databaseTableColumns(ctx, source.readDB, table)
			if err != nil {
				return err
			}
			rows, err := source.readDB.QueryContext(ctx, `SELECT `+strings.Join(columns, ",")+` FROM `+table)
			if err != nil {
				return fmt.Errorf("read restored table %s: %w", table, err)
			}
			placeholders := make([]string, len(columns))
			for index := range placeholders {
				placeholders[index] = "?"
			}
			insert := `INSERT INTO ` + table + ` (` + strings.Join(columns, ",") + `) VALUES (` + strings.Join(placeholders, ",") + `)`
			for rows.Next() {
				values := make([]any, len(columns))
				destinations := make([]any, len(columns))
				for index := range values {
					destinations[index] = &values[index]
				}
				if err := rows.Scan(destinations...); err != nil {
					_ = rows.Close()
					return err
				}
				if _, err := tx.ExecContext(ctx, insert, values...); err != nil {
					_ = rows.Close()
					return fmt.Errorf("seed remote table %s: %w", table, err)
				}
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				return err
			}
			if err := rows.Close(); err != nil {
				return err
			}
		}
		seeded = true
		return nil
	})
	return seeded, err
}

func (c *localCache) usable(now time.Time) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.dirty && !c.lastSync.IsZero() && now.Sub(c.lastSync) <= c.maxAge
}

func (c *localCache) hasSnapshot() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return !c.lastSync.IsZero() && !c.dirty
}

// UsingOfflineCache reports whether startup accepted an existing cache while libSQL was unavailable.
func (s *Store) UsingOfflineCache() bool {
	if s == nil {
		return false
	}
	s.dbMu.RLock()
	defer s.dbMu.RUnlock()
	if s.cache == nil {
		return false
	}
	s.cache.mu.RLock()
	defer s.cache.mu.RUnlock()
	return s.cache.offline && !s.cache.lastSync.IsZero() && !s.cache.dirty
}

// HasLocalCache reports whether this store owns a configured SQLite replica.
func (s *Store) HasLocalCache() bool {
	if s == nil {
		return false
	}
	s.dbMu.RLock()
	defer s.dbMu.RUnlock()
	return s.cache != nil
}

func (c *localCache) setDirty(dirty bool) {
	c.mu.Lock()
	c.dirty = dirty
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), defaultWriteTimeout)
	defer cancel()
	value := "0"
	if dirty {
		value = "1"
	}
	if err := c.store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO citylist_cache_state(key,value) VALUES('dirty',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, value)
		return err
	}); err != nil {
		slog.Warn("persist local cache dirty state failed", "error", err)
	}
}

func (c *localCache) setStale() {
	c.mu.Lock()
	c.lastSync = time.Time{}
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), defaultWriteTimeout)
	defer cancel()
	if err := c.store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO citylist_cache_state(key,value) VALUES('last_sync','') ON CONFLICT(key) DO UPDATE SET value=''`)
		return err
	}); err != nil {
		slog.Warn("persist local cache stale state failed", "error", err)
	}
}

func (c *localCache) sync(ctx context.Context, remote *Store) error {
	if c == nil || remote == nil || !remote.remote {
		return errors.New("cache sync requires a remote database")
	}
	tables := cacheTables()
	err := c.store.Write(ctx, func(tx *sql.Tx) error {
		remoteKeys := make(map[string]map[string][]any, len(tables))
		for _, table := range tables {
			primaryKey, err := cachePrimaryKeyColumns(ctx, tx, table)
			if err != nil {
				return err
			}
			columns, err := databaseTableColumns(ctx, tx, table)
			if err != nil {
				return err
			}
			rows, err := remote.readDB.QueryContext(ctx, `SELECT `+strings.Join(columns, ",")+` FROM `+DatabaseObjectName(table, "libsql"))
			if err != nil {
				return fmt.Errorf("read remote table %s: %w", table, err)
			}
			remoteColumns, err := rows.Columns()
			if err != nil {
				_ = rows.Close()
				return fmt.Errorf("read remote columns for %s: %w", table, err)
			}
			if strings.Join(remoteColumns, ",") != strings.Join(columns, ",") {
				_ = rows.Close()
				return fmt.Errorf("remote table %s has a different column set than the local cache", table)
			}
			for _, column := range remoteColumns {
				if !simpleIdentifier(column) {
					_ = rows.Close()
					return fmt.Errorf("remote table %s has an invalid column name", table)
				}
			}
			placeholders := make([]string, len(columns))
			for i := range placeholders {
				placeholders[i] = "?"
			}
			query := `INSERT OR REPLACE INTO ` + table + ` (` + strings.Join(columns, ",") + `) VALUES (` + strings.Join(placeholders, ",") + `)`
			remoteKeys[table] = make(map[string][]any)
			for rows.Next() {
				values := make([]any, len(columns))
				destinations := make([]any, len(columns))
				for i := range values {
					destinations[i] = &values[i]
				}
				if err := rows.Scan(destinations...); err != nil {
					_ = rows.Close()
					return fmt.Errorf("scan remote table %s: %w", table, err)
				}
				keyValues := cacheKeyValues(columns, primaryKey, values)
				key := cacheKey(keyValues)
				remoteKeys[table][key] = keyValues
				matches, err := cachedRowMatches(ctx, tx, table, columns, values)
				if err != nil {
					_ = rows.Close()
					return fmt.Errorf("compare cached table %s: %w", table, err)
				}
				if matches {
					continue
				}
				if _, err := tx.ExecContext(ctx, query, values...); err != nil {
					_ = rows.Close()
					return &cacheRowSyncError{table: table, primaryKey: primaryKey, keyValues: keyValues, cause: fmt.Errorf("write cached table %s: %w", table, err)}
				}
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				return fmt.Errorf("iterate remote table %s: %w", table, err)
			}
			if err := rows.Close(); err != nil {
				return fmt.Errorf("close remote table %s: %w", table, err)
			}
		}
		for i := len(tables) - 1; i >= 0; i-- {
			table := tables[i]
			primaryKey, err := cachePrimaryKeyColumns(ctx, tx, table)
			if err != nil {
				return err
			}
			selection := `SELECT ` + strings.Join(primaryKey, ",") + ` FROM ` + table
			localRows, err := tx.QueryContext(ctx, selection)
			if err != nil {
				return fmt.Errorf("read cached keys for %s: %w", table, err)
			}
			var stale [][]any
			for localRows.Next() {
				values := make([]any, len(primaryKey))
				destinations := make([]any, len(primaryKey))
				for index := range values {
					destinations[index] = &values[index]
				}
				if err := localRows.Scan(destinations...); err != nil {
					_ = localRows.Close()
					return fmt.Errorf("scan cached keys for %s: %w", table, err)
				}
				if _, exists := remoteKeys[table][cacheKey(values)]; !exists {
					stale = append(stale, values)
				}
			}
			if err := localRows.Err(); err != nil {
				_ = localRows.Close()
				return fmt.Errorf("iterate cached keys for %s: %w", table, err)
			}
			if err := localRows.Close(); err != nil {
				return fmt.Errorf("close cached keys for %s: %w", table, err)
			}
			conditions := make([]string, len(primaryKey))
			for index, column := range primaryKey {
				conditions[index] = column + " IS ?"
			}
			deleteQuery := `DELETE FROM ` + table + ` WHERE ` + strings.Join(conditions, " AND ")
			for _, values := range stale {
				if _, err := tx.ExecContext(ctx, deleteQuery, values...); err != nil {
					return fmt.Errorf("delete stale cached row from %s: %w", table, err)
				}
			}
		}
		stamp := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `INSERT INTO citylist_cache_state(key,value) VALUES('last_sync',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, stamp); err != nil {
			return fmt.Errorf("save cache sync timestamp: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO citylist_cache_state(key,value) VALUES('dirty','0') ON CONFLICT(key) DO UPDATE SET value='0'`); err != nil {
			return fmt.Errorf("clear cache dirty mark: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.lastSync = time.Now().UTC()
	c.dirty = false
	c.mu.Unlock()
	return nil
}

type queryContexter interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func databaseTableColumns(ctx context.Context, queryer queryContexter, table string) ([]string, error) {
	if !simpleIdentifier(table) {
		return nil, fmt.Errorf("invalid database table name %q", table)
	}
	rows, err := queryer.QueryContext(ctx, `PRAGMA table_info(`+table+")")
	if err != nil {
		return nil, fmt.Errorf("inspect database table %s: %w", table, err)
	}
	var columns []string
	for rows.Next() {
		var ordinal, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&ordinal, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !simpleIdentifier(name) {
			_ = rows.Close()
			return nil, fmt.Errorf("database table %s has an invalid column name", table)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("database table %s has no columns", table)
	}
	return columns, nil
}

func cachePrimaryKeyColumns(ctx context.Context, tx *sql.Tx, table string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(`+table+")")
	if err != nil {
		return nil, fmt.Errorf("inspect cache table %s: %w", table, err)
	}
	type keyColumn struct {
		order int
		name  string
	}
	var keys []keyColumn
	for rows.Next() {
		var ordinal, notNull, primary int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&ordinal, &name, &columnType, &notNull, &defaultValue, &primary); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if primary > 0 {
			keys = append(keys, keyColumn{order: primary, name: name})
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].order < keys[j].order })
	if len(keys) == 0 {
		return nil, fmt.Errorf("cache table %s has no primary key", table)
	}
	columns := make([]string, len(keys))
	for i := range keys {
		columns[i] = keys[i].name
	}
	return columns, nil
}

func cacheKeyValues(columns, primaryKey []string, values []any) []any {
	positions := make(map[string]int, len(columns))
	for index, column := range columns {
		positions[column] = index
	}
	keys := make([]any, len(primaryKey))
	for index, column := range primaryKey {
		keys[index] = values[positions[column]]
	}
	return keys
}

func cacheKey(values []any) string {
	var key strings.Builder
	for _, value := range values {
		fmt.Fprintf(&key, "%T:%v\x00", value, value)
	}
	return key.String()
}

func cachedRowMatches(ctx context.Context, tx *sql.Tx, table string, columns []string, values []any) (bool, error) {
	conditions := make([]string, len(columns))
	for index, column := range columns {
		conditions[index] = column + " IS ?"
	}
	query := `SELECT 1 FROM ` + table + ` WHERE ` + strings.Join(conditions, " AND ") + ` LIMIT 1`
	var exists int
	err := tx.QueryRowContext(ctx, query, values...).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (c *localCache) clear(ctx context.Context) error {
	tables := cacheTables()
	err := c.store.Write(ctx, func(tx *sql.Tx) error {
		for i := len(tables) - 1; i >= 0; i-- {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+tables[i]); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

func (c *localCache) deleteRow(ctx context.Context, table string, primaryKey []string, keyValues []any) error {
	if !simpleIdentifier(table) || len(primaryKey) == 0 || len(primaryKey) != len(keyValues) {
		return errors.New("invalid cached row identity")
	}
	conditions := make([]string, len(primaryKey))
	for index, column := range primaryKey {
		if !simpleIdentifier(column) {
			return errors.New("invalid cached row primary key")
		}
		conditions[index] = column + " IS ?"
	}
	return c.store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE `+strings.Join(conditions, " AND "), keyValues...)
		return err
	})
}

func cacheTables() []string {
	tables := make([]string, 0, len(schemaStatements))
	for _, statement := range schemaStatements {
		upper := strings.ToUpper(strings.TrimSpace(statement))
		if !strings.HasPrefix(upper, "CREATE TABLE") {
			continue
		}
		fields := strings.Fields(statement)
		if len(fields) < 6 {
			continue
		}
		name := strings.Trim(fields[5], "(`\" ")
		if name != "" {
			tables = append(tables, name)
		}
	}
	return tables
}

func isRemoteUnavailable(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"connection refused", "connection reset", "network is unreachable", "no such host", "i/o timeout", "deadline exceeded", "temporarily unavailable", "server misbehaving", "status 5"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
