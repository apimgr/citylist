package server

import (
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"strings"

	"github.com/apimgr/citylist/src/mode"
	"golang.org/x/net/publicsuffix"
)

var devOnlyTLDs = map[string]struct{}{
	"local": {}, "test": {}, "example": {}, "invalid": {}, "localhost": {}, "lan": {},
	"internal": {}, "home": {}, "localdomain": {}, "home.arpa": {}, "intranet": {},
	"corp": {}, "private": {},
}

// GetFQDN resolves the configured domain for startup and background tasks.
func GetFQDN(projectName string) string {
	if domains := getAllDomains(projectName); len(domains) > 0 {
		return domains[0]
	}
	if hostname, err := os.Hostname(); err == nil && IsValidHost(hostname, mode.IsDevelopment(), projectName) && !isOverlayHost(hostname) && !isLoopback(hostname) {
		return hostname
	}
	if hostname := strings.TrimSpace(os.Getenv("HOSTNAME")); hostname != "" && IsValidHost(hostname, mode.IsDevelopment(), projectName) && !isOverlayHost(hostname) {
		return hostname
	}
	if address := getGlobalIPv6(); address != "" {
		return address
	}
	if address := getGlobalIPv4(); address != "" {
		return address
	}
	return ""
}

// GetAllDomains returns the comma-separated DOMAIN values in configured order.
func GetAllDomains() []string {
	return getAllDomains("citylist")
}

func getAllDomains(projectName string) []string {
	parts := strings.Split(os.Getenv("DOMAIN"), ",")
	domains := make([]string, 0, len(parts))
	for _, part := range parts {
		if domain := strings.TrimSpace(part); IsValidHost(domain, mode.IsDevelopment(), projectName) && !isOverlayHost(domain) {
			domains = append(domains, domain)
		}
	}
	return domains
}

// WarnInvalidEnvironmentHosts records one startup warning for invalid host settings.
func WarnInvalidEnvironmentHosts(projectName string) {
	development := mode.IsDevelopment()
	invalidDomains := make([]string, 0)
	for _, value := range strings.Split(os.Getenv("DOMAIN"), ",") {
		value = strings.TrimSpace(value)
		if value != "" && (!IsValidHost(value, development, projectName) || isOverlayHost(value)) {
			invalidDomains = append(invalidDomains, value)
		}
	}
	hostname := strings.TrimSpace(os.Getenv("HOSTNAME"))
	invalidHostname := hostname != "" && (!IsValidHost(hostname, development, projectName) || isOverlayHost(hostname))
	if len(invalidDomains) > 0 || invalidHostname {
		fields := []any{}
		if len(invalidDomains) > 0 {
			fields = append(fields, "DOMAIN", strings.Join(invalidDomains, ","))
		}
		if invalidHostname {
			fields = append(fields, "HOSTNAME", hostname)
		}
		slog.Warn("invalid host environment values skipped", fields...)
	}
}

// IsValidHost validates a hostname for the active application mode.
func IsValidHost(host string, devMode bool, projectName string) bool {
	lower := strings.ToLower(strings.TrimSpace(host))
	if lower == "" {
		return false
	}
	if _, err := netip.ParseAddr(lower); err == nil {
		return false
	}
	if lower == "localhost" {
		return devMode
	}
	if !strings.Contains(lower, ".") {
		return false
	}
	if isOverlayHost(lower) {
		return true
	}
	if projectName != "" && strings.HasSuffix(lower, "."+strings.ToLower(projectName)) {
		return devMode
	}
	suffix, icann := publicsuffix.PublicSuffix(lower)
	if _, devOnly := devOnlyTLDs[suffix]; devOnly {
		return devMode
	}
	if !devMode && !icann {
		return false
	}
	etldPlusOne, err := publicsuffix.EffectiveTLDPlusOne(lower)
	return err == nil && etldPlusOne != ""
}

func isOverlayHost(host string) bool {
	lower := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	return strings.HasSuffix(lower, ".onion") || strings.HasSuffix(lower, ".i2p") || strings.HasSuffix(lower, ".exit")
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
