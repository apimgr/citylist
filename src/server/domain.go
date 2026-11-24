package server

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"

	"golang.org/x/net/publicsuffix"
)

var devOnlyTLDs = map[string]struct{}{
	"local": {}, "test": {}, "example": {}, "invalid": {}, "localhost": {}, "lan": {},
	"internal": {}, "home": {}, "localdomain": {}, "home.arpa": {}, "intranet": {},
	"corp": {}, "private": {},
}

// GetFQDN resolves the configured domain for startup and background tasks.
func GetFQDN(projectName string) string {
	if domains := GetAllDomains(); len(domains) > 0 {
		return domains[0]
	}
	if hostname, err := os.Hostname(); err == nil && hostname != "" && !isLoopback(hostname) {
		return hostname
	}
	if hostname := strings.TrimSpace(os.Getenv("HOSTNAME")); hostname != "" && !isLoopback(hostname) {
		return hostname
	}
	if address := getGlobalIPv6(); address != "" {
		return address
	}
	if address := getGlobalIPv4(); address != "" {
		return address
	}
	return "localhost"
}

// GetAllDomains returns the comma-separated DOMAIN values in configured order.
func GetAllDomains() []string {
	parts := strings.Split(os.Getenv("DOMAIN"), ",")
	domains := make([]string, 0, len(parts))
	for _, part := range parts {
		if domain := strings.TrimSpace(part); domain != "" {
			domains = append(domains, domain)
		}
	}
	return domains
}

func appendUniqueDomain(domains []string, domain string) []string {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return domains
	}
	for _, existing := range domains {
		if strings.EqualFold(existing, domain) {
			return domains
		}
	}
	return append(domains, domain)
}

// GetWildcardDomain returns a wildcard when every configured name shares a registrable domain.
func GetWildcardDomain() string {
	domains := GetAllDomains()
	if len(domains) < 2 {
		return ""
	}
	base := extractBaseDomain(domains[0])
	for _, domain := range domains[1:] {
		if extractBaseDomain(domain) != base {
			return ""
		}
	}
	return "*." + base
}

func extractBaseDomain(domain string) string {
	if address, err := netip.ParseAddr(domain); err == nil {
		return address.String()
	}
	base, err := publicsuffix.EffectiveTLDPlusOne(domain)
	if err != nil {
		return domain
	}
	return base
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.IsLoopback()
}

func getGlobalIPv6() string {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, address := range addresses {
		if ipNet, ok := address.(*net.IPNet); ok {
			ip := ipNet.IP
			if ip.To4() == nil && ip.IsGlobalUnicast() && !ip.IsPrivate() {
				return ip.String()
			}
		}
	}
	return ""
}

func getGlobalIPv4() string {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, address := range addresses {
		if ipNet, ok := address.(*net.IPNet); ok {
			ip := ipNet.IP
			if ip.To4() != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() {
				return ip.String()
			}
		}
	}
	return ""
}

// GetDisplayURL returns a single canonical URL for a listener.
func GetDisplayURL(projectName string, port int, isHTTPS bool) string {
	fqdn := GetFQDN(projectName)
	if !isDevTLD(fqdn, projectName) && fqdn != "localhost" {
		return formatURL(fqdn, port, isHTTPS)
	}
	if address := getGlobalIPv6(); address != "" {
		return formatURL("["+address+"]", port, isHTTPS)
	}
	if address := getGlobalIPv4(); address != "" {
		return formatURL(address, port, isHTTPS)
	}
	return formatURL(fqdn, port, isHTTPS)
}

func isDevTLD(host, projectName string) bool {
	lower := strings.ToLower(strings.TrimSuffix(host, "."))
	if projectName != "" {
		name := strings.ToLower(projectName)
		if lower == name || strings.HasSuffix(lower, "."+name) {
			return true
		}
	}
	for suffix := range devOnlyTLDs {
		if lower == suffix || strings.HasSuffix(lower, "."+suffix) {
			return true
		}
	}
	return false
}

func formatURL(host string, port int, isHTTPS bool) string {
	protocol := "http"
	if isHTTPS || port == 443 {
		protocol = "https"
	}
	if port == 80 || port == 443 {
		return protocol + "://" + host
	}
	return fmt.Sprintf("%s://%s:%d", protocol, host, port)
}
