package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsTrustedPeer(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want bool
	}{
		{"loopback with port", "127.0.0.1:1234", true},
		{"loopback bare", "127.0.0.1", true},
		{"private 10.x", "10.1.2.3:5555", true},
		{"private 192.168.x", "192.168.1.1:80", true},
		{"link local", "169.254.1.1:80", true},
		{"public ip", "8.8.8.8:80", false},
		{"invalid host", "not-an-ip:80", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTrustedPeer(tt.addr); got != tt.want {
				t.Errorf("isTrustedPeer(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestClientIPFromHeaders(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{"xff single", map[string]string{"X-Forwarded-For": "203.0.113.5"}, "203.0.113.5"},
		{"xff multiple takes first", map[string]string{"X-Forwarded-For": "203.0.113.5, 10.0.0.1"}, "203.0.113.5"},
		{"x-real-ip fallback", map[string]string{"X-Real-IP": "203.0.113.9"}, "203.0.113.9"},
		{"no headers", map[string]string{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			if got := clientIPFromHeaders(req); got != tt.want {
				t.Errorf("clientIPFromHeaders() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRealIPMiddleware(t *testing.T) {
	handler := realIPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(r.RemoteAddr))
	}))

	t.Run("trusted peer honors forwarded header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set("X-Forwarded-For", "203.0.113.7")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if got := rec.Body.String(); got != "203.0.113.7" {
			t.Errorf("RemoteAddr = %q, want 203.0.113.7", got)
		}
	})

	t.Run("untrusted peer ignores forwarded header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "203.0.113.100:1234"
		req.Header.Set("X-Forwarded-For", "198.51.100.1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if got := rec.Body.String(); got != "203.0.113.100:1234" {
			t.Errorf("RemoteAddr = %q, want unchanged 203.0.113.100:1234", got)
		}
	})
}
