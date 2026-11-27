package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/backup"
	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/database"
	"github.com/apimgr/citylist/src/paths"
	"github.com/apimgr/citylist/src/server"
	"github.com/shirou/gopsutil/v3/disk"
	"golang.org/x/term"
)

var errNoFullBackup = errors.New("no full backup exists yet")

func runBackupCommand(configPath, configDir, dataDir, backupDir, outputPath string, cfg *config.Config, includeSSL, includeData bool) error {
	if cfg == nil {
		return errors.New("configuration is required for backup")
	}
	databasePath := ""
	if config.NormalizeDatabaseDriver(cfg.Server.Database.Driver) == "sqlite" {
		var err error
		databasePath, err = resolveDatabasePath(cfg.Server.Database.URL, dataDir)
		if err != nil {
			return err
		}
	} else if cfg.Server.Database.Cache.Enabled {
		var err error
		databasePath, err = resolveDatabaseCachePath(cfg.Server.Database.Cache.Path, dataDir)
		if err != nil {
			return err
		}
	}
	if err := secureBackupDirectory(backupDir); err != nil {
		return err
	}
	if err := checkBackupDiskSpace(backupDir, cfg.Server.Backup.DiskThreshold); err != nil {
		return err
	}
	store, err := openConfiguredDatabase(cfg, dataDir)
	if err != nil {
		return fmt.Errorf("open database for backup: %w", err)
	}
	defer func() { _ = store.Close() }()
	if config.NormalizeDatabaseDriver(cfg.Server.Database.Driver) == "libsql" {
		slog.Info("remote database is not included in backup", "driver", cfg.Server.Database.Driver)
		if err := store.EnsureSchema(context.Background()); err != nil {
			return fmt.Errorf("prepare remote database for backup: %w", err)
		}
		if err := store.SyncCache(context.Background()); err != nil {
			return fmt.Errorf("sync local cache before backup: %w", err)
		}
	}
	options, cleanup, err := backupOptions(configPath, configDir, dataDir, databasePath, backupDir, cfg, includeSSL, includeData, store)
	if err != nil {
		return err
	}
	defer cleanup()
	if outputPath == "" {
		extension := ".tar.gz"
		if options.Password != "" {
			extension += ".enc"
		}
		outputPath = filepath.Join(backupDir, "citylist_backup_"+time.Now().UTC().Format("2006-01-02_150405")+extension)
	} else if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(backupDir, outputPath)
	}
	if _, err := backup.Create(context.Background(), options, outputPath); err != nil {
		_ = writeAuditEvent("backup.failed", "error", "failure", "maintenance", maintenanceOperatorIP(), map[string]string{"type": "backup", "id": filepath.Base(outputPath)}, map[string]any{"error": err.Error()})
		return err
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		return err
	}
	if auditErr := writeAuditEvent("backup.created", "info", "success", "maintenance", maintenanceOperatorIP(), map[string]string{"type": "backup", "id": filepath.Base(outputPath)}, map[string]any{"filename": filepath.Base(outputPath), "size": info.Size(), "encrypted": options.Password != "", "verified": true}); auditErr != nil {
		return fmt.Errorf("backup created but audit logging failed: %w", auditErr)
	}
	if options.Password == "" {
		fmt.Fprintln(os.Stderr, serverOutputText("backup_encryption_warning", "Warning: this backup is unencrypted. Set server.backup.encryption_password in secrets.yml to protect it."))
	}
	fmt.Println(serverOutputFormat("backup_created", "Backup created and verified: {path}", map[string]string{"path": outputPath}))
	return nil
}

func runBackupTestCommand(backupPath, backupDir, configDir string, cfg *config.Config) error {
	if backupPath == "" {
		var err error
		backupPath, err = newestBackup(backupDir)
		if err != nil {
			return err
		}
	} else if !filepath.IsAbs(backupPath) {
		backupPath = filepath.Join(backupDir, backupPath)
	}
	password, err := backupPassword(backupPath)
	if err != nil {
		return err
	}
	tempDir, cleanup, err := newBackupTempDir()
	if err != nil {
		return err
	}
	defer cleanup()
	limits := restoreLimits(cfg)
	limits.TempDir = tempDir
	manifest, err := backup.Verify(context.Background(), backupPath, password, limits)
	if err != nil {
		return err
	}
	if err := verifyBackupPGP(backupPath, password, configDir, tempDir, limits); err != nil {
		return err
	}
	if err := writeAuditEvent("backup.verified", "info", "success", "maintenance", maintenanceOperatorIP(), map[string]string{"type": "backup", "id": filepath.Base(backupPath)}, map[string]any{"filename": filepath.Base(backupPath), "backup_type": manifest.Type}); err != nil {
		return err
	}
	fmt.Println(serverOutputText("backup_test_passed", "Backup integrity and security key checks passed"))
	return nil
}

func verifyBackupPGP(backupPath, password, configDir, tempDir string, limits backup.Limits) error {
	extraction := filepath.Join(tempDir, "verified")
	if _, err := backup.ExtractVerified(context.Background(), backupPath, password, limits, extraction); err != nil {
		return err
	}
	securityDir := filepath.Join(extraction, "config", "security")
	privatePath := filepath.Join(securityDir, "pgp.priv.asc.enc")
	publicPath := filepath.Join(securityDir, "pgp.pub.asc")
	_, privateErr := os.Stat(privatePath)
	_, publicErr := os.Stat(publicPath)
	if errors.Is(privateErr, fs.ErrNotExist) && errors.Is(publicErr, fs.ErrNotExist) {
		return nil
	}
	if privateErr != nil || publicErr != nil {
		return errors.New("backup has an incomplete OpenPGP keypair")
	}
	databasePath := filepath.Join(extraction, "database", "server.db")
	store, err := database.OpenSQLite(databasePath, database.DefaultPoolConfig())
	if err != nil {
		return fmt.Errorf("open backed-up database for OpenPGP verification: %w", err)
	}
	defer func() { _ = store.Close() }()
	instance, err := server.NewPGPMaintenance(filepath.Join(extraction, "config"), config.DefaultConfig(), store)
	if err != nil {
		return err
	}
	key, err := instance.LoadProjectPGPKeypair(context.Background())
	if err != nil {
		return fmt.Errorf("decrypt and verify backed-up OpenPGP private key: %w", err)
	}
	if key == nil || key.Fingerprint == "" {
		return errors.New("backed-up OpenPGP key has no fingerprint")
	}
	return nil
}

func runRestoreCommand(backupPath, configPath, configDir, dataDir, backupDir string, cfg *config.Config) error {
	password := ""
	passwordResolved := false
	if backupPath == "" {
		candidate, err := newestFullBackupCandidate(backupDir)
		if err != nil {
			return err
		}
		password, err = backupPassword(candidate)
		if err != nil {
			return err
		}
		passwordResolved = true
		tempDir, cleanup, err := newBackupTempDir()
		if err != nil {
			return err
		}
		defer cleanup()
		backupPath, _, _, err = latestFullBackup(context.Background(), backupDir, password, tempDir, cfg)
		if err != nil {
			return err
		}
	}
	if !filepath.IsAbs(backupPath) {
		backupPath = filepath.Join(backupDir, backupPath)
	}
	driver := config.NormalizeDatabaseDriver(cfg.Server.Database.Driver)
	databasePath := ""
	var remoteStore *database.Store
	remoteEmpty := false
	var err error
	if driver == "sqlite" {
		databasePath, err = resolveDatabasePath(cfg.Server.Database.URL, dataDir)
		if err != nil {
			return err
		}
		empty, err := databaseEmpty(databasePath)
		if err != nil {
			return err
		}
		if err := authorizeBackupRestore(empty, cfg); err != nil {
			return err
		}
	} else {
		remoteConfig := *cfg
		remoteConfig.Server.Database.Cache.Enabled = false
		remoteStore, err = openConfiguredDatabase(&remoteConfig, dataDir)
		if err != nil {
			return fmt.Errorf("open remote database for restore: %w", err)
		}
		defer func() { _ = remoteStore.Close() }()
		remoteEmpty, err = remoteStore.IsEmpty(context.Background())
		if err != nil {
			return err
		}
		if err := authorizeBackupRestore(remoteEmpty, cfg); err != nil {
			return err
		}
		if cfg.Server.Database.Cache.Enabled {
			databasePath, err = resolveDatabaseCachePath(cfg.Server.Database.Cache.Path, dataDir)
			if err != nil {
				return err
			}
		}
	}
	if !passwordResolved {
		password, err = backupPassword(backupPath)
		if err != nil {
			return err
		}
	}
	tempDir, cleanup, err := newBackupTempDir()
	if err != nil {
		return err
	}
	defer cleanup()
	if driver != "sqlite" && databasePath == "" {
		databasePath = filepath.Join(tempDir, "server.db")
	}
	limits := restoreLimits(cfg)
	limits.TempDir = tempDir
	options := backup.RestoreOptions{
		ConfigDir: configDir, ConfigFile: configPath, DataDir: dataDir, DatabasePath: databasePath,
		TempDir: tempDir, TLSFiles: configuredTLSFiles(cfg),
	}
	manifest, err := backup.Restore(context.Background(), backupPath, password, options, limits)
	if err != nil {
		_ = writeAuditEvent("backup.failed", "error", "failure", "maintenance", maintenanceOperatorIP(), map[string]string{"type": "restore", "id": filepath.Base(backupPath)}, map[string]any{"error": err.Error()})
		return err
	}
	if driver != "sqlite" && remoteEmpty {
		for _, file := range manifest.Files {
			if file.Target == "database" && file.Path == "server.db" {
				if err := remoteStore.EnsureSchema(context.Background()); err != nil {
					return fmt.Errorf("initialize empty remote database for restore: %w", err)
				}
				seeded, err := remoteStore.SeedFromSQLite(context.Background(), databasePath)
				if err != nil {
					return fmt.Errorf("seed empty remote database from backup: %w", err)
				}
				if seeded {
					slog.Info("restored database seeded empty remote database", "driver", cfg.Server.Database.Driver)
				}
				break
			}
		}
	}
	if manifest.AppVersion != Version {
		fmt.Fprintln(os.Stderr, serverOutputFormat("restore_version_warning", "Backup version {backup_version} differs from running version {current_version}; restored configuration and database may require migration.", map[string]string{"backup_version": manifest.AppVersion, "current_version": Version}))
	}
	if err := writeAuditEvent("backup.restored", "warn", "success", "maintenance", maintenanceOperatorIP(), map[string]string{"type": "restore", "id": filepath.Base(backupPath)}, map[string]any{"filename": filepath.Base(backupPath)}); err != nil {
		return fmt.Errorf("restore completed but audit logging failed: %w", err)
	}
	fmt.Println(serverOutputText("backup_restored", "Restore completed successfully"))
	return nil
}

func newestFullBackupCandidate(directory string) (string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", err
	}
	var selected string
	var selectedTime time.Time
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasPrefix(entry.Name(), projectName+"_backup_") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if selected == "" || info.ModTime().After(selectedTime) {
			selected = filepath.Join(directory, entry.Name())
			selectedTime = info.ModTime()
		}
	}
	if selected == "" {
		return "", errNoFullBackup
	}
	return selected, nil
}

func backupOptions(configPath, configDir, dataDir, databasePath, backupDir string, cfg *config.Config, includeSSL, includeData bool, store *database.Store) (backup.Options, func(), error) {
	tempDir, cleanup, err := newBackupTempDir()
	if err != nil {
		return backup.Options{}, func() {}, err
	}
	options := backup.Options{
		ProjectName: projectName, AppVersion: Version, ConfigDir: configDir, ConfigFile: configPath,
		DataDir: dataDir, DatabasePath: databasePath, BackupDir: backupDir, TempDir: tempDir, IncludeSSL: includeSSL,
		IncludeData: includeData, Password: cfg.Server.Backup.EncryptionPassword,
		ComplianceEnabled: complianceEnabled(cfg.Server.Compliance), TLSFiles: configuredTLSFiles(cfg),
		ExternalTLSFiles: configuredExternalTLSFiles(cfg),
	}
	if store.HasLocalSnapshot() {
		options.SnapshotDatabase = store.Snapshot
	}
	return options, cleanup, nil
}

func configuredTLSFiles(cfg *config.Config) []string {
	return []string{cfg.Server.Cache.TLSCA, cfg.Server.Cache.TLSCert, cfg.Server.Cache.TLSKey}
}

func configuredExternalTLSFiles(cfg *config.Config) []backup.ExternalTLSFile {
	return []backup.ExternalTLSFile{
		{Name: "cert.pem", Path: cfg.Server.SSL.Cert},
		{Name: "key.pem", Path: cfg.Server.SSL.Key},
	}
}

func complianceEnabled(cfg config.ComplianceConfig) bool {
	return cfg.GDPR || cfg.CCPA || cfg.HIPAA || cfg.SOC2 || cfg.PCIDSS || cfg.ISO27001 || cfg.FedRAMP || cfg.LGPD || cfg.PIPEDA || cfg.APPI || cfg.PDPA
}

func secureBackupDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	if err := os.Chmod(path, 0700); err != nil {
		return fmt.Errorf("secure backup directory: %w", err)
	}
	return nil
}

func newBackupTempDir() (string, func(), error) {
	directory, cleanup, err := paths.NewProjectTempDir(internalName)
	if err != nil {
		return "", func() {}, fmt.Errorf("create project temporary directory: %w", err)
	}
	return directory, cleanup, nil
}

func restoreLimits(cfg *config.Config) backup.Limits {
	defaults := backup.DefaultLimits()
	limits := cfg.Server.Restore
	if value, err := parseBackupByteSize(limits.MaxArchiveSize); err == nil && value > 0 {
		defaults.MaxArchiveSize = value
	}
	if value, err := parseBackupByteSize(limits.MaxExpandedSize); err == nil && value > 0 {
		defaults.MaxExpandedSize = value
	}
	if value, err := parseBackupByteSize(limits.MaxFileSize); err == nil && value > 0 {
		defaults.MaxFileSize = value
	}
	if limits.MaxFiles > 0 {
		defaults.MaxFiles = limits.MaxFiles
	}
	if limits.MaxRatio > 0 {
		defaults.MaxRatio = limits.MaxRatio
	}
	return defaults
}

func parseBackupByteSize(value string) (int64, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	units := []struct {
		suffix string
		factor int64
	}{{"TIB", 1 << 40}, {"TB", 1 << 40}, {"TI", 1 << 40}, {"T", 1 << 40}, {"GIB", 1 << 30}, {"GB", 1 << 30}, {"GI", 1 << 30}, {"G", 1 << 30}, {"MIB", 1 << 20}, {"MB", 1 << 20}, {"MI", 1 << 20}, {"M", 1 << 20}, {"KIB", 1 << 10}, {"KB", 1 << 10}, {"KI", 1 << 10}, {"K", 1 << 10}, {"B", 1}}
	factor := int64(1)
	for _, unit := range units {
		if strings.HasSuffix(value, unit.suffix) {
			factor = unit.factor
			value = strings.TrimSpace(strings.TrimSuffix(value, unit.suffix))
			break
		}
	}
	amount, err := strconv.ParseInt(value, 10, 64)
	if err != nil || amount <= 0 || amount > (1<<63-1)/factor {
		return 0, errors.New("invalid backup size")
	}
	return amount * factor, nil
}

func backupPassword(backupPath string) (string, error) {
	if !strings.HasSuffix(backupPath, ".enc") {
		return "", nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("encrypted backup requires an interactive terminal to enter its password")
	}
	return readHiddenPGPInput(serverOutputText("backup_password_prompt", "Enter backup password: "))
}

func authorizeBackupRestore(empty bool, cfg *config.Config) error {
	if empty {
		return nil
	}
	if startedElevated {
		return requirePGPConfirmation(serverOutputText("restore_confirmation_prompt", "This will overwrite the current configuration and all data. Type RESTORE to confirm."), "RESTORE")
	}
	account, err := user.Current()
	if err == nil && account.Username == projectName {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("restore authorization requires a terminal to enter the operator token")
		}
		provided, err := readHiddenPGPInput(serverOutputText("pgp_operator_token_prompt", "Operator token: "))
		if err != nil {
			return err
		}
		if cfg.Server.Token == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(cfg.Server.Token)) != 1 {
			return errors.New(serverOutputText("pgp_operator_token_rejected", "Operator token was not accepted"))
		}
		return nil
	}
	return errors.New("restore requires administrator authorization: run as root or as the service user with the operator token")
}

func databaseEmpty(path string) (empty bool, retErr error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if info.Size() == 0 {
		return true, nil
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return false, err
	}
	defer func() { retErr = errors.Join(retErr, db.Close()) }()
	for _, query := range []string{"SELECT COUNT(*) FROM owners", "SELECT COUNT(*) FROM audit_log"} {
		var count int
		if err := db.QueryRow(query).Scan(&count); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "no such table") {
				continue
			}
			return false, err
		}
		if count > 0 {
			return false, nil
		}
	}
	return true, nil
}

func newestBackup(directory string) (string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", err
	}
	var newestPath string
	var newest time.Time
	tempDir, cleanup, err := newBackupTempDir()
	if err != nil {
		return "", err
	}
	defer cleanup()
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasPrefix(entry.Name(), projectName+"_backup_") && !strings.HasPrefix(entry.Name(), projectName+"-") {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".tar.gz") && !strings.HasSuffix(entry.Name(), ".tar.gz.enc") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		limits := backup.DefaultLimits()
		limits.TempDir = tempDir
		manifest, err := backup.Verify(context.Background(), path, "", limits)
		if err != nil && !strings.HasSuffix(path, ".enc") {
			return "", err
		}
		stamp, parseErr := time.Parse(time.RFC3339, manifest.SnapshotAt)
		if parseErr != nil {
			info, statErr := entry.Info()
			if statErr != nil {
				return "", statErr
			}
			stamp = info.ModTime()
		}
		if newestPath == "" || stamp.After(newest) {
			newestPath, newest = path, stamp
		}
	}
	if newestPath == "" {
		return "", errors.New("no backup archive found")
	}
	return newestPath, nil
}

func runScheduledDailyBackup(ctx context.Context, configPath, configDir, dataDir, backupDir string, cfg *config.Config, store *database.Store) error {
	return runScheduledBackup(ctx, "daily", configPath, configDir, dataDir, backupDir, cfg, store)
}

func runScheduledHourlyBackup(ctx context.Context, configPath, configDir, dataDir, backupDir string, cfg *config.Config, store *database.Store) error {
	return runScheduledBackup(ctx, "hourly", configPath, configDir, dataDir, backupDir, cfg, store)
}

func runScheduledBackup(ctx context.Context, kind, configPath, configDir, dataDir, backupDir string, cfg *config.Config, store *database.Store) error {
	databasePath := ""
	if config.NormalizeDatabaseDriver(cfg.Server.Database.Driver) == "sqlite" {
		var err error
		databasePath, err = resolveDatabasePath(cfg.Server.Database.URL, dataDir)
		if err != nil {
			return err
		}
	} else if cfg.Server.Database.Cache.Enabled {
		var err error
		databasePath, err = resolveDatabaseCachePath(cfg.Server.Database.Cache.Path, dataDir)
		if err != nil {
			return err
		}
	}
	if err := secureBackupDirectory(backupDir); err != nil {
		return err
	}
	tempDir, cleanup, err := newBackupTempDir()
	if err != nil {
		return err
	}
	defer cleanup()
	t0 := time.Now().UTC().Truncate(time.Second)
	var databaseSnapshot string
	if store.HasLocalSnapshot() {
		if err := store.SyncCache(ctx); err != nil {
			return err
		}
		databaseSnapshot = filepath.Join(tempDir, "server.db")
		if err := store.Snapshot(ctx, databaseSnapshot); err != nil {
			return err
		}
	}
	options := backup.Options{
		ProjectName: projectName, AppVersion: Version, ConfigDir: configDir, ConfigFile: configPath,
		DataDir: dataDir, DatabasePath: databasePath, BackupDir: backupDir, TempDir: tempDir,
		Password: cfg.Server.Backup.EncryptionPassword, ComplianceEnabled: complianceEnabled(cfg.Server.Compliance),
		TLSFiles: configuredTLSFiles(cfg), ExternalTLSFiles: configuredExternalTLSFiles(cfg), Now: func() time.Time { return t0 },
	}
	if databaseSnapshot != "" {
		options.SnapshotDatabase = func(_ context.Context, destination string) error {
			return copyPrivateFile(databaseSnapshot, destination)
		}
	}
	if kind == "daily" {
		if err := checkBackupDiskSpace(backupDir, cfg.Server.Backup.DiskThreshold); err != nil {
			_ = writeAuditEvent("backup.skipped_disk_full", "error", "failure", "scheduler", "127.0.0.1", map[string]string{"type": "backup", "id": "daily"}, map[string]any{"reason": err.Error(), "threshold_percent": cfg.Server.Backup.DiskThreshold})
			return err
		}
		if base, _, _, err := latestFullBackup(ctx, backupDir, options.Password, tempDir, cfg); err == nil {
			incremental := filepath.Join(backupDir, projectName+"-daily.tar.gz")
			if _, err := backup.CreateIncremental(ctx, options, incremental, base, t0); err != nil {
				return fmt.Errorf("create daily incremental: %w", err)
			}
		} else if !errors.Is(err, errNoFullBackup) {
			return fmt.Errorf("find verified daily incremental base: %w", err)
		}
		fullPath := filepath.Join(backupDir, projectName+"_backup_"+t0.Format("2006-01-02")+".tar.gz")
		if options.Password != "" {
			fullPath += ".enc"
		}
		manifest, err := backup.Create(ctx, options, fullPath)
		if err != nil {
			return fmt.Errorf("create daily full backup: %w", err)
		}
		if _, err := backup.Verify(ctx, fullPath, options.Password, backupLimits(tempDir, cfg)); err != nil {
			_ = os.Remove(fullPath)
			return fmt.Errorf("verify daily full backup: %w", err)
		}
		if err := pruneBackups(ctx, backupDir, cfg, options.Password); err != nil {
			return err
		}
		if err := writeAuditEvent("backup.created", "info", "success", "scheduler", "127.0.0.1", map[string]string{"type": "backup", "id": filepath.Base(fullPath)}, map[string]any{"filename": filepath.Base(fullPath), "encrypted": manifest.Encrypted, "verified": true}); err != nil {
			return err
		}
		return nil
	}
	base, _, _, err := latestFullBackup(ctx, backupDir, options.Password, tempDir, cfg)
	if err != nil {
		return fmt.Errorf("hourly incremental requires a full backup: %w", err)
	}
	path := filepath.Join(backupDir, projectName+"-hourly.tar.gz")
	if options.Password != "" {
		path += ".enc"
	}
	manifest, err := backup.CreateIncremental(ctx, options, path, base, t0)
	if err != nil {
		return fmt.Errorf("create hourly incremental: %w", err)
	}
	if _, err := backup.Verify(ctx, path, options.Password, backupLimits(tempDir, cfg)); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("verify hourly incremental: %w", err)
	}
	return writeAuditEvent("backup.daily_updated", "info", "success", "scheduler", "127.0.0.1", map[string]string{"type": "backup", "id": filepath.Base(path)}, map[string]any{"filename": filepath.Base(path), "snapshot_at": manifest.SnapshotAt})
}

func copyPrivateFile(sourcePath, destinationPath string) (retErr error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, source.Close()) }()
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(destination, source)
	closeErr := destination.Close()
	if copyErr != nil {
		_ = os.Remove(destinationPath)
		return copyErr
	}
	return closeErr
}

func latestFullBackup(ctx context.Context, directory, password, tempDir string, cfg *config.Config) (string, time.Time, string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", time.Time{}, "", err
	}
	var selected string
	var selectedTime time.Time
	var selectedChecksum string
	var candidateError error
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasPrefix(entry.Name(), projectName+"_backup_") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		manifest, err := backup.Verify(ctx, path, password, backupLimits(tempDir, cfg))
		if err != nil {
			if candidateError == nil {
				candidateError = fmt.Errorf("verify candidate full backup %s: %w", entry.Name(), err)
			}
			continue
		}
		if manifest.Type != "full" {
			continue
		}
		snapshotAt, err := time.Parse(time.RFC3339, manifest.SnapshotAt)
		if err != nil {
			if candidateError == nil {
				candidateError = fmt.Errorf("parse snapshot time in %s: %w", entry.Name(), err)
			}
			continue
		}
		if selected == "" || snapshotAt.After(selectedTime) {
			selected, selectedTime, selectedChecksum = path, snapshotAt, manifest.SHA256
		}
	}
	if selected == "" {
		if candidateError != nil {
			return "", time.Time{}, "", candidateError
		}
		return "", time.Time{}, "", errNoFullBackup
	}
	return selected, selectedTime, selectedChecksum, nil
}

func backupLimits(tempDir string, cfg *config.Config) backup.Limits {
	limits := restoreLimits(cfg)
	limits.TempDir = tempDir
	return limits
}

func pruneBackups(ctx context.Context, directory string, cfg *config.Config, password string) error {
	settings := cfg.Server.Backup.Retention
	deleted, err := backup.Prune(ctx, backup.RetentionOptions{
		ProjectName: projectName, BackupDir: directory, Password: password,
		MaxBackups: settings.MaxBackups, KeepWeekly: settings.KeepWeekly, KeepMonthly: settings.KeepMonthly,
		KeepYearly: settings.KeepYearly, MaxTotal: settings.MaxTotalSize,
		OnDelete: func(name, reason string) error {
			return writeAuditEvent("backup.deleted", "info", "success", "scheduler", "127.0.0.1", map[string]string{"type": "backup", "id": name}, map[string]any{"filename": name, "reason": reason})
		},
	})
	if err != nil {
		return err
	}
	if len(deleted) > 0 {
		return writeAuditEvent("backup.retention_cleanup", "info", "success", "scheduler", "127.0.0.1", map[string]string{"type": "backup", "id": "retention"}, map[string]any{"deleted": deleted, "remaining": backupCount(directory)})
	}
	return nil
}

func backupCount(directory string) int {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), projectName+"_") && strings.Contains(entry.Name(), "backup_") {
			count++
		}
	}
	return count
}

func checkBackupDiskSpace(directory string, threshold int) error {
	usage, err := disk.Usage(directory)
	if err != nil {
		return fmt.Errorf("check backup disk space: %w", err)
	}
	if threshold <= 0 || threshold > 100 {
		threshold = 90
	}
	var latestSize int64
	entries, err := os.ReadDir(directory)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var latestTime time.Time
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(latestTime) {
			latestTime, latestSize = info.ModTime(), info.Size()
		}
	}
	if latestSize > 0 && usage.Free < uint64(latestSize)*2 {
		return fmt.Errorf("free space %d bytes is below twice the most recent backup size", usage.Free)
	}
	if usage.UsedPercent > float64(threshold) {
		return fmt.Errorf("disk usage %.1f%% exceeds threshold %d%%", usage.UsedPercent, threshold)
	}
	return nil
}

func runScheduledDiskCheck(dataDir string, cfg *config.Config, notify operatorEventNotifier) error {
	settings := cfg.Server.Notifications.DiskLow
	if !settings.Enabled {
		return nil
	}
	threshold := settings.ThresholdPercent
	if threshold < 1 || threshold > 100 {
		threshold = 85
	}
	usage, err := disk.Usage(dataDir)
	if err != nil {
		return err
	}
	statePath := filepath.Join(dataDir, "notifications", "disk-low.state")
	if err := os.MkdirAll(filepath.Dir(statePath), 0700); err != nil {
		return fmt.Errorf("create disk notification state directory: %w", err)
	}
	var previous struct {
		UsagePercent float64 `json:"usage_percent"`
		NotifiedAt   string  `json:"notified_at"`
	}
	stateBytes, _ := os.ReadFile(statePath)
	_ = json.Unmarshal(stateBytes, &previous)
	if usage.UsedPercent < float64(threshold-5) {
		if previous.NotifiedAt != "" {
			slog.Info("disk.space_recovered", "usage_percent", usage.UsedPercent, "threshold_percent", threshold)
			if err := os.Remove(statePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		return nil
	}
	if usage.UsedPercent < float64(threshold) {
		return nil
	}
	lastNotice, _ := time.Parse(time.RFC3339, previous.NotifiedAt)
	repeat, parseErr := time.ParseDuration(settings.Repeat)
	if parseErr != nil || repeat <= 0 {
		repeat = 24 * time.Hour
	}
	now := time.Now().UTC()
	if previous.NotifiedAt == "" || now.Sub(lastNotice) >= repeat {
		slog.Warn("disk.space_low", "usage_percent", usage.UsedPercent, "free_bytes", usage.Free, "threshold_percent", threshold)
		if notify != nil {
			if err := notify(context.Background(), "disk_low", map[string]string{"usage_percent": fmt.Sprintf("%.1f", usage.UsedPercent), "free_bytes": strconv.FormatUint(usage.Free, 10), "threshold_percent": strconv.Itoa(threshold)}); err != nil {
				slog.Warn("disk.notification_failed", "error", err)
			}
		}
		previous.NotifiedAt = now.Format(time.RFC3339)
	}
	previous.UsagePercent = usage.UsedPercent
	state, err := json.Marshal(previous)
	if err != nil {
		return err
	}
	return writePrivateState(statePath, state)
}
