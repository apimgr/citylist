package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func isolateCLIState(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", clientTestDir(t))
	t.Setenv("XDG_DATA_HOME", clientTestDir(t))
	t.Setenv("XDG_CACHE_HOME", clientTestDir(t))
	t.Setenv("CITYLIST_SERVER_PRIMARY", "")
	t.Setenv("CITYLIST_TOKEN", "")
}

func TestReadCLITokenFile(t *testing.T) {
	path := clientTestDir(t) + "/token"
	want := "own_0123456789ABCDEFGHIJKLMNOPQRSTUV"
	if err := os.WriteFile(path, []byte(" \n"+want+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readCLITokenFile(path)
	if err != nil || got != want {
		t.Fatalf("readCLITokenFile() = %q, %v; want token", got, err)
	}
	if _, err := readCLITokenFile(path + "-missing"); err == nil {
		t.Fatal("readCLITokenFile() accepted a missing file")
	}
}

func TestRunQueriesAndFormatsJSON(t *testing.T) {
	isolateCLIState(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/autodiscover" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"server":{"name":"Citylist"},"cli_versions":{}}`)
			return
		}
		if r.URL.Path != "/api/v1/cities/1" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = fmt.Fprint(w, `{"success":true,"data":{"name":"London"}}`)
	}))
	defer server.Close()
	var output, errors strings.Builder
	code := run([]string{"--server", server.URL, "--output", "json", "city", "1"}, &output, &errors)
	if code != 0 || !strings.Contains(output.String(), "London") || errors.Len() != 0 {
		t.Fatalf("run() = %d, output %q, error %q", code, output.String(), errors.String())
	}
}

func TestBooleanEnvironmentAcceptsProjectAliases(t *testing.T) {
	tests := []struct {
		name  string
		env   string
		value string
	}{
		{name: "debug", env: "CITYLIST_DEBUG", value: "yes"},
		{name: "TUI enabled", env: "CITYLIST_TUI_ENABLED", value: "on"},
		{name: "quiet", env: "CITYLIST_QUIET", value: "yup"},
		{name: "verbose", env: "CITYLIST_VERBOSE", value: "affirmative"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			isolateCLIState(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/autodiscover" {
					_, _ = fmt.Fprint(w, `{"cli_versions":{}}`)
					return
				}
				if r.URL.Path != "/api/v1/cities/1" {
					t.Errorf("request path = %q", r.URL.Path)
				}
				_, _ = fmt.Fprint(w, `{"ok":true,"data":{"name":"London"}}`)
			}))
			defer server.Close()
			lookup := func(key string) (string, bool) {
				if key == test.env {
					return test.value, true
				}
				return os.LookupEnv(key)
			}
			var output, errors strings.Builder
			code := runWithEnvironment([]string{"--server", server.URL, "--output", "json", "city", "1"}, &output, &errors, strings.NewReader(""), lookup)
			wantTrace := test.env == "CITYLIST_DEBUG" || test.env == "CITYLIST_VERBOSE"
			if code != 0 || !strings.Contains(output.String(), "London") ||
				(wantTrace && !strings.Contains(errors.String(), "GET "+server.URL+"/api/v1/cities/1")) ||
				(!wantTrace && errors.Len() != 0) {
				t.Fatalf("runWithEnvironment() = %d, output %q, error %q", code, output.String(), errors.String())
			}
		})
	}
	isolateCLIState(t)
	lookup := func(key string) (string, bool) {
		if key == "CITYLIST_DEBUG" {
			return "sometimes", true
		}
		return os.LookupEnv(key)
	}
	var output, errors strings.Builder
	if code := runWithEnvironment([]string{"--server", "https://example.test", "--output", "json", "city", "1"}, &output, &errors, strings.NewReader(""), lookup); code != 2 || !strings.Contains(errors.String(), "supported boolean") {
		t.Fatalf("invalid environment boolean = %d, %q", code, errors.String())
	}
}

func TestBooleanFlagsAcceptProjectAliases(t *testing.T) {
	tests := []struct {
		name        string
		flag        string
		wantTrace   bool
		wantVer     bool
		wantInvalid bool
	}{
		{name: "debug alias", flag: "--debug=oui", wantTrace: true},
		{name: "debug implicit true", flag: "--debug", wantTrace: true},
		{name: "quiet false alias", flag: "--quiet=off"},
		{name: "verbose alias", flag: "--verbose=affirmative", wantTrace: true},
		{name: "version alias", flag: "--version=on", wantVer: true},
		{name: "invalid alias", flag: "--quiet=perhaps", wantInvalid: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			isolateCLIState(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/autodiscover" {
					_, _ = fmt.Fprint(w, `{"cli_versions":{}}`)
					return
				}
				_, _ = fmt.Fprint(w, `{"ok":true,"data":{"name":"London"}}`)
			}))
			defer server.Close()
			args := []string{test.flag}
			if !test.wantVer {
				args = append(args, "--server", server.URL, "--output", "json", "city", "1")
			}
			var output, errors strings.Builder
			code := runWithEnvironment(args, &output, &errors, strings.NewReader(""), func(string) (string, bool) { return "", false })
			if test.wantInvalid {
				if code != 2 || !strings.Contains(errors.String(), "invalid boolean") {
					t.Fatalf("invalid boolean flag = %d, %q", code, errors.String())
				}
				return
			}
			if code != 0 {
				t.Fatalf("boolean flag exit code = %d, stderr %q", code, errors.String())
			}
			if test.wantVer {
				if !strings.Contains(output.String(), Version) {
					t.Fatalf("version flag output = %q", output.String())
				}
				return
			}
			if !strings.Contains(output.String(), "London") || test.wantTrace != strings.Contains(errors.String(), "GET "+server.URL+"/api/v1/cities/1") {
				t.Fatalf("boolean flag output = %q, stderr %q", output.String(), errors.String())
			}
		})
	}
}

func TestRunUsageAndFailures(t *testing.T) {
	isolateCLIState(t)
	originalBinaryName := os.Args[0]
	os.Args[0] = "citylist-cli"
	t.Cleanup(func() { os.Args[0] = originalBinaryName })
	var output, errors strings.Builder
	if code := run([]string{"--help"}, &output, &errors); code != 0 || !strings.Contains(errors.String(), "citylist-cli") {
		t.Fatalf("help = %d, %q", code, errors.String())
	}
	output.Reset()
	errors.Reset()
	if code := run([]string{"--version"}, &output, &errors); code != 0 || !strings.Contains(output.String(), Version) {
		t.Fatalf("version = %d, %q", code, output.String())
	}
	output.Reset()
	errors.Reset()
	if code := run([]string{"city"}, &output, &errors); code != 2 || !strings.Contains(errors.String(), "city requires arguments") {
		t.Fatalf("missing command arguments = %d, %q", code, errors.String())
	}
	output.Reset()
	errors.Reset()
	if code := run(nil, &output, &errors); code != 2 || errors.Len() == 0 {
		t.Fatalf("missing command = %d, %q", code, errors.String())
	}
}

func TestRunServerErrors(t *testing.T) {
	isolateCLIState(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	var output, errors strings.Builder
	code := run([]string{"--server", server.URL, "city", "1"}, &output, &errors)
	if code != 1 || !strings.Contains(errors.String(), "503") {
		t.Fatalf("run() = %d, %q", code, errors.String())
	}
	output.Reset()
	errors.Reset()
	if code = run([]string{"--server", "invalid", "city", "1"}, &output, &errors); code != 2 {
		t.Fatalf("invalid server URL exit code = %d", code)
	}
}

func TestOutputFormats(t *testing.T) {
	data := []byte(`{"ok":true,"data":[{"id":1,"name":"London"}]}`)
	tests := []struct {
		name string
		want string
	}{
		{name: "json", want: `"name": "London"`},
		{name: "table", want: "id  name"},
		{name: "plain", want: "London"},
		{name: "yaml", want: "name: London"},
		{name: "csv", want: "id,name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output strings.Builder
			if err := writeResponse(&output, data, test.name); err != nil {
				t.Fatalf("writeResponse() error: %v", err)
			}
			if !strings.Contains(output.String(), test.want) {
				t.Errorf("output %q does not contain %q", output.String(), test.want)
			}
		})
	}
}

func TestValidFormatIncludesConfiguredFormats(t *testing.T) {
	for _, format := range []string{"json", "table", "yaml", "plain", "csv"} {
		if !validFormat(format) {
			t.Errorf("validFormat(%q) = false", format)
		}
	}
	if validFormat("xml") {
		t.Fatal("validFormat accepted an unsupported format")
	}
}

func TestParseDurationSettingAndResponseErrors(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  time.Duration
		fail  bool
	}{
		{name: "integer seconds", value: "30", want: 30 * time.Second},
		{name: "duration syntax", value: " 250ms ", want: 250 * time.Millisecond},
		{name: "invalid duration", value: "soon", fail: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseDurationSetting(test.value)
			if test.fail {
				if err == nil {
					t.Fatal("parseDurationSetting() accepted invalid input")
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("parseDurationSetting() = %s, %v; want %s", got, err, test.want)
			}
		})
	}

	if got := responseError("en", fmt.Errorf("wrapped: %w", errTableRequiresJSON)); got != "Table output requires a JSON response" {
		t.Errorf("responseError() for table output = %q", got)
	}
	if got := responseError("en", fmt.Errorf("invalid payload")); got != "Could not format the response: invalid payload" {
		t.Errorf("responseError() for generic error = %q", got)
	}
}
