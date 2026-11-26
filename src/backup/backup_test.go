package backup

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

type backupFixture struct {
	root       string
	tempDir    string
	configDir  string
	configFile string
	dataDir    string
	database   string
	backupDir  string
	sourceDB   string
	options    Options
}

func newBackupFixture(t *testing.T) *backupFixture {
	t.Helper()
	orgDir := filepath.Join(os.TempDir(), "apimgr")
	if err := os.MkdirAll(orgDir, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(orgDir, "citylist-backup-test-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	fixture := &backupFixture{
		root:       root,
		tempDir:    filepath.Join(root, "tmp"),
		configDir:  filepath.Join(root, "config"),
		configFile: filepath.Join(root, "config", "server.yml"),
		dataDir:    filepath.Join(root, "data"),
		database:   filepath.Join(root, "data", "db", "server.db"),
		backupDir:  filepath.Join(root, "backups"),
		sourceDB:   filepath.Join(root, "source.db"),
	}
	for _, directory := range []string{fixture.tempDir, fixture.configDir, filepath.Dir(fixture.database), fixture.backupDir} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(fixture.configFile, []byte("server:\n  mode: test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(fixture.configDir, "yaml.d"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.configDir, "yaml.d", "site.yml"), []byte("title: old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(fixture.configDir, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.configDir, "assets", "remove-me.svg"), []byte("asset"), 0600); err != nil {
		t.Fatal(err)
	}
	createBackupTestDatabase(t, fixture.sourceDB, "initial")
	if err := copyBackupTestFile(fixture.sourceDB, fixture.database); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2025, time.January, 8, 2, 0, 0, 0, time.UTC)
	fixture.options = Options{
		ProjectName:  "citylist",
		AppVersion:   "1.2.3",
		ConfigDir:    fixture.configDir,
		ConfigFile:   fixture.configFile,
		DataDir:      fixture.dataDir,
		DatabasePath: fixture.database,
		BackupDir:    fixture.backupDir,
		TempDir:      fixture.tempDir,
		Now:          func() time.Time { return now },
		SnapshotDatabase: func(_ context.Context, destination string) error {
			return copyBackupTestFile(fixture.sourceDB, destination)
		},
	}
	return fixture
}

func createBackupTestDatabase(t *testing.T, path, value string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS snapshot_data (value TEXT NOT NULL)`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM snapshot_data`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO snapshot_data(value) VALUES(?)`, value); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func copyBackupTestFile(source, destination string) (retErr error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, input.Close()) }()
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	return errors.Join(copyErr, closeErr)
}

func TestCreateAndVerifyFullBackupContents(t *testing.T) {
	fixture := newBackupFixture(t)
	name := "citylist_backup_2025-01-08_020000.tar.gz"
	manifest, err := Create(context.Background(), fixture.options, name)
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if manifest.Type != "full" || manifest.AppVersion != "1.2.3" || manifest.SnapshotAt != "2025-01-08T02:00:00Z" {
		t.Fatalf("manifest = %#v, want a full backup with configured version and snapshot time", manifest)
	}
	path := filepath.Join(fixture.backupDir, name)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("backup file mode = %04o, want 0600", info.Mode().Perm())
	}
	limits := DefaultLimits()
	limits.TempDir = fixture.tempDir
	verified, err := Verify(context.Background(), path, "", limits)
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if verified.SHA256 != manifest.SHA256 {
		t.Fatalf("verified checksum = %q, want %q", verified.SHA256, manifest.SHA256)
	}
	_, files, _, err := readArchive(context.Background(), path, "", limits)
	if err != nil {
		t.Fatalf("readArchive() error: %v", err)
	}
	for _, name := range []string{"config/server.yml", "config/yaml.d/site.yml", "config/assets/remove-me.svg", "database/server.db"} {
		if _, ok := files[name]; !ok {
			t.Errorf("backup is missing required file %q", name)
		}
	}
	if _, ok := files["data/db/server.db"]; ok {
		t.Fatal("live SQLite database was archived as optional data")
	}
}

func TestRemoteStyleBackupWithoutLocalDatabase(t *testing.T) {
	fixture := newBackupFixture(t)
	options := fixture.options
	options.DatabasePath = ""
	options.SnapshotDatabase = nil
	manifest, err := Create(context.Background(), options, "citylist_remote.tar.gz")
	if err != nil {
		t.Fatalf("Create() without a local database: %v", err)
	}
	for _, file := range manifest.Files {
		if file.Target == "database" {
			t.Fatalf("remote-style backup unexpectedly contains a database: %+v", file)
		}
	}
	limits := DefaultLimits()
	limits.TempDir = fixture.tempDir
	restoreOptions := RestoreOptions{ConfigDir: fixture.configDir, ConfigFile: fixture.configFile, DataDir: fixture.dataDir, TempDir: fixture.tempDir}
	if _, err := Restore(context.Background(), filepath.Join(fixture.backupDir, "citylist_remote.tar.gz"), "", restoreOptions, limits); err != nil {
		t.Fatalf("Restore() without a local database: %v", err)
	}
}

func TestExternalTLSFilesAreArchivedAndRestorePathsAreRewritten(t *testing.T) {
	fixture := newBackupFixture(t)
	certPath := filepath.Join(fixture.root, "managed-cert.pem")
	keyPath := filepath.Join(fixture.root, "managed-key.pem")
	if err := os.WriteFile(certPath, []byte("certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("private key"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.configFile, []byte("server:\n  ssl:\n    cert: "+certPath+"\n    key: "+keyPath+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fixture.options.ExternalTLSFiles = []ExternalTLSFile{{Name: "cert.pem", Path: certPath}, {Name: "key.pem", Path: keyPath}}
	name := "citylist_external_tls.tar.gz"
	if _, err := Create(context.Background(), fixture.options, name); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	path := filepath.Join(fixture.backupDir, name)
	limits := DefaultLimits()
	limits.TempDir = fixture.tempDir
	restoreOptions := RestoreOptions{
		ConfigDir: fixture.configDir, ConfigFile: fixture.configFile, DataDir: fixture.dataDir,
		DatabasePath: fixture.database, TempDir: fixture.tempDir,
	}
	if _, err := Restore(context.Background(), path, "", restoreOptions, limits); err != nil {
		t.Fatalf("Restore() error: %v", err)
	}
	for _, file := range []struct{ path, want string }{
		{filepath.Join(fixture.configDir, "ssl", "cert.pem"), "certificate"},
		{filepath.Join(fixture.configDir, "ssl", "key.pem"), "private key"},
	} {
		if content, err := os.ReadFile(file.path); err != nil || string(content) != file.want {
			t.Fatalf("restored TLS file %q = %q, %v; want %q", file.path, content, err, file.want)
		}
	}
	configuration, err := os.ReadFile(fixture.configFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, destination := range []string{filepath.Join(fixture.configDir, "ssl", "cert.pem"), filepath.Join(fixture.configDir, "ssl", "key.pem")} {
		if !strings.Contains(string(configuration), destination) {
			t.Errorf("restored server.yml does not reference %q: %s", destination, configuration)
		}
	}
}

func TestEncryptedBackupPasswordAndComplianceRequirements(t *testing.T) {
	fixture := newBackupFixture(t)
	fixture.options.Password = "backup-only-test-password"
	fixture.options.ComplianceEnabled = true
	path := filepath.Join(fixture.backupDir, "citylist_backup_2025-01-08_020000.tar.gz.enc")
	if _, err := Create(context.Background(), fixture.options, path); err != nil {
		t.Fatalf("Create() encrypted backup error: %v", err)
	}
	limits := DefaultLimits()
	limits.TempDir = fixture.tempDir
	if _, err := Verify(context.Background(), path, "", limits); err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("Verify() without password error = %v, want password error", err)
	}
	if _, err := Verify(context.Background(), path, "wrong-password", limits); err == nil {
		t.Fatal("Verify() accepted an incorrect password")
	}
	manifest, err := Verify(context.Background(), path, fixture.options.Password, limits)
	if err != nil {
		t.Fatalf("Verify() with correct password error: %v", err)
	}
	if !manifest.Encrypted || manifest.EncryptionMethod != "AES-256-GCM" {
		t.Fatalf("encrypted manifest = %#v, want AES-256-GCM", manifest)
	}
	unencrypted := newBackupFixture(t)
	unencrypted.options.ComplianceEnabled = true
	if _, err := Create(context.Background(), unencrypted.options, "blocked.tar.gz"); err == nil || !strings.Contains(err.Error(), "encryption_password") {
		t.Fatalf("Create() without compliance password error = %v, want setting guidance", err)
	}
}

func TestCreatePreservesExistingBackupWhenVerificationFails(t *testing.T) {
	fixture := newBackupFixture(t)
	name := "citylist_backup_2025-01-08.tar.gz"
	path := filepath.Join(fixture.backupDir, name)
	if _, err := Create(context.Background(), fixture.options, name); err != nil {
		t.Fatalf("Create() initial backup error: %v", err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fixture.options.SnapshotDatabase = func(_ context.Context, destination string) error {
		return os.WriteFile(destination, []byte("not a SQLite database"), 0600)
	}
	if _, err := Create(context.Background(), fixture.options, name); err == nil {
		t.Fatal("Create() accepted a backup with an invalid database")
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("existing backup was removed after verification failure: %v", err)
	}
	if !bytes.Equal(current, original) {
		t.Fatal("existing verified backup changed after replacement verification failed")
	}
}

func TestExtractVerifiedWritesPrivateValidatedFiles(t *testing.T) {
	fixture := newBackupFixture(t)
	archivePath := filepath.Join(fixture.backupDir, "citylist_backup_extract.tar.gz")
	if _, err := Create(context.Background(), fixture.options, archivePath); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	limits := DefaultLimits()
	limits.TempDir = fixture.tempDir
	destination := filepath.Join(fixture.root, "extracted")
	if _, err := ExtractVerified(context.Background(), archivePath, "", limits, destination); err != nil {
		t.Fatalf("ExtractVerified() error: %v", err)
	}
	path := filepath.Join(destination, "config", "server.yml")
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "server:\n  mode: test\n" {
		t.Fatalf("extracted configuration = %q, %v", contents, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("extracted configuration mode = %v, %v; want 0600", info, err)
	}
}

func TestIncrementalVerifyAndRestoreAppliesDeletion(t *testing.T) {
	fixture := newBackupFixture(t)
	baseName := "citylist_backup_2025-01-06.tar.gz"
	basePath := filepath.Join(fixture.backupDir, baseName)
	if _, err := Create(context.Background(), fixture.options, baseName); err != nil {
		t.Fatalf("Create() base error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fixture.configDir, "yaml.d", "site.yml"), []byte("title: changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(fixture.configDir, "assets", "remove-me.svg")); err != nil {
		t.Fatal(err)
	}
	createBackupTestDatabase(t, fixture.sourceDB, "changed")
	if err := os.Remove(fixture.database); err != nil {
		t.Fatal(err)
	}
	if err := copyBackupTestFile(fixture.sourceDB, fixture.database); err != nil {
		t.Fatal(err)
	}
	incrementalName := "citylist-daily.tar.gz"
	incrementalPath := filepath.Join(fixture.backupDir, incrementalName)
	snapshotAt := time.Date(2025, time.January, 8, 2, 0, 0, 0, time.UTC)
	manifest, err := CreateIncremental(context.Background(), fixture.options, incrementalName, basePath, snapshotAt)
	if err != nil {
		t.Fatalf("CreateIncremental() error: %v", err)
	}
	if manifest.Type != "incremental" || manifest.BaseBackup != baseName || manifest.SnapshotAt != snapshotAt.Format(time.RFC3339) {
		t.Fatalf("incremental manifest = %#v", manifest)
	}
	if len(manifest.Deleted) != 1 || manifest.Deleted[0] != "config/assets/remove-me.svg" {
		t.Fatalf("incremental deleted paths = %#v, want removed asset", manifest.Deleted)
	}
	limits := DefaultLimits()
	limits.TempDir = fixture.tempDir
	if _, err := Verify(context.Background(), incrementalPath, "", limits); err != nil {
		t.Fatalf("Verify() incremental error: %v", err)
	}
	restoreRoot := filepath.Join(fixture.root, "restore")
	configDir := filepath.Join(restoreRoot, "config")
	dataDir := filepath.Join(restoreRoot, "data")
	databasePath := filepath.Join(dataDir, "db", "server.db")
	options := RestoreOptions{
		ConfigDir: configDir, ConfigFile: filepath.Join(configDir, "server.yml"),
		DataDir: dataDir, DatabasePath: databasePath, TempDir: fixture.tempDir,
	}
	if _, err := Restore(context.Background(), incrementalPath, "", options, limits); err != nil {
		t.Fatalf("Restore() incremental error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "assets", "remove-me.svg")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted asset stat error = %v, want not-exist", err)
	}
	contents, err := os.ReadFile(filepath.Join(configDir, "yaml.d", "site.yml"))
	if err != nil || string(contents) != "title: changed\n" {
		t.Fatalf("restored changed config = %q, %v", contents, err)
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	var value string
	queryErr := db.QueryRow(`SELECT value FROM snapshot_data`).Scan(&value)
	closeErr := db.Close()
	if err := errors.Join(queryErr, closeErr); err != nil || value != "changed" {
		t.Fatalf("restored SQLite value = %q, %v", value, err)
	}
}

func TestIncrementalVerificationRejectsMissingOrCorruptBase(t *testing.T) {
	for _, test := range []struct {
		name   string
		remove bool
	}{
		{name: "missing base", remove: true},
		{name: "corrupt base"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newBackupFixture(t)
			baseName := "citylist_backup_2025-01-06.tar.gz"
			basePath := filepath.Join(fixture.backupDir, baseName)
			if _, err := Create(context.Background(), fixture.options, baseName); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fixture.configFile, []byte("server:\n  mode: changed\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := CreateIncremental(context.Background(), fixture.options, "citylist-daily.tar.gz", basePath, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if test.remove {
				if err := os.Remove(basePath); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(basePath, []byte("corrupt"), 0600); err != nil {
				t.Fatal(err)
			}
			limits := DefaultLimits()
			limits.TempDir = fixture.tempDir
			_, err := Verify(context.Background(), filepath.Join(fixture.backupDir, "citylist-daily.tar.gz"), "", limits)
			if err == nil || !strings.Contains(err.Error(), "ERR_BASE_") {
				t.Fatalf("Verify() base error = %v, want ERR_BASE_*", err)
			}
		})
	}
}

func TestRestoreLimitsRejectArchiveWithoutChangingTargets(t *testing.T) {
	fixture := newBackupFixture(t)
	name := "citylist_backup_2025-01-08_020000.tar.gz"
	if _, err := Create(context.Background(), fixture.options, name); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(fixture.root, "limited-restore")
	configDir := filepath.Join(root, "config")
	configFile := filepath.Join(configDir, "server.yml")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configFile, []byte("preserve-me"), 0600); err != nil {
		t.Fatal(err)
	}
	options := RestoreOptions{ConfigDir: configDir, ConfigFile: configFile, DataDir: filepath.Join(root, "data"), DatabasePath: filepath.Join(root, "data", "db", "server.db"), TempDir: fixture.tempDir}
	limits := DefaultLimits()
	limits.TempDir = fixture.tempDir
	limits.MaxFiles = 1
	if _, err := Restore(context.Background(), filepath.Join(fixture.backupDir, name), "", options, limits); err == nil {
		t.Fatal("Restore() accepted an archive exceeding MaxFiles")
	}
	contents, err := os.ReadFile(configFile)
	if err != nil || string(contents) != "preserve-me" {
		t.Fatalf("restore changed existing config despite limit failure: %q, %v", contents, err)
	}
}

func TestPruneKeepsNewestFullAndIsIdempotent(t *testing.T) {
	fixture := newBackupFixture(t)
	dates := []string{"2025-01-06", "2025-01-07", "2025-01-08"}
	for _, value := range dates {
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			t.Fatal(err)
		}
		fixture.options.Now = func() time.Time { return parsed }
		name := fmt.Sprintf("citylist_backup_%s.tar.gz", value)
		if _, err := Create(context.Background(), fixture.options, name); err != nil {
			t.Fatalf("Create(%s) error: %v", name, err)
		}
	}
	deleted, err := Prune(context.Background(), RetentionOptions{
		ProjectName: "citylist", BackupDir: fixture.backupDir, MaxBackups: 1,
		Now: time.Date(2025, time.January, 9, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Prune() error: %v", err)
	}
	if len(deleted) != 2 {
		t.Fatalf("Prune() deleted %v, want two old backups", deleted)
	}
	for _, name := range dates[:2] {
		if _, err := os.Stat(filepath.Join(fixture.backupDir, "citylist_backup_"+name+".tar.gz")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("old backup %s still exists; stat error %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(fixture.backupDir, "citylist_backup_2025-01-08.tar.gz")); err != nil {
		t.Fatalf("newest backup was pruned: %v", err)
	}
	deleted, err = Prune(context.Background(), RetentionOptions{
		ProjectName: "citylist", BackupDir: fixture.backupDir, MaxBackups: 1,
		Now: time.Date(2025, time.January, 9, 0, 0, 0, 0, time.UTC),
	})
	if err != nil || len(deleted) != 0 {
		t.Fatalf("second Prune() = %v, %v; want no-op", deleted, err)
	}
}
