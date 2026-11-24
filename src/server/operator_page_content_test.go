package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apimgr/citylist/src/config"
)

func TestLocalizedOperatorBodySupportsYAMLMapShapes(t *testing.T) {
	tests := []struct {
		name string
		body interface{}
		lang string
		want string
	}{
		{name: "string", body: "plain", lang: "es", want: "plain"},
		{name: "string map locale", body: map[string]string{"es": "Español", "default": "Default"}, lang: "es", want: "Español"},
		{name: "string map fallback", body: map[string]string{"default": "Default"}, lang: "fr", want: "Default"},
		{name: "interface map locale", body: map[string]interface{}{"es": "Español", "default": "Default"}, lang: "es", want: "Español"},
		{name: "interface map fallback", body: map[string]interface{}{"default": "Default"}, lang: "fr", want: "Default"},
		{name: "yaml interface map locale", body: map[interface{}]interface{}{"es": "Español", "default": "Default"}, lang: "es", want: "Español"},
		{name: "missing locale", body: map[string]string{"es": "Español"}, lang: "fr", want: ""},
		{name: "unsupported shape", body: []string{"not", "text"}, lang: "en", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := localizedOperatorBody(test.body, test.lang); got != test.want {
				t.Errorf("localizedOperatorBody() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestOperatorPageSourceValidatesFormatSizeAndContactLinks(t *testing.T) {
	tests := []struct {
		name    string
		page    string
		content config.BrandingPageContent
		want    string
	}{
		{name: "localized markdown and variables", content: config.BrandingPageContent{Format: "md", Body: map[string]string{"es": "Hola {project_name}", "default": "Hello"}}, want: "Hola citylist"},
		{name: "default format", content: config.BrandingPageContent{Body: "A paragraph"}, want: "A paragraph"},
		{name: "header requires html", page: "header", content: config.BrandingPageContent{Format: "md", Body: "# Header"}, want: ""},
		{name: "unknown format", content: config.BrandingPageContent{Format: "xml", Body: "<x/>"}, want: ""},
		{name: "contact link rejected", content: config.BrandingPageContent{Format: "md", Body: "[Contact](/server/contact)"}, want: ""},
		{name: "oversized body rejected", content: config.BrandingPageContent{Format: "md", Body: strings.Repeat("x", 256*1024+1)}, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			page := test.page
			if page == "" {
				page = "about"
			}
			switch page {
			case "header":
				cfg.Server.Branding.Content.Header = test.content
			default:
				cfg.Server.Branding.Content.About = test.content
			}
			s := testServerWithConfig(t, cfg)
			request := httptest.NewRequest(http.MethodGet, "/server/about?lang=es", nil)
			_, got := s.operatorPageSource(request, page)
			if got != test.want {
				t.Errorf("operatorPageSource() body = %q, want %q", got, test.want)
			}
		})
	}
}

func TestOperatorHTMLSanitizesMarkdownAndStrictHeaderHTML(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Branding.Content.About = config.BrandingPageContent{
		Format: "md",
		Body:   "[External](https://example.org) <script>alert(1)</script> {project_name}",
	}
	cfg.Server.Branding.Content.Header = config.BrandingPageContent{
		Format: "html",
		Body:   `<a href="https://example.org" onclick="alert(1)">Link</a><script>alert(1)</script>`,
	}
	s := testServerWithConfig(t, cfg)
	request := httptest.NewRequest(http.MethodGet, "/server/about", nil)

	markdown := string(s.operatorHTML(request, "about"))
	for _, want := range []string{`href="https://example.org"`, `rel="noopener noreferrer nofollow ugc"`, "citylist"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("sanitized Markdown is missing %q: %s", want, markdown)
		}
	}
	for _, forbidden := range []string{"<script>", "</script>", "onclick="} {
		if strings.Contains(markdown, forbidden) {
			t.Errorf("sanitized Markdown contains %q: %s", forbidden, markdown)
		}
	}

	header := string(s.operatorHTML(request, "header"))
	if strings.Contains(header, "<script") || strings.Contains(header, "onclick=") {
		t.Errorf("strict header HTML contains executable content: %s", header)
	}
}

func TestStandardServerPagesRenderHTMLAndJSON(t *testing.T) {
	s := testServer(t)
	pages := []string{
		"/server/about", "/server/help", "/server/privacy", "/server/terms", "/server/dpo",
		"/server/security", "/server/security/policy", "/server/security/thanks",
	}
	for _, page := range pages {
		t.Run(page, func(t *testing.T) {
			htmlResponse := doRequest(t, s, http.MethodGet, page, nil)
			if htmlResponse.Code != http.StatusOK || !strings.Contains(htmlResponse.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("GET %s HTML = %d %q", page, htmlResponse.Code, htmlResponse.Header().Get("Content-Type"))
			}
			if !strings.Contains(htmlResponse.Body.String(), "<main") {
				t.Errorf("GET %s did not render server-side page content", page)
			}

			apiPath := strings.Replace(page, "/server/", "/api/v1/server/", 1)
			request := httptest.NewRequest(http.MethodGet, apiPath, nil)
			request.Header.Set("Accept", "application/json")
			response := httptest.NewRecorder()
			s.Router().ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("GET %s JSON = %d: %s", apiPath, response.Code, response.Body.String())
			}
			if !strings.Contains(response.Header().Get("Content-Type"), "application/json") {
				t.Errorf("GET %s Content-Type = %q, want JSON", apiPath, response.Header().Get("Content-Type"))
			}
		})
	}
}
