package server

import (
	"encoding/json"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/apimgr/citylist/src/config"
)

var (
	htmlBodyTagPattern   = regexp.MustCompile(`(?i)<body\b[^>]*>`)
	htmlFooterTagPattern = regexp.MustCompile(`(?i)<footer\b`)
)

func injectRenderPageHooks(source string) string {
	source = strings.Replace(source, "</head>", "{{trackingScript}}\n</head>", 1)
	insertedBody := false
	source = htmlBodyTagPattern.ReplaceAllStringFunc(source, func(tag string) string {
		if insertedBody {
			return tag
		}
		insertedBody = true
		return tag + "{{trackingNoscript}}"
	})
	source = strings.Replace(source, "</header>", `{{operatorHTML "header"}}</header>`, 1)
	source = htmlFooterTagPattern.ReplaceAllString(source, `{{operatorHTML "footer"}}<footer`)
	return source
}

type tracker struct {
	tags    string
	body    string
	scripts []string
	connect []string
	images  []string
}

func jsString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func trackerFor(cfg config.TrackingConfig) (tracker, bool) {
	id := html.EscapeString(cfg.ID)
	base := strings.TrimSuffix(cfg.URL, "/")
	providerHost := func(value string) string {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" {
			return ""
		}
		return parsed.Scheme + "://" + parsed.Host
	}
	switch cfg.Type {
	case "google":
		if strings.HasPrefix(cfg.ID, "G-") {
			return tracker{
				tags: `<script async src="https://www.googletagmanager.com/gtag/js?id=` + url.QueryEscape(cfg.ID) + `"></script>`,
				body: "window.dataLayer=window.dataLayer||[];function gtag(){dataLayer.push(arguments);}" +
					"gtag('js',new Date());gtag('config'," + jsString(cfg.ID) + ");",
				scripts: []string{"https://www.googletagmanager.com"}, connect: []string{"https://www.google-analytics.com", "https://analytics.google.com"},
			}, true
		}
		return tracker{
			tags: `<script async src="https://www.google-analytics.com/analytics.js"></script>`,
			body: "window.ga=window.ga||function(){(ga.q=ga.q||[]).push(arguments)};ga.l=+new Date;" +
				"ga('create'," + jsString(cfg.ID) + ", 'auto');ga('send','pageview');",
			scripts: []string{"https://www.google-analytics.com"}, connect: []string{"https://www.google-analytics.com"},
		}, true
	case "matomo", "piwik":
		host := providerHost(cfg.URL)
		return tracker{
			body: "var _paq=window._paq=window._paq||[];_paq.push(['trackPageView']);_paq.push(['enableLinkTracking']);" +
				"_paq.push(['setTrackerUrl'," + jsString(base+"/matomo.php") + "]);_paq.push(['setSiteId'," + jsString(cfg.ID) + "]);" +
				"var g=document.createElement('script');g.async=true;g.src=" + jsString(base+"/matomo.js") + ";document.head.appendChild(g);",
			scripts: []string{host}, connect: []string{host},
		}, true
	case "owa":
		host := providerHost(cfg.URL)
		return tracker{
			body: "var owa_baseUrl=" + jsString(base+"/") + ";var owa_cmds=window.owa_cmds=window.owa_cmds||[];" +
				"owa_cmds.push(['setSiteId'," + jsString(cfg.ID) + "]);owa_cmds.push(['trackPageView']);owa_cmds.push(['trackClicks']);" +
				"var o=document.createElement('script');o.async=true;o.src=owa_baseUrl+'modules/base/js/owa.tracker-combined-min.js';document.head.appendChild(o);",
			scripts: []string{host}, connect: []string{host},
		}, true
	case "fathom":
		source := "https://cdn.usefathom.com/script.js"
		host := "https://cdn.usefathom.com"
		if cfg.URL != "" {
			source, host = base+"/tracker.js", providerHost(cfg.URL)
		}
		return tracker{tags: `<script src="` + html.EscapeString(source) + `" data-site="` + id + `" defer></script>`, scripts: []string{host}, connect: []string{host, "https://api.usefathom.com"}}, true
	case "plausible":
		source, host := "https://plausible.io/js/script.js", "https://plausible.io"
		if cfg.URL != "" {
			source, host = base+"/js/script.js", providerHost(cfg.URL)
		}
		return tracker{tags: `<script defer data-domain="` + id + `" src="` + html.EscapeString(source) + `"></script>`, scripts: []string{host}, connect: []string{host}}, true
	case "umami":
		host := providerHost(cfg.URL)
		return tracker{tags: `<script defer src="` + html.EscapeString(base) + `/script.js" data-website-id="` + id + `"></script>`, scripts: []string{host}, connect: []string{host}}, true
	case "simple":
		return tracker{
			tags:    `<script async defer src="https://scripts.simpleanalyticscdn.com/latest.js"></script>`,
			scripts: []string{"https://scripts.simpleanalyticscdn.com"}, connect: []string{"https://queue.simpleanalyticscdn.com"}, images: []string{"https://queue.simpleanalyticscdn.com"},
		}, true
	case "cloudflare":
		return tracker{
			tags:    `<script defer src="https://static.cloudflareinsights.com/beacon.min.js" data-cf-beacon='{"token":"` + id + `"}'></script>`,
			scripts: []string{"https://static.cloudflareinsights.com"}, connect: []string{"https://cloudflareinsights.com"},
		}, true
	default:
		return tracker{}, false
	}
}

func (s *Server) trackingAllowed(r *http.Request) bool {
	if s.cfg.Server.Tracking.Type == "" || !s.cfg.Server.Privacy.Cookies.Analytics.Enabled || s.trackingOverlayRequest(r) || privacySignalOptOut(r) {
		return false
	}
	if cookie, err := r.Cookie("ccpa_opt_out"); err == nil && strings.EqualFold(cookie.Value, "true") {
		return false
	}
	consent, valid := readConsentCookie(r)
	return valid && consent.Analytics
}

func (s *Server) trackingOverlayRequest(r *http.Request) bool {
	return s.contactRequestOnPrivateNetwork(r)
}

func (s *Server) trackingScript(r *http.Request) template.HTML {
	if !s.trackingAllowed(r) {
		return ""
	}
	configured, ok := trackerFor(s.cfg.Server.Tracking)
	if !ok {
		return ""
	}
	output := configured.tags
	if configured.body != "" {
		output += `<script async src="` + html.EscapeString(s.BuildURL(r, "/server/tracking.js")) + `"></script>`
	}
	return template.HTML(output)
}

func (s *Server) trackingNoscript(r *http.Request) template.HTML {
	if !s.trackingAllowed(r) || s.cfg.Server.Tracking.Type != "simple" {
		return ""
	}
	return template.HTML(`<noscript><img src="https://queue.simpleanalyticscdn.com/noscript.gif" alt="" referrerpolicy="no-referrer-when-downgrade"></noscript>`)
}

func (s *Server) handleTrackingScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.trackingAllowed(r) {
		http.NotFound(w, r)
		return
	}
	configured, ok := trackerFor(s.cfg.Server.Tracking)
	if !ok || configured.body == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write([]byte(configured.body))
}

func (s *Server) trackingCSPHosts() (scripts, connects, images []string) {
	configured, ok := trackerFor(s.cfg.Server.Tracking)
	if !ok {
		return nil, nil, nil
	}
	return configured.scripts, configured.connect, configured.images
}
