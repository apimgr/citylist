package i18n

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var placeholderPattern = regexp.MustCompile(`\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)

func TestKeyConsistency(t *testing.T) {
	english := locales["en"].Messages
	englishMessages := flattenMessages(english)
	wantKeys, wantPlurals := messageKeys(english)
	for _, language := range registry {
		gotKeys, gotPlurals := messageKeys(locales[language.Code].Messages)
		if !equalStrings(wantKeys, gotKeys) {
			t.Errorf("%s message keys differ from English: got %v, want %v", language.Code, gotKeys, wantKeys)
		}
		for key, englishForms := range wantPlurals {
			forms := gotPlurals[key]
			allowed := append([]string(nil), language.PluralCategories...)
			if contains(allowed, "zero") {
				allowed = append(allowed, "zero")
			}
			for form := range forms {
				if !contains(allowed, form) {
					t.Errorf("%s %s has unsupported plural category %q", language.Code, key, form)
				}
			}
			for _, form := range language.PluralCategories {
				message, ok := forms[form]
				if !ok {
					t.Errorf("%s %s is missing plural category %q", language.Code, key, form)
					continue
				}
				sourceMessage, ok := englishForms[form]
				if !ok {
					sourceMessage = englishForms["other"]
				}
				if !equalStrings(withoutCount(placeholderNames(sourceMessage)), withoutCount(placeholderNames(message))) {
					t.Errorf("%s %s.%s placeholder mismatch", language.Code, key, form)
				}
			}
		}
		for key, message := range flattenMessages(locales[language.Code].Messages) {
			if strings.TrimSpace(message) == "" {
				t.Errorf("%s %s has an empty translation", language.Code, key)
			}
			if source, ok := englishMessages[key]; ok && !isPluralCategory(key, gotPlurals) {
				wantPlaceholders := placeholderNames(source)
				gotPlaceholders := placeholderNames(message)
				if !equalStrings(wantPlaceholders, gotPlaceholders) {
					t.Errorf("%s %s placeholder mismatch", language.Code, key)
				}
			}
			if strings.Contains(message, "%s") || strings.Contains(message, "%d") || regexp.MustCompile(`\{[0-9]+\}`).MatchString(message) {
				t.Errorf("%s %s uses a positional format placeholder", language.Code, key)
			}
		}
	}
}

func TestCLIMessageCatalogCoverage(t *testing.T) {
	want := []string{
		"error_color", "error_flags", "error_config_path", "error_config_load", "error_runtime_dirs",
		"error_config_save", "error_invalid_token", "error_read_token_file", "error_invalid_token_file",
		"error_format", "error_command_required", "error_command", "error_server_required", "error_server_url",
		"error_request", "error_output", "error_interactive", "error_menu_choice", "error_invalid_server_url",
		"error_create_request", "error_request_detail", "error_read_response", "error_response_size", "error_status",
		"error_path_absolute", "error_path_invalid",
		"error_table_json", "menu_title", "menu_options",
		"menu_prompt", "prompt_server", "prompt_search_query", "prompt_country_optional", "prompt_zip_search",
		"prompt_state_optional", "prompt_city_id", "prompt_zipcode", "prompt_country", "prompt_page_optional",
		"prompt_limit_optional", "prompt_latitude", "prompt_longitude", "prompt_radius", "prompt_state",
		"endpoint_arguments", "endpoint_nearest", "endpoint_nearby", "endpoint_zip_nearby", "endpoint_unknown",
		"help", "version", "shell_help", "shell_arguments", "shell_unsupported", "shell_action",
		"completion_server", "completion_token", "completion_output", "completion_language",
	}
	for _, language := range registry {
		messages, ok := lookup(locales[language.Code].Messages, "cli")
		if !ok {
			t.Errorf("%s has no cli catalog", language.Code)
			continue
		}
		catalog, ok := messages.(map[string]any)
		if !ok {
			t.Errorf("%s cli catalog is not an object", language.Code)
			continue
		}
		for _, key := range want {
			if value, ok := catalog[key].(string); !ok || strings.TrimSpace(value) == "" {
				t.Errorf("%s cli.%s is missing or empty", language.Code, key)
			}
		}
	}
}

func TestLocalesFS(t *testing.T) {
	entries, err := localeFS.ReadDir("locales")
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		got = append(got, strings.TrimSuffix(entry.Name(), ".json"))
		data, err := localeFS.ReadFile("locales/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := json.Unmarshal(data, &document); err != nil {
			t.Errorf("%s: invalid JSON: %v", entry.Name(), err)
		}
	}
	want := make([]string, 0, len(registry))
	for _, item := range registry {
		want = append(want, item.Code)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !equalStrings(got, want) {
		t.Fatalf("locale files %v do not match registry %v", got, want)
	}
}

func TestTranslateAndFormat(t *testing.T) {
	tests := []struct {
		name string
		lang string
		key  string
		args map[string]string
		want string
	}{
		{name: "Spanish", lang: "es", key: "home.description", args: map[string]string{"cities": "1", "countries": "2"}, want: "Accede a 1 ciudades de 2 países con búsqueda geográfica."},
		{name: "translated format", lang: "de", key: "home.description", args: map[string]string{"cities": "3", "countries": "4"}, want: "Durchsuchen Sie 3 Städte aus 4 Ländern mit leistungsstarker geografischer Suche."},
		{name: "unknown key remains visible", lang: "en", key: "missing.key", want: "missing.key"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := TranslateFormat(test.lang, test.key, test.args); got != test.want {
				t.Errorf("TranslateFormat() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestTranslatePlural(t *testing.T) {
	tests := []struct {
		lang  string
		count int
		want  string
	}{
		{lang: "en", count: 1, want: "1 city found"},
		{lang: "en", count: 4, want: "4 cities found"},
		{lang: "ar", count: 2, want: "تم العثور على مدينتين"},
	}
	for _, test := range tests {
		if got := TranslatePlural(test.lang, "plurals.cities_found", test.count); got != test.want {
			t.Errorf("TranslatePlural(%q, %d) = %q, want %q", test.lang, test.count, got, test.want)
		}
	}
}

func TestLanguageSelectionAndCookie(t *testing.T) {
	tests := []struct {
		name          string
		query         string
		cookie        string
		accept        string
		want          string
		wantCookie    bool
		secureRequest bool
	}{
		{name: "query has priority and persists", query: "fr", cookie: "de", accept: "it", want: "fr", wantCookie: true},
		{name: "query cookie is secure on TLS", query: "ar", accept: "en", want: "ar", wantCookie: true, secureRequest: true},
		{name: "cookie has priority", cookie: "de", accept: "it", want: "de"},
		{name: "accept language matches region", accept: "fr-CA, en;q=0.8", want: "fr"},
		{name: "unsupported query falls back to English", query: "zh", cookie: "it", want: "en"},
		{name: "unsupported accept language falls back to English", accept: "zh-CN", want: "en"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := "/?lang=" + test.query
			if test.secureRequest {
				target = "https://example.test" + target
			}
			request := httptest.NewRequest(http.MethodGet, target, nil)
			if test.cookie != "" {
				request.AddCookie(&http.Cookie{Name: "lang", Value: test.cookie})
			}
			request.Header.Set("Accept-Language", test.accept)
			var got string
			handler := Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = LangFromRequest(r)
			}))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if got != test.want {
				t.Errorf("language = %q, want %q", got, test.want)
			}
			cookies := response.Result().Cookies()
			if test.wantCookie {
				if len(cookies) != 1 {
					t.Fatalf("query language cookie count = %d, want 1", len(cookies))
				}
				cookie := cookies[0]
				if cookie.Name != "lang" || cookie.Value != test.want || cookie.Path != "/" || cookie.MaxAge != 365*24*60*60 || cookie.SameSite != http.SameSiteLaxMode || !cookie.HttpOnly || cookie.Secure != test.secureRequest {
					t.Errorf("query language cookie = %+v", cookie)
				}
			} else if len(cookies) != 0 {
				t.Errorf("language detection unexpectedly set cookies: %v", cookies)
			}
		})
	}
}

func messageKeys(messages map[string]any) ([]string, map[string]map[string]string) {
	keys := make([]string, 0)
	plurals := make(map[string]map[string]string)
	var walk func(string, map[string]any)
	walk = func(prefix string, group map[string]any) {
		for name, value := range group {
			key := name
			if prefix != "" {
				key = prefix + "." + name
			}
			switch item := value.(type) {
			case string:
				keys = append(keys, key)
			case map[string]any:
				if forms, ok := pluralStringMap(item); ok {
					keys = append(keys, key)
					plurals[key] = forms
					continue
				}
				walk(key, item)
			}
		}
	}
	walk("", messages)
	sort.Strings(keys)
	return keys, plurals
}

func flattenMessages(messages map[string]any) map[string]string {
	result := make(map[string]string)
	var walk func(string, map[string]any)
	walk = func(prefix string, group map[string]any) {
		for name, value := range group {
			key := name
			if prefix != "" {
				key = prefix + "." + name
			}
			switch item := value.(type) {
			case string:
				result[key] = item
			case map[string]any:
				if forms, ok := pluralStringMap(item); ok {
					for category, message := range forms {
						result[key+"."+category] = message
					}
					continue
				}
				walk(key, item)
			}
		}
	}
	walk("", messages)
	return result
}

func isPluralCategory(key string, plurals map[string]map[string]string) bool {
	for pluralKey := range plurals {
		if strings.HasPrefix(key, pluralKey+".") {
			return true
		}
	}
	return false
}

func pluralStringMap(value any) (map[string]string, bool) {
	forms, ok := stringMap(value)
	if !ok || forms["other"] == "" {
		return nil, false
	}
	for category := range forms {
		switch category {
		case "zero", "one", "two", "few", "many", "other":
		default:
			return nil, false
		}
	}
	return forms, true
}

func stringMap(value any) (map[string]string, bool) {
	items, ok := value.(map[string]any)
	if !ok || len(items) == 0 {
		return nil, false
	}
	result := make(map[string]string, len(items))
	for key, item := range items {
		message, ok := item.(string)
		if !ok {
			return nil, false
		}
		result[key] = message
	}
	return result, true
}

func placeholderNames(message string) []string {
	matches := placeholderPattern.FindAllStringSubmatch(message, -1)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		result = append(result, match[1])
	}
	sort.Strings(result)
	return result
}

func withoutCount(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "count" {
			result = append(result, value)
		}
	}
	return result
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
