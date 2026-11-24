package server

import (
	"net/netip"
	"testing"
)

func TestConfiguredDomainsAndWildcard(t *testing.T) {
	t.Setenv("DOMAIN", " api.example.co.uk, www.example.co.uk ,,api.other.test ")
	if got := GetFQDN("citylist"); got != "api.example.co.uk" {
		t.Fatalf("GetFQDN() = %q, want first DOMAIN value", got)
	}
	domains := GetAllDomains()
	if len(domains) != 3 || domains[0] != "api.example.co.uk" || domains[1] != "www.example.co.uk" || domains[2] != "api.other.test" {
		t.Fatalf("GetAllDomains() = %v", domains)
	}
	if got := GetWildcardDomain(); got != "" {
		t.Fatalf("GetWildcardDomain() = %q, want empty for unrelated registrable domains", got)
	}
	t.Setenv("DOMAIN", "example.co.uk, api.example.co.uk")
	if got := GetWildcardDomain(); got != "*.example.co.uk" {
		t.Fatalf("GetWildcardDomain() = %q, want *.example.co.uk", got)
	}
}

func TestDomainDisplayRules(t *testing.T) {
	for _, host := range []string{"citylist", "api.citylist", "api.example.local", "localhost", "example.test"} {
		if !isDevTLD(host, "citylist") {
			t.Errorf("isDevTLD(%q) = false", host)
		}
	}
	for _, host := range []string{"example.com", "api.example.co.uk", "192.0.2.5"} {
		if isDevTLD(host, "citylist") {
			t.Errorf("isDevTLD(%q) = true", host)
		}
	}
	tests := []struct {
		host string
		port int
		tls  bool
		want string
	}{
		{host: "example.test", port: 80, want: "http://example.test"},
		{host: "example.test", port: 443, want: "https://example.test"},
		{host: "api.example.com", port: 8080, tls: true, want: "https://api.example.com:8080"},
		{host: "2001:db8::1", port: 8443, tls: true, want: "https://[2001:db8::1]:8443"},
	}
	for _, test := range tests {
		host := test.host
		if address, err := netip.ParseAddr(host); err == nil && address.Is6() {
			host = "[" + host + "]"
		}
		if got := formatURL(host, test.port, test.tls); got != test.want {
			t.Errorf("formatURL(%q, %d, %t) = %q, want %q", test.host, test.port, test.tls, got, test.want)
		}
	}
}
