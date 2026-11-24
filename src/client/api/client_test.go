package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestEndpoint(t *testing.T) {
	cases := []struct {
		command string
		args    []string
		want    string
	}{
		{"city", []string{"1"}, "/api/v1/cities/1"},
		{"zipcode", []string{"10001"}, "/api/v1/zipcodes/10001"},
		{"search", []string{"New York", "US"}, "/api/v1/cities/search?country=US&q=New+York"},
		{"zip-search", []string{"New York", "NY"}, "/api/v1/zipcodes/search?q=New+York&state=NY"},
		{"cities", nil, "/api/v1/cities?"},
		{"stats", nil, "/api/v1/stats"},
		{"nearest", []string{"1", "2"}, "/api/v1/cities/coordinates?latitude=1&longitude=2"},
		{"nearby", []string{"1", "2", "3"}, "/api/v1/cities/nearby?lat=1&lon=2&radius=3"},
		{"zip-nearby", []string{"1", "2", "3"}, "/api/v1/zipcodes/nearby?lat=1&lon=2&radius_km=3"},
	}
	for _, test := range cases {
		got, err := Endpoint(test.command, test.args)
		if err != nil || got != test.want {
			t.Errorf("Endpoint(%q) = %q, %v; want %q", test.command, got, err, test.want)
		}
	}
	if _, err := Endpoint("city", nil); err == nil {
		t.Error("missing required argument accepted")
	}
	if _, err := Endpoint("unknown", []string{"x"}); err == nil {
		t.Error("unknown command accepted")
	}
}

func TestClientGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prefix/api" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("q") != "a&b" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Error("Accept header missing")
		}
		if r.Header.Get("Authorization") != "Bearer own_test" {
			t.Error("Authorization header missing")
		}
		if r.Header.Get("Accept-Language") != "es" {
			t.Error("Accept-Language header missing")
		}
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	api, err := New(server.URL + "/prefix/")
	if err != nil {
		t.Fatal(err)
	}
	api.SetCredentials("own_test", "es")
	body, err := api.Get(context.Background(), "/api?q=a%26b")
	if err != nil || string(body) != `{"success":true}` {
		t.Fatalf("Get() = %q, %v", body, err)
	}
}

func TestClientUsesConfiguredAPIVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/cities/1" {
			t.Errorf("path = %q, want configured API version", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetAPIVersion("v2"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(context.Background(), "/api/v1/cities/1"); err != nil {
		t.Fatal(err)
	}
	if err := client.SetAPIVersion("v2/../v1"); err == nil {
		t.Fatal("SetAPIVersion() accepted a path expression")
	}
}

func TestClientErrors(t *testing.T) {
	for _, base := range []string{"", "//example.test", "ftp://example.test", "http://user:pass@example.test", "http://example.test?key=value"} {
		if _, err := New(base); err == nil {
			t.Errorf("New(%q) should fail", base)
		}
	}
	api, err := New("http://example.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.Get(context.Background(), "https://evil.example"); err == nil {
		t.Fatal("absolute external path accepted")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	api, err = New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := api.SetRequestPolicy(30*time.Second, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := api.Get(context.Background(), "/api"); err == nil {
		t.Fatal("non-success status should fail")
	}
	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, string(make([]byte, maxResponseBytes+1)))
	}))
	defer large.Close()
	api, err = New(large.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.Get(context.Background(), "/api"); err == nil {
		t.Fatal("oversized response should fail")
	}
}

func TestClientRetriesTransientResponses(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) < 3 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetRequestPolicy(30*time.Second, 2, 0); err != nil {
		t.Fatal(err)
	}
	body, err := client.Get(context.Background(), "/api")
	if err != nil || string(body) != `{"ok":true}` || requests.Load() != 3 {
		t.Fatalf("Get() = %q, %v; requests = %d", body, err, requests.Load())
	}
}
