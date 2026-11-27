package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/common/i18n"
	"github.com/apimgr/citylist/src/database"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

type securityThanksEntry struct {
	Credit   string `json:"credit"`
	Date     string `json:"date"`
	Severity string `json:"severity"`
}

type securityPolicySection struct {
	ID    string
	Title string
	Body  string
}

func securityPolicySections(r *http.Request) []securityPolicySection {
	lang := i18n.LangFromRequest(r)
	return []securityPolicySection{
		{ID: "security-policy-scope", Title: i18n.Translate(lang, "pages.security.policy_scope_title"), Body: i18n.TranslateFormat(lang, "pages.security.policy_scope", map[string]string{"fqdn": requestFQDN(r)})},
		{ID: "security-policy-out-of-scope", Title: i18n.Translate(lang, "pages.security.policy_out_of_scope_title"), Body: i18n.Translate(lang, "pages.security.policy_out_of_scope")},
		{ID: "security-policy-disclosure", Title: i18n.Translate(lang, "pages.security.policy_disclosure_title"), Body: i18n.Translate(lang, "pages.security.policy_disclosure")},
		{ID: "security-policy-safe-harbor", Title: i18n.Translate(lang, "pages.security.policy_safe_harbor_title"), Body: i18n.Translate(lang, "pages.security.policy_safe_harbor")},
		{ID: "security-policy-reporting", Title: i18n.Translate(lang, "pages.security.policy_reporting_title"), Body: i18n.Translate(lang, "pages.security.policy_reporting")},
	}
}

func (s *Server) securityContacts(r *http.Request) ([]map[string]string, string, error) {
	contacts := make([]map[string]string, 0, 3)
	reportURL := s.securityReportURL()
	if reportURL != "" {
		contacts = append(contacts, map[string]string{"type": "github", "value": reportURL})
	}
	if s.cfg.Server.Pages.Contact.Enabled {
		id, err := s.securityID(r.Context(), time.Now().Unix()/172800)
		if err != nil {
			return nil, "", err
		}
		formURL := s.BuildURL(r, "/server/contact") + "?security_id=" + url.QueryEscape(id)
		contacts = append(contacts, map[string]string{"type": "contact_form", "value": formURL})
	}
	configuredEmail := strings.TrimSpace(s.cfg.Web.Security.Contact)
	if configuredEmail == "" {
		configuredEmail = s.cfg.Server.Contact.Security.Email
	}
	email := s.resolveContactEmail(r, configuredEmail)
	if email != "" {
		contacts = append(contacts, map[string]string{"type": "email", "value": email})
	}
	for _, contact := range contacts {
		contact["href"] = contact["value"]
		if contact["type"] == "email" {
			contact["href"] = "mailto:" + contact["value"]
		}
	}
	return contacts, reportURL, nil
}

func (s *Server) securityReportURL() string {
	value := strings.TrimSpace(s.cfg.Web.Security.ReportURL)
	if value == "" {
		value = "https://github.com/apimgr/citylist/security/advisories/new"
	}
	value = strings.ReplaceAll(value, "{project_org}", "apimgr")
	value = strings.ReplaceAll(value, "{project_name}", "citylist")
	return value
}

func (s *Server) securityExpires() string {
	value := strings.TrimSpace(s.cfg.Web.Security.Expires)
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		return parsed.UTC().Format("2006-01-02T00:00:00Z")
	}
	return time.Now().UTC().AddDate(1, 0, 0).Format(time.RFC3339)
}

func (s *Server) securityPolicy(r *http.Request) string {
	if policy := strings.TrimSpace(s.cfg.Web.Security.Policy); policy != "" {
		policy = strings.ReplaceAll(policy, "{fqdn}", requestFQDN(r))
		policy = strings.ReplaceAll(policy, "{app_name}", "CityList API")
		return policy
	}
	var policy strings.Builder
	for _, section := range securityPolicySections(r) {
		policy.WriteString("## ")
		policy.WriteString(section.Title)
		policy.WriteString("\n\n")
		policy.WriteString(section.Body)
		policy.WriteString("\n\n")
	}
	return strings.TrimSpace(policy.String())
}

func (s *Server) projectPGPPublic(ctx context.Context) (*projectPGPKeypair, error) {
	return s.LoadProjectPGPKeypair(ctx)
}

func (s *Server) handleSecurityPage(w http.ResponseWriter, r *http.Request) {
	if !securityReadMethod(s, w, r) {
		return
	}
	contacts, _, err := s.securityContacts(r)
	if err != nil {
		s.respondError(w, http.StatusServiceUnavailable, "SECURITY_UNAVAILABLE", i18n.Translate(i18n.LangFromRequest(r), "errors.unavailable"))
		return
	}
	var key *projectPGPKeypair
	var keyErr error
	if s.cfg.Web.Security.PublishPGPKey {
		key, keyErr = s.projectPGPPublic(r.Context())
	}
	available := keyErr == nil && key != nil
	if keyErr != nil && !errors.Is(keyErr, os.ErrNotExist) {
		s.respondError(w, http.StatusServiceUnavailable, "SECURITY_UNAVAILABLE", i18n.Translate(i18n.LangFromRequest(r), "errors.unavailable"))
		return
	}
	pgpURL := s.BuildURL(r, "/.well-known/pgp-key.asc")
	policyURL := s.BuildURL(r, "/server/security/policy")
	thanksURL := s.BuildURL(r, "/server/security/thanks")
	textURL := s.BuildURL(r, "/.well-known/security.txt")
	reportURL := ""
	if s.cfg.Server.Pages.Contact.Enabled {
		reportURL = s.BuildURL(r, "/server/contact")
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		pgp := map[string]interface{}{"available": available, "url": nil, "fingerprint": nil}
		if available {
			pgp["url"] = pgpURL
			pgp["fingerprint"] = key.Fingerprint
		}
		data := map[string]interface{}{
			"contacts": contacts, "expires": s.securityExpires(), "pgp": pgp,
			"policy_url": policyURL, "thanks_url": thanksURL,
		}
		if reportURL != "" {
			data["report_url"] = reportURL
		}
		s.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: data})
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return
	}
	s.renderPage(w, r, "security.tmpl", map[string]interface{}{
		"Title": i18n.Translate(i18n.LangFromRequest(r), "pages.security.title"), "Contacts": contacts, "Expires": s.securityExpires(), "PGPAvailable": available,
		"PGPURL": pgpURL, "PGPFingerprint": func() string {
			if available {
				return key.Fingerprint
			}
			return ""
		}(),
		"PolicyURL": policyURL, "ThanksURL": thanksURL, "SecurityTxtURL": textURL, "ReportURL": reportURL,
		"Version": s.version, "NextTheme": nextTheme(themePreference(r)),
	})
}

func (s *Server) handleSecurityPolicy(w http.ResponseWriter, r *http.Request) {
	if !securityReadMethod(s, w, r) {
		return
	}
	policy := s.securityPolicy(r)
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: map[string]string{"policy": policy}})
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return
	}
	pageData := map[string]interface{}{
		"Title": i18n.Translate(i18n.LangFromRequest(r), "pages.security.policy_title"), "Policy": policy,
		"PolicySections": securityPolicySections(r), "Version": s.version,
		"NextTheme": nextTheme(themePreference(r)),
	}
	if strings.TrimSpace(s.cfg.Web.Security.Policy) != "" {
		pageData["PolicyHTML"] = template.HTML(renderSecurityPolicyMarkdown(policy))
	}
	s.renderPage(w, r, "security-policy.tmpl", pageData)
}

func renderSecurityPolicyMarkdown(markdown string) string {
	var rendered bytes.Buffer
	parser := goldmark.New(goldmark.WithExtensions(extension.GFM))
	if err := parser.Convert([]byte(markdown), &rendered); err != nil {
		return ""
	}
	policy := bluemonday.NewPolicy()
	policy.AllowStandardURLs()
	policy.AllowRelativeURLs(true)
	policy.AllowElements("p", "br", "hr", "blockquote", "ul", "ol", "li", "h1", "h2", "h3", "h4", "h5", "h6", "strong", "b", "em", "i", "del", "s", "code", "pre", "table", "thead", "tbody", "tr", "th", "td")
	policy.AllowAttrs("href", "title").OnElements("a")
	policy.AllowAttrs("align").Matching(regexp.MustCompile("^(left|center|right)$")).OnElements("th", "td")
	policy.AllowAttrs("class").Matching(regexp.MustCompile("^language-[a-zA-Z0-9_-]+$")).OnElements("code")
	sanitized := policy.Sanitize(rendered.String())
	linkTag := regexp.MustCompile(`(?i)<a\b([^>]*)>`)
	return linkTag.ReplaceAllStringFunc(sanitized, func(tag string) string {
		if !strings.Contains(strings.ToLower(tag), "href=") {
			return tag
		}
		return strings.TrimSuffix(tag, ">") + ` rel="noopener noreferrer nofollow ugc">`
	})
}

func (s *Server) handleSecurityThanks(w http.ResponseWriter, r *http.Request) {
	if !securityReadMethod(s, w, r) {
		return
	}
	entries := make([]securityThanksEntry, 0)
	if s.store != nil {
		err := s.store.Read(r.Context(), func(ctx context.Context, reader database.Reader) error {
			rows, err := reader.QueryContext(ctx, `SELECT credit, disclosed_on, severity FROM security_thanks ORDER BY disclosed_on DESC, tracking_id DESC`)
			if err != nil {
				return err
			}
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var entry securityThanksEntry
				var date string
				if err := rows.Scan(&entry.Credit, &date, &entry.Severity); err != nil {
					return err
				}
				entry.Date = date
				if parsed, err := time.Parse(time.RFC3339, date); err == nil {
					entry.Date = parsed.UTC().Format("2006-01-02")
				} else if parsed, err := time.Parse("2006-01-02 15:04:05-07:00", date); err == nil {
					entry.Date = parsed.UTC().Format("2006-01-02")
				}
				entries = append(entries, entry)
			}
			return rows.Err()
		})
		if err != nil {
			s.respondError(w, http.StatusServiceUnavailable, "SECURITY_UNAVAILABLE", i18n.Translate(i18n.LangFromRequest(r), "errors.unavailable"))
			return
		}
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: map[string]interface{}{"entries": entries}})
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return
	}
	s.renderPage(w, r, "security-thanks.tmpl", map[string]interface{}{
		"Title": i18n.Translate(i18n.LangFromRequest(r), "pages.security.thanks_title"), "Entries": entries, "Version": s.version,
		"NextTheme": nextTheme(themePreference(r)),
	})
}

func (s *Server) handleProjectPGPKey(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Web.Security.PublishPGPKey {
		s.respondError(w, http.StatusNotFound, "NOT_FOUND", i18n.Translate(i18n.LangFromRequest(r), "errors.not_found"))
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "This resource accepts GET and HEAD requests")
		return
	}
	key, err := s.projectPGPPublic(r.Context())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.respondError(w, http.StatusNotFound, "NOT_FOUND", i18n.Translate(i18n.LangFromRequest(r), "errors.not_found"))
			return
		}
		s.respondError(w, http.StatusServiceUnavailable, "SECURITY_UNAVAILABLE", i18n.Translate(i18n.LangFromRequest(r), "errors.unavailable"))
		return
	}
	w.Header().Set("Content-Type", "application/pgp-keys; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(key.PublicArmor)
}

func (s *Server) securityTxt(w http.ResponseWriter, r *http.Request) {
	contacts, _, err := s.securityContacts(r)
	if err != nil {
		s.respondError(w, http.StatusServiceUnavailable, "SECURITY_UNAVAILABLE", i18n.Translate(i18n.LangFromRequest(r), "errors.unavailable"))
		return
	}
	var content strings.Builder
	for _, contact := range contacts {
		value := contact["value"]
		if contact["type"] == "email" {
			value = "mailto:" + value
		}
		fmt.Fprintf(&content, "Contact: %s\n", value)
	}
	fmt.Fprintf(&content, "Expires: %s\nPolicy: %s\n", s.securityExpires(), s.BuildURL(r, "/server/security/policy"))
	if s.cfg.Web.Security.PublishPGPKey {
		key, err := s.projectPGPPublic(r.Context())
		if err == nil {
			fmt.Fprintf(&content, "Encryption: %s\n", s.BuildURL(r, "/.well-known/pgp-key.asc"))
			fmt.Fprintf(&content, "# OpenPGP fingerprint: %s\n", key.Fingerprint)
		} else if !errors.Is(err, os.ErrNotExist) {
			s.respondError(w, http.StatusServiceUnavailable, "SECURITY_UNAVAILABLE", i18n.Translate(i18n.LangFromRequest(r), "errors.unavailable"))
			return
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(content.String()))
}

func securityReadMethod(s *Server, w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "This page accepts GET and HEAD requests")
	return false
}
