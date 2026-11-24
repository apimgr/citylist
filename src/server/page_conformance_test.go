package server

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/apimgr/citylist/src/config"
	htmlparser "golang.org/x/net/html"
)

func TestRegisteredPagesAndErrorsConformToHTMLRequirements(t *testing.T) {
	s := testServer(t)
	paths := make([]string, 0, len(serverPageRegistry)+3)
	for _, page := range serverPageRegistry {
		paths = append(paths, page.Path)
	}
	paths = append(paths, "/server/preferences", "/server/contact", "/server/healthz")
	resolvedLinks := make(map[string]int)
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Accept", "text/html")
			rec := httptest.NewRecorder()
			s.Router().ServeHTTP(rec, req)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("GET %s = %d %q, want HTML 200", path, rec.Code, rec.Header().Get("Content-Type"))
			}
			assertPageConformance(t, rec.Body.Bytes(), path, func(link string) int {
				linkURL, err := url.Parse(link)
				if err != nil {
					return http.StatusBadRequest
				}
				if linkURL.IsAbs() && linkURL.Host != "example.com" {
					return http.StatusOK
				}
				linkPath := linkURL.Path
				if linkPath == "" {
					linkPath = path
				}
				requestPath := linkPath
				if linkURL.RawQuery != "" {
					requestPath += "?" + linkURL.RawQuery
				}
				if status, ok := resolvedLinks[requestPath]; ok {
					return status
				}
				request := httptest.NewRequest(http.MethodGet, requestPath, nil)
				request.Header.Set("Accept", "text/html")
				response := httptest.NewRecorder()
				s.Router().ServeHTTP(response, request)
				resolvedLinks[requestPath] = response.Code
				return response.Code
			})
		})
	}

	for _, status := range []int{
		http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable,
	} {
		t.Run(fmt.Sprintf("error_%d", status), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/error-test", nil)
			req.Header.Set("Accept", "text/html")
			rec := httptest.NewRecorder()
			s.renderError(rec, req, status, "private error details")
			assertPageConformance(t, rec.Body.Bytes(), "/error-test", nil)
		})
	}
}

func TestOperatorHeaderContentIsLastAndPreservesSharedHeader(t *testing.T) {
	baseHeader := renderHeaderForConformance(t, testServer(t))
	cfg := config.DefaultConfig()
	cfg.Server.Branding.Content.Header = config.BrandingPageContent{
		Format: "html",
		Body:   `<span id="conformance-header-fragment">Operator notice</span>`,
	}
	configuredHeader := renderHeaderForConformance(t, testServerWithConfig(t, cfg))
	children := elementChildren(configuredHeader)
	if len(children) != 5 || attrValue(children[4], "id") != "conformance-header-fragment" {
		t.Fatalf("configured header fragment is not the final child: %s", nodeHTML(configuredHeader))
	}
	if len(elementChildren(baseHeader)) != 4 {
		t.Fatalf("base header has unexpected elements: %s", nodeHTML(baseHeader))
	}
	var before, after bytes.Buffer
	for _, child := range elementChildren(baseHeader) {
		clearDynamicCSRF(child)
		if err := htmlparser.Render(&before, child); err != nil {
			t.Fatal(err)
		}
	}
	for _, child := range children[:4] {
		clearDynamicCSRF(child)
		if err := htmlparser.Render(&after, child); err != nil {
			t.Fatal(err)
		}
	}
	if before.String() != after.String() {
		t.Fatalf("adding header content changed shared header markup\nbefore: %s\nafter:  %s", before.String(), after.String())
	}
}

func TestTextBrowserGetsNoJavaScriptHTML(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept", "text/html")
	request.Header.Set("User-Agent", "Lynx/2.9.0")
	testServer(t).Router().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("text browser GET / = %d %q, want HTML 200", response.Code, response.Header().Get("Content-Type"))
	}
	if strings.Contains(strings.ToLower(response.Body.String()), "<script") || strings.Contains(strings.ToLower(response.Body.String()), "<noscript") {
		t.Fatal("text browser response retained script or noscript markup")
	}
	if !strings.Contains(strings.Join(response.Header().Values("Vary"), ","), "User-Agent") {
		t.Fatal("text browser response does not vary by User-Agent")
	}
	variant := renderNoJSHTML([]byte(`<html><body><noscript><a href="/plain">Use the plain page</a></noscript><button type="button">Copy</button><script>window.alert(1)</script><main>Citylist</main></body></html>`))
	if strings.Contains(strings.ToLower(string(variant)), "<script") || strings.Contains(strings.ToLower(string(variant)), "<noscript") || strings.Contains(string(variant), "Copy") {
		t.Fatalf("no-JS transform retained JavaScript-only markup: %s", variant)
	}
	if !strings.Contains(string(variant), `href="/plain"`) || !strings.Contains(string(variant), "Citylist") {
		t.Fatalf("no-JS transform lost server content or fallback link: %s", variant)
	}
}

func clearDynamicCSRF(node *htmlparser.Node) {
	walkHTML(node, func(child *htmlparser.Node) {
		if child.Type != htmlparser.ElementNode || child.Data != "input" || attrValue(child, "name") != "csrf_token" {
			return
		}
		for index := range child.Attr {
			if strings.EqualFold(child.Attr[index].Key, "value") {
				child.Attr[index].Val = "[csrf-token]"
			}
		}
	})
}

func renderHeaderForConformance(t *testing.T, s *Server) *htmlparser.Node {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d", rec.Code)
	}
	doc, err := htmlparser.Parse(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	var header *htmlparser.Node
	walkHTML(doc, func(node *htmlparser.Node) {
		if node.Type == htmlparser.ElementNode && node.Data == "header" && hasClass(node, "header") {
			header = node
		}
	})
	if header == nil {
		t.Fatal("page has no shared header")
	}
	return header
}

func assertPageConformance(t *testing.T, body []byte, path string, resolveLink func(string) int) {
	t.Helper()
	doc, err := htmlparser.Parse(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var nodes []*htmlparser.Node
	walkHTML(doc, func(node *htmlparser.Node) { nodes = append(nodes, node) })
	mainCount, h1Count := 0, 0
	var mainNode, headerNode *htmlparser.Node
	ids := make(map[string]bool)
	labels := make(map[string]bool)
	controls := make([]*htmlparser.Node, 0)
	for _, node := range nodes {
		if node.Type != htmlparser.ElementNode {
			continue
		}
		switch node.Data {
		case "main":
			mainCount++
			mainNode = node
		case "h1":
			h1Count++
		case "header":
			if hasClass(node, "header") {
				headerNode = node
			}
		case "label":
			if id := attrValue(node, "for"); id != "" {
				labels[id] = true
			}
		case "script":
			typeValue := strings.ToLower(attrValue(node, "type"))
			if attrValue(node, "src") == "" && typeValue != "application/ld+json" && strings.TrimSpace(nodeText(node)) != "" {
				t.Errorf("%s contains an executable inline script", path)
			}
		case "style":
			t.Errorf("%s contains a style element", path)
		case "img":
			if _, exists := attr(node, "alt"); !exists {
				t.Errorf("%s contains an image without alt", path)
			}
		case "table":
			if !hasDescendant(node, "thead") {
				t.Errorf("%s contains a table without thead", path)
			}
			if !hasAncestorClass(node, "table-wrapper") {
				t.Errorf("%s contains a table outside .table-wrapper", path)
			}
		case "input", "select", "textarea":
			if node.Data == "input" && strings.EqualFold(attrValue(node, "type"), "hidden") {
				break
			}
			controls = append(controls, node)
		}
		for _, attribute := range node.Attr {
			name := strings.ToLower(attribute.Key)
			if name == "style" {
				t.Errorf("%s contains an inline style attribute", path)
			}
			if strings.HasPrefix(name, "on") {
				t.Errorf("%s contains executable inline handler %q", path, name)
			}
		}
		if id := attrValue(node, "id"); id != "" {
			ids[id] = true
		}
	}
	for _, control := range controls {
		id := attrValue(control, "id")
		if attrValue(control, "aria-label") == "" && attrValue(control, "aria-labelledby") == "" &&
			(id == "" || !labels[id] && !hasAncestor(control, "label")) {
			t.Errorf("%s contains an unlabeled %s control (id=%q)", path, control.Data, id)
		}
	}
	if mainCount != 1 || h1Count != 1 {
		t.Errorf("%s has %d main landmarks and %d h1 headings; want exactly one each", path, mainCount, h1Count)
	}
	if mainNode == nil || attrValue(mainNode, "id") != "main-content" {
		t.Errorf("%s main landmark does not have id=main-content", path)
	}
	if headerNode == nil {
		t.Errorf("%s has no shared header", path)
	} else {
		assertHeaderElements(t, path, headerNode)
	}
	assertFirstFocusableSkipLinks(t, path, doc)
	for _, node := range nodes {
		if node.Type != htmlparser.ElementNode {
			continue
		}
		if node.Data == "form" && strings.EqualFold(attrValue(node, "method"), http.MethodPost) && !hasHiddenField(node, "csrf_token") {
			t.Errorf("%s has a POST form without csrf_token", path)
		}
		if node.Data != "a" {
			continue
		}
		href := strings.TrimSpace(attrValue(node, "href"))
		if href == "" {
			continue
		}
		linkURL, err := url.Parse(href)
		if err != nil {
			t.Errorf("%s has invalid link %q: %v", path, href, err)
			continue
		}
		if linkURL.IsAbs() && linkURL.Host != "example.com" || linkURL.Host != "" && linkURL.Host != "example.com" {
			continue
		}
		if linkURL.Path == "" {
			if linkURL.Fragment != "" && !ids[linkURL.Fragment] {
				t.Errorf("%s has broken fragment link %q", path, href)
			}
			continue
		}
		if linkURL.Fragment != "" && linkURL.Path == path && !ids[linkURL.Fragment] {
			t.Errorf("%s has broken fragment link %q", path, href)
		}
		if resolveLink != nil {
			if status := resolveLink(href); status < 200 || status >= 400 {
				t.Errorf("%s links to unresolved internal path %q (HTTP %d)", path, href, status)
			}
		}
	}
	assertHubBreadcrumb(t, path, nodes)
}

func assertFirstFocusableSkipLinks(t *testing.T, path string, doc *htmlparser.Node) {
	t.Helper()
	focusable := make([]*htmlparser.Node, 0, 4)
	walkHTML(doc, func(node *htmlparser.Node) {
		if node.Type != htmlparser.ElementNode {
			return
		}
		if node.Data == "a" && attrValue(node, "href") != "" || node.Data == "button" || node.Data == "select" || node.Data == "textarea" {
			focusable = append(focusable, node)
			return
		}
		if node.Data == "input" && !strings.EqualFold(attrValue(node, "type"), "hidden") {
			focusable = append(focusable, node)
		}
	})
	want := []string{"#main-content", "#navigation"}
	if len(focusable) < len(want) {
		t.Errorf("%s has only %d focusable elements", path, len(focusable))
		return
	}
	for index, href := range want {
		if focusable[index].Data != "a" || attrValue(focusable[index], "href") != href {
			t.Errorf("%s focusable element %d is not the required skip link %q", path, index+1, href)
		}
	}
}

func assertHeaderElements(t *testing.T, path string, header *htmlparser.Node) {
	t.Helper()
	children := elementChildren(header)
	if len(children) != 4 && len(children) != 5 {
		t.Errorf("%s header has %d direct elements, want 4 built-in elements and an optional operator fragment", path, len(children))
		return
	}
	want := []struct{ tag, class string }{{"a", "nav-brand"}, {"nav", "nav-links"}, {"label", "nav-toggle"}, {"div", "header-actions"}}
	for index, expected := range want {
		if children[index].Data != expected.tag || !hasClass(children[index], expected.class) {
			t.Errorf("%s header element %d = <%s class=%q>, want <%s class=%q>", path, index+1, children[index].Data, attrValue(children[index], "class"), expected.tag, expected.class)
		}
	}
}

func assertHubBreadcrumb(t *testing.T, path string, nodes []*htmlparser.Node) {
	t.Helper()
	var hub string
	switch {
	case strings.HasPrefix(path, "/server/help/"):
		hub = "/server/help"
	case strings.HasPrefix(path, "/server/preferences/"):
		hub = "/server/preferences"
	case strings.HasPrefix(path, "/server/security/"):
		hub = "/server/security"
	}
	count, found := 0, false
	for _, node := range nodes {
		if node.Type != htmlparser.ElementNode || node.Data != "nav" || !hasClass(node, "breadcrumb") {
			continue
		}
		count++
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == htmlparser.ElementNode && child.Data == "a" {
				link, err := url.Parse(attrValue(child, "href"))
				if err == nil {
					linkPath := strings.TrimRight(link.Path, "/")
					hubPath := strings.TrimRight(hub, "/")
					if linkPath == hubPath || strings.HasSuffix(linkPath, hubPath) {
						found = true
					}
				}
			}
		}
	}
	if hub == "" && count != 0 {
		t.Errorf("%s is a hub or top-level page but has %d breadcrumbs", path, count)
	}
	if hub != "" && (count != 1 || !found) {
		t.Errorf("%s has %d breadcrumbs with hub link present=%t; want one link to %s", path, count, found, hub)
	}
}

func hasHiddenField(node *htmlparser.Node, name string) bool {
	found := false
	walkHTML(node, func(child *htmlparser.Node) {
		if child.Type == htmlparser.ElementNode && child.Data == "input" && strings.EqualFold(attrValue(child, "type"), "hidden") && attrValue(child, "name") == name {
			found = true
		}
	})
	return found
}

func hasDescendant(node *htmlparser.Node, tag string) bool {
	found := false
	walkHTML(node, func(child *htmlparser.Node) {
		if child != node && child.Type == htmlparser.ElementNode && child.Data == tag {
			found = true
		}
	})
	return found
}

func hasAncestorClass(node *htmlparser.Node, class string) bool {
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if hasClass(parent, class) {
			return true
		}
	}
	return false
}

func hasAncestor(node *htmlparser.Node, tag string) bool {
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if parent.Type == htmlparser.ElementNode && parent.Data == tag {
			return true
		}
	}
	return false
}

func hasClass(node *htmlparser.Node, class string) bool {
	for _, value := range strings.Fields(attrValue(node, "class")) {
		if value == class {
			return true
		}
	}
	return false
}

func attr(node *htmlparser.Node, name string) (htmlparser.Attribute, bool) {
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, name) {
			return attribute, true
		}
	}
	return htmlparser.Attribute{}, false
}

func attrValue(node *htmlparser.Node, name string) string {
	attribute, _ := attr(node, name)
	return attribute.Val
}

func elementChildren(node *htmlparser.Node) []*htmlparser.Node {
	children := make([]*htmlparser.Node, 0)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == htmlparser.ElementNode {
			children = append(children, child)
		}
	}
	return children
}

func nodeText(node *htmlparser.Node) string {
	var result strings.Builder
	walkHTML(node, func(child *htmlparser.Node) {
		if child.Type == htmlparser.TextNode {
			result.WriteString(child.Data)
		}
	})
	return result.String()
}

func nodeHTML(node *htmlparser.Node) string {
	var result bytes.Buffer
	if err := htmlparser.Render(&result, node); err != nil {
		return err.Error()
	}
	return result.String()
}

func walkHTML(node *htmlparser.Node, visit func(*htmlparser.Node)) {
	visit(node)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		walkHTML(child, visit)
	}
}
