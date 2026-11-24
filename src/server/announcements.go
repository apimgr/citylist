package server

import (
	"html/template"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/common/i18n"
	"github.com/apimgr/citylist/src/config"
)

const dismissedAnnouncementsCookie = "dismissed_announcements"

var announcementIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

type announcementView struct {
	ID          string
	Type        string
	Title       string
	Message     string
	Dismissible bool
}

var announcementTemplate = template.Must(template.New("announcement").Parse(`<div class="site-banner site-banner-{{.Type}}" role="{{if or (eq .Type "warning") (eq .Type "error")}}alert{{else}}status{{end}}" data-announcement-id="{{.ID}}">
  {{if .Title}}<strong class="site-banner-title">{{.Title}}</strong>{{end}}
  <span class="site-banner-text">{{.Message}}</span>
  {{if .Dismissible}}<form method="post" action="{{.Action}}" class="site-banner-dismiss"><input type="hidden" name="csrf_token" value="{{.CSRFToken}}"><input type="hidden" name="id" value="{{.ID}}"><input type="hidden" name="return_to" value="{{.ReturnTo}}"><button type="submit" class="site-banner-close" aria-label="{{.DismissLabel}}">&times;</button></form>{{end}}
</div>`))

func (s *Server) activeAnnouncements(r *http.Request, csrfToken, returnTo string) template.HTML {
	config := s.cfg.Web.Announcements
	if !config.Enabled || len(config.Messages) == 0 {
		return ""
	}
	dismissed := dismissedAnnouncementIDs(r)
	now := time.Now().UTC()
	var rendered strings.Builder
	for _, item := range config.Messages {
		if !announcementIDPattern.MatchString(item.ID) || item.Message == "" || dismissed[item.ID] || !announcementIsActive(item, now) {
			continue
		}
		kind := item.Type
		switch kind {
		case "warning", "info", "error", "success":
		default:
			kind = "info"
		}
		if r.URL.Path == "/server/preferences" {
			item.Dismissible = false
		}
		view := struct {
			announcementView
			Action       string
			CSRFToken    string
			ReturnTo     string
			DismissLabel string
		}{
			announcementView: announcementView{ID: item.ID, Type: kind, Title: item.Title, Message: item.Message, Dismissible: item.Dismissible},
			Action:           strings.TrimSuffix(s.baseURL, "/") + "/announcements/dismiss", CSRFToken: csrfToken,
			ReturnTo: returnTo, DismissLabel: i18n.Translate(i18n.LangFromRequest(r), "announcements.dismiss"),
		}
		if err := announcementTemplate.Execute(&rendered, view); err == nil {
			rendered.WriteByte('\n')
		}
	}
	return template.HTML(rendered.String())
}

func announcementIsActive(item config.Announcement, now time.Time) bool {
	if item.Start != "" {
		start, err := time.Parse(time.RFC3339, item.Start)
		if err != nil || now.Before(start) {
			return false
		}
	}
	if item.End != "" {
		end, err := time.Parse(time.RFC3339, item.End)
		if err != nil || !now.Before(end) {
			return false
		}
	}
	return true
}

func dismissedAnnouncementIDs(r *http.Request) map[string]bool {
	result := make(map[string]bool)
	cookie, err := r.Cookie(dismissedAnnouncementsCookie)
	if err != nil {
		return result
	}
	for _, id := range strings.Split(cookie.Value, ",") {
		if announcementIDPattern.MatchString(id) {
			result[id] = true
		}
	}
	return result
}

func (s *Server) handleAnnouncementDismiss(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.FormValue("id"))
	if !announcementIDPattern.MatchString(id) {
		s.respondError(w, http.StatusBadRequest, "INVALID_ANNOUNCEMENT", "Announcement id is invalid")
		return
	}
	valid := false
	for _, item := range s.cfg.Web.Announcements.Messages {
		if item.ID == id && item.Dismissible {
			valid = true
			break
		}
	}
	if !s.cfg.Web.Announcements.Enabled || !valid {
		s.respondError(w, http.StatusNotFound, "ANNOUNCEMENT_NOT_FOUND", "Announcement cannot be dismissed")
		return
	}
	dismissed := dismissedAnnouncementIDs(r)
	dismissed[id] = true
	ids := make([]string, 0, len(dismissed))
	for _, item := range s.cfg.Web.Announcements.Messages {
		if dismissed[item.ID] && announcementIDPattern.MatchString(item.ID) {
			ids = append(ids, item.ID)
		}
		if len(ids) == 20 {
			break
		}
	}
	path := strings.TrimSuffix(s.baseURL, "/")
	if path == "" {
		path = "/"
	}
	if !privacySignalOptOut(r) {
		http.SetCookie(w, &http.Cookie{
			Name: dismissedAnnouncementsCookie, Value: strings.Join(ids, ","), Path: path,
			MaxAge: 365 * 24 * 60 * 60, SameSite: http.SameSiteLaxMode, Secure: s.requestUsesHTTPS(r),
		})
	}
	redirect := safePreferenceTarget(r.FormValue("return_to"))
	if redirect == "" {
		redirect = strings.TrimSuffix(s.baseURL, "/") + "/"
		if redirect == "" {
			redirect = "/"
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}
