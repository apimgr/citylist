package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Server.Port != "" {
		t.Errorf("Server.Port = %q, want empty for first-run selection", cfg.Server.Port)
	}
	if cfg.Server.Address != "[::]" {
		t.Errorf("Server.Address = %q, want [::]", cfg.Server.Address)
	}
	if cfg.Server.Mode != "production" {
		t.Errorf("Server.Mode = %q, want production", cfg.Server.Mode)
	}
	if cfg.Server.APIVersion != "v1" {
		t.Errorf("Server.APIVersion = %q, want v1", cfg.Server.APIVersion)
	}
	if cfg.Server.Database.Pool.MaxOpen != 25 || cfg.Server.Database.Pool.MaxIdle != 5 {
		t.Errorf("database pool defaults = %+v, want 25 open and 5 idle readers", cfg.Server.Database.Pool)
	}
	if cfg.Server.SSL.Enabled || cfg.Server.SSL.MinVersion != "TLS1.2" || cfg.Server.SSL.LetsEncrypt.Enabled ||
		cfg.Server.SSL.LetsEncrypt.Email != "" || cfg.Server.SSL.LetsEncrypt.Challenge != "http-01" || cfg.Server.SSL.LetsEncrypt.Staging {
		t.Errorf("SSL defaults do not match the server template: %+v", cfg.Server.SSL)
	}
	if !cfg.Web.Headers.HonorSecGPC {
		t.Error("web.headers.honor_sec_gpc must default to true")
	}
	if cfg.Web.Headers.HonorDNT {
		t.Error("web.headers.honor_dnt must default to false")
	}
	if cfg.Server.BaseURL != "/" || cfg.Server.Cache.Type != "memory" || cfg.Data.CVE.Source == "" {
		t.Errorf("node and external-data defaults are incomplete: baseurl=%q cache=%+v data=%+v", cfg.Server.BaseURL, cfg.Server.Cache, cfg.Data)
	}
	if cfg.Server.Tor.MaxCircuits != 32 || cfg.Server.Tor.VirtualPort != 80 || !cfg.Server.Tor.SafeLogging {
		t.Errorf("Tor defaults are incomplete: %+v", cfg.Server.Tor)
	}
	if cfg.Server.I2P.Enabled != nil || cfg.Server.I2P.SAMAddress != "127.0.0.1:7656" || cfg.Server.I2P.BootstrapTimeout != 300 {
		t.Errorf("I2P defaults must preserve an omitted enabled value: %+v", cfg.Server.I2P)
	}
	if !cfg.Server.Metrics.Enabled || !cfg.Server.Metrics.Root.Enabled || !cfg.Server.Metrics.IncludeSystem || !cfg.Server.Metrics.IncludeRuntime {
		t.Errorf("metrics defaults are incomplete: %+v", cfg.Server.Metrics)
	}
	if cfg.Server.Metrics.Loki.MaxEntries != 1000 || cfg.Server.Metrics.Loki.MaxAge != "1h" {
		t.Errorf("Loki defaults = %+v, want 1000 entries and 1h", cfg.Server.Metrics.Loki)
	}
	if cfg.Server.I18n.DefaultLanguage != "en" || len(cfg.Server.I18n.Available) != 6 {
		t.Errorf("Server.I18n defaults are incomplete: %+v", cfg.Server.I18n)
	}
	if cfg.Web.UI.Theme != "dark" {
		t.Errorf("Web.UI.Theme = %q, want dark", cfg.Web.UI.Theme)
	}
	if !cfg.Web.Announcements.Enabled || cfg.Web.Announcements.Messages == nil {
		t.Error("expected enabled announcements with an empty message list")
	}
	if len(cfg.Web.Robots.Allow) == 0 {
		t.Error("expected Web.Robots.Allow to be non-empty")
	}
	if len(cfg.Server.CORS.AllowedOrigins) != 1 || cfg.Server.CORS.AllowedOrigins[0] != "*" {
		t.Errorf("Server.CORS.AllowedOrigins = %v, want [*]", cfg.Server.CORS.AllowedOrigins)
	}
	if cfg.Server.Scheduler.Timezone != "America/New_York" || cfg.Server.Scheduler.CatchUpWindow != "1h" || len(cfg.Server.Scheduler.Tasks) < 14 {
		t.Errorf("scheduler defaults are incomplete: %+v", cfg.Server.Scheduler)
	}
	if cfg.Server.Pages.Help.Content != "" || !strings.Contains(cfg.Server.Pages.Help.MCP, "get_city") || !strings.Contains(cfg.Server.Pages.Help.MCP, "nearby_zipcodes") {
		t.Errorf("help content defaults are incomplete: %+v", cfg.Server.Pages.Help)
	}
}

func TestUnmarshalConfigDefaultsInvalidBooleanSettings(t *testing.T) {
	cfg := DefaultConfig()
	data := []byte("server:\n  ssl:\n    enabled: perhaps\n    letsencrypt:\n      staging: maybe\n  i2p:\n    enabled: maybe\n")
	if err := unmarshalConfig(data, cfg); err != nil {
		t.Fatalf("invalid boolean configuration should use defaults: %v", err)
	}
	if cfg.Server.SSL.Enabled || cfg.Server.SSL.LetsEncrypt.Staging {
		t.Fatalf("invalid booleans did not fall back to defaults: SSL=%t staging=%t", cfg.Server.SSL.Enabled, cfg.Server.SSL.LetsEncrypt.Staging)
	}
	if cfg.Server.I2P.Enabled != nil {
		t.Fatalf("invalid optional I2P setting should remain unset, got %t", *cfg.Server.I2P.Enabled)
	}
	valid := DefaultConfig()
	if err := unmarshalConfig([]byte("server:\n  ssl:\n    enabled: oui\n"), valid); err != nil {
		t.Fatalf("valid flexible boolean should parse: %v", err)
	}
	if !valid.Server.SSL.Enabled {
		t.Fatal("oui should parse as true")
	}
}

func TestLoadHelpPageContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yml")
	content := "server:\n  pages:\n    help:\n      content: '# Citylist help'\n      mcp: '# Citylist MCP help'\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Server.Pages.Help.Content != "# Citylist help" || cfg.Server.Pages.Help.MCP != "# Citylist MCP help" {
		t.Fatalf("help page content was not loaded: %+v", cfg.Server.Pages.Help)
	}
}

func TestValidateSchedulerRestoresInvalidValuesAndPreservesOverrides(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.Scheduler.Timezone = "not/a/timezone"
	cfg.Server.Scheduler.CatchUpWindow = "bad"
	cfg.Server.Scheduler.Tasks["backup_hourly"] = SchedulerTask{Schedule: "@hourly", Enabled: false}
	cfg.Server.Scheduler.Tasks["ssl_renewal"] = SchedulerTask{Schedule: "0 3 * * *", Enabled: false}
	cfg.Server.Scheduler.Tasks["pgp_publish_retry"] = SchedulerTask{Schedule: "@every 1m", Enabled: false}
	cfg.Server.Scheduler.Tasks["backup_daily"] = SchedulerTask{Schedule: "70 * * * *", Enabled: true}
	cfg.Server.Scheduler.Tasks["blocklist_update"] = SchedulerTask{Schedule: "@daily", Enabled: true, RetryOnFail: true, RetryDelay: "bad"}

	validateScheduler(cfg, "scheduler-test.yml")
	defaults := DefaultConfig().Server.Scheduler
	if cfg.Server.Scheduler.Timezone != defaults.Timezone || cfg.Server.Scheduler.CatchUpWindow != defaults.CatchUpWindow {
		t.Fatalf("invalid scheduler settings were not reset: %+v", cfg.Server.Scheduler)
	}
	if cfg.Server.Scheduler.Tasks["backup_daily"].Schedule != defaults.Tasks["backup_daily"].Schedule {
		t.Fatalf("invalid schedule did not use default: %+v", cfg.Server.Scheduler.Tasks["backup_daily"])
	}
	if task := cfg.Server.Scheduler.Tasks["backup_hourly"]; task.Enabled || task.Schedule != "@hourly" {
		t.Fatalf("valid disabled task override was not preserved: %+v", task)
	}
	if task := cfg.Server.Scheduler.Tasks["ssl_renewal"]; !task.Enabled {
		t.Fatalf("non-skippable task was disabled: %+v", task)
	}
	if task := cfg.Server.Scheduler.Tasks["keyserver_retry"]; !task.Enabled {
		t.Fatalf("PGP retry task was disabled: %+v", task)
	}
	if _, legacy := cfg.Server.Scheduler.Tasks["pgp_publish_retry"]; legacy {
		t.Fatal("legacy PGP retry task was not renamed")
	}
	if task := cfg.Server.Scheduler.Tasks["blocklist_update"]; task.Schedule != "@daily" || task.RetryDelay != defaults.Tasks["blocklist_update"].RetryDelay {
		t.Fatalf("valid schedule or invalid retry delay handling failed: %+v", task)
	}
	if _, ok := cfg.Server.Scheduler.Tasks["healthcheck_self"]; !ok {
		t.Fatal("missing built-in task was not restored")
	}
}

func TestLoadNodeCacheTLSAndSecurityDataSettings(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "server.yml")
	serverYAML := "server:\n  baseurl: /citylist\n  cache:\n    type: valkey\n    host: cache.internal\n    port: 6380\n  ssl:\n    enabled: true\n    cert: /etc/citylist/cert.pem\n    key: /etc/citylist/key.pem\n    min_version: TLS1.3\n    letsencrypt:\n      enabled: true\n      email: admin@example.test\n      challenge: dns-01\n      staging: true\n      dns_provider: cloudflare\n      dns_credentials:\n        CF_DNS_API_TOKEN: provider-secret\n"
	if err := os.WriteFile(path, []byte(serverYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	securityDir := filepath.Join(directory, "yaml.d")
	if err := os.Mkdir(securityDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dataYAML := "data:\n  security_dir: /data/security\n  cve:\n    source: https://nvd.example.test/api\n    filter_by_cpe: true\n    api_key: cve-secret\n  trivy:\n    enabled: true\n    source: ghcr.io/example/trivy-db:2\n"
	if err := os.WriteFile(filepath.Join(securityDir, "security.yml"), []byte(dataYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Server.BaseURL != "/citylist" || cfg.Server.Cache.Type != "valkey" || cfg.Server.Cache.Host != "cache.internal" {
		t.Fatalf("node settings were not loaded: baseurl=%q cache=%+v", cfg.Server.BaseURL, cfg.Server.Cache)
	}
	if !cfg.Server.SSL.Enabled || !cfg.Server.SSL.LetsEncrypt.Enabled || cfg.Server.SSL.LetsEncrypt.DNSProvider != "cloudflare" {
		t.Fatalf("TLS settings were not loaded: %+v", cfg.Server.SSL)
	}
	if cfg.Server.SSL.Cert != "/etc/citylist/cert.pem" || cfg.Server.SSL.Key != "/etc/citylist/key.pem" || cfg.Server.SSL.MinVersion != "TLS1.3" ||
		cfg.Server.SSL.LetsEncrypt.Email != "admin@example.test" || cfg.Server.SSL.LetsEncrypt.Challenge != "dns-01" || !cfg.Server.SSL.LetsEncrypt.Staging {
		t.Fatalf("extended TLS settings were not loaded: %+v", cfg.Server.SSL)
	}
	if cfg.Server.SSL.LetsEncrypt.DNSCredentials["CF_DNS_API_TOKEN"] != "provider-secret" {
		t.Fatal("DNS credentials were not merged from secrets.yml")
	}
	if cfg.Data.SecurityDir != "/data/security" || !cfg.Data.CVE.FilterByCPE || cfg.Data.CVE.APIKey != "cve-secret" || !cfg.Data.Trivy.Enabled {
		t.Fatalf("security data settings were not loaded: %+v", cfg.Data)
	}
	if cfg.Data.Trivy.Source != "ghcr.io/example/trivy-db:2" {
		t.Fatalf("Trivy source = %q, want configured source", cfg.Data.Trivy.Source)
	}
}

func TestLoadValidatesSEOValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yml")
	content := "server:\n  branding:\n    title: ''\n  seo:\n    title_separator: 'this is too long'\n    meta_tags:\n      Bad Name: value\n    twitter:\n      card: summary_invalid\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	defaults := DefaultConfig()
	if cfg.Server.Branding.Title != defaults.Server.Branding.Title || cfg.Server.SEO.TitleSeparator != defaults.Server.SEO.TitleSeparator ||
		len(cfg.Server.SEO.MetaTags) != 0 || cfg.Server.SEO.Twitter.Card != defaults.Server.SEO.Twitter.Card {
		t.Fatalf("invalid SEO settings were not reset to defaults: branding=%+v seo=%+v", cfg.Server.Branding, cfg.Server.SEO)
	}
}

func TestLoadI2PExplicitDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  i2p:\n    enabled: false\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Server.I2P.Enabled == nil || *cfg.Server.I2P.Enabled {
		t.Fatalf("explicit I2P false was not preserved: %+v", cfg.Server.I2P.Enabled)
	}
}

func TestLoadMergesOrderedDropinsAndResetsNullToDefaults(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  address: 127.0.0.1\nweb:\n  ui:\n    theme: light\n  robots:\n    allow: [/one, /two]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dropinDir := filepath.Join(directory, "yaml.d")
	if err := os.Mkdir(dropinDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"20-theme.yml":   "web:\n  ui:\n    theme: auto\n  robots:\n    allow: [/later]\n",
		"10-theme.yml":   "web:\n  ui:\n    accent: blue\n",
		"30-reset.yaml":  "server:\n  address: null\n",
		".hidden.yml":    "web:\n  ui:\n    theme: bad\n",
		"backup.yml.bak": "web:\n  ui:\n    theme: bad\n",
	} {
		if err := os.WriteFile(filepath.Join(dropinDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := LoadBasic(path)
	if err != nil {
		t.Fatalf("load merged config: %v", err)
	}
	if cfg.Web.UI.Theme != "auto" || cfg.Web.UI.Accent != "blue" {
		t.Fatalf("recursive ordered merge failed: %+v", cfg.Web.UI)
	}
	if len(cfg.Web.Robots.Allow) != 1 || cfg.Web.Robots.Allow[0] != "/later" {
		t.Fatalf("drop-in list did not replace prior list: %v", cfg.Web.Robots.Allow)
	}
	if cfg.Server.Address != DefaultConfig().Server.Address {
		t.Fatalf("null did not reset address to default: %q", cfg.Server.Address)
	}
}

func TestLoadMigratesAreaSectionsIntoAssignedFiles(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "server.yml")
	legacy := "server:\n  address: 127.0.0.1\n  branding:\n    title: Migrated title\n  logging:\n    level: debug\n    access_format: custom\nweb:\n  ui:\n    theme: light\n  robots:\n    allow: [/custom]\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	dropinDirectory := filepath.Join(directory, "yaml.d")
	if err := os.Mkdir(dropinDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dropinDirectory, "branding.yml"), []byte("server:\n  branding:\n    title: Existing title\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Server.Address != "127.0.0.1" || cfg.Server.Branding.Title != "Existing title" || cfg.Web.UI.Theme != "light" ||
		cfg.Server.Logs.Level != "debug" || cfg.Server.Logs.Access.Format != "custom" {
		t.Fatalf("migrated configuration = address %q, title %q, theme %q, logs %+v", cfg.Server.Address, cfg.Server.Branding.Title, cfg.Web.UI.Theme, cfg.Server.Logs)
	}
	for _, filename := range []string{"branding.yml", "web.yml", "logs.yml"} {
		if _, err := os.Stat(filepath.Join(dropinDirectory, filename)); err != nil {
			t.Errorf("expected migrated area file %s: %v", filename, err)
		}
	}
	backup, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("read server.yml backup: %v", err)
	}
	if string(backup) != legacy {
		t.Fatalf("server.yml backup = %q, want original %q", backup, legacy)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migrated server.yml: %v", err)
	}
	if !strings.HasPrefix(string(current), nodeConfigHeader) || strings.Contains(string(current), "branding:") || strings.Contains(string(current), "ui:") {
		t.Fatalf("server.yml contains moved sections or misses its header: %s", current)
	}
	for _, filename := range []string{path, path + ".bak", filepath.Join(dropinDirectory, "branding.yml"), filepath.Join(dropinDirectory, "web.yml")} {
		info, err := os.Stat(filename)
		if err != nil {
			t.Fatalf("stat %s: %v", filename, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %04o, want 0600", filename, info.Mode().Perm())
		}
	}
}

func TestLoadDropinsRejectsOutsideSymlink(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "server.yml")
	if err := os.WriteFile(path, []byte("web:\n  ui:\n    theme: light\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dropinDir := filepath.Join(directory, "yaml.d")
	if err := os.Mkdir(dropinDir, 0o700); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "outside.yml")
	if err := os.WriteFile(external, []byte("web:\n  ui:\n    theme: auto\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(dropinDir, "10-external.yml")); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadBasic(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Web.UI.Theme != "light" {
		t.Fatalf("outside symlink was loaded: theme=%q", cfg.Web.UI.Theme)
	}
}

func TestLoadIgnoresAndPreservesUnknownConfigurationKeys(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  mystery: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() rejected an unknown configuration key: %v", err)
	}
	if cfg.Server.Address != DefaultConfig().Server.Address {
		t.Fatalf("unknown key changed the server address: %q", cfg.Server.Address)
	}
	appData, err := os.ReadFile(filepath.Join(directory, "yaml.d", "app.yml"))
	if err != nil {
		t.Fatalf("read migrated app.yml: %v", err)
	}
	if !strings.Contains(string(appData), "mystery: true") {
		t.Fatalf("unknown key was not preserved in app.yml: %s", appData)
	}
}

func TestLoadI18nSettings(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "server.yml")
	data := []byte("server:\n  i18n:\n    default_language: fr\n    available_languages: [en, es, fr, de, it, ar]\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing server config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Server.I18n.DefaultLanguage != "fr" || len(cfg.Server.I18n.Available) != 6 {
		t.Errorf("loaded i18n configuration = %+v", cfg.Server.I18n)
	}
}

func TestInvalidI18nSettingsFallBackToSafeDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "server.yml")
	data := []byte("server:\n  i18n:\n    default_language: xx\n    available_languages: [en, es]\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing server config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Server.I18n.DefaultLanguage != "en" || len(cfg.Server.I18n.Available) != 6 {
		t.Errorf("invalid i18n settings were not reset: %+v", cfg.Server.I18n)
	}
}

func TestUpdateBranchAliasesNormalizeAndPersist(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  update:\n    branch: daily\n    defer_days: 400\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Server.Update.Branch != "devel" || cfg.Server.Update.DeferDays != 0 {
		t.Fatalf("update config = %+v, want normalized devel and defer 0", cfg.Server.Update)
	}
}

func TestSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.yml")

	cfg := DefaultConfig()
	cfg.Server.Port = "9090"
	cfg.Web.UI.Theme = "light"

	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("config file mode = %04o, want 0600", got)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded.Server.Port != "9090" {
		t.Errorf("loaded Server.Port = %q, want 9090", loaded.Server.Port)
	}
	if loaded.Web.UI.Theme != "light" {
		t.Errorf("loaded Web.UI.Theme = %q, want light", loaded.Web.UI.Theme)
	}
}

func TestLoadCreatesDefaultWhenMissing(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "missing.yml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Server.Port != "" {
		t.Errorf("expected empty port before first-run selection, got %q", cfg.Server.Port)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected Load() to create the config file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "yaml.d")); !os.IsNotExist(err) {
		t.Errorf("first run created an area directory: stat error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read server.yml: %v", err)
	}
	if !strings.HasPrefix(string(data), nodeConfigHeader) {
		t.Errorf("server.yml is missing the required node-only header: %s", data)
	}
}

func TestFirstRunSecretsAreGeneratedOutsideServerYAML(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "server.yml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !validToken(cfg.Server.Token, "adm_") || cfg.InitialToken != cfg.Server.Token {
		t.Fatalf("generated operator token has invalid first-run state")
	}
	key, err := base64.StdEncoding.DecodeString(cfg.Server.Security.EncryptionKey)
	if err != nil || len(key) != 32 || cfg.Server.Security.EncryptionKeyVersion != 1 {
		t.Fatalf("generated encryption key is invalid: length=%d version=%d err=%v", len(key), cfg.Server.Security.EncryptionKeyVersion, err)
	}
	secretPath := filepath.Join(tmpDir, "secrets.yml")
	info, err := os.Stat(secretPath)
	if err != nil {
		t.Fatalf("secrets.yml was not created: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("secrets.yml mode = %04o, want 0600", info.Mode().Perm())
	}
	metricsTokens := []string{
		cfg.Server.Metrics.Auth.Tokens.Prometheus,
		cfg.Server.Metrics.Auth.Tokens.Grafana,
		cfg.Server.Metrics.Auth.Tokens.Loki,
	}
	for i, token := range metricsTokens {
		if !validToken(token, "mtr_") {
			t.Fatalf("metrics token %d has invalid format", i)
		}
		for j := 0; j < i; j++ {
			if token == metricsTokens[j] {
				t.Fatal("metrics services share a bearer token")
			}
		}
	}
	secretData, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("reading secrets.yml: %v", err)
	}
	for _, token := range metricsTokens {
		if !strings.Contains(string(secretData), token) {
			t.Fatal("generated metrics token was not written to secrets.yml")
		}
	}
	serverData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading server.yml: %v", err)
	}
	if strings.Contains(string(serverData), cfg.Server.Token) || strings.Contains(string(serverData), cfg.Server.Security.EncryptionKey) {
		t.Fatal("generated secret was written into server.yml")
	}
	for _, token := range metricsTokens {
		if strings.Contains(string(serverData), token) {
			t.Fatal("metrics bearer token was written into server.yml")
		}
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("second Load() error: %v", err)
	}
	if loaded.Server.Token != cfg.Server.Token || loaded.InitialToken != "" {
		t.Fatal("existing operator token was not reused without being reissued")
	}
	if loaded.Server.Metrics.Auth.Tokens != cfg.Server.Metrics.Auth.Tokens {
		t.Fatal("existing metrics bearer tokens were not reused")
	}
}

func TestLoadBasicDoesNotCreateSecrets(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "server.yml")
	cfg, err := LoadBasic(path)
	if err != nil {
		t.Fatalf("LoadBasic() error: %v", err)
	}
	if cfg.Server.Port != "" {
		t.Fatalf("basic config port = %q, want empty before first-run selection", cfg.Server.Port)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "secrets.yml")); !os.IsNotExist(err) {
		t.Fatalf("LoadBasic() should not create secrets.yml, stat error = %v", err)
	}
}

func TestMetricsTokensMoveFromServerConfigToSecrets(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "server.yml")
	metricsToken := "mtr_0123456789ABCDEFGHIJKLMNOPQRSTUV"
	data := []byte("server:\n  metrics:\n    auth:\n      tokens:\n        prometheus: " + metricsToken + "\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing server config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Server.Metrics.Auth.Tokens.Prometheus != metricsToken {
		t.Fatal("server.yml metrics token was not loaded")
	}
	serverData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading server config: %v", err)
	}
	if strings.Contains(string(serverData), metricsToken) {
		t.Fatal("metrics token remained in server.yml after migration")
	}
	secretData, err := os.ReadFile(filepath.Join(tmpDir, "secrets.yml"))
	if err != nil {
		t.Fatalf("reading secrets config: %v", err)
	}
	if !strings.Contains(string(secretData), metricsToken) {
		t.Fatal("metrics token was not moved to secrets.yml")
	}
}

func TestInvalidMetricsBucketsAndLokiLimitsFallBackToSafeValues(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "server.yml")
	data := []byte("server:\n  metrics:\n    duration_buckets: [0.1, 0.01]\n    size_buckets: [100, 0]\n    loki:\n      max_entries: 20000\n      max_age: -1s\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing server config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	defaults := DefaultConfig().Server.Metrics
	if cfg.Server.Metrics.Loki.MaxEntries != 10000 || cfg.Server.Metrics.Loki.MaxAge != defaults.Loki.MaxAge {
		t.Fatalf("Loki limits = %+v, want max 10,000 entries and max age %q", cfg.Server.Metrics.Loki, defaults.Loki.MaxAge)
	}
	if len(cfg.Server.Metrics.DurationBuckets) != len(defaults.DurationBuckets) || len(cfg.Server.Metrics.SizeBuckets) != len(defaults.SizeBuckets) {
		t.Fatal("invalid histogram buckets were not replaced with defaults")
	}
}

func TestLoadSecuresExistingServerConfigBeforeReading(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  port: '8080'\n"), 0o644); err != nil {
		t.Fatalf("writing config fixture: %v", err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat server.yml: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("server.yml mode = %04o, want 0600", info.Mode().Perm())
	}
}

func TestLegacySecretsMoveToSecretsFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "server.yml")
	token := "adm_0123456789ABCDEFGHIJKLMNOPQRSTUV"
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	legacy := "server:\n  token: " + token + "\n  security:\n    encryption_key: " + key + "\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("writing legacy config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.Server.Token != token || cfg.Server.Security.EncryptionKey != key {
		t.Fatal("legacy secrets were not loaded into runtime configuration")
	}
	secrets, err := os.ReadFile(filepath.Join(tmpDir, "secrets.yml"))
	if err != nil {
		t.Fatalf("reading secrets.yml: %v", err)
	}
	if !strings.Contains(string(secrets), token) || !strings.Contains(string(secrets), key) {
		t.Fatal("legacy secrets were not migrated into secrets.yml")
	}
	server, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading server.yml after migration: %v", err)
	}
	if strings.Contains(string(server), token) || strings.Contains(string(server), key) {
		t.Fatal("migrated secret remains in server.yml")
	}
}

func TestLegacySecretsInAreaFilesMoveAndAreRemoved(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "server.yml")
	if err := os.WriteFile(path, []byte("server:\n  address: 127.0.0.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dropinDir := filepath.Join(tmpDir, "yaml.d")
	if err := os.Mkdir(dropinDir, 0o700); err != nil {
		t.Fatal(err)
	}
	secretsPath := filepath.Join(dropinDir, "50-legacy.yml")
	legacy := "server:\n  cache:\n    password: cache-secret\n  ssl:\n    letsencrypt:\n      dns_credentials:\n        CF_DNS_API_TOKEN: dns-secret\n  contact:\n    admin:\n      webhooks:\n        alert: https://example.test/hook?token=webhook-secret\ndata:\n  cve:\n    api_key: cve-secret\n"
	if err := os.WriteFile(secretsPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	stored, err := os.ReadFile(filepath.Join(tmpDir, "secrets.yml"))
	if err != nil {
		t.Fatalf("read secrets.yml: %v", err)
	}
	for _, value := range []string{"cache-secret", "dns-secret", "webhook-secret", "cve-secret"} {
		if !strings.Contains(string(stored), value) {
			t.Errorf("secrets.yml is missing migrated value %q", value)
		}
	}
	remaining, err := os.ReadFile(secretsPath)
	if err != nil {
		t.Fatalf("read legacy drop-in: %v", err)
	}
	for _, value := range []string{"cache-secret", "dns-secret", "webhook-secret", "cve-secret"} {
		if strings.Contains(string(remaining), value) {
			t.Errorf("legacy drop-in still contains credential %q: %s", value, remaining)
		}
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "bad.yml")

	if err := os.WriteFile(path, []byte("not: valid: yaml: [broken"), 0o644); err != nil {
		t.Fatalf("failed to write test fixture: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Error("expected error loading invalid YAML, got nil")
	}
}

func TestMigrateYamlToYml(t *testing.T) {
	tmpDir := t.TempDir()
	ymlPath := filepath.Join(tmpDir, "config.yml")
	yamlPath := filepath.Join(tmpDir, "config.yaml")

	if err := os.WriteFile(yamlPath, []byte("server:\n  port: \"1234\"\n"), 0o644); err != nil {
		t.Fatalf("failed to write test fixture: %v", err)
	}

	if err := migrateYamlToYml(ymlPath); err != nil {
		t.Fatalf("migrateYamlToYml() error: %v", err)
	}

	if _, err := os.Stat(ymlPath); err != nil {
		t.Errorf("expected %q to exist after migration: %v", ymlPath, err)
	}
	if _, err := os.Stat(yamlPath); !os.IsNotExist(err) {
		t.Errorf("expected old .yaml file to be removed, stat err = %v", err)
	}
}

func TestMigrateYamlToYmlNoOldFile(t *testing.T) {
	tmpDir := t.TempDir()
	ymlPath := filepath.Join(tmpDir, "config.yml")

	if err := migrateYamlToYml(ymlPath); err != nil {
		t.Fatalf("migrateYamlToYml() error when no old file exists: %v", err)
	}
}

func TestMigrateYamlToYmlNonYmlPath(t *testing.T) {
	if err := migrateYamlToYml("/some/path/config.json"); err != nil {
		t.Fatalf("migrateYamlToYml() should be a no-op for non-.yml paths: %v", err)
	}
}

func TestNormalizeDatabaseConfigUsesURLScheme(t *testing.T) {
	cfg := DatabaseConfig{Driver: "sqlite3", URL: "libsql://example.turso.io?authToken=secret"}
	if err := NormalizeDatabaseConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Driver != "libsql" {
		t.Fatalf("driver = %q, want libsql", cfg.Driver)
	}
}

func TestNormalizeDatabaseConfigRequiresRemoteURL(t *testing.T) {
	cfg := DatabaseConfig{Driver: "turso"}
	if err := NormalizeDatabaseConfig(&cfg); err == nil {
		t.Fatal("expected missing remote URL to be rejected")
	}
}

func TestNormalizeDatabaseConfigValidatesLocalCache(t *testing.T) {
	tests := []struct {
		name string
		cfg  DatabaseConfig
	}{
		{name: "cache requires libsql", cfg: DatabaseConfig{Driver: "sqlite", Cache: DatabaseCacheConfig{Enabled: true, Path: "cache.db"}}},
		{name: "cache requires path", cfg: DatabaseConfig{Driver: "libsql", URL: "libsql://example.turso.io", Cache: DatabaseCacheConfig{Enabled: true}}},
		{name: "cache requires positive interval", cfg: DatabaseConfig{Driver: "libsql", URL: "libsql://example.turso.io", Cache: DatabaseCacheConfig{Enabled: true, Path: "cache.db", SyncInterval: "0s"}}},
		{name: "cache requires positive max age", cfg: DatabaseConfig{Driver: "libsql", URL: "libsql://example.turso.io", Cache: DatabaseCacheConfig{Enabled: true, Path: "cache.db", MaxAge: "0s"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := NormalizeDatabaseConfig(&test.cfg); err == nil {
				t.Fatal("expected invalid cache configuration to be rejected")
			}
		})
	}
	valid := DatabaseConfig{Driver: "libsql", URL: "libsql://example.turso.io", Cache: DatabaseCacheConfig{Enabled: true, Path: "cache.db"}}
	if err := NormalizeDatabaseConfig(&valid); err != nil {
		t.Fatalf("valid cache configuration rejected: %v", err)
	}
}
