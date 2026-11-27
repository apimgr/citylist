package server

import (
	"context"
	"crypto/tls"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/apimgr/citylist/src/cities"
	"github.com/apimgr/citylist/src/common/i18n"
	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/database"
	"github.com/apimgr/citylist/src/graphql"
	"github.com/apimgr/citylist/src/health"
	"github.com/apimgr/citylist/src/mode"
	"github.com/apimgr/citylist/src/overlay/i2p"
	"github.com/apimgr/citylist/src/overlay/tor"
	"github.com/apimgr/citylist/src/scheduler"
	"github.com/apimgr/citylist/src/server/metrics"
	sslmanager "github.com/apimgr/citylist/src/ssl"
	"github.com/apimgr/citylist/src/zipcodes"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	graphqlgophers "github.com/graph-gophers/graphql-go"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

//go:embed static/* template
var staticFS embed.FS

//go:embed template
var templateFS embed.FS

func mustParseEmbeddedTemplate(name string, functions template.FuncMap, partials ...string) *template.Template {
	content, err := templateFS.ReadFile(name)
	if err != nil {
		panic(err)
	}
	parsed := template.Must(template.New(path.Base(name)).Funcs(functions).Parse(string(content)))
	for _, partial := range partials {
		content, err := templateFS.ReadFile(partial)
		if err != nil {
			panic(err)
		}
		parsed = template.Must(parsed.Parse(string(content)))
	}
	return parsed
}

// Build info (set from main)
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Server represents the HTTP server
type Server struct {
	router                  *chi.Mux
	configMu                sync.RWMutex
	citySvc                 *cities.Service
	zipSvc                  *zipcodes.Service
	store                   *database.Store
	scheduler               *scheduler.Scheduler
	configWatcher           *config.ConfigWatcher
	cfg                     *config.Config
	address                 string
	port                    string
	configDir               string
	dataDir                 string
	cacheDir                string
	brandingImageMu         sync.RWMutex
	brandingImages          map[string]brandingImage
	brandingVariants        map[string]brandingImage
	brandingImageWarnings   map[string]struct{}
	metricsCollector        *metrics.SystemCollector
	version                 string
	buildDate               string
	commit                  string
	baseURL                 string
	graphQL                 *graphqlgophers.Schema
	graphQLByLanguage       map[string]*graphqlgophers.Schema
	torManager              *tor.Manager
	i2pManager              *i2p.Manager
	torStartupFailed        bool
	i2pStartupFailed        bool
	startedAt               time.Time
	requestTotal            atomic.Uint64
	requestActive           atomic.Uint64
	shuttingDown            atomic.Bool
	maintenanceMu           sync.RWMutex
	maintenance             *maintenanceState
	requestHoursMu          sync.Mutex
	requestHours            [24]requestHour
	rateMu                  sync.Mutex
	rateBuckets             map[string]rateBucket
	trustedPrefixes         []netip.Prefix
	trustedProxyMu          sync.RWMutex
	trustedProxyRefreshMu   sync.Mutex
	trustedProxyLastRefresh time.Time
}

type requestHour struct {
	hour  int64
	count uint64
}

type maintenanceState struct {
	reason      string
	since       time.Time
	attempts    int
	lastAttempt *time.Time
	nextAttempt *time.Time
	exhausted   bool
}

type MaintenanceTransition struct {
	Entered   bool
	Recovered bool
	Exhausted bool
	Attempted bool
	State     *Maintenance
}

// SetOverlayManagers attaches the optional network managers used by health
// reporting. The managers remain owned and shut down by the application.
func (s *Server) SetOverlayManagers(torManager *tor.Manager, i2pManager *i2p.Manager, torStartupFailed, i2pStartupFailed bool) {
	s.torManager = torManager
	s.i2pManager = i2pManager
	s.torStartupFailed = torStartupFailed
	s.i2pStartupFailed = i2pStartupFailed
}

func (s *Server) RefreshMaintenance() MaintenanceTransition {
	reason := criticalHealthReason()
	now := time.Now().UTC()
	s.maintenanceMu.Lock()
	defer s.maintenanceMu.Unlock()
	if reason == "" {
		if s.maintenance == nil || s.maintenance.exhausted {
			return MaintenanceTransition{State: s.maintenanceSnapshotLocked()}
		}
		recoveredState := s.maintenanceSnapshotLocked()
		s.maintenance = nil
		slog.Info("server.maintenance_recovered")
		return MaintenanceTransition{Recovered: true, State: recoveredState}
	}
	entered := s.maintenance == nil
	if entered {
		s.maintenance = &maintenanceState{reason: reason, since: now}
		slog.Error("server.maintenance_entered", "reason", reason)
	} else if !s.maintenance.exhausted {
		s.maintenance.reason = reason
	}
	state := s.maintenanceSnapshotLocked()
	return MaintenanceTransition{Entered: entered, State: state}
}

func criticalHealthReason() string {
	for _, check := range health.Results() {
		if check.Status != "error" {
			continue
		}
		switch check.Key {
		case "database":
			return "database_connection"
		case "disk":
			return "disk_full"
		}
	}
	return ""
}

func (s *Server) MaintenanceActive() bool {
	s.maintenanceMu.RLock()
	active := s.maintenance != nil
	s.maintenanceMu.RUnlock()
	return active || criticalHealthReason() != ""
}

func (s *Server) BeginMaintenanceAttempt() MaintenanceTransition {
	s.maintenanceMu.Lock()
	defer s.maintenanceMu.Unlock()
	if s.maintenance == nil || s.maintenance.exhausted {
		return MaintenanceTransition{State: s.maintenanceSnapshotLocked()}
	}
	settings := s.cfg.Server.Maintenance.SelfHealing
	if settings.MaxAttempts > 0 && s.maintenance.attempts >= settings.MaxAttempts {
		s.maintenance.exhausted = true
		s.maintenance.nextAttempt = nil
		slog.Error("server.self_healing_exhausted", "reason", s.maintenance.reason, "attempts", s.maintenance.attempts)
		return MaintenanceTransition{Exhausted: true, State: s.maintenanceSnapshotLocked()}
	}
	now := time.Now().UTC()
	if s.maintenance.nextAttempt != nil && now.Before(*s.maintenance.nextAttempt) {
		return MaintenanceTransition{State: s.maintenanceSnapshotLocked()}
	}
	interval, err := time.ParseDuration(settings.RetryInterval)
	if err != nil || interval < 10*time.Second {
		interval = 30 * time.Second
	}
	s.maintenance.attempts++
	s.maintenance.lastAttempt = &now
	next := now.Add(interval)
	s.maintenance.nextAttempt = &next
	return MaintenanceTransition{Attempted: true, State: s.maintenanceSnapshotLocked()}
}

func (s *Server) MaintenanceSnapshot() *Maintenance {
	s.maintenanceMu.RLock()
	defer s.maintenanceMu.RUnlock()
	return s.maintenanceSnapshotLocked()
}

func (s *Server) maintenanceSnapshotLocked() *Maintenance {
	if s.maintenance == nil {
		return nil
	}
	reason, message := s.maintenance.reason, "Critical storage checks are failing; the server is in read-only maintenance mode."
	if reason == "database_connection" {
		message = "Database connection or write checks are failing; the server is in read-only maintenance mode."
	}
	snapshot := &Maintenance{
		Reason: reason, Message: message, Since: s.maintenance.since.Format(time.RFC3339),
		SelfHealing: SelfHealingInfo{Enabled: !s.maintenance.exhausted, Exhausted: s.maintenance.exhausted, Attempts: s.maintenance.attempts},
	}
	if s.maintenance.lastAttempt != nil {
		value := s.maintenance.lastAttempt.Format(time.RFC3339)
		snapshot.SelfHealing.LastAttempt = &value
	}
	if s.maintenance.nextAttempt != nil {
		value := s.maintenance.nextAttempt.Format(time.RFC3339)
		snapshot.SelfHealing.NextAttempt = &value
	}
	return snapshot
}

// rateBucket tracks one client address during a fixed request window.
type rateBucket struct {
	window time.Time
	count  int
}

const (
	requestsPerMinute       = 120
	globalRequestsPerMinute = 240
	writeRequestsPerMinute  = 10
)

// APIResponse is the standard JSON response format.
type APIResponse struct {
	Success bool        `json:"ok"`
	Data    interface{} `json:"data,omitempty"`
	Count   int         `json:"count,omitempty"`
	Error   string      `json:"error,omitempty"`
	Message string      `json:"message,omitempty"`
	Details interface{} `json:"details,omitempty"`
}

type HealthResponse struct {
	Project        HealthProject  `json:"project"`
	Status         string         `json:"status"`
	PendingRestart bool           `json:"pending_restart,omitempty"`
	RestartReason  []string       `json:"restart_reason,omitempty"`
	Maintenance    *Maintenance   `json:"maintenance,omitempty"`
	Version        string         `json:"version"`
	GoVersion      string         `json:"go_version"`
	Build          HealthBuild    `json:"build"`
	Uptime         string         `json:"uptime"`
	Mode           string         `json:"mode"`
	Timestamp      time.Time      `json:"timestamp"`
	Features       HealthFeatures `json:"features"`
	Checks         HealthChecks   `json:"checks"`
	Stats          HealthStats    `json:"stats"`
}

type HealthProject struct {
	Name        string `json:"name"`
	Tagline     string `json:"tagline"`
	Description string `json:"description"`
}

type HealthBuild struct {
	Commit string `json:"commit"`
	Date   string `json:"date"`
}

type HealthFeatures struct {
	Tor   HealthTorFeature `json:"tor"`
	I2P   HealthI2PFeature `json:"i2p"`
	GeoIP bool             `json:"geoip"`
}

type HealthTorFeature struct {
	Enabled  bool   `json:"enabled"`
	Running  bool   `json:"running"`
	State    string `json:"state"`
	Status   string `json:"status"`
	Hostname string `json:"hostname"`
}

type HealthI2PFeature struct {
	Enabled  bool   `json:"enabled"`
	Running  bool   `json:"running"`
	Status   string `json:"status"`
	State    string `json:"state"`
	Hostname string `json:"hostname"`
	Provider string `json:"provider"`
}

type HealthStats struct {
	RequestsTotal     uint64 `json:"requests_total"`
	Requests24h       uint64 `json:"requests_24h"`
	ActiveConnections uint64 `json:"active_connections"`
}

type Maintenance struct {
	Reason      string          `json:"reason"`
	Message     string          `json:"message"`
	Since       string          `json:"since"`
	SelfHealing SelfHealingInfo `json:"self_healing"`
}

type SelfHealingInfo struct {
	Enabled     bool    `json:"enabled"`
	Exhausted   bool    `json:"exhausted"`
	Attempts    int     `json:"attempts"`
	LastAttempt *string `json:"last_attempt"`
	NextAttempt *string `json:"next_attempt"`
}

type HealthCheck struct {
	Key   string
	Value string
}

type HealthChecks struct {
	Database  string
	Cache     string
	Disk      string
	Scheduler string
	Tor       string
	I2P       string
	Datasets  []HealthCheck
}

func (c HealthChecks) MarshalJSON() ([]byte, error) {
	var result []byte
	result = append(result, '{')
	first := true
	appendCheck := func(key, value string) {
		if value == "" {
			return
		}
		if !first {
			result = append(result, ',')
		}
		first = false
		result = strconv.AppendQuote(result, key)
		result = append(result, ':')
		result = strconv.AppendQuote(result, value)
	}
	appendCheck("database", c.Database)
	appendCheck("cache", c.Cache)
	appendCheck("disk", c.Disk)
	appendCheck("scheduler", c.Scheduler)
	appendCheck("tor", c.Tor)
	appendCheck("i2p", c.I2P)
	for _, check := range c.Datasets {
		appendCheck(check.Key, check.Value)
	}
	return append(result, '}'), nil
}

func (c HealthChecks) Values() map[string]string {
	values := map[string]string{"database": c.Database, "cache": c.Cache, "disk": c.Disk, "scheduler": c.Scheduler}
	if c.Tor != "" {
		values["tor"] = c.Tor
	}
	if c.I2P != "" {
		values["i2p"] = c.I2P
	}
	for _, check := range c.Datasets {
		values[check.Key] = check.Value
	}
	return values
}

func (c HealthChecks) OrderedKeys() []string {
	keys := []string{"database", "cache", "disk", "scheduler"}
	if c.Tor != "" {
		keys = append(keys, "tor")
	}
	if c.I2P != "" {
		keys = append(keys, "i2p")
	}
	for _, check := range c.Datasets {
		keys = append(keys, check.Key)
	}
	return keys
}

// PaginatedResponse includes pagination info
type PaginatedResponse struct {
	Cities     []cities.City  `json:"cities"`
	Pagination PaginationInfo `json:"pagination"`
}

// PaginationInfo contains pagination metadata
type PaginationInfo struct {
	Page       int  `json:"page"`
	Limit      int  `json:"limit"`
	Total      int  `json:"total"`
	TotalPages int  `json:"total_pages"`
	HasNext    bool `json:"has_next"`
	HasPrev    bool `json:"has_prev"`
}

// New creates a new server instance
func New(citySvc *cities.Service, cfg *config.Config, address, port, version, buildDate, commit string) *Server {
	return NewWithZipcodes(citySvc, nil, cfg, address, port, version, buildDate, commit)
}

// NewWithZipcodes creates a server with city and optional US postal datasets.
func NewWithZipcodes(citySvc *cities.Service, zipSvc *zipcodes.Service, cfg *config.Config, address, port, version, buildDate, commit string) *Server {
	return NewWithDatabase(citySvc, zipSvc, cfg, address, port, version, buildDate, commit, nil)
}

// NewWithDatabase creates a server with its persistent application store.
func NewWithDatabase(citySvc *cities.Service, zipSvc *zipcodes.Service, cfg *config.Config, address, port, version, buildDate, commit string, store *database.Store) *Server {
	metrics.Init(cfg.Server.Metrics.DurationBuckets, cfg.Server.Metrics.SizeBuckets)
	metrics.SetBuildInfo(version, commit, buildDate)
	metrics.DefaultLogStore.SetLimits(cfg.Server.Metrics.Loki.MaxEntries, parseMetricsAge(cfg.Server.Metrics.Loki.MaxAge))
	metrics.DefaultLogStore.RegisterSecret(cfg.Server.Token)
	metrics.DefaultLogStore.RegisterSecret(cfg.Server.Security.EncryptionKey)
	metrics.DefaultLogStore.RegisterSecret(cfg.Server.Notifications.Email.SMTP.Password)
	metrics.DefaultLogStore.RegisterSecret(cfg.Server.Pages.Contact.CaptchaSecretKey)
	metrics.DefaultLogStore.RegisterSecret(cfg.Server.Metrics.Auth.Tokens.Prometheus)
	metrics.DefaultLogStore.RegisterSecret(cfg.Server.Metrics.Auth.Tokens.Grafana)
	metrics.DefaultLogStore.RegisterSecret(cfg.Server.Metrics.Auth.Tokens.Loki)
	for _, role := range []string{"admin", "security", "abuse", "general"} {
		roleConfig := contactRoleConfig(cfg, role)
		for _, endpoint := range roleConfig.Webhooks {
			metrics.DefaultLogStore.RegisterSecret(endpoint)
		}
		for _, secret := range roleConfig.WebhookSecrets {
			metrics.DefaultLogStore.RegisterSecret(secret)
		}
	}
	graphQLByLanguage := make(map[string]*graphqlgophers.Schema, len(i18n.LanguageCodes()))
	for _, lang := range i18n.LanguageCodes() {
		graphQLByLanguage[lang] = graphql.NewSchemaForLanguage(lang, citySvc, zipSvc)
	}
	s := &Server{
		router:            chi.NewRouter(),
		citySvc:           citySvc,
		zipSvc:            zipSvc,
		store:             store,
		cfg:               cfg,
		address:           address,
		port:              port,
		version:           version,
		buildDate:         buildDate,
		commit:            commit,
		baseURL:           "/",
		startedAt:         time.Now(),
		graphQL:           graphQLByLanguage["en"],
		graphQLByLanguage: graphQLByLanguage,
		rateBuckets:       make(map[string]rateBucket),
		trustedPrefixes:   parseTrustedProxyPrefixes(cfg.Server.TrustedProxies.Additional),
		metricsCollector:  metrics.NewSystemCollector("", cfg.Server.Metrics.IncludeSystem, cfg.Server.Metrics.IncludeRuntime),
	}
	s.metricsCollector.SetAdditionalCollector(s.collectApplicationMetrics)
	metrics.CitiesTotal.Set(float64(citySvc.Count()))
	if zipSvc != nil {
		metrics.ZipcodesTotal.Set(float64(zipSvc.Count()))
	}
	s.collectApplicationMetrics()
	s.refreshTrustedProxies(true)
	s.setupRoutes()
	return s
}

// Router returns the HTTP router
func (s *Server) Router() http.Handler {
	return panicSafeHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.configMu.RLock()
		defer s.configMu.RUnlock()
		prefix := strings.TrimSuffix(s.baseURL, "/")
		if prefix == "." {
			prefix = ""
		}
		if prefix != "" {
			if r.URL.Path == prefix {
				http.Redirect(w, r, prefix+"/", http.StatusPermanentRedirect)
				return
			}
			if !strings.HasPrefix(r.URL.Path, prefix+"/") {
				http.NotFound(w, r)
				return
			}
			clone := r.Clone(r.Context())
			clone.URL = new(url.URL)
			*clone.URL = *r.URL
			clone.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
			if clone.URL.Path == "" {
				clone.URL.Path = "/"
			}
			r = clone
		}
		s.applyRobotsHeader(w, r)
		s.router.ServeHTTP(w, r)
	}))
}

type panicResponseWriter struct {
	http.ResponseWriter
	status  int
	request *http.Request
}

func (w *panicResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	if strings.HasPrefix(strings.ToLower(w.Header().Get("Content-Type")), "text/html") {
		setHTMLCacheHeaders(w.ResponseWriter, w.request, status)
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *panicResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func panicSafeHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tracked := &panicResponseWriter{ResponseWriter: w, request: r}
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("http.handler_panic", "panic", recovered, "stack", string(debug.Stack()))
				if tracked.status == 0 {
					writeFallbackError(tracked, r, http.StatusInternalServerError)
				}
			}
		}()
		next.ServeHTTP(tracked, r)
	})
}

// SetBaseURL configures a URL path prefix for reverse-proxy deployments.
func (s *Server) SetBaseURL(base string) error {
	clean, err := normalizeBaseURL(base)
	if err != nil {
		return err
	}
	s.configMu.Lock()
	s.baseURL = clean
	s.configMu.Unlock()
	return nil
}

func normalizeBaseURL(base string) (string, error) {
	if base == "" {
		base = "/"
	}
	if !strings.HasPrefix(base, "/") || strings.ContainsAny(base, "?#") {
		return "", fmt.Errorf("base URL must be an absolute path without query or fragment")
	}
	clean := path.Clean(base)
	if clean == "." {
		clean = "/"
	}
	return clean, nil
}

// ApplyConfig atomically applies reloadable settings while retaining settings
// that require a process or listener restart.
func (s *Server) ApplyConfig(next *config.Config, restartRequired []string, baseURL string) error {
	if next == nil {
		return fmt.Errorf("configuration is required")
	}
	cleanBaseURL, err := normalizeBaseURL(baseURL)
	if err != nil {
		return err
	}
	candidate := *next
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if s.cfg != nil {
		for _, setting := range restartRequired {
			switch setting {
			case "server.port":
				candidate.Server.Port = s.cfg.Server.Port
			case "server.address":
				candidate.Server.Address = s.cfg.Server.Address
			case "server.daemonize":
				candidate.Server.Daemonize = s.cfg.Server.Daemonize
			case "server.database":
				candidate.Server.Database = s.cfg.Server.Database
			case "server.tor":
				candidate.Server.Tor = s.cfg.Server.Tor
			case "server.ssl":
				candidate.Server.SSL = s.cfg.Server.SSL
			}
		}
	}
	s.cfg = &candidate
	s.baseURL = cleanBaseURL
	s.router = chi.NewRouter()
	s.trustedPrefixes = parseTrustedProxyPrefixes(candidate.Server.TrustedProxies.Additional)
	s.metricsCollector = metrics.NewSystemCollector(s.dataDir, candidate.Server.Metrics.IncludeSystem, candidate.Server.Metrics.IncludeRuntime)
	s.metricsCollector.SetAdditionalCollector(s.collectApplicationMetrics)
	metrics.DefaultLogStore.SetLimits(candidate.Server.Metrics.Loki.MaxEntries, parseMetricsAge(candidate.Server.Metrics.Loki.MaxAge))
	for _, secret := range []string{
		candidate.Server.Token,
		candidate.Server.Security.EncryptionKey,
		candidate.Server.Notifications.Email.SMTP.Password,
		candidate.Server.Pages.Contact.CaptchaSecretKey,
		candidate.Server.Metrics.Auth.Tokens.Prometheus,
		candidate.Server.Metrics.Auth.Tokens.Grafana,
		candidate.Server.Metrics.Auth.Tokens.Loki,
	} {
		metrics.DefaultLogStore.RegisterSecret(secret)
	}
	for _, role := range []string{"admin", "security", "abuse", "general"} {
		roleConfig := contactRoleConfig(&candidate, role)
		for _, endpoint := range roleConfig.Webhooks {
			metrics.DefaultLogStore.RegisterSecret(endpoint)
		}
		for _, secret := range roleConfig.WebhookSecrets {
			metrics.DefaultLogStore.RegisterSecret(secret)
		}
	}
	s.setupRoutes()
	s.refreshTrustedProxies(true)
	return nil
}

// SetConfigDir sets the resolved configuration directory for disk-backed server features.
func (s *Server) SetConfigDir(configDir string) {
	s.configDir = strings.TrimSpace(configDir)
}

// SetCacheDir sets the private cache directory for derived server assets.
func (s *Server) SetCacheDir(cacheDir string) {
	s.cacheDir = strings.TrimSpace(cacheDir)
}

// SetScheduler attaches the application's task scheduler for diagnostics.
func (s *Server) SetScheduler(instance *scheduler.Scheduler) {
	s.scheduler = instance
}

// SetConfigWatcher attaches the reload state used in public health output.
func (s *Server) SetConfigWatcher(watcher *config.ConfigWatcher) {
	s.configWatcher = watcher
}

// SetDataDir configures the disk path sampled by the system metrics collector.
func (s *Server) SetDataDir(dataDir string) {
	s.dataDir = strings.TrimSpace(dataDir)
	s.metricsCollector = metrics.NewSystemCollector(s.dataDir, s.cfg.Server.Metrics.IncludeSystem, s.cfg.Server.Metrics.IncludeRuntime)
	s.metricsCollector.SetAdditionalCollector(s.collectApplicationMetrics)
}

func (s *Server) collectApplicationMetrics() {
	if s.store == nil {
		return
	}
	open, inUse := s.store.PoolStats()
	metrics.DBConnectionsOpen.Set(float64(open))
	metrics.DBConnectionsInUse.Set(float64(inUse))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.store.Read(ctx, func(ctx context.Context, reader database.Reader) error {
		var primary, linked int
		if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE kind = 'primary' AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, time.Now().Unix()).Scan(&primary); err != nil {
			return err
		}
		if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE kind = 'linked' AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, time.Now().Unix()).Scan(&linked); err != nil {
			return err
		}
		metrics.OwnerTokensActive.WithLabelValues("primary").Set(float64(primary))
		metrics.OwnerTokensActive.WithLabelValues("linked").Set(float64(linked))
		return nil
	})
}

// Run starts the HTTP server with graceful shutdown support
func (s *Server) Run(ctx context.Context, addr string) error {
	return s.RunListeners(ctx, addr, nil)
}

// RunListener serves with a listener bound during privileged startup when one is provided.
func (s *Server) RunListener(ctx context.Context, addr string, listener net.Listener) error {
	var listeners []net.Listener
	if listener != nil {
		listeners = append(listeners, listener)
	}
	return s.RunListeners(ctx, addr, listeners)
}

// RunListeners serves on the configured address and any sockets bound before privilege drop.
func (s *Server) RunListeners(ctx context.Context, addr string, listeners []net.Listener) error {
	if s.cfg.Server.Metrics.Enabled {
		s.metricsCollector.Start()
		defer s.metricsCollector.Stop()
	}
	var tlsConfig *tls.Config
	var certManager *sslmanager.Manager
	if tlsConfigured(s.cfg.Server.Port, s.cfg.Server.SSL.Enabled) {
		sslConfig := s.cfg.Server.SSL
		certPath, keyPath := sslConfig.Cert, sslConfig.Key
		if certPath != "" && !filepath.IsAbs(certPath) {
			certPath = filepath.Join(s.configDir, "ssl", certPath)
		}
		if keyPath != "" && !filepath.IsAbs(keyPath) {
			keyPath = filepath.Join(s.configDir, "ssl", keyPath)
		}
		certManager = sslmanager.NewManager(sslmanager.Config{
			Enabled: true, CertPath: s.configDir, Cert: certPath, Key: keyPath, MinVersion: sslConfig.MinVersion,
			Staging: sslConfig.LetsEncrypt.Staging,
			LetsEncrypt: sslmanager.LetsEncryptConfig{
				Enabled: sslConfig.LetsEncrypt.Enabled, Email: strings.ReplaceAll(sslConfig.LetsEncrypt.Email, "{fqdn}", s.cfg.Server.FQDN),
				Challenge: sslConfig.LetsEncrypt.Challenge, Staging: sslConfig.LetsEncrypt.Staging,
				DNSProviderType: sslConfig.LetsEncrypt.DNSProvider, DNSCredentials: sslConfig.LetsEncrypt.DNSCredentials,
			},
		})
		var err error
		domains := []string{s.cfg.Server.FQDN}
		for _, domain := range GetAllDomains() {
			domains = appendUniqueDomain(domains, domain)
		}
		if wildcard := GetWildcardDomain(); wildcard != "" && sslmanager.ParseChallenge(sslConfig.LetsEncrypt.Challenge) == "dns-01" {
			domains = append(domains, wildcard)
		}
		tlsConfig, err = certManager.GetTLSConfig(domains)
		if err != nil {
			return fmt.Errorf("configure TLS: %w", err)
		}
	}
	serverHandler := s.Router()
	if certManager != nil {
		serverHandler = certManager.GetHTTPHandler(serverHandler)
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           serverHandler,
		TLSConfig:         tlsConfig,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      65 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	errChan := make(chan error, 1)
	go func() {
		if len(listeners) == 0 {
			var err error
			if tlsConfig != nil {
				var listener net.Listener
				listener, err = net.Listen("tcp", addr)
				if err == nil {
					err = srv.Serve(tls.NewListener(listener, tlsConfig))
				}
			} else {
				err = srv.ListenAndServe()
			}
			if err != nil && err != http.ErrServerClosed {
				errChan <- err
			}
			return
		}
		for _, listener := range listeners {
			listener := listener
			go func() {
				serveListener := listener
				if tlsConfig != nil && shouldServeTLS(listener, s.cfg.Server.Port, s.cfg.Server.SSL.Enabled) {
					serveListener = tls.NewListener(listener, tlsConfig)
				}
				if err := srv.Serve(serveListener); err != nil && err != http.ErrServerClosed {
					select {
					case errChan <- err:
					default:
					}
				}
			}()
		}
		<-ctx.Done()
	}()

	select {
	case <-ctx.Done():
		s.shuttingDown.Store(true)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errChan:
		return err
	}
}

func shouldServeTLS(listener net.Listener, configuredPorts string, sslEnabled bool) bool {
	if sslEnabled {
		return listener.Addr().Network() == "tcp" && listener.Addr().String() != ""
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return false
	}
	ports := strings.Split(configuredPorts, ",")
	if len(ports) > 1 {
		second, err := strconv.Atoi(strings.TrimSpace(ports[1]))
		return err == nil && address.Port == second
	}
	return address.Port == 443
}

func tlsConfigured(configuredPorts string, sslEnabled bool) bool {
	if sslEnabled {
		return true
	}
	ports := strings.Split(configuredPorts, ",")
	for index, value := range ports {
		port, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil && (port == 443 || (index == 1 && len(ports) == 2)) {
			return true
		}
	}
	return false
}

func (s *Server) setupRoutes() {
	r := s.router
	r.Use(s.healthStatsMiddleware)
	if !validAPIVersion(s.cfg.Server.APIVersion) {
		s.cfg.Server.APIVersion = "v1"
	}
	apiPrefix := "/api/" + s.cfg.Server.APIVersion
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Method == http.MethodGet && req.URL.Path == apiPrefix+"/" {
				target := apiPrefix
				if req.URL.RawQuery != "" {
					target += "?" + req.URL.RawQuery
				}
				http.Redirect(w, req, target, http.StatusMovedPermanently)
				return
			}
			next.ServeHTTP(w, req)
		})
	})

	// Middleware

	r.Use(s.realIPMiddleware)
	r.Use(s.gpcMiddleware)
	r.Use(s.metricsMiddleware)
	r.Use(s.accessLogMiddleware)
	r.Use(s.contentNegotiationMiddleware)
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(s.rateLimitMiddleware)
	r.Use(s.corsMiddleware)
	r.Use(s.securityHeadersMiddleware)
	r.Use(s.languageQueryCookieMiddleware)
	r.Use(i18n.MiddlewareWithSettings(i18n.Settings{
		Enabled:          true,
		DefaultLanguage:  s.cfg.Server.I18n.DefaultLanguage,
		FallbackLanguage: "en",
		CookieName:       "lang",
		CookieMaxAge:     31536000,
		Available:        s.cfg.Server.I18n.Available,
	}))
	r.Use(s.maintenanceMiddleware)
	r.Use(s.csrfMiddleware)
	r.Use(s.debugAuthMiddleware)
	r.Use(s.debugMiddleware)
	s.registerDebugRoutes(r)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		s.renderError(w, r, http.StatusNotFound, "This page could not be found.")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/server/tor/") {
			s.respondJSON(w, http.StatusNotFound, APIResponse{Success: false, Error: "NOT_FOUND", Message: "This page could not be found."})
			return
		}
		s.renderError(w, r, http.StatusMethodNotAllowed, "This request method is not supported.")
	})

	// Static files
	staticContent, _ := fs.Sub(staticFS, "static")
	staticFiles := http.StripPrefix("/static/", http.FileServer(http.FS(staticContent)))
	r.Get("/static/css/accent.css", s.handleAccentCSS)
	r.Head("/static/css/accent.css", s.handleAccentCSS)
	r.Get("/static/css/common.css", s.handleCommonCSS)
	r.Head("/static/css/common.css", s.handleCommonCSS)
	staticHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, err := staticContent.Open(strings.TrimPrefix(r.URL.Path, "/static/"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		info, statErr := file.Stat()
		_ = file.Close()
		if statErr != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		if setAssetCacheHeaders(w, r) {
			return
		}
		staticFiles.ServeHTTP(w, r)
	})
	r.Get("/static/*", staticHandler)
	r.Head("/static/*", staticHandler)
	r.Get("/locales/{lang}.json", s.handleLocale)
	r.Head("/locales/{lang}.json", s.handleLocale)

	// Health check endpoints
	r.Get("/api/autodiscover", s.handleAutoDiscover)
	if s.cfg.Server.Healthz.Root.Enabled {
		r.Get("/healthz", s.handleHealthz)
	}
	r.Get(apiPrefix+"/server/healthz", s.handleHealthz)
	r.Get("/api/healthz", s.handleHealthz)

	// Web routes
	r.Get("/", s.handleHome)
	r.Get("/search", s.handleSearchPage)
	r.Get("/coordinates", s.handleCoordinatesPage)
	r.Get("/server/about", s.handleAboutPage)
	r.Get("/server/help", s.handleHelpPage)
	r.Get("/server/help/mcp", s.handleMCPHelp)
	r.Get("/server/contact", s.handleContactPage)
	r.Post("/server/contact", s.handleContactPost)
	r.Get("/server/security", s.handleSecurityPage)
	r.Get("/server/security/policy", s.handleSecurityPolicy)
	r.Get("/server/security/thanks", s.handleSecurityThanks)
	r.Get("/server/security/report/{trackingID}", s.handleSecurityReportStatus)
	r.Get("/server/privacy", s.handlePrivacyPage)
	r.Get("/server/tracking.js", s.handleTrackingScript)
	r.Get("/server/terms", s.handleTermsPage)
	r.Get("/server/preferences", s.handlePreferencesPage)
	r.Head("/server/preferences", s.handlePreferencesPage)
	r.Get("/server/preferences/tokens", s.handleOwnerTokensPage)
	r.Get("/server/preferences/sync", s.handlePreferencesSyncPage)
	r.Get("/server/preferences/sync/import", s.handlePreferencesSyncImport)
	r.Get("/server/dpo", s.handleDPOPage)
	r.Post("/announcements/dismiss", s.handleAnnouncementDismiss)
	r.Post("/server/preferences/token/create", s.handleOwnerTokenIssueWeb)
	r.Post("/server/preferences/token/restore", s.handleOwnerTokenRestore)
	r.Post("/server/preferences/token/forget", s.handleOwnerTokenForget)
	r.Post("/server/preferences/token/reveal", s.handleOwnerTokenPrimaryAction)
	r.Post("/server/preferences/token/rotate", s.handleOwnerTokenPrimaryAction)
	r.Post("/server/preferences/token/revoke", s.handleOwnerTokenRevokePrimary)
	r.Post("/server/preferences/tokens", s.handleOwnerTokenLinkedCreateWeb)
	r.Post("/server/preferences/tokens/{id}/reveal", s.handleOwnerTokenLinkedAction)
	r.Post("/server/preferences/tokens/{id}/rotate", s.handleOwnerTokenLinkedAction)
	r.Post("/server/preferences/tokens/{id}/update", s.handleOwnerTokenLinkedAction)
	r.Post("/server/preferences/tokens/{id}/revoke", s.handleOwnerTokenLinkedAction)
	r.Get(apiPrefix+"/server/about", s.apiPage(s.handleAboutPage))
	r.Get(apiPrefix+"/server/help", s.apiPage(s.handleHelpPage))
	r.Get(apiPrefix+"/server/help/mcp", s.apiPage(s.handleMCPHelp))
	r.Get(apiPrefix+"/server/contact", s.handleContactAPI)
	r.Post(apiPrefix+"/server/contact", s.handleContactAPI)
	r.Get(apiPrefix+"/server/security", s.apiPage(s.handleSecurityPage))
	r.Get(apiPrefix+"/server/security/policy", s.apiPage(s.handleSecurityPolicy))
	r.Get(apiPrefix+"/server/security/thanks", s.apiPage(s.handleSecurityThanks))
	r.Get(apiPrefix+"/server/privacy", s.apiPage(s.handlePrivacyPage))
	r.Get(apiPrefix+"/server/terms", s.apiPage(s.handleTermsPage))
	r.Get(apiPrefix+"/server/preferences", s.handlePreferencesAPI)
	r.Post(apiPrefix+"/server/preferences", s.handlePreferencesAPI)
	r.Get(apiPrefix+"/server/preferences/tokens", s.handleOwnerTokensAPI)
	r.Post(apiPrefix+"/server/preferences/tokens", s.handleOwnerTokensAPI)
	r.Post(apiPrefix+"/server/preferences/tokens/primary", s.handleOwnerTokenIssueAPI)
	r.Post(apiPrefix+"/server/preferences/tokens/primary/reveal", s.handleOwnerTokenPrimaryAction)
	r.Post(apiPrefix+"/server/preferences/tokens/primary/rotate", s.handleOwnerTokenPrimaryAction)
	r.Delete(apiPrefix+"/server/preferences/tokens/primary", s.handleOwnerTokenRevokePrimary)
	r.Post(apiPrefix+"/server/preferences/tokens/{id}/reveal", s.handleOwnerTokenLinkedAction)
	r.Post(apiPrefix+"/server/preferences/tokens/{id}/rotate", s.handleOwnerTokenLinkedAction)
	r.Patch(apiPrefix+"/server/preferences/tokens/{id}", s.handleOwnerTokenLinkedAction)
	r.Delete(apiPrefix+"/server/preferences/tokens/{id}", s.handleOwnerTokenLinkedAction)
	r.Get(apiPrefix+"/server/dpo", s.apiPage(s.handleDPOPage))
	r.Get(apiPrefix+"/server/preferences/sync", s.handlePreferencesSyncAPI)
	r.Get(apiPrefix+"/server/preferences/sync/import", s.handlePreferencesImportAPI)
	r.Post("/server/preferences", s.handlePreferencesUpdate)
	r.Post("/server/consent", s.handleConsentPost)
	r.Post("/server/ccpa", s.handleCCPAPost)
	r.Post("/server/preferences/sync", s.handlePreferencesSyncPost)
	r.Get("/server/healthz", s.handleHealthz)
	s.registerTorControlRoutes(r)
	s.registerMetricsRoutes()

	// PWA routes
	r.Get("/manifest.json", s.handleManifest)
	r.Get("/favicon.ico", s.handleFavicon)
	r.Head("/favicon.ico", s.handleFavicon)
	r.Get("/branding/{name}", s.handleBrandingImage)
	r.Head("/branding/{name}", s.handleBrandingImage)
	r.Get("/sw.js", s.handleServiceWorker)
	r.Get("/robots.txt", s.handleRobots)
	r.Get("/sitemap.xml", s.handleSitemap)
	r.Head("/sitemap.xml", s.handleSitemap)
	r.Get("/feed.xml", s.handleAtomFeed)
	r.Head("/feed.xml", s.handleAtomFeed)
	r.Get("/rss.xml", s.handleRSSFeed)
	r.Head("/rss.xml", s.handleRSSFeed)
	r.Get("/.well-known/security.txt", s.handleSecurityTxt)
	r.Head("/.well-known/security.txt", s.handleSecurityTxt)
	r.Get("/.well-known/pgp-key.asc", s.handleProjectPGPKey)
	r.Head("/.well-known/pgp-key.asc", s.handleProjectPGPKey)
	r.Get("/.well-known/llms.txt", s.handleLLMSTxt)
	r.Head("/.well-known/llms.txt", s.handleLLMSTxt)
	r.Get("/llms.txt", s.handleLLMSTxt)
	r.Head("/llms.txt", s.handleLLMSTxt)

	// OpenAPI/Swagger
	r.Get("/server/docs/swagger", s.handleOpenAPI)
	r.Get(apiPrefix+"/server/swagger", s.handleOpenAPISpec)
	r.Get("/api/swagger", s.handleOpenAPISpec)
	r.Get("/server/docs/graphql", s.handleGraphQLDocs)
	r.Get(apiPrefix+"/server/graphql", s.handleGraphQL)
	r.Post(apiPrefix+"/server/graphql", s.handleGraphQL)
	r.Get("/api/graphql", s.handleGraphQL)
	r.Post("/api/graphql", s.handleGraphQL)
	r.Post(apiPrefix+"/server/mcp", s.handleMCP)
	r.Post("/api/mcp", s.handleMCP)
	r.Post("/mcp", s.handleMCP)

	// API routes
	r.Get(apiPrefix+"/server/auth/check", s.handleAuthCheck)

	// Cities
	r.Get(apiPrefix+"/cities", s.handleGetCities)
	r.Get(apiPrefix+"/cities/search", s.handleSearchCities)
	r.Get(apiPrefix+"/cities/{id}", s.handleGetCityByID)
	r.Get(apiPrefix+"/cities/country/{code}", s.handleGetCitiesByCountry)
	r.Get(apiPrefix+"/cities/coordinates", s.handleFindNearestGET)
	r.Post(apiPrefix+"/cities/coordinates", s.handleFindNearestPOST)
	r.Get(apiPrefix+"/cities/nearby", s.handleFindNearby)

	// US postal codes
	r.Get(apiPrefix+"/zipcodes/{zip}", s.handleGetZipcode)
	r.Get(apiPrefix+"/zipcodes/search", s.handleSearchZipcodes)
	r.Get(apiPrefix+"/zipcodes/state/{state}", s.handleGetZipcodesByState)
	r.Get(apiPrefix+"/zipcodes/nearby", s.handleNearbyZipcodes)

	// Stats
	r.Get(apiPrefix+"/stats", s.handleStats)
	r.Get(apiPrefix+"/stats.txt", s.handleStatsTxt)
	r.Get(apiPrefix+"/count", s.handleCount)
	r.Get(apiPrefix+"/count.txt", s.handleCountTxt)
	r.Get(apiPrefix, s.handleAPIInfo)

	// Versioned convenience endpoints
	r.Get(apiPrefix+"/random", s.handleRandomCity)
	r.Get(apiPrefix+"/random.txt", s.handleRandomCityTxt)
}

func (s *Server) accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(wrapped, r)
		status := wrapped.Status()
		if r.Method == http.MethodGet && status >= http.StatusOK && status < http.StatusMultipleChoices && s.successfulHealthRequest(r.URL.Path) {
			return
		}
		writeAccessLog(r, status, wrapped.BytesWritten(), time.Since(started))
		if s.successfulHealthRequest(r.URL.Path) && status >= http.StatusMultipleChoices {
			slog.Error("http.request", "method", r.Method, "path", r.URL.EscapedPath(), "status", status, "bytes", wrapped.BytesWritten(), "duration", time.Since(started))
			return
		}
		slog.Info("http.request", "method", r.Method, "path", r.URL.EscapedPath(), "status", status, "bytes", wrapped.BytesWritten(), "duration", time.Since(started))
	})
}

func (s *Server) healthStatsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requestActive.Add(1)
		defer s.requestActive.Add(^uint64(0))
		if !healthStatsExcludedPath(r.URL.Path) {
			now := time.Now()
			s.requestTotal.Add(1)
			s.recordRequestHour(now)
		}
		next.ServeHTTP(w, r)
	})
}

func healthStatsExcludedPath(requestPath string) bool {
	return requestPath == "/server/healthz" || requestPath == "/healthz" ||
		requestPath == "/api/healthz" || strings.HasSuffix(requestPath, "/server/healthz") ||
		requestPath == "/server/metrics" || strings.HasPrefix(requestPath, "/server/metrics/") ||
		strings.HasSuffix(requestPath, "/server/metrics") || strings.Contains(requestPath, "/server/metrics/")
}

func (s *Server) recordRequestHour(now time.Time) {
	hour := now.Unix() / 3600
	index := int(hour % int64(len(s.requestHours)))
	s.requestHoursMu.Lock()
	if s.requestHours[index].hour != hour {
		s.requestHours[index] = requestHour{hour: hour}
	}
	s.requestHours[index].count++
	s.requestHoursMu.Unlock()
}

func (s *Server) requestsLast24Hours(now time.Time) uint64 {
	currentHour := now.Unix() / 3600
	var count uint64
	s.requestHoursMu.Lock()
	defer s.requestHoursMu.Unlock()
	for _, bucket := range s.requestHours {
		if bucket.hour >= currentHour-23 && bucket.hour <= currentHour {
			count += bucket.count
		}
	}
	return count
}

func (s *Server) successfulHealthRequest(path string) bool {
	return path == "/server/healthz" || path == "/api/healthz" || path == "/healthz" || path == "/api/"+s.cfg.Server.APIVersion+"/server/healthz"
}

func (s *Server) languageQueryCookieMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		preferencesAllowed := canSetPreferenceCookie(r)
		if requested := r.URL.Query().Get("lang"); requested != "" && preferencesAllowed {
			if language, ok := normalizePreferenceLanguage(requested, s.cfg.Server.I18n.Available); ok {
				http.SetCookie(w, &http.Cookie{
					Name: "lang", Value: language, Path: "/", MaxAge: 31536000,
					Secure: s.requestUsesHTTPS(r), SameSite: http.SameSiteLaxMode,
				})
			}
		}
		if !preferencesAllowed {
			r = withoutPreferenceCookies(r)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) apiPage(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") == "" || r.Header.Get("Accept") == "*/*" {
			request := r.Clone(r.Context())
			request.Header = r.Header.Clone()
			request.Header.Set("Accept", "application/json")
			r = request
		}
		handler(w, r)
	}
}

func validAPIVersion(version string) bool {
	if !strings.HasPrefix(version, "v") || len(version) < 2 {
		return false
	}
	number, err := strconv.Atoi(version[1:])
	return err == nil && number > 0
}

// Middleware

func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if host, _, err := net.SplitHostPort(ip); err == nil {
			ip = host
		}
		now := time.Now()
		class, classRule := requestRateRule(r, s.cfg.Server.RateLimit)
		globalLimit := s.cfg.Server.RateLimit.GlobalBurst
		if globalLimit <= 0 {
			globalLimit = globalRequestsPerMinute
		}
		s.rateMu.Lock()
		globalKey := ip + "|global"
		classKey := ip + "|" + class
		globalBucket := currentRateBucket(s.rateBuckets[globalKey], now, time.Minute)
		classBucket := currentRateBucket(s.rateBuckets[classKey], now, time.Duration(classRule.Window)*time.Second)
		if len(s.rateBuckets) >= 200000 && s.rateBuckets[globalKey].window.IsZero() {
			for key, candidate := range s.rateBuckets {
				if now.Sub(candidate.window) >= time.Minute {
					delete(s.rateBuckets, key)
				}
			}
			if len(s.rateBuckets) >= 200000 {
				s.rateMu.Unlock()
				metrics.RateLimitBlocked.WithLabelValues("global").Inc()
				w.Header().Set("Retry-After", "60")
				s.respondError(w, http.StatusTooManyRequests, "RATE_LIMITED", i18n.Translate(i18n.LangFromRequest(r), "api_errors.tracking_capacity"))
				return
			}
		}
		if globalBucket.count >= globalLimit {
			s.rateMu.Unlock()
			metrics.RateLimitBlocked.WithLabelValues("global").Inc()
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(time.Until(globalBucket.window.Add(time.Minute)).Seconds()))))
			s.respondError(w, http.StatusTooManyRequests, "RATE_LIMITED", i18n.Translate(i18n.LangFromRequest(r), "api_errors.rate_limited"))
			return
		}
		if classBucket.count >= classRule.Requests {
			s.rateMu.Unlock()
			metrics.RateLimitBlocked.WithLabelValues(class).Inc()
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(time.Until(classBucket.window.Add(time.Duration(classRule.Window)*time.Second)).Seconds()))))
			s.respondError(w, http.StatusTooManyRequests, "RATE_LIMITED", i18n.Translate(i18n.LangFromRequest(r), "api_errors.rate_limited"))
			return
		}
		globalBucket.count++
		classBucket.count++
		s.rateBuckets[globalKey] = globalBucket
		s.rateBuckets[classKey] = classBucket
		s.rateMu.Unlock()
		metrics.RateLimitRequests.WithLabelValues(class, "allowed").Inc()
		next.ServeHTTP(w, r)
	})
}

func requestRateRule(r *http.Request, limits config.RateLimitConfig) (string, config.RateLimitRule) {
	if r.URL.Path == "/healthz" || strings.HasSuffix(r.URL.Path, "/server/healthz") {
		return "health", configuredRateRule(limits.Health, config.RateLimitRule{Requests: requestsPerMinute, Window: 60})
	}
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return "read", configuredRateRule(limits.Read, config.RateLimitRule{Requests: requestsPerMinute, Window: 60})
	}
	return "write", configuredRateRule(limits.Write, config.RateLimitRule{Requests: writeRequestsPerMinute, Window: 60})
}

func configuredRateRule(rule, fallback config.RateLimitRule) config.RateLimitRule {
	if rule.Requests <= 0 {
		rule.Requests = fallback.Requests
	}
	if rule.Window <= 0 {
		rule.Window = fallback.Window
	}
	return rule
}

func currentRateBucket(bucket rateBucket, now time.Time, window time.Duration) rateBucket {
	if bucket.window.IsZero() || now.Sub(bucket.window) >= window {
		return rateBucket{window: now}
	}
	return bucket
}

// realIPMiddleware rewrites r.RemoteAddr with the client IP resolved from
// X-Forwarded-For/X-Real-IP, but only when the immediate TCP peer is a
// trusted (private/loopback) address. Unlike chi's deprecated
// middleware.RealIP, forwarded headers from an untrusted peer are ignored,
// preventing IP spoofing (AI.md PART 12 "Trusted Proxies").
// clientIPFromHeaders extracts the client IP from trusted proxy headers.
func clientIPFromHeaders(r *http.Request) string {
	peer, ok := normalizeAddress(r.RemoteAddr)
	if !ok {
		return ""
	}
	resolved, ok := resolveForwardedAddress(r, peer, nil, "auto")
	if !ok {
		return ""
	}
	return resolved.String()
}

// isTrustedPeer reports whether addr (host:port or bare host) is a
// loopback or private-range address.
func isTrustedPeer(addr string) bool {
	address, ok := normalizeAddress(addr)
	return ok && trustedAddress(address, nil)
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		origins, wildcard, disabled := s.corsAllowedOrigins(r)
		originMatched := false
		if parsed, ok := parseCORSOrigin(origin); ok {
			origin = parsed
			if wildcard {
				originMatched = true
			} else {
				for _, allowed := range origins {
					if strings.EqualFold(origin, allowed) {
						originMatched = true
						break
					}
				}
			}
		}
		if !disabled && originMatched {
			if wildcard {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
				if s.cfg.Server.CORS.AllowCredentials {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
			}
		}
		if r.Method == http.MethodOptions {
			if !disabled && originMatched {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, X-Requested-With, Authorization, X-CSRF-Token, X-XSRF-Token, MCP-Protocol-Version, Mcp-Method, Mcp-Name")
				maxAge := s.cfg.Server.CORS.MaxAge
				if maxAge <= 0 {
					maxAge = 86400
				}
				w.Header().Set("Access-Control-Max-Age", strconv.Itoa(maxAge))
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) corsAllowedOrigins(r *http.Request) ([]string, bool, bool) {
	configured := s.cfg.Server.CORS.AllowedOrigins
	for _, origin := range configured {
		if strings.TrimSpace(origin) == "" {
			return nil, false, true
		}
	}
	origins := make([]string, 0, len(configured)+len(configuredDomains())+1)
	wildcard := false
	for _, origin := range configured {
		origin = strings.TrimSpace(origin)
		if origin == "*" {
			wildcard = true
			continue
		}
		if parsed, ok := parseCORSOrigin(origin); ok {
			origins = append(origins, parsed)
		}
	}
	if wildcard {
		return origins, true, false
	}
	for _, domain := range configuredDomains() {
		if parsed, ok := parseCORSOrigin("https://" + domain); ok {
			origins = append(origins, parsed)
		}
	}
	if learned := s.proxyLearnedOrigin(r); learned != "" {
		origins = append(origins, learned)
	}
	if len(origins) == 0 {
		return nil, true, false
	}
	return origins, false, false
}

func (s *Server) proxyLearnedOrigin(r *http.Request) string {
	peer, ok := normalizeAddress(originalPeer(r))
	if !ok || !trustedAddress(peer, s.trustedProxySnapshot()) {
		return ""
	}
	forwardedHost := false
	for _, header := range []string{"X-Forwarded-Host", "X-Real-Host", "X-Original-Host"} {
		if strings.TrimSpace(firstHeaderValue(r.Header.Values(header))) != "" {
			forwardedHost = true
			break
		}
	}
	if !forwardedHost {
		return ""
	}
	resolved, err := url.Parse(s.BuildURL(r, "/"))
	if err != nil {
		return ""
	}
	origin, ok := parseCORSOrigin(resolved.Scheme + "://" + resolved.Host)
	if !ok {
		return ""
	}
	return origin
}

func parseCORSOrigin(origin string) (string, bool) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || !validHostPort(parsed.Host) || !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return "", false
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), true
}

func (s *Server) maintenanceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions || s.maintenanceReadOnlyPost(r) || !s.MaintenanceActive() {
			next.ServeHTTP(w, r)
			return
		}
		maintenance := s.MaintenanceSnapshot()
		if maintenance == nil {
			maintenance = s.RefreshMaintenance().State
		}
		if maintenance == nil {
			next.ServeHTTP(w, r)
			return
		}
		retryAfter := 30
		if interval, err := time.ParseDuration(s.cfg.Server.Maintenance.SelfHealing.RetryInterval); err == nil && interval > 0 {
			retryAfter = max(1, int(interval.Seconds()))
		}
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		w.Header().Set("X-Maintenance-Mode", "true")
		w.Header().Set("X-Maintenance-Reason", maintenance.Reason)
		message := "Server is in read-only maintenance mode."
		if translated := i18n.Translate(w.Header().Get("Content-Language"), "errors.unavailable"); translated != "errors.unavailable" {
			message = translated
		}
		if tracked, ok := w.(*panicResponseWriter); ok && tracked.request != nil && !strings.HasPrefix(tracked.request.URL.Path, "/api/") && strings.Contains(strings.ToLower(tracked.request.Header.Get("Accept")), "text/html") {
			s.renderError(w, tracked.request, http.StatusServiceUnavailable, message)
			return
		}
		s.respondJSON(w, http.StatusServiceUnavailable, APIResponse{
			Success: false,
			Error:   "MAINTENANCE",
			Message: message,
			Details: map[string]any{"reason": maintenance.Reason, "self_healing": maintenance.SelfHealing.Enabled},
		})
	})
}

func (s *Server) maintenanceReadOnlyPost(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	requestPath := r.URL.Path
	baseURL := strings.TrimSuffix(s.cfg.Server.BaseURL, "/")
	if baseURL != "" && baseURL != "/" {
		requestPath = strings.TrimPrefix(requestPath, baseURL)
	}
	apiPrefix := "/api/" + strings.Trim(s.cfg.Server.APIVersion, "/")
	switch requestPath {
	case apiPrefix + "/cities/coordinates", apiPrefix + "/server/graphql", "/api/graphql", apiPrefix + "/server/mcp", "/api/mcp", "/mcp":
		return true
	default:
		return false
	}
}

func (s *Server) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Permitted-Cross-Domain-Policies", "none")
		w.Header().Set("Origin-Agent-Cluster", "?1")
		w.Header().Set("Cross-Origin-Opener-Policy", "unsafe-none")
		w.Header().Set("Cross-Origin-Embedder-Policy", "unsafe-none")
		w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
		w.Header().Set("Permissions-Policy", "accelerometer=(), camera=(), display-capture=(), geolocation=(), gyroscope=(), hid=(), idle-detection=(), magnetometer=(), microphone=(), midi=(), screen-wake-lock=(), serial=(), usb=(), xr-spatial-tracking=(), attribution-reporting=(), browsing-topics=(), autoplay=(self), encrypted-media=(self), fullscreen=(self), payment=(self), picture-in-picture=(self), publickey-credentials-get=(self), storage-access=(self)")
		if s.requestUsesHTTPS(r) {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		}
		if !s.contactRequestOnPrivateNetwork(r) {
			w.Header().Set("Content-Security-Policy", s.contentSecurityPolicy(r))
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) contentSecurityPolicy(r *http.Request) string {
	policy := []string{"default-src 'self'", "script-src 'self'", "style-src 'self' 'unsafe-inline'", "img-src 'self' data: blob: https:", "font-src 'self' https:", "connect-src 'self'", "media-src 'self' blob:", "worker-src 'self' blob:", "manifest-src 'self'", "frame-src 'self'", "frame-ancestors 'self'", "base-uri 'self'", "form-action 'self'", "object-src 'none'"}
	connectSources := make([]string, 0)
	trackingScripts, trackingConnects, trackingImages := s.trackingCSPHosts()
	connectSources = append(connectSources, trackingConnects...)
	for _, origin := range s.cfg.Server.CORS.AllowedOrigins {
		if parsed, ok := parseCORSOrigin(origin); ok {
			connectSources = append(connectSources, parsed)
		}
	}
	for _, domain := range configuredDomains() {
		if parsed, ok := parseCORSOrigin("https://" + domain); ok {
			connectSources = append(connectSources, parsed)
		}
	}
	if learned := s.proxyLearnedOrigin(r); learned != "" {
		connectSources = append(connectSources, learned)
	}
	for index := range policy {
		if strings.HasPrefix(policy[index], "script-src ") && len(trackingScripts) > 0 {
			policy[index] += " " + strings.Join(trackingScripts, " ")
		}
		if strings.HasPrefix(policy[index], "img-src ") && len(trackingImages) > 0 {
			policy[index] += " " + strings.Join(trackingImages, " ")
		}
		if strings.HasPrefix(policy[index], "connect-src ") && len(connectSources) > 0 {
			policy[index] += " " + strings.Join(connectSources, " ")
		}
	}
	provider := strings.ToLower(s.cfg.Server.Pages.Contact.Captcha)
	switch provider {
	case "recaptcha":
		policy[1] += " https://www.google.com https://www.gstatic.com"
		policy[9] += " https://www.google.com"
	case "hcaptcha":
		policy[1] += " https://js.hcaptcha.com https://*.hcaptcha.com"
		policy[9] += " https://*.hcaptcha.com"
	}
	if s.requestUsesHTTPS(r) {
		policy = append(policy, "upgrade-insecure-requests")
	}
	policy = append(policy, "report-to default", "report-uri "+s.baseURL+"/api/"+s.cfg.Server.APIVersion+"/server/reports/csp")
	return strings.Join(policy, "; ")
}

// Response helpers

func (s *Server) respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(data)
}

func (s *Server) respondSuccess(w http.ResponseWriter, data interface{}, count int) {
	s.respondJSON(w, http.StatusOK, APIResponse{
		Success: true,
		Data:    data,
		Count:   count,
	})
}

func (s *Server) respondError(w http.ResponseWriter, status int, code, message string) {
	if strings.TrimSpace(message) == "" {
		if key := errorTranslationKey(status); key != "" {
			message = i18n.Translate(w.Header().Get("Content-Language"), key)
		}
	}
	if tracked, ok := w.(*panicResponseWriter); ok && tracked.request != nil && !strings.HasPrefix(tracked.request.URL.Path, "/api/") && strings.Contains(strings.ToLower(tracked.request.Header.Get("Accept")), "text/html") {
		s.renderError(w, tracked.request, status, message)
		return
	}
	s.respondJSON(w, status, APIResponse{
		Success: false,
		Error:   canonicalAPIErrorCode(status, code),
		Message: message,
	})
}

func canonicalAPIErrorCode(status int, code string) string {
	switch code {
	case "BAD_REQUEST", "VALIDATION_FAILED", "UNAUTHORIZED", "TOKEN_EXPIRED", "TOKEN_INVALID",
		"FORBIDDEN", "CSRF_FAILED", "ACCOUNT_LOCKED", "NOT_FOUND", "METHOD_NOT_ALLOWED",
		"CONFLICT", "RATE_LIMITED", "SERVER_ERROR", "MAINTENANCE":
		return code
	}
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		if code == "INVALID_JSON" || code == "INVALID_GRAPHQL_REQUEST" || code == "MISSING_QUERY" || code == "REQUEST_TOO_LARGE" {
			return "BAD_REQUEST"
		}
		return "VALIDATION_FAILED"
	case http.StatusUnauthorized:
		return "UNAUTHORIZED"
	case http.StatusForbidden:
		return "FORBIDDEN"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusMethodNotAllowed:
		return "METHOD_NOT_ALLOWED"
	case http.StatusConflict:
		return "CONFLICT"
	case http.StatusTooManyRequests:
		return "RATE_LIMITED"
	case http.StatusServiceUnavailable:
		return "MAINTENANCE"
	default:
		return "SERVER_ERROR"
	}
}

func errorTranslationKey(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "errors.bad_request"
	case http.StatusUnauthorized:
		return "errors.unauthorized"
	case http.StatusForbidden:
		return "errors.forbidden"
	case http.StatusNotFound:
		return "errors.not_found"
	case http.StatusMethodNotAllowed:
		return "errors.method_not_allowed"
	case http.StatusTooManyRequests:
		return "errors.rate_limited"
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return "errors.unavailable"
	case http.StatusInternalServerError:
		return "errors.server_error"
	default:
		return ""
	}
}

func (s *Server) respondText(w http.ResponseWriter, text string) {
	s.respondTextStatus(w, http.StatusOK, text)
}

func (s *Server) respondTextStatus(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(text))
}

// Handlers

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	lang := i18n.LangFromRequest(r)
	now := time.Now().UTC().Truncate(time.Second)
	branding := s.cfg.Server.Branding
	checks := HealthChecks{Database: "ok", Cache: "ok", Disk: "ok", Scheduler: "ok"}
	for _, check := range health.Results() {
		switch check.Key {
		case "database":
			checks.Database = check.Status
		case "cache":
			checks.Cache = check.Status
		case "disk":
			checks.Disk = check.Status
		case "scheduler":
			checks.Scheduler = check.Status
		case "tor":
			checks.Tor = check.Status
		case "i2p":
			checks.I2P = check.Status
		default:
			checks.Datasets = append(checks.Datasets, HealthCheck{Key: check.Key, Value: check.Status})
		}
	}
	response := HealthResponse{
		Project: HealthProject{Name: branding.Title, Tagline: branding.Tagline, Description: branding.Description},
		Status:  "healthy", Version: s.version, GoVersion: runtime.Version(),
		Build: HealthBuild{Commit: s.commit, Date: s.buildDate}, Uptime: compactUptime(time.Since(s.startedAt)),
		Mode: mode.Get().String(), Timestamp: now,
		Features: HealthFeatures{
			GeoIP: s.cfg.Server.GeoIP.Enabled,
			Tor:   HealthTorFeature{Enabled: false, Running: false, State: "disabled", Status: "disabled", Hostname: ""},
			I2P:   HealthI2PFeature{Enabled: false, Running: false, Status: "disabled", State: "disabled", Provider: "none", Hostname: ""},
		}, Checks: checks,
		Stats: HealthStats{RequestsTotal: s.requestTotal.Load(), Requests24h: s.requestsLast24Hours(time.Now()), ActiveConnections: s.requestActive.Load()},
	}
	if s.configWatcher != nil && s.configWatcher.PendingRestart() {
		response.PendingRestart = true
		response.RestartReason = s.configWatcher.RestartSettings()
		response.Status = "restart_required"
	}
	if response.Checks.Database == "error" || response.Checks.Disk == "error" {
		response.Status = "unhealthy"
	} else if response.Checks.Cache != "ok" || response.Checks.Scheduler != "ok" {
		response.Status = "degraded"
	}
	if s.shuttingDown.Load() {
		response.Status = "shutting_down"
	}
	for _, check := range health.Results() {
		if check.Status == "ok" {
			continue
		}
		if check.Critical {
			response.Status = "unhealthy"
		} else if response.Status == "healthy" || response.Status == "restart_required" {
			response.Status = "degraded"
		}
	}
	if s.torManager != nil {
		snapshot := s.torManager.Snapshot()
		response.Features.Tor = HealthTorFeature{Enabled: snapshot.Enabled, Running: snapshot.Running, State: torLifecycle(snapshot.Status), Status: snapshot.Status, Hostname: snapshot.Hostname}
		if snapshot.Enabled {
			response.Checks.Tor = "ok"
			if snapshot.Status == "unavailable" {
				response.Checks.Tor = "degraded"
				response.Status = "degraded"
			}
		}
	} else if s.torStartupFailed {
		response.Features.Tor = HealthTorFeature{Enabled: true, Running: false, State: "error", Status: "unavailable", Hostname: ""}
		response.Checks.Tor = "degraded"
		response.Status = "degraded"
	}
	if s.i2pManager != nil {
		snapshot := s.i2pManager.Snapshot()
		response.Features.I2P = HealthI2PFeature{Enabled: snapshot.Enabled, Running: snapshot.Running, Status: snapshot.Status, State: string(snapshot.State), Provider: snapshot.Provider, Hostname: snapshot.Hostname}
		if snapshot.Enabled {
			response.Checks.I2P = "ok"
			if snapshot.Status == "unavailable" {
				response.Checks.I2P = "degraded"
				response.Status = "degraded"
			}
		}
	} else if s.i2pStartupFailed {
		response.Features.I2P = HealthI2PFeature{Enabled: true, Running: false, Status: "unavailable", State: "error", Provider: "none", Hostname: ""}
		response.Checks.I2P = "degraded"
		response.Status = "degraded"
	}
	maintenance := s.RefreshMaintenance().State
	if maintenance != nil {
		response.Status = "maintenance"
		response.Maintenance = maintenance
	}
	if s.shuttingDown.Load() {
		response.Status = "shutting_down"
		response.Maintenance = nil
	}
	statusCode := healthHTTPStatus(response.Status)
	if strings.Contains(r.Header.Get("Accept"), "text/plain") {
		if isAPIHealthPath(r.URL.Path, s.cfg.Server.APIVersion) {
			s.respondTextStatus(w, statusCode, healthAPIText(response, lang))
		} else {
			s.respondTextStatus(w, statusCode, healthFrontendText(response, lang))
		}
		return
	}
	if statusCode != http.StatusOK {
		w.WriteHeader(statusCode)
	}
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		checks := make([]healthPageRow, 0, len(response.Checks.OrderedKeys()))
		checkValues := response.Checks.Values()
		for _, name := range response.Checks.OrderedKeys() {
			checks = append(checks, healthPageRow{Label: healthCheckLabel(lang, name), Value: healthValueText(lang, checkValues[name]), Class: healthCheckClass(checkValues[name])})
		}
		features := []healthPageRow{
			{Label: i18n.Translate(lang, "health.tor"), Value: healthFeatureText(response.Features.Tor.Status, response.Features.Tor.Hostname, lang), Class: healthCheckClass(response.Features.Tor.Status)},
			{Label: i18n.Translate(lang, "health.i2p"), Value: healthFeatureText(response.Features.I2P.Status, response.Features.I2P.Hostname, lang), Class: healthCheckClass(response.Features.I2P.Status)},
			{Label: i18n.Translate(lang, "health.geoip"), Value: healthValueText(lang, healthEnabledStatus(response.Features.GeoIP)), Class: healthCheckClass(healthEnabledStatus(response.Features.GeoIP))},
		}
		stats := []healthPageRow{
			{Label: i18n.Translate(lang, "health.total_requests"), Value: formatHealthCountLocale(response.Stats.RequestsTotal, lang)},
			{Label: i18n.Translate(lang, "health.requests_24h"), Value: formatHealthCountLocale(response.Stats.Requests24h, lang)},
			{Label: i18n.Translate(lang, "health.active_connections"), Value: formatHealthCountLocale(response.Stats.ActiveConnections, lang)},
		}
		s.renderPage(w, r, "healthz.tmpl", map[string]interface{}{
			"Title":              response.Project.Name + " - " + i18n.Translate(lang, "health.title"),
			"ProjectName":        response.Project.Name,
			"ProjectTagline":     response.Project.Tagline,
			"ProjectDescription": response.Project.Description,
			"Status":             healthValueText(lang, response.Status),
			"StatusClass":        healthStatusClass(response.Status),
			"StatusIcon":         healthStatusIcon(response.Status),
			"Version":            response.Version,
			"GoVersion":          response.GoVersion,
			"BuildCommit":        response.Build.Commit,
			"BuildDate":          response.Build.Date,
			"Uptime":             humanDuration(uptimeDuration(response.Uptime), lang),
			"Mode":               healthModeText(lang, response.Mode),
			"Timestamp":          response.Timestamp,
			"TimestampText":      response.Timestamp.Format("January 02, 2006 at 15:04:05 MST"),
			"Features":           features,
			"Checks":             checks,
			"Statistics":         stats,
			"Health":             response,
		})
		return
	}
	s.respondJSON(w, statusCode, response)
}

func healthHTTPStatus(status string) int {
	switch status {
	case "unhealthy", "maintenance", "shutting_down":
		return http.StatusServiceUnavailable
	default:
		return http.StatusOK
	}
}

func healthCheckLabel(lang, key string) string {
	labelKey := "health.check." + key
	label := i18n.Translate(lang, labelKey)
	if label == labelKey {
		return i18n.TranslateFormat(lang, "health.check.unknown", map[string]string{"key": key})
	}
	return label
}

func healthEnabledStatus(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

type healthPageRow struct {
	Label string
	Value string
	Class string
}

func healthStatusClass(status string) string {
	switch status {
	case "healthy":
		return "status-ok"
	case "degraded", "restart_required":
		return "status-warning"
	default:
		return "status-error"
	}
}

func healthCheckClass(status string) string {
	switch status {
	case "ok", "healthy", "running", "enabled":
		return "status-ok"
	case "degraded", "starting", "unavailable":
		return "status-warning"
	case "disabled":
		return "status-muted"
	default:
		return "status-error"
	}
}

func healthStatusIcon(status string) string {
	switch status {
	case "healthy":
		return "✅"
	case "degraded":
		return "⚠️"
	case "restart_required":
		return "🔄"
	case "maintenance":
		return "🚧"
	case "shutting_down":
		return "🛑"
	default:
		return "❌"
	}
}

func torLifecycle(status string) string {
	switch status {
	case "disabled", "starting", "healthy":
		if status == "healthy" {
			return "running"
		}
		return status
	default:
		return "error"
	}
}

func healthFeatureText(status, hostname, lang string) string {
	if status == "" {
		return i18n.Translate(lang, "health.values.disabled")
	}
	status = healthValueText(lang, status)
	if hostname != "" {
		return status + " - " + hostname
	}
	return status
}

func healthModeText(lang, mode string) string {
	if mode == "" {
		return i18n.Translate(lang, "health.values.unknown")
	}
	return healthValueText(lang, mode)
}

func healthValueText(lang, value string) string {
	if value == "" {
		return i18n.Translate(lang, "health.values.unknown")
	}
	key := "health.values." + strings.ToLower(value)
	translated := i18n.Translate(lang, key)
	if translated == key {
		return titleStatus(value)
	}
	return translated
}

func compactUptime(uptime time.Duration) string {
	if uptime < 0 {
		uptime = 0
	}
	seconds := int64(uptime.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	minutes %= 60
	if hours < 24 {
		if minutes == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	days := hours / 24
	hours %= 24
	if hours == 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dd %dh", days, hours)
}

func humanDuration(uptime time.Duration, lang string) string {
	if uptime < 0 {
		uptime = 0
	}
	seconds := int64(uptime.Seconds())
	if seconds < 60 {
		return i18n.TranslatePlural(lang, "health.duration.seconds", int(seconds))
	}
	minutes := seconds / 60
	if minutes < 60 {
		return i18n.TranslatePlural(lang, "health.duration.minutes", int(minutes))
	}
	hours := minutes / 60
	if hours < 24 {
		return i18n.TranslatePlural(lang, "health.duration.hours", int(hours))
	}
	days := hours / 24
	hours %= 24
	if hours == 0 {
		return i18n.TranslatePlural(lang, "health.duration.days", int(days))
	}
	return i18n.TranslateFormat(lang, "health.duration.day_hours", map[string]string{
		"days":  i18n.TranslatePlural(lang, "health.duration.days", int(days)),
		"hours": i18n.TranslatePlural(lang, "health.duration.hours", int(hours)),
	})
}

func isAPIHealthPath(requestPath, version string) bool {
	return requestPath == "/api/healthz" || requestPath == "/api/"+version+"/server/healthz"
}

func healthFrontendText(response HealthResponse, lang string) string {
	checked := response.Timestamp
	var output strings.Builder
	fmt.Fprintf(&output, "%s - %s\n\n%s: %s\n%s: %s\n%s: %s (%s)\n%s: %s\n%s: %s\n%s: %s\n%s: %s\n\n%s\n  %s: %s\n  %s: %s\n\n%s\n",
		response.Project.Name, response.Project.Tagline,
		i18n.Translate(lang, "health.status"), healthValueText(lang, response.Status),
		i18n.Translate(lang, "health.version"), response.Version,
		i18n.Translate(lang, "health.build"), response.Build.Commit, response.Build.Date,
		i18n.Translate(lang, "health.go_version"), response.GoVersion,
		i18n.Translate(lang, "health.uptime"), humanDuration(uptimeDuration(response.Uptime), lang),
		i18n.Translate(lang, "health.mode"), healthModeText(lang, response.Mode),
		i18n.Translate(lang, "health.checked"), checked.UTC().Format(time.RFC3339),
		i18n.Translate(lang, "health.features"), i18n.Translate(lang, "health.tor"),
		healthFeatureText(response.Features.Tor.Status, response.Features.Tor.Hostname, lang),
		i18n.Translate(lang, "health.i2p"), healthFeatureText(response.Features.I2P.Status, response.Features.I2P.Hostname, lang),
		i18n.Translate(lang, "health.checks"))
	fmt.Fprintf(&output, "%s: %s\n", i18n.Translate(lang, "health.geoip"), healthValueText(lang, healthEnabledStatus(response.Features.GeoIP)))
	checkValues := response.Checks.Values()
	for _, name := range response.Checks.OrderedKeys() {
		fmt.Fprintf(&output, "  %s: %s\n", healthCheckLabel(lang, name), healthValueText(lang, checkValues[name]))
	}
	fmt.Fprintf(&output, "\n%s\n  %s: %s\n  %s: %s\n  %s: %s\n",
		i18n.Translate(lang, "health.statistics"),
		i18n.Translate(lang, "health.total_requests"), formatHealthCountLocale(response.Stats.RequestsTotal, lang),
		i18n.Translate(lang, "health.requests_24h"), formatHealthCountLocale(response.Stats.Requests24h, lang),
		i18n.Translate(lang, "health.active_connections"), formatHealthCountLocale(response.Stats.ActiveConnections, lang))
	return output.String()
}

func healthAPIText(response HealthResponse, lang string) string {
	var output strings.Builder
	fmt.Fprintf(&output, "project.name: %s\nproject.tagline: %s\nproject.description: %s\nstatus: %s\nversion: %s\ngo_version: %s\nbuild.commit: %s\nbuild.date: %s\nuptime: %s\nmode: %s\ntimestamp: %s\n",
		response.Project.Name, response.Project.Tagline, response.Project.Description, healthValueText(lang, response.Status), response.Version,
		response.GoVersion, response.Build.Commit, response.Build.Date, response.Uptime, healthModeText(lang, response.Mode), response.Timestamp.Format(time.RFC3339))
	fmt.Fprintf(&output, "features.tor: %s\n", healthFeatureText(response.Features.Tor.Status, response.Features.Tor.Hostname, lang))
	fmt.Fprintf(&output, "features.i2p: %s", healthFeatureText(response.Features.I2P.Status, response.Features.I2P.Hostname, lang))
	if response.Features.I2P.Provider != "" && response.Features.I2P.Provider != "none" {
		fmt.Fprintf(&output, " provider=%s", response.Features.I2P.Provider)
	}
	if response.Features.I2P.State != "" {
		fmt.Fprintf(&output, " state=%s", response.Features.I2P.State)
	}
	output.WriteByte('\n')
	fmt.Fprintf(&output, "features.geoip: %s\n", healthEnabledStatus(response.Features.GeoIP))
	checkValues := response.Checks.Values()
	for _, name := range response.Checks.OrderedKeys() {
		fmt.Fprintf(&output, "checks.%s: %s\n", name, checkValues[name])
	}
	fmt.Fprintf(&output, "stats.requests_total: %d\nstats.requests_24h: %d\nstats.active_connections: %d\n",
		response.Stats.RequestsTotal, response.Stats.Requests24h, response.Stats.ActiveConnections)
	return output.String()
}

func uptimeDuration(value string) time.Duration {
	var days, hours, minutes, seconds int64
	_, _ = fmt.Sscanf(value, "%dd %dh", &days, &hours)
	if strings.HasSuffix(value, "d") {
		_, _ = fmt.Sscanf(value, "%dd", &days)
		return time.Duration(days) * 24 * time.Hour
	}
	if strings.HasSuffix(value, "h") || strings.Contains(value, "h ") {
		_, _ = fmt.Sscanf(value, "%dh %dm", &hours, &minutes)
		return time.Duration(days)*24*time.Hour + time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute
	}
	if strings.HasSuffix(value, "m") {
		_, _ = fmt.Sscanf(value, "%dm", &minutes)
		return time.Duration(minutes) * time.Minute
	}
	_, _ = fmt.Sscanf(value, "%ds", &seconds)
	return time.Duration(seconds) * time.Second
}

func titleStatus(status string) string {
	if status == "" {
		return "Unknown"
	}
	return strings.ToUpper(status[:1]) + status[1:]
}

func formatHealthCount(value uint64) string {
	text := strconv.FormatUint(value, 10)
	for index := len(text) - 3; index > 0; index -= 3 {
		text = text[:index] + "," + text[index:]
	}
	return text
}

func formatHealthCountLocale(value uint64, lang string) string {
	return message.NewPrinter(language.Make(lang)).Sprintf("%d", value)
}

func (s *Server) handleAPIInfo(w http.ResponseWriter, r *http.Request) {
	endpoints := apiEndpointList(s.router)
	lang := i18n.LangFromRequest(r)
	key := "api_info.description"
	if s.zipSvc != nil {
		key = "api_info.description_zip"
	}
	description := i18n.TranslateFormat(lang, key, map[string]string{
		"cities":    formatHealthCountLocale(uint64(s.citySvc.Count()), lang),
		"countries": formatHealthCountLocale(uint64(s.citySvc.CountryCount()), lang),
	})
	info := map[string]interface{}{
		"name":        i18n.Translate(lang, "swagger.title"),
		"version":     s.version,
		"description": description,
		"endpoints":   endpoints,
	}
	s.respondSuccess(w, info, 0)
}

func apiEndpointList(router *chi.Mux) []string {
	endpoints := make([]string, 0)
	_ = chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/api/") || route == "/graphql" {
			endpoints = append(endpoints, method+" "+route)
		}
		return nil
	})
	sort.Strings(endpoints)
	return endpoints
}

func (s *Server) handleGetCities(w http.ResponseWriter, r *http.Request) {
	page, err := parseBoundedInt(r.URL.Query().Get("page"), 1, 1, 1000000)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_PAGE", i18n.Translate(i18n.LangFromRequest(r), "api_errors.invalid_page"))
		return
	}
	limit, err := parseBoundedInt(r.URL.Query().Get("limit"), 50, 1, 100)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_LIMIT", i18n.Translate(i18n.LangFromRequest(r), "api_errors.invalid_limit"))
		return
	}

	cityList, total := s.citySvc.GetAll(page, limit)
	totalPages := (total + limit - 1) / limit

	response := PaginatedResponse{
		Cities: cityList,
		Pagination: PaginationInfo{
			Page:       page,
			Limit:      limit,
			Total:      total,
			TotalPages: totalPages,
			HasNext:    page < totalPages,
			HasPrev:    page > 1,
		},
	}

	s.respondSuccess(w, response, len(cityList))
}

func (s *Server) handleSearchCities(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(query) < 2 || len(query) > 100 {
		s.respondError(w, http.StatusBadRequest, "INVALID_QUERY", i18n.Translate(i18n.LangFromRequest(r), "api_errors.invalid_query"))
		return
	}

	limit, err := parseBoundedInt(r.URL.Query().Get("limit"), 20, 1, 100)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_LIMIT", i18n.Translate(i18n.LangFromRequest(r), "api_errors.invalid_limit"))
		return
	}

	country := r.URL.Query().Get("country")
	if country != "" && len(country) != 2 {
		s.respondError(w, http.StatusBadRequest, "INVALID_COUNTRY", i18n.Translate(i18n.LangFromRequest(r), "api_errors.invalid_country"))
		return
	}
	results := s.citySvc.SearchByCountry(query, country, limit)
	s.respondSuccess(w, results, len(results))
}

func (s *Server) handleGetCityByID(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_ID", i18n.Translate(i18n.LangFromRequest(r), "api_errors.invalid_city_id"))
		return
	}

	city, found := s.citySvc.GetByID(id)
	if !found {
		s.respondError(w, http.StatusNotFound, "NOT_FOUND", i18n.Translate(i18n.LangFromRequest(r), "api_errors.city_not_found"))
		return
	}

	s.respondSuccess(w, city, 1)
}

func (s *Server) handleGetCitiesByCountry(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")
	if len(code) != 2 {
		s.respondError(w, http.StatusBadRequest, "INVALID_CODE", i18n.Translate(i18n.LangFromRequest(r), "api_errors.invalid_country_code"))
		return
	}

	limit, err := parseBoundedInt(r.URL.Query().Get("limit"), 50, 1, 100)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_LIMIT", i18n.Translate(i18n.LangFromRequest(r), "api_errors.invalid_limit"))
		return
	}

	cityList := s.citySvc.GetByCountry(code, limit)
	s.respondSuccess(w, cityList, len(cityList))
}

func (s *Server) handleFindNearestGET(w http.ResponseWriter, r *http.Request) {
	latStr := r.URL.Query().Get("latitude")
	lonStr := r.URL.Query().Get("longitude")

	lat, err := strconv.ParseFloat(latStr, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_LATITUDE", i18n.Translate(i18n.LangFromRequest(r), "api_errors.invalid_latitude"))
		return
	}

	lon, err := strconv.ParseFloat(lonStr, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_LONGITUDE", i18n.Translate(i18n.LangFromRequest(r), "api_errors.invalid_longitude"))
		return
	}

	if lat < -90 || lat > 90 {
		s.respondError(w, http.StatusBadRequest, "OUT_OF_RANGE", i18n.Translate(i18n.LangFromRequest(r), "api_errors.latitude_range"))
		return
	}
	if lon < -180 || lon > 180 {
		s.respondError(w, http.StatusBadRequest, "OUT_OF_RANGE", i18n.Translate(i18n.LangFromRequest(r), "api_errors.longitude_range"))
		return
	}

	nearest, found := s.citySvc.FindNearest(lat, lon)
	if !found {
		s.respondError(w, http.StatusNotFound, "NOT_FOUND", i18n.Translate(i18n.LangFromRequest(r), "api_errors.no_cities_found"))
		return
	}

	result := map[string]interface{}{
		"city":     nearest.City,
		"distance": nearest.Distance,
		"coordinates": map[string]float64{
			"latitude":  lat,
			"longitude": lon,
		},
	}
	s.respondSuccess(w, result, 1)
}

func (s *Server) handleFindNearestPOST(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_JSON", i18n.Translate(i18n.LangFromRequest(r), "api_errors.invalid_json"))
		return
	}

	if req.Latitude < -90 || req.Latitude > 90 {
		s.respondError(w, http.StatusBadRequest, "OUT_OF_RANGE", i18n.Translate(i18n.LangFromRequest(r), "api_errors.latitude_range"))
		return
	}
	if req.Longitude < -180 || req.Longitude > 180 {
		s.respondError(w, http.StatusBadRequest, "OUT_OF_RANGE", i18n.Translate(i18n.LangFromRequest(r), "api_errors.longitude_range"))
		return
	}

	nearest, found := s.citySvc.FindNearest(req.Latitude, req.Longitude)
	if !found {
		s.respondError(w, http.StatusNotFound, "NOT_FOUND", i18n.Translate(i18n.LangFromRequest(r), "api_errors.no_cities_found"))
		return
	}

	result := map[string]interface{}{
		"city":     nearest.City,
		"distance": nearest.Distance,
		"coordinates": map[string]float64{
			"latitude":  req.Latitude,
			"longitude": req.Longitude,
		},
	}
	s.respondSuccess(w, result, 1)
}

func (s *Server) handleFindNearby(w http.ResponseWriter, r *http.Request) {
	latStr := r.URL.Query().Get("lat")
	lonStr := r.URL.Query().Get("lon")
	radiusStr := r.URL.Query().Get("radius")
	limitStr := r.URL.Query().Get("limit")

	lat, err := strconv.ParseFloat(latStr, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_LATITUDE", i18n.Translate(i18n.LangFromRequest(r), "api_errors.invalid_latitude"))
		return
	}

	lon, err := strconv.ParseFloat(lonStr, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_LONGITUDE", i18n.Translate(i18n.LangFromRequest(r), "api_errors.invalid_longitude"))
		return
	}

	if lat < -90 || lat > 90 {
		s.respondError(w, http.StatusBadRequest, "OUT_OF_RANGE", i18n.Translate(i18n.LangFromRequest(r), "api_errors.latitude_range"))
		return
	}
	if lon < -180 || lon > 180 {
		s.respondError(w, http.StatusBadRequest, "OUT_OF_RANGE", i18n.Translate(i18n.LangFromRequest(r), "api_errors.longitude_range"))
		return
	}

	radius := 50.0 // default 50km
	if radiusStr != "" {
		radius, err = strconv.ParseFloat(radiusStr, 64)
		if err != nil || radius <= 0 || radius > 20000 || math.IsNaN(radius) || math.IsInf(radius, 0) {
			s.respondError(w, http.StatusBadRequest, "INVALID_RADIUS", i18n.Translate(i18n.LangFromRequest(r), "api_errors.invalid_radius"))
			return
		}
	}

	limit, err := parseBoundedInt(limitStr, 10, 1, 100)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_LIMIT", i18n.Translate(i18n.LangFromRequest(r), "api_errors.invalid_limit"))
		return
	}

	results := s.citySvc.FindNearby(lat, lon, radius, limit)
	s.respondSuccess(w, results, len(results))
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := map[string]interface{}{
		"total_cities":    s.citySvc.Count(),
		"total_countries": s.citySvc.CountryCount(),
		"version":         s.version,
		"data_source":     "GeoNames",
	}
	s.respondSuccess(w, stats, 0)
}

func (s *Server) handleStatsTxt(w http.ResponseWriter, r *http.Request) {
	lang := i18n.LangFromRequest(r)
	text := fmt.Sprintf("%s\n%s: %d\n%s: %d\n%s: %s\n%s: GeoNames\n",
		i18n.Translate(lang, "api_text.stats_heading"),
		i18n.Translate(lang, "api_text.total_cities"), s.citySvc.Count(),
		i18n.Translate(lang, "api_text.total_countries"), s.citySvc.CountryCount(),
		i18n.Translate(lang, "api_text.version"), s.version,
		i18n.Translate(lang, "api_text.data_source"))
	s.respondText(w, text)
}

func (s *Server) handleCount(w http.ResponseWriter, r *http.Request) {
	s.respondSuccess(w, map[string]int{"count": s.citySvc.Count()}, 0)
}

func (s *Server) handleCountTxt(w http.ResponseWriter, r *http.Request) {
	s.respondText(w, fmt.Sprintf("%d\n", s.citySvc.Count()))
}

func (s *Server) handleRandomCity(w http.ResponseWriter, r *http.Request) {
	if s.citySvc.Count() == 0 {
		s.respondError(w, http.StatusNotFound, "NOT_FOUND", i18n.Translate(i18n.LangFromRequest(r), "api_errors.no_cities_available"))
		return
	}
	index := int(time.Now().UnixNano() % int64(s.citySvc.Count()))
	cityList, _ := s.citySvc.GetAll(index+1, 1)
	s.respondSuccess(w, cityList[0], 1)
}

func (s *Server) handleRandomCityTxt(w http.ResponseWriter, r *http.Request) {
	if s.citySvc.Count() == 0 {
		s.respondText(w, i18n.Translate(i18n.LangFromRequest(r), "api_errors.no_cities_available")+"\n")
		return
	}
	index := int(time.Now().UnixNano() % int64(s.citySvc.Count()))
	cityList, _ := s.citySvc.GetAll(index+1, 1)
	city := cityList[0]
	lang := i18n.LangFromRequest(r)
	text := fmt.Sprintf("%s: %s\n%s: %s\n%s: %d\n%s: %.6f\n%s: %.6f\n",
		i18n.Translate(lang, "api_text.city"), city.Name,
		i18n.Translate(lang, "api_text.country"), city.Country,
		i18n.Translate(lang, "api_text.id"), city.ID,
		i18n.Translate(lang, "api_text.latitude"), city.Coord.Lat,
		i18n.Translate(lang, "api_text.longitude"), city.Coord.Lon)
	s.respondText(w, text)
}

// Web handlers

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if acceptsMediaType(r.Header.Get("Accept"), "application/json") {
		s.respondSuccess(w, map[string]interface{}{
			"total_cities": s.citySvc.Count(), "total_countries": s.citySvc.CountryCount(), "version": s.version, "data_source": "GeoNames",
		}, 0)
		return
	}
	s.renderPage(w, r, "home.tmpl", map[string]interface{}{
		"Version":        s.version,
		"TotalCities":    s.citySvc.Count(),
		"TotalCountries": s.citySvc.CountryCount(),
		"ReturnTo":       r.URL.RequestURI(),
	})
}

func (s *Server) handleSearchPage(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	country := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("country")))
	searchType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))
	if searchType == "" {
		searchType = "city"
	}
	limit := 20
	data := map[string]interface{}{
		"Query": query, "Country": country, "Limit": limit,
		"HasSearch": query != "", "SearchType": searchType, "ReturnTo": r.URL.Path,
		"Cities": []cities.City{},
	}
	if query != "" {
		searchStatus := http.StatusBadRequest
		searchErrorCode := ""
		if searchType == "zip" {
			if !validUSZIP(query) {
				data["SearchError"] = i18n.Translate(i18n.LangFromRequest(r), "search.zip_invalid")
				searchErrorCode = "INVALID_ZIP"
			} else if s.zipSvc == nil {
				data["SearchError"] = i18n.Translate(i18n.LangFromRequest(r), "search.zip_unavailable")
				searchErrorCode = "MAINTENANCE"
				searchStatus = http.StatusServiceUnavailable
			} else if record, ok := s.zipSvc.Lookup(query); ok {
				data["ZIPRecord"] = record
			}
		} else if searchType != "city" {
			data["SearchError"] = i18n.Translate(i18n.LangFromRequest(r), "search.type_invalid")
			searchErrorCode = "INVALID_TYPE"
		} else {
			switch {
			case len(query) < 2 || len(query) > 100:
				data["SearchError"] = i18n.Translate(i18n.LangFromRequest(r), "search.query_invalid")
				searchErrorCode = "INVALID_QUERY"
			case country != "" && len(country) != 2:
				data["SearchError"] = i18n.Translate(i18n.LangFromRequest(r), "search.country_invalid")
				searchErrorCode = "INVALID_COUNTRY"
			default:
				var err error
				limit, err = parseBoundedInt(r.URL.Query().Get("limit"), 20, 1, 100)
				if err != nil {
					data["SearchError"] = i18n.Translate(i18n.LangFromRequest(r), "search.limit_invalid")
					searchErrorCode = "INVALID_LIMIT"
				} else {
					data["Limit"] = limit
					data["Cities"] = s.citySvc.SearchByCountry(query, country, limit)
				}
			}
		}
		if data["SearchError"] != nil {
			if acceptsMediaType(r.Header.Get("Accept"), "application/json") {
				s.respondError(w, searchStatus, searchErrorCode, data["SearchError"].(string))
				return
			}
			w.WriteHeader(searchStatus)
		}
	}
	if acceptsMediaType(r.Header.Get("Accept"), "application/json") {
		if searchType == "zip" {
			if s.zipSvc == nil {
				s.respondError(w, http.StatusServiceUnavailable, "MAINTENANCE", "US postal data is unavailable")
				return
			}
			if record, ok := data["ZIPRecord"]; ok {
				s.respondSuccess(w, record, 1)
				return
			}
			results := s.zipSvc.Search(query, "", 20)
			s.respondSuccess(w, results, len(results))
			return
		}
		results, _ := data["Cities"].([]cities.City)
		s.respondSuccess(w, results, len(results))
		return
	}
	s.renderPage(w, r, "search.tmpl", data)
}

func (s *Server) handleCoordinatesPage(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	data := map[string]interface{}{
		"Latitude": query.Get("lat"), "Longitude": query.Get("lon"),
		"Radius": "50", "Limit": "10", "ReturnTo": r.URL.Path,
	}
	hasCoordinates := query.Has("lat") || query.Has("lon") || query.Has("radius") || query.Has("limit")
	data["HasCoordinates"] = hasCoordinates
	if hasCoordinates {
		searchErrorCode := ""
		latitude, latErr := strconv.ParseFloat(query.Get("lat"), 64)
		longitude, lonErr := strconv.ParseFloat(query.Get("lon"), 64)
		radius, radiusErr := strconv.ParseFloat(query.Get("radius"), 64)
		if query.Get("radius") == "" {
			radius, radiusErr = 50, nil
		}
		limit, limitErr := parseBoundedInt(query.Get("limit"), 10, 1, 100)
		switch {
		case latErr != nil || math.IsNaN(latitude) || math.IsInf(latitude, 0) || latitude < -90 || latitude > 90:
			data["SearchError"] = i18n.Translate(i18n.LangFromRequest(r), "coordinates.latitude_invalid")
			searchErrorCode = "INVALID_LATITUDE"
		case lonErr != nil || math.IsNaN(longitude) || math.IsInf(longitude, 0) || longitude < -180 || longitude > 180:
			data["SearchError"] = i18n.Translate(i18n.LangFromRequest(r), "coordinates.longitude_invalid")
			searchErrorCode = "INVALID_LONGITUDE"
		case radiusErr != nil || math.IsNaN(radius) || math.IsInf(radius, 0) || radius <= 0 || radius > 20000:
			data["SearchError"] = i18n.Translate(i18n.LangFromRequest(r), "coordinates.radius_invalid")
			searchErrorCode = "INVALID_RADIUS"
		case limitErr != nil:
			data["SearchError"] = i18n.Translate(i18n.LangFromRequest(r), "coordinates.max_results_invalid")
			searchErrorCode = "INVALID_LIMIT"
		default:
			data["Radius"], data["Limit"] = strconv.FormatFloat(radius, 'f', -1, 64), limit
			data["Nearby"] = s.citySvc.FindNearby(latitude, longitude, radius, limit)
		}
		if data["SearchError"] != nil {
			if acceptsMediaType(r.Header.Get("Accept"), "application/json") {
				s.respondError(w, http.StatusBadRequest, searchErrorCode, data["SearchError"].(string))
				return
			}
			w.WriteHeader(http.StatusBadRequest)
		}
	}
	if acceptsMediaType(r.Header.Get("Accept"), "application/json") {
		results, ok := data["Nearby"].([]cities.CityWithDistance)
		if !ok {
			results = []cities.CityWithDistance{}
		}
		s.respondSuccess(w, results, len(results))
		return
	}
	s.renderPage(w, r, "coordinates.tmpl", data)
}

func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, name string, data map[string]interface{}) {
	content, err := templateFS.ReadFile("template/page/" + name)
	if err != nil {
		slog.Error("http.template_read_failed", "template", name, "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "This page is temporarily unavailable.")
		return
	}
	content = []byte(injectRenderPageHooks(string(content)))
	if data == nil {
		data = make(map[string]interface{})
	}
	s.applyRobotsHeader(w, r)
	pageTitle, _ := data["Title"].(string)
	if pageTitle == "" {
		pageTitle = s.cfg.Server.Branding.Title
	}
	pageDescription := s.cfg.Server.Branding.Description
	data["SEOHead"] = s.seoHead(r, r.URL.Path, pageTitle, pageDescription)
	data["BaseURL"] = strings.TrimSuffix(s.baseURL, "/")
	data["BrandTitle"] = s.brandTitle()
	data["LogoURL"] = s.imageURL("logo", r)
	data["HasAccent"] = s.cfg.Web.UI.Accent != ""
	data["CurrentPath"] = r.URL.Path
	data["APIBase"] = strings.TrimSuffix(s.baseURL, "/") + "/api/" + s.cfg.Server.APIVersion
	data["Theme"] = themePreference(r)
	data["NextTheme"] = nextTheme(themePreference(r))
	pageLanguage := i18n.LangFromRequest(r)
	data["Lang"] = pageLanguage
	data["Dir"] = i18n.Direction(pageLanguage)
	for _, language := range i18n.Languages() {
		if language.Code == pageLanguage {
			data["LanguageName"] = language.NativeName
			break
		}
	}
	data["AvailableLanguages"] = i18n.Languages()
	csrfToken := s.csrfToken(w, r)
	data["CSRFToken"] = csrfToken
	if data["ReturnTo"] == nil {
		data["ReturnTo"] = r.URL.RequestURI()
	}
	returnTo, _ := data["ReturnTo"].(string)
	data["ConsentBanner"] = s.renderCookieConsentBanner(r, csrfToken, returnTo)
	data["SiteBanners"] = s.activeAnnouncements(r, csrfToken, returnTo)
	data["TrackingScript"] = s.trackingScript(r)
	data["TrackingNoscript"] = s.trackingNoscript(r)
	page, err := template.New(name).Funcs(template.FuncMap{
		"t":                i18n.Translate,
		"tf":               i18n.TranslateFormat,
		"tp":               i18n.TranslatePlural,
		"asset":            assetURL,
		"operatorHTML":     func(pageName string) template.HTML { return s.operatorHTML(r, pageName) },
		"trackingScript":   func() template.HTML { return s.trackingScript(r) },
		"trackingNoscript": func() template.HTML { return s.trackingNoscript(r) },
		"dict": func(values ...interface{}) map[string]string {
			result := make(map[string]string, len(values)/2)
			for index := 0; index+1 < len(values); index += 2 {
				result[fmt.Sprint(values[index])] = fmt.Sprint(values[index+1])
			}
			return result
		},
	}).Parse(strings.Replace(stripLegacySEOTags(string(content)), "<head>", "<head>{{.SEOHead}}", 1))
	if err != nil {
		slog.Error("http.template_parse_failed", "template", name, "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "This page is temporarily unavailable.")
		return
	}
	var rendered strings.Builder
	if err := page.Execute(&rendered, data); err != nil {
		slog.Error("http.template_render_failed", "template", name, "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "This page is temporarily unavailable.")
		return
	}
	shared, err := s.applySharedPageShell(r, rendered.String(), data)
	if err != nil {
		slog.Error("http.page_shell_failed", "template", name, "error", err)
		s.renderError(w, r, http.StatusInternalServerError, "This page is temporarily unavailable.")
		return
	}
	writeHTMLResponse(w, r, http.StatusOK, []byte(shared))
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, status int, message string) {
	if r == nil {
		r = &http.Request{Header: make(http.Header), URL: &url.URL{Path: "/"}}
	}
	lang := i18n.LangFromRequest(r)
	s.applyRobotsHeader(w, r)
	statusText := http.StatusText(status)
	if key := errorTranslationKey(status); key != "" {
		statusText = i18n.Translate(lang, key)
		message = i18n.Translate(lang, "errors.page_unavailable")
	}
	if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json") {
		s.respondJSON(w, status, APIResponse{Success: false, Error: fallbackErrorCode(status), Message: message})
		return
	}
	data := map[string]interface{}{"StatusCode": status, "StatusText": statusText, "Message": message, "Version": s.version}
	data["BrandTitle"] = s.brandTitle()
	data["LogoURL"] = s.imageURL("logo", r)
	data["CurrentPath"] = r.URL.Path
	data["HasAccent"] = s.cfg.Web.UI.Accent != ""
	data["SEOHead"] = s.seoHead(r, r.URL.Path, "Error", message)
	content, err := templateFS.ReadFile("template/page/error.tmpl")
	if err == nil {
		data["BaseURL"] = strings.TrimSuffix(s.baseURL, "/")
		data["Theme"] = themePreference(r)
		data["NextTheme"] = nextTheme(themePreference(r))
		data["CSRFToken"] = s.csrfToken(w, r)
		data["ReturnTo"] = r.URL.RequestURI()
		data["Lang"] = lang
		data["Dir"] = i18n.Direction(lang)
		for _, language := range i18n.Languages() {
			if language.Code == lang {
				data["LanguageName"] = language.NativeName
				break
			}
		}
		csrfToken := data["CSRFToken"].(string)
		returnTo := r.URL.RequestURI()
		data["SiteBanners"] = s.activeAnnouncements(r, csrfToken, returnTo)
		data["ConsentBanner"] = s.renderCookieConsentBanner(r, csrfToken, returnTo)
		data["TrackingNoscript"] = s.trackingNoscript(r)
		page, parseErr := template.New("error.tmpl").Funcs(template.FuncMap{
			"t": i18n.Translate, "tf": i18n.TranslateFormat, "asset": assetURL,
			"operatorHTML": func(name string) template.HTML { return s.operatorHTML(r, name) },
		}).Parse(strings.Replace(stripLegacySEOTags(string(content)), "<head>", "<head>{{.SEOHead}}", 1))
		if parseErr == nil {
			var rendered strings.Builder
			if executeErr := page.Execute(&rendered, data); executeErr == nil {
				shared, shellErr := s.applySharedPageShell(r, rendered.String(), data)
				if shellErr == nil {
					writeHTMLResponse(w, r, status, []byte(shared))
					return
				}
				slog.Error("http.error_page_shell_failed", "status", status, "error", shellErr)
			}
		}
	}
	writeFallbackError(w, r, status)
}

func writeFallbackError(w http.ResponseWriter, r *http.Request, status int) {
	lang := i18n.LangFromRequest(r)
	translationKey := errorTranslationKey(status)
	if translationKey == "" {
		if status >= 400 && status < 500 {
			translationKey = "errors.bad_request"
		} else {
			translationKey = "errors.server_error"
		}
	}
	statusText := i18n.Translate(lang, translationKey)
	message := i18n.Translate(lang, "errors.page_unavailable")
	if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(APIResponse{Success: false, Error: fallbackErrorCode(status), Message: message})
		return
	}
	w.Header().Set("Content-Language", lang)
	body := fmt.Sprintf(`<!doctype html><html lang="%s" dir="%s"><head><meta charset="utf-8"><title>%d %s</title></head><body><main><h1>%d</h1><h2>%s</h2><p>%s</p></main></body></html>`, template.HTMLEscapeString(lang), i18n.Direction(lang), status, template.HTMLEscapeString(statusText), status, template.HTMLEscapeString(statusText), template.HTMLEscapeString(message))
	writeHTMLResponse(w, r, status, []byte(body))
}

func fallbackErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		return "BAD_REQUEST"
	case http.StatusUnprocessableEntity:
		return "VALIDATION_FAILED"
	case http.StatusUnauthorized:
		return "UNAUTHORIZED"
	case http.StatusForbidden:
		return "FORBIDDEN"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusMethodNotAllowed:
		return "METHOD_NOT_ALLOWED"
	case http.StatusTooManyRequests:
		return "RATE_LIMITED"
	case http.StatusServiceUnavailable:
		return "MAINTENANCE"
	default:
		return "SERVER_ERROR"
	}
}

func (s *Server) handleLocale(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "lang")
	code := strings.TrimSuffix(name, ".json")
	data, ok := i18n.LocaleFile(code)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if setAssetCacheHeaders(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(data)
}

// PWA/Meta handlers

func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	s.serveBrandingImage(w, r, "favicon")
}

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	if setRevalidatingCacheHeaders(w, r) {
		return
	}
	manifest := map[string]interface{}{
		"name":             s.brandTitle(),
		"short_name":       s.brandTitle(),
		"description":      s.cfg.Server.Branding.Description,
		"start_url":        strings.TrimSuffix(s.baseURL, "/") + "/",
		"display":          "standalone",
		"background_color": "#1a1a1a",
		"theme_color":      "#0066cc",
		"icons": []map[string]interface{}{
			{"src": assetURL(strings.TrimSuffix(s.baseURL, "/") + "/favicon.ico?size=192"), "sizes": "192x192", "type": "image/png", "purpose": "any maskable"},
			{"src": assetURL(strings.TrimSuffix(s.baseURL, "/") + "/favicon.ico?size=512"), "sizes": "512x512", "type": "image/png", "purpose": "any maskable"},
		},
	}
	s.respondJSON(w, http.StatusOK, manifest)
}

func (s *Server) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	if setRevalidatingCacheHeaders(w, r) {
		return
	}
	baseURL, _ := json.Marshal(strings.TrimSuffix(s.baseURL, "/"))
	stamp, _ := json.Marshal(AssetStamp())
	sw := `const BASE = ` + string(baseURL) + `;
const CACHE_VERSION = ` + string(stamp) + `;
const CACHE_NAME = "citylist-cache-" + CACHE_VERSION;
const PRECACHE_ASSETS = [
  BASE + "/static/css/common.css?v=" + encodeURIComponent(CACHE_VERSION),
  BASE + "/static/css/components.css?v=" + encodeURIComponent(CACHE_VERSION),
  BASE + "/static/css/public.css?v=" + encodeURIComponent(CACHE_VERSION),
  BASE + "/static/js/app.js?v=" + encodeURIComponent(CACHE_VERSION),
  BASE + "/static/images/icon.svg?v=" + encodeURIComponent(CACHE_VERSION),
  BASE + "/static/vendor/swagger-ui/swagger-ui.css?v=" + encodeURIComponent(CACHE_VERSION),
  BASE + "/static/vendor/swagger-ui/swagger-ui-bundle.js?v=" + encodeURIComponent(CACHE_VERSION),
  BASE + "/static/vendor/swagger-ui/swagger-ui-standalone-preset.js?v=" + encodeURIComponent(CACHE_VERSION)
];
const NAV_TIMEOUT_MS = 8000;
self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(CACHE_NAME).then((cache) => cache.addAll(PRECACHE_ASSETS)));
});
self.addEventListener("activate", (event) => {
  event.waitUntil(caches.keys().then((keys) => Promise.all(
    keys.filter((key) => key.startsWith("citylist-cache-") && key !== CACHE_NAME).map((key) => caches.delete(key))
  )).then(() => self.clients.claim()));
});
self.addEventListener("message", (event) => {
  if (event.data?.type === "SKIP_WAITING") self.skipWaiting();
});
function withTimeout(promise, milliseconds) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("navigation timeout")), milliseconds);
    promise.then((value) => { clearTimeout(timer); resolve(value); }, (error) => { clearTimeout(timer); reject(error); });
  });
}
function offlineFallbackResponse() {
  const defaults = {
    lang: "en", dir: "ltr", title: "Offline", heading: "You are offline",
    message: "This page could not be loaded and no cached copy is available. Check your connection and try again.",
    reload: "Reload"
  };
  return caches.open("citylist-offline-strings").then((cache) => cache.match("/offline-strings.json")).catch(() => null)
    .then(async (response) => {
      let strings = defaults;
      try {
        if (response) strings = Object.assign({}, defaults, await response.json());
      } catch {
        strings = defaults;
      }
      const escapeHTML = (value) => String(value).replace(/[&<>\"]/g, (character) => ({
        "&": "&amp;", "<": "&lt;", ">": "&gt;", "\"": "&quot;"
      }[character]));
      const body = "<!doctype html><html lang=\"" + escapeHTML(strings.lang) + "\" dir=\"" + escapeHTML(strings.dir) + "\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>" + escapeHTML(strings.title) + "</title></head><body><main><h1>" + escapeHTML(strings.heading) + "</h1><p>" + escapeHTML(strings.message) + "</p><a href=\"" + escapeHTML(BASE + "/") + "\">" + escapeHTML(strings.reload) + "</a></main></body></html>";
      return new Response(body, { status: 503, statusText: "Service Unavailable", headers: { "Content-Type": "text/html; charset=utf-8" } });
    });
}
self.addEventListener("fetch", (event) => {
  const request = event.request;
  const url = new URL(request.url);
  if (request.method !== "GET" || url.origin !== self.location.origin || url.pathname.startsWith(BASE + "/api/")) return;
  if (request.mode === "navigate") {
    event.respondWith(withTimeout(fetch(request), NAV_TIMEOUT_MS).then((response) => {
      if (!response.ok || response.redirected) throw new Error("navigation returned an error or redirect");
      event.waitUntil(caches.open(CACHE_NAME).then((cache) => cache.put(request, response.clone())));
      return response;
    }).catch(async () => (await caches.match(request)) || offlineFallbackResponse()));
    return;
  }
  if (url.pathname.startsWith(BASE + "/static/")) {
    event.respondWith(caches.match(request).then((cached) => cached || fetch(request).then((response) => {
      if (response.ok && !response.redirected) event.waitUntil(caches.open(CACHE_NAME).then((cache) => cache.put(request, response.clone())));
      return response;
    })).catch(() => new Response("", { status: 504, statusText: "Gateway Timeout" })));
    return;
  }
  event.respondWith(fetch(request).catch(async () => (await caches.match(request)) || new Response("", { status: 504, statusText: "Gateway Timeout" })));
});`
	w.Header().Set("Content-Type", "application/javascript")
	_, _ = w.Write([]byte(sw))
}

func (s *Server) handleRobots(w http.ResponseWriter, r *http.Request) {
	var sb strings.Builder
	sb.WriteString("User-agent: *\n")
	for _, path := range s.cfg.Web.Robots.Allow {
		_, _ = fmt.Fprintf(&sb, "Allow: %s\n", path)
	}
	for _, path := range s.cfg.Web.Robots.Deny {
		_, _ = fmt.Fprintf(&sb, "Disallow: %s\n", path)
	}
	_, _ = fmt.Fprintf(&sb, "Sitemap: %s\n", s.BuildURL(r, "/sitemap.xml"))
	s.respondText(w, sb.String())
}

func (s *Server) handleSecurityTxt(w http.ResponseWriter, r *http.Request) {
	s.securityTxt(w, r)
}

func validSecurityHost(host string) string {
	if host == "" {
		return ""
	}
	parsed, err := url.Parse("https://" + host)
	if err != nil || parsed.Host != host || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return ""
		}
	}
	return host
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, r, "openapi.tmpl", map[string]interface{}{"ReturnTo": r.URL.RequestURI()})
}

func (s *Server) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	apiPrefix := "/api/" + s.cfg.Server.APIVersion
	lang := i18n.LangFromRequest(r)
	paths := openAPIPaths(s.router, apiPrefix, lang)
	description := i18n.Translate(lang, "swagger.description")
	if s.zipSvc == nil {
		description = i18n.Translate(lang, "pages.about.description")
	}
	spec := map[string]interface{}{
		"openapi": "3.0.0",
		"info": map[string]interface{}{
			"title":       i18n.Translate(lang, "swagger.title"),
			"description": description,
			"version":     s.version,
		},
		"servers": []map[string]string{
			{"url": strings.TrimSuffix(s.baseURL, "/") + "/"},
		},
		"paths": paths,
		"components": map[string]interface{}{
			"schemas": map[string]interface{}{
				"ErrorResponse": map[string]interface{}{
					"type":        "object",
					"description": i18n.Translate(lang, "swagger.schema_error_response"),
					"properties": map[string]interface{}{
						"ok":      map[string]string{"type": "boolean"},
						"error":   map[string]interface{}{"type": "string", "description": i18n.Translate(lang, "swagger.schema_error_code")},
						"message": map[string]interface{}{"type": "string", "description": i18n.Translate(lang, "swagger.schema_error_message")},
					},
				},
			},
		},
	}
	s.respondJSON(w, http.StatusOK, spec)
}

func openAPIPaths(router *chi.Mux, apiPrefix, lang string) map[string]interface{} {
	paths := make(map[string]interface{})
	_ = chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.Contains(route, "*") || isInternalRoutePath(route) ||
			!strings.HasPrefix(route, "/api/") && route != "/mcp" {
			return nil
		}
		if route == "/api/v1" || strings.HasPrefix(route, "/api/v1/") {
			route = apiPrefix + strings.TrimPrefix(route, "/api/v1")
		}
		methodName := strings.ToLower(method)
		switch methodName {
		case "get", "head", "post", "put", "patch", "delete", "options":
		default:
			return nil
		}
		item, ok := paths[route].(map[string]interface{})
		if !ok {
			item = make(map[string]interface{})
			paths[route] = item
		}
		description := i18n.Translate(lang, "swagger.endpoints."+openAPIEndpointKey(route, apiPrefix))
		responses := map[string]interface{}{
			"200": map[string]string{"description": i18n.Translate(lang, "swagger.success")},
		}
		for code, definition := range openAPIErrorResponses(lang) {
			responses[code] = definition
		}
		operation := map[string]interface{}{
			"summary": description, "description": description, "responses": responses,
		}
		if parameters := openAPIParameters(route, methodName, apiPrefix, lang); len(parameters) > 0 {
			operation["parameters"] = parameters
		}
		if methodName == "post" && strings.TrimPrefix(route, apiPrefix) == "/cities/coordinates" {
			operation["requestBody"] = map[string]interface{}{
				"required":    true,
				"description": i18n.Translate(lang, "swagger.request_coordinates"),
				"content": map[string]interface{}{
					"application/json": map[string]interface{}{
						"schema": map[string]interface{}{
							"type":     "object",
							"required": []string{"latitude", "longitude"},
							"properties": map[string]interface{}{
								"latitude":  map[string]interface{}{"type": "number", "description": i18n.Translate(lang, "swagger.parameters.latitude")},
								"longitude": map[string]interface{}{"type": "number", "description": i18n.Translate(lang, "swagger.parameters.longitude")},
							},
						},
					},
				},
			}
		}
		item[methodName] = operation
		return nil
	})
	return paths
}

func isInternalRoutePath(route string) bool {
	return strings.Contains(route, "/metrics") || strings.HasPrefix(route, "/debug/") || strings.Contains(route, "/server/tor/")
}

func openAPIEndpointKey(route, apiPrefix string) string {
	path := strings.TrimPrefix(route, apiPrefix)
	if path == "" {
		path = "/"
	}
	switch {
	case path == "/" || path == "/api":
		return "api_info"
	case strings.HasSuffix(path, "/server/auth/check"):
		return "auth_check"
	case strings.HasSuffix(path, "/server/healthz") || path == "/healthz":
		return "health_check"
	case path == "/cities/search":
		return "cities_search"
	case path == "/cities/country/{code}":
		return "cities_country"
	case path == "/cities/coordinates":
		return "cities_coordinates"
	case path == "/cities/nearby":
		return "cities_nearby"
	case path == "/cities/{id}":
		return "city_get"
	case path == "/cities":
		return "cities_list"
	case path == "/zipcodes/search":
		return "zip_search"
	case path == "/zipcodes/state/{state}":
		return "zip_state"
	case path == "/zipcodes/nearby":
		return "zip_nearby"
	case path == "/zipcodes/{zip}":
		return "zip_get"
	case path == "/stats":
		return "stats"
	case path == "/stats.txt":
		return "stats_text"
	case path == "/count":
		return "count"
	case path == "/count.txt":
		return "count_text"
	case path == "/random":
		return "random"
	case path == "/random.txt":
		return "random_text"
	case strings.HasSuffix(path, "/server/graphql") || path == "/graphql" || path == "/api/graphql":
		return "graphql"
	default:
		return "generic"
	}
}

func openAPIParameters(route, methodName, apiPrefix, lang string) []map[string]interface{} {
	path := strings.TrimPrefix(route, apiPrefix)
	params := make([]map[string]interface{}, 0)
	for _, segment := range strings.Split(path, "/") {
		if !strings.HasPrefix(segment, "{") || !strings.HasSuffix(segment, "}") {
			continue
		}
		name := strings.Trim(segment, "{}")
		params = append(params, map[string]interface{}{
			"in": "path", "name": name, "required": true,
			"description": i18n.Translate(lang, "swagger.parameters."+name),
			"schema":      map[string]string{"type": "string"},
		})
	}
	queryParams := map[string][]string{
		"/cities":                 {"page", "limit"},
		"/cities/search":          {"q", "country", "limit"},
		"/cities/country/{code}":  {"limit"},
		"/cities/coordinates":     {"latitude", "longitude"},
		"/cities/nearby":          {"lat", "lon", "radius", "limit"},
		"/zipcodes/search":        {"q", "state", "limit"},
		"/zipcodes/nearby":        {"lat", "lon", "radius", "limit"},
		"/zipcodes/state/{state}": {"limit"},
	}
	if methodName == "get" && (strings.HasSuffix(path, "/server/graphql") || path == "/api/graphql") {
		for _, name := range []string{"query", "variables", "operationName"} {
			params = append(params, map[string]interface{}{
				"in": "query", "name": name, "required": false,
				"description": "GraphQL query parameter",
				"schema":      map[string]string{"type": "string"},
			})
		}
		return params
	}
	if methodName == "post" && path == "/cities/coordinates" {
		return params
	}
	for _, name := range queryParams[path] {
		key := name
		switch name {
		case "q":
			key = "query"
		case "lat":
			key = "latitude"
		case "lon":
			key = "longitude"
		case "country":
			key = "code"
		case "state":
			key = "state"
		}
		typeName := "string"
		if name == "page" || name == "limit" {
			typeName = "integer"
		}
		params = append(params, map[string]interface{}{
			"in": "query", "name": name, "required": false,
			"description": i18n.Translate(lang, "swagger.parameters."+key),
			"schema":      map[string]string{"type": typeName},
		})
	}
	return params
}

func openAPIErrorResponses(lang string) map[string]interface{} {
	definitions := map[string]struct{ code, key string }{
		"400": {"BAD_REQUEST", "400"}, "401": {"UNAUTHORIZED", "401"},
		"403": {"FORBIDDEN", "403"}, "404": {"NOT_FOUND", "404"},
		"405": {"METHOD_NOT_ALLOWED", "405"}, "429": {"RATE_LIMITED", "429"},
		"500": {"SERVER_ERROR", "500"}, "502": {"UNAVAILABLE", "502"},
		"503": {"UNAVAILABLE", "503"},
	}
	responses := make(map[string]interface{}, len(definitions))
	for status, definition := range definitions {
		message := i18n.Translate(lang, "swagger.errors."+definition.key)
		responses[status] = map[string]interface{}{
			"description": message,
			"content": map[string]interface{}{
				"application/json": map[string]interface{}{
					"schema":  map[string]string{"$ref": "#/components/schemas/ErrorResponse"},
					"example": map[string]interface{}{"ok": false, "error": definition.code, "message": message},
				},
			},
		}
	}
	return responses
}
