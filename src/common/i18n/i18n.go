package i18n

import (
	"context"
	"embed"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
)

//go:embed locales/*.json
var localeFS embed.FS

type Language struct {
	Code             string
	Name             string
	NativeName       string
	Direction        string
	PluralCategories []string
}

type locale struct {
	Meta struct {
		Language   string `json:"language"`
		Name       string `json:"name"`
		NativeName string `json:"native_name"`
		Direction  string `json:"direction"`
		Version    string `json:"version"`
	} `json:"meta"`
	Messages map[string]any `json:"messages"`
}

type contextKey struct{}

var requestLanguageKey contextKey

var registry = []Language{
	{Code: "en", Name: "English", NativeName: "English", Direction: "ltr", PluralCategories: []string{"one", "other"}},
	{Code: "es", Name: "Spanish", NativeName: "Español", Direction: "ltr", PluralCategories: []string{"one", "other"}},
	{Code: "fr", Name: "French", NativeName: "Français", Direction: "ltr", PluralCategories: []string{"one", "other"}},
	{Code: "ar", Name: "Arabic", NativeName: "العربية", Direction: "rtl", PluralCategories: []string{"zero", "one", "two", "few", "many", "other"}},
	{Code: "de", Name: "German", NativeName: "Deutsch", Direction: "ltr", PluralCategories: []string{"one", "other"}},
	{Code: "it", Name: "Italian", NativeName: "Italiano", Direction: "ltr", PluralCategories: []string{"one", "many", "other"}},
}

var locales = loadLocales()

func loadLocales() map[string]locale {
	loaded := make(map[string]locale, len(registry))
	for _, info := range registry {
		data, err := localeFS.ReadFile("locales/" + info.Code + ".json")
		if err != nil {
			panic("missing embedded locale " + info.Code + ": " + err.Error())
		}
		var item locale
		if err := json.Unmarshal(data, &item); err != nil {
			panic("invalid embedded locale " + info.Code + ": " + err.Error())
		}
		if item.Meta.Language != info.Code || item.Meta.Name != info.Name || item.Meta.NativeName != info.NativeName || item.Meta.Direction != info.Direction {
			panic("locale metadata does not match language registry: " + info.Code)
		}
		loaded[info.Code] = item
	}
	return loaded
}

func Languages() []Language {
	result := make([]Language, len(registry))
	copy(result, registry)
	for i := range result {
		result[i].PluralCategories = append([]string(nil), registry[i].PluralCategories...)
	}
	return result
}

func LanguageCodes() []string {
	codes := make([]string, 0, len(registry))
	for _, item := range registry {
		codes = append(codes, item.Code)
	}
	return codes
}

func LocaleFile(code string) ([]byte, bool) {
	if !IsSupported(code) {
		return nil, false
	}
	data, err := localeFS.ReadFile("locales/" + strings.ToLower(code) + ".json")
	return data, err == nil
}

func IsSupported(code string) bool {
	_, ok := locales[strings.ToLower(strings.TrimSpace(code))]
	return ok
}

func Direction(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	for _, item := range registry {
		if item.Code == code {
			return item.Direction
		}
	}
	return "ltr"
}

func Translate(code, key string) string {
	if !IsSupported(code) {
		code = "en"
	}
	if value, ok := lookup(locales[code].Messages, key); ok {
		if message, ok := value.(string); ok {
			return message
		}
	}
	if value, ok := lookup(locales["en"].Messages, key); ok {
		if message, ok := value.(string); ok {
			return message
		}
	}
	return key
}

func TranslateFormat(code, key string, values map[string]string) string {
	message := Translate(code, key)
	for name, value := range values {
		message = strings.ReplaceAll(message, "{"+name+"}", value)
	}
	return message
}

func TranslatePlural(code, key string, count int) string {
	if !IsSupported(code) {
		code = "en"
	}
	value, found := lookup(locales[code].Messages, key)
	if !found {
		value, _ = lookup(locales["en"].Messages, key)
	}
	forms, ok := value.(map[string]any)
	if !ok {
		return key
	}
	tag := language.MustParse(code)
	form := pluralFormName(plural.Cardinal.MatchPlural(tag, count, 0, 0, 0, 0))
	if message, ok := forms[form].(string); ok {
		return strings.ReplaceAll(message, "{count}", strconv.Itoa(count))
	}
	if message, ok := forms["other"].(string); ok {
		return strings.ReplaceAll(message, "{count}", strconv.Itoa(count))
	}
	return key
}

func pluralFormName(form plural.Form) string {
	switch form {
	case plural.Zero:
		return "zero"
	case plural.One:
		return "one"
	case plural.Two:
		return "two"
	case plural.Few:
		return "few"
	case plural.Many:
		return "many"
	default:
		return "other"
	}
}

func Middleware(next http.Handler) http.Handler {
	return MiddlewareWithSettings(Settings{
		Enabled:          true,
		DefaultLanguage:  "en",
		FallbackLanguage: "en",
		CookieName:       "lang",
		CookieMaxAge:     365 * 24 * 60 * 60,
		Available:        LanguageCodes(),
	})(next)
}

type Settings struct {
	Enabled          bool
	DefaultLanguage  string
	FallbackLanguage string
	CookieName       string
	CookieMaxAge     int
	Available        []string
}

func MiddlewareWithSettings(settings Settings) func(http.Handler) http.Handler {
	settings = normalizeSettings(settings)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			code := settings.DefaultLanguage
			if settings.Enabled {
				code = requestLanguage(r, settings)
				if requested := r.URL.Query().Get("lang"); requested != "" {
					if normalized, ok := normalizeAvailable(requested, settings.Available); ok {
						http.SetCookie(w, &http.Cookie{
							Name: settings.CookieName, Value: normalized, Path: "/",
							MaxAge: settings.CookieMaxAge, SameSite: http.SameSiteLaxMode,
							Secure: r.TLS != nil, HttpOnly: true,
						})
					}
				}
			}
			w.Header().Set("Content-Language", code)
			w.Header().Add("Vary", "Accept-Language")
			w.Header().Add("Vary", "Cookie")
			r = r.WithContext(context.WithValue(r.Context(), requestLanguageKey, code))
			next.ServeHTTP(w, r)
		})
	}
}

func normalizeSettings(settings Settings) Settings {
	if !IsSupported(settings.DefaultLanguage) {
		settings.DefaultLanguage = "en"
	}
	if !IsSupported(settings.FallbackLanguage) {
		settings.FallbackLanguage = settings.DefaultLanguage
	}
	if settings.CookieName == "" {
		settings.CookieName = "lang"
	}
	if settings.CookieMaxAge <= 0 {
		settings.CookieMaxAge = 365 * 24 * 60 * 60
	}
	if len(settings.Available) == 0 {
		settings.Available = LanguageCodes()
	}
	return settings
}

func LangFromRequest(r *http.Request) string {
	if code, ok := r.Context().Value(requestLanguageKey).(string); ok && IsSupported(code) {
		return code
	}
	return requestLanguage(r, normalizeSettings(Settings{Enabled: true, DefaultLanguage: "en", FallbackLanguage: "en", CookieName: "lang", Available: LanguageCodes()}))
}

func requestLanguage(r *http.Request, settings Settings) string {
	if requested := r.URL.Query().Get("lang"); requested != "" {
		if normalized, ok := normalizeAvailable(requested, settings.Available); ok {
			return normalized
		}
		return "en"
	}
	if cookie, err := r.Cookie(settings.CookieName); err == nil {
		if normalized, ok := normalizeAvailable(cookie.Value, settings.Available); ok {
			return normalized
		}
		return "en"
	}
	if accepted := r.Header.Get("Accept-Language"); accepted != "" {
		tags := languageTagsFor(settings.Available)
		matcher := language.NewMatcher(tags)
		if tags, _, err := language.ParseAcceptLanguage(accepted); err == nil {
			if tag, _, _ := matcher.Match(tags...); tag != language.Und {
				base, _ := tag.Base()
				if isAvailable(base.String(), settings.Available) {
					return base.String()
				}
			}
		}
	}
	return settings.DefaultLanguage
}

func normalizeAvailable(code string, available []string) (string, bool) {
	code = strings.TrimSpace(code)
	if isAvailable(code, available) {
		return strings.ToLower(code), true
	}
	if index := strings.IndexAny(code, "-_"); index > 0 {
		base := code[:index]
		if isAvailable(base, available) {
			return strings.ToLower(base), true
		}
	}
	return "", false
}

func languageTagsFor(available []string) []language.Tag {
	tags := make([]language.Tag, 0, len(available))
	for _, code := range available {
		if IsSupported(code) {
			tags = append(tags, language.MustParse(code))
		}
	}
	return tags
}

func isAvailable(code string, available []string) bool {
	if !IsSupported(code) {
		return false
	}
	for _, candidate := range available {
		if strings.EqualFold(code, candidate) {
			return true
		}
	}
	return false
}

func lookup(messages map[string]any, key string) (any, bool) {
	var current any = messages
	for _, part := range strings.Split(key, ".") {
		group, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = group[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}
