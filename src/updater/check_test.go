package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func updateTestDir(t *testing.T) string {
	t.Helper()
	base := filepath.Join(os.TempDir(), "apimgr")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatalf("create test temp namespace: %v", err)
	}
	dir, err := os.MkdirTemp(base, "citylist-")
	if err != nil {
		t.Fatalf("create test temp directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestCheckSelectsCumulativeRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("Accept header = %q", r.Header.Get("Accept"))
		}
		_, _ = w.Write([]byte(`[{"tag_name":"v2.0.0","prerelease":false,"published_at":"2026-01-01T00:00:00Z"},{"tag_name":"v1.9.0-beta","prerelease":true,"published_at":"2025-12-01T00:00:00Z"}]`))
	}))
	defer server.Close()

	release, err := check(context.Background(), server.Client(), server.URL, "beta", 0)
	if err != nil {
		t.Fatal(err)
	}
	if release == nil || release.TagName != "v2.0.0" {
		t.Fatalf("release = %#v, want newest stable release", release)
	}
}

func TestPublicCheckAndEligibilityUseReleaseContract(t *testing.T) {
	const releaseBody = `[{"tag_name":"v2.1.0","prerelease":false,"published_at":"2026-01-01T00:00:00Z"}]`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
			t.Errorf("release request headers = %v", r.Header)
		}
		_, _ = w.Write([]byte(releaseBody))
	}))
	defer server.Close()

	client := &http.Client{Transport: releaseEndpointTransport{endpoint: server.URL}}
	release, err := Check(context.Background(), client, "stable", 0)
	if err != nil || release == nil || release.TagName != "v2.1.0" {
		t.Fatalf("Check() = (%#v, %v), want v2.1.0", release, err)
	}

	now := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	release, err = CheckEligible(context.Background(), client, "stable", "v2.0.0", 0, 30, now)
	if err != nil || release == nil || release.TagName != "v2.1.0" {
		t.Fatalf("CheckEligible() after defer = (%#v, %v), want v2.1.0", release, err)
	}
	release, err = CheckEligible(context.Background(), client, "stable", "v2.0.0", 0, 31, now)
	if err != nil || release != nil {
		t.Fatalf("CheckEligible() before defer = (%#v, %v), want nil", release, err)
	}
	if _, err := CheckEligible(context.Background(), client, "stable", "v2.0.0", 0, 366, now); err == nil {
		t.Fatal("CheckEligible() accepted an out-of-range defer window")
	}
}

type releaseEndpointTransport struct {
	endpoint string
}

func (transport releaseEndpointTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	endpoint, err := http.NewRequestWithContext(request.Context(), request.Method, transport.endpoint, nil)
	if err != nil {
		return nil, err
	}
	clone.URL = endpoint.URL
	clone.Host = endpoint.URL.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func TestCheckErrorsAndDevelBuildEpoch(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		branch     string
		buildEpoch int64
		wantTag    string
		wantErr    bool
	}{
		{name: "server error", status: http.StatusBadGateway, wantErr: true},
		{name: "invalid json", status: http.StatusOK, body: "not-json", wantErr: true},
		{name: "devel newer", status: http.StatusOK, body: `[{"tag_name":"devel","prerelease":true,"published_at":"2026-01-02T00:00:00Z"}]`, branch: "daily", buildEpoch: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix(), wantTag: "devel"},
		{name: "devel already built", status: http.StatusOK, body: `[{"tag_name":"devel","prerelease":true,"published_at":"2026-01-02T00:00:00Z"}]`, branch: "devel", buildEpoch: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC).Unix()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			release, err := check(context.Background(), server.Client(), server.URL, test.branch, test.buildEpoch)
			if (err != nil) != test.wantErr {
				t.Fatalf("check error = %v, wantErr %t", err, test.wantErr)
			}
			if test.wantTag == "" && release != nil {
				t.Fatalf("release = %#v, want nil", release)
			}
			if test.wantTag != "" && (release == nil || release.TagName != test.wantTag) {
				t.Fatalf("release = %#v, want tag %q", release, test.wantTag)
			}
		})
	}
}

func TestCheckPagesPastUnmatchedRecentReleases(t *testing.T) {
	pageOne := make([]Release, 100)
	for index := range pageOne {
		pageOne[index] = Release{TagName: fmt.Sprintf("v1.0.%d-beta", 100-index), Prerelease: true}
	}
	pageTwo := []Release{{TagName: "v1.0.1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var releases []Release
		if r.URL.Query().Get("page") == "1" {
			releases = pageOne
		} else {
			releases = pageTwo
		}
		_ = json.NewEncoder(w).Encode(releases)
	}))
	defer server.Close()

	release, err := check(context.Background(), server.Client(), server.URL, "stable", 0)
	if err != nil || release == nil || release.TagName != "v1.0.1" {
		t.Fatalf("check() = (%#v, %v), want stable release from second page", release, err)
	}
}

func TestNormalizeBranch(t *testing.T) {
	for input, want := range map[string]string{
		" beta ": "beta", "DAILY": "devel", "rolling": "devel", "dev": "devel",
		"development": "devel", "stable": "stable", "unknown": "stable",
	} {
		if got := normalizeBranch(input); got != want {
			t.Errorf("normalizeBranch(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestReplaceBinaryUsesCurrentPermissionsAndReplacesContents(t *testing.T) {
	dir := updateTestDir(t)
	current := filepath.Join(dir, "citylist")
	staged := filepath.Join(dir, "citylist.new")
	if err := os.WriteFile(current, []byte("old"), 0o751); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := replaceBinary(current, staged); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(current)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("replacement contents = %q, want new", got)
	}
	info, err := os.Stat(current)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o751 {
		t.Fatalf("replacement mode = %04o, want 0751", got)
	}
}

func TestReplaceBinaryMissingInputs(t *testing.T) {
	dir := updateTestDir(t)
	if err := replaceBinary(filepath.Join(dir, "missing"), filepath.Join(dir, "staged")); err == nil {
		t.Fatal("replaceBinary with missing current binary succeeded")
	}
	current := filepath.Join(dir, "citylist")
	if err := os.WriteFile(current, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceBinary(current, filepath.Join(dir, "missing")); err == nil {
		t.Fatal("replaceBinary with missing staged binary succeeded")
	}
}

func TestStageAndApplyStagedRelease(t *testing.T) {
	binary := []byte("verified update binary")
	digest := sha256.Sum256(binary)
	manifest := fmt.Sprintf("%x  %s\n", digest, platformBinaryName())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sha256.txt":
			_, _ = w.Write([]byte(manifest))
		case "/binary":
			_, _ = w.Write(binary)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dataDir := updateTestDir(t)
	currentPath := filepath.Join(dataDir, "citylist")
	if err := os.WriteFile(currentPath, []byte("old executable"), 0o751); err != nil {
		t.Fatal(err)
	}
	release := &Release{
		TagName:     "v2.0.0",
		PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Assets: []Asset{
			{Name: platformBinaryName(), BrowserDownloadURL: server.URL + "/binary"},
			{Name: "sha256.txt", BrowserDownloadURL: server.URL + "/sha256.txt"},
		},
	}
	if err := Stage(context.Background(), server.Client(), release, "v1.0.0", 0, dataDir); err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	stagedDir := filepath.Join(dataDir, "update")
	info, err := os.Stat(stagedDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("staged update directory mode = %04o, want 0700", info.Mode().Perm())
	}
	metadataInfo, err := os.Stat(filepath.Join(stagedDir, "release.json"))
	if err != nil {
		t.Fatal(err)
	}
	if metadataInfo.Mode().Perm() != 0o600 {
		t.Fatalf("staged update metadata mode = %04o, want 0600", metadataInfo.Mode().Perm())
	}
	if err := Stage(context.Background(), server.Client(), release, "v1.0.0", 0, dataDir); err != nil {
		t.Fatalf("replace prior staged update: %v", err)
	}
	applied, err := applyStagedAt(stagedDir, currentPath, "v1.0.0", 0)
	if err != nil || !applied {
		t.Fatalf("applyStagedAt() = (%t, %v), want (true, nil)", applied, err)
	}
	got, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(binary) {
		t.Fatalf("updated executable = %q, want %q", got, binary)
	}
	if _, err := os.Lstat(stagedDir); !os.IsNotExist(err) {
		t.Fatalf("staged update directory remains after apply: %v", err)
	}
}

func TestApplyStagedRejectsTamperedAndOlderUpdates(t *testing.T) {
	for _, test := range []struct {
		name     string
		version  string
		checksum string
	}{
		{name: "tampered checksum", version: "v2.0.0", checksum: strings.Repeat("0", 64)},
		{name: "older version", version: "v1.0.0", checksum: strings.Repeat("0", 64)},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := updateTestDir(t)
			currentPath := filepath.Join(dir, "citylist")
			if err := os.WriteFile(currentPath, []byte("old executable"), 0o751); err != nil {
				t.Fatal(err)
			}
			updateDir := filepath.Join(dir, "update")
			if err := os.Mkdir(updateDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(updateDir, stagedBinaryName), []byte("tampered"), 0o700); err != nil {
				t.Fatal(err)
			}
			metadata, err := json.Marshal(stagedRelease{Version: test.version, PublishedAt: time.Now().Unix(), Binary: platformBinaryName(), SHA256: test.checksum})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(updateDir, "release.json"), metadata, 0o600); err != nil {
				t.Fatal(err)
			}
			if applied, err := applyStagedAt(updateDir, currentPath, "v1.0.0", 0); err == nil || applied {
				t.Fatalf("applyStagedAt() = (%t, %v), want error and no application", applied, err)
			}
			got, err := os.ReadFile(currentPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "old executable" {
				t.Fatalf("current executable changed after rejected update: %q", got)
			}
			if _, err := os.Lstat(updateDir); !os.IsNotExist(err) {
				t.Fatalf("rejected staged update was not removed: %v", err)
			}
		})
	}
}

func TestInstallRejectsMissingReleaseAssets(t *testing.T) {
	if err := Install(context.Background(), nil, nil, ""); err == nil {
		t.Fatal("Install with a nil release succeeded")
	}
	for _, release := range []*Release{
		{},
		{Assets: []Asset{{Name: "sha256.txt", BrowserDownloadURL: "https://example.test/sha256.txt"}}},
		{Assets: []Asset{{Name: "citylist-linux-amd64", BrowserDownloadURL: "https://example.test/citylist"}}},
	} {
		if err := Install(context.Background(), nil, release, ""); err == nil {
			t.Errorf("Install with assets %#v succeeded", release.Assets)
		}
	}
}

func TestChecksumFor(t *testing.T) {
	want := strings.Repeat("a", 64)
	got, err := checksumFor(want+"  citylist-linux-amd64\n", "citylist-linux-amd64")
	if err != nil || got != want {
		t.Fatalf("checksumFor = (%q, %v), want (%q, nil)", got, err, want)
	}
	for _, manifest := range []string{"", "short  citylist-linux-amd64", strings.Repeat("g", 64) + "  citylist-linux-amd64"} {
		if _, err := checksumFor(manifest, "citylist-linux-amd64"); err == nil {
			t.Errorf("checksumFor(%q) succeeded, want invalid-entry error", manifest)
		}
	}
}

func TestDownloadBoundsAndStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("binary"))
	}))
	defer server.Close()

	body, err := download(context.Background(), server.Client(), server.URL+"/ok", 6)
	if err != nil || string(body) != "binary" {
		t.Fatalf("download = (%q, %v), want binary", body, err)
	}
	if _, err := download(context.Background(), server.Client(), server.URL+"/ok", 5); err == nil {
		t.Fatal("oversized download succeeded")
	}
	if _, err := download(context.Background(), server.Client(), server.URL+"/missing", 10); err == nil {
		t.Fatal("HTTP 404 download succeeded")
	}
	if _, err := download(context.Background(), server.Client(), "http://example.com/file", 10); err == nil {
		t.Fatal("non-HTTPS URL succeeded")
	}
}

func TestSHA256ExpectedValueMatchesBytes(t *testing.T) {
	content := []byte("release binary")
	hash := sha256.Sum256(content)
	manifest := hex.EncodeToString(hash[:]) + "  citylist-linux-amd64"
	got, err := checksumFor(manifest, "citylist-linux-amd64")
	if err != nil || got != hex.EncodeToString(hash[:]) {
		t.Fatalf("checksumFor = (%q, %v), want calculated SHA-256", got, err)
	}
}

func TestRestartArgsRemovesUpdateCommands(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "update yes", args: []string{"--config", "/etc/citylist", "--update", "yes"}, want: []string{"--config", "/etc/citylist"}},
		{name: "update branch", args: []string{"--update", "branch", "beta", "--port", "8080"}, want: []string{"--port", "8080"}},
		{name: "maintenance update", args: []string{"--maintenance", "update", "check"}},
		{name: "equals form", args: []string{"--update=yes", "--address", "127.0.0.1"}, want: []string{"--address", "127.0.0.1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := restartArgs(test.args)
			if strings.Join(got, "\x00") != strings.Join(test.want, "\x00") {
				t.Fatalf("restartArgs(%q) = %q, want %q", test.args, got, test.want)
			}
		})
	}
}

func TestCheckHandlesNotFoundAndInvalidChannel(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	release, err := check(context.Background(), server.Client(), server.URL, "not-a-channel", 0)
	if err != nil || release != nil {
		t.Fatalf("check = (%#v, %v), want (nil, nil)", release, err)
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		candidate, current string
		published, built   int64
		want               bool
	}{
		{candidate: "v1.2.0", current: "v1.1.9", want: true},
		{candidate: "v1.2.0", current: "v1.2.0", want: false},
		{candidate: "v1.2.0-beta", current: "v1.1.0", want: true},
		{candidate: "devel", current: "v1.2.0", published: time.Now().Unix(), built: 1, want: true},
		{candidate: "devel", current: "v1.2.0", published: 1, built: 2, want: false},
		{candidate: "unknown", current: "v1.2.0", want: false},
	}
	for _, test := range tests {
		if got := IsNewer(test.candidate, test.current, test.published, test.built); got != test.want {
			t.Errorf("IsNewer(%q, %q) = %t, want %t", test.candidate, test.current, got, test.want)
		}
	}
}
