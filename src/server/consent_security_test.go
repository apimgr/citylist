package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestReadConsentCookieValidation(t *testing.T) {
	now := time.Now().Unix()
	tests := []struct {
		name    string
		consent cookieConsent
		valid   bool
	}{
		{name: "valid", consent: cookieConsent{Essential: true, Preferences: true, Timestamp: now}, valid: true},
		{name: "essential disabled", consent: cookieConsent{Preferences: true, Timestamp: now}},
		{name: "missing timestamp", consent: cookieConsent{Essential: true}},
		{name: "future timestamp", consent: cookieConsent{Essential: true, Timestamp: now + 1}},
		{name: "expired timestamp", consent: cookieConsent{Essential: true, Timestamp: now - int64(consentCookieMaxAge) - 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(test.consent)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest("GET", "/", nil)
			request.AddCookie(&http.Cookie{Name: "cookie_consent", Value: url.QueryEscape(string(data))})
			_, valid := readConsentCookie(request)
			if valid != test.valid {
				t.Fatalf("readConsentCookie() valid = %t, want %t", valid, test.valid)
			}
		})
	}
}

func TestRenderSecurityPolicyMarkdownSanitizesAndPreservesMarkdown(t *testing.T) {
	markdown := "# Policy\n\n[External](https://example.org)\n\n<script>alert(1)</script>\n\n| A | B |\n| - | - |\n| 1 | 2 |\n"
	rendered := renderSecurityPolicyMarkdown(markdown)
	for _, expected := range []string{"<h1>Policy</h1>", "<table>", `rel="noopener noreferrer nofollow ugc"`} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("rendered policy does not contain %q: %s", expected, rendered)
		}
	}
	if strings.Contains(rendered, "<script") {
		t.Fatalf("rendered policy contains executable script markup: %s", rendered)
	}
}
