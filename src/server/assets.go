package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// AssetStamp changes whenever the application version or source revision changes.
func AssetStamp() string {
	version := strings.TrimSpace(Version)
	if version == "" {
		version = "dev"
	}
	commit := strings.TrimSpace(Commit)
	if commit == "" {
		commit = "unknown"
	}
	if commit != "unknown" && len(commit) > 7 {
		commit = commit[:7]
	}
	return version + "-" + commit
}

func assetURL(value string) string {
	if !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "?#") {
		return value
	}
	return fmt.Sprintf("%s?v=%s", value, url.QueryEscape(AssetStamp()))
}

func setAssetCacheHeaders(w http.ResponseWriter, r *http.Request) bool {
	stamp := AssetStamp()
	etag := `"` + stamp + `"`
	w.Header().Set("ETag", etag)
	if r.URL.Query().Get("v") == stamp {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}

func setRevalidatingCacheHeaders(w http.ResponseWriter, r *http.Request) bool {
	etag := `"` + AssetStamp() + `"`
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}

func setHTMLCacheHeaders(w http.ResponseWriter, r *http.Request, status int) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("ETag", `"`+AssetStamp()+`"`)
	if cookie, err := r.Cookie("citylist_build"); err == nil && cookie.Value != AssetStamp() && status < 300 && isTopLevelNavigation(r) {
		w.Header().Set("Clear-Site-Data", `"cache", "storage"`)
	}
	http.SetCookie(w, &http.Cookie{
		Name: "citylist_build", Value: AssetStamp(), Path: "/", MaxAge: 31536000,
		Secure: true, SameSite: http.SameSiteLaxMode,
	})
}

func isTopLevelNavigation(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	dest, mode := r.Header.Get("Sec-Fetch-Dest"), r.Header.Get("Sec-Fetch-Mode")
	return (dest == "" && mode == "") || (dest == "document" && mode == "navigate")
}
