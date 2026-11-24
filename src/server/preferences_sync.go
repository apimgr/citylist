package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/apimgr/citylist/src/common/i18n"
)

const preferenceCookiePrefix = "citylist_pref_"

type preferenceState struct {
	Theme   string            `json:"theme"`
	Lang    string            `json:"lang"`
	Prefs   map[string]string `json:"prefs"`
	Applied []string          `json:"applied,omitempty"`
	Ignored []string          `json:"ignored,omitempty"`
	URL     string            `json:"url,omitempty"`
	Code    string            `json:"code,omitempty"`
}

func (s *Server) handlePreferencesSyncAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "This route accepts GET requests")
		return
	}
	state := s.preferenceState(r)
	query := s.preferenceQuery(r)
	state.URL = s.BuildURL(r, "/server/preferences/sync/import") + "?" + query.Encode()
	state.Code = base64.RawURLEncoding.EncodeToString([]byte(query.Encode()))
	s.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: state})
}

func (s *Server) handlePreferencesSyncImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "This route accepts GET requests")
		return
	}
	values, _ := url.ParseQuery(r.URL.RawQuery)
	result := s.applyPreferenceImport(w, r, values)
	w.Header().Set("Cache-Control", "no-store")
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: result})
		return
	}
	http.Redirect(w, r, s.preferenceRedirect(r, "/"), http.StatusSeeOther)
}

func (s *Server) handlePreferencesSyncPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "This route accepts POST requests")
		return
	}
	_ = r.ParseForm()
	values := url.Values{}
	for key, items := range r.Form {
		if key == "code" || key == "redirect" || key == "csrf_token" {
			values[key] = append([]string(nil), items...)
			continue
		}
		values[key] = append([]string(nil), items...)
	}
	result := s.applyPreferenceImport(w, r, values)
	_ = result
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, s.preferenceRedirect(r, "/server/preferences/sync"), http.StatusSeeOther)
}

func (s *Server) handlePreferencesAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet:
		s.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: s.preferenceState(r)})
	case http.MethodPost:
		values, err := s.preferenceInput(w, r)
		if err != nil {
			s.respondError(w, http.StatusBadRequest, "VALIDATION_FAILED", "No valid preference values were supplied")
			return
		}
		result := s.applyPreferenceImport(w, r, values)
		if len(result.Applied) == 0 {
			s.respondError(w, http.StatusBadRequest, "VALIDATION_FAILED", "No valid preference values were supplied")
			return
		}
		s.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: result})
	default:
		w.Header().Set("Allow", "GET, POST")
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "This route accepts GET and POST requests")
	}
}

func (s *Server) handlePreferencesImportAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "This route accepts GET requests")
		return
	}
	values, _ := url.ParseQuery(r.URL.RawQuery)
	result := s.applyPreferenceImport(w, r, values)
	s.respondJSON(w, http.StatusOK, APIResponse{Success: true, Data: result})
}

func (s *Server) preferenceInput(w http.ResponseWriter, r *http.Request) (url.Values, error) {
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	}
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType == "application/json" {
		var input map[string]json.RawMessage
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&input); err != nil {
			return nil, err
		}
		values := make(url.Values, len(input))
		for key, raw := range input {
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				continue
			}
			values.Set(key, value)
		}
		return values, nil
	}
	if mediaType != "application/x-www-form-urlencoded" && mediaType != "" {
		return nil, errors.New("unsupported preference content type")
	}
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	return r.Form, nil
}

func (s *Server) preferenceState(r *http.Request) preferenceState {
	theme := "dark"
	if cookie, err := r.Cookie("theme"); err == nil && canSetPreferenceCookie(r) && validTheme(cookie.Value) {
		theme = cookie.Value
	}
	lang := s.cfg.Server.I18n.DefaultLanguage
	if cookie, err := r.Cookie("lang"); err == nil && canSetPreferenceCookie(r) {
		if value, ok := normalizePreferenceLanguage(cookie.Value, s.cfg.Server.I18n.Available); ok {
			lang = value
		}
	}
	prefs := make(map[string]string)
	if canSetPreferenceCookie(r) {
		for _, cookie := range r.Cookies() {
			if strings.HasPrefix(cookie.Name, preferenceCookiePrefix) {
				key := strings.TrimPrefix(cookie.Name, preferenceCookiePrefix)
				if validPreferenceKey(key) && validPreferenceValue(cookie.Value) {
					prefs[key] = cookie.Value
				}
			}
		}
	}
	return preferenceState{Theme: theme, Lang: lang, Prefs: prefs}
}

func (s *Server) preferenceQuery(r *http.Request) url.Values {
	state := s.preferenceState(r)
	values := url.Values{"theme": {state.Theme}, "lang": {state.Lang}}
	for key, value := range state.Prefs {
		values.Set(preferenceCookiePrefix+key, value)
	}
	return values
}

func (s *Server) applyPreferenceImport(w http.ResponseWriter, r *http.Request, direct url.Values) preferenceState {
	values := make(url.Values)
	code := strings.TrimSpace(direct.Get("code"))
	delete(direct, "code")
	delete(direct, "redirect")
	delete(direct, "csrf_token")
	if len(code) <= 2048 && code != "" {
		if queryIndex := strings.IndexByte(code, '?'); queryIndex >= 0 {
			code = code[queryIndex+1:]
		}
		candidate := strings.TrimRight(code, "=")
		if strings.Contains(candidate, "=") {
			code = candidate
		} else {
			decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(code, "="))
			if err != nil {
				decoded, err = base64.URLEncoding.DecodeString(code)
			}
			if err == nil {
				code = string(decoded)
			} else {
				code = ""
			}
		}
		if parsed, err := url.ParseQuery(code); err == nil {
			values = parsed
		}
	}
	for key, items := range direct {
		if len(items) > 0 {
			values[key] = append([]string(nil), items...)
		}
	}
	result := s.preferenceState(r)
	result.Applied = []string{}
	result.Ignored = []string{}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	seen := make(map[string]bool)
	for index, key := range keys {
		if index >= 32 {
			break
		}
		if isPreferenceControl(key) {
			continue
		}
		items := values[key]
		if len(items) == 0 {
			continue
		}
		value := items[0]
		if len(value) > 64 {
			if !seen[key] {
				result.Ignored = append(result.Ignored, key)
				seen[key] = true
			}
			continue
		}
		cookieName, normalized, valid := s.validatePreference(key, value)
		if !valid {
			if !seen[key] {
				result.Ignored = append(result.Ignored, key)
				seen[key] = true
			}
			continue
		}
		if !canSetPreferenceCookie(r) {
			if !seen[key] {
				result.Ignored = append(result.Ignored, key)
				seen[key] = true
			}
			continue
		}
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: normalized, Path: "/", MaxAge: 31536000, Secure: s.requestUsesHTTPS(r), SameSite: http.SameSiteLaxMode})
		switch cookieName {
		case "theme":
			result.Theme = normalized
		case "lang":
			result.Lang = normalized
		default:
			result.Prefs[strings.TrimPrefix(cookieName, preferenceCookiePrefix)] = normalized
		}
		result.Applied = append(result.Applied, key)
	}
	return result
}

func (s *Server) validatePreference(key, value string) (string, string, bool) {
	if key == "theme" {
		return "theme", value, validTheme(value)
	}
	if key == "lang" {
		lang, ok := normalizePreferenceLanguage(value, s.cfg.Server.I18n.Available)
		return "lang", lang, ok
	}
	if strings.HasPrefix(key, preferenceCookiePrefix) {
		prefKey := strings.TrimPrefix(key, preferenceCookiePrefix)
		if validPreferenceKey(prefKey) && validPreferenceValue(value) {
			return key, value, true
		}
	}
	return "", "", false
}

func normalizePreferenceLanguage(value string, available []string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, lang := range available {
		if strings.EqualFold(value, lang) && i18nSupported(lang) {
			return lang, true
		}
	}
	base, _, _ := strings.Cut(value, "-")
	for _, lang := range available {
		if strings.EqualFold(base, lang) && i18nSupported(lang) {
			return lang, true
		}
	}
	return "", false
}

func i18nSupported(lang string) bool {
	return i18n.IsSupported(lang)
}

func validTheme(value string) bool {
	return value == "dark" || value == "light" || value == "auto"
}

func isPreferenceControl(key string) bool {
	switch key {
	case "csrf_token", "code", "redirect", "action", "consent_form", "consent_preferences", "consent_analytics", "ccpa_opt_out":
		return true
	default:
		return false
	}
}

func (s *Server) preferenceRedirect(r *http.Request, fallback string) string {
	redirect := r.URL.Query().Get("redirect")
	if r.Method == http.MethodPost {
		redirect = r.FormValue("redirect")
	}
	if target := safePreferenceTarget(redirect); target != "" && !isPreferenceLoopTarget(target) {
		return target
	}
	if target := s.sameOriginPreferenceReferer(r); target != "" && !isPreferenceLoopTarget(target) {
		return target
	}
	return fallback
}

func (s *Server) sameOriginPreferenceReferer(r *http.Request) string {
	referer, err := url.Parse(r.Referer())
	if err != nil || referer.Scheme == "" || referer.Host == "" {
		return ""
	}
	origin, err := url.Parse(s.BuildURL(r, "/"))
	if err != nil || !strings.EqualFold(referer.Scheme, origin.Scheme) || !strings.EqualFold(referer.Host, origin.Host) {
		return ""
	}
	path := referer.EscapedPath()
	if path == "" {
		path = "/"
	}
	if referer.RawQuery != "" {
		path += "?" + referer.RawQuery
	}
	return safePreferenceTarget(path)
}

func safePreferenceTarget(target string) string {
	parsed, err := url.ParseRequestURI(target)
	if err != nil || !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") || strings.HasPrefix(target, "/\\") || strings.ContainsAny(target, "\r\n") || parsed.IsAbs() || parsed.Host != "" {
		return ""
	}
	return target
}

func isPreferenceLoopTarget(target string) bool {
	path := strings.SplitN(target, "?", 2)[0]
	return path == "/server/preferences/sync/import" || path == "/server/preferences" || path == "/server/preferences/sync"
}
