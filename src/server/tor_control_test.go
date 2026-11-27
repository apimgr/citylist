package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apimgr/citylist/src/overlay/tor"
)

func TestTorControlRoutesRequireLoopbackAndRejectUnknownMethodsAndFields(t *testing.T) {
	srv := testServer(t)
	tests := []struct {
		name        string
		method      string
		path        string
		peer        string
		forwarded   string
		body        string
		contentType string
		wantStatus  int
		wantError   string
	}{
		{name: "remote caller cannot use loopback control even with spoofed header", method: http.MethodGet, path: "/server/tor/status", peer: "192.0.2.10:42000", forwarded: "127.0.0.1", wantStatus: http.StatusNotFound, wantError: "NOT_FOUND"},
		{name: "wrong method is hidden", method: http.MethodPut, path: "/server/tor/status", peer: "127.0.0.1:42000", wantStatus: http.StatusNotFound, wantError: "NOT_FOUND"},
		{name: "unknown body fields fail", method: http.MethodPost, path: "/server/tor/validate", peer: "127.0.0.1:42000", body: `{"unexpected":true}`, wantStatus: http.StatusBadRequest, wantError: "BAD_REQUEST"},
		{name: "required body is missing", method: http.MethodPost, path: "/server/tor/regenerate", peer: "127.0.0.1:42000", wantStatus: http.StatusBadRequest, wantError: "BAD_REQUEST"},
		{name: "unsupported content type fails", method: http.MethodPost, path: "/server/tor/validate", peer: "127.0.0.1:42000", body: `{}`, contentType: "text/plain", wantStatus: http.StatusBadRequest, wantError: "BAD_REQUEST"},
		{name: "multiple JSON values fail", method: http.MethodPost, path: "/server/tor/validate", peer: "127.0.0.1:42000", body: `{} {}`, wantStatus: http.StatusBadRequest, wantError: "BAD_REQUEST"},
		{name: "destructive action requires confirmation", method: http.MethodPost, path: "/server/tor/regenerate", peer: "127.0.0.1:42000", body: `{"confirm":false}`, wantStatus: http.StatusBadRequest, wantError: "VALIDATION_FAILED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.RemoteAddr = test.peer
			if test.forwarded != "" {
				request.Header.Set("X-Forwarded-For", test.forwarded)
			}
			if test.body != "" {
				contentType := test.contentType
				if contentType == "" {
					contentType = "application/json"
				}
				request.Header.Set("Content-Type", contentType)
			}
			response := httptest.NewRecorder()
			srv.Router().ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("%s %s = %d, want %d: %s", test.method, test.path, response.Code, test.wantStatus, response.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not JSON: %v: %s", err, response.Body.String())
			}
			if got := body["error"]; got != test.wantError {
				t.Fatalf("error = %v, want %q", got, test.wantError)
			}
		})
	}
}

func TestTorControlStatusUsesTheImmediatePeerAndReturnsVanityState(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/server/tor/status", nil)
	request.RemoteAddr = "127.0.0.1:42000"
	request.Header.Set("X-Forwarded-For", "203.0.113.18")
	response := httptest.NewRecorder()
	testServer(t).Router().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /server/tor/status = %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		OK   bool `json:"ok"`
		Data struct {
			Version string `json:"version"`
			Vanity  struct {
				State      string   `json:"state"`
				Candidates []string `json:"candidates"`
			} `json:"vanity"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Data.Version != "unavailable" || body.Data.Vanity.State != "idle" || len(body.Data.Vanity.Candidates) != 0 {
		t.Fatalf("unexpected status response: %+v", body)
	}
}

func TestTorControlErrorsUseTheRequestedLanguage(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/server/tor/regenerate", strings.NewReader(`{"confirm":false}`))
	request.RemoteAddr = "127.0.0.1:42000"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept-Language", "es")
	response := httptest.NewRecorder()
	testServer(t).Router().ServeHTTP(response, request)
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v: %s", err, response.Body.String())
	}
	if body["error"] != "VALIDATION_FAILED" || body["message"] != "La solicitud no es válida." {
		t.Fatalf("localized error response = %#v", body)
	}
}

func TestTorControlUsesLocalizedFallbackForUnknownErrorCode(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/server/tor/status", nil)
	request.Header.Set("Accept-Language", "es")
	response := httptest.NewRecorder()
	testServer(t).writeTorError(response, request, http.StatusConflict, "CONFLICT")
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v: %s", err, response.Body.String())
	}
	if body["error"] != "CONFLICT" || body["message"] != "La solicitud no es válida." {
		t.Fatalf("localized fallback error = %#v", body)
	}
}

func TestTorControlRejectsOversizedRequestBody(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/server/tor/validate", strings.NewReader(strings.Repeat("x", 16*1024+1)))
	request.RemoteAddr = "127.0.0.1:42000"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	testServer(t).Router().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized body status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}

func TestTorValidateReportsStartupFailure(t *testing.T) {
	srv := testServer(t)
	srv.torStartupFailed = true
	request := httptest.NewRequest(http.MethodPost, "/server/tor/validate", nil)
	request.RemoteAddr = "127.0.0.1:42000"
	response := httptest.NewRecorder()
	srv.Router().ServeHTTP(response, request)
	var body struct {
		OK   bool `json:"ok"`
		Data struct {
			Valid  bool  `json:"valid"`
			Errors []any `json:"errors"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v: %s", err, response.Body.String())
	}
	if !body.OK || body.Data.Valid || len(body.Data.Errors) != 0 {
		t.Fatalf("validation result = %+v", body)
	}
}

func TestTorControlRoutesHandleDisabledManager(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		body       string
		wantStatus int
		wantError  string
	}{
		{name: "validate", path: "/server/tor/validate", wantStatus: http.StatusOK},
		{name: "restart", path: "/server/tor/restart", wantStatus: http.StatusServiceUnavailable, wantError: "SERVER_ERROR"},
		{name: "regenerate", path: "/server/tor/regenerate", body: `{"confirm":true}`, wantStatus: http.StatusServiceUnavailable, wantError: "SERVER_ERROR"},
		{name: "vanity start", path: "/server/tor/vanity/start", body: `{"prefix":"abc","workers":1}`, wantStatus: http.StatusServiceUnavailable, wantError: "SERVER_ERROR"},
		{name: "vanity stop", path: "/server/tor/vanity/stop", wantStatus: http.StatusOK},
		{name: "vanity apply", path: "/server/tor/vanity/apply", body: `{"address":"candidate.onion","confirm":true}`, wantStatus: http.StatusServiceUnavailable, wantError: "SERVER_ERROR"},
		{name: "import keys", path: "/server/tor/import-keys", body: `{"path":"/tmp/keys","confirm":true}`, wantStatus: http.StatusServiceUnavailable, wantError: "SERVER_ERROR"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.RemoteAddr = "127.0.0.1:42000"
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			response := httptest.NewRecorder()
			testServer(t).Router().ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("POST %s = %d, want %d: %s", test.path, response.Code, test.wantStatus, response.Body.String())
			}
			if test.wantError != "" {
				var body map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatalf("response is not JSON: %v: %s", err, response.Body.String())
				}
				if body["error"] != test.wantError {
					t.Fatalf("error = %v, want %q", body["error"], test.wantError)
				}
			}
		})
	}
}

func TestTorControlRoutesHandleStoppedManager(t *testing.T) {
	root := t.TempDir()
	options := tor.DefaultOptions(filepath.Join(root, "config"), filepath.Join(root, "data"), filepath.Join(root, "logs"))
	options.TorBinary = filepath.Join(root, "missing-tor")
	manager, err := tor.Start(context.Background(), options)
	if err != nil {
		t.Fatalf("start disabled Tor manager: %v", err)
	}
	defer func() {
		if err := manager.Close(); err != nil {
			t.Errorf("close disabled Tor manager: %v", err)
		}
	}()
	srv := testServer(t)
	srv.torManager = manager
	tests := []struct {
		name       string
		path       string
		body       string
		wantStatus int
		wantError  string
	}{
		{name: "validate", path: "/server/tor/validate", wantStatus: http.StatusOK},
		{name: "restart", path: "/server/tor/restart", wantStatus: http.StatusInternalServerError, wantError: "SERVER_ERROR"},
		{name: "regenerate", path: "/server/tor/regenerate", body: `{"confirm":true}`, wantStatus: http.StatusInternalServerError, wantError: "SERVER_ERROR"},
		{name: "vanity start", path: "/server/tor/vanity/start", body: `{"prefix":"abc","workers":1}`, wantStatus: http.StatusBadRequest, wantError: "VALIDATION_FAILED"},
		{name: "vanity stop", path: "/server/tor/vanity/stop", wantStatus: http.StatusOK},
		{name: "vanity apply", path: "/server/tor/vanity/apply", body: `{"address":"candidate.onion","confirm":true}`, wantStatus: http.StatusInternalServerError, wantError: "SERVER_ERROR"},
		{name: "import keys", path: "/server/tor/import-keys", body: `{"path":"/tmp/keys","confirm":true}`, wantStatus: http.StatusInternalServerError, wantError: "SERVER_ERROR"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.RemoteAddr = "127.0.0.1:42000"
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			response := httptest.NewRecorder()
			srv.Router().ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("POST %s = %d, want %d: %s", test.path, response.Code, test.wantStatus, response.Body.String())
			}
			if test.wantError != "" {
				var body map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatalf("response is not JSON: %v: %s", err, response.Body.String())
				}
				if body["error"] != test.wantError {
					t.Fatalf("error = %v, want %q", body["error"], test.wantError)
				}
			}
		})
	}
}
