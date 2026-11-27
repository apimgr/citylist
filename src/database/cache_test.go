package database

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCacheTablesIncludeEveryApplicationTable(t *testing.T) {
	got := cacheTables()
	if len(got) != 13 {
		t.Fatalf("cache table count = %d, want 13: %v", len(got), got)
	}
	for _, required := range []string{"owners", "api_tokens", "app_secrets", "health_write_probe", "security_reports", "project_pgp_key"} {
		found := false
		for _, name := range got {
			if name == required {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("cache table list is missing %q", required)
		}
	}
}

func TestConfigureLocalCacheValidatesRemoteSettings(t *testing.T) {
	if err := (*Store)(nil).ConfigureLocalCache("cache.db", time.Second, time.Minute, false); err == nil {
		t.Fatal("nil store must be rejected")
	}
	if err := (&Store{}).ConfigureLocalCache("cache.db", time.Second, time.Minute, false); err == nil {
		t.Fatal("local database must be rejected")
	}
	remote := &Store{remote: true}
	for _, settings := range []struct {
		path      string
		syncEvery time.Duration
		maxAge    time.Duration
	}{{"", time.Second, time.Minute}, {"cache.db", 0, time.Minute}, {"cache.db", time.Second, 0}} {
		if err := remote.ConfigureLocalCache(settings.path, settings.syncEvery, settings.maxAge, false); err == nil {
			t.Errorf("invalid cache settings accepted: %+v", settings)
		}
	}
	dir := projectTempDir(t)
	if err := remote.ConfigureLocalCache(filepath.Join(dir, "configured-cache.db"), time.Second, time.Minute, true); err != nil {
		t.Fatalf("configure valid cache: %v", err)
	}
	if remote.cache == nil || remote.cache.store == nil || !remote.cache.offlineMode {
		t.Fatal("configured cache did not retain its store and offline setting")
	}
	if err := remote.cache.store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLocalCacheStateAndAvailabilityHelpers(t *testing.T) {
	dir := projectTempDir(t)
	store, err := OpenSQLite(filepath.Join(dir, "cache-state.db"), DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.writeDB.ExecContext(context.Background(), `CREATE TABLE citylist_cache_state (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	cache := &localCache{store: store, maxAge: time.Hour, lastSync: now, offline: true}
	owner := &Store{cache: cache}
	if !cache.usable(now) || !cache.hasSnapshot() || !owner.UsingOfflineCache() {
		t.Fatal("fresh synchronized offline cache should be usable")
	}
	cache.setDirty(true)
	if cache.usable(now) || cache.hasSnapshot() || owner.UsingOfflineCache() {
		t.Fatal("dirty cache should not be usable as an offline snapshot")
	}
	var dirty string
	if err := store.readDB.QueryRowContext(context.Background(), `SELECT value FROM citylist_cache_state WHERE key='dirty'`).Scan(&dirty); err != nil || dirty != "1" {
		t.Fatalf("persisted dirty state = %q, error = %v", dirty, err)
	}
	cache.setDirty(false)
	cache.setStale()
	if cache.usable(now) || cache.hasSnapshot() || owner.UsingOfflineCache() {
		t.Fatal("stale cache should not be usable")
	}
	var stamp string
	if err := store.readDB.QueryRowContext(context.Background(), `SELECT value FROM citylist_cache_state WHERE key='last_sync'`).Scan(&stamp); err != nil || stamp != "" {
		t.Fatalf("persisted sync timestamp = %q, error = %v", stamp, err)
	}
	if (&Store{}).UsingOfflineCache() {
		t.Fatal("store without cache must not claim offline-cache mode")
	}
}

func TestRemoteUnavailableClassifier(t *testing.T) {
	for _, message := range []string{"connection refused", "Connection reset", "network is unreachable", "no such host", "i/o timeout", "deadline exceeded", "temporarily unavailable", "server misbehaving", "status 503"} {
		if !isRemoteUnavailable(errors.New(message)) {
			t.Errorf("remote failure %q was not recognized", message)
		}
	}
	if isRemoteUnavailable(nil) || isRemoteUnavailable(errors.New("invalid query")) {
		t.Fatal("non-connectivity error was classified as remote unavailable")
	}
}

func TestLocalCacheDeleteRowRemovesOnlyAffectedRow(t *testing.T) {
	dir := projectTempDir(t)
	store, err := OpenSQLite(filepath.Join(dir, "cache-delete.db"), DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.writeDB.ExecContext(context.Background(), `INSERT INTO owners(id) VALUES('affected'),('unrelated')`); err != nil {
		t.Fatal(err)
	}
	cache := &localCache{store: store}
	if err := cache.deleteRow(context.Background(), "owners", []string{"id"}, []any{"affected"}); err != nil {
		t.Fatalf("delete affected row: %v", err)
	}
	var count int
	if err := store.readDB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM owners WHERE id='affected'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("affected row count = %d, error = %v", count, err)
	}
	if err := store.readDB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM owners WHERE id='unrelated'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("unrelated row count = %d, error = %v", count, err)
	}
	if err := cache.deleteRow(context.Background(), "owners; DROP TABLE owners", []string{"id"}, []any{"unrelated"}); err == nil {
		t.Fatal("unsafe table name must be rejected")
	}
}

func TestRemoteWriteSurvivesCacheRowFailureAndInvalidatesAffectedRow(t *testing.T) {
	dir := projectTempDir(t)
	remote, err := OpenSQLite(filepath.Join(dir, "remote-cache-write.db"), DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = remote.Close() }()
	for _, statement := range schemaStatements {
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(statement)), "CREATE INDEX") {
			continue
		}
		if _, err := remote.writeDB.ExecContext(context.Background(), prefixDatabaseSQL(statement)); err != nil {
			t.Fatalf("create remote schema: %v", err)
		}
	}
	remote.remote = true
	if _, err := remote.writeDB.ExecContext(context.Background(), `INSERT INTO srv_owners(id,last_seen_at) VALUES('affected',0)`); err != nil {
		t.Fatal(err)
	}
	cacheStore, err := OpenSQLite(filepath.Join(dir, "write-cache.db"), DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cacheStore.Close() }()
	if err := cacheStore.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheStore.writeDB.ExecContext(context.Background(), `CREATE TABLE citylist_cache_state (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheStore.writeDB.ExecContext(context.Background(), `INSERT INTO owners(id) VALUES('affected'),('unrelated')`); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheStore.writeDB.ExecContext(context.Background(), `CREATE TRIGGER reject_owner_cache BEFORE INSERT ON owners BEGIN SELECT RAISE(ABORT, 'cache write blocked'); END`); err != nil {
		t.Fatal(err)
	}
	cache := &localCache{store: cacheStore, maxAge: time.Hour, lastSync: time.Now().UTC()}
	remote.cache = cache
	if err := remote.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE srv_owners SET last_seen_at=1 WHERE id='affected'`)
		return err
	}); err != nil {
		t.Fatalf("remote write should remain successful when cache reconciliation fails: %v", err)
	}
	var count int
	if err := cacheStore.readDB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM owners WHERE id='affected'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("affected cached row count = %d, error = %v", count, err)
	}
	if err := cacheStore.readDB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM owners WHERE id='unrelated'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("unrelated cached row count = %d, error = %v", count, err)
	}
	if cache.dirty {
		t.Fatal("successful targeted cache deletion should not mark the cache dirty")
	}
	if err := remote.readDB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM srv_owners WHERE id='affected'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("remote write was not committed: count=%d error=%v", count, err)
	}
	if _, err := cacheStore.writeDB.ExecContext(context.Background(), `DROP TRIGGER reject_owner_cache`); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheStore.writeDB.ExecContext(context.Background(), `INSERT INTO owners(id) VALUES('affected')`); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheStore.writeDB.ExecContext(context.Background(), `CREATE TRIGGER reject_owner_cache BEFORE INSERT ON owners BEGIN SELECT RAISE(ABORT, 'cache write blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheStore.writeDB.ExecContext(context.Background(), `CREATE TRIGGER reject_owner_delete BEFORE DELETE ON owners BEGIN SELECT RAISE(ABORT, 'cache delete blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE srv_owners SET last_seen_at=2 WHERE id='affected'`)
		return err
	}); err != nil {
		t.Fatalf("second remote write should remain successful: %v", err)
	}
	if !cache.dirty || cache.usable(time.Now().UTC()) {
		t.Fatal("failed affected-row cleanup must leave the cache dirty and bypassable")
	}
}

func TestOfflineCacheServesReadsAndRejectsWrites(t *testing.T) {
	dir := projectTempDir(t)
	remote, err := OpenSQLite(filepath.Join(dir, "remote-offline.db"), DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = remote.Close() }()
	if err := remote.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	remote.remote = true
	cacheStore, err := OpenSQLite(filepath.Join(dir, "offline-cache.db"), DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := cacheStore.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheStore.writeDB.ExecContext(context.Background(), `CREATE TABLE citylist_cache_state (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheStore.writeDB.ExecContext(context.Background(), `INSERT INTO owners(id) VALUES('cached-owner')`); err != nil {
		t.Fatal(err)
	}
	cache := &localCache{store: cacheStore, maxAge: time.Hour, offlineMode: true, lastSync: time.Now().UTC(), offline: true}
	remote.cache = cache
	if err := remote.Read(context.Background(), func(ctx context.Context, reader Reader) error {
		var count int
		if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM owners WHERE id='cached-owner'`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return errors.New("offline read did not use the cached owner row")
		}
		return nil
	}); err != nil {
		t.Fatalf("offline cache read failed: %v", err)
	}
	if err := remote.Write(context.Background(), func(tx *sql.Tx) error { return nil }); err == nil || !strings.Contains(err.Error(), "offline read-only") {
		t.Fatalf("offline cache write error = %v, want read-only rejection", err)
	}
}

func TestLocalCacheSyncIsAtomicAndRemoteWins(t *testing.T) {
	dir := projectTempDir(t)
	remote, err := OpenSQLite(filepath.Join(dir, "remote.db"), DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = remote.Close() }()
	if err := remote.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, statement := range schemaStatements {
		if len(statement) >= 12 && statement[:12] == "CREATE INDEX" {
			continue
		}
		if _, err := remote.writeDB.ExecContext(context.Background(), prefixDatabaseSQL(statement)); err != nil {
			t.Fatalf("create prefixed remote schema: %v", err)
		}
	}
	remote.remote = true
	if _, err := remote.writeDB.ExecContext(context.Background(), `INSERT INTO srv_owners(id) VALUES('remote-owner')`); err != nil {
		t.Fatal(err)
	}
	cacheStore, err := OpenSQLite(filepath.Join(dir, "cache.db"), DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cacheStore.Close() }()
	if err := cacheStore.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheStore.writeDB.ExecContext(context.Background(), `CREATE TABLE citylist_cache_state (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := cacheStore.writeDB.ExecContext(context.Background(), `INSERT INTO owners(id) VALUES('stale-owner')`); err != nil {
		t.Fatal(err)
	}
	cache := &localCache{store: cacheStore}
	if err := cache.sync(context.Background(), remote); err != nil {
		t.Fatal(err)
	}
	var ownerCount int
	if err := cacheStore.readDB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM owners WHERE id='remote-owner'`).Scan(&ownerCount); err != nil {
		t.Fatal(err)
	}
	if ownerCount != 1 {
		t.Fatalf("cached owner count = %d, want 1", ownerCount)
	}
	if err := cacheStore.readDB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM owners WHERE id='stale-owner'`).Scan(&ownerCount); err != nil {
		t.Fatal(err)
	}
	if ownerCount != 0 {
		t.Fatalf("stale cached owner count = %d, want 0", ownerCount)
	}
	if _, err := remote.writeDB.ExecContext(context.Background(), `DROP TABLE srv_project_pgp_key`); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.writeDB.ExecContext(context.Background(), `INSERT INTO srv_owners(id) VALUES('second-owner')`); err != nil {
		t.Fatal(err)
	}
	if err := cache.sync(context.Background(), remote); err == nil {
		t.Fatal("expected failed reconciliation after a remote table disappeared")
	}
	if err := cacheStore.readDB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM owners WHERE id='remote-owner'`).Scan(&ownerCount); err != nil {
		t.Fatal(err)
	}
	if ownerCount != 1 {
		t.Fatalf("failed sync changed prior cache snapshot: owner count = %d", ownerCount)
	}
	if err := cacheStore.readDB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM owners WHERE id='second-owner'`).Scan(&ownerCount); err != nil {
		t.Fatal(err)
	}
	if ownerCount != 0 {
		t.Fatalf("failed sync committed new cache rows: count = %d", ownerCount)
	}
}
