package main

import (
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/database"
	"github.com/apimgr/citylist/src/health"
	"github.com/apimgr/citylist/src/scheduler"
	"github.com/apimgr/citylist/src/securitydata"
	"github.com/apimgr/citylist/src/server"
	sslmanager "github.com/apimgr/citylist/src/ssl"
	"github.com/apimgr/citylist/src/updater"
	"github.com/oschwald/maxminddb-golang"
)

var scheduledUpdateRestart = make(chan struct{}, 1)

type operatorEventNotifier func(context.Context, string, map[string]string) error

type databaseErrorMonitor struct {
	mu           sync.Mutex
	failures     int
	since        time.Time
	lastNotified time.Time
}

func (m *databaseErrorMonitor) observe(ctx context.Context, err error, cfg *config.Config, notify operatorEventNotifier, now time.Time) {
	settings := cfg.Server.Notifications.DatabaseError
	m.mu.Lock()
	if err == nil {
		recovered := m.failures > 0
		m.failures, m.since, m.lastNotified = 0, time.Time{}, time.Time{}
		m.mu.Unlock()
		if recovered {
			slog.Info("database.connection_recovered")
		}
		return
	}
	m.failures++
	if m.since.IsZero() {
		m.since = now
	}
	threshold, repeat := settings.Failures, 24*time.Hour
	if threshold < 1 {
		threshold = 3
	}
	if duration, parseErr := time.ParseDuration(settings.Repeat); parseErr == nil && duration > 0 {
		repeat = duration
	}
	shouldNotify := settings.Enabled && m.failures >= threshold && (m.lastNotified.IsZero() || now.Sub(m.lastNotified) >= repeat)
	since := m.since.UTC().Format(time.RFC3339)
	if shouldNotify {
		m.lastNotified = now
	}
	m.mu.Unlock()
	if !shouldNotify {
		return
	}
	message := sanitizeDatabaseError(err, cfg)
	slog.Warn("database.connection_failed", "since", since, "error", message, "failures", threshold)
	if notify != nil {
		notifyContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if notifyErr := notify(notifyContext, "database_error", map[string]string{"since": since, "error": message}); notifyErr != nil {
			slog.Warn("database.notification_failed", "error", notifyErr)
		}
	}
}

func sanitizeDatabaseError(err error, cfg *config.Config) string {
	if err == nil {
		return "unknown database error"
	}
	message := strings.NewReplacer("\r", " ", "\n", " ").Replace(err.Error())
	if cfg != nil {
		for _, secret := range []string{cfg.Server.Database.URL, cfg.Server.Database.Token} {
			if secret != "" {
				message = strings.ReplaceAll(message, secret, "[REDACTED]")
			}
		}
	}
	message = databaseCredentialPattern.ReplaceAllString(message, "$1$2[REDACTED]")
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}

var databaseCredentialPattern = regexp.MustCompile(`(?i)(password|token|secret|authorization)([=: ]+)[^\s,&;]+`)

func newApplicationScheduler(cfg *config.Config, configDir, dataDir, logsDir, backupDir string, store *database.Store, appContext context.Context, healthTasks map[string]func() error, refreshBrandingImages func(context.Context) error, notifiers ...operatorEventNotifier) (*scheduler.Scheduler, error) {
	return newApplicationSchedulerWithMode(cfg, configDir, dataDir, logsDir, backupDir, store, appContext, healthTasks, refreshBrandingImages, false, notifiers...)
}

func newApplicationSchedulerWithMode(cfg *config.Config, configDir, dataDir, logsDir, backupDir string, store *database.Store, appContext context.Context, healthTasks map[string]func() error, refreshBrandingImages func(context.Context) error, maintenanceOnly bool, notifiers ...operatorEventNotifier) (*scheduler.Scheduler, error) {
	if cfg == nil || store == nil {
		return nil, errors.New("scheduler requires configuration and database")
	}
	var notify operatorEventNotifier
	if len(notifiers) > 0 {
		notify = notifiers[0]
	}
	if maintenanceOnly || store.UsingOfflineCache() {
		slog.Warn("scheduled tasks are disabled while the database is unavailable")
		instance := scheduler.New()
		settings := cfg.Server.Notifications.DatabaseError
		if settings.Enabled {
			location, err := time.LoadLocation(cfg.Server.Scheduler.Timezone)
			if err != nil {
				return nil, fmt.Errorf("load scheduler timezone: %w", err)
			}
			monitor := &databaseErrorMonitor{}
			run := func() error {
				monitor.observe(appContext, probeDatabaseConnections(store), cfg, notify, time.Now())
				return nil
			}
			if err := instance.AddScheduledTask("database_check", "@every "+settings.CheckInterval, true, location, run); err != nil {
				return nil, err
			}
		}
		if run, ok := healthTasks["maintenance_health"]; ok {
			offlineLocation, err := time.LoadLocation(cfg.Server.Scheduler.Timezone)
			if err != nil {
				return nil, fmt.Errorf("load scheduler timezone: %w", err)
			}
			if err := instance.AddScheduledTask("maintenance_health", "@every 10s", true, offlineLocation, run); err != nil {
				return nil, err
			}
		}
		return instance, nil
	}
	location, err := time.LoadLocation(cfg.Server.Scheduler.Timezone)
	if err != nil {
		return nil, fmt.Errorf("load scheduler timezone: %w", err)
	}
	catchUpWindow, err := time.ParseDuration(cfg.Server.Scheduler.CatchUpWindow)
	if err != nil || catchUpWindow < 0 {
		return nil, errors.New("invalid scheduler catch-up window")
	}
	instance, err := scheduler.NewPersistent(store, catchUpWindow)
	if err != nil {
		return nil, err
	}
	instance.SetFailureHook(func(name string, taskErr error) {
		if auditErr := writeAuditEvent("scheduler.task_failed", "warn", "failure", "scheduler", "127.0.0.1", map[string]string{"type": "task", "id": name}, map[string]any{"error": taskErr.Error()}); auditErr != nil {
			slog.Error("scheduler.audit_failed", "task", name, "error", auditErr)
		}
		if notify != nil {
			event := "scheduler_error"
			details := map[string]string{"task_name": name, "error": taskErr.Error()}
			if strings.HasPrefix(name, "backup_") {
				event = "backup_failed"
			} else if name == "ssl_renewal" {
				event = "ssl_renewal_failed"
				details = sslCertificateNotificationDetails(configDir, cfg, time.Now())
				details["task_name"] = name
				details["error"] = taskErr.Error()
				for _, task := range instance.GetTasks() {
					if task.Name == name && !task.NextRun.IsZero() {
						details["next_retry"] = task.NextRun.Format(time.RFC3339)
						break
					}
				}
			}
			if err := notify(appContext, event, details); err != nil {
				slog.Warn("scheduler.notification_failed", "event", event, "error", err)
			}
		}
	})
	securityDir := strings.ReplaceAll(cfg.Data.SecurityDir, "{data_dir}", dataDir)
	if !filepath.IsAbs(securityDir) {
		securityDir = filepath.Join(dataDir, securityDir)
	}
	updateClient := &http.Client{Timeout: 2 * time.Minute}
	databaseMonitor := &databaseErrorMonitor{}
	registered := map[string]func() error{
		"security_cleanup": func() error { return cleanupSecurityData(cfg, configDir, store) },
		"keyserver_retry": func() error {
			ctx, cancel := context.WithTimeout(appContext, 30*time.Second)
			defer cancel()
			return retryScheduledPGPPublishing(ctx, configDir, logsDir, cfg, store)
		},
		"token_cleanup":    func() error { return cleanupTokenData(cfg, store) },
		"healthcheck_self": func() error { return checkDatabaseHealth(store) },
		"database_check": func() error {
			err := probeDatabaseConnections(store)
			databaseMonitor.observe(appContext, err, cfg, notify, time.Now())
			return nil
		},
		"ssl_renewal": func() error {
			return runScheduledSSLRenewal(configDir, cfg, notify)
		},
		"geoip_update": func() error {
			if !cfg.Server.GeoIP.Enabled {
				return nil
			}
			ctx, cancel := context.WithTimeout(appContext, 10*time.Minute)
			defer cancel()
			return securitydata.UpdateGeoIP(ctx, filepath.Join(securityDir, "geoip"), updateClient)
		},
		"blocklist_update": func() error {
			ctx, cancel := context.WithTimeout(appContext, 5*time.Minute)
			defer cancel()
			trusted := trustedBlocklistRanges(ctx, cfg)
			return securitydata.UpdateBlocklists(ctx, filepath.Join(securityDir, "blocklists"), cfg.Server.Security.Blocklists, trusted, updateClient)
		},
		"cve_update": func() error {
			ctx, cancel := context.WithTimeout(appContext, 6*time.Hour)
			defer cancel()
			if err := securitydata.UpdateNVD(ctx, filepath.Join(securityDir, "cve"), cfg.Data.CVE.Source, cfg.Data.CVE.APIKey, updateClient, time.Now()); err != nil {
				return err
			}
			if cfg.Data.Trivy.Enabled {
				return securitydata.UpdateTrivy(ctx, filepath.Join(securityDir, "trivy"), cfg.Data.Trivy.Source, updateClient, time.Now())
			}
			return nil
		},
		"update_check": func() error {
			return runScheduledUpdateCheck(appContext, updateClient, cfg, dataDir, notify)
		},
		"backup_daily": func() error {
			return runScheduledDailyBackup(appContext, filepath.Join(configDir, "server.yml"), configDir, dataDir, backupDir, cfg, store)
		},
		"backup_hourly": func() error {
			return runScheduledHourlyBackup(appContext, filepath.Join(configDir, "server.yml"), configDir, dataDir, backupDir, cfg, store)
		},
		"disk_check":   func() error { return runScheduledDiskCheck(dataDir, cfg, notify) },
		"log_rotation": rotateConfiguredLogs,
	}
	if refreshBrandingImages != nil {
		registered["branding_image_refresh"] = func() error {
			ctx, cancel := context.WithTimeout(appContext, 2*time.Minute)
			defer cancel()
			return refreshBrandingImages(ctx)
		}
	}
	for name, run := range healthTasks {
		if name == "tor_health" || name == "i2p_health" || name == "dataset_health" || name == "maintenance_health" {
			registered[name] = run
		}
	}
	if !cfg.Server.GeoIP.Enabled {
		delete(registered, "geoip_update")
	}
	if !cfg.Server.Security.Blocklists.Enabled {
		delete(registered, "blocklist_update")
	}
	for name, run := range registered {
		taskConfig, ok := cfg.Server.Scheduler.Tasks[name]
		if !ok {
			continue
		}
		if name == "database_check" {
			taskConfig.Schedule = "@every " + cfg.Server.Notifications.DatabaseError.CheckInterval
			taskConfig.Enabled = cfg.Server.Notifications.DatabaseError.Enabled
		}
		if err := instance.AddScheduledTask(name, taskConfig.Schedule, taskConfig.Enabled, location, run); err != nil {
			return nil, err
		}
		if taskConfig.RetryOnFail {
			delay, err := time.ParseDuration(taskConfig.RetryDelay)
			if err != nil {
				return nil, fmt.Errorf("invalid retry delay for scheduler task %q: %w", name, err)
			}
			if err := instance.SetRetryPolicy(name, true, delay); err != nil {
				return nil, err
			}
		}
	}
	return instance, nil
}

func runScheduledSSLRenewal(configDir string, cfg *config.Config, notifiers ...operatorEventNotifier) error {
	if cfg == nil || !cfg.Server.SSL.Enabled || !cfg.Server.SSL.LetsEncrypt.Enabled {
		return nil
	}
	domain := strings.TrimSpace(cfg.Server.FQDN)
	if domain == "" || strings.ContainsAny(domain, `/\\`) {
		return errors.New("SSL renewal requires a valid server.fqdn")
	}
	var notify operatorEventNotifier
	if len(notifiers) > 0 {
		notify = notifiers[0]
	}
	before, _ := managedCertificate(configDir, domain)
	if before != nil {
		days := daysUntilCertificateExpiry(before, time.Now())
		if days == 30 || days == 14 || days == 7 || days == 3 || days == 1 {
			slog.Warn("ssl.certificate_expiring", "fqdn", domain, "expires_in", days, "expiry_date", before.NotAfter.UTC().Format(time.RFC3339))
		}
		if notify != nil && (days == 7 || days == 3 || days == 1) {
			details := sslCertificateNotificationDetails(configDir, cfg, time.Now())
			if err := notify(context.Background(), "ssl_expiring", details); err != nil {
				slog.Warn("ssl.expiring_notification_failed", "error", err)
			}
		}
	}
	sslConfig := cfg.Server.SSL
	manager := sslmanager.NewManager(sslmanager.Config{
		Enabled: true, CertPath: configDir, Cert: sslConfig.Cert, Key: sslConfig.Key,
		MinVersion: sslConfig.MinVersion, Staging: sslConfig.LetsEncrypt.Staging,
		LetsEncrypt: sslmanager.LetsEncryptConfig{
			Enabled: true, Email: strings.ReplaceAll(sslConfig.LetsEncrypt.Email, "{fqdn}", domain),
			Challenge: sslConfig.LetsEncrypt.Challenge, Staging: sslConfig.LetsEncrypt.Staging,
			DNSProviderType: sslConfig.LetsEncrypt.DNSProvider, DNSCredentials: sslConfig.LetsEncrypt.DNSCredentials,
		},
	})
	domains := []string{domain}
	for _, configured := range server.GetAllDomains() {
		domains = appendUniqueSchedulerDomain(domains, configured)
	}
	if sslmanager.ParseChallenge(sslConfig.LetsEncrypt.Challenge) == "dns-01" {
		if wildcard := server.GetWildcardDomain(); wildcard != "" {
			domains = appendUniqueSchedulerDomain(domains, wildcard)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := manager.RenewManagedCertificate(ctx, domains); err != nil {
		return err
	}
	after, _ := managedCertificate(configDir, domain)
	if after != nil && (before == nil || after.NotAfter.After(before.NotAfter)) {
		slog.Info("ssl.certificate_renewed", "fqdn", domain, "valid_until", after.NotAfter.UTC().Format(time.RFC3339))
	}
	return nil
}

func managedCertificate(configDir, domain string) (*x509.Certificate, error) {
	if strings.TrimSpace(domain) == "" || strings.ContainsAny(domain, `/\\`) {
		return nil, errors.New("invalid managed certificate domain")
	}
	path := filepath.Join(configDir, "ssl", "letsencrypt", domain, "fullchain.pem")
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(contents)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("managed certificate PEM is invalid")
	}
	return x509.ParseCertificate(block.Bytes)
}

func daysUntilCertificateExpiry(certificate *x509.Certificate, now time.Time) int {
	if certificate == nil || !certificate.NotAfter.After(now) {
		return 0
	}
	remaining := certificate.NotAfter.Sub(now)
	days := int(remaining / (24 * time.Hour))
	if remaining%(24*time.Hour) != 0 {
		days++
	}
	return days
}

func sslCertificateNotificationDetails(configDir string, cfg *config.Config, now time.Time) map[string]string {
	details := map[string]string{"fqdn": strings.TrimSpace(cfg.Server.FQDN), "expires_in": "", "expiry_date": ""}
	if certificate, err := managedCertificate(configDir, details["fqdn"]); err == nil {
		details["expires_in"] = fmt.Sprintf("%d", daysUntilCertificateExpiry(certificate, now))
		details["expiry_date"] = certificate.NotAfter.UTC().Format(time.RFC3339)
	}
	return details
}

func appendUniqueSchedulerDomain(domains []string, value string) []string {
	for _, domain := range domains {
		if strings.EqualFold(domain, value) {
			return domains
		}
	}
	return append(domains, value)
}

func trustedBlocklistRanges(ctx context.Context, cfg *config.Config) []string {
	trusted := append([]string(nil), cfg.Server.TrustedProxies.Additional...)
	listenAddress := strings.Trim(strings.TrimSpace(cfg.Server.Address), "[]")
	if address, err := netip.ParseAddr(listenAddress); err == nil && !address.IsUnspecified() {
		bits := 24
		if address.Is6() {
			bits = 64
		}
		trusted = append(trusted, netip.PrefixFrom(address, bits).Masked().String())
	}
	for _, configured := range cfg.Server.TrustedProxies.Additional {
		value := strings.Trim(strings.TrimSpace(configured), "[]")
		if _, err := netip.ParseAddr(value); err == nil {
			continue
		}
		if _, err := netip.ParsePrefix(value); err == nil {
			continue
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", value)
		if err != nil {
			slog.Warn("securitydata.trusted_proxy_resolution_failed", "error", err)
			continue
		}
		for _, address := range addresses {
			trusted = append(trusted, address.String())
		}
	}
	return trusted
}

func runScheduledUpdateCheck(ctx context.Context, client *http.Client, cfg *config.Config, dataDir string, notify operatorEventNotifier) error {
	checkContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	release, err := updater.CheckEligible(checkContext, client, cfg.Server.Update.Branch, Version, mustBuildEpoch(), cfg.Server.Update.DeferDays, time.Now())
	if err != nil || release == nil {
		return err
	}
	stateDir := filepath.Join(dataDir, "update-state")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return fmt.Errorf("create update notification state: %w", err)
	}
	statePath := filepath.Join(stateDir, "last-notified")
	previous, _ := os.ReadFile(statePath)
	releaseKey := releaseIdentity(release)
	newNotice := strings.TrimSpace(string(previous)) != releaseKey
	if newNotice {
		slog.Warn("update.available", "current_version", Version, "version", release.TagName, "channel", cfg.Server.Update.Branch, "published_at", release.PublishedAt.UTC().Format(time.RFC3339))
		if notify != nil {
			details := map[string]string{"current_version": Version, "version": release.TagName, "channel": cfg.Server.Update.Branch}
			if cfg.Server.Update.AutoInstall && (runtime.GOOS == "windows" || !startedElevated) {
				details["manual_command"] = "citylist --update yes"
			}
			if err := notify(ctx, "update_available", details); err != nil {
				slog.Warn("update.notification_failed", "event", "update_available", "error", err)
			}
		}
		if auditErr := writeAuditEvent("update.available", "warn", "success", "scheduler", "127.0.0.1", map[string]string{"type": "release", "id": release.TagName}, map[string]any{"version": release.TagName, "channel": cfg.Server.Update.Branch}); auditErr != nil {
			slog.Error("update.audit_failed", "version", release.TagName, "error", auditErr)
		}
		if err := writePrivateState(statePath, []byte(releaseKey+"\n")); err != nil {
			return fmt.Errorf("record update notification: %w", err)
		}
	}
	if !cfg.Server.Update.AutoInstall {
		return nil
	}
	if runtime.GOOS == "windows" || !startedElevated {
		if newNotice {
			if err := writeAuditEvent("update.install_skipped", "info", "success", "scheduler", "127.0.0.1", map[string]string{"type": "release", "id": release.TagName}, map[string]any{"reason": "not_elevated", "version": release.TagName}); err != nil {
				slog.Error("update.audit_failed", "version", release.TagName, "error", err)
			}
			slog.Warn("update.install_skipped", "version", release.TagName, "command", "citylist --update yes")
		}
		return nil
	}
	failedPath := filepath.Join(stateDir, "last-failed")
	failed, _ := os.ReadFile(failedPath)
	if strings.TrimSpace(string(failed)) == releaseKey {
		return nil
	}
	if stagedVersion, stagedPublishedAt, err := updater.StagedReleaseInfo(dataDir); err == nil && stagedVersion == release.TagName && stagedPublishedAt == release.PublishedAt.Unix() {
		return nil
	} else if err != nil {
		slog.Warn("update.staged_metadata_invalid", "error", err)
	}
	stageContext, cancelStage := context.WithTimeout(ctx, 10*time.Minute)
	defer cancelStage()
	if err := updater.Stage(stageContext, client, release, Version, mustBuildEpoch(), dataDir); err != nil {
		if auditErr := writeAuditEvent("update.failed", "error", "failure", "scheduler", "127.0.0.1", map[string]string{"type": "release", "id": release.TagName}, map[string]any{"version": release.TagName}); auditErr != nil {
			slog.Error("update.audit_failed", "version", release.TagName, "error", auditErr)
		}
		if notify != nil {
			if notifyErr := notify(ctx, "update_failed", map[string]string{"version": release.TagName, "reason": "download, checksum verification, or staging failed; see server logs"}); notifyErr != nil {
				slog.Warn("update.notification_failed", "event", "update_failed", "error", notifyErr)
			}
		}
		if stateErr := writePrivateState(failedPath, []byte(releaseKey+"\n")); stateErr != nil {
			slog.Error("update.state_write_failed", "error", stateErr)
		}
		return fmt.Errorf("stage update %s: %w", release.TagName, err)
	}
	_ = os.Remove(failedPath)
	if err := writeAuditEvent("update.staged", "info", "success", "scheduler", "127.0.0.1", map[string]string{"type": "release", "id": release.TagName}, map[string]any{"version": release.TagName}); err != nil {
		slog.Error("update.audit_failed", "version", release.TagName, "error", err)
	}
	if notify != nil {
		if err := notify(ctx, "update_staged", map[string]string{"version": release.TagName}); err != nil {
			slog.Warn("update.notification_failed", "event", "update_staged", "error", err)
		}
	}
	if serviceManagedStart() {
		select {
		case scheduledUpdateRestart <- struct{}{}:
		default:
		}
	} else {
		slog.Info("update.staged", "version", release.TagName, "action", "restart to apply")
	}
	return nil
}

func releaseIdentity(release *updater.Release) string {
	if release != nil && release.TagName == "devel" {
		return release.TagName + ":" + release.PublishedAt.UTC().Format(time.RFC3339)
	}
	if release == nil {
		return ""
	}
	return release.TagName
}

func writePrivateState(path string, content []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func initializeSecurityData(sched *scheduler.Scheduler, cfg *config.Config, dataDir string, scheduleMissing bool) {
	securityDir := strings.ReplaceAll(cfg.Data.SecurityDir, "{data_dir}", dataDir)
	if !filepath.IsAbs(securityDir) {
		securityDir = filepath.Join(dataDir, securityDir)
	}
	registerSecurityDataChecks(cfg, securityDir)
	blocklistPaths := []string{filepath.Join(securityDir, "blocklists", "firehol_level1.txt")}
	for _, source := range cfg.Server.Security.Blocklists.ExtraSources {
		if source.Enabled != nil && !*source.Enabled || !validBlocklistFileID(source.ID) {
			continue
		}
		blocklistPaths = append(blocklistPaths, filepath.Join(securityDir, "blocklists", source.ID+".txt"))
	}
	checks := []struct {
		enabled bool
		task    string
		paths   []string
	}{
		{cfg.Server.GeoIP.Enabled, "geoip_update", []string{
			filepath.Join(securityDir, "geoip", "iptoasn-asn.mmdb"),
			filepath.Join(securityDir, "geoip", "iptoasn-country.mmdb"),
			filepath.Join(securityDir, "geoip", "dbip-city-ipv4.mmdb"),
			filepath.Join(securityDir, "geoip", "dbip-city-ipv6.mmdb"),
		}},
		{cfg.Server.Security.Blocklists.Enabled, "blocklist_update", blocklistPaths},
		{true, "cve_update", []string{filepath.Join(securityDir, "cve", "nvd.json")}},
	}
	for _, check := range checks {
		if !check.enabled {
			continue
		}
		missing := false
		for _, path := range check.paths {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				missing = true
			} else if err != nil {
				slog.Warn("securitydata.initial_check_failed", "dataset", check.task, "error", err)
				missing = false
				break
			}
		}
		if missing && check.task != "cve_update" && scheduleMissing {
			if err := sched.RunNowAsync(check.task); err != nil {
				slog.Warn("securitydata.initial_update_failed", "dataset", check.task, "error", err)
			}
		}
	}
	needsCVE := false
	if _, err := os.Stat(filepath.Join(securityDir, "cve", "nvd.json")); os.IsNotExist(err) {
		needsCVE = true
	}
	if cfg.Data.Trivy.Enabled {
		if _, err := os.Stat(filepath.Join(securityDir, "trivy", "trivy.db")); os.IsNotExist(err) {
			needsCVE = true
		}
	}
	if needsCVE && scheduleMissing {
		if err := sched.RunNowAsync("cve_update"); err != nil {
			slog.Warn("securitydata.initial_update_failed", "dataset", "cve", "error", err)
		}
	}
}

func registerSecurityDataChecks(cfg *config.Config, securityDir string) {
	if cfg.Server.GeoIP.Enabled {
		geoDir := filepath.Join(securityDir, "geoip")
		for _, database := range []struct{ id, file string }{
			{"asn", "iptoasn-asn.mmdb"},
			{"country", "iptoasn-country.mmdb"},
			{"city_ipv4", "dbip-city-ipv4.mmdb"},
			{"city_ipv6", "dbip-city-ipv6.mmdb"},
		} {
			file := filepath.Join(geoDir, database.file)
			health.Register("geoip_"+database.id, false, func() (string, error) {
				return datasetFileStatus(file, filepath.Join(geoDir, ".last_updated"), 21*24*time.Hour, func(path string) error {
					reader, err := maxminddb.Open(path)
					if err != nil {
						return errors.New("dataset could not be parsed")
					}
					return reader.Close()
				})
			})
		}
	}
	if cfg.Server.Security.Blocklists.Enabled {
		blocklistDir := filepath.Join(securityDir, "blocklists")
		ids := []string{"firehol_level1"}
		for _, source := range cfg.Server.Security.Blocklists.ExtraSources {
			if source.Enabled == nil || *source.Enabled {
				if validBlocklistFileID(source.ID) {
					ids = append(ids, source.ID)
				}
			}
		}
		for _, id := range ids {
			file := filepath.Join(blocklistDir, id+".txt")
			marker := filepath.Join(blocklistDir, id+".last_updated")
			health.Register("blocklist_"+id, false, func() (string, error) {
				return datasetFileStatus(file, marker, 72*time.Hour, nil)
			})
		}
	}
	cveDir := filepath.Join(securityDir, "cve")
	health.Register("data_cve", false, func() (string, error) {
		return datasetFileStatus(filepath.Join(cveDir, "nvd.json"), filepath.Join(cveDir, ".last_updated"), 48*time.Hour, func(path string) error {
			content, err := os.ReadFile(path)
			if err != nil {
				return errors.New("dataset could not be read")
			}
			var records []json.RawMessage
			if err := json.Unmarshal(content, &records); err != nil || len(records) == 0 {
				return errors.New("dataset could not be parsed")
			}
			return nil
		})
	})
	if cfg.Data.Trivy.Enabled {
		trivyDir := filepath.Join(securityDir, "trivy")
		health.Register("data_trivy", false, func() (string, error) {
			return datasetFileStatus(filepath.Join(trivyDir, "trivy.db"), filepath.Join(trivyDir, ".last_updated"), 48*time.Hour, func(path string) error {
				info, err := os.Stat(path)
				if err != nil || info.Size() == 0 {
					return errors.New("dataset could not be read")
				}
				metadata, err := os.ReadFile(filepath.Join(trivyDir, "metadata.json"))
				if err != nil || !json.Valid(metadata) {
					return errors.New("dataset metadata could not be parsed")
				}
				return nil
			})
		})
	}
}

func datasetFileStatus(file, marker string, maxAge time.Duration, validate func(string) error) (string, error) {
	info, err := os.Stat(file)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return "error", errors.New("dataset is missing or empty")
	}
	if validate != nil {
		if err := validate(file); err != nil {
			return "error", errors.New("dataset is invalid")
		}
	}
	updatedAt, err := os.ReadFile(marker)
	if err != nil {
		return "degraded", nil
	}
	updated, err := time.Parse(time.RFC3339, strings.TrimSpace(string(updatedAt)))
	if err != nil || time.Since(updated) > maxAge || updated.After(time.Now().Add(time.Minute)) {
		return "degraded", nil
	}
	return "ok", nil
}

func validBlocklistFileID(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func checkDatabaseHealth(store *database.Store) error {
	return probeDatabaseConnections(store)
}

func probeDatabaseConnections(store *database.Store) error {
	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelWrite()
	if err := store.ProbeWrite(writeCtx); err != nil {
		return fmt.Errorf("write handle: %w", err)
	}
	readCtx, cancelRead := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRead()
	return store.Read(readCtx, func(ctx context.Context, reader database.Reader) error {
		var result int
		return reader.QueryRowContext(ctx, `SELECT 1`).Scan(&result)
	})
}

func cleanupTokenData(cfg *config.Config, store *database.Store) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	now := time.Now().UTC()
	return store.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM captcha_challenges WHERE expires_at <= ?`, now.Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("delete expired CAPTCHA challenges: %w", err)
		}
		if cfg.Server.Tokens.IdlePurgeDays <= 0 {
			return nil
		}
		cutoff := now.AddDate(0, 0, -cfg.Server.Tokens.IdlePurgeDays).Unix()
		if _, err := tx.ExecContext(ctx, `DELETE FROM owners WHERE last_seen_at <= ?`, cutoff); err != nil {
			return fmt.Errorf("purge idle owners: %w", err)
		}
		return nil
	})
}

func cleanupSecurityData(cfg *config.Config, configDir string, store *database.Store) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if cfg.Server.Security.Reports.RetentionDays > 0 {
		cutoff := time.Now().UTC().AddDate(0, 0, -cfg.Server.Security.Reports.RetentionDays).Format(time.RFC3339Nano)
		if err := store.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM security_reports WHERE status = 'closed' AND closed_at IS NOT NULL AND closed_at <= ?`, cutoff)
			return err
		}); err != nil {
			return fmt.Errorf("purge retained security reports: %w", err)
		}
	}
	manager, err := server.NewPGPMaintenance(configDir, cfg, store)
	if err != nil {
		return err
	}
	if err := manager.CleanupExpiredOldProjectPGPKey(ctx); err != nil {
		return fmt.Errorf("purge retired PGP key: %w", err)
	}
	return nil
}
