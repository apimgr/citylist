package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"sort"
	"strings"

	htmlparser "golang.org/x/net/html"
)

type capturedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newCapturedResponse() *capturedResponse {
	return &capturedResponse{header: make(http.Header)}
}

func (r *capturedResponse) Header() http.Header {
	return r.header
}

func (r *capturedResponse) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *capturedResponse) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(body)
}

func (s *Server) contentNegotiationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isMCPRoute(r.URL.Path) || strings.HasPrefix(r.URL.Path, "/server/tor/") {
			next.ServeHTTP(w, r)
			return
		}
		apiRoute := strings.HasPrefix(r.URL.Path, "/api/")
		accept := r.Header.Get("Accept")
		textExtension := apiRoute && strings.HasSuffix(r.URL.Path, ".txt") && !isExplicitAPITextRoute(r.URL.Path)
		varyByUserAgent := strings.TrimSpace(accept) == "" || strings.TrimSpace(accept) == "*/*"
		if isOurCLIRequest(r) && !textExtension {
			request := r.Clone(r.Context())
			request.Header = r.Header.Clone()
			request.Header.Set("Accept", "application/json")
			w.Header().Add("Vary", "Accept")
			w.Header().Add("Vary", "User-Agent")
			next.ServeHTTP(w, request)
			return
		}
		if !shouldRenderText(r, apiRoute, textExtension, accept) {
			w.Header().Add("Vary", "Accept")
			if varyByUserAgent {
				w.Header().Add("Vary", "User-Agent")
			}
			next.ServeHTTP(w, r)
			return
		}
		if textExtension || !acceptsPlainText(accept) {
			request := r.Clone(r.Context())
			request.Header = r.Header.Clone()
			url := *r.URL
			if textExtension {
				url.Path = strings.TrimSuffix(url.Path, ".txt")
			}
			request.URL = &url
			request.Header.Set("Accept", "text/plain")
			r = request
		}

		captured := newCapturedResponse()
		next.ServeHTTP(captured, r)
		contentType := captured.header.Get("Content-Type")
		body := captured.body.Bytes()
		switch {
		case strings.HasPrefix(contentType, "text/html"):
			body = htmlDocumentToPlainText(body)
		case strings.HasPrefix(contentType, "application/json"):
			body = jsonDocumentToPlainText(body)
		default:
			copyHeaders(w.Header(), captured.header)
			w.Header().Add("Vary", "Accept")
			if varyByUserAgent {
				w.Header().Add("Vary", "User-Agent")
			}
			w.WriteHeader(captured.statusOrOK())
			if r.Method != http.MethodHead {
				_, _ = w.Write(body)
			}
			return
		}

		copyHeaders(w.Header(), captured.header)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Del("Content-Length")
		w.Header().Add("Vary", "Accept")
		if varyByUserAgent {
			w.Header().Add("Vary", "User-Agent")
		}
		w.WriteHeader(captured.statusOrOK())
		if r.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
	})
}

func shouldRenderText(r *http.Request, apiRoute, textExtension bool, accept string) bool {
	if textExtension {
		return true
	}
	if isOurCLIRequest(r) {
		return false
	}
	if acceptsPlainText(accept) {
		if apiRoute && acceptsMediaType(accept, "application/json") {
			return false
		}
		if !apiRoute && (acceptsMediaType(accept, "application/json") || acceptsMediaType(accept, "text/html")) {
			return false
		}
		return true
	}
	if strings.TrimSpace(accept) != "" && strings.TrimSpace(accept) != "*/*" {
		if apiRoute && isTextBrowserRequest(r) && !acceptsMediaType(accept, "application/json") {
			return true
		}
		return false
	}
	if apiRoute {
		return isTextBrowserRequest(r)
	}
	return isHTTPToolRequest(r)
}

func acceptsMediaType(header, wanted string) bool {
	for _, value := range strings.Split(header, ",") {
		mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(value))
		if err != nil || mediaType != wanted {
			continue
		}
		if quality, exists := parameters["q"]; exists {
			var q float64
			if _, err := fmt.Sscanf(quality, "%f", &q); err != nil || q <= 0 {
				continue
			}
		}
		return true
	}
	return false
}

func isOurCLIRequest(r *http.Request) bool {
	ua := strings.ToLower(r.UserAgent())
	return strings.HasPrefix(ua, "citylist-cli/")
}

func isTextBrowserRequest(r *http.Request) bool {
	ua := strings.ToLower(r.UserAgent())
	for _, marker := range []string{"lynx/", "w3m/", "links ", "links/", "elinks/", "browsh/", "carbonyl/", "netsurf"} {
		if strings.Contains(ua, marker) {
			return true
		}
	}
	return false
}

func isHTTPToolRequest(r *http.Request) bool {
	ua := strings.ToLower(r.UserAgent())
	for _, marker := range []string{
		"curl/", "wget/", "httpie/", "xh/", "hurl/", "libcurl/", "python-requests/", "python-urllib/", "python-httpx/", "aiohttp/", "urllib3/",
		"go-http-client/", "go-resty/", "fasthttp", "reqwest/", "ureq/", "hyper/", "axios/", "node-fetch/", "undici", "superagent/", "deno/", "bun/",
		"okhttp/", "apache-httpclient/", "java/", "guzzlehttp/", "libwww-perl/", "lwp-trivial/", "faraday", "dart/", "windowspowershell/", "postmanruntime/", "insomnia/", "bruno-runtime/", "k6/",
	} {
		if strings.Contains(ua, marker) {
			return true
		}
	}
	return ua == ""
}

func isMCPRoute(path string) bool {
	return path == "/mcp" || path == "/api/mcp" || strings.HasSuffix(path, "/server/mcp")
}

func isExplicitAPITextRoute(path string) bool {
	switch path {
	case "/api/v1/stats.txt", "/api/v1/count.txt", "/api/v1/random.txt":
		return true
	default:
		return false
	}
}

func (r *capturedResponse) statusOrOK() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}

func copyHeaders(destination, source http.Header) {
	for key, values := range source {
		destination[key] = append([]string(nil), values...)
	}
}

func acceptsPlainText(header string) bool {
	for _, value := range strings.Split(header, ",") {
		mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(value))
		if err != nil || mediaType != "text/plain" {
			continue
		}
		if quality, exists := parameters["q"]; exists {
			var q float64
			if _, err := fmt.Sscanf(quality, "%f", &q); err != nil || q <= 0 {
				continue
			}
		}
		return true
	}
	return false
}

func jsonDocumentToPlainText(body []byte) []byte {
	var value interface{}
	if err := json.Unmarshal(body, &value); err != nil {
		return body
	}
	var output strings.Builder
	writePlainValue(&output, value, "")
	return []byte(strings.TrimRight(output.String(), "\n") + "\n")
}

func writePlainValue(output *strings.Builder, value interface{}, label string) {
	switch typed := value.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			writePlainValue(output, typed[key], key)
		}
	case []interface{}:
		for index, item := range typed {
			writePlainValue(output, item, fmt.Sprintf("%s %d", label, index+1))
		}
	default:
		if label != "" {
			output.WriteString(label)
			output.WriteString(": ")
		}
		if typed != nil {
			_, _ = fmt.Fprint(output, typed)
		}
		output.WriteByte('\n')
	}
}

func htmlDocumentToPlainText(body []byte) []byte {
	document, err := htmlparser.Parse(bytes.NewReader(body))
	if err != nil {
		return body
	}
	var output strings.Builder
	appendHTMLText(&output, document, 0)
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	clean := lines[:0]
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			clean = append(clean, wrapPlainText(line, 80)...)
		}
	}
	return []byte(strings.Join(clean, "\n") + "\n")
}

func appendHTMLText(output *strings.Builder, node *htmlparser.Node, listDepth int) {
	if node.Type == htmlparser.ElementNode && isSkippedTextElement(node.Data) {
		return
	}
	if node.Type == htmlparser.ElementNode {
		switch node.Data {
		case "a":
			var label strings.Builder
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				appendHTMLText(&label, child, listDepth)
			}
			text := strings.TrimSpace(label.String())
			href := ""
			for _, attribute := range node.Attr {
				if attribute.Key == "href" && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(attribute.Val)), "javascript:") {
					href = strings.TrimSpace(attribute.Val)
					break
				}
			}
			if text != "" {
				output.WriteString(text)
				if href != "" && href != text {
					output.WriteString(" [")
					output.WriteString(href)
					output.WriteByte(']')
				}
			}
			return
		case "ul", "ol":
			appendHTMLList(output, node, listDepth, node.Data == "ol")
			return
		case "table":
			appendHTMLTable(output, node, listDepth)
			return
		case "h1", "h2", "h3":
			prefix := map[string]string{"h1": "═══ ", "h2": "─── ", "h3": "► "}[node.Data]
			appendTextNewline(output)
			output.WriteString(prefix)
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				appendHTMLText(output, child, listDepth)
			}
			output.WriteByte('\n')
			return
		case "br", "hr":
			appendTextNewline(output)
			return
		}
	}
	block := node.Type == htmlparser.ElementNode && isTextBlockElement(node.Data)
	if block && output.Len() > 0 {
		appendTextNewline(output)
	}
	if node.Type == htmlparser.TextNode {
		text := strings.Join(strings.Fields(node.Data), " ")
		if text != "" {
			if output.Len() > 0 && !builderEndsWith(output, '\n') && !builderEndsWith(output, ' ') {
				output.WriteByte(' ')
			}
			output.WriteString(text)
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		appendHTMLText(output, child, listDepth)
	}
	if block && output.Len() > 0 && !builderEndsWith(output, '\n') {
		output.WriteByte('\n')
	}
}

func isSkippedTextElement(element string) bool {
	switch element {
	case "script", "style", "noscript", "form", "button", "input", "select", "textarea", "option":
		return true
	default:
		return false
	}
}

func appendHTMLList(output *strings.Builder, list *htmlparser.Node, depth int, ordered bool) {
	index := 0
	for child := list.FirstChild; child != nil; child = child.NextSibling {
		if child.Type != htmlparser.ElementNode || child.Data != "li" {
			continue
		}
		appendTextNewline(output)
		output.WriteString(strings.Repeat("  ", depth))
		if ordered {
			index++
			fmt.Fprintf(output, "%d. ", index)
		} else {
			output.WriteString("• ")
		}
		for item := child.FirstChild; item != nil; item = item.NextSibling {
			if item.Type == htmlparser.ElementNode && (item.Data == "ul" || item.Data == "ol") {
				appendHTMLList(output, item, depth+1, item.Data == "ol")
				continue
			}
			appendHTMLText(output, item, depth+1)
		}
		appendTextNewline(output)
	}
}

func appendHTMLTable(output *strings.Builder, table *htmlparser.Node, depth int) {
	var rows [][]string
	var collect func(*htmlparser.Node)
	collect = func(node *htmlparser.Node) {
		if node.Type == htmlparser.ElementNode && node.Data == "tr" {
			var cells []string
			for cell := node.FirstChild; cell != nil; cell = cell.NextSibling {
				if cell.Type != htmlparser.ElementNode || (cell.Data != "td" && cell.Data != "th") {
					continue
				}
				var value strings.Builder
				for part := cell.FirstChild; part != nil; part = part.NextSibling {
					appendHTMLText(&value, part, depth)
				}
				cells = append(cells, strings.Join(strings.Fields(value.String()), " "))
			}
			if len(cells) > 0 {
				rows = append(rows, cells)
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			collect(child)
		}
	}
	collect(table)
	if len(rows) == 0 {
		return
	}
	appendTextNewline(output)
	for rowIndex, row := range rows {
		if rowIndex > 0 {
			output.WriteByte('\n')
		}
		output.WriteString(strings.Repeat("  ", depth))
		output.WriteString(strings.Join(row, " │ "))
		if rowIndex == 0 {
			output.WriteByte('\n')
			output.WriteString(strings.Repeat("  ", depth))
			output.WriteString(strings.Repeat("─", min(80, max(1, len(strings.Join(row, " │ "))))))
		}
	}
	output.WriteByte('\n')
}

func appendTextNewline(output *strings.Builder) {
	if output.Len() > 0 && !builderEndsWith(output, '\n') {
		output.WriteByte('\n')
	}
}

func wrapPlainText(line string, width int) []string {
	if len(line) <= width {
		return []string{line}
	}
	words := strings.Fields(line)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	current := words[0]
	for _, word := range words[1:] {
		if len(current)+1+len(word) > width {
			lines = append(lines, current)
			current = word
			continue
		}
		current += " " + word
	}
	return append(lines, current)
}

func builderEndsWith(output *strings.Builder, suffix byte) bool {
	value := output.String()
	return len(value) > 0 && value[len(value)-1] == suffix
}

func isTextBlockElement(element string) bool {
	switch element {
	case "address", "article", "blockquote", "br", "dd", "div", "dl", "dt", "fieldset", "figcaption", "figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6", "header", "hr", "li", "main", "nav", "ol", "p", "pre", "section", "table", "tbody", "td", "th", "tr", "ul":
		return true
	default:
		return false
	}
}
