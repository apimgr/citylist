package server

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/apimgr/citylist/src/common/i18n"
)

const maxMCPRequestBytes = 1 << 20

const supportedMCPVersion = "2026-07-28"

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Meta    map[string]any  `json:"_meta"`
}

type mcpCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

type mcpTool struct {
	Name        string                                       `json:"name"`
	Description string                                       `json:"description"`
	InputSchema map[string]interface{}                       `json:"inputSchema"`
	Annotations map[string]bool                              `json:"annotations,omitempty"`
	Path        func(map[string]interface{}) (string, error) `json:"-"`
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		mcpHTTPError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", i18n.Translate(i18n.LangFromRequest(r), "mcp.http.post_only"))
		return
	}
	if !s.validMCPOrigin(r) {
		mcpHTTPError(w, http.StatusForbidden, "FORBIDDEN", i18n.Translate(i18n.LangFromRequest(r), "errors.forbidden"))
		return
	}
	if _, _, ok := s.resolveBearerToken(r, true); !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="citylist"`)
		mcpErrorWithStatus(w, http.StatusUnauthorized, nil, -32000, i18n.Translate(i18n.LangFromRequest(r), "errors.unauthorized"), nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMCPRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	var message mcpRequest
	if err := decoder.Decode(&message); err != nil {
		mcpHTTPError(w, http.StatusBadRequest, "INVALID_MCP_REQUEST", i18n.Translate(i18n.LangFromRequest(r), "mcp.http.invalid_body"))
		return
	}
	var extra interface{}
	if err := decoder.Decode(&extra); err != io.EOF {
		mcpHTTPError(w, http.StatusBadRequest, "INVALID_MCP_REQUEST", i18n.Translate(i18n.LangFromRequest(r), "mcp.http.invalid_body_trailing"))
		return
	}
	protocolVersion := r.Header.Get("MCP-Protocol-Version")
	metaVersion, _ := message.Meta["io.modelcontextprotocol/protocolVersion"].(string)
	legacyInitialize := message.Method == "initialize" && protocolVersion == "" && metaVersion == ""
	if legacyInitialize || protocolVersion != supportedMCPVersion {
		mcpError(w, message.ID, -32000, i18n.TranslateFormat(i18n.LangFromRequest(r), "mcp.errors.unsupported_protocol_version", map[string]string{"version": supportedMCPVersion}), map[string]interface{}{"supported": []string{supportedMCPVersion}})
		return
	}
	if protocolVersion == "" || metaVersion != protocolVersion || r.Header.Get("Mcp-Method") != message.Method {
		mcpErrorWithStatus(w, http.StatusBadRequest, message.ID, -32020, i18n.Translate(i18n.LangFromRequest(r), "mcp.errors.header_mismatch"), nil)
		return
	}
	if !acceptsMCP(r.Header.Get("Accept")) {
		mcpHTTPError(w, http.StatusNotAcceptable, "NOT_ACCEPTABLE", i18n.Translate(i18n.LangFromRequest(r), "mcp.http.accept_required"))
		return
	}
	if message.JSONRPC != "2.0" || message.Method == "" {
		mcpError(w, message.ID, -32600, i18n.Translate(i18n.LangFromRequest(r), "mcp.errors.invalid_request"), nil)
		return
	}
	if len(message.ID) == 0 && strings.HasPrefix(message.Method, "notifications/") {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if len(message.ID) == 0 {
		mcpError(w, nil, -32600, i18n.Translate(i18n.LangFromRequest(r), "mcp.errors.request_id_required"), nil)
		return
	}
	var result interface{}
	switch message.Method {
	case "initialize":
		result = s.mcpInitialize()
	case "ping":
		result = map[string]interface{}{}
	case "tools/list":
		result = map[string]interface{}{"tools": s.mcpTools(i18n.LangFromRequest(r))}
	case "tools/call":
		var params mcpCallParams
		if err := json.Unmarshal(message.Params, &params); err != nil || params.Name == "" {
			mcpError(w, message.ID, -32602, i18n.Translate(i18n.LangFromRequest(r), "mcp.errors.invalid_params"), nil)
			return
		}
		if headerName, ok := decodeMCPName(r.Header.Get("Mcp-Name")); !ok || headerName != params.Name {
			mcpErrorWithStatus(w, http.StatusBadRequest, message.ID, -32020, i18n.Translate(i18n.LangFromRequest(r), "mcp.errors.header_mismatch"), nil)
			return
		}
		result = s.mcpCall(params, r)
	default:
		mcpErrorWithStatus(w, http.StatusNotFound, message.ID, -32601, i18n.Translate(i18n.LangFromRequest(r), "mcp.errors.method_not_found"), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": message.ID, "result": result})
}

func decodeMCPName(value string) (string, bool) {
	if value == "" {
		return "", false
	}
	if strings.HasPrefix(value, "=?") {
		decoded, err := (&mime.WordDecoder{}).DecodeHeader(value)
		return decoded, err == nil
	}
	for _, char := range value {
		if char < 0x20 || char > 0x7e {
			return "", false
		}
	}
	return value, true
}

func acceptsMCP(value string) bool {
	accepted := map[string]bool{}
	for _, candidate := range strings.Split(value, ",") {
		mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(candidate))
		if err != nil {
			continue
		}
		quality := 1.0
		if rawQuality, exists := parameters["q"]; exists {
			if _, err := fmt.Sscanf(rawQuality, "%f", &quality); err != nil || quality <= 0 || quality > 1 {
				continue
			}
		}
		accepted[strings.ToLower(mediaType)] = true
	}
	return accepted["application/json"] && accepted["text/event-stream"]
}

func (s *Server) validMCPOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	resolved, err := url.Parse(s.BuildURL(r, "/"))
	if err == nil && strings.EqualFold(parsed.Scheme, resolved.Scheme) && strings.EqualFold(parsed.Host, resolved.Host) {
		return true
	}
	if fqdn := validSecurityHost(s.cfg.Server.FQDN); fqdn != "" && strings.EqualFold(parsed.Hostname(), fqdn) && strings.EqualFold(parsed.Scheme, resolved.Scheme) {
		return true
	}
	allowedOrigins, wildcard, disabled := s.corsAllowedOrigins(r)
	if !disabled && !wildcard {
		for _, allowed := range allowedOrigins {
			if strings.EqualFold(parsed.Scheme+"://"+parsed.Host, allowed) {
				return true
			}
		}
	}
	if !disabled {
		for _, domain := range configuredDomains() {
			allowedURL, parseErr := url.Parse("https://" + domain)
			if parseErr == nil && strings.EqualFold(parsed.Scheme, allowedURL.Scheme) && strings.EqualFold(parsed.Host, allowedURL.Host) {
				return true
			}
		}
	}
	return false
}

func (s *Server) mcpInitialize() interface{} {
	return map[string]interface{}{
		"protocolVersion": supportedMCPVersion,
		"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
		"serverInfo":      map[string]string{"name": "citylist", "version": s.version},
	}
}

func (s *Server) mcpTools(languages ...string) []mcpTool {
	language := "en"
	if len(languages) > 0 {
		language = languages[0]
	}
	translate := func(key string) string { return i18n.Translate(language, key) }
	stringSchema := func(description string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": description}
	}
	numberSchema := func(description string) map[string]interface{} {
		return map[string]interface{}{"type": "number", "description": description}
	}
	integerSchema := func(description string) map[string]interface{} {
		return map[string]interface{}{"type": "integer", "description": description}
	}
	tools := []mcpTool{
		mcpTool{Name: "get_city", Description: translate("mcp.tools.get_city"), InputSchema: mcpObjectSchema(map[string]interface{}{"id": stringSchema(translate("mcp.fields.geonames_id"))}, "id"), Path: func(a map[string]interface{}) (string, error) { return pathWithID("/cities/", a, "id") }},
		mcpTool{Name: "list_cities", Description: translate("mcp.tools.list_cities"), InputSchema: mcpObjectSchema(map[string]interface{}{"page": integerSchema(translate("mcp.fields.page_number")), "limit": integerSchema(translate("mcp.fields.limit_1_100"))}), Path: func(a map[string]interface{}) (string, error) { return queryPath("/cities", a, "page", "limit") }},
		mcpTool{Name: "search_cities", Description: translate("mcp.tools.search_cities"), InputSchema: mcpObjectSchema(map[string]interface{}{"q": stringSchema(translate("mcp.fields.city_name")), "country": stringSchema(translate("mcp.fields.iso_country_code")), "limit": integerSchema(translate("mcp.fields.limit_1_100"))}, "q"), Path: func(a map[string]interface{}) (string, error) {
			return queryPath("/cities/search", a, "q", "country", "limit")
		}},
		mcpTool{Name: "cities_by_country", Description: translate("mcp.tools.cities_by_country"), InputSchema: mcpObjectSchema(map[string]interface{}{"code": stringSchema(translate("mcp.fields.iso_country_code")), "limit": integerSchema(translate("mcp.fields.limit_1_100"))}, "code"), Path: func(a map[string]interface{}) (string, error) {
			code, err := requiredString(a, "code")
			if err != nil {
				return "", err
			}
			return "/cities/country/" + url.PathEscape(code) + querySuffix(a, "limit"), nil
		}},
		mcpTool{Name: "nearest_city", Description: translate("mcp.tools.nearest_city"), InputSchema: mcpObjectSchema(map[string]interface{}{"latitude": numberSchema(translate("mcp.fields.latitude")), "longitude": numberSchema(translate("mcp.fields.longitude"))}, "latitude", "longitude"), Path: func(a map[string]interface{}) (string, error) {
			return queryPath("/cities/coordinates", a, "latitude", "longitude")
		}},
		mcpTool{Name: "nearby_cities", Description: translate("mcp.tools.nearby_cities"), InputSchema: mcpObjectSchema(map[string]interface{}{"lat": numberSchema(translate("mcp.fields.latitude")), "lon": numberSchema(translate("mcp.fields.longitude")), "radius": numberSchema(translate("mcp.fields.radius_km")), "limit": integerSchema(translate("mcp.fields.limit"))}, "lat", "lon"), Path: func(a map[string]interface{}) (string, error) {
			return queryPath("/cities/nearby", a, "lat", "lon", "radius", "limit")
		}},
		mcpTool{Name: "get_zipcode", Description: translate("mcp.tools.get_zipcode"), InputSchema: mcpObjectSchema(map[string]interface{}{"zip": stringSchema(translate("mcp.fields.zip_code"))}, "zip"), Path: func(a map[string]interface{}) (string, error) { return pathWithID("/zipcodes/", a, "zip") }},
		mcpTool{Name: "search_zipcodes", Description: translate("mcp.tools.search_zipcodes"), InputSchema: mcpObjectSchema(map[string]interface{}{"q": stringSchema(translate("mcp.fields.city_name")), "state": stringSchema(translate("mcp.fields.state_abbreviation")), "limit": integerSchema(translate("mcp.fields.limit"))}, "q"), Path: func(a map[string]interface{}) (string, error) {
			return queryPath("/zipcodes/search", a, "q", "state", "limit")
		}},
		mcpTool{Name: "zipcodes_by_state", Description: translate("mcp.tools.zipcodes_by_state"), InputSchema: mcpObjectSchema(map[string]interface{}{"state": stringSchema(translate("mcp.fields.state_abbreviation")), "page": integerSchema(translate("mcp.fields.page_number")), "limit": integerSchema(translate("mcp.fields.limit"))}, "state"), Path: func(a map[string]interface{}) (string, error) {
			state, err := requiredString(a, "state")
			if err != nil {
				return "", err
			}
			return "/zipcodes/state/" + url.PathEscape(state) + querySuffix(a, "page", "limit"), nil
		}},
		mcpTool{Name: "nearby_zipcodes", Description: translate("mcp.tools.nearby_zipcodes"), InputSchema: mcpObjectSchema(map[string]interface{}{"lat": numberSchema(translate("mcp.fields.latitude")), "lon": numberSchema(translate("mcp.fields.longitude")), "radius_km": numberSchema(translate("mcp.fields.radius_km")), "limit": integerSchema(translate("mcp.fields.limit"))}, "lat", "lon"), Path: func(a map[string]interface{}) (string, error) {
			return queryPath("/zipcodes/nearby", a, "lat", "lon", "radius_km", "limit")
		}},
		mcpTool{Name: "random_city", Description: translate("mcp.tools.random_city"), InputSchema: mcpObjectSchema(nil), Path: func(map[string]interface{}) (string, error) { return "/random", nil }},
		mcpTool{Name: "dataset_stats", Description: translate("mcp.tools.dataset_stats"), InputSchema: mcpObjectSchema(nil), Path: func(map[string]interface{}) (string, error) { return "/stats", nil }},
		mcpTool{Name: "city_count", Description: translate("mcp.tools.city_count"), InputSchema: mcpObjectSchema(nil), Path: func(map[string]interface{}) (string, error) { return "/count", nil }},
	}
	for index := range tools {
		tools[index].Annotations = map[string]bool{"readOnlyHint": true}
	}
	return tools
}

func mcpObjectSchema(properties map[string]interface{}, required ...string) map[string]interface{} {
	if properties == nil {
		properties = map[string]interface{}{}
	}
	schema := map[string]interface{}{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func (s *Server) mcpCall(params mcpCallParams, source *http.Request) map[string]interface{} {
	for _, tool := range s.mcpTools(i18n.LangFromRequest(source)) {
		if tool.Name != params.Name {
			continue
		}
		path, err := tool.Path(params.Arguments)
		if err != nil {
			return mcpToolError(i18n.Translate(i18n.LangFromRequest(source), "errors.bad_request"))
		}
		request := httptest.NewRequest(http.MethodGet, "/api/"+s.cfg.Server.APIVersion+path, nil)
		request.RemoteAddr = source.RemoteAddr
		request.Header.Set("Accept", "application/json")
		request.Header.Set("User-Agent", "citylist-mcp")
		request.Header.Set("Accept-Language", i18n.LangFromRequest(source))
		if authorization := source.Header.Get("Authorization"); authorization != "" {
			request.Header.Set("Authorization", authorization)
		}
		response := httptest.NewRecorder()
		s.router.ServeHTTP(response, request)
		content := strings.TrimSpace(response.Body.String())
		if response.Code < 200 || response.Code >= 300 {
			return mcpToolError(content)
		}
		return map[string]interface{}{"content": []map[string]string{{"type": "text", "text": content}}, "isError": false}
	}
	return mcpToolError(i18n.Translate(i18n.LangFromRequest(source), "errors.not_found"))
}

func mcpToolError(message string) map[string]interface{} {
	return map[string]interface{}{"content": []map[string]string{{"type": "text", "text": message}}, "isError": true}
}

func pathWithID(prefix string, args map[string]interface{}, key string) (string, error) {
	value, err := requiredString(args, key)
	if err != nil {
		return "", err
	}
	return prefix + url.PathEscape(value), nil
}

func requiredString(args map[string]interface{}, key string) (string, error) {
	value, ok := args[key]
	if !ok || value == nil {
		return "", fmt.Errorf("%s is required", key)
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || len(text) > 256 {
		return "", fmt.Errorf("%s must contain between 1 and 256 characters", key)
	}
	return text, nil
}

func queryPath(route string, args map[string]interface{}, keys ...string) (string, error) {
	values := url.Values{}
	for _, key := range keys {
		value, ok := args[key]
		if !ok || value == nil {
			continue
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "" || len(text) > 256 {
			return "", fmt.Errorf("%s must contain between 1 and 256 characters", key)
		}
		values.Set(key, text)
	}
	if values.Encode() == "" {
		return route, nil
	}
	return route + "?" + values.Encode(), nil
}

func querySuffix(args map[string]interface{}, keys ...string) string {
	values := url.Values{}
	for _, key := range keys {
		if value, ok := args[key]; ok && value != nil {
			values.Set(key, strings.TrimSpace(fmt.Sprint(value)))
		}
	}
	if values.Encode() == "" {
		return ""
	}
	return "?" + values.Encode()
}

func mcpError(w http.ResponseWriter, id json.RawMessage, code int, message string, data interface{}) {
	mcpErrorWithStatus(w, http.StatusBadRequest, id, code, message, data)
}

func mcpErrorWithStatus(w http.ResponseWriter, status int, id json.RawMessage, code int, message string, data interface{}) {
	errorBody := map[string]interface{}{"code": code, "message": message}
	if data != nil {
		errorBody["data"] = data
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"jsonrpc": "2.0", "id": id, "error": errorBody})
}

func mcpHTTPError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": false, "error": code, "message": message})
}

type mcpHelpData struct {
	Endpoint         string
	APIAlias         string
	RootAlias        string
	Lang             string
	LanguageName     string
	Version          string
	Dir              string
	Theme            string
	NextTheme        string
	BaseURL          string
	BrandTitle       string
	SEOHead          template.HTML
	HasAccent        bool
	CurrentPath      string
	ReturnTo         string
	CSRFToken        string
	ConsentBanner    template.HTML
	SiteBanners      template.HTML
	OperatorHeader   template.HTML
	OperatorFooter   template.HTML
	TrackingScript   template.HTML
	TrackingNoscript template.HTML
	ClaudeCommand    string
	GenericConfig    string
	ToolsListCommand string
	Tools            []mcpHelpTool
	Project          template.HTML
	HasProject       bool
	Text             map[string]string
}

type mcpHelpTool struct {
	Name        string
	Description template.HTML
}

var mcpHelpTemplate = template.Must(template.New("mcp-help").Funcs(template.FuncMap{"t": i18n.Translate, "asset": assetURL}).Parse(`<!doctype html>
<html lang="{{.Lang}}" dir="{{.Dir}}" class="theme-{{.Theme}}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="base-url" content="{{.BaseURL}}">
  {{.SEOHead}}
  <link rel="stylesheet" href="{{asset (printf "%s/static/css/common.css" .BaseURL)}}">
  <link rel="stylesheet" href="{{asset (printf "%s/static/css/components.css" .BaseURL)}}">
  {{if .HasAccent}}<link rel="stylesheet" href="{{asset (printf "%s/static/css/accent.css" .BaseURL)}}">{{end}}
</head>
<body>
  {{.TrackingNoscript}}
  <a class="skip-link" href="#main-content">{{t .Lang "common.skip_main"}}</a>
  <a class="skip-link" href="#navigation">{{t .Lang "common.skip_navigation"}}</a>
  {{.SiteBanners}}
  <input type="checkbox" id="nav-toggle" class="nav-checkbox" aria-label="{{t .Lang "nav.toggle"}}">
  <header class="header">
    <a class="nav-brand" href="{{.BaseURL}}/">{{.BrandTitle}}</a>
    <nav id="navigation" class="nav-links" aria-label="{{t .Lang "nav.main"}}">
      <a href="{{.BaseURL}}/" {{if eq .CurrentPath "/"}}class="nav-link active" aria-current="page"{{else}}class="nav-link"{{end}}>{{t .Lang "common.home"}}</a>
      <a href="{{.BaseURL}}/search" {{if eq .CurrentPath "/search"}}class="nav-link active" aria-current="page"{{else}}class="nav-link"{{end}}>{{t .Lang "common.search"}}</a>
      <a href="{{.BaseURL}}/coordinates" {{if eq .CurrentPath "/coordinates"}}class="nav-link active" aria-current="page"{{else}}class="nav-link"{{end}}>{{t .Lang "coordinates.title"}}</a>
    </nav>
    <label for="nav-toggle" class="nav-toggle" aria-hidden="true">☰</label>
    <div class="header-actions">
      <a class="nav-link" href="{{.BaseURL}}/server/preferences">{{t .Lang "common.preferences"}}</a>
      <form class="theme-toggle-form" action="{{.BaseURL}}/server/preferences" method="post"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><input type="hidden" name="redirect" value="{{.ReturnTo}}"><input type="hidden" name="theme" value="{{.NextTheme}}"><button class="btn btn-ghost" type="submit" aria-label="{{t .Lang "common.theme_toggle"}}">{{t .Lang "common.theme_toggle"}}</button></form>
    </div>
    {{.OperatorHeader}}
  </header>
  <main id="main-content" class="container container-md page-shell">
    <nav class="breadcrumb" aria-label="{{t .Lang "common.breadcrumb"}}"><a href="{{.BaseURL}}/server/help">{{t .Lang "common.help"}}</a></nav>
    <article>
      <header class="page-header"><h1 class="page-title">{{index .Text "title"}}</h1><p class="page-lead">{{index .Text "page_lead"}}</p></header>
      <section class="section-card" id="overview" aria-labelledby="overview-title"><h2 id="overview-title">{{index .Text "overview_title"}}</h2><p>{{index .Text "overview_body"}}</p></section>
      <section class="section-card" id="endpoint" aria-labelledby="endpoint-title"><h2 id="endpoint-title">{{index .Text "endpoint_title"}}</h2><p>{{index .Text "endpoint_prompt"}}</p><div class="code-block"><code class="code-content selectable-url">{{.Endpoint}}</code><button type="button" class="copy-btn" data-copy="{{.Endpoint}}" aria-label="{{t .Lang "common.copy"}}"><span class="copy-text" aria-live="polite">{{t .Lang "common.copy"}}</span></button></div><p>{{index .Text "aliases_label"}}</p><div class="code-block"><code class="code-content selectable-url">{{.APIAlias}}</code><button type="button" class="copy-btn" data-copy="{{.APIAlias}}" aria-label="{{t .Lang "common.copy"}}"><span class="copy-text" aria-live="polite">{{t .Lang "common.copy"}}</span></button></div><div class="code-block"><code class="code-content selectable-url">{{.RootAlias}}</code><button type="button" class="copy-btn" data-copy="{{.RootAlias}}" aria-label="{{t .Lang "common.copy"}}"><span class="copy-text" aria-live="polite">{{t .Lang "common.copy"}}</span></button></div></section>
      <section class="section-card" id="authentication" aria-labelledby="authentication-title"><h2 id="authentication-title">{{index .Text "authentication_title"}}</h2><p>{{index .Text "authentication_body"}}</p></section>
      <section class="section-card" id="connect" aria-labelledby="connect-title"><h2 id="connect-title">{{index .Text "connect_title"}}</h2><p>{{index .Text "connect_intro"}}</p><h3>{{index .Text "claude_code_title"}}</h3><div class="code-block"><pre><code>{{.ClaudeCommand}}</code></pre><button type="button" class="copy-btn" data-copy="{{.ClaudeCommand}}" aria-label="{{t .Lang "common.copy"}}"><span class="copy-text" aria-live="polite">{{t .Lang "common.copy"}}</span></button></div><h3>{{index .Text "generic_config_title"}}</h3><div class="code-block"><pre><code>{{.GenericConfig}}</code></pre><button type="button" class="copy-btn" data-copy="{{.GenericConfig}}" aria-label="{{t .Lang "common.copy"}}"><span class="copy-text" aria-live="polite">{{t .Lang "common.copy"}}</span></button></div><p>{{index .Text "protocol_headers"}}</p><h3>{{index .Text "tools_list_title"}}</h3><div class="code-block"><pre><code>{{.ToolsListCommand}}</code></pre><button type="button" class="copy-btn" data-copy="{{.ToolsListCommand}}" aria-label="{{t .Lang "common.copy"}}"><span class="copy-text" aria-live="polite">{{t .Lang "common.copy"}}</span></button></div></section>
      <section class="section-card" id="tools" aria-labelledby="tools-title"><h2 id="tools-title">{{index .Text "tools_title"}}</h2><div class="table-wrapper"><table class="data-table"><thead><tr><th scope="col">{{index .Text "tool_column"}}</th><th scope="col">{{index .Text "description_column"}}</th></tr></thead><tbody>{{range .Tools}}<tr><th scope="row">{{.Name}}</th><td>{{.Description}}</td></tr>{{end}}</tbody></table></div></section>
      <section class="section-card" id="limits" aria-labelledby="limits-title"><h2 id="limits-title">{{index .Text "limits_title"}}</h2><p>{{index .Text "limits_body"}}</p></section>
      <section class="section-card" id="troubleshooting" aria-labelledby="troubleshooting-title"><h2 id="troubleshooting-title">{{index .Text "troubleshooting_title"}}</h2><div class="table-wrapper"><table class="data-table"><thead><tr><th scope="col">{{index .Text "status_column"}}</th><th scope="col">{{index .Text "cause_fix_column"}}</th></tr></thead><tbody><tr><th scope="row">400</th><td>{{index .Text "http_400"}}</td></tr><tr><th scope="row">401</th><td>{{index .Text "http_401"}}</td></tr><tr><th scope="row">403</th><td>{{index .Text "http_403"}}</td></tr><tr><th scope="row">405</th><td>{{index .Text "http_405"}}</td></tr></tbody></table></div></section>
      {{if .HasProject}}<section class="section-card" id="project" aria-labelledby="project-title"><h2 id="project-title">{{index .Text "project_title"}}</h2>{{.Project}}</section>{{end}}
    </article>
  </main>
  <nav class="nav-panel" id="nav-panel" aria-label="{{t .Lang "nav.main"}}">
    <label for="nav-toggle" class="nav-close" role="button" tabindex="0" aria-label="{{t .Lang "nav.close"}}">✕</label>
    <a href="{{.BaseURL}}/" {{if eq .CurrentPath "/"}}class="nav-link active" aria-current="page"{{else}}class="nav-link"{{end}}>{{t .Lang "common.home"}}</a>
    <a href="{{.BaseURL}}/search" {{if eq .CurrentPath "/search"}}class="nav-link active" aria-current="page"{{else}}class="nav-link"{{end}}>{{t .Lang "common.search"}}</a>
    <a href="{{.BaseURL}}/coordinates" {{if eq .CurrentPath "/coordinates"}}class="nav-link active" aria-current="page"{{else}}class="nav-link"{{end}}>{{t .Lang "coordinates.title"}}</a>
  </nav>
  <label for="nav-toggle" class="nav-overlay" aria-hidden="true"></label>
  {{.OperatorFooter}}
  <footer class="footer">
    <nav class="footer-links" aria-label="{{t .Lang "nav.footer"}}">
      <a href="{{.BaseURL}}/server/about">{{t .Lang "nav.about"}}</a>
      <a href="{{.BaseURL}}/server/help">{{t .Lang "nav.help"}}</a>
      <a href="{{.BaseURL}}/server/docs/swagger">{{t .Lang "common.api_docs"}}</a>
      <a href="{{.BaseURL}}/server/docs/graphql">GraphQL</a>
      <a href="{{.BaseURL}}/server/privacy">{{t .Lang "nav.privacy"}}</a>
      <a href="{{.BaseURL}}/server/terms">{{t .Lang "nav.terms"}}</a>
      <a href="{{.BaseURL}}/server/preferences#language" lang="{{.Lang}}">{{.LanguageName}}</a>
    </nav>
    <small>{{.BrandTitle}} {{.Version}}</small>
  </footer>
  {{.TrackingScript}}
  <script src="{{asset (printf "%s/static/js/app.js" .BaseURL)}}" defer></script>
  {{.ConsentBanner}}
</body>
</html>`))

func (s *Server) handleMCPHelp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		mcpHTTPError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", i18n.Translate(i18n.LangFromRequest(r), "errors.method_not_allowed"))
		return
	}
	base := strings.TrimSuffix(s.BuildURL(r, "/"), "/")
	apiURL := s.BuildURL(r, "/api/"+s.cfg.Server.APIVersion+"/server/mcp")
	apiAlias := s.BuildURL(r, "/api/mcp")
	rootAlias := s.BuildURL(r, "/mcp")
	if r.Method == http.MethodHead {
		contentType := "text/html; charset=utf-8"
		if strings.Contains(r.Header.Get("Accept"), "application/json") {
			contentType = "application/json; charset=utf-8"
		} else if strings.Contains(r.Header.Get("Accept"), "text/plain") {
			contentType = "text/plain; charset=utf-8"
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		return
	}
	lang := i18n.LangFromRequest(r)
	project := s.cfg.Server.Pages.Help.MCP
	tools := s.mcpTools(lang)
	data := map[string]interface{}{
		"enabled": true,
		"url":     apiURL,
		"aliases": []string{apiAlias, rootAlias},
		"auth":    map[string]string{"scheme": "Bearer"},
		"tools":   publicMCPToolDescriptions(tools),
		"project": project,
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		s.respondSuccess(w, data, 1)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		mcpHTTPError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", i18n.Translate(i18n.LangFromRequest(r), "errors.method_not_allowed"))
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "text/plain") {
		plain := i18n.Translate(lang, "mcp.help.plain_title") + "\n\n" + i18n.Translate(lang, "mcp.help.plain_endpoint") + " " + apiURL + "\n" + i18n.Translate(lang, "mcp.help.plain_aliases") + " " + apiAlias + ", " + rootAlias + "\n" + i18n.Translate(lang, "mcp.help.plain_read_only") + "\n"
		if project != "" {
			plain += "\n" + project + "\n"
		}
		s.respondText(w, plain)
		return
	}
	projectHTML := template.HTML(renderSecurityPolicyMarkdown(project))
	htmlTools := make([]mcpHelpTool, 0, len(tools))
	for _, tool := range tools {
		htmlTools = append(htmlTools, mcpHelpTool{Name: tool.Name, Description: template.HTML(renderSecurityPolicyMarkdown(tool.Description))})
	}
	claudeCommand := fmt.Sprintf("claude mcp add --transport http citylist %s --header \"Authorization: Bearer $MCP_TOKEN\"", apiURL)
	genericConfig, err := json.MarshalIndent(map[string]interface{}{"mcpServers": map[string]interface{}{"citylist": map[string]interface{}{"type": "http", "url": apiURL, "headers": map[string]string{"Authorization": "Bearer $MCP_TOKEN"}}}}, "", "  ")
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "")
		return
	}
	toolsListCommand := fmt.Sprintf("curl -q -LSsf -X POST \\\n  -H \"Authorization: Bearer $MCP_TOKEN\" \\\n  -H \"Content-Type: application/json\" \\\n  -H \"MCP-Protocol-Version: %s\" \\\n  -H \"Mcp-Method: tools/list\" \\\n  --data-binary '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\",\"_meta\":{\"io.modelcontextprotocol/protocolVersion\":\"%s\"},\"params\":{}}' \\\n  %s", supportedMCPVersion, supportedMCPVersion, apiURL)
	languageName := lang
	for _, language := range i18n.Languages() {
		if language.Code == lang {
			languageName = language.NativeName
			break
		}
	}
	dataHTML := mcpHelpData{Endpoint: apiURL, APIAlias: apiAlias, RootAlias: rootAlias, Lang: lang, LanguageName: languageName, Version: s.version, Dir: i18n.Direction(lang),
		ClaudeCommand: claudeCommand, GenericConfig: string(genericConfig), ToolsListCommand: toolsListCommand,
		Tools: htmlTools, Project: projectHTML, HasProject: strings.TrimSpace(project) != "",
		Text: mcpHelpStrings(lang, "/api/"+s.cfg.Server.APIVersion+"/server/preferences/tokens/primary")}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	csrfToken := s.csrfToken(w, r)
	dataHTML.BaseURL = base
	dataHTML.BrandTitle = s.brandTitle()
	dataHTML.HasAccent = s.cfg.Web.UI.Accent != ""
	dataHTML.CurrentPath = r.URL.Path
	dataHTML.SEOHead = s.seoHead(r, r.URL.Path, dataHTML.Text["title"], dataHTML.Text["overview_body"])
	dataHTML.Theme = themePreference(r)
	dataHTML.NextTheme = nextTheme(dataHTML.Theme)
	dataHTML.ReturnTo = r.URL.RequestURI()
	dataHTML.CSRFToken = csrfToken
	dataHTML.ConsentBanner = s.renderCookieConsentBanner(r, csrfToken, dataHTML.ReturnTo)
	dataHTML.SiteBanners = s.activeAnnouncements(r, csrfToken, dataHTML.ReturnTo)
	dataHTML.OperatorHeader = s.operatorHTML(r, "header")
	dataHTML.OperatorFooter = s.operatorHTML(r, "footer")
	dataHTML.TrackingScript = s.trackingScript(r)
	dataHTML.TrackingNoscript = s.trackingNoscript(r)
	var rendered strings.Builder
	if err := mcpHelpTemplate.Execute(&rendered, dataHTML); err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "")
		return
	}
	shared, err := s.applySharedPageShell(r, rendered.String(), map[string]interface{}{
		"CSRFToken": dataHTML.CSRFToken, "ReturnTo": dataHTML.ReturnTo, "SiteBanners": dataHTML.SiteBanners,
		"ConsentBanner": dataHTML.ConsentBanner, "TrackingNoscript": dataHTML.TrackingNoscript,
	})
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "")
		return
	}
	_, _ = io.WriteString(w, shared)
}

func mcpHelpStrings(language, tokenEndpoint string) map[string]string {
	text := map[string]string{}
	for _, key := range []string{
		"title", "page_lead", "overview_title", "overview_body", "endpoint_title", "endpoint_prompt", "aliases_label",
		"authentication_title", "connect_title", "connect_intro", "protocol_headers", "tools_title",
		"tool_column", "description_column", "limits_title", "limits_body", "troubleshooting_title",
		"status_column", "cause_fix_column", "http_400", "http_401", "http_403", "http_405",
		"project_title", "generic_config_title", "tools_list_title", "claude_code_title",
		"plain_title", "plain_endpoint", "plain_aliases", "plain_read_only",
	} {
		text[key] = i18n.Translate(language, "mcp.help."+key)
	}
	text["authentication_body"] = i18n.TranslateFormat(language, "mcp.help.authentication_body", map[string]string{
		"issue_endpoint": tokenEndpoint,
		"token_page":     "/server/preferences/tokens",
	})
	return text
}

func publicMCPToolDescriptions(tools []mcpTool) []map[string]string {
	result := make([]map[string]string, len(tools))
	for i, tool := range tools {
		result[i] = map[string]string{"name": tool.Name, "description": tool.Description}
	}
	return result
}
