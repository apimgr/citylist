package server

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/citylist/src/cities"
	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/zipcodes"
)

func TestZipcodeAndGraphQLEndpoints(t *testing.T) {
	cityService, err := cities.NewService([]byte(`[{"id":1,"name":"London","country":"GB","coord":{"lat":51.5,"lon":-0.1}}]`))
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	file, err := writer.Create("US.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("US\t10001\tNew York\tNew York\tNY\tNew York\t061\t\t\t40.75\t-73.99\t4\n"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	postal, err := zipcodes.NewService(archive.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithZipcodes(cityService, postal, config.DefaultConfig(), "127.0.0.1", "8080", "test", "date", "commit")
	for _, path := range []string{"/api/v1/zipcodes/10001", "/api/v1/zipcodes/search?q=York", "/api/v1/zipcodes/state/NY", "/api/v1/zipcodes/nearby?lat=40.75&lon=-73.99&radius_km=5"} {
		rec := doRequest(t, s, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200: %s", path, rec.Code, rec.Body.String())
		}
	}
	graphqlRequest := httptest.NewRequest(http.MethodPost, "/api/v1/server/graphql", strings.NewReader(`{"query":"{ zipcode(zip: \"10001\") { city state } }"}`))
	graphqlRequest.Header.Set("Content-Type", "application/json")
	graphqlResponse := httptest.NewRecorder()
	s.Router().ServeHTTP(graphqlResponse, graphqlRequest)
	if graphqlResponse.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/server/graphql = %d: %s", graphqlResponse.Code, graphqlResponse.Body.String())
	}
	for _, path := range []string{"/api/v1/zipcodes/nope", "/api/v1/zipcodes/search?q=x", "/api/v1/zipcodes/state/NY?page=-1", "/api/v1/zipcodes/nearby?lat=999&lon=0&radius_km=5"} {
		rec := doRequest(t, s, http.MethodGet, path, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, rec.Code)
		}
	}
}

func TestSearchPageCanLookUpZIPWithoutJavaScript(t *testing.T) {
	cityService, err := cities.NewService([]byte(`[{"id":1,"name":"London","country":"GB","coord":{"lat":51.5,"lon":-0.1}}]`))
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	file, err := writer.Create("US.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("US\t10001\tNew York\tNew York\tNY\tNew York\t061\t\t\t40.75\t-73.99\t4\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	postal, err := zipcodes.NewService(archive.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithZipcodes(cityService, postal, config.DefaultConfig(), "127.0.0.1", "8080", "test", "date", "commit")

	response := doRequest(t, s, http.MethodGet, "/search?type=zip&q=10001", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /search ZIP lookup = %d, want 200: %s", response.Code, response.Body.String())
	}
	for _, expected := range []string{"name=\"api-base\" content=\"/api/v1\"", "value=\"zip\" selected", "New York", "NY", "10001", "40.7500", "-73.9900"} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("ZIP lookup page missing %q", expected)
		}
	}

	invalid := doRequest(t, s, http.MethodGet, "/search?type=zip&q=12", nil)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "Enter a valid five-digit US ZIP code") {
		t.Errorf("invalid ZIP lookup response = %d, %s", invalid.Code, invalid.Body.String())
	}

	missing := doRequest(t, s, http.MethodGet, "/search?type=zip&q=99999", nil)
	if missing.Code != http.StatusOK || !strings.Contains(missing.Body.String(), "No ZIP code found") {
		t.Errorf("unknown ZIP lookup response = %d, %s", missing.Code, missing.Body.String())
	}
}

func TestBaseURLPrefix(t *testing.T) {
	s := testServer(t)
	if err := s.SetBaseURL("/citylist/"); err != nil {
		t.Fatal(err)
	}
	if got := doRequest(t, s, http.MethodGet, "/citylist/server/healthz", nil).Code; got != http.StatusOK {
		t.Fatalf("prefixed health = %d, want 200", got)
	}
	for _, test := range []struct {
		path string
		want string
	}{
		{"/citylist/", `href="/citylist/search"`},
		{"/citylist/search", `src="/citylist/static/js/app.js?v=` + AssetStamp()},
		{"/citylist/server/docs/graphql", `src="/citylist/static/js/app.js?v=` + AssetStamp()},
	} {
		response := doRequest(t, s, http.MethodGet, test.path, nil)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.want) {
			t.Errorf("GET %s response = %d, missing %q", test.path, response.Code, test.want)
		}
	}
	manifestRequest := httptest.NewRequest(http.MethodGet, "/citylist/manifest.json", nil)
	manifestRequest.Header.Set("Accept", "application/json")
	manifest := httptest.NewRecorder()
	s.Router().ServeHTTP(manifest, manifestRequest)
	if !strings.Contains(manifest.Body.String(), `"start_url": "/citylist/"`) {
		t.Errorf("manifest start_url missing prefix: %s", manifest.Body.String())
	}
	worker := doRequest(t, s, http.MethodGet, "/citylist/sw.js", nil)
	if !strings.Contains(worker.Body.String(), `const BASE = "/citylist"`) || !strings.Contains(worker.Body.String(), `BASE + "/static/js/app.js?v="`) {
		t.Errorf("service worker missing base path: %s", worker.Body.String())
	}
	if got := doRequest(t, s, http.MethodGet, "/healthz", nil).Code; got != http.StatusNotFound {
		t.Fatalf("unprefixed health = %d, want 404", got)
	}
	if err := s.SetBaseURL("https://example.test/path"); err == nil {
		t.Fatal("SetBaseURL should reject URLs with a scheme")
	}
}

func TestRateLimit(t *testing.T) {
	s := testServer(t)
	var status int
	for i := 0; i <= requestsPerMinute; i++ {
		status = doRequest(t, s, http.MethodGet, "/healthz", nil).Code
	}
	if status != http.StatusTooManyRequests {
		t.Fatalf("request %d status = %d, want 429", requestsPerMinute+1, status)
	}
}
