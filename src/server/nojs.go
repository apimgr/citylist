package server

import (
	"bytes"
	"net/http"
	"regexp"
	"strings"

	htmlparser "golang.org/x/net/html"
)

var noJSNoscriptPattern = regexp.MustCompile(`(?is)<noscript\b[^>]*>(.*?)</noscript\s*>`)

// renderNoJSHTML creates the text-browser page variant from server-rendered HTML.
func renderNoJSHTML(source []byte) []byte {
	source = noJSNoscriptPattern.ReplaceAll(source, []byte("$1"))
	document, err := htmlparser.Parse(bytes.NewReader(source))
	if err != nil {
		return source
	}
	var transform func(*htmlparser.Node)
	transform = func(node *htmlparser.Node) {
		for child := node.FirstChild; child != nil; {
			next := child.NextSibling
			switch {
			case child.Type == htmlparser.ElementNode && (child.Data == "script" || child.Data == "style"):
				node.RemoveChild(child)
			case child.Type == htmlparser.ElementNode && child.Data == "button" && strings.EqualFold(noJSAttribute(child, "type"), "button"):
				node.RemoveChild(child)
			default:
				for index := 0; index < len(child.Attr); {
					if strings.HasPrefix(strings.ToLower(child.Attr[index].Key), "on") {
						child.Attr = append(child.Attr[:index], child.Attr[index+1:]...)
						continue
					}
					index++
				}
				transform(child)
			}
			child = next
		}
	}
	transform(document)
	var output bytes.Buffer
	if err := htmlparser.Render(&output, document); err != nil {
		return source
	}
	return output.Bytes()
}

func noJSAttribute(node *htmlparser.Node, name string) string {
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, name) {
			return attribute.Val
		}
	}
	return ""
}

func writeHTMLResponse(w http.ResponseWriter, r *http.Request, status int, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r != nil && isTextBrowserRequest(r) {
		body = renderNoJSHTML(body)
		w.Header().Add("Vary", "User-Agent")
	}
	w.Header().Del("Content-Length")
	if status != 0 {
		w.WriteHeader(status)
	}
	if r == nil || r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
