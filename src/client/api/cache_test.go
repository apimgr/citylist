package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResponseCacheConfigurationAndPermissions(t *testing.T) {
	if err := SetDefaultResponseCache("", true, time.Minute, 1024); err == nil {
		t.Fatal("enabled default cache accepted an empty directory")
	}
	if err := SetDefaultResponseCache("", false, time.Minute, 0); err == nil {
		t.Fatal("default cache accepted a zero byte limit")
	}
	if err := SetDefaultResponseCache("", false, -time.Second, 1024); err == nil {
		t.Fatal("default cache accepted a negative TTL")
	}

	directory := filepath.Join(t.TempDir(), "cache")
	if err := SetDefaultResponseCache(directory, true, time.Minute, 1024); err != nil {
		t.Fatalf("SetDefaultResponseCache() error: %v", err)
	}
	t.Cleanup(func() { _ = SetDefaultResponseCache("", false, 0, 1) })
	info, err := os.Stat(directory)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("default cache directory mode = %v, %v; want 0700", info, err)
	}
	client, err := New("https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	if !client.cacheEnabled || client.cacheDir != directory || client.cacheTTL != time.Minute || client.cacheMaxBytes != 1024 {
		t.Fatalf("default cache settings were not applied: %+v", client)
	}
	if err := client.SetResponseCache("", true, time.Minute, 1024); err == nil {
		t.Fatal("enabled response cache accepted an empty directory")
	}
	if err := client.SetResponseCache("", false, 0, 1024); err != nil {
		t.Fatalf("disabled cache should not require a directory: %v", err)
	}
	if client.cacheEnabled {
		t.Fatal("zero-TTL response cache remained enabled")
	}
}

func TestResponseCacheKeysEligibilityExpiryAndUnsafeFiles(t *testing.T) {
	client := &Client{cacheDir: t.TempDir(), cacheEnabled: true, cacheTTL: time.Minute, cacheMaxBytes: 64}
	for _, path := range []string{"/api/v1/cities", "/api/v1/zipcodes/10001"} {
		if !cacheablePath(path) {
			t.Errorf("cacheablePath(%q) = false", path)
		}
	}
	for _, path := range []string{"/cities", "/api/v1/server/about", "/api/graphql"} {
		if cacheablePath(path) {
			t.Errorf("cacheablePath(%q) = true", path)
		}
	}
	key := client.cacheKey("https://example.test/api/v1/cities", "application/json")
	if key == client.cacheKey("https://example.test/api/v1/zipcodes", "application/json") {
		t.Fatal("different paths produced the same cache key")
	}
	client.token, client.language = "owner-token", "en"
	if key == client.cacheKey("https://example.test/api/v1/cities", "application/json") {
		t.Fatal("credential and language context was omitted from cache key")
	}
	client.cacheWrite(key, []byte("body"))
	if body, ok := client.cacheRead(key); !ok || string(body) != "body" {
		t.Fatalf("cacheRead() = %q, %t; want body", body, ok)
	}
	path := filepath.Join(client.cacheDir, key)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("cache file mode = %v, %v; want 0600", info, err)
	}
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, ok := client.cacheRead(key); ok {
		t.Fatal("expired cache entry was returned")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expired cache entry was not removed: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 65)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := client.cacheRead(key); ok {
		t.Fatal("oversized cache entry was returned")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(client.cacheDir, "missing"), path); err != nil {
		t.Fatal(err)
	}
	if _, ok := client.cacheRead(key); ok {
		t.Fatal("symlink cache entry was returned")
	}
}

func TestResponseCacheEvictsOldEntriesAndSkipsInvalidWrites(t *testing.T) {
	directory := t.TempDir()
	client := &Client{cacheDir: directory, cacheEnabled: true, cacheTTL: time.Minute, cacheMaxBytes: 6}
	client.cacheWrite("older.cache", []byte("1111"))
	time.Sleep(time.Millisecond)
	client.cacheWrite("newer.cache", []byte("2222"))
	if _, err := os.Stat(filepath.Join(directory, "older.cache")); !os.IsNotExist(err) {
		t.Fatalf("old cache entry was not evicted: %v", err)
	}
	if body, ok := client.cacheRead("newer.cache"); !ok || string(body) != "2222" {
		t.Fatalf("new cache entry = %q, %t", body, ok)
	}
	client.cacheWrite("empty.cache", nil)
	client.cacheWrite("large.cache", []byte("too large"))
	for _, name := range []string{"empty.cache", "large.cache"} {
		if _, err := os.Stat(filepath.Join(directory, name)); !os.IsNotExist(err) {
			t.Errorf("invalid cache write created %s: %v", name, err)
		}
	}
	if err := os.Symlink("target", filepath.Join(directory, "link.cache")); err != nil {
		t.Fatal(err)
	}
	client.cacheWrite("link.cache", []byte("safe"))
	if target, err := os.Readlink(filepath.Join(directory, "link.cache")); err != nil || target != "target" {
		t.Fatalf("cache write replaced a symlink: target=%q err=%v", target, err)
	}
}

func TestClientUsesCacheOnlyForSuccessfulCacheableResponses(t *testing.T) {
	var publicCalls, privateCalls, noStoreCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/cities":
			publicCalls++
			_, _ = fmt.Fprint(w, `{"ok":true}`)
		case "/api/v1/server/about":
			privateCalls++
			_, _ = fmt.Fprint(w, `{"ok":true}`)
		case "/api/v1/zipcodes":
			noStoreCalls++
			w.Header().Set("Cache-Control", "no-store")
			_, _ = fmt.Fprint(w, `{"ok":true}`)
		}
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetResponseCache(t.TempDir(), true, time.Minute, 1024); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/cities", "/api/v1/cities", "/api/v1/server/about", "/api/v1/server/about", "/api/v1/zipcodes", "/api/v1/zipcodes"} {
		if _, err := client.Get(context.Background(), path); err != nil {
			t.Fatalf("Get(%q) error: %v", path, err)
		}
	}
	if publicCalls != 1 || privateCalls != 2 || noStoreCalls != 2 {
		t.Fatalf("server calls public=%d private=%d no-store=%d; want 1, 2, 2", publicCalls, privateCalls, noStoreCalls)
	}
}
