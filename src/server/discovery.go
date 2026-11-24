package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/apimgr/citylist/src/common/i18n"
	"github.com/go-chi/chi/v5"
)

func (s *Server) handleLLMSTxt(w http.ResponseWriter, r *http.Request) {
	lang := i18n.LangFromRequest(r)
	apiPrefix := "/api/" + s.cfg.Server.APIVersion
	apiBase := s.BuildURL(r, apiPrefix)
	endpoints := make([]string, 0)
	_ = chi.Walk(s.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/api/") {
			endpoints = append(endpoints, fmt.Sprintf("- %s %s", method, s.BuildURL(r, route)))
		}
		return nil
	})
	endpoints = append(endpoints, "- GET "+s.BuildURL(r, "/server/healthz"))
	sort.Strings(endpoints)
	apiContact := strings.TrimSpace(s.cfg.Web.APIContact)
	if apiContact == "" {
		apiContact = "https://github.com/apimgr/citylist/issues"
	}
	securityContact := strings.TrimSpace(s.cfg.Web.Security.Contact)
	if securityContact != "" {
		securityContact = "mailto:" + s.resolveContactEmail(r, securityContact)
	} else {
		securityContact = s.securityReportURL()
	}
	lines := []string{
		"# " + i18n.Translate(lang, "discovery.title"),
		"> " + i18n.Translate(lang, "discovery.summary"),
		"",
		"## " + i18n.Translate(lang, "discovery.api"),
		i18n.Translate(lang, "discovery.base_url") + ": " + apiBase,
		i18n.Translate(lang, "discovery.authentication"),
		i18n.TranslateFormat(lang, "discovery.rate_limit", map[string]string{"limit": fmt.Sprint(requestsPerMinute)}),
		i18n.TranslateFormat(lang, "discovery.mcp", map[string]string{"url": s.BuildURL(r, "/mcp")}),
		i18n.TranslateFormat(lang, "discovery.graphql", map[string]string{"url": s.BuildURL(r, "/api/graphql")}),
		i18n.TranslateFormat(lang, "discovery.openapi", map[string]string{"url": s.BuildURL(r, apiPrefix+"/server/swagger")}),
		"",
		"## " + i18n.Translate(lang, "discovery.endpoints"),
	}
	lines = append(lines, endpoints...)
	lines = append(lines,
		"",
		"## "+i18n.Translate(lang, "discovery.capabilities"),
		"- "+i18n.Translate(lang, "discovery.cities"),
		"- "+i18n.Translate(lang, "discovery.zip_codes"),
		"- "+i18n.Translate(lang, "discovery.interfaces"),
		"- "+i18n.Translate(lang, "discovery.embedded_data"),
		"",
		"## "+i18n.Translate(lang, "discovery.contact"),
		i18n.TranslateFormat(lang, "discovery.api_issues", map[string]string{"url": apiContact}),
		i18n.TranslateFormat(lang, "discovery.security", map[string]string{"url": securityContact}),
	)
	text := strings.Join(lines, "\n") + "\n"
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return
	}
	s.respondText(w, text)
}
