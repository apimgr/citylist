package server

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/apimgr/citylist/src/config"
)

func TestResolveForwardedAddressWalksProxyChain(t *testing.T) {
	prefixes := parseTrustedProxyPrefixes([]string{"198.51.100.0/24"})
	tests := []struct {
		name, peer, xff string
		want            string
	}{
		{name: "proxy appends real address", peer: "10.0.0.1:8080", xff: "1.2.3.4, 203.0.113.9", want: "203.0.113.9"},
		{name: "trusted public proxy", peer: "10.0.0.1:8080", xff: "203.0.113.9, 198.51.100.7", want: "203.0.113.9"},
		{name: "invalid nearest hop falls back to peer", peer: "10.0.0.1:8080", xff: "203.0.113.9, unknown", want: "10.0.0.1"},
		{name: "mapped peer normalizes", peer: "[::ffff:10.0.0.1]:8080", xff: "2001:db8::5, 2001:db8::6", want: "2001:db8::6"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = test.peer
			req.Header.Set("X-Forwarded-For", test.xff)
			peer, ok := normalizeAddress(test.peer)
			if !ok {
				t.Fatal("test peer did not parse")
			}
			got, _ := resolveForwardedAddress(req, peer, prefixes, "auto")
			if got.String() != test.want {
				t.Fatalf("resolved address = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRealIPMiddlewarePreservesOriginalPeer(t *testing.T) {
	prefixes := parseTrustedProxyPrefixes([]string{"10.0.0.0/8"})
	handler := realIPMiddlewareWithPrefixes(prefixes, "auto", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := originalPeer(r); got != "10.0.0.1:8080" {
			t.Errorf("original peer = %q, want TCP peer", got)
		}
		_, _ = w.Write([]byte(r.RemoteAddr))
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:8080"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if got := response.Body.String(); got != "203.0.113.9" {
		t.Fatalf("resolved RemoteAddr = %q, want 203.0.113.9", got)
	}
}

func TestBuildURLUsesTrustedProxyHeadersAndSafeHost(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.TrustedProxies.Additional = []string{"10.0.0.0/8"}
	s := testServerWithConfig(t, cfg)
	request := httptest.NewRequest(http.MethodGet, "http://backend.local/", nil)
	request.RemoteAddr = "10.0.0.1:8080"
	request.Header.Set("X-Forwarded-Host", "api.example.com")
	request.Header.Set("X-Forwarded-Proto", "https")
	got := s.BuildURL(request, "/server/preferences/sync/import")
	if got != "https://api.example.com/server/preferences/sync/import" {
		t.Fatalf("BuildURL() = %q", got)
	}

	request.RemoteAddr = "203.0.113.8:8080"
	request.Header.Set("X-Forwarded-Host", "attacker.example")
	got = s.BuildURL(request, "/")
	if got != "http://backend.local:8080/" {
		t.Fatalf("untrusted forwarded host was accepted: %q", got)
	}
}

func TestNormalizeAddressAndTrustedCIDR(t *testing.T) {
	address, ok := normalizeAddress("[2001:DB8::1%eth0]:443")
	if !ok || address.String() != "2001:db8::1" {
		t.Fatalf("normalized address = %q, %t", address, ok)
	}
	if !trustedAddress(netip.MustParseAddr("198.51.100.7"), parseTrustedProxyPrefixes([]string{"198.51.100.0/24"})) {
		t.Fatal("configured trusted proxy CIDR did not match")
	}
}
