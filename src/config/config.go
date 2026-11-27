package config

import (
	"errors"
	"fmt"
	"log"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/common/i18n"
	"github.com/apimgr/citylist/src/scheduler"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
)

var (
	apiVersionPattern            = regexp.MustCompile(`^v[1-9][0-9]*$`)
	seoMetaNamePattern           = regexp.MustCompile(`^[a-z0-9:_.-]+$`)
	verificationAttributePattern = regexp.MustCompile(`^[A-Za-z0-9_:-]+$`)
	accentColorPattern           = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

// Config holds the application configuration
type Config struct {
	Server           ServerConfig `yaml:"server"`
	Web              WebConfig    `yaml:"web"`
	Data             DataConfig   `yaml:"data"`
	Output           OutputConfig `yaml:"output"`
	InitialToken     string       `yaml:"-"`
	GeneratedSecrets []string     `yaml:"-"`
}

// OutputConfig controls optional terminal output behavior.
type OutputConfig struct {
	Emoji *bool `yaml:"emoji,omitempty"`
}

// DataConfig contains configuration for external security data sources.
type DataConfig struct {
	SecurityDir string          `yaml:"security_dir"`
	CVE         CVEDataConfig   `yaml:"cve"`
	Trivy       TrivyDataConfig `yaml:"trivy"`
}

// CVEDataConfig configures the NVD vulnerability feed.
type CVEDataConfig struct {
	Source      string `yaml:"source"`
	FilterByCPE bool   `yaml:"filter_by_cpe"`
	APIKey      string `yaml:"api_key,omitempty"`
}

// TrivyDataConfig configures optional Trivy vulnerability database updates.
type TrivyDataConfig struct {
	Enabled bool   `yaml:"enabled"`
	Source  string `yaml:"source"`
}

// SecurityBlocklistsConfig controls downloaded IP and domain blocklists.
type SecurityBlocklistsConfig struct {
	Enabled      bool                      `yaml:"enabled"`
	ExtraSources []SecurityBlocklistSource `yaml:"extra_sources"`
}

// SecurityBlocklistSource describes one operator-supplied HTTPS list.
type SecurityBlocklistSource struct {
	ID      string `yaml:"id"`
	Type    string `yaml:"type"`
	URL     string `yaml:"url"`
	Enabled *bool  `yaml:"enabled,omitempty"`
}

// GeoIPConfig controls the GeoIP feature without storing its databases in YAML.
type GeoIPConfig struct {
	Enabled bool `yaml:"enabled"`
}

// WebConfig contains public web behavior configured under web.*.
type WebConfig struct {
	UI            WebUIConfig         `yaml:"ui"`
	Robots        WebRobotsConfig     `yaml:"robots"`
	Headers       WebHeadersConfig    `yaml:"headers"`
	Security      WebSecurityConfig   `yaml:"security"`
	APIContact    string              `yaml:"api_contact"`
	Announcements AnnouncementsConfig `yaml:"announcements"`
}

// WebHeadersConfig controls handling of browser privacy signals.
type WebHeadersConfig struct {
	HonorSecGPC bool `yaml:"honor_sec_gpc"`
	HonorDNT    bool `yaml:"honor_dnt"`
}

// AnnouncementsConfig controls server-rendered site-wide notices.
type AnnouncementsConfig struct {
	Enabled  bool           `yaml:"enabled"`
	Messages []Announcement `yaml:"messages"`
}

// Announcement contains one scheduled public banner.
type Announcement struct {
	ID          string `yaml:"id"`
	Type        string `yaml:"type"`
	Title       string `yaml:"title"`
	Message     string `yaml:"message"`
	Start       string `yaml:"start"`
	End         string `yaml:"end"`
	Dismissible bool   `yaml:"dismissible"`
}

// ServerConfig holds server-specific configuration
type ServerConfig struct {
	Port           string                    `yaml:"port"`
	BaseURL        string                    `yaml:"baseurl"`
	Daemonize      bool                      `yaml:"daemonize"`
	Token          string                    `yaml:"token,omitempty"`
	Database       DatabaseConfig            `yaml:"database"`
	Cache          CacheConfig               `yaml:"cache"`
	SSL            SSLConfig                 `yaml:"ssl"`
	FQDN           string                    `yaml:"fqdn"`
	Address        string                    `yaml:"address"`
	Mode           string                    `yaml:"mode"`
	APIVersion     string                    `yaml:"api_version"`
	RateLimit      RateLimitConfig           `yaml:"rate_limit"`
	Update         UpdateConfig              `yaml:"update"`
	Scheduler      SchedulerConfig           `yaml:"scheduler"`
	Backup         BackupConfig              `yaml:"backup"`
	Restore        RestoreConfig             `yaml:"restore"`
	Tor            TorConfig                 `yaml:"tor"`
	I2P            I2PConfig                 `yaml:"i2p"`
	Healthz        HealthzConfig             `yaml:"healthz"`
	Metrics        MetricsConfig             `yaml:"metrics"`
	Logs           LogsConfig                `yaml:"logs"`
	Compliance     ComplianceConfig          `yaml:"compliance"`
	Maintenance    MaintenanceConfig         `yaml:"maintenance"`
	Logging        LoggingConfig             `yaml:"logging"`
	Security       SecurityConfig            `yaml:"security"`
	I18n           I18nConfig                `yaml:"i18n"`
	TrustedProxies TrustedProxiesConfig      `yaml:"trusted_proxies"`
	Privacy        PrivacyConfig             `yaml:"privacy"`
	Tokens         TokensConfig              `yaml:"tokens"`
	Contact        ServerContactConfig       `yaml:"contact"`
	Pages          PagesConfig               `yaml:"pages"`
	Notifications  ServerNotificationsConfig `yaml:"notifications"`
	CORS           CORSConfig                `yaml:"cors"`
	Branding       BrandingConfig            `yaml:"branding"`
	Tracking       TrackingConfig            `yaml:"tracking"`
	SEO            SEOConfig                 `yaml:"seo"`
	GeoIP          GeoIPConfig               `yaml:"geoip"`
}

type RateLimitConfig struct {
	Read        RateLimitRule `yaml:"read"`
	Write       RateLimitRule `yaml:"write"`
	Health      RateLimitRule `yaml:"health"`
	Strict      RateLimitRule `yaml:"strict"`
	AuthFailure RateLimitRule `yaml:"auth_failures"`
	GlobalBurst int           `yaml:"global_burst"`
}

type RateLimitRule struct {
	Requests int `yaml:"requests"`
	Window   int `yaml:"window"`
}

// CacheConfig describes the optional memory or Redis-compatible cache.
type CacheConfig struct {
	Type          string `yaml:"type"`
	URL           string `yaml:"url,omitempty"`
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	Username      string `yaml:"username"`
	Password      string `yaml:"password,omitempty"`
	DB            int    `yaml:"db"`
	TLS           bool   `yaml:"tls"`
	TLSSkipVerify bool   `yaml:"tls_skip_verify"`
	TLSCA         string `yaml:"tls_ca"`
	TLSCert       string `yaml:"tls_cert"`
	TLSKey        string `yaml:"tls_key"`
	PoolSize      int    `yaml:"pool_size"`
	MinIdle       int    `yaml:"min_idle"`
	Timeout       string `yaml:"timeout"`
	Prefix        string `yaml:"prefix"`
	TTL           string `yaml:"ttl"`
}

// SSLConfig controls listener TLS and built-in certificate issuance.
type SSLConfig struct {
	Enabled     bool              `yaml:"enabled"`
	Cert        string            `yaml:"cert"`
	Key         string            `yaml:"key"`
	MinVersion  string            `yaml:"min_version"`
	LetsEncrypt LetsEncryptConfig `yaml:"letsencrypt"`
}

// LetsEncryptConfig contains certificate issuance settings.
type LetsEncryptConfig struct {
	Enabled        bool              `yaml:"enabled"`
	Email          string            `yaml:"email"`
	Challenge      string            `yaml:"challenge"`
	Staging        bool              `yaml:"staging"`
	DNSProvider    string            `yaml:"dns_provider"`
	DNSCredentials map[string]string `yaml:"dns_credentials,omitempty"`
}

// BrandingConfig contains the application's display identity.
type BrandingConfig struct {
	Title       string                `yaml:"title"`
	Tagline     string                `yaml:"tagline"`
	Description string                `yaml:"description"`
	Favicon     string                `yaml:"favicon"`
	Logo        string                `yaml:"logo"`
	Content     BrandingContentConfig `yaml:"content"`
}

// BrandingContentConfig contains operator-supplied public page extensions.
type BrandingContentConfig struct {
	Header  BrandingPageContent `yaml:"header"`
	About   BrandingPageContent `yaml:"about"`
	Privacy BrandingPageContent `yaml:"privacy"`
	Footer  BrandingPageContent `yaml:"footer"`
	Terms   BrandingPageContent `yaml:"terms"`
}

// BrandingPageContent stores a localized operator-supplied page fragment.
type BrandingPageContent struct {
	Format string      `yaml:"format"`
	Body   interface{} `yaml:"body"`
}

// TrackingConfig configures an optional client side analytics provider.
type TrackingConfig struct {
	Type string `yaml:"type"`
	ID   string `yaml:"id"`
	URL  string `yaml:"url"`
}

// TypeName returns the display name for a configured analytics provider.
func (c TrackingConfig) TypeName() string {
	names := map[string]string{"google": "Google Analytics", "matomo": "Matomo", "piwik": "Piwik", "owa": "Open Web Analytics", "fathom": "Fathom", "plausible": "Plausible", "umami": "Umami", "simple": "Simple Analytics", "cloudflare": "Cloudflare Web Analytics"}
	return names[strings.ToLower(strings.TrimSpace(c.Type))]
}

// ValidateTracking validates the provider identifier and URL.
func ValidateTracking(cfg *TrackingConfig) error {
	if cfg == nil {
		return errors.New("tracking config is nil")
	}
	cfg.Type = strings.ToLower(strings.TrimSpace(cfg.Type))
	cfg.ID = strings.TrimSpace(cfg.ID)
	cfg.URL = strings.TrimSpace(cfg.URL)
	if cfg.Type == "" || cfg.Type == "none" {
		cfg.Type, cfg.ID, cfg.URL = "", "", ""
		return nil
	}
	if cfg.Type == "google" || cfg.Type == "simple" || cfg.Type == "cloudflare" {
		cfg.URL = ""
	}
	validURL := func(value string) bool {
		parsed, err := url.Parse(value)
		return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != ""
	}
	switch cfg.Type {
	case "google":
		if !regexp.MustCompile(`^(UA-\d+-\d+|G-[A-Z0-9]+)$`).MatchString(cfg.ID) {
			return errors.New("invalid Google Analytics ID format")
		}
	case "matomo", "piwik":
		if _, err := strconv.Atoi(cfg.ID); err != nil {
			return errors.New("matomo/piwik ID must be an integer")
		}
		if !validURL(cfg.URL) {
			return errors.New("matomo/piwik requires a valid URL")
		}
	case "owa":
		if cfg.ID == "" || !validURL(cfg.URL) {
			return errors.New("OWA requires a site ID and valid URL")
		}
	case "fathom":
		if cfg.ID == "" || (cfg.URL != "" && !validURL(cfg.URL)) {
			return errors.New("fathom requires a site ID and an optional valid URL")
		}
	case "plausible":
		if !regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?$`).MatchString(cfg.ID) || (cfg.URL != "" && !validURL(cfg.URL)) {
			return errors.New("plausible requires a valid domain and an optional valid URL")
		}
	case "umami":
		if _, err := uuid.Parse(cfg.ID); err != nil || !validURL(cfg.URL) {
			return errors.New("umami requires a valid UUID and URL")
		}
	case "simple":
	case "cloudflare":
		if cfg.ID == "" {
			return errors.New("cloudflare requires a beacon token")
		}
	default:
		return fmt.Errorf("unknown tracking type: %s", cfg.Type)
	}
	if cfg.URL != "" && !validURL(cfg.URL) {
		return errors.New("invalid tracking URL")
	}
	return nil
}

// SEOConfig controls generated page metadata and crawler directives.
type SEOConfig struct {
	Keywords       []string           `yaml:"keywords"`
	Author         string             `yaml:"author"`
	OGImage        string             `yaml:"og_image"`
	TwitterHandle  string             `yaml:"twitter_handle"`
	TitleSeparator string             `yaml:"title_separator"`
	MetaTags       map[string]string  `yaml:"meta_tags"`
	Canonical      string             `yaml:"canonical"`
	NoIndex        bool               `yaml:"noindex"`
	OpenGraph      OpenGraphConfig    `yaml:"opengraph"`
	Twitter        TwitterConfig      `yaml:"twitter"`
	Verification   VerificationConfig `yaml:"verification"`
}

// VerificationConfig contains validated site ownership verification values.
type VerificationConfig struct {
	Google    string                  `yaml:"google"`
	Bing      string                  `yaml:"bing"`
	Yandex    string                  `yaml:"yandex"`
	Baidu     string                  `yaml:"baidu"`
	Pinterest string                  `yaml:"pinterest"`
	Facebook  string                  `yaml:"facebook"`
	Custom    []CustomVerificationTag `yaml:"custom"`
}

// CustomVerificationTag describes an additional validated verification meta tag.
type CustomVerificationTag struct {
	Name     string `yaml:"name"`
	Property string `yaml:"property"`
	Content  string `yaml:"content"`
}

// OpenGraphConfig controls OpenGraph page metadata.
type OpenGraphConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Type     string `yaml:"type"`
	SiteName string `yaml:"site_name"`
}

// TwitterConfig controls Twitter card metadata.
type TwitterConfig struct {
	Enabled bool   `yaml:"enabled"`
	Card    string `yaml:"card"`
	Creator string `yaml:"creator"`
}

// MaintenanceConfig controls self-healing and disk-pressure cleanup.
type MaintenanceConfig struct {
	SelfHealing MaintenanceSelfHealingConfig `yaml:"self_healing"`
	Cleanup     MaintenanceCleanupConfig     `yaml:"cleanup"`
	Notify      MaintenanceNotifyConfig      `yaml:"notify"`
}

type MaintenanceSelfHealingConfig struct {
	RetryInterval string `yaml:"retry_interval"`
	MaxAttempts   int    `yaml:"max_attempts"`
}

type MaintenanceCleanupConfig struct {
	DiskThreshold    int `yaml:"disk_threshold"`
	LogRetentionDays int `yaml:"log_retention_days"`
}

type MaintenanceNotifyConfig struct {
	OnEnter bool `yaml:"on_enter"`
	OnExit  bool `yaml:"on_exit"`
}

// ComplianceConfig enables optional compliance reporting standards.
type ComplianceConfig struct {
	GDPR     bool `yaml:"gdpr"`
	CCPA     bool `yaml:"ccpa"`
	HIPAA    bool `yaml:"hipaa"`
	SOC2     bool `yaml:"soc2"`
	PCIDSS   bool `yaml:"pci_dss"`
	ISO27001 bool `yaml:"iso27001"`
	FedRAMP  bool `yaml:"fedramp"`
	LGPD     bool `yaml:"lgpd"`
	PIPEDA   bool `yaml:"pipeda"`
	APPI     bool `yaml:"appi"`
	PDPA     bool `yaml:"pdpa"`
}

// CORSConfig controls cross-origin request policy.
type CORSConfig struct {
	AllowedOrigins   []string `yaml:"allowed_origins"`
	AllowCredentials bool     `yaml:"allow_credentials"`
	MaxAge           int      `yaml:"max_age"`
}

// ServerContactConfig configures contact recipients by role.
type ServerContactConfig struct {
	Admin    ContactRoleConfig `yaml:"admin"`
	Security ContactRoleConfig `yaml:"security"`
	Abuse    ContactRoleConfig `yaml:"abuse"`
	DPO      DPOContactConfig  `yaml:"dpo"`
	General  ContactRoleConfig `yaml:"general"`
}

// ContactRoleConfig contains an email and optional webhook transports.
type ContactRoleConfig struct {
	Email          string            `yaml:"email"`
	Webhooks       map[string]string `yaml:"webhooks"`
	WebhookSecrets map[string]string `yaml:"-"`
}

// DPOContactConfig contains the public data-protection contact.
type DPOContactConfig struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

// PagesConfig contains public page settings.
type PagesConfig struct {
	Contact ContactPageConfig `yaml:"contact"`
	Help    HelpPageConfig    `yaml:"help"`
}

// HelpPageConfig contains project-specific public help content.
type HelpPageConfig struct {
	Content string `yaml:"content"`
	MCP     string `yaml:"mcp"`
}

// ContactPageConfig controls the abuse, DMCA, privacy, and security forms.
type ContactPageConfig struct {
	Enabled          bool                  `yaml:"enabled"`
	Captcha          string                `yaml:"captcha"`
	CaptchaSiteKey   string                `yaml:"captcha_site_key"`
	CaptchaSecretKey string                `yaml:"captcha_secret_key,omitempty"`
	Antispam         ContactAntispamConfig `yaml:"antispam"`
	SuccessMessage   string                `yaml:"success_message"`
}

// ContactAntispamConfig defines bounded contact-form abuse controls.
type ContactAntispamConfig struct {
	Honeypot             bool     `yaml:"honeypot"`
	MinFillSeconds       int      `yaml:"min_fill_seconds"`
	MaxAgeSeconds        int      `yaml:"max_age_seconds"`
	PerIP                int      `yaml:"per_ip"`
	PerEmail             int      `yaml:"per_email"`
	Global               int      `yaml:"global"`
	Window               string   `yaml:"window"`
	MaxLinks             int      `yaml:"max_links"`
	RejectMarkup         bool     `yaml:"reject_markup"`
	BlockedWords         []string `yaml:"blocked_words"`
	BlockedEmailDomains  []string `yaml:"blocked_email_domains"`
	Strikes              int      `yaml:"strikes"`
	BanHours             int      `yaml:"ban_hours"`
	ReviewMinSeconds     int      `yaml:"review_min_seconds"`
	ReviewMaxSeconds     int      `yaml:"review_max_seconds"`
	DuplicateWindowHours int      `yaml:"duplicate_window_hours"`
}

// ServerNotificationsConfig contains outbound notification settings.
type ServerNotificationsConfig struct {
	Email         EmailNotificationConfig         `yaml:"email"`
	DiskLow       DiskLowNotificationConfig       `yaml:"disk_low"`
	DatabaseError DatabaseErrorNotificationConfig `yaml:"database_error"`
}

// DatabaseErrorNotificationConfig controls scheduled database connectivity alerts.
type DatabaseErrorNotificationConfig struct {
	Enabled       bool   `yaml:"enabled"`
	Failures      int    `yaml:"failures"`
	CheckInterval string `yaml:"check_interval"`
	Repeat        string `yaml:"repeat"`
}

// DiskLowNotificationConfig controls scheduled disk-capacity warnings.
type DiskLowNotificationConfig struct {
	Enabled          bool   `yaml:"enabled"`
	ThresholdPercent int    `yaml:"threshold_percent"`
	CheckInterval    string `yaml:"check_interval"`
	Repeat           string `yaml:"repeat"`
}

// EmailNotificationConfig contains SMTP and sender settings.
type EmailNotificationConfig struct {
	SMTP   SMTPConfig      `yaml:"smtp"`
	From   EmailFromConfig `yaml:"from"`
	Events map[string]bool `yaml:"events"`
}

// SMTPConfig contains SMTP connection settings.
type SMTPConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password,omitempty"`
	TLS      string `yaml:"tls"`
}

// EmailFromConfig contains the configured sender identity.
type EmailFromConfig struct {
	Name  string `yaml:"name"`
	Email string `yaml:"email"`
}

// PrivacyConfig controls privacy-sensitive application behavior.
type PrivacyConfig struct {
	Data       PrivacyDataConfig       `yaml:"data"`
	Retention  PrivacyRetentionConfig  `yaml:"retention"`
	Consent    PrivacyConsentConfig    `yaml:"consent"`
	Cookies    PrivacyCookiesConfig    `yaml:"cookies"`
	ThirdParty PrivacyThirdPartyConfig `yaml:"third_party"`
}

type PrivacyRetentionConfig struct {
	Period            string `yaml:"period"`
	ExportAvailable   bool   `yaml:"export_available"`
	DeletionAvailable bool   `yaml:"deletion_available"`
}

type PrivacyCookiesConfig struct {
	Essential   PrivacyCookieCategory  `yaml:"essential"`
	Preferences PrivacyCookieCategory  `yaml:"preferences"`
	Analytics   PrivacyAnalyticsCookie `yaml:"analytics"`
}

type PrivacyCookieCategory struct {
	Enabled     bool   `yaml:"enabled"`
	Description string `yaml:"description"`
}

type PrivacyAnalyticsCookie struct {
	PrivacyCookieCategory    `yaml:",inline"`
	DescriptionSuffixNotSold string `yaml:"description_suffix_not_sold"`
	DescriptionSuffixSold    string `yaml:"description_suffix_sold"`
}

type PrivacyThirdPartyConfig struct {
	Services []PrivacyThirdPartyService `yaml:"services"`
}

type PrivacyThirdPartyService struct {
	Name      string `yaml:"name"`
	Purpose   string `yaml:"purpose"`
	DataSent  string `yaml:"data_sent"`
	PolicyURL string `yaml:"policy_url"`
}

// PrivacyConsentConfig controls the server-rendered cookie consent banner.
type PrivacyConsentConfig struct {
	ShowUntilAcknowledged bool                 `yaml:"show_until_acknowledged"`
	Message               string               `yaml:"message"`
	MessageIfSold         string               `yaml:"message_if_sold"`
	Policy                PrivacyPolicyConfig  `yaml:"policy"`
	Buttons               ConsentButtonsConfig `yaml:"buttons"`
	Position              string               `yaml:"position"`
	ShowPreferences       bool                 `yaml:"show_preferences"`
	PreferencesText       string               `yaml:"preferences_text"`
}

// PrivacyPolicyConfig configures the consent banner's privacy-policy link.
type PrivacyPolicyConfig struct {
	Text string `yaml:"text"`
	URL  string `yaml:"url"`
}

// ConsentButtonsConfig configures the consent banner's action labels.
type ConsentButtonsConfig struct {
	Decline string `yaml:"decline"`
	Accept  string `yaml:"accept"`
}

// PrivacyDataConfig describes the application's data use.
type PrivacyDataConfig struct {
	Sold           bool                      `yaml:"sold"`
	StoredOnServer bool                      `yaml:"stored_on_server"`
	Sharing        []PrivacySharingCondition `yaml:"sharing"`
}

type PrivacySharingCondition struct {
	Condition string `yaml:"condition"`
	When      string `yaml:"when"`
	Data      string `yaml:"data"`
}

func (p PrivacyConfig) GetAnalyticsDescription() string {
	analytics := p.Cookies.Analytics
	description := strings.TrimSpace(analytics.Description)
	suffix := analytics.DescriptionSuffixNotSold
	if p.Data.Sold {
		suffix = analytics.DescriptionSuffixSold
	}
	if strings.TrimSpace(suffix) == "" {
		return description
	}
	if description == "" {
		return strings.TrimSpace(suffix)
	}
	return description + " " + strings.TrimSpace(suffix)
}

// GetConsentMessage selects the configured consent message for the data policy.
func (c PrivacyConfig) GetConsentMessage() string {
	if c.Data.Sold && c.Consent.MessageIfSold != "" {
		return c.Consent.MessageIfSold
	}
	return c.Consent.Message
}

// TokensConfig controls anonymous owner-token behavior.
type TokensConfig struct {
	CookieName    string `yaml:"-"`
	CookieMaxAge  string `yaml:"cookie_max_age"`
	MaxLinked     int    `yaml:"max_linked"`
	IdlePurgeDays int    `yaml:"idle_purge_days"`
}

// TrustedProxiesConfig controls proxy trust and client address selection.
type TrustedProxiesConfig struct {
	Additional     []string `yaml:"additional"`
	ClientIPHeader string   `yaml:"client_ip_header"`
}

// UpdateConfig controls the release channel and operator update policy.
type UpdateConfig struct {
	Branch        string `yaml:"branch"`
	AutoInstall   bool   `yaml:"auto_install"`
	DeferDays     int    `yaml:"defer_days"`
	CLIMinVersion string `yaml:"cli_min_version"`
}

// SchedulerConfig controls the always-running background task scheduler.
type SchedulerConfig struct {
	Timezone      string                   `yaml:"timezone"`
	CatchUpWindow string                   `yaml:"catch_up_window"`
	Tasks         map[string]SchedulerTask `yaml:"tasks"`
}

// SchedulerTask configures one built-in scheduled task.
type SchedulerTask struct {
	Schedule    string `yaml:"schedule"`
	Enabled     bool   `yaml:"enabled"`
	RetryOnFail bool   `yaml:"retry_on_fail"`
	RetryDelay  string `yaml:"retry_delay"`
}

// BackupConfig controls archive encryption, retention and disk safeguards.
type BackupConfig struct {
	EncryptionPassword string                `yaml:"encryption_password,omitempty"`
	Retention          BackupRetentionConfig `yaml:"retention"`
	DiskThreshold      int                   `yaml:"disk_threshold"`
}

// BackupRetentionConfig controls how many full and incremental backups remain.
type BackupRetentionConfig struct {
	MaxBackups   int    `yaml:"max_backups"`
	KeepWeekly   int    `yaml:"keep_weekly"`
	KeepMonthly  int    `yaml:"keep_monthly"`
	KeepYearly   int    `yaml:"keep_yearly"`
	MaxTotalSize string `yaml:"max_total_size"`
}

// RestoreConfig limits archive extraction and import work.
type RestoreConfig struct {
	MaxArchiveSize  string `yaml:"max_archive_size"`
	MaxExpandedSize string `yaml:"max_expanded_size"`
	MaxFiles        int    `yaml:"max_files"`
	MaxFileSize     string `yaml:"max_file_size"`
	MaxRatio        int64  `yaml:"max_ratio"`
}

// TorConfig controls the dedicated Tor hidden service and optional outbound client.
type TorConfig struct {
	Binary                    string `yaml:"binary"`
	UseNetwork                bool   `yaml:"use_network"`
	MaxCircuits               int    `yaml:"max_circuits"`
	StartupTimeout            int    `yaml:"startup_timeout"`
	CircuitTimeout            int    `yaml:"circuit_timeout"`
	BootstrapTimeout          int    `yaml:"bootstrap_timeout"`
	SafeLogging               bool   `yaml:"safe_logging"`
	MaxStreamsPerCircuit      int    `yaml:"max_streams_per_circuit"`
	CloseCircuitOnStreamLimit bool   `yaml:"close_circuit_on_stream_limit"`
	BandwidthRate             string `yaml:"bandwidth_rate"`
	BandwidthBurst            string `yaml:"bandwidth_burst"`
	MaxMonthlyBandwidth       string `yaml:"max_monthly_bandwidth"`
	NumIntroPoints            int    `yaml:"num_intro_points"`
	VirtualPort               int    `yaml:"virtual_port"`
}

// I2PConfig controls the optional I2P eepsite. A nil Enabled preserves the
// distinction between an omitted setting and an explicit false value.
type I2PConfig struct {
	Enabled          *bool  `yaml:"enabled,omitempty"`
	Binary           string `yaml:"binary"`
	SAMAddress       string `yaml:"sam_address"`
	VirtualPort      int    `yaml:"virtual_port"`
	InboundLength    int    `yaml:"inbound_length"`
	OutboundLength   int    `yaml:"outbound_length"`
	InboundQuantity  int    `yaml:"inbound_quantity"`
	OutboundQuantity int    `yaml:"outbound_quantity"`
	SignatureType    int    `yaml:"signature_type"`
	BootstrapTimeout int    `yaml:"bootstrap_timeout"`
}

type I18nConfig struct {
	DefaultLanguage string   `yaml:"default_language"`
	Available       []string `yaml:"available_languages"`
}

// DatabaseConfig holds the local database driver and pool settings.
type DatabaseConfig struct {
	Driver string              `yaml:"driver"`
	URL    string              `yaml:"url,omitempty"`
	Token  string              `yaml:"token,omitempty"`
	Cache  DatabaseCacheConfig `yaml:"cache"`
	Pool   DatabasePoolConfig  `yaml:"pool"`
}

// DatabaseCacheConfig controls the optional local SQLite read replica for libSQL.
type DatabaseCacheConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Path         string `yaml:"path"`
	SyncInterval string `yaml:"sync_interval"`
	OfflineMode  bool   `yaml:"offline_mode"`
	MaxAge       string `yaml:"max_age"`
}

// NormalizeDatabaseDriver maps the documented user-facing aliases to a driver name.
func NormalizeDatabaseDriver(driver string) string {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", "sqlite", "sqlite2", "sqlite3":
		return "sqlite"
	case "libsql", "turso":
		return "libsql"
	default:
		return strings.ToLower(strings.TrimSpace(driver))
	}
}

// NormalizeDatabaseConfig applies URL scheme precedence and validates remote URLs.
func NormalizeDatabaseConfig(cfg *DatabaseConfig) error {
	if cfg == nil {
		return fmt.Errorf("database configuration is required")
	}
	driver := NormalizeDatabaseDriver(cfg.Driver)
	if cfg.URL != "" {
		parsed, err := url.Parse(strings.TrimSpace(cfg.URL))
		if err != nil {
			return fmt.Errorf("server.database.url is invalid; use the URL form")
		}
		switch strings.ToLower(parsed.Scheme) {
		case "libsql", "https":
			driver = "libsql"
		case "", "file":
			if driver == "libsql" {
				return fmt.Errorf("server.database.url must use libsql:// or https:// for the libsql driver")
			}
			driver = "sqlite"
		default:
			return fmt.Errorf("server.database.url uses an unsupported scheme; use the URL form")
		}
	}
	if driver != "sqlite" && driver != "libsql" {
		return fmt.Errorf("server.database.driver must be sqlite or libsql")
	}
	if driver == "libsql" && strings.TrimSpace(cfg.URL) == "" {
		return fmt.Errorf("libsql driver requires server.database.url: use libsql://host?authToken=xxx or https://host with token field")
	}
	if cfg.Cache.Enabled && driver != "libsql" {
		return fmt.Errorf("server.database.cache.enabled requires the libsql driver")
	}
	if cfg.Cache.Enabled {
		if strings.TrimSpace(cfg.Cache.Path) == "" {
			return fmt.Errorf("server.database.cache.path is required when cache is enabled")
		}
		syncInterval := strings.TrimSpace(cfg.Cache.SyncInterval)
		if syncInterval == "" {
			syncInterval = "30s"
		}
		if parsed, err := time.ParseDuration(syncInterval); err != nil || parsed <= 0 {
			return fmt.Errorf("server.database.cache.sync_interval must be a positive duration")
		}
		maxAgeValue := strings.TrimSpace(cfg.Cache.MaxAge)
		if maxAgeValue == "" {
			maxAgeValue = "1h"
		}
		maxAge, err := time.ParseDuration(maxAgeValue)
		if err != nil || maxAge <= 0 {
			return fmt.Errorf("server.database.cache.max_age must be a positive duration")
		}
	}
	cfg.Driver = driver
	return nil
}

// DatabasePoolConfig configures database/sql reader pooling.
type DatabasePoolConfig struct {
	MaxOpen     int    `yaml:"max_open"`
	MaxIdle     int    `yaml:"max_idle"`
	MaxLifetime string `yaml:"max_lifetime"`
	MaxIdleTime string `yaml:"max_idle_time"`
}

// SecurityConfig contains security values merged from secrets.yml.
type SecurityConfig struct {
	EncryptionKey        string                   `yaml:"encryption_key,omitempty"`
	EncryptionKeyVersion int                      `yaml:"encryption_key_version,omitempty"`
	Reports              SecurityReportsConfig    `yaml:"reports"`
	Blocklists           SecurityBlocklistsConfig `yaml:"blocklists"`
}

type SecurityReportsConfig struct {
	RetentionDays int `yaml:"retention_days"`
}

// HealthzConfig controls optional root-level health aliases.
type HealthzConfig struct {
	Root HealthzRootConfig `yaml:"root"`
}

// HealthzRootConfig controls the /healthz compatibility route.
type HealthzRootConfig struct {
	Enabled bool `yaml:"enabled"`
}

// MetricsConfig controls internal metrics collection and endpoint access.
type MetricsConfig struct {
	Enabled         bool              `yaml:"enabled"`
	Root            MetricsRootConfig `yaml:"root"`
	Auth            MetricsAuthConfig `yaml:"auth"`
	IncludeSystem   bool              `yaml:"include_system"`
	IncludeRuntime  bool              `yaml:"include_runtime"`
	Loki            MetricsLokiConfig `yaml:"loki"`
	DurationBuckets []float64         `yaml:"duration_buckets"`
	SizeBuckets     []float64         `yaml:"size_buckets"`
}

// MetricsRootConfig controls the optional Prometheus-compatible root aliases.
type MetricsRootConfig struct {
	Enabled bool `yaml:"enabled"`
}

// MetricsAuthConfig contains the internal metrics authentication policy.
type MetricsAuthConfig struct {
	AllowUnauthenticated bool                 `yaml:"allow_unauthenticated"`
	Tokens               MetricsServiceTokens `yaml:"tokens"`
}

// MetricsServiceTokens are held in secrets.yml and are never serialized to server.yml.
type MetricsServiceTokens struct {
	Prometheus string `yaml:"-"`
	Grafana    string `yaml:"-"`
	Loki       string `yaml:"-"`
}

// MetricsLokiConfig bounds the recent structured log stream.
type MetricsLokiConfig struct {
	MaxEntries int    `yaml:"max_entries"`
	MaxAge     string `yaml:"max_age"`
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	AccessFormat string `yaml:"access_format"`
	Level        string `yaml:"level"`
}

// LogsConfig describes the per-kind log outputs and their shared defaults.
type LogsConfig struct {
	Level    string         `yaml:"level"`
	Defaults LogDefaults    `yaml:"defaults"`
	Access   LogKindConfig  `yaml:"access"`
	Server   LogKindConfig  `yaml:"server"`
	Error    LogKindConfig  `yaml:"error"`
	App      LogKindConfig  `yaml:"app"`
	Auth     LogKindConfig  `yaml:"auth"`
	Audit    AuditLogConfig `yaml:"audit"`
	Security LogKindConfig  `yaml:"security"`
	Debug    LogKindConfig  `yaml:"debug"`
}

// LogDefaults are inherited by log kinds without an explicit value.
type LogDefaults struct {
	Custom string `yaml:"custom"`
	Rotate string `yaml:"rotate"`
	Keep   string `yaml:"keep"`
}

// LogKindConfig configures a single application log destination.
type LogKindConfig struct {
	Enabled  *bool  `yaml:"enabled,omitempty"`
	Filename string `yaml:"filename,omitempty"`
	Format   string `yaml:"format,omitempty"`
	Rotate   string `yaml:"rotate,omitempty"`
	Keep     string `yaml:"keep,omitempty"`
}

// AuditLogConfig includes category controls specific to the audit log.
type AuditLogConfig struct {
	LogKindConfig `yaml:",inline"`
	Compress      bool           `yaml:"compress"`
	Events        AuditLogEvents `yaml:"events"`
	IncludeUA     bool           `yaml:"include_user_agent"`
	MaskEmails    *bool          `yaml:"mask_emails,omitempty"`
	TokenUsage    bool           `yaml:"token_usage"`
}

// AuditLogEvents toggles supported audit event categories.
type AuditLogEvents struct {
	Configuration bool `yaml:"configuration"`
	Security      bool `yaml:"security"`
	Backup        bool `yaml:"backup"`
	Server        bool `yaml:"server"`
}

// WebUIConfig holds web UI configuration
type WebUIConfig struct {
	Theme  string `yaml:"theme"`
	Accent string `yaml:"accent"`
}

// WebRobotsConfig holds robots.txt configuration
type WebRobotsConfig struct {
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
}

// WebSecurityConfig holds security configuration
type WebSecurityConfig struct {
	ReportURL     string   `yaml:"report_url"`
	Contact       string   `yaml:"contact"`
	Expires       string   `yaml:"expires"`
	Policy        string   `yaml:"policy"`
	Keyservers    []string `yaml:"keyservers"`
	PublishPGPKey bool     `yaml:"publish_pgp_key"`
}

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:       "",
			BaseURL:    "/",
			Address:    "[::]",
			Mode:       "production",
			APIVersion: "v1",
			RateLimit: RateLimitConfig{
				Read:        RateLimitRule{Requests: 120, Window: 60},
				Write:       RateLimitRule{Requests: 10, Window: 60},
				Health:      RateLimitRule{Requests: 120, Window: 60},
				Strict:      RateLimitRule{Requests: 10, Window: 900},
				AuthFailure: RateLimitRule{Requests: 5, Window: 900},
				GlobalBurst: 240,
			},
			SSL: SSLConfig{
				MinVersion: "TLS1.2",
				LetsEncrypt: LetsEncryptConfig{
					Email: "", Challenge: "http-01",
				},
			},
			Branding: BrandingConfig{
				Title:       "citylist",
				Description: "Public reference data for cities around the world and United States ZIP codes.",
			},
			SEO: SEOConfig{
				TitleSeparator: " - ",
				OpenGraph:      OpenGraphConfig{Enabled: true, Type: "website"},
				Twitter:        TwitterConfig{Enabled: true, Card: "summary_large_image"},
			},
			Maintenance: MaintenanceConfig{
				SelfHealing: MaintenanceSelfHealingConfig{RetryInterval: "30s"},
				Cleanup:     MaintenanceCleanupConfig{DiskThreshold: 90, LogRetentionDays: 7},
				Notify:      MaintenanceNotifyConfig{OnEnter: true, OnExit: true},
			},
			Backup:   BackupConfig{Retention: BackupRetentionConfig{MaxBackups: 1, MaxTotalSize: "10%"}, DiskThreshold: 90},
			Restore:  RestoreConfig{MaxArchiveSize: "1G", MaxExpandedSize: "4G", MaxFiles: 10000, MaxFileSize: "512M", MaxRatio: 100},
			Security: SecurityConfig{Reports: SecurityReportsConfig{RetentionDays: 365}, Blocklists: SecurityBlocklistsConfig{Enabled: true}},
			GeoIP:    GeoIPConfig{Enabled: true},
			Update:   UpdateConfig{Branch: "stable"},
			Scheduler: SchedulerConfig{
				Timezone: "America/New_York", CatchUpWindow: "1h",
				Tasks: map[string]SchedulerTask{
					"ssl_renewal":            {Schedule: "0 3 * * *", Enabled: true},
					"geoip_update":           {Schedule: "0 3 * * 0", Enabled: true},
					"blocklist_update":       {Schedule: "0 4 * * *", Enabled: true, RetryOnFail: true, RetryDelay: "1h"},
					"security_cleanup":       {Schedule: "30 3 * * *", Enabled: true},
					"keyserver_retry":        {Schedule: "@every 15m", Enabled: true},
					"cve_update":             {Schedule: "0 5 * * *", Enabled: true, RetryOnFail: true, RetryDelay: "1h"},
					"update_check":           {Schedule: "0 6 * * *", Enabled: true},
					"token_cleanup":          {Schedule: "@every 15m", Enabled: true},
					"log_rotation":           {Schedule: "0 0 * * *", Enabled: true},
					"backup_daily":           {Schedule: "0 2 * * *", Enabled: true},
					"backup_hourly":          {Schedule: "@hourly", Enabled: false},
					"disk_check":             {Schedule: "@every 5m", Enabled: true},
					"healthcheck_self":       {Schedule: "@every 5m", Enabled: true},
					"database_check":         {Schedule: "@every 1m", Enabled: true},
					"branding_image_refresh": {Schedule: "0 4 * * *", Enabled: true, RetryOnFail: true, RetryDelay: "1h"},
					"tor_health":             {Schedule: "@every 10m", Enabled: true},
					"i2p_health":             {Schedule: "@every 10m", Enabled: true},
					"dataset_health":         {Schedule: "@every 60s", Enabled: true},
					"maintenance_health":     {Schedule: "@every 10s", Enabled: true},
				},
			},
			Tor: TorConfig{
				MaxCircuits: 32, StartupTimeout: 60, CircuitTimeout: 60,
				BootstrapTimeout: 180, SafeLogging: true, MaxStreamsPerCircuit: 100,
				CloseCircuitOnStreamLimit: true, BandwidthRate: "1 MB",
				BandwidthBurst: "2 MB", MaxMonthlyBandwidth: "100 GB",
				NumIntroPoints: 3, VirtualPort: 80,
			},
			I2P: I2PConfig{
				SAMAddress: "127.0.0.1:7656", VirtualPort: 80,
				InboundLength: 3, OutboundLength: 3, InboundQuantity: 5,
				OutboundQuantity: 5, SignatureType: 7, BootstrapTimeout: 300,
			},
			TrustedProxies: TrustedProxiesConfig{ClientIPHeader: "auto"},
			Privacy: PrivacyConfig{
				Data: PrivacyDataConfig{StoredOnServer: true, Sharing: []PrivacySharingCondition{
					{Condition: "analytics", When: "Tracking configured (server.tracking.type set) AND user consents", Data: "Anonymized: page views, browser type, country"},
					{Condition: "email", When: "SMTP configured for sending emails", Data: "Email address, message content"},
					{Condition: "user_initiated", When: "User explicitly shares content (social buttons, exports)", Data: "Whatever user chooses to share"},
				}},
				Retention: PrivacyRetentionConfig{
					Period:          "Resources you create are kept until they expire or you delete them with your owner token. Preferences are stored only in cookies in your browser. Operational logs are kept for up to 30 days. Anonymized analytics data may be retained for up to 12 months.",
					ExportAvailable: true, DeletionAvailable: true,
				},
				Consent: PrivacyConsentConfig{
					ShowUntilAcknowledged: true,
					Message:               "In accordance with the EU GDPR law this message is being displayed. We use cookies for essential site functionality and, with your consent, for preferences and analytics. Your data is stored on our servers and is never sold.",
					MessageIfSold:         "In accordance with the EU GDPR law this message is being displayed. We use cookies for essential site functionality and, with your consent, for preferences and analytics. Your data may be shared with or sold to third parties as described in our Privacy Policy.",
					Policy:                PrivacyPolicyConfig{Text: "Privacy Policy", URL: "/server/privacy"},
					Buttons:               ConsentButtonsConfig{Decline: "Decline", Accept: "I Agree"},
					Position:              "bottom", ShowPreferences: true, PreferencesText: "Manage Preferences",
				},
				Cookies: PrivacyCookiesConfig{
					Essential:   PrivacyCookieCategory{Enabled: true, Description: "Required for the site to function. Includes security tokens (CSRF) and site preferences. These cookies are strictly necessary and cannot be disabled."},
					Preferences: PrivacyCookieCategory{Enabled: true, Description: "Remember your settings such as theme (dark/light), language, and UI preferences. Disabling will reset to defaults on each visit."},
					Analytics: PrivacyAnalyticsCookie{
						PrivacyCookieCategory:    PrivacyCookieCategory{Enabled: true, Description: "Help us understand how visitors use our site to improve the experience."},
						DescriptionSuffixNotSold: "Analytics data is anonymized and never sold.",
						DescriptionSuffixSold:    "Analytics data may be shared with third parties.",
					},
				},
				ThirdParty: PrivacyThirdPartyConfig{Services: []PrivacyThirdPartyService{}},
			},
			Tokens: TokensConfig{CookieName: "citylist_owner_token_lgqdhqtw", CookieMaxAge: "1y", MaxLinked: 25},
			Contact: ServerContactConfig{
				Security: ContactRoleConfig{Email: "security@{fqdn}"},
				Abuse:    ContactRoleConfig{Webhooks: map[string]string{}},
				General:  ContactRoleConfig{Webhooks: map[string]string{}},
				Admin:    ContactRoleConfig{Webhooks: map[string]string{}},
			},
			Pages: PagesConfig{Contact: ContactPageConfig{
				Enabled: true, Captcha: "simple", SuccessMessage: "contact.success_message",
				Antispam: ContactAntispamConfig{
					Honeypot: true, MinFillSeconds: 3, MaxAgeSeconds: 43200,
					PerIP: 5, PerEmail: 3, Global: 60, Window: "1h", MaxLinks: 10,
					RejectMarkup: true, Strikes: 3, BanHours: 24,
					ReviewMinSeconds: 2, ReviewMaxSeconds: 900, DuplicateWindowHours: 24,
				},
			}, Help: HelpPageConfig{MCP: `### Citylist lookup tools

Citylist exposes read-only lookup tools over its embedded global city and United States ZIP-code datasets. Tools return the stored reference data and never create or modify records.

Example prompts: "Find cities named London," "Look up GeoNames city 2643743," and "Find ZIP codes near 40.7, -74.0." No resources or prompts are exposed by this server.

Tool tips and input limits:
- get_city: provide a GeoNames city ID in id; the result is one city record.
- list_cities: use page and limit for pagination; limit is from 1 to 100.
- search_cities: provide the required name query in q; narrow results with an ISO country code in country and a limit from 1 to 100.
- cities_by_country: provide the required ISO country code in code; use limit to bound the returned list.
- nearest_city: provide required latitude and longitude coordinates to find the closest city.
- nearby_cities: provide lat, lon, and a search radius in kilometers; use limit to bound results.
- get_zipcode: provide a five-digit United States postal code in zip.
- search_zipcodes: provide a city name in q; optionally narrow by the two-letter state abbreviation and set limit.
- zipcodes_by_state: provide a two-letter state abbreviation; use page and limit to paginate.
- nearby_zipcodes: provide lat, lon, and radius_km in kilometers; use limit to bound results.
- random_city: takes no arguments and returns one embedded city record.
- dataset_stats: takes no arguments and returns dataset statistics.
- city_count: takes no arguments and returns the number of city records.`}},
			Notifications: ServerNotificationsConfig{DiskLow: DiskLowNotificationConfig{Enabled: true, ThresholdPercent: 85, CheckInterval: "5m", Repeat: "24h"}, DatabaseError: DatabaseErrorNotificationConfig{Enabled: true, Failures: 3, CheckInterval: "1m", Repeat: "24h"}, Email: EmailNotificationConfig{
				SMTP: SMTPConfig{Port: 587, TLS: "auto"},
				Events: map[string]bool{
					"startup": false, "shutdown": false, "backup_complete": false, "backup_failed": true,
					"ssl_expiring": true, "ssl_renewed": false, "ssl_renewal_failed": true,
					"security_alert": true, "scheduler_error": true, "database_error": true, "update_available": false,
					"update_installed": true, "update_failed": true, "update_staged": true, "disk_low": true,
					"maintenance_entered": true, "maintenance_exited": true, "self_healing_exhausted": true,
				},
			}},
			CORS: CORSConfig{AllowedOrigins: []string{"*"}, AllowCredentials: true, MaxAge: 86400},
			I18n: I18nConfig{
				DefaultLanguage: "en",
				Available:       i18n.LanguageCodes(),
			},
			Database: DatabaseConfig{
				Driver: "sqlite",
				Pool: DatabasePoolConfig{
					MaxOpen:     25,
					MaxIdle:     5,
					MaxLifetime: "5m",
					MaxIdleTime: "1m",
				},
			},
			Cache: CacheConfig{Type: "memory", Host: "localhost", Port: 6379, TLSSkipVerify: true, PoolSize: 10, MinIdle: 2, Timeout: "5s", Prefix: "citylist:", TTL: "1h"},
			Metrics: MetricsConfig{
				Enabled:         true,
				Root:            MetricsRootConfig{Enabled: true},
				IncludeSystem:   true,
				IncludeRuntime:  true,
				Loki:            MetricsLokiConfig{MaxEntries: 1000, MaxAge: "1h"},
				DurationBuckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
				SizeBuckets:     []float64{100, 1000, 10000, 100000, 1000000, 10000000},
			},
			Logging: LoggingConfig{
				AccessFormat: "apache",
				Level:        "info",
			},
			Logs: LogsConfig{
				Defaults: LogDefaults{Custom: "", Rotate: "weekly,50MB", Keep: "none"},
				Access:   LogKindConfig{Format: "apache", Rotate: "monthly"},
				Server:   LogKindConfig{Format: "text"},
				Error:    LogKindConfig{Format: "text"},
				Audit: AuditLogConfig{
					LogKindConfig: LogKindConfig{Enabled: boolValue(true), Format: "json", Rotate: "daily"},
					Events:        AuditLogEvents{Configuration: true, Security: true, Backup: true, Server: true},
					MaskEmails:    boolValue(true),
				},
				Security: LogKindConfig{Format: "fail2ban"},
				Debug:    LogKindConfig{Enabled: boolValue(false), Format: "text"},
			},
		},
		Web: WebConfig{
			APIContact:    "https://github.com/apimgr/citylist/issues",
			Announcements: AnnouncementsConfig{Enabled: true, Messages: []Announcement{}},
			Headers:       WebHeadersConfig{HonorSecGPC: true},
			UI: WebUIConfig{
				Theme:  "dark",
				Accent: "",
			},
			Robots: WebRobotsConfig{
				Allow: []string{"/", "/api"},
				Deny:  []string{"/debug"},
			},
			Security: WebSecurityConfig{
				ReportURL:     "https://github.com/apimgr/citylist/security/advisories/new",
				Contact:       "security@{fqdn}",
				Expires:       "{1year}",
				PublishPGPKey: true,
			},
		},
		Data: DataConfig{
			SecurityDir: "{data_dir}/security",
			CVE:         CVEDataConfig{Source: "https://services.nvd.nist.gov/rest/json/cves/2.0"},
			Trivy:       TrivyDataConfig{Enabled: false, Source: "ghcr.io/aquasecurity/trivy-db:2"},
		},
	}
}

func boolValue(value bool) *bool { return &value }

// migrateYamlToYml migrates from .yaml to .yml extension
func migrateYamlToYml(path string) error {
	if !strings.HasSuffix(path, ".yml") {
		return nil
	}

	oldPath := strings.TrimSuffix(path, ".yml") + ".yaml"
	if _, err := os.Stat(oldPath); err == nil {
		if _, targetErr := os.Stat(path); targetErr == nil {
			return nil
		} else if !os.IsNotExist(targetErr) {
			return targetErr
		}
		if err := os.Rename(oldPath, path); err != nil {
			return err
		}
	}
	return nil
}

// Load loads configuration from a YAML file
func Load(path string) (*Config, error) {
	// Migrate .yaml to .yml if needed
	if err := migrateYamlToYml(path); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); err == nil {
		if err := securePrivateFile(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	cfg := DefaultConfig()

	data, err := readMergedConfig(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Create default config file
			if err := SaveNodeConfig(path, cfg); err != nil {
				return nil, err
			}
			data, err = readMergedConfig(path)
			if err != nil {
				return cfg, err
			}
		} else {
			return cfg, err
		}
	}

	if err := unmarshalConfig(data, cfg); err != nil {
		return cfg, err
	}
	if err := loadLegacyMetricsTokens(data, cfg); err != nil {
		return cfg, err
	}
	generated, err := loadSecrets(filepath.Join(filepath.Dir(path), "secrets.yml"), path, cfg, data)
	if err != nil {
		return cfg, err
	}
	cfg.InitialToken = generated
	migrateAreaConfig(path)
	data, err = readMergedConfig(path)
	if err != nil {
		return cfg, fmt.Errorf("reload configuration after area migration: %w", err)
	}
	if err := unmarshalConfig(data, cfg); err != nil {
		return cfg, err
	}
	if err := loadLegacyMetricsTokens(data, cfg); err != nil {
		return cfg, err
	}
	if _, err := loadSecrets(filepath.Join(filepath.Dir(path), "secrets.yml"), path, cfg, data); err != nil {
		return cfg, err
	}
	cfg.InitialToken = generated
	if err := NormalizeDatabaseConfig(&cfg.Server.Database); err != nil {
		return cfg, err
	}
	if err := validateBlocklistSources(cfg.Server.Security.Blocklists.ExtraSources); err != nil {
		return cfg, err
	}
	if err := validateCVEDataSource(cfg.Data.CVE.Source); err != nil {
		return cfg, err
	}
	applySMTPEnvironment(cfg)
	validateAPIVersion(cfg, path)
	validateI18n(cfg, path)
	validateUpdate(cfg, path)
	validateScheduler(cfg, path)
	validateMaintenance(cfg, path)
	validateDatabaseErrorNotification(cfg, path)
	validateTokens(cfg, path)
	validateMetrics(cfg, path)
	validateSEO(cfg, path)
	validatePrivacy(cfg, path)
	if err := ValidateTracking(&cfg.Server.Tracking); err != nil {
		log.Printf("invalid server.tracking configuration in %s; using defaults: %v", path, err)
		cfg.Server.Tracking = DefaultConfig().Server.Tracking
	}
	if err := validateContact(cfg, path); err != nil {
		return cfg, err
	}
	validateSMTP(cfg, path)

	return cfg, nil
}

func validateSEO(cfg *Config, path string) {
	defaults := DefaultConfig().Server.SEO
	seo := &cfg.Server.SEO
	if strings.TrimSpace(cfg.Server.Branding.Title) == "" {
		log.Printf("Invalid server.branding.title in %s; using defaults", path)
		cfg.Server.Branding.Title = DefaultConfig().Server.Branding.Title
	}
	if len(seo.TitleSeparator) > 8 {
		log.Printf("Invalid server.seo.title_separator in %s; using defaults", path)
		seo.TitleSeparator = defaults.TitleSeparator
	}
	if seo.TitleSeparator == "" {
		seo.TitleSeparator = defaults.TitleSeparator
	}
	if len(seo.MetaTags) > 20 {
		log.Printf("Invalid server.seo.meta_tags in %s; using defaults", path)
		seo.MetaTags = defaults.MetaTags
	}
	for name := range seo.MetaTags {
		if !seoMetaNamePattern.MatchString(name) {
			log.Printf("Invalid server.seo.meta_tags in %s; using defaults", path)
			seo.MetaTags = defaults.MetaTags
			break
		}
	}
	if seo.Canonical != "" {
		parsed, err := url.Parse(seo.Canonical)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			log.Printf("Invalid server.seo.canonical in %s; using defaults", path)
			seo.Canonical = defaults.Canonical
		}
	}
	if seo.OpenGraph.Type == "" {
		seo.OpenGraph.Type = defaults.OpenGraph.Type
	}
	if seo.Twitter.Card != "summary" && seo.Twitter.Card != "summary_large_image" {
		log.Printf("Invalid server.seo.twitter.card in %s; using defaults", path)
		seo.Twitter.Card = defaults.Twitter.Card
	}
	verification := &seo.Verification
	for _, item := range []struct {
		name      string
		value     *string
		pattern   *regexp.Regexp
		maxLength int
	}{
		{"google", &verification.Google, regexp.MustCompile(`^[A-Za-z0-9_-]+$`), 43},
		{"bing", &verification.Bing, regexp.MustCompile(`^[A-F0-9]+$`), 32},
		{"yandex", &verification.Yandex, regexp.MustCompile(`^[a-f0-9]+$`), 32},
		{"baidu", &verification.Baidu, regexp.MustCompile(`^[A-Za-z0-9]+$`), 32},
		{"pinterest", &verification.Pinterest, regexp.MustCompile(`^[a-f0-9]+$`), 32},
		{"facebook", &verification.Facebook, regexp.MustCompile(`^[a-z0-9]+$`), 64},
	} {
		if *item.value != "" && (len(*item.value) > item.maxLength || !item.pattern.MatchString(*item.value)) {
			log.Printf("Invalid server.seo.verification.%s in %s; ignoring value", item.name, path)
			*item.value = ""
		}
	}
	for index := range verification.Custom {
		tag := &verification.Custom[index]
		validAttribute := func(value string) bool {
			return value == "" || verificationAttributePattern.MatchString(value)
		}
		valid := (tag.Name != "") != (tag.Property != "") && validAttribute(tag.Name) && validAttribute(tag.Property) &&
			strings.TrimSpace(tag.Content) != "" && len(tag.Content) <= 256
		if !valid {
			log.Printf("Invalid server.seo.verification.custom[%d] in %s; ignoring tag", index, path)
			verification.Custom[index] = CustomVerificationTag{}
		}
	}
	if cfg.Web.UI.Accent != "" && !accentColorPattern.MatchString(cfg.Web.UI.Accent) {
		log.Printf("Invalid web.ui.accent in %s; ignoring value", path)
		cfg.Web.UI.Accent = ""
	}
}

func validateMetrics(cfg *Config, path string) {
	validBuckets := func(values []float64) bool {
		if len(values) == 0 {
			return true
		}
		previous := 0.0
		for _, value := range values {
			if math.IsNaN(value) || math.IsInf(value, 0) || value <= previous {
				return false
			}
			previous = value
		}
		return true
	}
	defaults := DefaultConfig().Server.Metrics
	if !validBuckets(cfg.Server.Metrics.DurationBuckets) {
		log.Printf("Invalid server.metrics.duration_buckets in %s; using defaults", path)
		cfg.Server.Metrics.DurationBuckets = defaults.DurationBuckets
	}
	if !validBuckets(cfg.Server.Metrics.SizeBuckets) {
		log.Printf("Invalid server.metrics.size_buckets in %s; using defaults", path)
		cfg.Server.Metrics.SizeBuckets = defaults.SizeBuckets
	}
	if cfg.Server.Metrics.Loki.MaxEntries < 1 {
		cfg.Server.Metrics.Loki.MaxEntries = defaults.Loki.MaxEntries
	}
	if cfg.Server.Metrics.Loki.MaxEntries > 10000 {
		cfg.Server.Metrics.Loki.MaxEntries = 10000
	}
	maxAge, err := time.ParseDuration(cfg.Server.Metrics.Loki.MaxAge)
	if err != nil || maxAge <= 0 {
		log.Printf("Invalid server.metrics.loki.max_age in %s; using defaults", path)
		cfg.Server.Metrics.Loki.MaxAge = defaults.Loki.MaxAge
	}
}

func loadLegacyMetricsTokens(data []byte, cfg *Config) error {
	values := map[string]interface{}{}
	if err := yaml.Unmarshal(data, &values); err != nil {
		return fmt.Errorf("read legacy metrics tokens: %w", err)
	}
	server, _ := values["server"].(map[string]interface{})
	metrics, _ := server["metrics"].(map[string]interface{})
	auth, _ := metrics["auth"].(map[string]interface{})
	tokens, _ := auth["tokens"].(map[string]interface{})
	cfg.Server.Metrics.Auth.Tokens.Prometheus = stringValue(tokens["prometheus"])
	cfg.Server.Metrics.Auth.Tokens.Grafana = stringValue(tokens["grafana"])
	cfg.Server.Metrics.Auth.Tokens.Loki = stringValue(tokens["loki"])
	return nil
}

// LoadBasic reads only non-secret listener settings for health checks.
func LoadBasic(path string) (*Config, error) {
	if err := migrateYamlToYml(path); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); err == nil {
		if err := securePrivateFile(path); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	cfg := DefaultConfig()
	data, err := readMergedConfig(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := unmarshalConfig(data, cfg); err != nil {
		return cfg, err
	}
	validateAPIVersion(cfg, path)
	validateI18n(cfg, path)
	validateUpdate(cfg, path)
	validateScheduler(cfg, path)
	validateMaintenance(cfg, path)
	validateDatabaseErrorNotification(cfg, path)
	validateTokens(cfg, path)
	validatePrivacy(cfg, path)
	if err := ValidateTracking(&cfg.Server.Tracking); err != nil {
		log.Printf("invalid server.tracking configuration in %s; using defaults: %v", path, err)
		cfg.Server.Tracking = DefaultConfig().Server.Tracking
	}
	validateSMTP(cfg, path)
	return cfg, nil
}

func validatePrivacy(cfg *Config, path string) {
	privacy := &cfg.Server.Privacy
	defaults := DefaultConfig().Server.Privacy
	if privacy.Data.Sharing == nil {
		privacy.Data.Sharing = defaults.Data.Sharing
	}
	if strings.TrimSpace(privacy.Retention.Period) == "" {
		privacy.Retention.Period = defaults.Retention.Period
	}
	privacy.Cookies.Essential.Enabled = true
	if strings.TrimSpace(privacy.Cookies.Essential.Description) == "" {
		privacy.Cookies.Essential.Description = defaults.Cookies.Essential.Description
	}
	if strings.TrimSpace(privacy.Cookies.Preferences.Description) == "" {
		privacy.Cookies.Preferences.Description = defaults.Cookies.Preferences.Description
	}
	if strings.TrimSpace(privacy.Cookies.Analytics.Description) == "" {
		privacy.Cookies.Analytics.Description = defaults.Cookies.Analytics.Description
	}
	if strings.TrimSpace(privacy.Cookies.Analytics.DescriptionSuffixNotSold) == "" {
		privacy.Cookies.Analytics.DescriptionSuffixNotSold = defaults.Cookies.Analytics.DescriptionSuffixNotSold
	}
	if strings.TrimSpace(privacy.Cookies.Analytics.DescriptionSuffixSold) == "" {
		privacy.Cookies.Analytics.DescriptionSuffixSold = defaults.Cookies.Analytics.DescriptionSuffixSold
	}
	if privacy.ThirdParty.Services == nil {
		privacy.ThirdParty.Services = defaults.ThirdParty.Services
	}
	consent := &cfg.Server.Privacy.Consent
	consentDefaults := defaults.Consent
	if strings.TrimSpace(consent.Message) == "" {
		consent.Message = consentDefaults.Message
	}
	if strings.TrimSpace(consent.MessageIfSold) == "" {
		consent.MessageIfSold = consentDefaults.MessageIfSold
	}
	if strings.TrimSpace(consent.Policy.Text) == "" {
		consent.Policy.Text = consentDefaults.Policy.Text
	}
	if strings.TrimSpace(consent.Policy.URL) == "" {
		consent.Policy.URL = consentDefaults.Policy.URL
	}
	if strings.TrimSpace(consent.Buttons.Decline) == "" {
		consent.Buttons.Decline = consentDefaults.Buttons.Decline
	}
	if strings.TrimSpace(consent.Buttons.Accept) == "" {
		consent.Buttons.Accept = consentDefaults.Buttons.Accept
	}
	if strings.TrimSpace(consent.PreferencesText) == "" {
		consent.PreferencesText = consentDefaults.PreferencesText
	}
	consent.Position = strings.ToLower(strings.TrimSpace(consent.Position))
	if consent.Position != "top" && consent.Position != "bottom" {
		log.Printf("warning: invalid server.privacy.consent.position in %s; using bottom", path)
		consent.Position = consentDefaults.Position
	}
}

func validateContact(cfg *Config, path string) error {
	contact := &cfg.Server.Pages.Contact
	if strings.TrimSpace(contact.Captcha) == "" {
		contact.Captcha = "simple"
	}
	contact.Captcha = strings.ToLower(strings.TrimSpace(contact.Captcha))
	if contact.Captcha != "simple" && contact.Captcha != "recaptcha" && contact.Captcha != "hcaptcha" {
		log.Printf("warning: invalid server.pages.contact.captcha in %s; using simple", path)
		contact.Captcha = "simple"
	}
	if contact.Captcha != "simple" && (strings.TrimSpace(contact.CaptchaSiteKey) == "" || strings.TrimSpace(contact.CaptchaSecretKey) == "") {
		return fmt.Errorf("server.pages.contact.captcha=%s requires server.pages.contact.captcha_site_key and captcha_secret_key in %s", contact.Captcha, path)
	}
	defaults := DefaultConfig().Server.Pages.Contact
	anti := &contact.Antispam
	defaultAnti := defaults.Antispam
	if anti.MinFillSeconds < 0 {
		anti.MinFillSeconds = defaultAnti.MinFillSeconds
	}
	if anti.MaxAgeSeconds <= 0 {
		anti.MaxAgeSeconds = defaultAnti.MaxAgeSeconds
	}
	if anti.PerIP < 0 {
		anti.PerIP = defaultAnti.PerIP
	}
	if anti.PerEmail < 0 {
		anti.PerEmail = defaultAnti.PerEmail
	}
	if anti.Global < 0 {
		anti.Global = defaultAnti.Global
	}
	if _, err := time.ParseDuration(anti.Window); err != nil || anti.Window == "" {
		anti.Window = defaultAnti.Window
	}
	if anti.MaxLinks < 0 {
		anti.MaxLinks = defaultAnti.MaxLinks
	}
	if anti.Strikes < 0 {
		anti.Strikes = defaultAnti.Strikes
	}
	if anti.BanHours < 0 {
		anti.BanHours = defaultAnti.BanHours
	}
	if anti.ReviewMinSeconds < 0 {
		anti.ReviewMinSeconds = defaultAnti.ReviewMinSeconds
	}
	if anti.ReviewMaxSeconds <= 0 {
		anti.ReviewMaxSeconds = defaultAnti.ReviewMaxSeconds
	}
	if anti.DuplicateWindowHours < 0 {
		anti.DuplicateWindowHours = defaultAnti.DuplicateWindowHours
	}
	if strings.TrimSpace(contact.SuccessMessage) == "" {
		contact.SuccessMessage = defaults.SuccessMessage
	}
	return nil
}

func validateSMTP(cfg *Config, path string) {
	smtp := &cfg.Server.Notifications.Email.SMTP
	if smtp.Port <= 0 || smtp.Port > 65535 {
		smtp.Port = 587
	}
	smtp.TLS = strings.ToLower(strings.TrimSpace(smtp.TLS))
	switch smtp.TLS {
	case "", "auto":
		smtp.TLS = "auto"
	case "starttls", "tls", "none":
	default:
		log.Printf("warning: invalid server.notifications.email.smtp.tls in %s; using auto", path)
		smtp.TLS = "auto"
	}
}

func validateBlocklistSources(sources []SecurityBlocklistSource) error {
	for index, source := range sources {
		parsed, err := url.Parse(strings.TrimSpace(source.URL))
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return fmt.Errorf("server.security.blocklists.extra_sources[%d].url must be an HTTPS URL", index)
		}
	}
	return nil
}

func validateCVEDataSource(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("data.cve.source must be an HTTPS URL")
	}
	return nil
}

func applySMTPEnvironment(cfg *Config) {
	smtp := &cfg.Server.Notifications.Email.SMTP
	from := &cfg.Server.Notifications.Email.From
	if value := os.Getenv("SMTP_HOST"); value != "" {
		smtp.Host = value
	}
	if value := os.Getenv("SMTP_PORT"); value != "" {
		if port, err := strconv.Atoi(value); err == nil && port > 0 && port <= 65535 {
			smtp.Port = port
		}
	}
	if value := os.Getenv("SMTP_USERNAME"); value != "" {
		smtp.Username = value
	}
	if value := os.Getenv("SMTP_PASSWORD"); value != "" {
		smtp.Password = value
	}
	if value := os.Getenv("SMTP_TLS"); value != "" {
		smtp.TLS = value
	}
	if value := os.Getenv("SMTP_FROM_NAME"); value != "" {
		from.Name = value
	}
	if value := os.Getenv("SMTP_FROM_EMAIL"); value != "" {
		from.Email = value
	}
}

func validateAPIVersion(cfg *Config, path string) {
	if !apiVersionPattern.MatchString(cfg.Server.APIVersion) {
		log.Printf("warning: invalid server.api_version in %s; using v1", path)
		cfg.Server.APIVersion = "v1"
	}
}

func validateUpdate(cfg *Config, path string) {
	branch := strings.ToLower(strings.TrimSpace(cfg.Server.Update.Branch))
	switch branch {
	case "daily", "rolling", "dev", "development":
		branch = "devel"
	case "stable", "beta", "devel":
	default:
		log.Printf("warning: invalid server.update.branch in %s; using stable", path)
		branch = "stable"
	}
	cfg.Server.Update.Branch = branch
	if cfg.Server.Update.DeferDays < 0 || cfg.Server.Update.DeferDays > 365 {
		log.Printf("warning: invalid server.update.defer_days in %s; using 0", path)
		cfg.Server.Update.DeferDays = 0
	}
}

func validateScheduler(cfg *Config, path string) {
	defaults := DefaultConfig().Server.Scheduler
	nonSkippable := map[string]struct{}{
		"ssl_renewal": {}, "security_cleanup": {}, "keyserver_retry": {}, "token_cleanup": {},
		"log_rotation": {}, "healthcheck_self": {}, "branding_image_refresh": {}, "dataset_health": {}, "maintenance_health": {},
	}
	schedulerConfig := &cfg.Server.Scheduler
	if legacyTask, ok := schedulerConfig.Tasks["pgp_publish_retry"]; ok {
		if _, hasCanonicalTask := schedulerConfig.Tasks["keyserver_retry"]; !hasCanonicalTask {
			schedulerConfig.Tasks["keyserver_retry"] = legacyTask
		}
		delete(schedulerConfig.Tasks, "pgp_publish_retry")
	}
	if _, err := time.LoadLocation(schedulerConfig.Timezone); err != nil {
		log.Printf("warning: invalid server.scheduler.timezone in %s; using %s", path, defaults.Timezone)
		schedulerConfig.Timezone = defaults.Timezone
	}
	if catchUp, err := time.ParseDuration(schedulerConfig.CatchUpWindow); err != nil || catchUp < 0 {
		log.Printf("warning: invalid server.scheduler.catch_up_window in %s; using %s", path, defaults.CatchUpWindow)
		schedulerConfig.CatchUpWindow = defaults.CatchUpWindow
	}
	if schedulerConfig.Tasks == nil {
		schedulerConfig.Tasks = make(map[string]SchedulerTask, len(defaults.Tasks))
	}
	for name, defaultTask := range defaults.Tasks {
		task, ok := schedulerConfig.Tasks[name]
		if !ok {
			schedulerConfig.Tasks[name] = defaultTask
			continue
		}
		if err := scheduler.ValidateSchedule(task.Schedule); err != nil {
			log.Printf("warning: invalid server.scheduler.tasks.%s.schedule in %s; using %s", name, path, defaultTask.Schedule)
			task.Schedule = defaultTask.Schedule
		}
		if name == "branding_image_refresh" && task.Schedule != defaultTask.Schedule {
			log.Printf("warning: server.scheduler.tasks.%s has a fixed schedule in %s; using %s", name, path, defaultTask.Schedule)
			task.Schedule = defaultTask.Schedule
		}
		if name == "dataset_health" && task.Schedule != defaultTask.Schedule {
			log.Printf("warning: server.scheduler.tasks.%s has a fixed schedule in %s; using %s", name, path, defaultTask.Schedule)
			task.Schedule = defaultTask.Schedule
		}
		if name == "maintenance_health" && task.Schedule != defaultTask.Schedule {
			log.Printf("warning: server.scheduler.tasks.%s has a fixed schedule in %s; using %s", name, path, defaultTask.Schedule)
			task.Schedule = defaultTask.Schedule
		}
		if _, required := nonSkippable[name]; required && !task.Enabled {
			log.Printf("warning: server.scheduler.tasks.%s cannot be disabled in %s; enabling it", name, path)
			task.Enabled = true
		}
		if task.RetryOnFail {
			if delay, err := time.ParseDuration(task.RetryDelay); err != nil || delay <= 0 {
				fallback := defaultTask.RetryDelay
				if fallback == "" {
					fallback = "5m"
				}
				log.Printf("warning: invalid server.scheduler.tasks.%s.retry_delay in %s; using %s", name, path, fallback)
				task.RetryDelay = fallback
			}
		}
		schedulerConfig.Tasks[name] = task
	}
}

func validateMaintenance(cfg *Config, path string) {
	defaults := DefaultConfig().Server.Maintenance
	settings := &cfg.Server.Maintenance
	retry, err := time.ParseDuration(settings.SelfHealing.RetryInterval)
	if err != nil || retry < 10*time.Second {
		log.Printf("warning: invalid server.maintenance.self_healing.retry_interval in %s; using %s", path, defaults.SelfHealing.RetryInterval)
		settings.SelfHealing.RetryInterval = defaults.SelfHealing.RetryInterval
	}
	if settings.SelfHealing.MaxAttempts < 0 {
		log.Printf("warning: invalid server.maintenance.self_healing.max_attempts in %s; using 0", path)
		settings.SelfHealing.MaxAttempts = 0
	}
	if settings.Cleanup.DiskThreshold < 1 || settings.Cleanup.DiskThreshold > 99 {
		log.Printf("warning: invalid server.maintenance.cleanup.disk_threshold in %s; using %d", path, defaults.Cleanup.DiskThreshold)
		settings.Cleanup.DiskThreshold = defaults.Cleanup.DiskThreshold
	}
	if settings.Cleanup.LogRetentionDays < 1 {
		log.Printf("warning: invalid server.maintenance.cleanup.log_retention_days in %s; using %d", path, defaults.Cleanup.LogRetentionDays)
		settings.Cleanup.LogRetentionDays = defaults.Cleanup.LogRetentionDays
	}
}

func validateDatabaseErrorNotification(cfg *Config, path string) {
	defaults := DefaultConfig().Server.Notifications.DatabaseError
	settings := &cfg.Server.Notifications.DatabaseError
	if settings.Failures < 1 || settings.Failures > 100 {
		log.Printf("warning: invalid server.notifications.database_error.failures in %s; using %d", path, defaults.Failures)
		settings.Failures = defaults.Failures
	}
	if interval, err := time.ParseDuration(settings.CheckInterval); err != nil || interval <= 0 {
		log.Printf("warning: invalid server.notifications.database_error.check_interval in %s; using %s", path, defaults.CheckInterval)
		settings.CheckInterval = defaults.CheckInterval
	}
	if repeat, err := time.ParseDuration(settings.Repeat); err != nil || repeat <= 0 {
		log.Printf("warning: invalid server.notifications.database_error.repeat in %s; using %s", path, defaults.Repeat)
		settings.Repeat = defaults.Repeat
	}
}

func validateI18n(cfg *Config, path string) {
	defaults := DefaultConfig().Server.I18n
	if !i18n.IsSupported(cfg.Server.I18n.DefaultLanguage) {
		log.Printf("warning: invalid server.i18n.default_language in %s; using %s", path, defaults.DefaultLanguage)
		cfg.Server.I18n.DefaultLanguage = defaults.DefaultLanguage
	}
	if len(cfg.Server.I18n.Available) != len(defaults.Available) {
		log.Printf("warning: server.i18n.available_languages in %s must include every embedded language; using defaults", path)
		cfg.Server.I18n.Available = defaults.Available
		return
	}
	seen := make(map[string]bool, len(cfg.Server.I18n.Available))
	for _, code := range cfg.Server.I18n.Available {
		if !i18n.IsSupported(code) || seen[strings.ToLower(code)] {
			log.Printf("warning: invalid server.i18n.available_languages in %s; using defaults", path)
			cfg.Server.I18n.Available = defaults.Available
			return
		}
		code = strings.ToLower(code)
		seen[code] = true
	}
	for index := range cfg.Server.I18n.Available {
		cfg.Server.I18n.Available[index] = strings.ToLower(cfg.Server.I18n.Available[index])
	}
}

func validateTokens(cfg *Config, path string) {
	defaults := DefaultConfig().Server.Tokens
	cfg.Server.Tokens.CookieName = defaults.CookieName
	if cfg.Server.Tokens.CookieMaxAge == "" || tokenCookieMaxAgeSeconds(cfg.Server.Tokens.CookieMaxAge) <= 0 {
		log.Printf("warning: invalid server.tokens.cookie_max_age in %s; using %s", path, defaults.CookieMaxAge)
		cfg.Server.Tokens.CookieMaxAge = defaults.CookieMaxAge
	}
	if cfg.Server.Tokens.MaxLinked < 0 {
		log.Printf("warning: invalid server.tokens.max_linked in %s; using %d", path, defaults.MaxLinked)
		cfg.Server.Tokens.MaxLinked = defaults.MaxLinked
	}
	if cfg.Server.Tokens.IdlePurgeDays < 0 {
		log.Printf("warning: invalid server.tokens.idle_purge_days in %s; using 0", path)
		cfg.Server.Tokens.IdlePurgeDays = defaults.IdlePurgeDays
	}
}

func (cfg TokensConfig) CookieMaxAgeSeconds() int {
	return tokenCookieMaxAgeSeconds(cfg.CookieMaxAge)
}

func tokenCookieMaxAgeSeconds(value string) int {
	value = strings.TrimSpace(strings.ToLower(value))
	if strings.HasSuffix(value, "y") {
		years, err := strconv.Atoi(strings.TrimSuffix(value, "y"))
		if err != nil || years < 1 || years > int(^uint(0)>>1)/(365*24*60*60) {
			return 0
		}
		return years * 365 * 24 * 60 * 60
	}
	if strings.HasSuffix(value, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(value, "d"))
		if err != nil || days < 1 || days > int(^uint(0)>>1)/(24*60*60) {
			return 0
		}
		return days * 24 * 60 * 60
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 || duration.Seconds() > float64(int(^uint(0)>>1)) {
		return 0
	}
	return int(duration.Seconds())
}

// Save saves configuration to a YAML file
func Save(path string, cfg *Config) error {
	data, err := marshalSafeConfig(cfg)
	if err != nil {
		return err
	}
	if err := writePrivateFileAtomically(path, append([]byte(nodeConfigHeader), data...)); err != nil {
		return err
	}
	migrateAreaConfigWithPrecedence(path, true)
	return nil
}

// SaveNodeConfig writes only listener and node-specific settings to server.yml.
func SaveNodeConfig(path string, cfg *Config) error {
	data, err := marshalSafeConfig(cfg)
	if err != nil {
		return err
	}
	values, err := decodeConfigMap(data)
	if err != nil {
		return err
	}
	server, _ := values["server"].(map[string]any)
	for key := range server {
		if !nodeServerKeys[key] {
			delete(server, key)
		}
	}
	for key := range values {
		if key != "server" {
			delete(values, key)
		}
	}
	nodeData, err := yaml.Marshal(values)
	if err != nil {
		return err
	}
	return writePrivateFileAtomically(path, append([]byte(nodeConfigHeader), nodeData...))
}

func marshalSafeConfig(cfg *Config) ([]byte, error) {
	safeConfig := *cfg
	safeConfig.Server.Token = ""
	safeConfig.Server.Database.Token = ""
	if databaseURLHasCredential(safeConfig.Server.Database.URL) {
		safeConfig.Server.Database.URL = ""
	}
	safeConfig.Server.Security.EncryptionKey = ""
	safeConfig.Server.Security.EncryptionKeyVersion = 0
	safeConfig.Data.CVE.APIKey = ""
	safeConfig.Server.Notifications.Email.SMTP.Password = ""
	safeConfig.Server.Pages.Contact.CaptchaSecretKey = ""
	return yaml.Marshal(&safeConfig)
}
