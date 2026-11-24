//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/remote"
	htmlparser "golang.org/x/net/html"
)

func baseURL(t *testing.T) string {
	t.Helper()
	base := strings.TrimRight(os.Getenv("CITYLIST_BASE_URL"), "/")
	if base == "" {
		t.Fatal("CITYLIST_BASE_URL must point to the running Citylist server")
	}
	return base
}

func browserContext(t *testing.T) context.Context {
	t.Helper()
	endpoint := os.Getenv("CHROME_REMOTE_DEBUGGING_URL")
	if endpoint == "" {
		t.Fatal("CHROME_REMOTE_DEBUGGING_URL must point to the headless Chromium CDP endpoint")
	}
	ctx, cancel := remote.NewAllocator(context.Background(), endpoint)
	t.Cleanup(cancel)
	ctx, cancel = chromedp.NewContext(ctx)
	t.Cleanup(cancel)
	ctx, cancel = context.WithTimeout(ctx, 90*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func saveBrowserFailure(t *testing.T, ctx context.Context) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		dir := os.Getenv("E2E_ARTIFACT_DIR")
		if dir == "" {
			projectName, projectOrg := "citylist", "apimgr"
			if info, ok := debug.ReadBuildInfo(); ok {
				parts := strings.Split(info.Main.Path, "/")
				if len(parts) >= 2 {
					projectName, projectOrg = parts[len(parts)-1], parts[len(parts)-2]
				}
			}
			parent := filepath.Join(os.TempDir(), projectOrg)
			if err := os.MkdirAll(parent, 0700); err != nil {
				t.Errorf("create project-scoped artifact directory: %v", err)
				return
			}
			var err error
			dir, err = os.MkdirTemp(parent, projectName+"-")
			if err != nil {
				t.Errorf("create project-scoped artifact directory: %v", err)
				return
			}
			if err := os.Chmod(dir, 0700); err != nil {
				t.Errorf("restrict artifact directory permissions: %v", err)
				return
			}
		}
		page, _ := chromedp.Run(ctx, chromedp.OuterHTML(chromedp.CSS("html")))
		screenshot, _ := chromedp.Run(ctx, chromedp.CaptureScreenshot())
		_ = os.WriteFile(filepath.Join(dir, strings.ReplaceAll(t.Name(), "/", "-")+".html"), []byte(page), 0600)
		_ = os.WriteFile(filepath.Join(dir, strings.ReplaceAll(t.Name(), "/", "-")+".png"), screenshot, 0600)
	})
}

func getPage(t *testing.T, path string) (int, string) {
	t.Helper()
	client := &http.Client{Timeout: 15 * time.Second}
	request, err := http.NewRequest(http.MethodGet, baseURL(t)+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "text/html")
	request.Header.Set("User-Agent", "Mozilla/5.0 CitylistE2E/1.0")
	resp, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

func assertPageAccessibility(t *testing.T, source string) {
	t.Helper()
	document, err := htmlparser.Parse(strings.NewReader(source))
	if err != nil {
		t.Fatalf("parse rendered HTML: %v", err)
	}
	var elements []*htmlparser.Node
	var walk func(*htmlparser.Node)
	walk = func(node *htmlparser.Node) {
		if node.Type == htmlparser.ElementNode {
			elements = append(elements, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	attribute := func(node *htmlparser.Node, name string) string {
		for _, item := range node.Attr {
			if item.Key == name {
				return item.Val
			}
		}
		return ""
	}
	ids, labels := map[string]bool{}, map[string]bool{}
	landmarks := map[string]bool{}
	for _, node := range elements {
		if id := attribute(node, "id"); id != "" {
			ids[id] = true
		}
		if node.Data == "label" {
			labels[attribute(node, "for")] = true
		}
		switch node.Data {
		case "main", "nav", "header", "footer":
			landmarks[node.Data] = true
		}
	}
	if !ids["main-content"] || !landmarks["main"] || !landmarks["nav"] {
		t.Errorf("page is missing the main-content skip target or main/navigation landmarks")
	}
	firstFocusable := ""
	for _, node := range elements {
		if node.Data == "a" && strings.Contains(attribute(node, "class"), "skip-link") {
			firstFocusable = attribute(node, "href")
			break
		}
	}
	if firstFocusable != "#main-content" {
		t.Errorf("first skip link target = %q, want #main-content", firstFocusable)
	}
	lastHeading := 0
	hasHeading := false
	for _, node := range elements {
		switch node.Data {
		case "img":
			if _, exists := func() (string, bool) {
				for _, item := range node.Attr {
					if item.Key == "alt" {
						return item.Val, true
					}
				}
				return "", false
			}(); !exists {
				t.Errorf("image is missing an alt attribute")
			}
		case "input", "select", "textarea":
			if node.Data == "input" && strings.EqualFold(attribute(node, "type"), "hidden") {
				continue
			}
			id := attribute(node, "id")
			if id == "" || !labels[id] && attribute(node, "aria-label") == "" && attribute(node, "aria-labelledby") == "" {
				if !hasLabelAncestor(node) {
					t.Errorf("%s control %q has no associated label", node.Data, attribute(node, "name"))
				}
			}
		default:
			if len(node.Data) == 2 && node.Data[0] == 'h' && node.Data[1] >= '1' && node.Data[1] <= '6' {
				level := int(node.Data[1] - '0')
				if !hasHeading && level != 1 {
					t.Errorf("first heading is h%d, want h1", level)
				}
				if hasHeading && level > lastHeading+1 {
					t.Errorf("heading hierarchy jumps from h%d to h%d", lastHeading, level)
				}
				hasHeading, lastHeading = true, level
			}
		}
	}
	if !hasHeading {
		t.Errorf("page has no heading")
	}
	for _, target := range []string{"#main-content", "#navigation"} {
		found := false
		for _, node := range elements {
			if node.Data == "a" && strings.Contains(attribute(node, "class"), "skip-link") && attribute(node, "href") == target {
				found = true
			}
		}
		if !found {
			t.Errorf("page is missing skip link to %s", target)
		}
	}
}

func hasLabelAncestor(node *htmlparser.Node) bool {
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if parent.Type == htmlparser.ElementNode && parent.Data == "label" {
			return true
		}
	}
	return false
}

func TestTier1SSRFeaturePages(t *testing.T) {
	pages := []struct {
		path    string
		content string
	}{
		{path: "/", content: "Citylist"},
		{path: "/search?q=London&country=GB", content: "London"},
		{path: "/search?type=zip&q=10001", content: "New York"},
		{path: "/coordinates?lat=51.5072&lon=-0.1276&radius=25", content: "London"},
		{path: "/server/about", content: "Citylist"},
		{path: "/server/help", content: "Citylist"},
		{path: "/server/help/mcp", content: "MCP"},
		{path: "/server/contact", content: "Contact"},
		{path: "/server/security", content: "Security"},
		{path: "/server/security/policy", content: "Security"},
		{path: "/server/security/thanks", content: "Security"},
		{path: "/server/privacy", content: "Privacy"},
		{path: "/server/dpo", content: "DPO"},
		{path: "/server/terms", content: "Terms"},
		{path: "/server/preferences", content: "Preferences"},
		{path: "/server/preferences/tokens", content: "Token"},
		{path: "/server/preferences/sync", content: "Preferences"},
		{path: "/server/docs/swagger", content: "OpenAPI"},
		{path: "/server/docs/graphql", content: "GraphQL"},
	}
	for _, page := range pages {
		t.Run(page.path, func(t *testing.T) {
			code, html := getPage(t, page.path)
			if code != http.StatusOK {
				t.Fatalf("GET %s returned %d", page.path, code)
			}
			for _, marker := range []string{"<!doctype html>", "<title>", "<meta charset=", "name=\"viewport\"", "<main"} {
				if !strings.Contains(strings.ToLower(html), marker) {
					t.Errorf("SSR response lacks %q", marker)
				}
			}
			if !strings.Contains(strings.ToLower(html), strings.ToLower(page.content)) {
				t.Errorf("SSR response lacks real page content %q", page.content)
			}
			assertPageAccessibility(t, html)
		})
	}

	code, html := getPage(t, "/search?q=x")
	if code != http.StatusBadRequest || !strings.Contains(html, "form-error") {
		t.Errorf("invalid search should render a 400 page with a server-side error; got %d", code)
	}
	code, html = getPage(t, "/__citylist_e2e_missing_route__")
	if code != http.StatusNotFound || !strings.Contains(html, "404") {
		t.Errorf("unknown path should render a themed 404; got %d", code)
	}
}

func TestTier2NoJavaScriptSearchAndCoordinates(t *testing.T) {
	ctx := browserContext(t)
	saveBrowserFailure(t, ctx)
	if _, err := chromedp.Call(ctx, emulation.SetScriptExecutionDisabled, emulation.SetScriptExecutionDisabledParams{Value: true}); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Do(ctx,
		chromedp.Navigate(baseURL(t)+"/search"),
		chromedp.SetValue(chromedp.CSS("#query"), "London"),
		chromedp.Submit(chromedp.CSS("#searchForm")),
		chromedp.WaitVisible(chromedp.CSS("#resultsList .result-card-title")),
	); err != nil {
		t.Fatal(err)
	}
	page, err := chromedp.Run(ctx, chromedp.OuterHTML(chromedp.CSS("html")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, "London") {
		t.Fatal("native search form did not render London without JavaScript")
	}
	if err := chromedp.Do(ctx,
		chromedp.Navigate(baseURL(t)+"/coordinates"),
		chromedp.SetValue(chromedp.CSS("#lat"), "51.5072"),
		chromedp.SetValue(chromedp.CSS("#lon"), "-0.1276"),
		chromedp.Submit(chromedp.CSS("#coordForm")),
		chromedp.WaitVisible(chromedp.CSS("#resultsList .result-card-title")),
	); err != nil {
		t.Fatal(err)
	}
	page, err = chromedp.Run(ctx, chromedp.OuterHTML(chromedp.CSS("html")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, "London") {
		t.Fatal("native coordinate form did not render real nearby city results without JavaScript")
	}
	if err := chromedp.Do(ctx,
		chromedp.Navigate(baseURL(t)+"/search"),
		chromedp.SetValue(chromedp.CSS("#searchType"), "zip"),
		chromedp.SetValue(chromedp.CSS("#query"), "10001"),
		chromedp.Submit(chromedp.CSS("#searchForm")),
		chromedp.WaitVisible(chromedp.CSS("#resultsList .result-card-title")),
	); err != nil {
		t.Fatal(err)
	}
	page, err = chromedp.Run(ctx, chromedp.OuterHTML(chromedp.CSS("html")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, "10001") || !strings.Contains(page, "New York") {
		t.Fatal("native ZIP lookup did not render its real result without JavaScript")
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL(t)+"/search?q=x")); err != nil {
		t.Fatal(err)
	}
	page, err = chromedp.Run(ctx, chromedp.OuterHTML(chromedp.CSS("html")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, "form-error") {
		t.Fatal("invalid search did not render its validation error without JavaScript")
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL(t)+"/server/preferences")); err != nil {
		t.Fatal(err)
	}
	csrf, err := chromedp.Run(ctx, chromedp.Evaluate[string](`document.querySelector('form[action="/server/preferences"] input[name="csrf_token"]').value`))
	if err != nil || csrf == "" {
		t.Fatalf("no-JavaScript preference form did not include its CSRF token: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, baseURL(t)+"/server/preferences", strings.NewReader("theme=light"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Errorf("preference form without CSRF returned %d, want 403", response.StatusCode)
	}
}

func TestTier3BrowserSearchPreferencesAndResponsiveLayout(t *testing.T) {
	ctx := browserContext(t)
	saveBrowserFailure(t, ctx)
	var mu sync.Mutex
	var failed []string
	var consoleErrors []string
	if err := chromedp.Listen(ctx, func(event any) {
		mu.Lock()
		defer mu.Unlock()
		switch e := event.(type) {
		case *network.EventLoadingFailed:
			failed = append(failed, e.ErrorText)
		case *runtime.EventExceptionThrown:
			consoleErrors = append(consoleErrors, e.ExceptionDetails.Text)
		case *runtime.EventConsoleAPICalled:
			if e.Type == runtime.ConsoleAPICalledTypeError {
				consoleErrors = append(consoleErrors, "console.error")
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := chromedp.Call(ctx, network.Enable, network.EnableParams{}); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Do(ctx,
		chromedp.Navigate(baseURL(t)+"/search"),
		chromedp.SetValue(chromedp.CSS("#query"), "London"),
		chromedp.SetValue(chromedp.CSS("#country"), "GB"),
		chromedp.Click(chromedp.CSS("#searchForm button[type=submit]")),
		chromedp.WaitVisible(chromedp.CSS("#resultsList .result-card-title")),
	); err != nil {
		t.Fatal(err)
	}
	page, err := chromedp.Run(ctx, chromedp.OuterHTML(chromedp.CSS("html")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, "London") {
		t.Fatal("enhanced city search did not render a London result")
	}
	if err := chromedp.Do(ctx,
		chromedp.SetValue(chromedp.CSS("#searchType"), "zip"),
		chromedp.SetValue(chromedp.CSS("#query"), "10001"),
		chromedp.Click(chromedp.CSS("#searchForm button[type=submit]")),
		chromedp.WaitVisible(chromedp.CSS("#resultsList .result-card-title")),
	); err != nil {
		t.Fatal(err)
	}
	page, err = chromedp.Run(ctx, chromedp.OuterHTML(chromedp.CSS("html")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, "10001") || !strings.Contains(page, "New York") {
		t.Fatal("enhanced ZIP lookup did not render its real result")
	}

	if _, err := chromedp.Call(ctx, emulation.SetDeviceMetricsOverride, emulation.SetDeviceMetricsOverrideParams{
		Width: 375, Height: 812, DeviceScaleFactor: 1, Mobile: false,
	}); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL(t)+"/search")); err != nil {
		t.Fatal(err)
	}
	noHorizontalOverflow, err := chromedp.Run(ctx, chromedp.Evaluate[bool](`document.documentElement.scrollWidth <= window.innerWidth`))
	if err != nil {
		t.Fatal(err)
	}
	if !noHorizontalOverflow {
		overflow, _ := chromedp.Run(ctx, chromedp.Evaluate[string](`JSON.stringify(Array.from(document.querySelectorAll("body *")).map(element => { const rect = element.getBoundingClientRect(); return {tag: element.tagName, id: element.id, className: typeof element.className === "string" ? element.className : "", left: Math.round(rect.left), right: Math.round(rect.right), width: Math.round(rect.width)}; }).filter(rect => rect.left < 0 || rect.right > innerWidth).slice(0, 8))`))
		t.Errorf("375px viewport has horizontal page overflow: %s", overflow)
	}
	navigationUsable, err := chromedp.Run(ctx, chromedp.Evaluate[bool](`document.querySelectorAll("#navigation a.nav-link").length > 0 && Array.from(document.querySelectorAll("#navigation a.nav-link")).every(link => link.getBoundingClientRect().width > 0)`))
	if err != nil {
		t.Fatal(err)
	}
	if !navigationUsable {
		if err := chromedp.Do(ctx, chromedp.Click(chromedp.CSS(".nav-toggle"))); err != nil {
			t.Fatal(err)
		}
		navigationUsable, err = chromedp.Run(ctx, chromedp.Evaluate[bool](`document.querySelectorAll("#nav-panel a.nav-link").length > 0 && Array.from(document.querySelectorAll("#nav-panel a.nav-link")).every(link => { const r = link.getBoundingClientRect(); return r.width >= 44 && r.height >= 44; })`))
		if err != nil {
			t.Fatal(err)
		}
		if !navigationUsable {
			t.Error("mobile drawer navigation links are not usable at a 375px viewport")
		}
	}

	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL(t)+"/server/preferences")); err != nil {
		t.Fatal(err)
	}
	initialBackground, err := chromedp.Run(ctx, chromedp.Evaluate[string](`getComputedStyle(document.body).backgroundColor`))
	if err != nil {
		t.Fatal(err)
	}
	initialHeading, err := chromedp.Run(ctx, chromedp.Evaluate[string](`document.querySelector("h1").textContent.trim()`))
	if err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Do(ctx,
		chromedp.SetValue(chromedp.CSS("#theme"), "light"),
		chromedp.SetValue(chromedp.CSS("#lang"), "es"),
	); err != nil {
		t.Fatal(err)
	}
	consentChecked, err := chromedp.Run(ctx, chromedp.Evaluate[bool](`(() => { const checkbox = document.querySelector("#consent-preferences"); checkbox.checked = true; return checkbox.checked; })()`))
	if err != nil {
		t.Fatal(err)
	}
	if !consentChecked {
		t.Fatal("could not enable preference-cookie consent")
	}
	preferenceFormState, err := chromedp.Run(ctx, chromedp.Evaluate[string](`JSON.stringify({theme:document.querySelector("#theme").value,lang:document.querySelector("#lang").value,consent:document.querySelector("#consent-preferences").checked})`))
	if err != nil {
		t.Fatal(err)
	}
	if preferenceFormState != `{"theme":"light","lang":"es","consent":true}` {
		t.Fatalf("browser did not prepare preference values for submission: %s", preferenceFormState)
	}
	if err := chromedp.Do(ctx, chromedp.Click(chromedp.CSS("#main-content form button[type=submit]")), chromedp.Sleep(500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Do(ctx, chromedp.Sleep(500*time.Millisecond), chromedp.Navigate(baseURL(t)+"/server/preferences")); err != nil {
		t.Fatal(err)
	}
	language, err := chromedp.Run(ctx, chromedp.Evaluate[string](`document.documentElement.lang`))
	if err != nil {
		t.Fatal(err)
	}
	theme, err := chromedp.Run(ctx, chromedp.Evaluate[string](`document.documentElement.className`))
	if err != nil {
		t.Fatal(err)
	}
	if language != "es" || !strings.Contains(theme, "theme-light") {
		t.Errorf("preferences did not apply; lang=%q class=%q", language, theme)
	}
	lightBackground, err := chromedp.Run(ctx, chromedp.Evaluate[string](`getComputedStyle(document.body).backgroundColor`))
	if err != nil {
		t.Fatal(err)
	}
	spanishHeading, err := chromedp.Run(ctx, chromedp.Evaluate[string](`document.querySelector("h1").textContent.trim()`))
	if err != nil {
		t.Fatal(err)
	}
	if lightBackground == initialBackground {
		t.Errorf("light theme did not change computed page background: %q", lightBackground)
	}
	if spanishHeading == initialHeading {
		t.Errorf("Spanish preference did not change rendered strings: heading=%q", spanishHeading)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(failed) > 0 {
		t.Errorf("failed browser requests: %v", failed)
	}
	if len(consoleErrors) > 0 {
		t.Errorf("browser JavaScript errors: %v", consoleErrors)
	}
}

func tokenManagementStatus(t *testing.T, token, method, path string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, baseURL(t)+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func linkedTokenExpiry(t *testing.T, token, name string) time.Time {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL(t)+"/api/v1/server/preferences/tokens", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list linked tokens returned %d", resp.StatusCode)
	}
	var response struct {
		Data struct {
			Linked []struct {
				Name      string  `json:"name"`
				ExpiresAt *string `json:"expires_at"`
			} `json:"linked"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&response); err != nil {
		t.Fatal(err)
	}
	for _, linked := range response.Data.Linked {
		if linked.Name != name {
			continue
		}
		if linked.ExpiresAt == nil {
			t.Fatalf("linked token %q had no expiry", name)
		}
		expiry, err := time.Parse(time.RFC3339, *linked.ExpiresAt)
		if err != nil {
			t.Fatalf("parse linked token expiry %q: %v", *linked.ExpiresAt, err)
		}
		return expiry
	}
	t.Fatalf("linked token %q was not returned by the token API", name)
	return time.Time{}
}

func createLinkedToken(t *testing.T, primaryToken, name string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	body, err := json.Marshal(map[string]string{"name": name, "expires": "30d"})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL(t)+"/api/v1/server/preferences/tokens", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+primaryToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create sibling linked token returned %d", resp.StatusCode)
	}
	var response struct {
		Data struct {
			Token string `json:"linked_token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Token == "" {
		t.Fatal("linked-token creation returned no token")
	}
	return response.Data.Token
}

func callMCP(t *testing.T, token string, wantStatus int) {
	t.Helper()
	message := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"_meta":  map[string]string{"io.modelcontextprotocol/protocolVersion": "2026-07-28"},
		"params": map[string]any{"name": "get_city", "arguments": map[string]string{"id": "2643743"}},
	}
	body, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL(t)+"/api/v1/server/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", "get_city")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Errorf("MCP tools/call returned %d, want %d", resp.StatusCode, wantStatus)
	}
	if wantStatus == http.StatusOK {
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Contains(data, []byte("London")) {
			t.Error("MCP get_city did not return the expected city record")
		}
	}
}

func TestTier3OwnerTokenBrowserLifecycle(t *testing.T) {
	ctx := browserContext(t)
	saveBrowserFailure(t, ctx)
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL(t)+"/server/preferences/tokens")); err != nil {
		t.Fatal(err)
	}
	csrfState, err := chromedp.Run(ctx, chromedp.Evaluate[string](`(() => { const input = document.querySelector("form[action='/server/preferences/token/create'] input[name=csrf_token]"); const cookie = document.cookie.split(";").map(value => value.trim()).find(value => value.startsWith("csrf_token=")); return JSON.stringify({input:Boolean(input),cookie:Boolean(cookie),equal:Boolean(input && cookie && input.value === decodeURIComponent(cookie.slice("csrf_token=".length)))}); })()`))
	if err != nil || csrfState != `{"input":true,"cookie":true,"equal":true}` {
		t.Fatalf("token page CSRF form token does not match its browser cookie: %s, %v", csrfState, err)
	}
	if err := chromedp.Do(ctx,
		chromedp.Click(chromedp.CSS("form[action='/server/preferences/token/create'] button[type=submit]")),
		chromedp.WaitVisible(chromedp.CSS("#primary-token-title")),
	); err != nil {
		t.Fatal(err)
	}
	page, err := chromedp.Run(ctx, chromedp.OuterHTML(chromedp.CSS("html")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, `id="primary-token-title"`) {
		t.Fatal("primary token creation did not render the issued token")
	}
	tokenPattern := regexp.MustCompile(`own_[A-Za-z0-9]{32}`)
	primaryMatch := tokenPattern.FindString(page)
	if primaryMatch == "" {
		t.Fatal("primary token was not shown once after browser issuance")
	}
	if status := tokenManagementStatus(t, primaryMatch, http.MethodPost, "/api/v1/server/preferences/tokens/primary"); status != http.StatusConflict {
		t.Errorf("issuing a second primary token with a valid owner token returned %d, want 409", status)
	}
	checkToken := func(token string, want int) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, baseURL(t)+"/api/v1/server/auth/check", nil)
		if reqErr != nil {
			t.Fatal(reqErr)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, reqErr := (&http.Client{Timeout: 15 * time.Second}).Do(req)
		if reqErr != nil {
			t.Fatal(reqErr)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("owner token auth check returned %d, want %d", resp.StatusCode, want)
		}
	}

	if err := chromedp.Do(ctx,
		chromedp.SetValue(chromedp.CSS("#linked-name"), "e2e-client"),
		chromedp.SetValue(chromedp.CSS("#linked-expiry"), "30d"),
		chromedp.Click(chromedp.CSS("#main-content form[action='/server/preferences/tokens'] button[type=submit]")),
		chromedp.WaitVisible(chromedp.CSS(".token-card")),
	); err != nil {
		t.Fatal(err)
	}
	linkedRevealAction, err := chromedp.Run(ctx, chromedp.Evaluate[string](`document.querySelector('.token-card form[action*="/reveal"]').getAttribute('action')`))
	if err != nil || linkedRevealAction == "" {
		t.Fatalf("could not find linked token reveal form: %v", err)
	}
	if err := chromedp.Do(ctx,
		chromedp.Submit(chromedp.CSS("form[action='"+linkedRevealAction+"']")),
		chromedp.WaitVisible(chromedp.CSS(".token-card .copy-value")),
	); err != nil {
		t.Fatal(err)
	}
	linkedRaw, err := chromedp.Run(ctx, chromedp.Evaluate[string](`document.querySelector('.token-card .copy-value').textContent.trim()`))
	if err != nil || !tokenPattern.MatchString(linkedRaw) {
		t.Fatalf("linked token reveal did not show a valid token: %v", err)
	}
	if !strings.Contains(linkedRaw, "own_") {
		t.Fatal("linked token value was not revealed")
	}
	if status := tokenManagementStatus(t, linkedRaw, http.MethodGet, "/api/v1/server/preferences/tokens"); status != http.StatusForbidden {
		t.Errorf("linked token management request returned %d, want 403", status)
	}
	for _, request := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/server/preferences/tokens"},
		{http.MethodPost, "/api/v1/server/preferences/tokens/primary"},
		{http.MethodPost, "/api/v1/server/preferences/tokens/primary/reveal"},
		{http.MethodPost, "/api/v1/server/preferences/tokens/primary/rotate"},
		{http.MethodPost, "/api/v1/server/preferences/tokens/1/reveal"},
		{http.MethodPost, "/api/v1/server/preferences/tokens/1/update"},
		{http.MethodPost, "/api/v1/server/preferences/tokens/1/rotate"},
		{http.MethodPost, "/api/v1/server/preferences/tokens/1/revoke"},
		{http.MethodPatch, "/api/v1/server/preferences/tokens/1"},
		{http.MethodDelete, "/api/v1/server/preferences/tokens/1"},
	} {
		if status := tokenManagementStatus(t, linkedRaw, request.method, request.path); status != http.StatusForbidden {
			t.Errorf("linked token %s %s returned %d, want 403", request.method, request.path, status)
		}
	}
	if err := chromedp.Do(ctx, chromedp.Navigate(baseURL(t)+"/server/preferences/tokens")); err != nil {
		t.Fatal(err)
	}
	page, err = chromedp.Run(ctx, chromedp.OuterHTML(chromedp.CSS("html")))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(page, linkedRaw) {
		t.Error("linked token remained visible after navigating away from its reveal response")
	}
	callMCP(t, "", http.StatusUnauthorized)
	callMCP(t, primaryMatch, http.StatusOK)
	callMCP(t, linkedRaw, http.StatusOK)

	linkedIDAction, err := chromedp.Run(ctx, chromedp.Evaluate[string](`document.querySelector('.token-card form[action*="/update"]').getAttribute('action')`))
	if err != nil || linkedIDAction == "" {
		t.Fatalf("could not find linked token update form: %v", err)
	}
	if err := chromedp.Do(ctx,
		chromedp.SetValue(chromedp.CSS(".token-card form[action='"+linkedIDAction+"'] input[name=name]"), "e2e-renamed"),
		chromedp.Submit(chromedp.CSS("form[action='"+linkedIDAction+"']")),
	); err != nil {
		t.Fatal(err)
	}
	page, err = chromedp.Run(ctx, chromedp.OuterHTML(chromedp.CSS("html")))
	if err != nil || !strings.Contains(page, "e2e-renamed") {
		t.Fatalf("linked token rename did not render: %v", err)
	}
	if err := chromedp.Do(ctx,
		chromedp.SetValue(chromedp.CSS(".token-card form[action='"+linkedIDAction+"'] select[name=expiry]"), "7d"),
		chromedp.Submit(chromedp.CSS("form[action='"+linkedIDAction+"']")),
	); err != nil {
		t.Fatal(err)
	}
	expiry := linkedTokenExpiry(t, primaryMatch, "e2e-renamed")
	if remaining := time.Until(expiry); remaining < 6*24*time.Hour || remaining > 8*24*time.Hour {
		t.Errorf("linked token expiry was not updated to seven days: %s remaining", remaining)
	}
	siblingToken := createLinkedToken(t, primaryMatch, "e2e-sibling")
	linkedRotateAction, err := chromedp.Run(ctx, chromedp.Evaluate[string](`document.querySelector('.token-card form[action*="/rotate"]').getAttribute('action')`))
	if err != nil || linkedRotateAction == "" {
		t.Fatalf("could not find linked token rotation form: %v", err)
	}
	if err := chromedp.Do(ctx, chromedp.Submit(chromedp.CSS("form[action='"+linkedRotateAction+"']"))); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Do(ctx, chromedp.WaitVisible(chromedp.CSS(".token-card .code-copy"))); err != nil {
		t.Fatal(err)
	}
	page, err = chromedp.Run(ctx, chromedp.OuterHTML(chromedp.CSS("html")))
	if err != nil {
		t.Fatal(err)
	}
	rotatedLinked := tokenPattern.FindString(page)
	if rotatedLinked == "" || rotatedLinked == linkedRaw {
		t.Fatal("linked rotation did not issue a new token")
	}
	checkToken(primaryMatch, http.StatusOK)
	checkToken(linkedRaw, http.StatusUnauthorized)
	checkToken(rotatedLinked, http.StatusOK)
	checkToken(siblingToken, http.StatusOK)
	linkedRaw = rotatedLinked

	primaryRotateAction, err := chromedp.Run(ctx, chromedp.Evaluate[string](`document.querySelector('form[action$="/token/rotate"]').getAttribute('action')`))
	if err != nil || primaryRotateAction == "" {
		t.Fatalf("could not find primary rotation form: %v", err)
	}
	if err := chromedp.Do(ctx, chromedp.Submit(chromedp.CSS("form[action='"+primaryRotateAction+"']"))); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Do(ctx, chromedp.WaitVisible(chromedp.CSS("#primary .code-copy"))); err != nil {
		t.Fatal(err)
	}
	page, err = chromedp.Run(ctx, chromedp.OuterHTML(chromedp.CSS("html")))
	if err != nil {
		t.Fatal(err)
	}
	rotated := tokenPattern.FindString(page)
	if rotated == "" || rotated == primaryMatch {
		t.Fatal("primary rotation did not issue a new token")
	}
	checkToken(primaryMatch, http.StatusUnauthorized)
	checkToken(rotated, http.StatusOK)
	checkToken(linkedRaw, http.StatusOK)
	checkToken(siblingToken, http.StatusOK)
	revokeAction := strings.TrimSuffix(linkedRotateAction, "/rotate") + "/revoke"
	if err := chromedp.Do(ctx, chromedp.Submit(chromedp.CSS("form[action='"+revokeAction+"']"))); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Do(ctx, chromedp.Sleep(250*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	checkToken(linkedRaw, http.StatusUnauthorized)
	checkToken(siblingToken, http.StatusOK)

	newContext := browserContext(t)
	if _, err := chromedp.Call(newContext, network.ClearBrowserCookies, cdp.Empty{}); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Do(newContext,
		chromedp.Navigate(baseURL(t)+"/server/preferences/tokens"),
		chromedp.SetValue(chromedp.CSS("#owner-token"), rotated),
		chromedp.Submit(chromedp.CSS("form[action='/server/preferences/token/restore']")),
		chromedp.WaitVisible(chromedp.CSS("#primary-token-title")),
	); err != nil {
		t.Fatal(err)
	}
	page, err = chromedp.Run(newContext, chromedp.OuterHTML(chromedp.CSS("html")))
	if err != nil || !strings.Contains(page, "primary-token-title") {
		t.Fatalf("restoring a token in a fresh browser did not restore owner management: %v", err)
	}
	if err := chromedp.Do(newContext,
		chromedp.Click(chromedp.CSS(`button[popovertarget="revoke-primary"]`)),
		chromedp.SetValue(chromedp.CSS("#revoke-primary-confirm"), rotated[len(rotated)-4:]),
		chromedp.Submit(chromedp.CSS("#revoke-primary form")),
		chromedp.WaitVisible(chromedp.CSS("#create-primary-title")),
	); err != nil {
		t.Fatal(err)
	}
	checkToken(rotated, http.StatusUnauthorized)
}

func TestTier3InternalPageCrawl(t *testing.T) {
	ctx := browserContext(t)
	saveBrowserFailure(t, ctx)
	var mu sync.Mutex
	var failedRequests []string
	var consoleErrors []string
	if err := chromedp.Listen(ctx, func(event any) {
		mu.Lock()
		defer mu.Unlock()
		switch e := event.(type) {
		case *network.EventLoadingFailed:
			failedRequests = append(failedRequests, e.ErrorText)
		case *runtime.EventExceptionThrown:
			consoleErrors = append(consoleErrors, e.ExceptionDetails.Text)
		case *runtime.EventConsoleAPICalled:
			if e.Type == runtime.ConsoleAPICalledTypeError {
				consoleErrors = append(consoleErrors, "console.error")
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := chromedp.Call(ctx, network.Enable, network.EnableParams{}); err != nil {
		t.Fatal(err)
	}
	base, err := url.Parse(baseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	queue := []string{"/"}
	visited := make(map[string]bool)
	for len(queue) > 0 && len(visited) < 100 {
		path := queue[0]
		queue = queue[1:]
		if visited[path] {
			continue
		}
		visited[path] = true
		client := &http.Client{Timeout: 15 * time.Second}
		response, requestErr := client.Get(baseURL(t) + path)
		if requestErr != nil {
			t.Errorf("request %s: %v", path, requestErr)
			continue
		}
		response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 400 {
			t.Errorf("internal route %s returned %d", path, response.StatusCode)
			continue
		}
		if err := chromedp.Do(ctx, chromedp.Navigate(baseURL(t)+path), chromedp.WaitReady(chromedp.CSS("body"))); err != nil {
			t.Errorf("navigate %s: %v", path, err)
			continue
		}
		assets, assetErr := chromedp.Run(ctx, chromedp.Evaluate[[]string](`Array.from(document.querySelectorAll('link[rel="stylesheet"],script[src],img[src],source[src]')).map(a => a.href || a.src)`))
		if assetErr != nil {
			t.Errorf("collect assets from %s: %v", path, assetErr)
		} else {
			for _, asset := range assets {
				assetURL, parseErr := url.Parse(asset)
				if parseErr != nil || assetURL.Host != base.Host {
					continue
				}
				assetResponse, assetRequestErr := (&http.Client{Timeout: 15 * time.Second}).Get(asset)
				if assetRequestErr != nil {
					t.Errorf("asset %s: %v", assetURL.Path, assetRequestErr)
					continue
				}
				assetResponse.Body.Close()
				if assetResponse.StatusCode != http.StatusOK {
					t.Errorf("asset %s returned %d", assetURL.Path, assetResponse.StatusCode)
				}
			}
		}
		links, err := chromedp.Run(ctx, chromedp.Evaluate[[]string](`Array.from(document.querySelectorAll('a[href]')).map(a => a.href)`))
		if err != nil {
			t.Errorf("collect links from %s: %v", path, err)
			continue
		}
		for _, link := range links {
			parsed, parseErr := url.Parse(link)
			if parseErr != nil || parsed.Host != base.Host || parsed.Scheme != base.Scheme {
				continue
			}
			next := parsed.EscapedPath()
			if parsed.RawQuery != "" {
				next += "?" + parsed.RawQuery
			}
			if !visited[next] {
				queue = append(queue, next)
			}
		}
	}
	if len(visited) < 10 {
		t.Errorf("internal page crawl reached only %d routes", len(visited))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(failedRequests) != 0 {
		t.Errorf("browser requests failed while crawling pages: %v", failedRequests)
	}
	if len(consoleErrors) != 0 {
		t.Errorf("browser JavaScript errors while crawling pages: %v", consoleErrors)
	}
}
