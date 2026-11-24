package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContentNegotiationMiddleware(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		accept      string
		userAgent   string
		contentType string
		body        string
		wantType    string
		wantBody    string
	}{
		{
			name:        "html to readable text",
			path:        "/",
			accept:      "text/plain",
			contentType: "text/html; charset=utf-8",
			body:        "<main><h1>Cities</h1><p>Find a city</p><script>hidden()</script></main>",
			wantType:    "text/plain; charset=utf-8",
			wantBody:    "═══ Cities\nFind a city",
		},
		{
			name:        "json to labeled text",
			path:        "/api/v1/cities.txt",
			accept:      "application/json, text/plain;q=0.5",
			contentType: "application/json",
			body:        `{"name":"Paris"}`,
			wantType:    "text/plain; charset=utf-8",
			wantBody:    "name: Paris",
		},
		{
			name:        "zero quality does not negotiate",
			path:        "/",
			accept:      "text/plain;q=0, application/json",
			contentType: "application/json",
			body:        `{"ok":true}`,
			wantType:    "application/json",
			wantBody:    `{"ok":true}`,
		},
		{
			name:        "curl receives frontend text by default",
			path:        "/",
			userAgent:   "curl/8.0",
			contentType: "text/html; charset=utf-8",
			body:        "<main><h1>Cities</h1></main>",
			wantType:    "text/plain; charset=utf-8",
			wantBody:    "═══ Cities",
		},
		{
			name:        "curl receives API JSON by default",
			path:        "/api/v1/cities",
			userAgent:   "curl/8.0",
			contentType: "application/json",
			body:        `{"count":2}`,
			wantType:    "application/json",
			wantBody:    `{"count":2}`,
		},
		{
			name:        "explicit API JSON overrides curl detection",
			path:        "/api/v1/cities",
			accept:      "application/json",
			userAgent:   "curl/8.0",
			contentType: "application/json",
			body:        `{"count":2}`,
			wantType:    "application/json",
			wantBody:    `{"count":2}`,
		},
		{
			name:        "text browser receives plain API output",
			path:        "/api/v1/cities",
			userAgent:   "Lynx/2.9.0",
			contentType: "application/json",
			body:        `{"count":2}`,
			wantType:    "text/plain; charset=utf-8",
			wantBody:    "count: 2",
		},
		{
			name:        "explicit API JSON overrides text browser detection",
			path:        "/api/v1/cities",
			accept:      "application/json",
			userAgent:   "Lynx/2.9.0",
			contentType: "application/json",
			body:        `{"count":2}`,
			wantType:    "application/json",
			wantBody:    `{"count":2}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := (&Server{}).contentNegotiationMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(test.path, ".txt") && r.URL.Path != strings.TrimSuffix(test.path, ".txt") {
					t.Errorf("handler path = %q, want stripped .txt extension", r.URL.Path)
				}
				w.Header().Set("Content-Type", test.contentType)
				_, _ = w.Write([]byte(test.body))
			}))
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Header.Set("Accept", test.accept)
			request.Header.Set("User-Agent", test.userAgent)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if got := response.Header().Get("Content-Type"); got != test.wantType {
				t.Fatalf("Content-Type = %q, want %q", got, test.wantType)
			}
			if got := response.Header().Get("Vary"); !strings.Contains(got, "Accept") {
				t.Fatalf("Vary = %q, want Accept", got)
			}
			if got := strings.TrimSpace(response.Body.String()); got != test.wantBody {
				t.Fatalf("body = %q, want %q", got, test.wantBody)
			}
		})
	}
}

func TestHTMLDocumentToPlainTextFormatsInteractiveMarkup(t *testing.T) {
	body := `<main><h1>Directory</h1><p>See <a href="https://example.test">the site</a>.</p><ul><li>First</li><li>Second</li></ul><ol><li>One</li><li>Two</li></ol><table><tr><th>Name</th><th>City</th></tr><tr><td>Ada</td><td>London</td></tr></table><form><label>Hidden form</label><button>Submit</button></form><script>hidden()</script></main>`
	got := string(htmlDocumentToPlainText([]byte(body)))
	for _, expected := range []string{"═══ Directory", "the site [https://example.test]", "• First", "• Second", "1. One", "2. Two", "Name │ City", "Ada │ London"} {
		if !strings.Contains(got, expected) {
			t.Errorf("plain text output %q does not contain %q", got, expected)
		}
	}
	for _, omitted := range []string{"Hidden form", "Submit", "hidden()"} {
		if strings.Contains(got, omitted) {
			t.Errorf("plain text output %q contains skipped content %q", got, omitted)
		}
	}
	if !strings.HasSuffix(got, "\n") || strings.HasSuffix(got, "\n\n") {
		t.Errorf("plain text output must end with exactly one newline: %q", got)
	}
}

func TestContentNegotiationPreservesOurCLIJSON(t *testing.T) {
	handler := (&Server{}).contentNegotiationMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("CLI Accept = %q, want application/json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/cities", nil)
	request.Header.Set("Accept", "text/plain")
	request.Header.Set("User-Agent", "citylist-cli/1.0")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := response.Body.String(); got != `{"ok":true}` {
		t.Fatalf("body = %q, want JSON unchanged", got)
	}
}
