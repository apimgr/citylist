package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/paths"
	"github.com/apimgr/citylist/src/server"
	"github.com/shirou/gopsutil/v3/disk"
)

func probeDataDirectoryWrite(directory string) error {
	file, err := os.CreateTemp(directory, ".citylist-health-*")
	if err != nil {
		return fmt.Errorf("data directory write probe failed: %w", err)
	}
	name := file.Name()
	closeErr := file.Close()
	removeErr := os.Remove(name)
	if closeErr != nil || removeErr != nil {
		return fmt.Errorf("data directory write probe cleanup failed: %w", errors.Join(closeErr, removeErr))
	}
	return nil
}

func cleanupDiskPressure(ctx context.Context, logsDir, backupDir, dataDir string, cfg *config.Config) error {
	usage, err := disk.Usage(dataDir)
	if err != nil {
		return err
	}
	if usage.UsedPercent < float64(cfg.Server.Maintenance.Cleanup.DiskThreshold) {
		return nil
	}
	cutoff := time.Now().Add(-time.Duration(cfg.Server.Maintenance.Cleanup.LogRetentionDays) * 24 * time.Hour)
	logCount, logBytes, err := removeExpiredLogs(logsDir, cutoff)
	if err != nil {
		return err
	}
	tempCount, tempBytes, err := removeExpiredProjectTempDirs(time.Now().Add(-24 * time.Hour))
	if err != nil {
		return err
	}
	backupBefore, err := regularDirectoryBytes(backupDir)
	if err != nil {
		return err
	}
	if err := pruneBackups(ctx, backupDir, cfg, cfg.Server.Backup.EncryptionPassword); err != nil {
		return err
	}
	backupAfter, err := regularDirectoryBytes(backupDir)
	if err != nil {
		return err
	}
	backupBytes := max(int64(0), backupBefore-backupAfter)
	slog.Info("server.maintenance_cleanup_complete", "logs_removed", logCount, "temp_directories_removed", tempCount, "space_freed_bytes", logBytes+tempBytes+backupBytes)
	return nil
}

func removeExpiredLogs(directory string, cutoff time.Time) (int, int64, error) {
	active := make(map[string]struct{})
	auditPath := "audit.log"
	activeLogs.RLock()
	logs := activeLogs.value
	activeLogs.RUnlock()
	if logs != nil {
		logs.mu.Lock()
		for _, destination := range logs.files {
			active[filepath.Clean(destination.path)] = struct{}{}
		}
		if audit := logs.files["audit"]; audit != nil {
			auditPath = filepath.Base(audit.path)
		}
		logs.mu.Unlock()
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	removed := 0
	var freed int64
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := entry.Name()
		if !strings.Contains(name, ".log") {
			continue
		}
		if auditPath != "" && (name == auditPath || strings.HasPrefix(name, auditPath+".")) {
			continue
		}
		path := filepath.Join(directory, name)
		if _, isActive := active[filepath.Clean(path)]; isActive {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return removed, freed, err
		}
		if info.Mode().IsRegular() && info.ModTime().Before(cutoff) {
			if err := os.Remove(path); err != nil {
				return removed, freed, err
			}
			removed++
			freed += info.Size()
		}
	}
	return removed, freed, nil
}

func removeExpiredProjectTempDirs(cutoff time.Time) (int, int64, error) {
	organization := paths.ProjectOrganization()
	if organization == "" {
		return 0, 0, nil
	}
	base := filepath.Join(os.TempDir(), organization)
	return removeExpiredProjectTempDirsAt(base, internalName, cutoff)
}

func removeExpiredProjectTempDirsAt(base, prefix string, cutoff time.Time) (int, int64, error) {
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	removed := 0
	var freed int64
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasPrefix(entry.Name(), prefix+"-") {
			continue
		}
		path := filepath.Join(base, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return removed, freed, err
		}
		if info.ModTime().Before(cutoff) {
			size, err := regularDirectoryBytes(path)
			if err != nil {
				return removed, freed, err
			}
			if err := os.RemoveAll(path); err != nil {
				return removed, freed, err
			}
			removed++
			freed += size
		}
	}
	return removed, freed, nil
}

func regularDirectoryBytes(directory string) (int64, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	var total int64
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		if entry.IsDir() {
			size, err := regularDirectoryBytes(path)
			if err != nil {
				return total, err
			}
			total += size
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return total, err
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
	}
	return total, nil
}

func notifyMaintenanceTransition(ctx context.Context, cfg *config.Config, notify operatorEventNotifier, transition server.MaintenanceTransition) error {
	if transition.Entered {
		return recordMaintenanceEvent(ctx, notify, "server.maintenance_entered", "maintenance_entered", cfg.Server.Maintenance.Notify.OnEnter, transition.State)
	}
	if transition.Recovered {
		return recordMaintenanceEvent(ctx, notify, "server.maintenance_exited", "maintenance_exited", cfg.Server.Maintenance.Notify.OnExit, transition.State)
	}
	if transition.Exhausted {
		return recordMaintenanceEvent(ctx, notify, "server.self_healing_exhausted", "self_healing_exhausted", true, transition.State)
	}
	return nil
}

func recordMaintenanceEvent(ctx context.Context, notify operatorEventNotifier, auditEvent, emailEvent string, enabled bool, state *server.Maintenance) error {
	reason := "unknown"
	attempts := 0
	duration := "unknown"
	if state != nil {
		reason = state.Reason
		attempts = state.SelfHealing.Attempts
		if since, err := time.Parse(time.RFC3339, state.Since); err == nil {
			duration = time.Since(since).Round(time.Second).String()
		}
	}
	severity := "warning"
	if auditEvent == "server.maintenance_exited" {
		severity = "info"
	} else if auditEvent == "server.self_healing_exhausted" {
		severity = "error"
	}
	details := map[string]any{"reason": reason, "attempts": attempts}
	if auditEvent == "server.maintenance_exited" {
		details["duration"] = duration
	}
	if err := writeAuditEvent(auditEvent, severity, "success", "scheduler", "127.0.0.1", nil, details); err != nil {
		return err
	}
	if enabled && notify != nil {
		return notify(ctx, emailEvent, map[string]string{"reason": reason, "attempts": fmt.Sprint(attempts), "duration": duration})
	}
	return nil
}
