package server

import (
	"net/http/httptest"
	"testing"
)

func TestCSRFTokenIsStableWithinRequest(t *testing.T) {
	s := testServer(t)
	r := httptest.NewRequest("GET", "/server/preferences/tokens", nil)
	w := httptest.NewRecorder()

	first := s.csrfToken(w, r)
	second := s.csrfToken(w, r)
	if first == "" || first != second {
		t.Fatal("repeated CSRF token requests produced different values")
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != csrfCookieName || cookies[0].Value != first {
		t.Fatal("CSRF response did not set exactly one cookie matching the form token")
	}
}
