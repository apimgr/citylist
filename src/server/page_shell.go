package server

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/apimgr/citylist/src/common/i18n"
)

var (
	pageMainPattern    = regexp.MustCompile(`(?is)<main\b([^>]*)>(.*?)</main\s*>`)
	pageClassPattern   = regexp.MustCompile(`\bcontainer-(sm|md|lg)\b`)
	pageScriptPattern  = regexp.MustCompile(`(?is)<script\b[^>]*\bsrc=["'][^"']+["'][^>]*>\s*</script\s*>`)
	pageScriptSrc      = regexp.MustCompile(`(?is)\bsrc=["']([^"']+)["']`)
	pageAttributeClass = regexp.MustCompile(`(?is)\bclass=["']([^"']*)["']`)
)

var sharedPageShell = mustParseEmbeddedTemplate("template/layout/public.tmpl", template.FuncMap{
	"t":            i18n.Translate,
	"asset":        assetURL,
	"operatorHTML": func(string) template.HTML { return "" },
}, "template/partial/public/header.tmpl", "template/partial/public/footer.tmpl")

func (s *Server) applySharedPageShell(r *http.Request, rendered string, data map[string]interface{}) (string, error) {
	mainMatch := pageMainPattern.FindStringSubmatchIndex(rendered)
	if len(mainMatch) < 6 {
		return "", fmt.Errorf("rendered page has no main element")
	}
	mainAttrs := rendered[mainMatch[2]:mainMatch[3]]
	mainContent := rendered[mainMatch[4]:mainMatch[5]]
	mainClass := "container container-md"
	if classMatch := pageAttributeClass.FindStringSubmatch(mainAttrs); len(classMatch) == 2 {
		width := "container-md"
		if match := pageClassPattern.FindStringSubmatch(classMatch[1]); len(match) == 2 {
			width = "container-" + match[1]
		}
		mainClass = "container " + width
	}
	head := ""
	if start := strings.Index(strings.ToLower(rendered), "<head>"); start >= 0 {
		start += len("<head>")
		if end := strings.Index(strings.ToLower(rendered[start:]), "</head>"); end >= 0 {
			head = rendered[start : start+end]
		}
	}
	var scripts strings.Builder
	for _, script := range pageScriptPattern.FindAllString(rendered[mainMatch[1]:], -1) {
		if match := pageScriptSrc.FindStringSubmatch(script); len(match) == 2 {
			parsed, err := url.Parse(match[1])
			if err == nil && strings.HasSuffix(parsed.Path, "/static/js/app.js") {
				continue
			}
			scripts.WriteString(script)
		}
	}
	lang := i18n.LangFromRequest(r)
	languageName := lang
	for _, language := range i18n.Languages() {
		if language.Code == lang {
			languageName = language.NativeName
			break
		}
	}
	returnTo, _ := data["ReturnTo"].(string)
	if returnTo == "" {
		returnTo = r.URL.RequestURI()
	}
	torAddress, i2pAddress := s.publishedOverlayAddresses()
	values := map[string]interface{}{
		"Head": template.HTML(head), "MainClass": mainClass, "MainContent": template.HTML(mainContent), "PageScripts": template.HTML(scripts.String()),
		"Lang": lang, "Dir": i18n.Direction(lang), "Theme": themePreference(r), "BaseURL": strings.TrimSuffix(s.baseURL, "/"),
		"BrandTitle": s.brandTitle(), "LogoURL": s.imageURL("logo", r), "CurrentPath": r.URL.Path, "CSRFToken": data["CSRFToken"],
		"LanguageName": languageName, "Version": s.version, "BuildDate": s.buildDate, "TorAddress": torAddress, "I2PAddress": i2pAddress,
		"ReturnTo": returnTo, "NextTheme": nextTheme(themePreference(r)), "TrackingNoscript": data["TrackingNoscript"],
		"SiteBanners": data["SiteBanners"], "ConsentBanner": data["ConsentBanner"],
	}
	var output strings.Builder
	page, err := sharedPageShell.Clone()
	if err != nil {
		return "", err
	}
	page = page.Funcs(template.FuncMap{"operatorHTML": func(pageName string) template.HTML { return s.operatorHTML(r, pageName) }})
	if err := page.Execute(&output, values); err != nil {
		return "", err
	}
	return output.String(), nil
}
