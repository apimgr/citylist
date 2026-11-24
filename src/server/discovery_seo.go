package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"net/http"
	"strings"
	"time"
)

type sitemapDocument struct {
	XMLName xml.Name       `xml:"urlset"`
	XMLNS   string         `xml:"xmlns,attr"`
	URLs    []sitemapEntry `xml:"url"`
}

type sitemapEntry struct {
	Location   string `xml:"loc"`
	ChangeFreq string `xml:"changefreq"`
	Priority   string `xml:"priority"`
}

type atomFeed struct {
	XMLName xml.Name   `xml:"feed"`
	XMLNS   string     `xml:"xmlns,attr"`
	ID      string     `xml:"id"`
	Title   string     `xml:"title"`
	Updated string     `xml:"updated"`
	Links   []atomLink `xml:"link"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr,omitempty"`
	Href string `xml:"href,attr"`
}

type rssDocument struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title string `xml:"title"`
	Link  string `xml:"link"`
	Desc  string `xml:"description"`
}

func (s *Server) handleSitemap(w http.ResponseWriter, r *http.Request) {
	document := sitemapDocument{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9"}
	for _, page := range serverPageRegistry {
		if !page.Sitemap || !s.robotsAllowsPath(page.Path) {
			continue
		}
		document.URLs = append(document.URLs, sitemapEntry{
			Location: s.BuildURL(r, page.Path), ChangeFreq: page.ChangeFreq, Priority: page.Priority,
		})
	}
	body, err := xml.Marshal(document)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "")
		return
	}
	s.respondDiscoveryXML(w, r, append([]byte(xml.Header), body...), "application/xml; charset=utf-8")
}

func (s *Server) handleAtomFeed(w http.ResponseWriter, r *http.Request) {
	base := s.BuildURL(r, "/")
	updated := s.startedAt.UTC().Format(time.RFC3339)
	document := atomFeed{
		XMLNS: "http://www.w3.org/2005/Atom", ID: base,
		Title: s.brandTitle(), Updated: updated,
		Links: []atomLink{{Href: base}, {Rel: "self", Href: s.BuildURL(r, "/feed.xml")}},
	}
	body, err := xml.Marshal(document)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "")
		return
	}
	s.respondDiscoveryXML(w, r, append([]byte(xml.Header), body...), "application/atom+xml; charset=utf-8")
}

func (s *Server) handleRSSFeed(w http.ResponseWriter, r *http.Request) {
	document := rssDocument{
		Version: "2.0",
		Channel: rssChannel{
			Title: s.brandTitle(), Link: s.BuildURL(r, "/"),
			Desc: s.cfg.Server.Branding.Description,
		},
	}
	body, err := xml.Marshal(document)
	if err != nil {
		s.renderError(w, r, http.StatusInternalServerError, "")
		return
	}
	s.respondDiscoveryXML(w, r, append([]byte(xml.Header), body...), "application/rss+xml; charset=utf-8")
}

func (s *Server) respondDiscoveryXML(w http.ResponseWriter, r *http.Request, body []byte, contentType string) {
	digest := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(digest[:]) + `"`
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("ETag", etag)
	modified := s.startedAt.UTC().Truncate(time.Second)
	w.Header().Set("Last-Modified", modified.Format(http.TimeFormat))
	if match := r.Header.Get("If-None-Match"); match != "" {
		if match == "*" || match == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	if r.Header.Get("If-None-Match") == "" {
		if modifiedSince := r.Header.Get("If-Modified-Since"); modifiedSince != "" {
			if parsed, err := time.Parse(http.TimeFormat, modifiedSince); err == nil && !modified.After(parsed) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func (s *Server) robotsAllowsPath(requestPath string) bool {
	allowed := true
	bestLength := -1
	for _, pattern := range s.cfg.Web.Robots.Allow {
		if strings.HasPrefix(requestPath, pattern) && len(pattern) > bestLength {
			allowed = true
			bestLength = len(pattern)
		}
	}
	for _, pattern := range s.cfg.Web.Robots.Deny {
		if strings.HasPrefix(requestPath, pattern) && len(pattern) >= bestLength {
			allowed = false
			bestLength = len(pattern)
		}
	}
	return allowed
}
