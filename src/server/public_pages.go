package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/common/i18n"
	"github.com/apimgr/citylist/src/config"
)

type publicPageData struct {
	Title             string
	BrandTitle        string
	TitleKey          string
	Lead              string
	Version           string
	Body              []string
	BaseURL           string
	Theme             string
	Lang              string
	Dir               string
	ReturnTo          string
	CSRFToken         string
	ConsentBanner     template.HTML
	SiteBanners       template.HTML
	SecurityReportURL string
	CurrentPath       string
	LanguageName      string
	NextTheme         string
	SectionTitles     []string
	HubPages          []hubPage
	SEOHead           template.HTML
	HasAccent         bool
	PrivacyPage       bool
	GPCNotice         bool
	PrivacySignal     string
	DataSold          bool
	CCPAOptOut        bool
	ShowConsentPrefs  bool
	AboutPage         bool
	TermsPage         bool
	PrivacySections   []privacyPageSection
	BuildDate         string
	AttributionHTML   template.HTML
	OperatorHTML      template.HTML
	AboutFeatures     []string
	AboutLinks        []hubPage
	PrivacyContact    string
	PrivacyContactURL string
	TrackingScript    template.HTML
	TrackingNoscript  template.HTML
	OperatorHeader    template.HTML
	OperatorFooter    template.HTML
	HelpPage          bool
	HelpExamples      []helpPageExample
	TorAddress        string
	I2PAddress        string
}

type helpPageExample struct {
	Title   string
	Command string
}

type privacyPageSection struct {
	Title    string
	Body     []string
	Services []config.PrivacyThirdPartyService
}

type hubPage struct {
	Path  string
	Title string
	Desc  string
}

type contactTypeChoice struct {
	Type  string
	Title string
}

type contactField struct {
	Name      string
	Label     string
	Help      string
	Type      string
	Value     string
	Required  bool
	MaxLength int
	Options   []contactOption
}

type contactOption struct {
	Value string
	Label string
}

type contactPageView struct {
	Type                    string
	TypeTitle               string
	Types                   []contactTypeChoice
	Fields                  []contactField
	FormToken               string
	ReviewToken             string
	CaptchaID               string
	CaptchaQuestion         string
	CaptchaProvider         string
	CaptchaSiteKey          string
	CaptchaFallbackID       string
	CaptchaFallbackQuestion string
	SecurityURL             string
	AbuseEmail              string
	FQDN                    string
	CSRFToken               string
	ReturnTo                string
	Review                  bool
	SubmittedFields         []contactField
	SecurityMode            bool
	FormError               string
	FormNotice              string
}

func preferenceHubPages(lang string) []hubPage {
	return registeredHubPages(lang, "preferences")
}

func helpHubPages(lang string) []hubPage {
	return registeredHubPages(lang, "help")
}

var publicPageTemplate = mustParseEmbeddedTemplate("template/page/public-page.tmpl", template.FuncMap{
	"t":     i18n.Translate,
	"asset": assetURL,
})

func (s *Server) handleAboutPage(w http.ResponseWriter, r *http.Request) {
	s.publicPage(w, r, "pages.about.title", "pages.about.lead", []string{
		"pages.about.description_title", "pages.about.data_title", "pages.about.interfaces_title",
	}, []string{
		"pages.about.description", "pages.about.data", "pages.about.interfaces",
	})
}

func (s *Server) handleHelpPage(w http.ResponseWriter, r *http.Request) {
	lang := i18n.LangFromRequest(r)
	apiPrefix := "/api/" + s.cfg.Server.APIVersion
	examples := []helpPageExample{
		{Title: i18n.Translate(lang, "pages.help.example_search"), Command: "curl -q -LSsf '" + s.BuildURL(r, apiPrefix+"/cities/search?q=London") + "'"},
		{Title: i18n.Translate(lang, "pages.help.example_nearby"), Command: "curl -q -LSsf '" + s.BuildURL(r, apiPrefix+"/cities/nearby?lat=51.5072&lon=-0.1276&radius=10") + "'"},
		{Title: i18n.Translate(lang, "pages.help.example_zip"), Command: "curl -q -LSsf '" + s.BuildURL(r, apiPrefix+"/zipcodes/10001") + "'"},
	}
	torAddress, i2pAddress := "", ""
	if s.torManager != nil {
		snapshot := s.torManager.Snapshot()
		if snapshot.Enabled && snapshot.Running && strings.HasSuffix(strings.ToLower(snapshot.Hostname), ".onion") {
			torAddress = snapshot.Hostname
		}
	}
	if s.i2pManager != nil {
		snapshot := s.i2pManager.Snapshot()
		if snapshot.Enabled && snapshot.Running && strings.HasSuffix(strings.ToLower(snapshot.Hostname), ".b32.i2p") {
			i2pAddress = snapshot.Hostname
		}
	}
	s.publicHelpPage(w, r, examples, torAddress, i2pAddress)
}

func (s *Server) handleContactPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Cache-Control", "no-store")
	if !s.contactEnabled(w, r) {
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", i18n.Translate(i18n.LangFromRequest(r), "errors.method_not_allowed"))
		return
	}
	s.applyRobotsHeader(w, r)
	lang := i18n.LangFromRequest(r)
	securityID := strings.TrimSpace(r.URL.Query().Get("security_id"))
	security := securityID != "" && s.validSecurityID(r.Context(), securityID)
	if securityID != "" && !security {
		s.logInvalidSecurityID(r, securityID)
		securityID = ""
	}
	kind := strings.TrimSpace(r.URL.Query().Get("type"))
	if security {
		kind = "security"
	}
	view := contactPageView{
		Types: s.contactTypes(false, lang), SecurityURL: s.BuildURL(r, "/server/security"),
		AbuseEmail: s.publicAbuseEmail(r), FQDN: requestFQDN(r),
	}
	if token, err := s.newContactFormToken(r.Context()); err == nil {
		view.FormToken = token
	} else {
		s.respondError(w, http.StatusServiceUnavailable, "CONTACT_UNAVAILABLE", i18n.Translate(i18n.LangFromRequest(r), "errors.unavailable"))
		return
	}
	if !contactTypeOffered(kind, security) {
		s.renderContactPage(w, r, view)
		return
	}
	view.Type, view.TypeTitle, view.SecurityMode = kind, i18n.Translate(lang, "contact.type_"+kind), security
	view.Fields = s.contactFields(kind, lang, nil, securityID)
	captcha, err := s.contactCaptcha(r.Context(), r)
	if err != nil {
		s.respondError(w, http.StatusServiceUnavailable, "CONTACT_UNAVAILABLE", i18n.Translate(i18n.LangFromRequest(r), "errors.unavailable"))
		return
	}
	view.CaptchaProvider = fmt.Sprint(captcha["provider"])
	view.CaptchaID, _ = captcha["captcha_id"].(string)
	view.CaptchaQuestion, _ = captcha["question"].(string)
	view.CaptchaSiteKey, _ = captcha["site_key"].(string)
	if fallback, ok := captcha["fallback"].(map[string]string); ok {
		view.CaptchaFallbackID = fallback["captcha_id"]
		view.CaptchaFallbackQuestion = fallback["question"]
	}
	s.renderContactPage(w, r, view)
}

func (s *Server) handlePrivacyPage(w http.ResponseWriter, r *http.Request) {
	lang := i18n.LangFromRequest(r)
	privacy := s.cfg.Server.Privacy
	tracking := s.cfg.Server.Tracking
	trackingEnabled := tracking.Type != ""
	cookieDescriptions := []string{privacy.Cookies.Essential.Description, privacy.Cookies.Preferences.Description}
	if trackingEnabled {
		cookieDescriptions = append(cookieDescriptions, privacy.GetAnalyticsDescription()+" "+i18n.TranslateFormat(lang, "pages.privacy.analytics_provider", map[string]string{"provider": tracking.TypeName()}))
	} else {
		cookieDescriptions = append(cookieDescriptions, i18n.Translate(lang, "pages.privacy.analytics_disabled"))
	}
	sections := []privacyPageSection{
		{Title: i18n.Translate(lang, "pages.privacy.summary_title"), Body: []string{
			i18n.Translate(lang, map[bool]string{true: "pages.privacy.stored_on_server", false: "pages.privacy.storage_unspecified"}[privacy.Data.StoredOnServer]),
			i18n.Translate(lang, map[bool]string{true: "pages.privacy.data_sold", false: "pages.privacy.data_not_sold"}[privacy.Data.Sold]),
		}},
		{Title: i18n.Translate(lang, "pages.privacy.cookies_title"), Body: cookieDescriptions},
		{Title: i18n.Translate(lang, "pages.privacy.collection_title"), Body: []string{
			i18n.Translate(lang, "pages.privacy.collection"),
		}},
		{Title: i18n.Translate(lang, "pages.privacy.usage_title"), Body: []string{
			i18n.Translate(lang, map[bool]string{true: "pages.privacy.usage_sold", false: "pages.privacy.usage_not_sold"}[privacy.Data.Sold]),
		}},
		{Title: i18n.Translate(lang, "pages.privacy.security_title"), Body: []string{
			i18n.Translate(lang, "pages.privacy.security"),
		}},
		{Title: i18n.Translate(lang, "pages.privacy.storage_title"), Body: privacySharingText(lang, privacy.Data.StoredOnServer, privacy.Data.Sharing)},
		{Title: i18n.Translate(lang, "pages.privacy.retention_title"), Body: []string{privacy.Retention.Period}},
	}
	thirdParty := append([]config.PrivacyThirdPartyService(nil), privacy.ThirdParty.Services...)
	if trackingEnabled {
		thirdParty = append(thirdParty, config.PrivacyThirdPartyService{Name: tracking.TypeName(), Purpose: i18n.Translate(lang, "pages.privacy.analytics_purpose"), DataSent: i18n.Translate(lang, "pages.privacy.analytics_data")})
	}
	thirdPartySection := privacyPageSection{Title: i18n.Translate(lang, "pages.privacy.third_party_title"), Services: thirdParty}
	if len(thirdParty) == 0 {
		thirdPartySection.Body = []string{i18n.Translate(lang, "pages.privacy.no_third_party")}
	}
	sections = append(sections, thirdPartySection)
	rights := []string{i18n.Translate(lang, "pages.privacy.access_right")}
	if privacy.Retention.ExportAvailable {
		rights = append(rights, i18n.Translate(lang, "pages.privacy.export_right"))
	}
	if privacy.Retention.DeletionAvailable {
		rights = append(rights, i18n.Translate(lang, "pages.privacy.deletion_right"))
	}
	sections = append(sections, privacyPageSection{Title: i18n.Translate(lang, "pages.privacy.rights_title"), Body: rights})
	if privacy.Data.Sold {
		sections = append(sections, privacyPageSection{Title: i18n.Translate(lang, "pages.privacy.ccpa_title"), Body: []string{i18n.Translate(lang, "pages.privacy.ccpa_description")}})
	}
	contact := strings.TrimSpace(s.cfg.Server.Contact.DPO.Email)
	contactName := strings.TrimSpace(s.cfg.Server.Contact.DPO.Name)
	if contact == "" {
		contact = strings.TrimSpace(s.cfg.Server.Contact.General.Email)
		contactName = ""
	}
	contactURL := ""
	if contact != "" {
		contact = s.resolveContactEmail(r, contact)
		contactURL = "mailto:" + contact
		if contactName != "" {
			contact = contactName + " — " + contact
		}
	} else {
		contact = i18n.Translate(lang, "pages.privacy.security_contact")
		contactURL = "/server/security"
	}
	s.publicPageWithExtras(w, r, "pages.privacy.title", "pages.privacy.lead", nil, nil, sections, contact, contactURL)
}

func privacySharingText(lang string, stored bool, sharing []config.PrivacySharingCondition) []string {
	result := []string{i18n.Translate(lang, map[bool]string{true: "pages.privacy.storage_server", false: "pages.privacy.storage_unspecified"}[stored])}
	for _, condition := range sharing {
		result = append(result, strings.TrimSpace(condition.Condition+": "+condition.When+" — "+condition.Data))
	}
	return result
}

func (s *Server) handleTermsPage(w http.ResponseWriter, r *http.Request) {
	s.publicPage(w, r, "pages.terms.title", "pages.terms.lead", []string{
		"pages.terms.acceptance_title", "pages.terms.use_title", "pages.terms.liability_title",
		"pages.terms.changes_title", "pages.terms.governing_law_title",
	}, []string{
		"pages.terms.acceptance", "pages.terms.use", "pages.terms.liability",
		"pages.terms.changes", "pages.terms.governing_law",
	})
}

func (s *Server) publicPage(w http.ResponseWriter, r *http.Request, titleKey, leadKey string, sectionKeys, bodyKeys []string) {
	lang := i18n.LangFromRequest(r)
	titles := make([]string, len(sectionKeys))
	body := make([]string, len(bodyKeys))
	for index, key := range sectionKeys {
		titles[index] = i18n.Translate(lang, key)
	}
	for index, key := range bodyKeys {
		body[index] = i18n.Translate(lang, key)
	}
	s.publicPageWithValues(w, r, titleKey, leadKey, titles, body)
}

func (s *Server) publicPageWithValues(w http.ResponseWriter, r *http.Request, titleKey, leadKey string, sectionTitles, body []string) {
	s.publicPageWithExtras(w, r, titleKey, leadKey, sectionTitles, body, nil, "", "")
}

func (s *Server) publicPageWithExtras(w http.ResponseWriter, r *http.Request, titleKey, leadKey string, sectionTitles, body []string, privacySections []privacyPageSection, privacyContact, privacyContactURL string) {
	s.publicPageWithDetails(w, r, titleKey, leadKey, sectionTitles, body, privacySections, privacyContact, privacyContactURL, nil, "", "")
}

func (s *Server) publicHelpPage(w http.ResponseWriter, r *http.Request, examples []helpPageExample, torAddress, i2pAddress string) {
	s.publicPageWithDetails(w, r, "pages.help.title", "pages.help.lead", nil, nil, nil, "", "", examples, torAddress, i2pAddress)
}

func (s *Server) publishedOverlayAddresses() (string, string) {
	torAddress, i2pAddress := "", ""
	if s.torManager != nil {
		snapshot := s.torManager.Snapshot()
		if snapshot.Enabled && snapshot.Running && strings.HasSuffix(strings.ToLower(snapshot.Hostname), ".onion") {
			torAddress = snapshot.Hostname
		}
	}
	if s.i2pManager != nil {
		snapshot := s.i2pManager.Snapshot()
		if snapshot.Enabled && snapshot.Running && strings.HasSuffix(strings.ToLower(snapshot.Hostname), ".b32.i2p") {
			i2pAddress = snapshot.Hostname
		}
	}
	return torAddress, i2pAddress
}

func (s *Server) publicPageWithDetails(w http.ResponseWriter, r *http.Request, titleKey, leadKey string, sectionTitles, body []string, privacySections []privacyPageSection, privacyContact, privacyContactURL string, helpExamples []helpPageExample, torAddress, i2pAddress string) {
	if torAddress == "" && i2pAddress == "" {
		torAddress, i2pAddress = s.publishedOverlayAddresses()
	}
	lang := i18n.LangFromRequest(r)
	title := i18n.Translate(i18n.LangFromRequest(r), titleKey)
	lead := i18n.Translate(lang, leadKey)
	if titleKey == "pages.about.title" {
		if configured := strings.TrimSpace(s.cfg.Server.Branding.Tagline); configured != "" {
			lead = configured
		}
		if len(body) > 0 && strings.TrimSpace(s.cfg.Server.Branding.Description) != "" {
			body[0] = s.cfg.Server.Branding.Description
		}
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", i18n.Translate(i18n.LangFromRequest(r), "errors.method_not_allowed"))
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		if titleKey == "pages.help.title" {
			registeredPages := helpHubPages(lang)
			pageItems := make([]map[string]string, 0, len(registeredPages))
			for _, page := range registeredPages {
				pageItems = append(pageItems, map[string]string{"title": page.Title, "path": page.Path})
			}
			content := s.cfg.Server.Pages.Help.Content
			if strings.TrimSpace(content) == "" {
				content = i18n.Translate(lang, "pages.help.default_content")
			}
			s.respondJSON(w, http.StatusOK, map[string]interface{}{"title": title, "content": content, "format": "markdown", "pages": pageItems})
			return
		}
		if titleKey == "pages.about.title" {
			content, source := s.operatorPageSource(r, "about")
			format := strings.ToLower(strings.TrimSpace(content.Format))
			switch format {
			case "", "md":
				format = "markdown"
			case "txt":
				format = "text"
			}
			if source == "" {
				var builtIn strings.Builder
				fmt.Fprintf(&builtIn, "# %s\n\n%s\n\n", s.brandTitle(), lead)
				for index, paragraph := range body {
					fmt.Fprintf(&builtIn, "## %s\n\n%s\n\n", sectionTitles[index], paragraph)
				}
				builtIn.WriteString("## " + i18n.Translate(lang, "pages.about.features_title") + "\n\n")
				for _, featureKey := range []string{"pages.about.feature_search", "pages.about.feature_lookup", "pages.about.feature_browser", "pages.about.feature_apis", "pages.about.feature_cli", "pages.about.feature_tokens"} {
					builtIn.WriteString("- " + i18n.Translate(lang, featureKey) + "\n")
				}
				builtIn.WriteString("\n## " + i18n.Translate(lang, "pages.about.links_title") + "\n\n- [GitHub](https://github.com/apimgr/citylist)\n- [Documentation](https://apimgr-citylist.readthedocs.io)\n")
				source, format = builtIn.String(), "markdown"
			}
			s.respondJSON(w, http.StatusOK, map[string]interface{}{"title": title, "content": source, "format": format})
			return
		}
		if titleKey == "pages.privacy.title" {
			privacy := s.cfg.Server.Privacy
			cookie, cookieErr := r.Cookie("ccpa_opt_out")
			sharing := make([]map[string]string, 0, len(privacy.Data.Sharing))
			for _, item := range privacy.Data.Sharing {
				sharing = append(sharing, map[string]string{"condition": item.Condition, "when": item.When, "data": item.Data})
			}
			thirdPartyServices := append([]config.PrivacyThirdPartyService(nil), privacy.ThirdParty.Services...)
			tracking := s.cfg.Server.Tracking
			if tracking.Type != "" {
				thirdPartyServices = append(thirdPartyServices, config.PrivacyThirdPartyService{Name: tracking.TypeName(), Purpose: i18n.Translate(lang, "pages.privacy.analytics_purpose"), DataSent: i18n.Translate(lang, "pages.privacy.analytics_data")})
			}
			trackingDescription := privacy.GetAnalyticsDescription()
			if tracking.Type == "" {
				trackingDescription = i18n.Translate(lang, "pages.privacy.analytics_disabled")
			}
			s.respondJSON(w, http.StatusOK, map[string]interface{}{
				"title":   title,
				"summary": map[string]interface{}{"data_stored_on_server": privacy.Data.StoredOnServer, "data_sold": privacy.Data.Sold, "user_control": true},
				"cookies": map[string]interface{}{
					"essential":   map[string]interface{}{"enabled": privacy.Cookies.Essential.Enabled, "description": privacy.Cookies.Essential.Description},
					"preferences": map[string]interface{}{"enabled": privacy.Cookies.Preferences.Enabled, "description": privacy.Cookies.Preferences.Description},
					"analytics":   map[string]interface{}{"enabled": privacy.Cookies.Analytics.Enabled && tracking.Type != "", "description": trackingDescription},
				},
				"data":        map[string]interface{}{"sold": privacy.Data.Sold, "stored_on_server": privacy.Data.StoredOnServer, "sharing": sharing},
				"tracking":    map[string]interface{}{"enabled": tracking.Type != "", "type": tracking.Type, "type_name": tracking.TypeName()},
				"retention":   map[string]interface{}{"period": privacy.Retention.Period, "export_available": privacy.Retention.ExportAvailable, "deletion_available": privacy.Retention.DeletionAvailable},
				"third_party": map[string]interface{}{"services": thirdPartyServices},
				"ccpa":        map[string]interface{}{"applicable": privacy.Data.Sold, "opt_out_url": "/server/privacy#ccpa-opt-out", "user_opted_out": cookieErr == nil && strings.EqualFold(cookie.Value, "true")},
				"content":     map[string]string{"consent_message": privacy.GetConsentMessage(), "data_usage": i18n.Translate(lang, map[bool]string{true: "pages.privacy.usage_sold", false: "pages.privacy.usage_not_sold"}[privacy.Data.Sold])},
			})
			return
		}
		if titleKey == "pages.terms.title" {
			content, source := s.operatorPageSource(r, "terms")
			format := strings.ToLower(strings.TrimSpace(content.Format))
			switch format {
			case "", "md":
				format = "markdown"
			case "txt":
				format = "text"
			}
			if source == "" {
				var builtIn strings.Builder
				for index, paragraph := range body {
					fmt.Fprintf(&builtIn, "## %s\n\n%s\n\n", sectionTitles[index], paragraph)
				}
				source, format = builtIn.String(), "markdown"
			}
			s.respondJSON(w, http.StatusOK, map[string]interface{}{"title": title, "content": source, "format": format})
			return
		}
		s.respondJSON(w, http.StatusOK, map[string]interface{}{"title": title, "version": s.version, "content": body})
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/plain") {
		var output strings.Builder
		output.WriteString(title + " - CityList\n\n")
		if titleKey == "pages.help.title" {
			output.WriteString(lead + "\n\n")
			for _, example := range helpExamples {
				output.WriteString(example.Title + "\n" + example.Command + "\n\n")
			}
			for _, page := range helpHubPages(lang) {
				output.WriteString(page.Title + ": " + s.BuildURL(r, page.Path) + "\n")
			}
			s.respondText(w, output.String())
			return
		}
		for _, paragraph := range body {
			output.WriteString(paragraph + "\n\n")
		}
		for _, section := range privacySections {
			output.WriteString(section.Title + "\n")
			for _, paragraph := range section.Body {
				output.WriteString(paragraph + "\n")
			}
			output.WriteString("\n")
		}
		if titleKey == "pages.about.title" {
			output.WriteString(i18n.Translate(lang, "pages.about.features_title") + "\n")
			for _, key := range []string{"pages.about.feature_search", "pages.about.feature_lookup", "pages.about.feature_browser", "pages.about.feature_apis", "pages.about.feature_cli", "pages.about.feature_tokens"} {
				output.WriteString("- " + i18n.Translate(lang, key) + "\n")
			}
			output.WriteString("\n" + i18n.Translate(lang, "pages.about.version_title") + ": " + s.version + " (" + s.buildDate + ")\n")
			output.WriteString("\n" + i18n.Translate(lang, "pages.about.links_title") + "\n- https://github.com/apimgr/citylist\n- https://apimgr-citylist.readthedocs.io\n")
			output.WriteString("\n<a href=\"https://db-ip.com/\">IP Geolocation by DB-IP</a>\n")
			if _, source := s.operatorPageSource(r, "about"); strings.TrimSpace(source) != "" {
				output.WriteString("\n" + source + "\n")
			}
		}
		if titleKey == "pages.privacy.title" {
			if s.cfg.Server.Privacy.Consent.ShowPreferences {
				output.WriteString(i18n.Translate(lang, "consent.manage_preferences") + ": /server/preferences\n")
			}
			if s.cfg.Server.Privacy.Data.Sold {
				output.WriteString(i18n.Translate(lang, "preferences.ccpa_opt_out") + ": /server/ccpa\n")
			}
			if _, source := s.operatorPageSource(r, "privacy"); strings.TrimSpace(source) != "" {
				output.WriteString("\n" + source + "\n")
			}
			output.WriteString("\n" + i18n.Translate(lang, "pages.privacy.contact_title") + ": " + privacyContact + "\n")
		}
		if titleKey == "pages.terms.title" {
			if _, source := s.operatorPageSource(r, "terms"); strings.TrimSpace(source) != "" {
				output.Reset()
				output.WriteString(title + " - " + s.brandTitle() + "\n\n" + source + "\n")
			}
		}
		s.respondText(w, output.String())
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return
	}
	baseURL := strings.TrimSuffix(s.baseURL, "/")
	reportURL := ""
	if titleKey == "pages.security.title" {
		reportURL = "https://github.com/apimgr/citylist/security/advisories/new"
	}
	languageName := lang
	for _, language := range i18n.Languages() {
		if language.Code == lang {
			languageName = language.NativeName
			break
		}
	}
	csrfToken := s.csrfToken(w, r)
	var rendered strings.Builder
	if err := publicPageTemplate.Execute(&rendered, publicPageData{
		Title: title, BrandTitle: s.brandTitle(), TitleKey: titleKey, Lead: lead, Version: s.version, BuildDate: s.buildDate, Body: body,
		SectionTitles: sectionTitles, BaseURL: baseURL, Theme: themePreference(r),
		NextTheme: nextTheme(themePreference(r)), Lang: lang, Dir: i18n.Direction(lang),
		ReturnTo: r.URL.RequestURI(), CSRFToken: csrfToken,
		ConsentBanner: s.renderCookieConsentBanner(r, csrfToken, r.URL.RequestURI()),
		SiteBanners:   s.activeAnnouncements(r, csrfToken, r.URL.RequestURI()),
		PrivacyPage:   titleKey == "pages.privacy.title", GPCNotice: titleKey == "pages.privacy.title" && privacySignalOptOut(r),
		PrivacySignal: privacySignal(r), DataSold: s.cfg.Server.Privacy.Data.Sold,
		AboutPage: titleKey == "pages.about.title", TermsPage: titleKey == "pages.terms.title", PrivacySections: privacySections,
		PrivacyContact: privacyContact, PrivacyContactURL: privacyContactURL,
		AttributionHTML: template.HTML(`<a href="https://db-ip.com/">IP Geolocation by DB-IP</a>`),
		OperatorHTML: func() template.HTML {
			if titleKey == "pages.about.title" {
				return s.operatorHTML(r, "about")
			}
			if titleKey == "pages.privacy.title" {
				return s.operatorHTML(r, "privacy")
			}
			if titleKey == "pages.terms.title" {
				return s.operatorHTML(r, "terms")
			}
			if titleKey == "pages.help.title" {
				return s.operatorHTML(r, "help")
			}
			return ""
		}(),
		TrackingScript: s.trackingScript(r), TrackingNoscript: s.trackingNoscript(r),
		OperatorHeader: s.operatorHTML(r, "header"), OperatorFooter: s.operatorHTML(r, "footer"),
		HelpPage: titleKey == "pages.help.title", HelpExamples: helpExamples, TorAddress: torAddress, I2PAddress: i2pAddress,
		AboutFeatures: func() []string {
			if titleKey != "pages.about.title" {
				return nil
			}
			return []string{i18n.Translate(lang, "pages.about.feature_search"), i18n.Translate(lang, "pages.about.feature_lookup"), i18n.Translate(lang, "pages.about.feature_browser"), i18n.Translate(lang, "pages.about.feature_apis"), i18n.Translate(lang, "pages.about.feature_cli"), i18n.Translate(lang, "pages.about.feature_tokens")}
		}(),
		AboutLinks: func() []hubPage {
			if titleKey != "pages.about.title" {
				return nil
			}
			return []hubPage{{Path: "https://github.com/apimgr/citylist", Title: i18n.Translate(lang, "pages.about.github")}, {Path: "https://apimgr-citylist.readthedocs.io", Title: i18n.Translate(lang, "pages.about.documentation")}}
		}(),
		CCPAOptOut: func() bool {
			cookie, err := r.Cookie("ccpa_opt_out")
			return err == nil && strings.EqualFold(cookie.Value, "true")
		}(),
		ShowConsentPrefs:  s.cfg.Server.Privacy.Consent.ShowPreferences,
		SecurityReportURL: reportURL, CurrentPath: r.URL.Path, LanguageName: languageName,
		SEOHead:   s.seoHead(r, r.URL.Path, title, i18n.Translate(lang, leadKey)),
		HasAccent: s.cfg.Web.UI.Accent != "",
		HubPages: func() []hubPage {
			if titleKey == "pages.help.title" {
				return helpHubPages(lang)
			}
			return nil
		}(),
	}); err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "")
		return
	}
	shared, err := s.applySharedPageShell(r, rendered.String(), map[string]interface{}{
		"CSRFToken": csrfToken, "ReturnTo": r.URL.RequestURI(), "SiteBanners": s.activeAnnouncements(r, csrfToken, r.URL.RequestURI()),
		"ConsentBanner": s.renderCookieConsentBanner(r, csrfToken, r.URL.RequestURI()), "TrackingNoscript": s.trackingNoscript(r),
	})
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "")
		return
	}
	writeHTMLResponse(w, r, http.StatusOK, []byte(shared))
}

func nextTheme(theme string) string {
	switch theme {
	case "dark":
		return "light"
	case "light":
		return "auto"
	default:
		return "dark"
	}
}

func themePreference(r *http.Request) string {
	if !canSetPreferenceCookie(r) {
		return "dark"
	}
	cookie, err := r.Cookie("theme")
	if err != nil {
		return "dark"
	}
	switch cookie.Value {
	case "dark", "light", "auto":
		return cookie.Value
	default:
		return "dark"
	}
}

func (s *Server) handlePreferencesPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", i18n.Translate(i18n.LangFromRequest(r), "errors.method_not_allowed"))
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "private, no-store")
		_ = s.csrfToken(w, r)
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	language := i18n.LangFromRequest(r)
	languageName := language
	for _, item := range i18n.Languages() {
		if item.Code == language {
			languageName = item.NativeName
			break
		}
	}
	langOverride := ""
	if cookie, err := r.Cookie("lang"); err == nil && canSetPreferenceCookie(r) && i18n.IsSupported(cookie.Value) {
		langOverride = cookie.Value
	}
	consentPreferences, consentAnalytics := consentPreferences(r)
	ccpaOptOut := false
	if cookie, err := r.Cookie("ccpa_opt_out"); err == nil {
		ccpaOptOut = strings.EqualFold(cookie.Value, "true")
	}
	s.renderPage(w, r, "preferences.tmpl", map[string]interface{}{
		"Title":              i18n.Translate(language, "preferences.title"),
		"LanguageName":       languageName,
		"HubPages":           preferenceHubPages(language),
		"LangOverride":       langOverride,
		"ConsentPreferences": consentPreferences,
		"ConsentAnalytics":   consentAnalytics,
		"CCPAOptOut":         ccpaOptOut,
		"DataSold":           s.cfg.Server.Privacy.Data.Sold,
		"GPCNotice":          privacySignalOptOut(r),
		"PrivacySignal":      privacySignal(r),
	})
}

func consentPreferences(r *http.Request) (bool, bool) {
	if privacySignalOptOut(r) {
		return false, false
	}
	consent, valid := readConsentCookie(r)
	if !valid || !consent.Essential || consent.Timestamp <= 0 {
		return false, false
	}
	return consent.Preferences, consent.Analytics
}

func (s *Server) handleDPOPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", i18n.Translate(i18n.LangFromRequest(r), "errors.method_not_allowed"))
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		privacyURL := s.BuildURL(r, "/server/privacy")
		name := strings.TrimSpace(s.cfg.Server.Contact.DPO.Name)
		email := strings.TrimSpace(s.cfg.Server.Contact.DPO.Email)
		if email == "" {
			email = strings.TrimSpace(s.cfg.Server.Contact.General.Email)
		}
		email = s.resolveContactEmail(r, email)
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			return
		}
		s.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: map[string]string{"name": name, "email": email, "privacy_url": privacyURL}})
		return
	}
	email := strings.TrimSpace(s.cfg.Server.Contact.DPO.Email)
	if email == "" {
		email = strings.TrimSpace(s.cfg.Server.Contact.General.Email)
	}
	s.renderDPOPage(w, r, dpoPageView{
		Name: s.cfg.Server.Contact.DPO.Name, Email: s.resolveContactEmail(r, email),
		PrivacyURL: s.BuildURL(r, "/server/privacy"),
	})
}

type dpoPageView struct {
	Name       string
	Email      string
	PrivacyURL string
}

// renderDPOPage renders the configured public DPO contact data. An empty
// email intentionally leaves the page with the security-policy contact path.
func (s *Server) renderDPOPage(w http.ResponseWriter, r *http.Request, view dpoPageView) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", i18n.Translate(i18n.LangFromRequest(r), "errors.method_not_allowed"))
		return
	}
	if view.PrivacyURL == "" {
		view.PrivacyURL = strings.TrimSuffix(s.baseURL, "/") + "/server/privacy"
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return
	}
	lang := i18n.LangFromRequest(r)
	languageName := lang
	for _, item := range i18n.Languages() {
		if item.Code == lang {
			languageName = item.NativeName
			break
		}
	}
	s.renderPage(w, r, "dpo.tmpl", map[string]interface{}{
		"Title": i18n.Translate(lang, "pages.dpo.title"), "DPO": view, "LanguageName": languageName,
	})
}

func (s *Server) renderContactPage(w http.ResponseWriter, r *http.Request, view contactPageView) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", i18n.Translate(i18n.LangFromRequest(r), "errors.method_not_allowed"))
		return
	}
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = s.csrfToken(w, r)
		w.WriteHeader(http.StatusOK)
		return
	}
	lang := i18n.LangFromRequest(r)
	languageName := lang
	for _, item := range i18n.Languages() {
		if item.Code == lang {
			languageName = item.NativeName
			break
		}
	}
	if view.CSRFToken == "" {
		view.CSRFToken = s.csrfToken(w, r)
	}
	if view.ReturnTo == "" {
		view.ReturnTo = r.URL.RequestURI()
	}
	if view.SecurityURL == "" {
		view.SecurityURL = strings.TrimSuffix(s.baseURL, "/") + "/server/security"
	}
	s.renderPage(w, r, "contact.tmpl", map[string]interface{}{
		"Title":        i18n.Translate(lang, "pages.contact.title"),
		"LanguageName": languageName,
		"Contact":      view,
		"NoIndex":      true,
	})
}

type preferencesSyncView struct {
	ExportURL  string
	ExportCode string
}

type linkedTokenPageItem struct {
	ID         string
	Name       string
	Masked     string
	Status     string
	CreatedAt  string
	LastUsedAt string
	ExpiresAt  string
	Revealed   string
}

type tokensPageView struct {
	HasOwner        bool
	PrimaryMasked   string
	PrimaryLast4    string
	PrimaryStatus   string
	PrimaryRevealed string
	LinkedTokens    []linkedTokenPageItem
	LinkedCount     int
	TokenCount      int
	ResourceCount   int
	CSRFToken       string
	ResourcesLabel  string
}

// renderTokensPage renders the credential-gated owner-token page. The caller
// supplies only data authorized for the current owner cookie; without an owner
// it should pass HasOwner=false and no token records.
func (s *Server) renderTokensPage(w http.ResponseWriter, r *http.Request, view tokensPageView) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", i18n.Translate(i18n.LangFromRequest(r), "errors.method_not_allowed"))
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if view.CSRFToken == "" {
		view.CSRFToken = s.csrfToken(w, r)
	}
	if view.ResourcesLabel == "" {
		view.ResourcesLabel = "items"
	}
	lang := i18n.LangFromRequest(r)
	languageName := lang
	for _, item := range i18n.Languages() {
		if item.Code == lang {
			languageName = item.NativeName
			break
		}
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		return
	}
	s.renderPage(w, r, "preferences-tokens.tmpl", map[string]interface{}{
		"Title": i18n.Translate(lang, "preferences.tokens_title"), "LanguageName": languageName,
		"TokenPage": view, "CurrentPath": r.URL.RequestURI(), "NextTheme": nextTheme(themePreference(r)),
	})
}

func (s *Server) handlePreferencesSyncPage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "application/json") {
		s.handlePreferencesSyncAPI(w, r)
		return
	}
	view := s.buildPreferencesSyncView(r)
	if r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/plain") {
		lang := i18n.LangFromRequest(r)
		s.respondText(w, i18n.Translate(lang, "api_text.export_url")+": "+view.ExportURL+"\n"+i18n.Translate(lang, "api_text.export_code")+": "+view.ExportCode+"\n")
		return
	}
	s.renderPreferencesSyncPage(w, r, view)
}

func (s *Server) renderPreferencesSyncPage(w http.ResponseWriter, r *http.Request, view preferencesSyncView) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", i18n.Translate(i18n.LangFromRequest(r), "errors.method_not_allowed"))
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = s.csrfToken(w, r)
		w.WriteHeader(http.StatusOK)
		return
	}
	language := i18n.LangFromRequest(r)
	languageName := language
	for _, item := range i18n.Languages() {
		if item.Code == language {
			languageName = item.NativeName
			break
		}
	}
	s.renderPage(w, r, "preferences-sync.tmpl", map[string]interface{}{
		"Title":        i18n.Translate(language, "preferences.sync_title"),
		"LanguageName": languageName,
		"ExportURL":    view.ExportURL,
		"ExportCode":   view.ExportCode,
	})
}

func (s *Server) buildPreferencesSyncView(r *http.Request) preferencesSyncView {
	values := s.preferenceQuery(r)
	query := values.Encode()
	return preferencesSyncView{
		ExportURL:  s.BuildURL(r, "/server/preferences/sync/import") + "?" + query,
		ExportCode: base64.RawURLEncoding.EncodeToString([]byte(query)),
	}
}

func validPreferenceKey(key string) bool {
	if len(key) < 1 || len(key) > 64 || key[0] < 'a' || key[0] > 'z' {
		return false
	}
	for _, char := range key[1:] {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' {
			continue
		}
		return false
	}
	return true
}

func validPreferenceValue(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || strings.ContainsRune("_.-", char) {
			continue
		}
		return false
	}
	return true
}

func (s *Server) handlePreferencesUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", i18n.Translate(i18n.LangFromRequest(r), "errors.method_not_allowed"))
		return
	}
	returnTo := s.preferenceRedirect(r, "/")
	if r.FormValue("action") == "reset" {
		for _, cookie := range r.Cookies() {
			if isCredentialCookie(cookie.Name) {
				continue
			}
			http.SetCookie(w, &http.Cookie{Name: cookie.Name, Value: "", Path: "/", MaxAge: 0, Secure: s.requestUsesHTTPS(r), SameSite: http.SameSiteLaxMode})
		}
		w.Header().Set("Clear-Site-Data", `"storage"`)
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, returnTo, http.StatusSeeOther)
		return
	}
	consentForm := r.FormValue("consent_form") == "1"
	preferencesAllowed := canSetPreferenceCookie(r)
	ccpaOptOut := s.cfg.Server.Privacy.Data.Sold && r.FormValue("ccpa_opt_out") == "true"
	if consentForm {
		preferencesAllowed = r.FormValue("consent_preferences") == "true"
		if ccpaOptOut {
			preferencesAllowed = false
		}
	}
	if privacySignalOptOut(r) {
		preferencesAllowed = false
		ccpaOptOut = false
	}
	if preferencesAllowed {
		if theme := r.FormValue("theme"); theme == "dark" || theme == "light" || theme == "auto" {
			http.SetCookie(w, &http.Cookie{
				Name: "theme", Value: theme, Path: "/", MaxAge: 365 * 24 * 60 * 60,
				Secure: s.requestUsesHTTPS(r), SameSite: http.SameSiteLaxMode,
			})
		}
		if selected := r.FormValue("lang"); selected == "auto" {
			http.SetCookie(w, &http.Cookie{
				Name: "lang", Value: "", Path: "/", MaxAge: -1,
				Secure: s.requestUsesHTTPS(r), SameSite: http.SameSiteLaxMode,
			})
		} else if selected != "" && containsLanguage(s.cfg.Server.I18n.Available, selected) {
			http.SetCookie(w, &http.Cookie{
				Name: "lang", Value: selected, Path: "/", MaxAge: 31536000,
				Secure: s.requestUsesHTTPS(r), SameSite: http.SameSiteLaxMode,
			})
		}
	} else {
		s.clearPreferenceCookies(w, r)
	}
	if consentForm && !privacySignalOptOut(r) {
		analyticsAllowed := r.FormValue("consent_analytics") == "true" && !ccpaOptOut && !privacySignalOptOut(r)
		consent, err := json.Marshal(cookieConsent{
			Essential:   true,
			Preferences: preferencesAllowed,
			Analytics:   analyticsAllowed,
			Timestamp:   time.Now().Unix(),
		})
		if err == nil {
			http.SetCookie(w, &http.Cookie{
				Name: "cookie_consent", Value: url.QueryEscape(string(consent)), Path: "/",
				MaxAge: 31536000, Secure: s.requestUsesHTTPS(r), SameSite: http.SameSiteLaxMode,
			})
		}
		if s.cfg.Server.Privacy.Data.Sold {
			if r.FormValue("ccpa_opt_out") == "true" {
				http.SetCookie(w, &http.Cookie{
					Name: "ccpa_opt_out", Value: "true", Path: "/", MaxAge: 31536000,
					Secure: s.requestUsesHTTPS(r), SameSite: http.SameSiteLaxMode,
				})
			} else {
				http.SetCookie(w, &http.Cookie{
					Name: "ccpa_opt_out", Value: "", Path: "/", MaxAge: 0,
					Secure: s.requestUsesHTTPS(r), SameSite: http.SameSiteLaxMode,
				})
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, returnTo, http.StatusSeeOther)
}

func isCredentialCookie(name string) bool {
	lower := strings.ToLower(name)
	return name == csrfCookieName || strings.Contains(lower, "token") || strings.Contains(lower, "session") || strings.Contains(lower, "auth")
}

func containsLanguage(available []string, selected string) bool {
	for _, language := range available {
		if strings.EqualFold(language, selected) {
			return i18n.IsSupported(selected)
		}
	}
	return false
}
