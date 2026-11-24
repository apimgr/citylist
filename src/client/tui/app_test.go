package tui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textinput"
	bubbletea "charm.land/bubbletea/v2"
)

func testModel(mode viewMode) model {
	input := textinput.New()
	return model{mode: mode, input: input, width: 80, height: 24, apiVersion: "v1", timeout: 30 * time.Second, retries: 3, retryDelay: time.Second, mouse: true, unicode: true}
}

func TestWindowResizeSetsMinimumAndMode(t *testing.T) {
	m := testModel(menuMode)
	updated, _ := m.Update(bubbletea.WindowSizeMsg{Width: 10, Height: 3})
	got := updated.(model)
	if got.width != 20 || got.height != 5 {
		t.Fatalf("minimum dimensions = %dx%d, want 20x5", got.width, got.height)
	}
	if got.sizeMode.String() != "micro" {
		t.Fatalf("size mode = %s, want micro", got.sizeMode)
	}
}

func TestResponseMessageFormatsJSONAndErrors(t *testing.T) {
	m := testModel(menuMode)
	m.loading = true
	updated, _ := m.Update(responseMsg{body: []byte(`{"ok":true}`)})
	got := updated.(model)
	if got.mode != resultMode || got.loading || !strings.Contains(strings.Join(got.result, ""), `"ok": true`) {
		t.Fatalf("JSON response state = %+v", got)
	}

	updated, _ = got.Update(responseMsg{body: []byte("plain\ntext")})
	got = updated.(model)
	if strings.Join(got.result, "|") != "plain|text" {
		t.Fatalf("plain response lines = %q", got.result)
	}

	wantErr := errors.New("request failed")
	updated, _ = got.Update(responseMsg{err: wantErr})
	got = updated.(model)
	if got.status != wantErr.Error() || got.mode != resultMode || got.loading {
		t.Fatalf("error response state = %+v", got)
	}
}

func TestMenuNavigationFilteringAndHelp(t *testing.T) {
	m := testModel(menuMode)
	updated, _ := m.Update(bubbletea.KeyPressMsg{Code: bubbletea.KeyDown})
	m = updated.(model)
	if m.cursor != 1 {
		t.Fatalf("cursor after down = %d, want 1", m.cursor)
	}
	updated, _ = m.Update(bubbletea.KeyPressMsg{Code: '?'})
	m = updated.(model)
	if m.mode != helpMode || !strings.Contains(m.View().Content, "Keyboard help") {
		t.Fatalf("help state or view is incorrect")
	}
	updated, _ = m.Update(bubbletea.KeyPressMsg{Code: 'x'})
	m = updated.(model)
	if m.mode != menuMode {
		t.Fatalf("help key did not return to menu")
	}
	updated, _ = m.Update(bubbletea.KeyPressMsg{Code: '/'})
	m = updated.(model)
	updated, _ = m.Update(bubbletea.KeyPressMsg{Text: "z", Code: 'z'})
	m = updated.(model)
	if !m.filtering || m.filter != "z" || len(m.visibleCommands()) == 0 {
		t.Fatalf("filter state = active:%v filter:%q visible:%d", m.filtering, m.filter, len(m.visibleCommands()))
	}
	m.filter = "zzzz"
	if len(m.visibleCommands()) != 0 {
		t.Fatal("unmatched filter unexpectedly returned commands")
	}
	updated, _ = m.Update(bubbletea.KeyPressMsg{Code: bubbletea.KeyEscape})
	m = updated.(model)
	if m.filtering || m.filter != "" {
		t.Fatalf("escape did not clear filter")
	}
}

func TestFormRequiresRequiredFieldsAndAllowsOptionalFields(t *testing.T) {
	m := testModel(formMode)
	m.cursor = 2
	m.form = 0
	updated, _ := m.submitField()
	m = updated.(model)
	if m.status == "" || m.form != 0 {
		t.Fatalf("empty required field state = %+v", m)
	}

	m.cursor = 5
	m.form = 0
	updated, _ = m.submitField()
	m = updated.(model)
	if m.form != 1 || m.mode != formMode || len(m.values) != 1 || m.values[0] != "" {
		t.Fatalf("empty optional field state = %+v", m)
	}
	updated, _ = m.submitField()
	m = updated.(model)
	if !m.loading || m.mode != resultMode {
		t.Fatalf("completed form state = %+v", m)
	}
}

func TestResultNavigationAndLayoutHelpers(t *testing.T) {
	m := testModel(resultMode)
	m.result = []string{"a", "b", "c"}
	m.height = 8
	updated, _ := m.Update(bubbletea.KeyPressMsg{Code: bubbletea.KeyDown})
	m = updated.(model)
	if m.resultPos != 1 {
		t.Fatalf("result position after down = %d, want 1", m.resultPos)
	}
	updated, _ = m.Update(bubbletea.KeyPressMsg{Code: bubbletea.KeyEnter})
	m = updated.(model)
	if m.mode != menuMode {
		t.Fatalf("enter did not return to menu")
	}
	if got := truncate("abcdef", 5); got != "ab..." {
		t.Fatalf("truncate() = %q", got)
	}
	if got := truncate("abc", 3); got != "abc" {
		t.Fatalf("truncate short value = %q", got)
	}
	if got := m.resultHeight(); got != 1 {
		t.Fatalf("resultHeight() = %d, want 1", got)
	}
	for _, key := range []string{"city", "zip-nearby", "unknown"} {
		_ = commandTranslationKey(key)
	}
	for _, field := range []string{"City ID", "ZIP code", "Query", "Country code (optional)", "City or state", "State code", "State code (optional)", "Page (optional)", "Limit (optional)", "Latitude", "Longitude", "Radius in km", "unknown"} {
		_ = fieldTranslationKey(field)
	}
}

func TestFetchRejectsInvalidCommandValues(t *testing.T) {
	m := testModel(menuMode)
	cmd := m.fetch(command{name: "not-a-command"})
	if cmd == nil {
		t.Fatal("fetch() returned no command for invalid endpoint")
	}
	if msg, ok := cmd().(responseMsg); !ok || msg.err == nil {
		t.Fatalf("invalid fetch response = %#v", msg)
	}
}

func TestSetupTestsServerAndOptionalTokenBeforeSaving(t *testing.T) {
	var authenticated bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/server/healthz":
			w.WriteHeader(http.StatusOK)
		case "/api/v1/server/auth/check":
			authenticated = r.Header.Get("Authorization") == "Bearer own_test"
			if authenticated {
				w.WriteHeader(http.StatusOK)
			} else {
				http.Error(w, "invalid token", http.StatusUnauthorized)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	saved := ""
	m := testModel(setupMode)
	m.serverURL = server.URL
	m.token = "own_test"
	m.version = "test"
	m.saveServer = func(value string) error {
		saved = value
		return nil
	}
	response := m.testConnection(server.URL)().(responseMsg)
	if response.err != nil {
		t.Fatalf("testConnection() error: %v", response.err)
	}
	if response.info != server.URL {
		t.Fatalf("testConnection() server info = %q, want fallback %q", response.info, server.URL)
	}
	updated, _ := m.Update(response)
	got := updated.(model)
	if !authenticated || got.mode != menuMode || saved != server.URL {
		t.Fatalf("setup state = mode:%v authenticated:%v saved:%q", got.mode, authenticated, saved)
	}

	m = testModel(setupMode)
	m.serverURL = "http://127.0.0.1:1"
	saved = ""
	response = m.testConnection(m.serverURL)().(responseMsg)
	if response.err == nil {
		t.Fatal("testConnection() accepted an unreachable server")
	}
	updated, _ = m.Update(response)
	got = updated.(model)
	if got.mode != setupMode || saved != "" {
		t.Fatalf("failed setup state = mode:%v saved:%q", got.mode, saved)
	}
}
