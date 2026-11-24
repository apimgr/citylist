package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/citylist/src/config"
)

func TestScheduledAnnouncementRendersAndCanBeDismissedWithoutJavaScript(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Web.Announcements.Messages = []config.Announcement{{
		ID: "maintenance-window", Type: "warning", Title: "Maintenance", Message: "Service maintenance is scheduled.",
		Start: time.Now().UTC().Add(-time.Hour).Format(time.RFC3339), End: time.Now().UTC().Add(time.Hour).Format(time.RFC3339), Dismissible: true,
	}}
	s := testServerWithConfig(t, cfg)
	page := doRequest(t, s, http.MethodGet, "/", nil)
	body := page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(body, `data-announcement-id="maintenance-window"`) {
		t.Fatalf("announcement not rendered: status=%d", page.Code)
	}
	if strings.Index(body, `data-announcement-id="maintenance-window"`) > strings.Index(body, "<main") {
		t.Fatal("announcement banner must render before main content")
	}
	csrf := findCookie(page.Result().Cookies(), csrfCookieName)
	if csrf == nil {
		t.Fatal("page did not issue a CSRF cookie")
	}
	values := url.Values{"csrf_token": {csrf.Value}, "id": {"maintenance-window"}, "return_to": {"/"}}
	request := httptest.NewRequest(http.MethodPost, "/announcements/dismiss", strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(csrf)
	response := httptest.NewRecorder()
	s.Router().ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/" {
		t.Fatalf("dismiss response = %d, location %q", response.Code, response.Header().Get("Location"))
	}
	dismissed := findCookie(response.Result().Cookies(), dismissedAnnouncementsCookie)
	if dismissed == nil || dismissed.Value != "maintenance-window" {
		t.Fatalf("dismissal cookie = %#v", dismissed)
	}
	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(dismissed)
	response = httptest.NewRecorder()
	s.Router().ServeHTTP(response, request)
	if strings.Contains(response.Body.String(), `data-announcement-id="maintenance-window"`) {
		t.Fatal("dismissed announcement was rendered again")
	}
}

func TestAnnouncementWindowAndDismissalValidation(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct {
		name string
		item config.Announcement
		want bool
	}{
		{name: "no bounds", item: config.Announcement{}, want: true},
		{name: "future start", item: config.Announcement{Start: now.Add(time.Minute).Format(time.RFC3339)}, want: false},
		{name: "past end", item: config.Announcement{End: now.Add(-time.Minute).Format(time.RFC3339)}, want: false},
		{name: "invalid start", item: config.Announcement{Start: "invalid"}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := announcementIsActive(test.item, now); got != test.want {
				t.Fatalf("announcementIsActive() = %t, want %t", got, test.want)
			}
		})
	}
	if announcementIDPattern.MatchString("../outside") || !announcementIDPattern.MatchString("notice-2026.01") {
		t.Fatal("announcement ID validation accepted an unsafe ID or rejected a valid ID")
	}
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}
