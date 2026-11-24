package securitydata

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/citylist/src/config"
)

type fixtureTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (transport fixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	copy := request.Clone(request.Context())
	copy.URL = new(url.URL)
	*copy.URL = *request.URL
	copy.URL.Scheme = transport.target.Scheme
	copy.URL.Host = transport.target.Host
	copy.Host = transport.target.Host
	return transport.base.RoundTrip(copy)
}

func fixtureClient(server *httptest.Server) *http.Client {
	target, _ := url.Parse(server.URL)
	return &http.Client{Transport: fixtureTransport{target: target, base: http.DefaultTransport}}
}

func TestCanonicalBlocklistEntriesRejectPrivateTrustedAndInvalidIPs(t *testing.T) {
	trusted := parsePrefixes([]string{"198.51.100.0/24", "invalid"})
	got, skipped := canonicalBlocklistEntries(strings.Join([]string{
		"# comment",
		"203.0.113.9/24",
		"203.0.113.0/24",
		"10.1.2.3",
		"198.51.100.8",
		"2001:db8::1",
		"not-an-address",
	}, "\n"), "ip", trusted)
	want := []string{"2001:db8::1/128", "203.0.113.0/24"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("canonical IP entries = %v, want %v", got, want)
	}
	if skipped != 3 {
		t.Fatalf("skipped IP entries = %d, want 3", skipped)
	}
	if got, ok := normalizeIPEntry("::ffff:203.0.113.7", nil); !ok || got != "203.0.113.7/32" {
		t.Fatalf("normalize mapped IPv4 = (%q, %t), want IPv4 /32", got, ok)
	}
}

func TestCanonicalBlocklistDomains(t *testing.T) {
	got, skipped := canonicalBlocklistEntries(strings.Join([]string{
		"EXAMPLE.COM.",
		"user@bücher.example",
		"*.sub.example.com",
		"*.com",
		"127.0.0.1",
		"bad..example",
		"https://example.net/path",
	}, "\n"), "domain", nil)
	want := []string{"*.sub.example.com", "example.com", "xn--bcher-kva.example"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("canonical domain entries = %v, want %v", got, want)
	}
	if skipped != 4 {
		t.Fatalf("skipped domain entries = %d, want 4", skipped)
	}
	for _, input := range []string{"", ".", "-bad.example", "bad-.example", strings.Repeat("a", 64) + ".example"} {
		if _, ok := normalizeDomainEntry(input); ok {
			t.Errorf("normalizeDomainEntry(%q) accepted invalid domain", input)
		}
	}
}

func TestFetchHTTPSPolicySizeAndStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "fixture")
	}))
	defer server.Close()
	client := fixtureClient(server)
	if got, err := fetch(context.Background(), client, "https://data.example/list", 7); err != nil || string(got) != "fixture" {
		t.Fatalf("fetch fixture = %q, %v", got, err)
	}
	if _, err := fetch(context.Background(), client, "https://data.example/list", 6); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("oversized fetch error = %v, want size-limit error", err)
	}
	for _, raw := range []string{"http://data.example/list", "https://user:pass@data.example/list", "://bad"} {
		if _, err := fetch(context.Background(), client, raw, 100); err == nil {
			t.Errorf("fetch(%q) accepted a forbidden URL", raw)
		}
	}

	statusServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer statusServer.Close()
	if _, err := fetch(context.Background(), fixtureClient(statusServer), "https://data.example/list", 100); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("non-200 fetch error = %v, want HTTP 503", err)
	}
}

func TestUpdateBlocklistsUsesLocalSourcesAndWritesStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/firehol/blocklist-ipsets/master/firehol_level1.netset":
			_, _ = io.WriteString(w, "203.0.114.0/24\n10.0.0.0/8\n")
		case "/custom-domains.txt":
			_, _ = io.WriteString(w, "bad.example\n*.evil.example\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.SecurityBlocklistsConfig{Enabled: true, ExtraSources: []config.SecurityBlocklistSource{{ID: "custom_domains", Type: "domain", URL: "https://data.example/custom-domains.txt"}}}
	directory := filepath.Join(t.TempDir(), "security", "blocklists")
	client := fixtureClient(server)
	if err := UpdateBlocklists(context.Background(), directory, cfg, []string{"203.0.113.128/25"}, client); err != nil {
		t.Fatalf("UpdateBlocklists() error: %v", err)
	}
	for name, want := range map[string]string{
		"firehol_level1.txt": "203.0.114.0/24\n",
		"custom_domains.txt": "*.evil.example\nbad.example\n",
	} {
		got, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
	marker, err := os.ReadFile(filepath.Join(directory, ".last_updated"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimSpace(string(marker))); err != nil {
		t.Fatalf("blocklist update marker %q is not RFC3339: %v", marker, err)
	}
	mode, err := os.Stat(filepath.Join(directory, "custom_domains.txt"))
	if err != nil || mode.Mode().Perm() != 0640 {
		t.Fatalf("blocklist mode = %v, %v; want 0640", mode, err)
	}
	if err := UpdateBlocklists(context.Background(), directory, config.SecurityBlocklistsConfig{}, nil, nil); err != nil {
		t.Fatalf("disabled UpdateBlocklists() should not require a source: %v", err)
	}
}

func TestNVDRequestURLAndUpdateAgainstLocalFixture(t *testing.T) {
	if _, err := nvdRequestURL("http://nvd.example/api", 0, [2]time.Time{}, true); err == nil {
		t.Fatal("nvdRequestURL() accepted HTTP")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/cves/2.0" || r.URL.Query().Get("resultsPerPage") != "2000" {
			t.Errorf("unexpected NVD request: %s %s", r.Method, r.URL.String())
		}
		if r.Header.Get("apiKey") != "fixture-key" {
			t.Errorf("NVD API key header = %q", r.Header.Get("apiKey"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"totalResults":2,"vulnerabilities":[{"cve":{"id":"CVE-2026-0002","description":"second"}},{"cve":{"id":"CVE-2026-0001","description":"first"}}]}`)
	}))
	defer server.Close()
	endpoint, _ := url.Parse(server.URL + "/cves/2.0")
	endpoint.Scheme = "https"
	directory := filepath.Join(t.TempDir(), "cve")
	now := time.Date(2026, 11, 20, 5, 0, 0, 0, time.UTC)
	if err := UpdateNVD(context.Background(), directory, endpoint.String(), "fixture-key", fixtureClient(server), now); err != nil {
		t.Fatalf("UpdateNVD() error: %v", err)
	}
	var records []map[string]any
	content, err := os.ReadFile(filepath.Join(directory, "nvd.json"))
	if err != nil || json.Unmarshal(content, &records) != nil || len(records) != 2 || records[0]["id"] != "CVE-2026-0001" {
		t.Fatalf("published NVD records = %s, %v", content, err)
	}
	marker, err := os.ReadFile(filepath.Join(directory, ".last_updated"))
	if err != nil || strings.TrimSpace(string(marker)) != now.Format(time.RFC3339) {
		t.Fatalf("NVD update marker = %q, %v", marker, err)
	}
}

func TestGeoIPChecksumParsingAndFailedUpdateLeavesExistingData(t *testing.T) {
	digest := sha256.Sum256([]byte("database"))
	if got, err := parseSHA256(fmt.Sprintf("%x  database.mmdb\n", digest)); err != nil || got != hex.EncodeToString(digest[:]) {
		t.Fatalf("parseSHA256() = %q, %v", got, err)
	}
	for _, input := range []string{"", "not-a-digest", strings.Repeat("z", 64)} {
		if _, err := parseSHA256(input); err == nil {
			t.Errorf("parseSHA256(%q) accepted invalid checksum", input)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, ".sha256") {
			_, _ = fmt.Fprintf(w, "%x  %s\n", digest, filepath.Base(strings.TrimSuffix(r.URL.Path, ".sha256")))
			return
		}
		_, _ = io.WriteString(w, "wrong database contents")
	}))
	defer server.Close()
	client := fixtureClient(server)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
	directory := filepath.Join(t.TempDir(), "security", "geoip")
	if err := os.MkdirAll(directory, 0750); err != nil {
		t.Fatal(err)
	}
	previous := filepath.Join(directory, "iptoasn-asn.mmdb")
	if err := os.WriteFile(previous, []byte("previous"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := UpdateGeoIP(context.Background(), directory, client); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("UpdateGeoIP() error = %v, want checksum mismatch", err)
	}
	got, err := os.ReadFile(previous)
	if err != nil || string(got) != "previous" {
		t.Fatalf("previous GeoIP database = %q, %v; update must preserve it", got, err)
	}
}

func TestTrivyReferenceAndLayerValidation(t *testing.T) {
	tests := []struct {
		input     string
		registry  string
		repo      string
		reference string
		bad       bool
	}{
		{input: "ghcr.io/aquasecurity/trivy-db:2", registry: "ghcr.io", repo: "aquasecurity/trivy-db", reference: "2"},
		{input: "registry.example:5443/team/db", registry: "registry.example:5443", repo: "team/db", reference: "latest"},
		{input: "registry.example/team/db@sha256:abc", bad: true},
		{input: "docker.io/team/db:latest", bad: true},
		{input: "registry.example/../db", bad: true},
		{input: "just-a-name", bad: true},
	}
	for _, test := range tests {
		registry, repo, reference, err := parseOCIReference(test.input)
		if (err != nil) != test.bad || (!test.bad && (registry != test.registry || repo != test.repo || reference != test.reference)) {
			t.Errorf("parseOCIReference(%q) = (%q, %q, %q, %v)", test.input, registry, repo, reference, err)
		}
	}

	layer := trivyTestLayer(t, map[string][]byte{
		"trivy.db":      []byte("database bytes"),
		"metadata.json": []byte(`{"NextUpdate":"2026-12-01T00:00:00Z"}`),
	})
	directory := t.TempDir()
	if err := extractTrivyLayer(layer, directory); err != nil {
		t.Fatalf("extractTrivyLayer() error: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(directory, "trivy.db")); err != nil || string(got) != "database bytes" {
		t.Fatalf("extracted Trivy database = %q, %v", got, err)
	}
	if err := extractTrivyLayer([]byte("not gzip"), t.TempDir()); err == nil {
		t.Fatal("extractTrivyLayer() accepted invalid gzip")
	}
	missingMetadata := trivyTestLayer(t, map[string][]byte{"trivy.db": []byte("database")})
	if err := extractTrivyLayer(missingMetadata, t.TempDir()); err == nil {
		t.Fatal("extractTrivyLayer() accepted a layer without metadata")
	}
}

func trivyTestLayer(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(writer)
	for name, contents := range files {
		if err := tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}
