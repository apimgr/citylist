package urlutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	maxRemoteImageSize = 10 << 20
	remoteImageTimeout = 30 * time.Second
)

var remoteImageTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true,
	"image/webp": true, "image/x-icon": true, "image/vnd.microsoft.icon": true,
}

// FetchRemoteImage fetches an operator-configured HTTPS image without allowing
// redirects, proxies, or DNS results to reach private or local network hosts.
func FetchRemoteImage(ctx context.Context, rawURL string) (data []byte, contentType string, retErr error) {
	parsed, err := validateRemoteURL(rawURL)
	if err != nil {
		return nil, "", err
	}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil {
				return nil, fmt.Errorf("resolve image host: %w", err)
			}
			var lastErr error
			for _, ip := range ips {
				if !publicAddress(ip) {
					continue
				}
				conn, err := (&net.Dialer{Timeout: remoteImageTimeout}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, fmt.Errorf("image host resolved only to a private or local address")
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: remoteImageTimeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many image redirects")
		}
		if _, err := validateRemoteURL(req.URL.String()); err != nil {
			return fmt.Errorf("image redirect rejected: %w", err)
		}
		return nil
	}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, "", fmt.Errorf("create image request: %w", err)
	}
	request.Header.Set("User-Agent", "citylist/1.0")
	request.Header.Set("Accept", "image/png, image/jpeg, image/gif, image/webp, image/x-icon")
	response, err := client.Do(request)
	if err != nil {
		return nil, "", fmt.Errorf("fetch image: %w", err)
	}
	defer func() { retErr = errors.Join(retErr, response.Body.Close()) }()
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("image server returned HTTP %d", response.StatusCode)
	}
	parsedContentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !remoteImageTypes[strings.ToLower(parsedContentType)] {
		return nil, "", fmt.Errorf("image content type is not allowed")
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, maxRemoteImageSize+1))
	if err != nil {
		return nil, "", fmt.Errorf("read image response: %w", err)
	}
	if len(data) == 0 || len(data) > maxRemoteImageSize {
		return nil, "", fmt.Errorf("image response exceeds the 10 MiB limit or is empty")
	}
	return data, strings.ToLower(parsedContentType), nil
}

func validateRemoteURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil {
		return nil, fmt.Errorf("remote image URL must be an absolute HTTPS URL without user information")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") {
		return nil, fmt.Errorf("remote image host is local or internal")
	}
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("remote image host could not be resolved")
	}
	for _, ip := range ips {
		if !publicAddress(ip) {
			return nil, fmt.Errorf("remote image host resolves to a private or local address")
		}
	}
	return parsed, nil
}

func publicAddress(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsUnspecified() && !ip.IsMulticast()
}
