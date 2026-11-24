package server

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/common/i18n"
	"github.com/apimgr/citylist/src/config"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

var operatorLinkPattern = regexp.MustCompile(`(?i)(?:href\s*=\s*['"]?|\]\()([^'"\s)>]+)`)

// operatorHTML returns a localized, bounded, sanitized operator page fragment.
func (s *Server) operatorHTML(r *http.Request, name string) template.HTML {
	content, body := s.operatorPageSource(r, name)
	if strings.TrimSpace(body) == "" {
		return ""
	}
	if len(body) > 256*1024 {
		return ""
	}
	if operatorLinksContact(body) {
		return ""
	}
	if name == "header" || name == "footer" {
		return template.HTML(sanitizeStrictOperatorHTML(body))
	}
	body = s.expandOperatorPageVariables(r, body)
	var rendered string
	switch strings.ToLower(strings.TrimSpace(content.Format)) {
	case "", "md":
		var output bytes.Buffer
		if err := goldmark.New(goldmark.WithExtensions(extension.GFM)).Convert([]byte(body), &output); err != nil {
			log.Printf("warning: ignoring invalid server.branding.content.%s markdown", name)
			return ""
		}
		rendered = output.String()
	case "html":
		rendered = body
	case "txt":
		paragraphs := strings.Split(strings.TrimSpace(body), "\n\n")
		for index, paragraph := range paragraphs {
			paragraphs[index] = "<p>" + strings.ReplaceAll(html.EscapeString(strings.TrimSpace(paragraph)), "\n", "<br>") + "</p>"
		}
		rendered = strings.Join(paragraphs, "\n")
	default:
		log.Printf("warning: ignoring server.branding.content.%s with unknown format %q", name, content.Format)
		return ""
	}
	return template.HTML(sanitizeOperatorPageFragment(rendered))
}

func (s *Server) operatorPageSource(r *http.Request, name string) (config.BrandingPageContent, string) {
	var content config.BrandingPageContent
	switch name {
	case "header":
		content = s.cfg.Server.Branding.Content.Header
	case "about":
		content = s.cfg.Server.Branding.Content.About
	case "privacy":
		content = s.cfg.Server.Branding.Content.Privacy
	case "footer":
		content = s.cfg.Server.Branding.Content.Footer
	case "terms":
		content = s.cfg.Server.Branding.Content.Terms
	case "help":
		content = config.BrandingPageContent{Format: "md", Body: s.cfg.Server.Pages.Help.Content}
	default:
		return content, ""
	}
	body := localizedOperatorBody(content.Body, i18n.LangFromRequest(r))
	if name == "help" && strings.TrimSpace(body) == "" {
		body = i18n.Translate(i18n.LangFromRequest(r), "pages.help.default_content")
	}
	if strings.TrimSpace(body) == "" {
		return content, ""
	}
	configKey := "server.branding.content." + name
	if name == "help" {
		configKey = "server.pages.help.content"
	}
	if len(body) > 256*1024 {
		log.Printf("warning: ignoring %s body larger than 256 KiB", configKey)
		return config.BrandingPageContent{}, ""
	}
	if operatorLinksContact(body) {
		log.Printf("warning: ignoring %s body that links to /server/contact", configKey)
		return config.BrandingPageContent{}, ""
	}
	format := strings.ToLower(strings.TrimSpace(content.Format))
	if (name == "header" || name == "footer") && format != "html" {
		log.Printf("warning: ignoring server.branding.content.%s with non-html format %q", name, content.Format)
		return config.BrandingPageContent{}, ""
	}
	if format != "" && format != "md" && format != "html" && format != "txt" {
		log.Printf("warning: ignoring %s with unknown format %q", configKey, content.Format)
		return config.BrandingPageContent{}, ""
	}
	return content, s.expandOperatorPageVariables(r, body)
}

func localizedOperatorBody(body interface{}, lang string) string {
	switch value := body.(type) {
	case string:
		return value
	case map[string]interface{}:
		if localized, ok := value[lang].(string); ok && strings.TrimSpace(localized) != "" {
			return localized
		}
		if fallback, ok := value["default"].(string); ok {
			return fallback
		}
	case map[string]string:
		if localized := value[lang]; strings.TrimSpace(localized) != "" {
			return localized
		}
		return value["default"]
	case map[interface{}]interface{}:
		if localized, ok := value[lang].(string); ok && strings.TrimSpace(localized) != "" {
			return localized
		}
		if fallback, ok := value["default"].(string); ok {
			return fallback
		}
	}
	return ""
}

func operatorLinksContact(body string) bool {
	for _, match := range operatorLinkPattern.FindAllStringSubmatch(body, -1) {
		parsed, err := url.Parse(strings.TrimSpace(match[1]))
		if err == nil && strings.EqualFold(strings.TrimRight(parsed.Path, "/"), "/server/contact") {
			return true
		}
	}
	return false
}

func sanitizeOperatorPageFragment(fragment string) string {
	policy := bluemonday.NewPolicy()
	policy.AllowStandardURLs()
	policy.AllowRelativeURLs(true)
	policy.AllowElements("p", "br", "hr", "blockquote", "ul", "ol", "li", "h1", "h2", "h3", "h4", "h5", "h6", "strong", "b", "em", "i", "del", "s", "code", "pre", "table", "thead", "tbody", "tr", "th", "td", "dl", "dt", "dd", "sup", "sub")
	policy.AllowAttrs("href", "title").OnElements("a")
	policy.AllowAttrs("align").Matching(regexp.MustCompile("^(left|center|right)$")).OnElements("th", "td")
	policy.AllowAttrs("class").Matching(regexp.MustCompile("^language-[a-zA-Z0-9_-]+$")).OnElements("code")
	clean := policy.Sanitize(fragment)
	linkTag := regexp.MustCompile(`(?i)<a\b([^>]*)>`)
	return linkTag.ReplaceAllStringFunc(clean, func(tag string) string {
		if !strings.Contains(strings.ToLower(tag), "href=") {
			return tag
		}
		return strings.TrimSuffix(tag, ">") + ` rel="noopener noreferrer nofollow ugc">`
	})
}

func sanitizeStrictOperatorHTML(fragment string) string {
	policy := bluemonday.NewPolicy()
	policy.AllowElements("p", "br", "span", "div", "strong", "b", "em", "i", "u", "s", "small", "h1", "h2", "h3", "h4", "h5", "h6", "ul", "ol", "li")
	policy.AllowAttrs("href", "title", "target", "rel").OnElements("a")
	policy.AllowAttrs("src", "alt", "title", "width", "height").OnElements("img")
	policy.AllowURLSchemes("https", "data")
	policy.AllowAttrs("class", "id").Globally()
	policy.RequireNoReferrerOnLinks(true)
	return policy.Sanitize(fragment)
}

func (s *Server) expandOperatorPageVariables(r *http.Request, body string) string {
	now := time.Now()
	buildDate := s.buildDate
	if parsed, err := time.Parse(time.RFC3339, buildDate); err == nil {
		buildDate = parsed.Format("January 02, 2006 at 15:04:05 MST")
	}
	onionAddress, i2pAddress := "", ""
	if s.torManager != nil {
		snapshot := s.torManager.Snapshot()
		if snapshot.Enabled && snapshot.Running {
			onionAddress = snapshot.Hostname
		}
	}
	if s.i2pManager != nil {
		snapshot := s.i2pManager.Snapshot()
		if snapshot.Enabled && snapshot.Running {
			i2pAddress = snapshot.Hostname
		}
	}
	values := map[string]string{
		"{current_year}": fmt.Sprint(now.Year()), "{project_name}": "citylist", "{project_org}": "apimgr",
		"{project_version}": s.version, "{build_datetime}": buildDate,
		"{onion_address}": onionAddress, "{i2p_address}": i2pAddress,
	}
	for placeholder, value := range values {
		body = strings.ReplaceAll(body, placeholder, value)
	}
	return body
}
