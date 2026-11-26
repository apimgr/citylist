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

var sharedPageShell = template.Must(template.New("shared-page-shell").Funcs(template.FuncMap{
	"t":            i18n.Translate,
	"asset":        assetURL,
	"operatorHTML": func(string) template.HTML { return "" },
}).Parse(`<!doctype html>
<html lang="{{.Lang}}" dir="{{.Dir}}" class="theme-{{.Theme}}">
<head>{{.Head}}</head>
<body>
  <a class="skip-link" href="#main-content">{{t .Lang "common.skip_main"}}</a>
  <a class="skip-link" href="#navigation">{{t .Lang "common.skip_navigation"}}</a>
  {{.TrackingNoscript}}
  {{.SiteBanners}}
  <input type="checkbox" id="nav-toggle" class="nav-checkbox" aria-label="{{t .Lang "nav.toggle"}}">
  <header class="header">
    <a class="nav-brand" href="{{.BaseURL}}/">{{if .LogoURL}}<img src="{{.LogoURL}}?size=200" alt="{{.BrandTitle}}">{{end}}{{.BrandTitle}}</a>
    <nav id="navigation" class="nav-links" aria-label="{{t .Lang "nav.main"}}">
      <a href="{{.BaseURL}}/" {{if eq .CurrentPath "/"}}class="nav-link active" aria-current="page"{{else}}class="nav-link"{{end}}>{{t .Lang "common.home"}}</a>
      <a href="{{.BaseURL}}/search" {{if eq .CurrentPath "/search"}}class="nav-link active" aria-current="page"{{else}}class="nav-link"{{end}}>{{t .Lang "common.search"}}</a>
      <a href="{{.BaseURL}}/coordinates" {{if eq .CurrentPath "/coordinates"}}class="nav-link active" aria-current="page"{{else}}class="nav-link"{{end}}>{{t .Lang "coordinates.title"}}</a>
    </nav>
    <label for="nav-toggle" class="nav-toggle" aria-hidden="true">☰</label>
    <div class="header-actions">
      <a class="nav-link" href="{{.BaseURL}}/server/preferences">{{t .Lang "common.preferences"}}</a>
      <form class="theme-toggle-form" action="{{.BaseURL}}/server/preferences" method="post"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><input type="hidden" name="redirect" value="{{.ReturnTo}}"><input type="hidden" name="theme" value="{{.NextTheme}}"><button class="btn btn-ghost" type="submit" aria-label="{{t .Lang (printf "common.%s" .NextTheme)}}">{{t .Lang (printf "common.%s" .NextTheme)}}</button></form>
    </div>
    {{operatorHTML "header"}}
  </header>
  <nav class="nav-panel" id="nav-panel" aria-label="{{t .Lang "nav.main"}}">
    <a href="{{.BaseURL}}/" {{if eq .CurrentPath "/"}}class="nav-link active" aria-current="page"{{else}}class="nav-link"{{end}}>{{t .Lang "common.home"}}</a>
    <a href="{{.BaseURL}}/search" {{if eq .CurrentPath "/search"}}class="nav-link active" aria-current="page"{{else}}class="nav-link"{{end}}>{{t .Lang "common.search"}}</a>
    <a href="{{.BaseURL}}/coordinates" {{if eq .CurrentPath "/coordinates"}}class="nav-link active" aria-current="page"{{else}}class="nav-link"{{end}}>{{t .Lang "coordinates.title"}}</a>
  </nav>
  <label for="nav-toggle" class="nav-overlay" aria-hidden="true"></label>
  <main id="main-content" class="{{.MainClass}}">{{.MainContent}}</main>
  <footer class="footer">
    {{operatorHTML "footer"}}
    {{if .TorAddress}}<p class="footer-onion"><a href="{{.BaseURL}}/server/help#tor-access" aria-label="{{t .Lang "pages.help.tor_title"}}">🧅</a> <code class="onion-address">{{.TorAddress}}</code> <button type="button" class="copy-btn" data-copy="{{.TorAddress}}" aria-label="{{t .Lang "common.copy"}}">{{t .Lang "common.copy"}}</button></p>{{end}}
    {{if .I2PAddress}}<p class="footer-i2p"><a href="{{.BaseURL}}/server/help#i2p-access" aria-label="{{t .Lang "pages.help.i2p_title"}}">🔗</a> <code class="i2p-address">{{.I2PAddress}}</code> <button type="button" class="copy-btn" data-copy="{{.I2PAddress}}" aria-label="{{t .Lang "common.copy"}}">{{t .Lang "common.copy"}}</button></p>{{end}}
    <nav class="footer-links" aria-label="{{t .Lang "nav.footer"}}">
      <a href="{{.BaseURL}}/server/about">{{t .Lang "nav.about"}}</a>
      <a href="{{.BaseURL}}/server/privacy">{{t .Lang "nav.privacy"}}</a>
      <a href="{{.BaseURL}}/server/help">{{t .Lang "nav.help"}}</a>
      <a href="{{.BaseURL}}/server/preferences">{{t .Lang "common.preferences"}}</a>
      <a href="{{.BaseURL}}/server/preferences#language" lang="{{.Lang}}">{{.LanguageName}}</a>
      <a href="{{.BaseURL}}/server/docs/swagger">{{t .Lang "common.api_docs"}}</a>
      <a href="{{.BaseURL}}/server/docs/graphql">{{t .Lang "graphql.title"}}</a>
      <a href="{{.BaseURL}}/server/terms">{{t .Lang "nav.terms"}}</a>
    </nav>
    <p><a href="https://github.com/apimgr/citylist">{{t .Lang "footer.made_with"}}</a> ❤️ · {{.Version}}</p>
    <p><a href="{{.BaseURL}}/server/healthz">{{t .Lang "footer.last_update"}}: {{.BuildDate}}</a></p>
  </footer>
  {{.ConsentBanner}}
  <script src="{{asset (printf "%s/static/js/app.js" .BaseURL)}}" defer></script>
  {{.PageScripts}}
</body>
</html>`))

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
