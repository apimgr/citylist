package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	pathpkg "path"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/citylist/src/common/i18n"
)

const maxResponseBytes = 2 << 20

var clientLog struct {
	sync.RWMutex
	handler func(string, string)
}

// SetLogHandler installs the client-wide structured log sink.
func SetLogHandler(handler func(level, message string)) {
	clientLog.Lock()
	clientLog.handler = handler
	clientLog.Unlock()
}

func logClient(level, message string) {
	clientLog.RLock()
	handler := clientLog.handler
	clientLog.RUnlock()
	if handler != nil {
		handler(level, message)
	}
}

// Client performs bounded read-only requests against a Citylist server.
type Client struct {
	baseURL       *url.URL
	http          *http.Client
	userAgent     string
	token         string
	language      string
	apiVersion    string
	retries       int
	retryDelay    time.Duration
	cacheDir      string
	cacheEnabled  bool
	cacheTTL      time.Duration
	cacheMaxBytes int64
}

// New creates a client for an HTTP or HTTPS Citylist base URL.
func New(base string) (*Client, error) {
	return NewWithLanguage(base, "en")
}

// NewWithLanguage creates a client and localizes URL validation errors.
func NewWithLanguage(base, lang string) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New(i18n.Translate(lang, "cli.error_invalid_server_url"))
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	client := &Client{baseURL: parsed, http: &http.Client{Timeout: 30 * time.Second}, userAgent: "citylist-cli/dev", apiVersion: "v1", retries: 3, retryDelay: time.Second}
	applyDefaultResponseCache(client)
	return client, nil
}

// SetAPIVersion sets the version segment used in API paths.
func (c *Client) SetAPIVersion(version string) error {
	if len(version) == 0 || len(version) > 32 {
		return errors.New("invalid API version")
	}
	for _, character := range version {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '-' {
			return errors.New("invalid API version")
		}
	}
	c.apiVersion = version
	return nil
}

// SetRequestPolicy applies bounded timeout and retry settings from cli.yml.
func (c *Client) SetRequestPolicy(timeout time.Duration, retries int, retryDelay time.Duration) error {
	if timeout <= 0 || retries < 0 || retries > 10 || retryDelay < 0 {
		return errors.New("invalid API request policy")
	}
	c.http.Timeout = timeout
	c.retries = retries
	c.retryDelay = retryDelay
	return nil
}

// SetUserAgent applies the fixed project identity and build version to requests.
func (c *Client) SetUserAgent(userAgent string) {
	if strings.TrimSpace(userAgent) != "" && !strings.ContainsAny(userAgent, "\r\n") {
		c.userAgent = userAgent
	}
}

// SetCredentials configures optional bearer authentication and response language.
func (c *Client) SetCredentials(token, language string) {
	c.token = strings.TrimSpace(token)
	c.language = strings.TrimSpace(language)
}

// Get performs a GET request for a server-relative path and returns a bounded response body.
func (c *Client) Get(ctx context.Context, path string) ([]byte, error) {
	return c.GetWithAccept(ctx, path, "application/json")
}

// GetWithAccept performs a bounded GET request using the requested response format.
func (c *Client) GetWithAccept(ctx context.Context, path, accept string) ([]byte, error) {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\r\n") {
		return nil, errors.New(i18n.Translate(c.language, "cli.error_path_absolute"))
	}
	if c.apiVersion != "" && c.apiVersion != "v1" {
		path = strings.Replace(path, "/api/v1/", "/api/"+c.apiVersion+"/", 1)
	}
	route, err := url.ParseRequestURI(path)
	if err != nil || route.IsAbs() || route.Host != "" {
		return nil, errors.New(i18n.Translate(c.language, "cli.error_path_invalid"))
	}
	endpoint := *c.baseURL
	basePath := strings.TrimRight(endpoint.Path, "/")
	endpoint.Path = pathpkg.Join(basePath, route.Path)
	endpoint.RawPath = pathpkg.Join(basePath, route.EscapedPath())
	endpoint.RawQuery = route.RawQuery
	cacheKey := ""
	if c.cacheEnabled && cacheablePath(route.Path) {
		cacheKey = c.cacheKey(endpoint.String(), accept)
		if body, ok := c.cacheRead(cacheKey); ok {
			logClient("debug", "response cache hit path="+route.Path)
			return body, nil
		}
	}
	for attempt := 0; ; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", i18n.Translate(c.language, "cli.error_create_request"), err)
		}
		request.Header.Set("Accept", "application/json")
		if strings.TrimSpace(accept) != "" {
			request.Header.Set("Accept", accept)
		}
		request.Header.Set("User-Agent", c.userAgent)
		if c.language != "" {
			request.Header.Set("Accept-Language", c.language)
		}
		if c.token != "" {
			request.Header.Set("Authorization", "Bearer "+c.token)
		}
		logClient("debug", "HTTP GET path="+route.Path)
		response, err := c.http.Do(request)
		if err != nil {
			if attempt < c.retries {
				if err := waitRetry(ctx, c.retryDelay); err != nil {
					return nil, err
				}
				continue
			}
			logClient("error", "HTTP GET path="+route.Path+" transport_error")
			return nil, fmt.Errorf("%s: %w", i18n.Translate(c.language, "cli.error_request_detail"), err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
		_ = response.Body.Close()
		if readErr != nil {
			logClient("error", "HTTP GET path="+route.Path+" response_read_error")
			return nil, fmt.Errorf("%s: %w", i18n.Translate(c.language, "cli.error_read_response"), readErr)
		}
		if len(body) > maxResponseBytes {
			logClient("error", "HTTP GET path="+route.Path+" response_too_large")
			return nil, errors.New(i18n.TranslateFormat(c.language, "cli.error_response_size", map[string]string{"max": fmt.Sprint(maxResponseBytes)}))
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			logClient("warn", fmt.Sprintf("HTTP GET path=%s status=%d", route.Path, response.StatusCode))
			if (response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError) && attempt < c.retries {
				if err := waitRetry(ctx, c.retryDelay); err != nil {
					return nil, err
				}
				continue
			}
			message := strings.TrimSpace(string(body))
			if len(message) > 512 {
				message = message[:512]
			}
			return nil, errors.New(i18n.TranslateFormat(c.language, "cli.error_status", map[string]string{"status": response.Status, "message": message}))
		}
		logClient("debug", fmt.Sprintf("HTTP GET path=%s status=%d", route.Path, response.StatusCode))
		if cacheKey != "" && response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices && !strings.Contains(strings.ToLower(response.Header.Get("Cache-Control")), "no-store") {
			c.cacheWrite(cacheKey, body)
		}
		return body, nil
	}
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Endpoint constructs a URL-encoded read endpoint for the supported CLI commands.
func Endpoint(command string, args []string) (string, error) {
	return EndpointWithLanguage(command, args, "en")
}

// EndpointWithLanguage builds a read-only route and localizes validation errors.
func EndpointWithLanguage(command string, args []string, lang string) (string, error) {
	if len(args) == 0 && command != "cities" && command != "stats" && command != "random" {
		return "", errors.New(i18n.TranslateFormat(lang, "cli.endpoint_arguments", map[string]string{"command": command}))
	}
	query := url.Values{}
	switch command {
	case "city":
		return "/api/v1/cities/" + url.PathEscape(args[0]), nil
	case "zipcode":
		return "/api/v1/zipcodes/" + url.PathEscape(args[0]), nil
	case "search":
		query.Set("q", args[0])
		if len(args) > 1 {
			query.Set("country", args[1])
		}
		return "/api/v1/cities/search?" + query.Encode(), nil
	case "zip-search":
		query.Set("q", args[0])
		if len(args) > 1 {
			query.Set("state", args[1])
		}
		return "/api/v1/zipcodes/search?" + query.Encode(), nil
	case "country":
		return "/api/v1/cities/country/" + url.PathEscape(args[0]), nil
	case "cities":
		if len(args) > 0 {
			query.Set("page", args[0])
		}
		if len(args) > 1 {
			query.Set("limit", args[1])
		}
		return "/api/v1/cities?" + query.Encode(), nil
	case "stats":
		return "/api/v1/stats", nil
	case "random":
		return "/api/v1/random", nil
	case "nearest":
		if len(args) < 2 {
			return "", errors.New(i18n.Translate(lang, "cli.endpoint_nearest"))
		}
		query.Set("latitude", args[0])
		query.Set("longitude", args[1])
		return "/api/v1/cities/coordinates?" + query.Encode(), nil
	case "nearby":
		if len(args) < 3 {
			return "", errors.New(i18n.Translate(lang, "cli.endpoint_nearby"))
		}
		query.Set("lat", args[0])
		query.Set("lon", args[1])
		query.Set("radius", args[2])
		return "/api/v1/cities/nearby?" + query.Encode(), nil
	case "zip-state":
		query.Set("page", "1")
		if len(args) > 1 {
			query.Set("limit", args[1])
		}
		return "/api/v1/zipcodes/state/" + url.PathEscape(args[0]) + "?" + query.Encode(), nil
	case "zip-nearby":
		if len(args) < 3 {
			return "", errors.New(i18n.Translate(lang, "cli.endpoint_zip_nearby"))
		}
		query.Set("lat", args[0])
		query.Set("lon", args[1])
		query.Set("radius_km", args[2])
		return "/api/v1/zipcodes/nearby?" + query.Encode(), nil
	default:
		return "", errors.New(i18n.TranslateFormat(lang, "cli.endpoint_unknown", map[string]string{"command": command}))
	}
}
