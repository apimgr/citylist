package server

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/apimgr/citylist/src/config"
)

func TestPreferenceSyncImportDirectValuesOverrideCode(t *testing.T) {
	s := testServer(t)
	query := "lang=fr&theme=light&citylist_pref_sort=name"
	encoded := base64.RawURLEncoding.EncodeToString([]byte(query))
	request := httptest.NewRequest(http.MethodGet, "/server/preferences/sync/import?theme=dark&code="+url.QueryEscape(encoded), nil)
	response := httptest.NewRecorder()
	result := s.applyPreferenceImport(response, request, request.URL.Query())
	if result.Theme != "dark" || result.Lang != "fr" || result.Prefs["sort"] != "name" {
		t.Fatalf("import result = %#v", result)
	}
	if len(response.Result().Cookies()) != 3 {
		t.Fatalf("set %d cookies, want 3", len(response.Result().Cookies()))
	}
}

func TestPreferenceSyncCodeFormsAndInvalidValues(t *testing.T) {
	s := testServer(t)
	tests := []struct {
		name string
		code string
	}{
		{name: "plain query", code: "theme=light&lang=fr"},
		{name: "padded base64url", code: base64.URLEncoding.EncodeToString([]byte("theme=light&lang=fr"))},
		{name: "full URL", code: "https://example.test/server/preferences/sync/import?theme=light&lang=fr"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := url.Values{"code": {test.code}}
			request := httptest.NewRequest(http.MethodPost, "/server/preferences/sync", strings.NewReader(values.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			result := s.applyPreferenceImport(response, request, values)
			if result.Theme != "light" || result.Lang != "fr" {
				t.Fatalf("import result = %#v", result)
			}
		})
	}
	bad := url.Values{"theme": {"neon"}, "lang": {"xx"}, "cookie_consent": {"true"}}
	response := httptest.NewRecorder()
	result := s.applyPreferenceImport(response, httptest.NewRequest(http.MethodGet, "/", nil), bad)
	if len(response.Result().Cookies()) != 0 || len(result.Ignored) != 3 {
		t.Fatalf("invalid import set cookies=%d ignored=%v", len(response.Result().Cookies()), result.Ignored)
	}
}

func TestPreferenceSyncExportExcludesNonPreferenceCookies(t *testing.T) {
	cfg := config.DefaultConfig()
	s := testServerWithConfig(t, cfg)
	request := httptest.NewRequest(http.MethodGet, "http://example.test/server/preferences/sync", nil)
	request.AddCookie(&http.Cookie{Name: "theme", Value: "light"})
	request.AddCookie(&http.Cookie{Name: "lang", Value: "fr"})
	request.AddCookie(&http.Cookie{Name: "citylist_pref_sort", Value: "name"})
	request.AddCookie(&http.Cookie{Name: "cookie_consent", Value: "secret"})
	request.AddCookie(&http.Cookie{Name: "owner_token", Value: "secret"})
	query := s.preferenceQuery(request).Encode()
	if query != "citylist_pref_sort=name&lang=fr&theme=light" {
		t.Fatalf("export query = %q", query)
	}
}

func TestPreferenceRedirectRejectsExternalAndAcceptsSameOriginReferer(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.TrustedProxies.Additional = []string{"10.0.0.0/8"}
	s := testServerWithConfig(t, cfg)
	request := httptest.NewRequest(http.MethodPost, "http://example.test:8080/server/preferences/sync?redirect=%2F%2Fevil.example", nil)
	request.Header.Set("Referer", "http://example.test:8080/search?q=city")
	if got := s.preferenceRedirect(request, "/server/preferences/sync"); got != "/search?q=city" {
		t.Fatalf("redirect = %q", got)
	}
}
