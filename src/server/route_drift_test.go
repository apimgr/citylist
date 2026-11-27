package server

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/apimgr/citylist/src/mode"
	"github.com/go-chi/chi/v5"
)

type routeInventory struct {
	routes   map[string]struct{}
	prefixes map[string]struct{}
}

var (
	markdownPathPattern = regexp.MustCompile("`(/[^`]+)`")
	pathParameter       = regexp.MustCompile(`\{[^/}]+\}`)
	optionalPathPart    = regexp.MustCompile(`\[(/[^\]]+)\]`)
)

func TestRouteInventoryDrift(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate route drift test source")
	}
	projectRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../.."))
	aiContent, err := os.ReadFile(filepath.Join(projectRoot, "AI.md"))
	if err != nil {
		t.Fatalf("read AI.md route inventory: %v", err)
	}
	ideaContent, err := os.ReadFile(filepath.Join(projectRoot, "IDEA.md"))
	if err != nil {
		t.Fatalf("read IDEA.md route inventory: %v", err)
	}
	templateInventory := parseRouteInventory(string(aiContent), true)
	ideaInventory, found := parseIDEARouteInventory(string(ideaContent))
	if !found {
		t.Fatal("IDEA.md is missing its ### API routes inventory")
	}
	contract := mergeRouteInventories(templateInventory, ideaInventory)
	if len(contract.routes) == 0 {
		t.Fatal("AI.md and IDEA.md route inventories contain no methoded routes")
	}
	for _, page := range serverPageRegistry {
		key := routeKey(http.MethodGet, page.Path)
		if !routeSetContains(contract.routes, key) {
			t.Errorf("registered page %s is missing from the AI.md/IDEA.md route inventory", page.Path)
		}
	}

	srv := testServer(t)
	registered := routeInventory{routes: make(map[string]struct{}), prefixes: make(map[string]struct{})}
	if err := chi.Walk(srv.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method == "ALL" {
			registered.prefixes[normalizeRoutePath(route)] = struct{}{}
			return nil
		}
		registered.routes[routeKey(method, route)] = struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("walk router: %v", err)
	}
	for _, page := range serverPageRegistry {
		key := routeKey(http.MethodGet, page.Path)
		if !routeSetContains(registered.routes, key) {
			t.Errorf("registered page %s is missing its GET router route", page.Path)
		}
	}
	for prefix := range contract.prefixes {
		count := 0
		for key := range registered.routes {
			_, route := splitRouteKey(key)
			if strings.HasPrefix(route, strings.TrimSuffix(prefix, "*")) {
				count++
				matched := false
				for expected := range contract.routes {
					if routeKeyMatches(expected, key) {
						matched = true
						break
					}
				}
				if !matched {
					t.Errorf("%s is registered under prefix %s but missing from the explicit route inventory", key, prefix)
				}
			}
		}
		if count == 0 {
			t.Errorf("route prefix %s is declared in AI.md but has no registered routes", prefix)
		}
	}

	for key := range contract.routes {
		method, route := splitRouteKey(key)
		if isOptionalUnconfiguredRoute(route, srv) {
			continue
		}
		if !routeSetContains(registered.routes, key) {
			t.Errorf("%s %s is in the AI.md/IDEA.md inventory but missing from the router", method, route)
		}
	}
	for key := range registered.routes {
		method, route := splitRouteKey(key)
		if isUntrackedSupportingRoute(route) {
			continue
		}
		if !routeSetContains(contract.routes, key) {
			t.Errorf("%s %s is registered in the router but missing from AI.md and IDEA.md inventories", method, route)
		}
	}

	openAPI := openAPIPaths(srv.router, "/api/v1", "en")
	openAPIRoutes := make(map[string]struct{})
	for route, rawOperations := range openAPI {
		operations, ok := rawOperations.(map[string]interface{})
		if !ok {
			t.Errorf("OpenAPI route %s has invalid operation data %T", route, rawOperations)
			continue
		}
		for method := range operations {
			openAPIRoutes[routeKey(strings.ToUpper(method), route)] = struct{}{}
		}
	}
	for key := range contract.routes {
		method, route := splitRouteKey(key)
		if !isAPIRoute(route) || isInternalRoute(route) || isOptionalUnconfiguredRoute(route, srv) {
			continue
		}
		if !routeSetContains(openAPIRoutes, key) {
			t.Errorf("%s %s is an API route in the AI.md/IDEA.md inventory but missing from OpenAPI", method, route)
		}
	}
	for key := range openAPIRoutes {
		method, route := splitRouteKey(key)
		if isInternalRoute(route) {
			t.Errorf("internal route %s %s must not appear in OpenAPI", method, route)
			continue
		}
		if !isAPIRoute(route) {
			t.Errorf("page route %s %s must not appear in OpenAPI", method, route)
			continue
		}
		if !routeSetContains(contract.routes, key) {
			t.Errorf("OpenAPI route %s %s is missing from AI.md and IDEA.md inventories", method, route)
		}
	}
}

func routeSetContains(routes map[string]struct{}, key string) bool {
	if _, exists := routes[key]; exists {
		return true
	}
	for candidate := range routes {
		if routeKeyMatches(candidate, key) {
			return true
		}
	}
	return false
}

func parseIDEARouteInventory(document string) (routeInventory, bool) {
	start := strings.Index(document, "### API routes")
	if start < 0 {
		return routeInventory{}, false
	}
	remaining := document[start+len("### API routes"):]
	if next := strings.Index(remaining, "\n### "); next >= 0 {
		remaining = remaining[:next]
	}
	return parseRouteInventory(remaining, false), true
}

func parseRouteInventory(document string, template bool) routeInventory {
	inventory := routeInventory{routes: make(map[string]struct{}), prefixes: make(map[string]struct{})}
	lines := strings.Split(document, "\n")
	inCodeBlock := false
	for index := 0; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if strings.HasPrefix(line, "```") {
			inCodeBlock = !inCodeBlock
			continue
		}
		if inCodeBlock || !strings.HasPrefix(line, "|") {
			continue
		}
		cells := markdownTableCells(line)
		if len(cells) == 0 {
			continue
		}
		methodColumn, pathColumn := -1, -1
		for column, cell := range cells {
			header := strings.ToLower(strings.NewReplacer("*", "", "`", "", "_", " ").Replace(strings.TrimSpace(cell)))
			switch header {
			case "method", "methods":
				methodColumn = column
			case "path", "endpoint", "route":
				pathColumn = column
			}
		}
		if methodColumn < 0 || pathColumn < 0 || index+1 >= len(lines) || !isMarkdownTableDivider(lines[index+1]) {
			continue
		}
		if introducedAsIllustration(lines, index) {
			for index++; index < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[index]), "|"); index++ {
			}
			index--
			continue
		}
		index += 2
		for ; index < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[index]), "|"); index++ {
			row := markdownTableCells(strings.TrimSpace(lines[index]))
			if len(row) <= methodColumn || len(row) <= pathColumn {
				continue
			}
			methods, validMethods := parseHTTPMethods(row[methodColumn])
			if !validMethods {
				continue
			}
			paths, validPaths := inventoryPaths(row[pathColumn], template)
			if !validPaths {
				continue
			}
			for _, sourcePath := range paths {
				for _, path := range expandInventoryPath(sourcePath) {
					for _, method := range methods {
						if method == "ALL" && strings.HasSuffix(path, "/*") {
							inventory.prefixes[path] = struct{}{}
							continue
						}
						if method == "ALL" {
							continue
						}
						inventory.routes[routeKey(method, path)] = struct{}{}
					}
				}
			}
		}
		index--
	}
	return inventory
}

func markdownTableCells(line string) []string {
	line = strings.TrimSpace(strings.Trim(line, "|"))
	cells := strings.Split(line, "|")
	for index := range cells {
		cells[index] = strings.TrimSpace(cells[index])
	}
	return cells
}

func isMarkdownTableDivider(line string) bool {
	trimmed := strings.TrimSpace(strings.Trim(line, "|"))
	return trimmed != "" && strings.Trim(trimmed, "-|: ") == ""
}

func introducedAsIllustration(lines []string, tableStart int) bool {
	for previous := tableStart - 1; previous >= 0 && previous >= tableStart-20; previous-- {
		line := strings.ToLower(strings.TrimSpace(lines[previous]))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			return false
		}
		if strings.HasPrefix(line, "|") {
			continue
		}
		return strings.Contains(line, "illustrative example") || strings.Contains(line, "illustrative table") ||
			strings.Contains(line, "example table") || strings.Contains(line, "following example") ||
			strings.Contains(line, "example route") || strings.Contains(line, "for example")
	}
	return false
}

func inventoryPaths(cell string, template bool) ([]string, bool) {
	cell = strings.TrimSpace(cell)
	if template {
		match := markdownPathPattern.FindStringSubmatch(cell)
		if len(match) != 2 || "`"+match[1]+"`" != cell || !strings.HasPrefix(match[1], "/") {
			return nil, false
		}
		if strings.Contains(match[1], "{resource}") {
			return nil, false
		}
		return []string{match[1]}, true
	}
	entries := strings.Split(cell, ",")
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if strings.HasPrefix(entry, "`") && strings.HasSuffix(entry, "`") {
			entry = strings.TrimSuffix(strings.TrimPrefix(entry, "`"), "`")
		}
		if !strings.HasPrefix(entry, "/") || strings.ContainsAny(entry, " `\t") {
			return nil, false
		}
		paths = append(paths, entry)
	}
	return paths, true
}

func parseHTTPMethods(cell string) ([]string, bool) {
	cell = strings.ToUpper(strings.TrimSpace(cell))
	if cell == "ALL" {
		return []string{"ALL"}, true
	}
	methods := strings.FieldsFunc(cell, func(r rune) bool { return r == '/' || r == ',' })
	if len(methods) == 0 {
		return nil, false
	}
	for index, method := range methods {
		method = strings.TrimSpace(method)
		switch method {
		case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
			methods[index] = method
		default:
			return nil, false
		}
	}
	return methods, true
}

func expandInventoryPath(path string) []string {
	path = strings.ReplaceAll(path, "{api_version}", "v1")
	path = strings.ReplaceAll(path, "{admin_path}", "admin")
	path = strings.ReplaceAll(path, "{project_name}", "citylist")
	if strings.HasPrefix(path, "/api/") {
		path = strings.TrimSuffix(path, ".txt")
	}
	match := optionalPathPart.FindStringSubmatch(path)
	if len(match) > 0 {
		withoutOptional := strings.Replace(path, match[0], "", 1)
		withOptional := strings.Replace(path, match[0], match[1], 1)
		return append(expandInventoryPath(withoutOptional), expandInventoryPath(withOptional)...)
	}
	if strings.Contains(path, "{service}") && strings.Contains(path, "metrics") {
		var expanded []string
		for _, service := range []string{"prometheus", "grafana", "loki"} {
			expanded = append(expanded, normalizeRoutePath(strings.ReplaceAll(path, "{service}", service)))
		}
		return expanded
	}
	return []string{normalizeRoutePath(path)}
}

func normalizeRoutePath(path string) string {
	if strings.HasPrefix(path, "/api/") {
		path = strings.TrimSuffix(path, ".txt")
	}
	path = pathParameter.ReplaceAllString(path, "{param}")
	return path
}

func routeKey(method, path string) string {
	return strings.ToUpper(method) + " " + normalizeRoutePath(path)
}

func routeKeyMatches(pattern, registered string) bool {
	patternMethod, patternPath := splitRouteKey(pattern)
	registeredMethod, registeredPath := splitRouteKey(registered)
	if patternMethod != registeredMethod {
		return false
	}
	patternSegments := strings.Split(strings.Trim(patternPath, "/"), "/")
	registeredSegments := strings.Split(strings.Trim(registeredPath, "/"), "/")
	wildcard := len(patternSegments) > 0 && patternSegments[len(patternSegments)-1] == "*"
	if wildcard {
		patternSegments = patternSegments[:len(patternSegments)-1]
		if len(registeredSegments) < len(patternSegments) {
			return false
		}
	} else if len(patternSegments) != len(registeredSegments) {
		return false
	}
	for index, segment := range patternSegments {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			continue
		}
		if segment != registeredSegments[index] {
			return false
		}
	}
	return true
}

func splitRouteKey(key string) (string, string) {
	method, route, _ := strings.Cut(key, " ")
	return method, route
}

func mergeRouteInventories(inventories ...routeInventory) routeInventory {
	merged := routeInventory{routes: make(map[string]struct{}), prefixes: make(map[string]struct{})}
	for _, inventory := range inventories {
		for key := range inventory.routes {
			merged.routes[key] = struct{}{}
		}
		for prefix := range inventory.prefixes {
			merged.prefixes[prefix] = struct{}{}
		}
	}
	return merged
}

func isAPIRoute(path string) bool {
	return strings.HasPrefix(path, "/api/") || path == "/mcp"
}

func isInternalRoute(path string) bool {
	return strings.Contains(path, "/metrics") || strings.HasPrefix(path, "/debug/") || strings.Contains(path, "/server/tor/")
}

func isOptionalUnconfiguredRoute(path string, srv *Server) bool {
	return path == "/healthz" && !srv.cfg.Server.Healthz.Root.Enabled ||
		strings.HasPrefix(path, "/debug/") && !mode.IsDebugEnabled()
}

func isUntrackedSupportingRoute(path string) bool {
	return path == "/favicon.ico" || path == "/sitemap.xml" || path == "/feed.xml" || path == "/rss.xml" ||
		path == "/.well-known/pgp-key.asc" || path == "/server/tracking.js" ||
		strings.HasPrefix(path, "/branding/") || path == "/announcements/dismiss"
}
