package server

import (
	"encoding/json"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var metaNamePattern = regexp.MustCompile(`^[a-z0-9:_.-]+$`)
var legacySEOTagPattern = regexp.MustCompile(`(?is)<title\b[^>]*>.*?</title\s*>|<meta\s+(?:name|property)="(?:description|robots|keywords|author|og:[^"]+|twitter:[^"]+)"[^>]*>\s*`)

func (s *Server) robotsDirective(requestPath string) string {
	if s.cfg.Server.SEO.NoIndex {
		return "noindex,nofollow"
	}
	for _, page := range serverPageRegistry {
		if page.Path == requestPath && page.Sitemap {
			return "index,follow"
		}
	}
	return "noindex,nofollow"
}

func (s *Server) brandTitle() string {
	if title := strings.TrimSpace(s.cfg.Server.Branding.Title); title != "" {
		return title
	}
	return "CityList API"
}

func (s *Server) applyRobotsHeader(w http.ResponseWriter, r *http.Request) {
	requestPath := r.URL.Path
	if s.baseURL != "/" {
		requestPath = strings.TrimPrefix(requestPath, strings.TrimSuffix(s.baseURL, "/"))
		if requestPath == "" {
			requestPath = "/"
		}
	}
	if s.robotsDirective(requestPath) == "noindex,nofollow" {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	}
}

func (s *Server) seoHead(r *http.Request, requestPath, pageTitle, pageDescription string) template.HTML {
	branding, seo := s.cfg.Server.Branding, s.cfg.Server.SEO
	title := s.brandTitle()
	if strings.TrimSpace(branding.Tagline) != "" {
		title += seo.TitleSeparator + strings.TrimSpace(branding.Tagline)
	}
	description := strings.TrimSpace(branding.Description)
	if description == "" {
		description = pageDescription
	}
	if description == "" {
		description = pageTitle
	}
	canonical := s.BuildURL(r, requestPath)
	if seo.Canonical != "" {
		if parsed, err := url.Parse(seo.Canonical); err == nil && parsed.IsAbs() && parsed.Host != "" && parsed.Scheme == "https" {
			canonical = strings.TrimSuffix(seo.Canonical, "/") + requestPath
		}
	}
	robots := s.robotsDirective(requestPath)
	var out strings.Builder
	out.WriteString(`<title>` + html.EscapeString(title) + `</title>` + "\n")
	out.WriteString(`<link rel="icon" href="` + html.EscapeString(s.baseURL+"favicon.ico") + `" type="image/svg+xml">` + "\n")
	writeMeta := func(attribute, value, content string) {
		if strings.TrimSpace(content) == "" {
			return
		}
		out.WriteString(`<meta ` + attribute + `="` + html.EscapeString(value) + `" content="` + html.EscapeString(content) + `">` + "\n")
	}
	writeMeta("name", "description", description)
	writeMeta("name", "robots", robots)
	writeMeta("name", "keywords", strings.Join(seo.Keywords, ", "))
	writeMeta("name", "author", seo.Author)
	out.WriteString(`<link rel="canonical" href="` + html.EscapeString(canonical) + `">` + "\n")
	for name, content := range seo.MetaTags {
		if metaNamePattern.MatchString(name) && !isGeneratedMetaName(name) {
			writeMeta("name", name, content)
		}
	}
	if seo.OpenGraph.Enabled {
		writeMeta("property", "og:title", title)
		writeMeta("property", "og:description", description)
		writeMeta("property", "og:type", firstNonEmpty(seo.OpenGraph.Type, "website"))
		writeMeta("property", "og:url", canonical)
		writeMeta("property", "og:site_name", firstNonEmpty(seo.OpenGraph.SiteName, branding.Title))
		writeMeta("property", "og:image", seo.OGImage)
	}
	if seo.Twitter.Enabled {
		writeMeta("name", "twitter:card", firstNonEmpty(seo.Twitter.Card, "summary_large_image"))
		writeMeta("name", "twitter:title", title)
		writeMeta("name", "twitter:description", description)
		writeMeta("name", "twitter:image", seo.OGImage)
		writeMeta("name", "twitter:site", seo.TwitterHandle)
		writeMeta("name", "twitter:creator", seo.Twitter.Creator)
	}
	verification := seo.Verification
	writeMeta("name", "google-site-verification", verification.Google)
	writeMeta("name", "msvalidate.01", verification.Bing)
	writeMeta("name", "yandex-verification", verification.Yandex)
	writeMeta("name", "baidu-site-verification", verification.Baidu)
	writeMeta("name", "p:domain_verify", verification.Pinterest)
	writeMeta("property", "fb:domain_verification", verification.Facebook)
	for _, tag := range verification.Custom {
		if tag.Name != "" {
			writeMeta("name", tag.Name, tag.Content)
		} else if tag.Property != "" {
			writeMeta("property", tag.Property, tag.Content)
		}
	}
	structuredData, _ := json.Marshal(map[string]string{
		"@context": "https://schema.org", "@type": "WebSite", "name": title,
		"description": description, "url": s.BuildURL(r, requestPath),
	})
	out.WriteString(`<script type="application/ld+json">` + string(structuredData) + `</script>` + "\n")
	return template.HTML(out.String())
}

func stripLegacySEOTags(content string) string {
	return legacySEOTagPattern.ReplaceAllString(content, "")
}

func isGeneratedMetaName(name string) bool {
	switch name {
	case "description", "keywords", "author", "robots", "viewport":
		return true
	default:
		return false
	}
}

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
