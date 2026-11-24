package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAssetURLAndCacheHeaders(t *testing.T) {
	stamp := AssetStamp()
	if got := assetURL("/static/js/app.js"); got != "/static/js/app.js?v="+stamp {
		t.Fatalf("assetURL() = %q, want stamped URL", got)
	}
	if got := assetURL("https://example.test/app.js"); got != "https://example.test/app.js" {
		t.Fatalf("assetURL() changed external URL to %q", got)
	}
	for _, test := range []struct {
		query string
		want  string
	}{{"?v=" + stamp, "public, max-age=31536000, immutable"}, {"", "no-cache"}, {"?v=old", "no-cache"}} {
		req := httptest.NewRequest("GET", "/static/js/app.js"+test.query, nil)
		rec := httptest.NewRecorder()
		if setAssetCacheHeaders(rec, req) {
			t.Fatal("unexpected conditional response")
		}
		if got := rec.Header().Get("Cache-Control"); got != test.want {
			t.Errorf("Cache-Control for %q = %q, want %q", test.query, got, test.want)
		}
	}
	req := httptest.NewRequest("GET", "/static/js/app.js", nil)
	req.Header.Set("If-None-Match", `"`+stamp+`"`)
	rec := httptest.NewRecorder()
	if !setAssetCacheHeaders(rec, req) || rec.Code != 304 {
		t.Fatalf("matching asset ETag returned %d, want 304", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("unstamped 304 Cache-Control = %q, want no-cache", got)
	}
	stampedRequest := httptest.NewRequest("GET", "/static/js/app.js?v="+stamp, nil)
	stampedRequest.Header.Set("If-None-Match", `"`+stamp+`"`)
	stampedResponse := httptest.NewRecorder()
	if !setAssetCacheHeaders(stampedResponse, stampedRequest) || stampedResponse.Code != 304 {
		t.Fatalf("matching stamped ETag returned %d, want 304", stampedResponse.Code)
	}
	if got := stampedResponse.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("stamped 304 Cache-Control = %q", got)
	}
}

func TestHTMLHeadersAndVersionPurge(t *testing.T) {
	stale := httptest.NewRequest("GET", "/", nil)
	stale.AddCookie(&http.Cookie{Name: "citylist_build", Value: "old"})
	rec := httptest.NewRecorder()
	setHTMLCacheHeaders(rec, stale, http.StatusOK)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("Clear-Site-Data"); got != `"cache", "storage"` {
		t.Errorf("Clear-Site-Data = %q", got)
	}
	if got := rec.Header().Get("ETag"); got != `"`+AssetStamp()+`"` {
		t.Errorf("ETag = %q", got)
	}
	if cookie := rec.Result().Cookies()[0]; cookie.Name != "citylist_build" || !cookie.Secure || cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("build cookie attributes are incorrect: %+v", cookie)
	}

	for _, test := range []struct {
		name   string
		dest   string
		mode   string
		method string
		purge  bool
	}{{"top-level navigation", "document", "navigate", "GET", true}, {"iframe", "iframe", "navigate", "GET", false}, {"fetch", "empty", "cors", "GET", false}, {"head", "document", "navigate", "HEAD", false}} {
		req := httptest.NewRequest(test.method, "/", nil)
		req.Header.Set("Sec-Fetch-Dest", test.dest)
		req.Header.Set("Sec-Fetch-Mode", test.mode)
		req.AddCookie(&http.Cookie{Name: "citylist_build", Value: "old"})
		rec := httptest.NewRecorder()
		setHTMLCacheHeaders(rec, req, http.StatusOK)
		if got := rec.Header().Get("Clear-Site-Data") != ""; got != test.purge {
			t.Errorf("%s purge = %t, want %t", test.name, got, test.purge)
		}
	}
	errorRequest := httptest.NewRequest("GET", "/missing", nil)
	errorRequest.AddCookie(&http.Cookie{Name: "citylist_build", Value: "old"})
	errorResponse := httptest.NewRecorder()
	setHTMLCacheHeaders(errorResponse, errorRequest, http.StatusNotFound)
	if got := errorResponse.Header().Get("Clear-Site-Data"); got != "" {
		t.Errorf("error response Clear-Site-Data = %q, want none", got)
	}
}
