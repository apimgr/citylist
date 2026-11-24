package server

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type requestPeerKey struct{}

var validHostname = regexp.MustCompile(`(?i)^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$`)

func parseTrustedProxyPrefixes(values []string) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if prefix, err := netip.ParsePrefix(value); err == nil {
			prefixes = append(prefixes, prefix.Masked())
			continue
		}
		if address, ok := normalizeAddress(value); ok {
			prefixes = append(prefixes, netip.PrefixFrom(address, address.BitLen()))
		}
	}
	return prefixes
}

func normalizeAddress(value string) (netip.Addr, bool) {
	value = strings.TrimSpace(strings.Trim(value, `"`))
	if value == "" || strings.EqualFold(value, "unknown") || strings.HasPrefix(value, "_") {
		return netip.Addr{}, false
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	} else if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
	}
	if zone := strings.LastIndexByte(value, '%'); zone >= 0 {
		value = value[:zone]
	}
	address, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}

func trustedAddress(address netip.Addr, additional []netip.Prefix) bool {
	if !address.IsValid() {
		return false
	}
	if address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsUnspecified() {
		return true
	}
	for _, prefix := range additional {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func (s *Server) realIPMiddleware(next http.Handler) http.Handler {
	return realIPMiddlewareWithPrefixes(s.trustedProxySnapshot(), s.cfg.Server.TrustedProxies.ClientIPHeader, next)
}

func realIPMiddleware(next http.Handler) http.Handler {
	return realIPMiddlewareWithPrefixes(nil, "auto", next)
}

func realIPMiddlewareWithPrefixes(prefixes []netip.Prefix, pinnedHeader string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originalPeer := r.RemoteAddr
		r = r.WithContext(context.WithValue(r.Context(), requestPeerKey{}, originalPeer))
		if peer, ok := normalizeAddress(originalPeer); ok {
			client := peer
			if trustedAddress(peer, prefixes) {
				if resolved, ok := resolveForwardedAddress(r, peer, prefixes, pinnedHeader); ok {
					client = resolved
				}
			}
			r.RemoteAddr = client.String()
		}
		next.ServeHTTP(w, r)
	})
}

func resolveForwardedAddress(r *http.Request, peer netip.Addr, prefixes []netip.Prefix, pinnedHeader string) (netip.Addr, bool) {
	var name string
	if pinnedHeader != "" && !strings.EqualFold(pinnedHeader, "auto") {
		name = http.CanonicalHeaderKey(pinnedHeader)
	} else {
		for _, candidate := range []string{"X-Forwarded-For", "Forwarded", "CF-Connecting-IP", "True-Client-IP", "X-Real-IP", "X-Client-IP"} {
			if len(r.Header.Values(candidate)) > 0 {
				name = candidate
				break
			}
		}
	}
	if name == "" {
		return peer, false
	}
	hops := forwardedHops(r.Header.Values(name), name)
	if len(hops) == 0 {
		return peer, false
	}
	if len(hops) > 32 {
		hops = hops[len(hops)-32:]
	}
	nearest := peer
	for index := len(hops) - 1; index >= 0; index-- {
		address, ok := normalizeAddress(hops[index])
		if !ok {
			return nearest, true
		}
		nearest = address
		if !trustedAddress(address, prefixes) {
			return address, true
		}
	}
	return nearest, true
}

func forwardedHops(values []string, header string) []string {
	joined := strings.Join(values, ",")
	if strings.EqualFold(header, "Forwarded") {
		parts := strings.Split(joined, ",")
		hops := make([]string, 0, len(parts))
		for _, part := range parts {
			for _, parameter := range strings.Split(part, ";") {
				key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
				if ok && strings.EqualFold(key, "for") {
					hops = append(hops, strings.TrimSpace(value))
					break
				}
			}
		}
		return hops
	}
	if !strings.EqualFold(header, "X-Forwarded-For") {
		return []string{joined}
	}
	parts := strings.Split(joined, ",")
	hops := make([]string, 0, len(parts))
	for _, part := range parts {
		hops = append(hops, strings.TrimSpace(part))
	}
	return hops
}

func originalPeer(r *http.Request) string {
	if value, ok := r.Context().Value(requestPeerKey{}).(string); ok && value != "" {
		return value
	}
	return r.RemoteAddr
}

func (s *Server) BuildURL(r *http.Request, requestPath string) string {
	proto := "http"
	trusted := false
	protoResolved := false
	if peer, ok := normalizeAddress(originalPeer(r)); ok {
		trusted = trustedAddress(peer, s.trustedProxySnapshot())
	}
	if trusted {
		for _, header := range []string{"X-Forwarded-Proto", "X-Forwarded-Ssl", "X-Url-Scheme"} {
			value := firstHeaderValue(r.Header.Values(header))
			if strings.EqualFold(header, "X-Forwarded-Ssl") {
				if strings.EqualFold(value, "on") {
					proto = "https"
					protoResolved = true
					break
				}
				continue
			}
			if strings.EqualFold(value, "http") || strings.EqualFold(value, "https") {
				proto = strings.ToLower(value)
				protoResolved = true
				break
			}
		}
	}
	if r.TLS != nil && !protoResolved {
		proto = "https"
	}

	domains := configuredDomains()
	host := ""
	forwardedHost := false
	if trusted {
		for _, header := range []string{"X-Forwarded-Host", "X-Real-Host", "X-Original-Host"} {
			if candidate := firstHeaderValue(r.Header.Values(header)); candidate != "" {
				if validHostPort(candidate) {
					host = candidate
					forwardedHost = true
					break
				}
			}
		}
	}
	if host == "" && validHostPort(r.Host) && (len(domains) == 0 || allowedDomain(hostnameOnly(r.Host), domains)) {
		host = r.Host
	}
	if host == "" && len(domains) > 0 {
		host = domains[0]
	}
	if host == "" {
		host = s.cfg.Server.FQDN
	}
	if host == "" {
		host, _ = os.Hostname()
	}
	if host == "" {
		host = os.Getenv("HOSTNAME")
	}
	if host == "" {
		host = "localhost"
	}

	if trusted {
		if port := firstHeaderValue(r.Header.Values("X-Forwarded-Port")); validPort(port) {
			host = net.JoinHostPort(hostnameOnly(host), port)
		}
	}
	if _, _, err := net.SplitHostPort(host); err != nil {
		port := ""
		if !forwardedHost {
			port = s.port
		}
		if port == "" || port == "80" && proto == "http" || port == "443" && proto == "https" {
			port = ""
		}
		if port != "" {
			host = net.JoinHostPort(host, port)
		}
	}
	if _, _, err := net.SplitHostPort(host); err != nil {
		if address, parseErr := netip.ParseAddr(host); parseErr == nil && address.Is6() {
			host = "[" + host + "]"
		}
	}
	pathValue := requestPath
	if pathValue == "" {
		pathValue = "/"
	}
	if !strings.HasPrefix(pathValue, "/") {
		pathValue = "/" + pathValue
	}
	if s.baseURL != "/" {
		pathValue = strings.TrimSuffix(s.baseURL, "/") + pathValue
	}
	return (&url.URL{Scheme: proto, Host: host, Path: pathValue}).String()
}

func (s *Server) trustedProxySnapshot() []netip.Prefix {
	s.refreshTrustedProxies(false)
	s.trustedProxyMu.RLock()
	defer s.trustedProxyMu.RUnlock()
	return append([]netip.Prefix(nil), s.trustedPrefixes...)
}

func (s *Server) refreshTrustedProxies(force bool) {
	if !force {
		s.trustedProxyMu.RLock()
		fresh := time.Since(s.trustedProxyLastRefresh) < 5*time.Minute
		s.trustedProxyMu.RUnlock()
		if fresh {
			return
		}
	}
	s.trustedProxyRefreshMu.Lock()
	defer s.trustedProxyRefreshMu.Unlock()
	s.trustedProxyMu.RLock()
	if !force && time.Since(s.trustedProxyLastRefresh) < 5*time.Minute {
		s.trustedProxyMu.RUnlock()
		return
	}
	previous := append([]netip.Prefix(nil), s.trustedPrefixes...)
	s.trustedProxyMu.RUnlock()

	values := s.cfg.Server.TrustedProxies.Additional
	prefixes := parseTrustedProxyPrefixes(values)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	lookupFailed := false
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || strings.Contains(value, "/") {
			continue
		}
		if address, err := netip.ParseAddr(value); err == nil {
			_ = address
			continue
		}
		if !validHostname.MatchString(strings.TrimSuffix(value, ".")) {
			continue
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", value)
		if err != nil {
			lookupFailed = true
			continue
		}
		for _, address := range addresses {
			address = address.Unmap()
			prefixes = append(prefixes, netip.PrefixFrom(address, address.BitLen()))
		}
	}
	if lookupFailed {
		prefixes = append(prefixes, previous...)
	}
	s.trustedProxyMu.Lock()
	s.trustedPrefixes = prefixes
	s.trustedProxyLastRefresh = time.Now()
	s.trustedProxyMu.Unlock()
}

func (s *Server) requestUsesHTTPS(r *http.Request) bool {
	return strings.HasPrefix(s.BuildURL(r, "/"), "https://")
}

func configuredDomains() []string {
	parts := strings.Split(os.Getenv("DOMAIN"), ",")
	domains := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if validHostPort(part) {
			domains = append(domains, part)
		}
	}
	return domains
}

func allowedDomain(host string, domains []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, domain := range domains {
		base := strings.ToLower(strings.TrimSuffix(hostnameOnly(domain), "."))
		if host == base || strings.HasSuffix(base, "."+host) || strings.HasSuffix(host, "."+base) {
			return true
		}
	}
	return false
}

func validHostPort(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "@/%?#\\\r\n\t ") {
		return false
	}
	host := hostnameOnly(value)
	if address, err := netip.ParseAddr(host); err == nil {
		return address.IsValid()
	}
	return validHostname.MatchString(strings.TrimSuffix(host, "."))
}

func hostnameOnly(value string) string {
	if host, _, err := net.SplitHostPort(value); err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(value, "[]")
}

func validPort(value string) bool {
	port, err := strconv.Atoi(value)
	return err == nil && port > 0 && port <= 65535
}

func firstHeaderValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.Split(values[0], ",")[0])
}
