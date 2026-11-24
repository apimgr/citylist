package server

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/common/i18n"
	"github.com/apimgr/citylist/src/config"
)

type cookieConsent struct {
	Essential   bool  `json:"essential"`
	Preferences bool  `json:"preferences"`
	Analytics   bool  `json:"analytics"`
	Timestamp   int64 `json:"timestamp"`
}

const consentCookieMaxAge = 365 * 24 * 60 * 60

type cookieConsentBannerData struct {
	BaseURL          string
	CSRFToken        string
	ReturnTo         string
	Message          string
	PolicyURL        string
	PolicyText       string
	DeclineText      string
	AcceptText       string
	PreferencesText  string
	AriaLabel        string
	ShowPreferences  bool
	DataSold         bool
	Position         string
	HasConsentCookie bool
	Preferences      bool
	Analytics        bool
	EssentialLabel   string
	EssentialHelp    string
	PreferencesLabel string
	PreferencesHelp  string
	AnalyticsLabel   string
	AnalyticsHelp    string
	SaveText         string
	CancelText       string
}

var cookieConsentBannerTemplate = template.Must(template.New("cookie-consent").Parse(`{{if not .HasConsentCookie}}<aside id="cookie-consent" class="cookie-banner cookie-banner-{{.Position}}" data-sold="{{.DataSold}}" aria-label="{{.AriaLabel}}">
  <div class="cookie-banner-content">
    <p class="cookie-message">{{.Message}} - <a class="policy-link" href="{{.PolicyURL}}">{{.PolicyText}}</a>{{if .ShowPreferences}} <span aria-hidden="true">·</span> <a class="policy-link" href="{{.BaseURL}}/server/preferences" data-action="cookie-preferences">{{.PreferencesText}}</a>{{end}}</p>
    <div class="cookie-buttons">
      <form method="post" action="{{.BaseURL}}/server/consent" data-consent-choice><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><input type="hidden" name="redirect" value="{{.ReturnTo}}"><input type="hidden" name="choice" value="decline"><button type="submit" class="btn-decline">{{.DeclineText}}</button></form>
      <form method="post" action="{{.BaseURL}}/server/consent" data-consent-choice><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><input type="hidden" name="redirect" value="{{.ReturnTo}}"><input type="hidden" name="choice" value="accept"><button type="submit" class="btn-accept">{{.AcceptText}}</button></form>
    </div>
  </div>
</aside>{{end}}{{if .ShowPreferences}}<dialog class="cookie-preferences-modal" id="cookie-preferences-modal" aria-labelledby="cookie-preferences-title">
  <form method="post" action="{{.BaseURL}}/server/consent" data-consent-save>
    <input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><input type="hidden" name="redirect" value="{{.ReturnTo}}"><input type="hidden" name="choice" value="save">
    <h2 id="cookie-preferences-title">{{.PreferencesText}}</h2>
    <div class="cookie-preference-option"><label for="pref-essential">{{.EssentialLabel}}</label><input id="pref-essential" type="checkbox" checked disabled><p>{{.EssentialHelp}}</p></div>
    <div class="cookie-preference-option"><label for="pref-preferences">{{.PreferencesLabel}}</label><input id="pref-preferences" type="checkbox" name="preferences" value="true"{{if .Preferences}} checked{{end}}><p>{{.PreferencesHelp}}</p></div>
    <div class="cookie-preference-option"><label for="pref-analytics">{{.AnalyticsLabel}}</label><input id="pref-analytics" type="checkbox" name="analytics" value="true"{{if .Analytics}} checked{{end}}><p>{{.AnalyticsHelp}}</p></div>
    <div class="cookie-preferences-actions"><button type="button" class="btn btn-secondary" data-action="close-cookie-preferences">{{.CancelText}}</button><button type="submit" class="btn btn-primary">{{.SaveText}}</button></div>
  </form>
</dialog>{{end}}`))

func (s *Server) renderCookieConsentBanner(r *http.Request, csrfToken, returnTo string) template.HTML {
	if privacySignalOptOut(r) {
		return ""
	}
	lang := i18n.LangFromRequest(r)
	defaults := config.DefaultConfig().Server.Privacy.Consent
	consent := s.cfg.Server.Privacy.Consent
	message := s.cfg.Server.Privacy.GetConsentMessage()
	if message == defaults.Message || message == defaults.MessageIfSold {
		key := "consent.message"
		if s.cfg.Server.Privacy.Data.Sold {
			key = "consent.message_sold"
		}
		message = i18n.Translate(lang, key)
	}
	policyText := consent.Policy.Text
	if policyText == defaults.Policy.Text {
		policyText = i18n.Translate(lang, "consent.policy")
	}
	declineText := consent.Buttons.Decline
	if declineText == defaults.Buttons.Decline {
		declineText = i18n.Translate(lang, "consent.decline")
	}
	acceptText := consent.Buttons.Accept
	if acceptText == defaults.Buttons.Accept {
		acceptText = i18n.Translate(lang, "consent.accept")
	}
	preferencesText := consent.PreferencesText
	if preferencesText == defaults.PreferencesText {
		preferencesText = i18n.Translate(lang, "consent.manage_preferences")
	}
	ariaLabel := i18n.Translate(lang, "consent.aria_label")
	position := consent.Position
	if position != "top" {
		position = "bottom"
	}
	policyURL := consent.Policy.URL
	if strings.HasPrefix(policyURL, "/") && !strings.HasPrefix(policyURL, "//") {
		policyURL = s.BuildURL(r, policyURL)
	}
	current, _ := readConsentCookie(r)
	validConsent := hasValidConsentCookie(r)
	data := cookieConsentBannerData{
		BaseURL: strings.TrimSuffix(s.baseURL, "/"), CSRFToken: csrfToken, ReturnTo: returnTo,
		Message: message, PolicyURL: policyURL, PolicyText: policyText,
		DeclineText: declineText, AcceptText: acceptText, PreferencesText: preferencesText, AriaLabel: ariaLabel,
		ShowPreferences: consent.ShowPreferences, DataSold: s.cfg.Server.Privacy.Data.Sold, Position: position,
		HasConsentCookie: validConsent,
		Preferences:      validConsent && current.Preferences, Analytics: validConsent && current.Analytics,
		EssentialLabel:   i18n.Translate(lang, "preferences.essential"),
		EssentialHelp:    i18n.Translate(lang, "preferences.essential_description"),
		PreferencesLabel: i18n.Translate(lang, "preferences.preference_cookies"),
		PreferencesHelp:  i18n.Translate(lang, "preferences.preference_description"),
		AnalyticsLabel:   i18n.Translate(lang, "preferences.analytics_cookies"),
		AnalyticsHelp:    i18n.Translate(lang, "preferences.analytics_description"),
		SaveText:         i18n.Translate(lang, "common.save"), CancelText: i18n.Translate(lang, "common.cancel"),
	}
	var output bytes.Buffer
	if err := cookieConsentBannerTemplate.Execute(&output, data); err != nil {
		return ""
	}
	return template.HTML(output.String())
}

func hasValidConsentCookie(r *http.Request) bool {
	consent, valid := readConsentCookie(r)
	return valid && consent.Essential && consent.Timestamp > 0
}

func canSetPreferenceCookie(r *http.Request) bool {
	if privacySignalOptOut(r) {
		return false
	}
	consent, valid := readConsentCookie(r)
	return !valid || consent.Essential && consent.Preferences && consent.Timestamp > 0
}

func withoutPreferenceCookies(r *http.Request) *http.Request {
	var values []string
	for _, cookie := range r.Cookies() {
		if cookie.Name == "theme" || cookie.Name == "lang" || strings.HasPrefix(cookie.Name, preferenceCookiePrefix) {
			continue
		}
		values = append(values, cookie.Name+"="+cookie.Value)
	}
	request := r.Clone(r.Context())
	if len(values) == 0 {
		request.Header.Del("Cookie")
	} else {
		request.Header.Set("Cookie", strings.Join(values, "; "))
	}
	return request
}

func readConsentCookie(r *http.Request) (cookieConsent, bool) {
	cookie, err := r.Cookie("cookie_consent")
	if err != nil {
		return cookieConsent{}, false
	}
	value, err := url.QueryUnescape(cookie.Value)
	if err != nil {
		return cookieConsent{}, false
	}
	var consent cookieConsent
	if err := json.Unmarshal([]byte(value), &consent); err != nil {
		return cookieConsent{}, false
	}
	now := time.Now().Unix()
	if !consent.Essential || consent.Timestamp <= 0 || consent.Timestamp > now || now-consent.Timestamp > int64(consentCookieMaxAge) {
		return cookieConsent{}, false
	}
	return consent, true
}

func (s *Server) handleConsentPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", i18n.Translate(i18n.LangFromRequest(r), "errors.method_not_allowed"))
		return
	}
	choice := r.FormValue("choice")
	if choice != "accept" && choice != "decline" && choice != "save" {
		s.respondError(w, http.StatusBadRequest, "INVALID_CONSENT_CHOICE", i18n.Translate(i18n.LangFromRequest(r), "errors.bad_request"))
		return
	}
	preferences := choice == "accept"
	analytics := choice == "accept"
	if choice == "save" {
		preferences = r.FormValue("preferences") == "true"
		analytics = r.FormValue("analytics") == "true"
	}
	if privacySignalOptOut(r) {
		preferences = false
		analytics = false
	}
	value, err := json.Marshal(cookieConsent{
		Essential: true, Preferences: preferences, Analytics: analytics, Timestamp: time.Now().Unix(),
	})
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "CONSENT_SAVE_FAILED", i18n.Translate(i18n.LangFromRequest(r), "errors.server_error"))
		return
	}
	if !privacySignalOptOut(r) {
		http.SetCookie(w, &http.Cookie{
			Name: "cookie_consent", Value: url.QueryEscape(string(value)), Path: "/", MaxAge: consentCookieMaxAge,
			Secure: s.requestUsesHTTPS(r), SameSite: http.SameSiteLaxMode,
		})
	}
	if !preferences {
		s.clearPreferenceCookies(w, r)
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, s.preferenceRedirect(r, "/"), http.StatusSeeOther)
}

func (s *Server) handleCCPAPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", i18n.Translate(i18n.LangFromRequest(r), "errors.method_not_allowed"))
		return
	}
	if !s.cfg.Server.Privacy.Data.Sold {
		http.NotFound(w, r)
		return
	}
	choice := r.FormValue("choice")
	switch choice {
	case "opt-out":
		if !gpcOptOut(r) {
			http.SetCookie(w, &http.Cookie{Name: "ccpa_opt_out", Value: "true", Path: "/", MaxAge: 31536000, Secure: s.requestUsesHTTPS(r), SameSite: http.SameSiteLaxMode})
			value, err := json.Marshal(cookieConsent{Essential: true, Timestamp: time.Now().Unix()})
			if err == nil {
				http.SetCookie(w, &http.Cookie{Name: "cookie_consent", Value: url.QueryEscape(string(value)), Path: "/", MaxAge: 31536000, Secure: s.requestUsesHTTPS(r), SameSite: http.SameSiteLaxMode})
			}
			s.clearPreferenceCookies(w, r)
		}
	case "", "opt-in":
		if !gpcOptOut(r) {
			http.SetCookie(w, &http.Cookie{Name: "ccpa_opt_out", Value: "", Path: "/", MaxAge: -1, Secure: s.requestUsesHTTPS(r), SameSite: http.SameSiteLaxMode})
		}
	default:
		s.respondError(w, http.StatusBadRequest, "INVALID_CCPA_CHOICE", i18n.Translate(i18n.LangFromRequest(r), "errors.bad_request"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, s.preferenceRedirect(r, "/server/privacy"), http.StatusSeeOther)
}

func (s *Server) clearPreferenceCookies(w http.ResponseWriter, r *http.Request) {
	seen := make(map[string]bool)
	for _, cookie := range r.Cookies() {
		if cookie.Name != "theme" && cookie.Name != "lang" && !strings.HasPrefix(cookie.Name, preferenceCookiePrefix) {
			continue
		}
		if seen[cookie.Name] {
			continue
		}
		seen[cookie.Name] = true
		http.SetCookie(w, &http.Cookie{Name: cookie.Name, Value: "", Path: "/", MaxAge: -1, Secure: s.requestUsesHTTPS(r), SameSite: http.SameSiteLaxMode})
	}
}
