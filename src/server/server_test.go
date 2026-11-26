package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/citylist/src/cities"
	"github.com/apimgr/citylist/src/common/i18n"
	"github.com/apimgr/citylist/src/common/theme"
	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/database"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	return testServerWithConfig(t, config.DefaultConfig())
}

func testServerWithConfig(t *testing.T, cfg *config.Config) *Server {
	t.Helper()

	jsonData := []byte(`[
		{"id":1,"name":"London","country":"GB","coord":{"lon":-0.1257,"lat":51.5085}},
		{"id":2,"name":"Paris","country":"FR","coord":{"lon":2.3522,"lat":48.8566}},
		{"id":3,"name":"Berlin","country":"DE","coord":{"lon":13.405,"lat":52.52}}
	]`)

	citySvc, err := cities.NewService(jsonData)
	if err != nil {
		t.Fatalf("cities.NewService() error: %v", err)
	}

	store, err := database.OpenSQLite(filepath.Join(t.TempDir(), "server.db"), database.DefaultPoolConfig())
	if err != nil {
		t.Fatalf("database.OpenSQLite() error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("EnsureSchema() error: %v", err)
	}
	if err := InitializeInstallationSecret(context.Background(), store); err != nil {
		t.Fatalf("InitializeInstallationSecret() error: %v", err)
	}
	return NewWithDatabase(citySvc, nil, cfg, "127.0.0.1", "8080", "1.0.0-test", "2026-01-01", "abc123", store)
}

func TestTLSListenerSelection(t *testing.T) {
	tests := []struct {
		name  string
		ports string
		ssl   bool
		port  int
		want  bool
	}{
		{name: "single HTTP", ports: "8080", port: 8080},
		{name: "single HTTPS default port", ports: "443", port: 443, want: true},
		{name: "dual HTTP", ports: "8080,8443", port: 8080},
		{name: "dual HTTPS", ports: "8080,8443", port: 8443, want: true},
		{name: "ssl override", ports: "8080", ssl: true, port: 8080, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(tt.port)))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := listener.Close(); err != nil {
					t.Errorf("close listener: %v", err)
				}
			}()
			if got := shouldServeTLS(listener, tt.ports, tt.ssl); got != tt.want {
				t.Errorf("shouldServeTLS() = %t, want %t", got, tt.want)
			}
		})
	}
	if !tlsConfigured("8080,8443", false) || !tlsConfigured("443", false) || !tlsConfigured("8080", true) || tlsConfigured("8080", false) {
		t.Fatal("tlsConfigured() did not follow single, dual, and override port modes")
	}
}

func doRequest(t *testing.T, s *Server, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if strings.HasPrefix(path, "/api/") || path == "/server/healthz" {
		req.Header.Set("Accept", "application/json")
	} else {
		req.Header.Set("Accept", "text/html")
	}
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	return rec
}

func TestErrorPageUsesSharedPageChrome(t *testing.T) {
	rec := doRequest(t, testServer(t), http.MethodGet, "/not-a-page", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /not-a-page = %d, want 404", rec.Code)
	}
	body := rec.Body.String()
	for _, required := range []string{
		`href="#main-content"`, `href="#navigation"`, `id="navigation"`, `class="page-title"`,
		`class="section-card"`, `class="footer"`, `class="theme-toggle-form"`,
		`class="cookie-banner`, `Skip to main content`, `Skip to navigation`,
	} {
		if !strings.Contains(body, required) {
			t.Errorf("error page is missing shared page element %q", required)
		}
	}
	if got := strings.Count(body, "<main "); got != 1 {
		t.Errorf("error page contains %d main landmarks, want 1", got)
	}
	if got := strings.Count(body, "<h1"); got != 1 {
		t.Errorf("error page contains %d h1 elements, want 1", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("error page Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("ETag"); got != `"`+AssetStamp()+`"` {
		t.Errorf("error page ETag = %q", got)
	}
	if got := rec.Header().Get("Clear-Site-Data"); got != "" {
		t.Errorf("error page must not purge site data, got %q", got)
	}
}

func TestApplyConfigRebuildsRoutesAndRetainsRestartOnlySettings(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Port = "8080"
	cfg.Server.Address = "127.0.0.1"
	cfg.Server.Database.URL = "file:original.db"
	cfg.Server.Tor.Binary = "original-tor"
	cfg.Server.SSL.Enabled = false
	s := testServerWithConfig(t, cfg)

	next := config.DefaultConfig()
	next.Server.Port = "9090"
	next.Server.Address = "192.0.2.10"
	next.Server.Database.URL = "file:replacement.db"
	next.Server.Tor.Binary = "replacement-tor"
	next.Server.SSL.Enabled = true
	next.Server.Branding.Title = "Reloaded Citylist"
	if err := s.ApplyConfig(next, []string{"server.port", "server.address", "server.database", "server.tor", "server.ssl"}, "/citylist"); err != nil {
		t.Fatalf("ApplyConfig() error: %v", err)
	}
	if s.cfg.Server.Port != "8080" || s.cfg.Server.Address != "127.0.0.1" || s.cfg.Server.Database.URL != "file:original.db" || s.cfg.Server.Tor.Binary != "original-tor" || s.cfg.Server.SSL.Enabled {
		t.Fatalf("restart-only settings changed immediately: %+v", s.cfg.Server)
	}
	if s.cfg.Server.Branding.Title != "Reloaded Citylist" || s.baseURL != "/citylist" {
		t.Fatalf("reloadable settings were not applied: title=%q baseurl=%q", s.cfg.Server.Branding.Title, s.baseURL)
	}
	if got := doRequest(t, s, http.MethodGet, "/citylist/server/healthz", nil).Code; got != http.StatusOK {
		t.Fatalf("GET /citylist/server/healthz = %d, want 200", got)
	}
	if got := doRequest(t, s, http.MethodGet, "/server/healthz", nil).Code; got != http.StatusNotFound {
		t.Fatalf("GET /server/healthz after prefix reload = %d, want 404", got)
	}
}

func TestRegisteredHTMLPagesUseSharedAccessibleHeader(t *testing.T) {
	s := testServer(t)
	paths := []string{
		"/", "/search", "/coordinates", "/server/about", "/server/help", "/server/help/mcp",
		"/server/privacy", "/server/dpo", "/server/terms", "/server/security", "/server/security/policy",
		"/server/security/thanks", "/server/docs/swagger", "/server/docs/graphql", "/server/preferences",
		"/server/preferences/tokens", "/server/preferences/sync", "/server/contact", "/server/healthz",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("Accept", "text/html")
			response := httptest.NewRecorder()
			s.Router().ServeHTTP(response, request)
			if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("GET %s = %d %q", path, response.Code, response.Header().Get("Content-Type"))
			}
			body := response.Body.String()
			firstSkip := strings.Index(body, `href="#main-content"`)
			secondSkip := strings.Index(body, `href="#navigation"`)
			banner := strings.Index(body, `class="site-banner`)
			checkbox := strings.Index(body, `id="nav-toggle"`)
			header := strings.Index(body, `<header class="header">`)
			if firstSkip < 0 || secondSkip <= firstSkip || checkbox <= secondSkip || header <= checkbox || banner >= 0 && banner <= secondSkip {
				t.Fatalf("page does not place both skip links before banners and header: %s", body[:min(len(body), 1400)])
			}
			if !strings.Contains(body, `id="navigation" class="nav-links"`) || !strings.Contains(body, `class="nav-toggle"`) ||
				!strings.Contains(body, `class="header-actions"`) || !strings.Contains(body, `class="nav-panel"`) {
				t.Fatalf("page is missing shared navigation markup")
			}
		})
	}
}

func TestAllRequiredHTTPErrorStatusesRenderThemedPages(t *testing.T) {
	s := testServer(t)
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/error-test", nil)
			req.Header.Set("Accept", "text/html")
			rec := httptest.NewRecorder()
			s.renderError(rec, req, status, "sensitive-stack-trace")
			if rec.Code != status {
				t.Fatalf("error response = %d, want %d", rec.Code, status)
			}
			body := rec.Body.String()
			if !strings.Contains(body, "class=\"page-title\"") || !strings.Contains(body, "class=\"footer\"") {
				t.Fatal("error response is missing shared themed page chrome")
			}
			if strings.Contains(body, "sensitive-stack-trace") {
				t.Fatal("error response exposed internal details")
			}
		})
	}
}

func TestJSONErrorPageUsesCanonicalEnvelope(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/error-test", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	testServer(t).renderError(rec, req, http.StatusNotFound, "sensitive-stack-trace")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("error response = %d, want %d", rec.Code, http.StatusNotFound)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body["ok"] != false || body["error"] != "NOT_FOUND" || body["message"] == "" {
		t.Fatalf("error response does not use the canonical envelope: %#v", body)
	}
	if _, exists := body["status"]; exists {
		t.Fatalf("error response duplicates HTTP status in body: %#v", body)
	}
	if strings.Contains(rec.Body.String(), "sensitive-stack-trace") {
		t.Fatal("error response exposed internal details")
	}
}

func TestDirectHTMLPageGetsBuildCacheHeaders(t *testing.T) {
	rec := doRequest(t, testServer(t), http.MethodGet, "/server/about", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /server/about = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("about page Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("ETag"); got != `"`+AssetStamp()+`"` {
		t.Errorf("about page ETag = %q", got)
	}
	found := false
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "citylist_build" && cookie.Value == AssetStamp() && cookie.Secure && cookie.SameSite == http.SameSiteLaxMode {
			found = true
		}
	}
	if !found {
		t.Error("about page did not set the build-stamp cookie")
	}
}

func TestGraphQLExplorerProvidesNoJavaScriptQueryPath(t *testing.T) {
	s := testServer(t)
	page := doRequest(t, s, http.MethodGet, "/server/docs/graphql", nil)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /server/docs/graphql = %d, want 200", page.Code)
	}
	for _, required := range []string{
		`class="page-title"`, `class="section-card"`, `id="graphiql"`,
		`data-endpoint="/api/v1/server/graphql"`, `<noscript>`,
		`href="/api/v1/server/graphql"`,
		`/static/vendor/graphiql/react.production.min.js`,
		`/static/vendor/graphiql/react-dom.production.min.js`,
		`/static/vendor/graphiql/graphiql.umd.js`,
		`/static/js/docs-graphql.js`,
	} {
		if !strings.Contains(page.Body.String(), required) {
			t.Errorf("GraphQL explorer is missing %q", required)
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/server/graphql", strings.NewReader("query=%7Bcities%28limit%3A1%29%7Bname%7D%7D"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	recorder := httptest.NewRecorder()
	s.Router().ServeHTTP(recorder, request)
	result := recorder
	if result.Code != http.StatusOK || !strings.Contains(result.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("form-encoded GraphQL POST = %d %q, want JSON 200", result.Code, result.Header().Get("Content-Type"))
	}
	if !strings.Contains(result.Body.String(), `"data"`) {
		t.Fatalf("form-encoded GraphQL query returned %q, want a data result", result.Body.String())
	}
	get := doRequest(t, s, http.MethodGet, "/api/v1/server/graphql?query="+url.QueryEscape(`{cities(limit:1){name}}`), nil)
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"London"`) {
		t.Errorf("GET GraphQL query = %d %q, want a London result", get.Code, get.Body.String())
	}
	variables := url.QueryEscape(`{"limit":1}`)
	variableQuery := url.QueryEscape(`query CityList($limit:Int){cities(limit:$limit){name}}`)
	variableResult := doRequest(t, s, http.MethodGet, "/api/graphql?query="+variableQuery+"&variables="+variables+"&operationName=CityList", nil)
	if variableResult.Code != http.StatusOK || !strings.Contains(variableResult.Body.String(), `"London"`) {
		t.Errorf("GET GraphQL query with variables = %d %q", variableResult.Code, variableResult.Body.String())
	}
	mutation := doRequest(t, s, http.MethodGet, "/api/v1/server/graphql?query="+url.QueryEscape(`mutation { cities(limit:1) { name } }`), nil)
	if mutation.Code != http.StatusOK || !strings.Contains(mutation.Body.String(), `"errors"`) {
		t.Errorf("GET GraphQL mutation = %d %q, want a non-executed GraphQL error", mutation.Code, mutation.Body.String())
	}
	usage := doRequest(t, s, http.MethodGet, "/api/v1/server/graphql", nil)
	if usage.Code != http.StatusOK || !strings.Contains(usage.Body.String(), "query parameter") {
		t.Errorf("GET GraphQL usage = %d %q", usage.Code, usage.Body.String())
	}
	invalid := doRequest(t, s, http.MethodGet, "/api/v1/server/graphql?unknown=value", nil)
	if invalid.Code != http.StatusBadRequest {
		t.Errorf("GET GraphQL unknown parameter = %d, want 400", invalid.Code)
	}
}

func TestCommonCSSIsRenderedFromTheGoThemePalette(t *testing.T) {
	s := testServer(t)
	response := doRequest(t, s, http.MethodGet, "/static/css/common.css?v="+AssetStamp(), nil)
	if response.Code != http.StatusOK || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("GET common.css = %d %q", response.Code, response.Header().Get("Content-Type"))
	}
	for _, color := range []string{
		theme.ThemePaletteDark.Background,
		theme.ThemePaletteDark.Foreground,
		theme.ThemePaletteDark.Muted,
		theme.ThemePaletteLight.Background,
		theme.ThemePaletteLight.Foreground,
		theme.ThemePaletteLight.Muted,
	} {
		if !strings.Contains(response.Body.String(), color) {
			t.Errorf("common.css is missing palette color %s", color)
		}
	}
	if strings.Contains(response.Body.String(), "__CITYLIST_") {
		t.Error("common.css response contains unresolved palette placeholders")
	}
	head := doRequest(t, s, http.MethodHead, "/static/css/common.css?v="+AssetStamp(), nil)
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Errorf("HEAD common.css = %d with %d body bytes", head.Code, head.Body.Len())
	}
}

func TestHealthz(t *testing.T) {
	s := testServer(t)
	for _, path := range []string{"/server/healthz", "/api/v1/server/healthz"} {
		rec := doRequest(t, s, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode GET %s health: %v", path, err)
		}
		for _, key := range []string{"project", "status", "version", "go_version", "build", "uptime", "mode", "timestamp", "cluster", "features", "checks", "stats"} {
			if _, ok := payload[key]; !ok {
				t.Errorf("GET %s health response omitted %q", path, key)
			}
		}
		var features map[string]map[string]interface{}
		if err := json.Unmarshal(payload["features"], &features); err != nil {
			t.Fatalf("decode health features: %v", err)
		}
		for _, name := range []string{"tor", "i2p"} {
			if features[name]["status"] != "disabled" || features[name]["enabled"] != false || features[name]["running"] != false {
				t.Errorf("default %s health feature = %#v, want disabled", name, features[name])
			}
		}
	}
	if rec := doRequest(t, s, http.MethodGet, "/healthz", nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET /healthz = %d, want 404 while root alias is disabled", rec.Code)
	}
}

func TestAutoDiscoverPublishesServerAndCLIRequirements(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Branding.Title = "Citylist API"
	cfg.Server.FQDN = "cities.example.test"
	cfg.Server.Update.CLIMinVersion = "1.2.3"
	s := testServerWithConfig(t, cfg)
	rec := doRequest(t, s, http.MethodGet, "/api/autodiscover", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/autodiscover = %d, want 200", rec.Code)
	}
	var response autoDiscoverResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode autodiscover response: %v", err)
	}
	if response.Server.Name != "Citylist API" || response.Server.Version != "1.0.0-test" || response.Server.APIVersion != cfg.Server.APIVersion || response.Server.Domain != cfg.Server.FQDN {
		t.Fatalf("autodiscover server info = %+v", response.Server)
	}
	if response.CLIMinVersion != "1.2.3" || response.CLIVersions == nil || len(response.CLIVersions) != 0 {
		t.Fatalf("autodiscover CLI information = min %q versions %#v", response.CLIMinVersion, response.CLIVersions)
	}
}

func TestGPCOptOutIsAuditedAndShownOnPrivacyPages(t *testing.T) {
	s := testServer(t)
	for _, path := range []string{"/server/privacy", "/server/preferences"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept", "text/html")
		req.Header.Set("Sec-GPC", "1")
		rec := httptest.NewRecorder()
		s.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Global Privacy Control (GPC)") {
			t.Fatalf("GET %s with GPC = %d, notice missing from response", path, rec.Code)
		}
		for _, cookie := range rec.Result().Cookies() {
			if cookie.Name == "theme" || cookie.Name == "lang" || cookie.Name == "cookie_consent" {
				t.Errorf("GPC response set non-essential cookie %q", cookie.Name)
			}
		}
	}
	var count int
	if err := s.store.Read(context.Background(), func(ctx context.Context, reader database.Reader) error {
		return reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE action = 'compliance.gpc_honored'`).Scan(&count)
	}); err != nil {
		t.Fatalf("read GPC audit events: %v", err)
	}
	if count != 2 {
		t.Fatalf("GPC audit events = %d, want one for each honored request", count)
	}
}

func TestGPCOptOutCanBeDisabledByConfiguration(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Web.Headers.HonorSecGPC = false
	s := testServerWithConfig(t, cfg)
	req := httptest.NewRequest(http.MethodGet, "/server/privacy", nil)
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Sec-GPC", "1")
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "Global Privacy Control (GPC)") {
		t.Fatal("disabled GPC handling still showed an opt-out notice")
	}
	var count int
	if err := s.store.Read(context.Background(), func(ctx context.Context, reader database.Reader) error {
		return reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE action = 'compliance.gpc_honored'`).Scan(&count)
	}); err != nil {
		t.Fatalf("read GPC audit events: %v", err)
	}
	if count != 0 {
		t.Fatalf("GPC audit events = %d, want 0 when disabled", count)
	}
}

func TestDNTOptOutHonorsConfigurationWithoutChangingCCPAChoice(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Web.Headers.HonorDNT = true
	cfg.Server.Privacy.Data.Sold = true
	s := testServerWithConfig(t, cfg)
	for _, path := range []string{"/server/privacy", "/server/preferences"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept", "text/html")
		req.Header.Set("DNT", "1")
		rec := httptest.NewRecorder()
		s.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Do Not Track (DNT)") || strings.Contains(rec.Body.String(), "Global Privacy Control (GPC)") {
			t.Fatalf("GET %s with DNT = %d, DNT notice missing or incorrect", path, rec.Code)
		}
		for _, cookie := range rec.Result().Cookies() {
			if cookie.Name == "theme" || cookie.Name == "lang" || cookie.Name == "cookie_consent" {
				t.Errorf("DNT response set non-essential cookie %q", cookie.Name)
			}
		}
	}
	var count int
	if err := s.store.Read(context.Background(), func(ctx context.Context, reader database.Reader) error {
		return reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE action = 'compliance.dnt_honored' AND details LIKE '%"signal":"dnt"%'`).Scan(&count)
	}); err != nil {
		t.Fatalf("read DNT audit events: %v", err)
	}
	if count != 2 {
		t.Fatalf("DNT audit events = %d, want one for each honored request", count)
	}
	req := httptest.NewRequest(http.MethodPost, "/server/ccpa", strings.NewReader("choice=opt-out"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("DNT", "1")
	rec := httptest.NewRecorder()
	s.gpcMiddleware(http.HandlerFunc(s.handleCCPAPost)).ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("DNT CCPA choice status = %d, want 303", rec.Code)
	}
	foundCCPAChoice := false
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "ccpa_opt_out" && cookie.Value == "true" {
			foundCCPAChoice = true
		}
	}
	if !foundCCPAChoice {
		t.Fatal("DNT signal incorrectly blocked the explicit CCPA choice")
	}
}

func TestGPCOptOutDoesNotPersistConsentOrCCPAChoices(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Privacy.Data.Sold = true
	s := testServerWithConfig(t, cfg)
	for _, test := range []struct {
		name    string
		handler http.HandlerFunc
		body    string
	}{
		{name: "consent", handler: s.handleConsentPost, body: "choice=accept"},
		{name: "preferences", handler: s.handlePreferencesUpdate, body: "consent_form=1&consent_preferences=true&consent_analytics=true&ccpa_opt_out=true&theme=dark&lang=es"},
		{name: "CCPA", handler: s.handleCCPAPost, body: "choice=opt-in"},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Sec-GPC", "1")
			rec := httptest.NewRecorder()
			s.gpcMiddleware(test.handler).ServeHTTP(rec, req)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("GPC POST status = %d, want 303", rec.Code)
			}
			for _, cookie := range rec.Result().Cookies() {
				switch cookie.Name {
				case "cookie_consent", "ccpa_opt_out", "theme", "lang":
					t.Errorf("GPC POST set non-essential cookie %q", cookie.Name)
				}
			}
		})
	}
}

func TestHealthTextUsesFrontendAndMachineLayouts(t *testing.T) {
	s := testServer(t)
	for _, test := range []struct {
		path    string
		want    string
		mustNot string
	}{
		{path: "/server/healthz", want: "citylist -", mustNot: "project.name:"},
		{path: "/api/v1/server/healthz", want: "project.name: citylist", mustNot: "citylist -"},
	} {
		req := httptest.NewRequest(http.MethodGet, test.path, nil)
		req.Header.Set("Accept", "text/plain")
		rec := httptest.NewRecorder()
		s.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), test.want) || strings.Contains(rec.Body.String(), test.mustNot) {
			t.Errorf("health text %s = %q, status %d", test.path, rec.Body.String(), rec.Code)
		}
	}
}

func TestHealthFormatHelpers(t *testing.T) {
	for _, test := range []struct {
		duration time.Duration
		want     string
	}{{45 * time.Second, "45s"}, {24 * time.Minute, "24m"}, {2*time.Hour + 16*time.Minute, "2h 16m"}, {25*time.Hour + 4*time.Minute, "1d 1h"}} {
		if got := compactUptime(test.duration); got != test.want {
			t.Errorf("compactUptime(%s) = %q, want %q", test.duration, got, test.want)
		}
	}
	if got := formatHealthCount(1234567); got != "1,234,567" {
		t.Fatalf("formatHealthCount() = %q", got)
	}
}

func TestMetricsRoutesRequireServiceTokensAndShareHandlers(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Metrics.Auth.Tokens = config.MetricsServiceTokens{
		Prometheus: "mtr_prometheus_token_0123456789abcdef",
		Grafana:    "mtr_grafana_token_0123456789abcdef",
		Loki:       "mtr_loki_token_0123456789abcdef",
	}
	s := testServerWithConfig(t, cfg)

	for _, path := range []string{"/server/metrics", "/server/metrics/prometheus", "/api/v1/server/metrics", "/api/v1/server/metrics/prometheus", "/api/metrics", "/api/metrics/prometheus", "/metrics", "/metrics/prometheus"} {
		if got := doRequest(t, s, http.MethodGet, path, nil); got.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without token = %d, want 401", path, got.Code)
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+cfg.Server.Metrics.Auth.Tokens.Prometheus)
		rec := httptest.NewRecorder()
		s.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/plain") || !strings.Contains(rec.Body.String(), "citylist_http_requests_total") {
			t.Errorf("GET %s with token = %d, content type %q; missing Prometheus exposition", path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}

	for _, service := range []struct {
		name, contentType, token string
	}{
		{"grafana", "application/json", cfg.Server.Metrics.Auth.Tokens.Grafana},
		{"loki", "application/json", cfg.Server.Metrics.Auth.Tokens.Loki},
	} {
		path := "/server/metrics/" + service.name
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+service.token)
		rec := httptest.NewRecorder()
		s.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), service.contentType) {
			t.Errorf("GET %s = %d, content type %q", path, rec.Code, rec.Header().Get("Content-Type"))
		}
		if service.name == "grafana" && (!strings.Contains(rec.Body.String(), "schemaVersion") || !strings.Contains(rec.Body.String(), "citylist_db_connections_open")) {
			t.Error("Grafana dashboard is missing its schema version or database panels")
		}
		if service.name == "loki" && !strings.Contains(rec.Body.String(), `"streams"`) {
			t.Error("Loki response is missing streams")
		}
	}

	queryToken := httptest.NewRequest(http.MethodGet, "/server/metrics?token="+cfg.Server.Metrics.Auth.Tokens.Prometheus, nil)
	queryRec := httptest.NewRecorder()
	s.Router().ServeHTTP(queryRec, queryToken)
	if queryRec.Code != http.StatusUnauthorized {
		t.Errorf("query-string token status = %d, want 401", queryRec.Code)
	}
}

func TestMetricsRootAliasCanBeDisabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Metrics.Root.Enabled = false
	s := testServerWithConfig(t, cfg)
	if got := doRequest(t, s, http.MethodGet, "/metrics", nil); got.Code != http.StatusNotFound {
		t.Errorf("GET /metrics with root alias disabled = %d, want 404", got.Code)
	}
}

func TestLocalizedServerPagesAndLocaleAssets(t *testing.T) {
	s := testServer(t)
	page := doRequest(t, s, http.MethodGet, "/search?lang=fr&q=Paris", nil)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /search?lang=fr = %d, want 200", page.Code)
	}
	if page.Header().Get("Content-Language") != "fr" || !strings.Contains(page.Body.String(), `<html lang="fr" dir="ltr"`) {
		t.Fatalf("French page language metadata missing: Content-Language=%q", page.Header().Get("Content-Language"))
	}
	if !strings.Contains(page.Body.String(), "Rechercher des villes") {
		t.Fatal("French search heading was not rendered")
	}
	if !hasCookieValue(page.Result().Cookies(), "lang", "fr") {
		t.Fatal("one-shot lang query did not persist the selected language")
	}

	asset := doRequest(t, s, http.MethodGet, "/locales/ar.json", nil)
	if asset.Code != http.StatusOK || !strings.Contains(asset.Body.String(), `"language": "ar"`) {
		t.Fatalf("GET /locales/ar.json = %d, response does not contain Arabic metadata", asset.Code)
	}
	if got := asset.Header().Get("Cache-Control"); got != "no-cache" || asset.Header().Get("ETag") == "" {
		t.Errorf("unstamped locale cache headers: Cache-Control=%q ETag=%q", got, asset.Header().Get("ETag"))
	}
	stamped := doRequest(t, s, http.MethodGet, "/locales/ar.json?v="+AssetStamp(), nil)
	if got := stamped.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("stamped locale Cache-Control = %q", got)
	}
	conditionalRequest := httptest.NewRequest(http.MethodGet, "/locales/ar.json", nil)
	conditionalRequest.Header.Set("If-None-Match", asset.Header().Get("ETag"))
	conditionalResponse := httptest.NewRecorder()
	s.Router().ServeHTTP(conditionalResponse, conditionalRequest)
	if conditionalResponse.Code != http.StatusNotModified {
		t.Errorf("conditional locale GET = %d, want 304", conditionalResponse.Code)
	}
	if missing := doRequest(t, s, http.MethodGet, "/locales/xx.json", nil); missing.Code != http.StatusNotFound {
		t.Fatalf("GET /locales/xx.json = %d, want 404", missing.Code)
	}
}

func TestAPIErrorMessageUsesResolvedLanguage(t *testing.T) {
	s := testServer(t)
	response := doRequest(t, s, http.MethodGet, "/api/v1/cities?lang=es&limit=invalid", nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("GET invalid city limit = %d, want 400", response.Code)
	}
	if response.Header().Get("Content-Language") != "es" || !strings.Contains(response.Body.String(), "La solicitud no es válida.") {
		t.Fatalf("Spanish error response is incomplete: %s", response.Body.String())
	}
}

func TestPlainTextAPIResponsesUseResolvedLanguage(t *testing.T) {
	s := testServer(t)
	for _, test := range []struct {
		path string
		want []string
	}{
		{path: "/api/v1/stats.txt?lang=es", want: []string{"Estadísticas de la API", "Ciudades totales:", "Países totales:", "Fuente de datos:"}},
		{path: "/api/v1/random.txt?lang=fr", want: []string{"Ville:", "Pays:", "Latitude:", "Longitude:"}},
	} {
		response := doRequest(t, s, http.MethodGet, test.path, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", test.path, response.Code)
		}
		for _, text := range test.want {
			if !strings.Contains(response.Body.String(), text) {
				t.Errorf("GET %s response is missing translated label %q: %s", test.path, text, response.Body.String())
			}
		}
	}
}

func TestPreferencesCSRFAndLanguageSelection(t *testing.T) {
	s := testServer(t)
	page := doRequest(t, s, http.MethodGet, "/server/preferences", nil)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /server/preferences = %d, want 200", page.Code)
	}
	var csrf *http.Cookie
	for _, cookie := range page.Result().Cookies() {
		if cookie.Name == csrfCookieName {
			csrf = cookie
		}
	}
	if csrf == nil || !validCSRFToken(csrf.Value) || !strings.Contains(page.Body.String(), csrf.Value) {
		t.Fatal("preferences page did not issue a matching CSRF cookie and form token")
	}
	csrfSecret, err := s.projectSecret(context.Background(), "csrf_token_secret")
	if err != nil || !verifyCSRFToken(csrfSecret, csrf.Value) {
		t.Fatalf("CSRF token signature is invalid: %v", err)
	}
	mutatedToken := "A" + csrf.Value[1:]
	if strings.HasPrefix(csrf.Value, "A") {
		mutatedToken = "B" + csrf.Value[1:]
	}
	if verifyCSRFToken(csrfSecret, mutatedToken) {
		t.Fatal("CSRF token accepted a modified signature")
	}
	missing := httptest.NewRequest(http.MethodPost, "/server/preferences", strings.NewReader("lang=fr"))
	missing.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	missingResponse := httptest.NewRecorder()
	s.Router().ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusForbidden {
		t.Fatalf("POST without CSRF token = %d, want 403", missingResponse.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/server/preferences", strings.NewReader("csrf_token="+csrf.Value+"&lang=fr&theme=light&return_to=%2Fserver%2Fpreferences"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(csrf)
	response := httptest.NewRecorder()
	s.Router().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("valid preferences POST = %d, want 303", response.Code)
	}
	cookies := response.Result().Cookies()
	if !hasCookieValue(cookies, "lang", "fr") || !hasCookieValue(cookies, "theme", "light") {
		t.Fatalf("preferences POST did not set selected cookies: %v", cookies)
	}
}

func hasCookieValue(cookies []*http.Cookie, name, value string) bool {
	for _, cookie := range cookies {
		if cookie.Name == name && cookie.Value == value {
			return true
		}
	}
	return false
}

func TestAuthCheck(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Token = "adm_0123456789ABCDEFGHIJKLMNOPQRSTUV"
	s := testServerWithConfig(t, cfg)
	for _, test := range []struct {
		name          string
		authorization string
		status        int
	}{
		{name: "missing token", status: http.StatusUnauthorized},
		{name: "wrong token", authorization: "Bearer adm_00000000000000000000000000000000", status: http.StatusUnauthorized},
		{name: "malformed scheme", authorization: "Basic " + cfg.Server.Token, status: http.StatusUnauthorized},
		{name: "operator token", authorization: "Bearer " + cfg.Server.Token, status: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/server/auth/check", nil)
			req.Header.Set("Accept", "application/json")
			if test.authorization != "" {
				req.Header.Set("Authorization", test.authorization)
			}
			rec := httptest.NewRecorder()
			s.Router().ServeHTTP(rec, req)
			if rec.Code != test.status {
				t.Fatalf("GET /api/v1/server/auth/check = %d, want %d", rec.Code, test.status)
			}
			if test.status == http.StatusOK && !strings.Contains(rec.Body.String(), `"kind": "operator"`) {
				t.Fatalf("valid token response does not identify operator kind: %s", rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), cfg.Server.Token) {
				t.Fatal("auth check response exposed the bearer token")
			}
		})
	}
}

func TestHandleAPIInfo(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/api/v1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1 = %d, want 200", rec.Code)
	}

	var resp APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Success {
		t.Error("expected Success=true")
	}
}

func TestMCPRoutes(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Token = "adm_0123456789ABCDEFGHIJKLMNOPQRSTUV"
	s := testServerWithConfig(t, cfg)
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"},"params":{}}`)
	for _, endpoint := range []string{"/api/v1/server/mcp", "/api/mcp", "/mcp"} {
		req := httptest.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+cfg.Server.Token)
		req.Header.Set("MCP-Protocol-Version", supportedMCPVersion)
		req.Header.Set("Mcp-Method", "tools/list")
		rec := httptest.NewRecorder()
		s.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("POST %s = %d, want 200: %s", endpoint, rec.Code, rec.Body.String())
			continue
		}
		var response map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Errorf("POST %s returned invalid JSON: %v", endpoint, err)
			continue
		}
		if response["jsonrpc"] != "2.0" || response["id"] != float64(1) {
			t.Errorf("POST %s returned unexpected JSON-RPC envelope: %#v", endpoint, response)
		}
		result := response["result"].(map[string]interface{})
		tools := result["tools"].([]interface{})
		if len(tools) == 0 || tools[0].(map[string]interface{})["annotations"] == nil {
			t.Errorf("POST %s returned tools without method annotations", endpoint)
		}
	}
}

func TestMCPRejectsHeaderMismatchAndNonPost(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Token = "adm_0123456789ABCDEFGHIJKLMNOPQRSTUV"
	s := testServerWithConfig(t, cfg)
	request := func(method string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/mcp", bytes.NewReader(body))
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+cfg.Server.Token)
		req.Header.Set("MCP-Protocol-Version", supportedMCPVersion)
		req.Header.Set("Mcp-Method", "tools/list")
		req.Header.Set("Mcp-Method", "tools/list")
		rec := httptest.NewRecorder()
		s.Router().ServeHTTP(rec, req)
		return rec
	}
	if got := request(http.MethodGet, nil); got.Code != http.StatusMethodNotAllowed || !strings.Contains(got.Header().Get("Content-Type"), "application/json") {
		t.Errorf("GET /mcp = %d (%s), want JSON 405", got.Code, got.Header().Get("Content-Type"))
	}
	got := request(http.MethodPost, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping","_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}`))
	var mismatch map[string]interface{}
	if err := json.Unmarshal(got.Body.Bytes(), &mismatch); err != nil {
		t.Fatalf("Mcp-Method mismatch returned invalid JSON: %v", err)
	}
	mismatchError, _ := mismatch["error"].(map[string]interface{})
	if got.Code != http.StatusBadRequest || mismatchError["code"] != float64(-32020) {
		t.Errorf("Mcp-Method mismatch = %d %s, want JSON-RPC HeaderMismatch", got.Code, got.Body.String())
	}
}

func TestMCPRequiresBearerToken(t *testing.T) {
	s := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	var response map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("unauthenticated MCP request returned invalid JSON: %v", err)
	}
	errorBody, _ := response["error"].(map[string]interface{})
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != `Bearer realm="citylist"` || errorBody["message"] != i18n.Translate("en", "errors.unauthorized") {
		t.Fatalf("unauthenticated MCP request = %d, challenge=%q body=%s", rec.Code, rec.Header().Get("WWW-Authenticate"), rec.Body.String())
	}
}

func TestMCPHelpPageAndJSONMirror(t *testing.T) {
	s := testServer(t)
	projectContent := "### Citylist lookup tools\n\nUse `search_cities` to find city names."
	s.cfg.Server.Pages.Help.MCP = projectContent
	for endpoint, accept := range map[string]string{"/server/help/mcp": "text/html", "/api/v1/server/help/mcp": "application/json"} {
		req := httptest.NewRequest(http.MethodGet, endpoint, nil)
		req.Header.Set("Accept", accept)
		rec := httptest.NewRecorder()
		s.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", endpoint, rec.Code)
		}
		if endpoint == "/server/help/mcp" {
			body := rec.Body.String()
			if !strings.Contains(body, "canonical endpoint") {
				t.Errorf("MCP help page omitted endpoint details")
			}
			if !strings.Contains(body, "Mcp-Method: tools/list") || !strings.Contains(body, "mcpServers") {
				t.Errorf("MCP help page omitted tools/list or generic client setup")
			}
			if strings.Contains(body, `"method":"initialize"`) {
				t.Errorf("MCP help page still documents an initialize handshake")
			}
			if !strings.Contains(body, "Citylist lookup tools") {
				t.Errorf("MCP help page omitted configured project Markdown")
			}
		}
		if endpoint == "/api/v1/server/help/mcp" {
			var response APIResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode MCP help response: %v", err)
			}
			data, ok := response.Data.(map[string]interface{})
			if !ok || data["project"] != projectContent {
				t.Errorf("MCP help API project content = %#v, want configured Markdown", data["project"])
			}
			if !ok || !strings.Contains(rec.Body.String(), `"tools"`) {
				t.Errorf("MCP help API omitted tools")
			}
			if auth, ok := data["auth"].(map[string]interface{}); !ok || len(auth) != 1 || auth["scheme"] != "Bearer" {
				t.Errorf("MCP help API auth = %#v, want only the Bearer scheme", data["auth"])
			}
		}
	}
}

func TestMCPAcceptAndOriginValidation(t *testing.T) {
	if acceptsMCP("application/json;q=0, text/event-stream") {
		t.Fatal("acceptsMCP accepted application/json with q=0")
	}
	if !acceptsMCP("application/json;q=0.9, text/event-stream;q=0.5") {
		t.Fatal("acceptsMCP rejected two acceptable media types")
	}
	s := testServer(t)
	request := httptest.NewRequest(http.MethodPost, "http://example.com:8080/mcp", nil)
	request.Header.Set("Origin", "http://example.com:8080")
	if !s.validMCPOrigin(request) {
		t.Fatal("valid same-origin request was rejected")
	}
	request.Header.Set("Origin", "https://example.com:8080")
	if s.validMCPOrigin(request) {
		t.Fatal("cross-scheme origin was accepted")
	}
	request.Header.Set("Origin", "https://attacker.example")
	if s.validMCPOrigin(request) {
		t.Fatal("unconfigured foreign origin was accepted")
	}
	s.cfg.Server.CORS.AllowedOrigins = []string{"https://client.example"}
	request.Header.Set("Origin", "https://client.example")
	if !s.validMCPOrigin(request) {
		t.Fatal("explicit CORS origin was rejected")
	}
}

func TestHandleGetCities(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/api/v1/cities?page=1&limit=2", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/cities = %d, want 200", rec.Code)
	}

	var resp APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Count != 2 {
		t.Errorf("Count = %d, want 2", resp.Count)
	}
}

func TestHandleSearchCities(t *testing.T) {
	s := testServer(t)

	t.Run("valid query", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/search?q=lon", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("query too short", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/search?q=l", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("limit over maximum", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/search?q=lon&limit=101", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleGetCityByID(t *testing.T) {
	s := testServer(t)

	t.Run("found", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/1", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("not found", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/999", nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("invalid id", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/abc", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleGetCitiesByCountry(t *testing.T) {
	s := testServer(t)

	t.Run("valid code", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/country/GB", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("invalid code length", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/country/GBR", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleFindNearestGET(t *testing.T) {
	s := testServer(t)

	t.Run("valid coordinates", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/coordinates?latitude=51.5&longitude=-0.12", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("invalid latitude", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/coordinates?latitude=abc&longitude=0", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("invalid longitude", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/coordinates?latitude=0&longitude=abc", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("latitude out of range", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/coordinates?latitude=999&longitude=0", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("longitude out of range", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/coordinates?latitude=0&longitude=999", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleFindNearestPOST(t *testing.T) {
	s := testServer(t)

	t.Run("valid body", func(t *testing.T) {
		body := []byte(`{"latitude":51.5,"longitude":-0.12}`)
		rec := doRequest(t, s, http.MethodPost, "/api/v1/cities/coordinates", body)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodPost, "/api/v1/cities/coordinates", []byte(`not json`))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("out of range", func(t *testing.T) {
		body := []byte(`{"latitude":999,"longitude":0}`)
		rec := doRequest(t, s, http.MethodPost, "/api/v1/cities/coordinates", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleFindNearby(t *testing.T) {
	s := testServer(t)

	t.Run("defaults", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/nearby?lat=51.5&lon=-0.12", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("with radius and limit", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/nearby?lat=51.5&lon=-0.12&radius=500&limit=5", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
	})

	t.Run("invalid radius is rejected", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/nearby?lat=51.5&lon=-0.12&radius=-5", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("invalid limit is rejected", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/nearby?lat=51.5&lon=-0.12&limit=-5", nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("invalid coordinates", func(t *testing.T) {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/cities/nearby?lat=abc&lon=0", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
}

func TestHandleStats(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/api/v1/stats", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleStatsTxt(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/api/v1/stats.txt", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("expected non-empty body")
	}
}

func TestHandleCount(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/api/v1/count", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleCountTxt(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/api/v1/count.txt", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleRandomCity(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/api/v1/random", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleRandomCityTxt(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/api/v1/random.txt", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleHomePage(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleSearchPage(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/search", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleCoordinatesPage(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/coordinates", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestPublicServerPagesAndAPIMirrors(t *testing.T) {
	s := testServer(t)
	for _, path := range []string{
		"/server/about", "/server/help", "/server/contact", "/server/security", "/server/privacy", "/server/terms",
		"/api/v1/server/about", "/api/v1/server/help", "/api/v1/server/contact", "/api/v1/server/security", "/api/v1/server/privacy", "/api/v1/server/terms",
	} {
		rec := doRequest(t, s, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
	}
}

func TestDocumentationAliases(t *testing.T) {
	s := testServer(t)
	for _, path := range []string{"/api/swagger", "/api/v1/server/swagger"} {
		rec := doRequest(t, s, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
	}
	for _, path := range []string{"/api/graphql", "/api/v1/server/graphql"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"query":"{__typename}"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("POST %s = %d, want 200", path, rec.Code)
		}
	}
}

func TestConfiguredAPIVersionAndPageMirrors(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.APIVersion = "v2"
	s := testServerWithConfig(t, cfg)
	for _, path := range []string{"/api/v2/cities", "/api/v2/server/about"} {
		rec := doRequest(t, s, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
		}
	}
	if rec := doRequest(t, s, http.MethodGet, "/api/v1/cities", nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/v1/cities = %d, want 404 with configured v2 prefix", rec.Code)
	}
	if rec := doRequest(t, s, http.MethodGet, "/api/v2/server/about", nil); rec.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Errorf("server page API mirror Content-Type = %q, want JSON by default", rec.Header().Get("Content-Type"))
	}
}

func TestHandleManifest(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/manifest.json", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleServiceWorker(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/sw.js", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestHandleRobots(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/robots.txt", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Sitemap: "+s.BuildURL(httptest.NewRequest(http.MethodGet, "/robots.txt", nil), "/sitemap.xml")) {
		t.Fatal("robots.txt is missing the request-specific sitemap URL")
	}
}

func TestSEOMetadataAndRobotsPolicy(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Branding.Title = `City <List>`
	cfg.Server.Branding.Tagline = "Global cities"
	cfg.Server.Branding.Description = "City data & ZIP codes"
	cfg.Server.SEO.Canonical = "https://canonical.example"
	cfg.Server.SEO.Author = "Example Org"
	cfg.Server.SEO.Verification.Google = "abc_123"
	cfg.Server.SEO.MetaTags = map[string]string{"application-name": `<script>alert(1)</script>`, "description": "ignored"}
	s := testServerWithConfig(t, cfg)

	public := doRequest(t, s, http.MethodGet, "/", nil)
	if public.Header().Get("X-Robots-Tag") != "" {
		t.Errorf("public route X-Robots-Tag = %q, want empty", public.Header().Get("X-Robots-Tag"))
	}
	if body := public.Body.String(); !strings.Contains(body, "<title>City &lt;List&gt; - Global cities</title>") ||
		!strings.Contains(body, `content="https://canonical.example/"`) ||
		!strings.Contains(body, `name="robots" content="index,follow"`) ||
		!strings.Contains(body, `name="google-site-verification" content="abc_123"`) ||
		!strings.Contains(body, `name="application-name" content="&lt;script&gt;alert(1)&lt;/script&gt;"`) {
		t.Fatalf("SEO metadata missing or unescaped: %s", body[:min(len(body), 1200)])
	}
	api := doRequest(t, s, http.MethodGet, "/api/v1/", nil)
	if api.Header().Get("X-Robots-Tag") != "noindex, nofollow" {
		t.Errorf("API route X-Robots-Tag = %q, want noindex, nofollow", api.Header().Get("X-Robots-Tag"))
	}
	private := config.DefaultConfig()
	private.Server.SEO.NoIndex = true
	privateServer := testServerWithConfig(t, private)
	if rec := doRequest(t, privateServer, http.MethodGet, "/", nil); rec.Header().Get("X-Robots-Tag") != "noindex, nofollow" {
		t.Errorf("global noindex X-Robots-Tag = %q", rec.Header().Get("X-Robots-Tag"))
	}
}

func TestAccentStylesheetAndContrast(t *testing.T) {
	dark, err := parseAccentRGB("#282a36")
	if err != nil {
		t.Fatal(err)
	}
	white := accentRGB{1, 1, 1}
	base, err := parseAccentRGB("#6366f1")
	if err != nil {
		t.Fatal(err)
	}
	hsl := rgbToAccentHSL(base)
	darkAccent := deriveAccent(base, hsl, dark, true)
	lightAccent := deriveAccent(base, hsl, white, false)
	if accentContrast(darkAccent, dark) < 4.5 || accentContrast(lightAccent, white) < 4.5 {
		t.Fatalf("derived accent contrast ratios = %.2f and %.2f, want >=4.5", accentContrast(darkAccent, dark), accentContrast(lightAccent, white))
	}
	css, err := accentCSS("#6366f1")
	if err != nil || !strings.Contains(css, "html.theme-light") || !strings.Contains(css, "prefers-color-scheme:light") {
		t.Fatalf("accent CSS generation failed: err=%v css=%q", err, css)
	}
	cfg := config.DefaultConfig()
	cfg.Web.UI.Accent = "#6366f1"
	s := testServerWithConfig(t, cfg)
	response := doRequest(t, s, http.MethodGet, "/static/css/accent.css", nil)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/css; charset=utf-8" || !strings.Contains(response.Body.String(), "--color-primary:") {
		t.Fatalf("accent route response = %d %q %q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
	withoutAccent := testServer(t)
	if response := doRequest(t, withoutAccent, http.MethodGet, "/static/css/accent.css", nil); response.Code != http.StatusNotFound {
		t.Fatalf("accent route without configured color = %d, want 404", response.Code)
	}
}

func TestFaviconRoute(t *testing.T) {
	s := testServer(t)
	get := doRequest(t, s, http.MethodGet, "/favicon.ico", nil)
	if get.Code != http.StatusOK || get.Header().Get("Content-Type") != "image/svg+xml" || !strings.Contains(get.Body.String(), "<svg") {
		t.Fatalf("favicon GET = %d %q %q", get.Code, get.Header().Get("Content-Type"), get.Body.String())
	}
	head := doRequest(t, s, http.MethodHead, "/favicon.ico", nil)
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatalf("favicon HEAD = %d with %d body bytes", head.Code, head.Body.Len())
	}
}

func TestSitemapListsOnlyRegisteredPublicPagesAllowedByRobots(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/sitemap.xml", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/xml; charset=utf-8" {
		t.Fatalf("sitemap response = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	body := rec.Body.String()
	for _, path := range []string{`<loc>` + s.BuildURL(httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil), "/") + `</loc>`, "/server/help/mcp", "/server/docs/swagger"} {
		if !strings.Contains(body, path) {
			t.Errorf("sitemap is missing public page %q", path)
		}
	}
	for _, path := range []string{"/server/contact", "/server/preferences", "/api/v1/", "/feed.xml", "/rss.xml"} {
		if strings.Contains(body, path) {
			t.Errorf("sitemap includes excluded path %q", path)
		}
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=300" {
		t.Errorf("sitemap Cache-Control = %q", got)
	}
}

func TestSyndicationFeedsAreValidAndConditionallyCached(t *testing.T) {
	s := testServer(t)
	for _, route := range []struct {
		path        string
		contentType string
		forbidden   string
	}{{"/feed.xml", "application/atom+xml; charset=utf-8", "<entry>"}, {"/rss.xml", "application/rss+xml; charset=utf-8", "<item>"}} {
		t.Run(route.path, func(t *testing.T) {
			rec := doRequest(t, s, http.MethodGet, route.path, nil)
			if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != route.contentType {
				t.Fatalf("feed response = %d %q", rec.Code, rec.Header().Get("Content-Type"))
			}
			if strings.Contains(rec.Body.String(), route.forbidden) || rec.Header().Get("ETag") == "" {
				t.Fatalf("empty feed contains entries or is missing an ETag: %s", rec.Body.String())
			}
			req := httptest.NewRequest(http.MethodGet, route.path, nil)
			req.Header.Set("If-None-Match", rec.Header().Get("ETag"))
			conditional := httptest.NewRecorder()
			s.Router().ServeHTTP(conditional, req)
			if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 {
				t.Fatalf("conditional feed response = %d with %d body bytes", conditional.Code, conditional.Body.Len())
			}
		})
	}
}

func TestHandleSecurityTxt(t *testing.T) {
	s := testServer(t)
	canonical := doRequest(t, s, http.MethodGet, "/.well-known/security.txt", nil)
	if canonical.Code != http.StatusOK {
		t.Fatalf("GET /.well-known/security.txt = %d, want 200", canonical.Code)
	}
	head := doRequest(t, s, http.MethodHead, "/.well-known/security.txt", nil)
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Errorf("HEAD /.well-known/security.txt = %d with %d body bytes, want 200 with no body", head.Code, head.Body.Len())
	}
	post := doRequest(t, s, http.MethodPost, "/.well-known/security.txt", nil)
	if post.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /.well-known/security.txt = %d, want 405", post.Code)
	}
	alias := doRequest(t, s, http.MethodGet, "/security.txt", nil)
	if alias.Code != http.StatusNotFound {
		t.Errorf("GET /security.txt = %d, want 404", alias.Code)
	}
}

func TestHandleLLMSTxt(t *testing.T) {
	s := testServer(t)
	for _, path := range []string{"/.well-known/llms.txt", "/llms.txt"} {
		rec := doRequest(t, s, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
			continue
		}
		if contentType := rec.Header().Get("Content-Type"); contentType != "text/plain; charset=utf-8" {
			t.Errorf("GET %s Content-Type = %q, want text/plain; charset=utf-8", path, contentType)
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte("/api/v1/cities/search")) {
			t.Errorf("GET %s does not advertise city search", path)
		}
		head := doRequest(t, s, http.MethodHead, path, nil)
		if head.Code != http.StatusOK || head.Body.Len() != 0 {
			t.Errorf("HEAD %s = %d with %d body bytes, want 200 with no body", path, head.Code, head.Body.Len())
		}
	}
	post := doRequest(t, s, http.MethodPost, "/.well-known/llms.txt", nil)
	if post.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /.well-known/llms.txt = %d, want 405", post.Code)
	}
}

func TestAPITextExtensions(t *testing.T) {
	s := testServer(t)
	for _, test := range []struct {
		path string
		want string
	}{
		{path: "/api/v1/stats.txt", want: "Citylist API Statistics"},
		{path: "/api/v1/count.txt", want: "3"},
		{path: "/api/v1/cities/1.txt", want: "name: London"},
	} {
		response := doRequest(t, s, http.MethodGet, test.path, nil)
		if response.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", test.path, response.Code)
			continue
		}
		if contentType := response.Header().Get("Content-Type"); contentType != "text/plain; charset=utf-8" {
			t.Errorf("GET %s Content-Type = %q, want text/plain; charset=utf-8", test.path, contentType)
		}
		if !strings.Contains(response.Body.String(), test.want) {
			t.Errorf("GET %s body %q does not contain %q", test.path, response.Body.String(), test.want)
		}
	}
}

func TestHandleOpenAPI(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/server/docs/swagger", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	for _, expected := range []string{
		`class="page-title"`, `id="swagger-ui"`,
		`data-openapi-url="/api/v1/server/swagger"`,
		`/static/vendor/swagger-ui/swagger-ui.css`,
		`/static/vendor/swagger-ui/swagger-ui-bundle.js`,
		`/static/vendor/swagger-ui/swagger-ui-standalone-preset.js`,
		`/static/js/docs-swagger.js`,
		`<noscript>`,
	} {
		if !strings.Contains(rec.Body.String(), expected) {
			t.Errorf("Swagger page is missing %q", expected)
		}
	}
	asset := doRequest(t, s, http.MethodGet, "/static/vendor/swagger-ui/swagger-ui.css", nil)
	if asset.Code != http.StatusOK || !strings.Contains(asset.Header().Get("Content-Type"), "text/css") {
		t.Errorf("Swagger UI CSS asset = %d %q, want CSS 200", asset.Code, asset.Header().Get("Content-Type"))
	}
	if got := asset.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("unstamped Swagger asset Cache-Control = %q, want no-cache", got)
	}
	missingSwaggerAsset := doRequest(t, s, http.MethodGet, "/static/vendor/swagger-ui/missing.js?v="+AssetStamp(), nil)
	if missingSwaggerAsset.Code != http.StatusNotFound || strings.Contains(missingSwaggerAsset.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("missing Swagger asset returned %d with Cache-Control %q", missingSwaggerAsset.Code, missingSwaggerAsset.Header().Get("Cache-Control"))
	}
	missingStaticAsset := doRequest(t, s, http.MethodGet, "/static/missing.js?v="+AssetStamp(), nil)
	if missingStaticAsset.Code != http.StatusNotFound || strings.Contains(missingStaticAsset.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("missing static asset returned %d with Cache-Control %q", missingStaticAsset.Code, missingStaticAsset.Header().Get("Cache-Control"))
	}
}

func TestHandleOpenAPISpec(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/api/v1/server/swagger", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var spec struct {
		Paths map[string]map[string]interface{} `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatalf("decoding OpenAPI response: %v", err)
	}
	if _, ok := spec.Paths["/api/v1/server/auth/check"]["get"]; !ok {
		t.Fatal("OpenAPI document is missing the operator token check route")
	}
	for _, route := range []string{"/api/graphql", "/api/v1/server/graphql"} {
		operations, ok := spec.Paths[route]
		if !ok {
			t.Errorf("OpenAPI document is missing GraphQL route %s", route)
			continue
		}
		if _, ok := operations["post"]; !ok {
			t.Errorf("OpenAPI document is missing POST %s", route)
		}
		if _, ok := operations["get"]; !ok {
			t.Errorf("OpenAPI document is missing GET %s", route)
		} else {
			getOperation := operations["get"].(map[string]interface{})
			parameters := getOperation["parameters"].([]interface{})
			if len(parameters) != 3 {
				t.Errorf("OpenAPI GET %s has %d parameters, want query, variables, operationName", route, len(parameters))
			}
		}
	}
	for _, obsolete := range []string{"/api/v1/openapi.json", "/graphql"} {
		if got := doRequest(t, s, http.MethodGet, obsolete, nil).Code; got != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404 for a removed endpoint", obsolete, got)
		}
	}
	if got := doRequest(t, s, http.MethodPost, "/graphql", []byte(`{"query":"{__typename}"}`)).Code; got != http.StatusNotFound {
		t.Errorf("POST /graphql = %d, want 404 for a removed endpoint", got)
	}
}

func TestCORSMiddleware(t *testing.T) {
	s := testServer(t)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/cities", nil)
	req.Header.Set("Origin", "https://client.example")
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("OPTIONS request status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("CORS header = %q, want *", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("wildcard CORS must not allow credentials: %q", rec.Header().Get("Access-Control-Allow-Credentials"))
	}
}

func TestCORSMiddlewareUsesResolvedOrigins(t *testing.T) {
	t.Setenv("DOMAIN", "")
	cfg := config.DefaultConfig()
	cfg.Server.CORS.AllowedOrigins = []string{"https://allowed.example"}
	cfg.Server.CORS.AllowCredentials = true
	s := testServerWithConfig(t, cfg)

	allowed := httptest.NewRequest(http.MethodOptions, "/api/v1/cities", nil)
	allowed.Header.Set("Origin", "https://allowed.example")
	allowedRec := httptest.NewRecorder()
	s.Router().ServeHTTP(allowedRec, allowed)
	if allowedRec.Code != http.StatusNoContent || allowedRec.Header().Get("Access-Control-Allow-Origin") != "https://allowed.example" || allowedRec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("configured origin headers/status = %d %q %q", allowedRec.Code, allowedRec.Header().Get("Access-Control-Allow-Origin"), allowedRec.Header().Get("Access-Control-Allow-Credentials"))
	}

	denied := httptest.NewRequest(http.MethodOptions, "/api/v1/cities", nil)
	denied.Header.Set("Origin", "https://attacker.example")
	deniedRec := httptest.NewRecorder()
	s.Router().ServeHTTP(deniedRec, denied)
	if deniedRec.Code != http.StatusNoContent || deniedRec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unlisted origin should receive no CORS grant: %d %q", deniedRec.Code, deniedRec.Header().Get("Access-Control-Allow-Origin"))
	}

	domainConfig := config.DefaultConfig()
	domainConfig.Server.CORS.AllowedOrigins = nil
	t.Setenv("DOMAIN", "api.city.example")
	domainServer := testServerWithConfig(t, domainConfig)
	domainRequest := httptest.NewRequest(http.MethodGet, "/api/v1/cities", nil)
	domainRequest.Header.Set("Origin", "https://api.city.example")
	domainRec := httptest.NewRecorder()
	domainServer.Router().ServeHTTP(domainRec, domainRequest)
	if domainRec.Header().Get("Access-Control-Allow-Origin") != "https://api.city.example" {
		t.Fatalf("DOMAIN origin was not resolved: %q", domainRec.Header().Get("Access-Control-Allow-Origin"))
	}

	proxyConfig := config.DefaultConfig()
	proxyConfig.Server.CORS.AllowedOrigins = nil
	proxyConfig.Server.TrustedProxies.Additional = []string{"127.0.0.1/32"}
	proxyServer := testServerWithConfig(t, proxyConfig)
	proxyRequest := httptest.NewRequest(http.MethodGet, "/api/v1/cities", nil)
	proxyRequest.RemoteAddr = "127.0.0.1:4567"
	proxyRequest.Header.Set("X-Forwarded-Proto", "https")
	proxyRequest.Header.Set("X-Forwarded-Host", "proxy.city.example")
	proxyRequest.Header.Set("Origin", "https://proxy.city.example")
	proxyRec := httptest.NewRecorder()
	proxyServer.Router().ServeHTTP(proxyRec, proxyRequest)
	if proxyRec.Header().Get("Access-Control-Allow-Origin") != "https://proxy.city.example" {
		t.Fatalf("trusted-proxy learned origin was not resolved: %q", proxyRec.Header().Get("Access-Control-Allow-Origin"))
	}

	disabledConfig := config.DefaultConfig()
	disabledConfig.Server.CORS.AllowedOrigins = []string{""}
	disabledServer := testServerWithConfig(t, disabledConfig)
	disabledRequest := httptest.NewRequest(http.MethodGet, "/api/v1/cities", nil)
	disabledRequest.Header.Set("Origin", "https://api.city.example")
	disabledRec := httptest.NewRecorder()
	disabledServer.Router().ServeHTTP(disabledRec, disabledRequest)
	if disabledRec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("empty CORS origin should disable all CORS headers: %q", disabledRec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/healthz", nil)

	if rec.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q, want SAMEORIGIN", rec.Header().Get("X-Frame-Options"))
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", rec.Header().Get("X-Content-Type-Options"))
	}
}

func TestStaticFiles(t *testing.T) {
	s := testServer(t)
	rec := doRequest(t, s, http.MethodGet, "/static/nonexistent.css", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status for missing static file = %d, want 404", rec.Code)
	}
}
