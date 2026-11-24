package server

import (
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/server/metrics"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type requestSizeReader struct {
	io.ReadCloser
	read int64
}

func (reader *requestSizeReader) Read(buffer []byte) (int, error) {
	n, err := reader.ReadCloser.Read(buffer)
	reader.read += int64(n)
	return n, err
}

func (s *Server) metricsMiddleware(next http.Handler) http.Handler {
	if !s.cfg.Server.Metrics.Enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if metricsExcludedPath(r.URL.Path, s.cfg.Server.Metrics.Root.Enabled) {
			next.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		reader := &requestSizeReader{ReadCloser: r.Body}
		r.Body = reader
		metrics.HTTPActiveRequests.Inc()
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		defer func() {
			metrics.HTTPActiveRequests.Dec()
			requestSize := reader.read
			if r.ContentLength >= 0 && r.ContentLength > requestSize {
				requestSize = r.ContentLength
			}
			method := normalizedHTTPMethod(r.Method)
			path := normalizeMetricPath(r)
			metrics.HTTPRequestSize.WithLabelValues(method, path).Observe(float64(requestSize))
			metrics.HTTPResponseSize.WithLabelValues(method, path).Observe(float64(wrapped.BytesWritten()))
			status := wrapped.Status()
			if status == 0 {
				status = http.StatusOK
			}
			metrics.HTTPRequestsTotal.WithLabelValues(method, path, strconv.Itoa(status)).Inc()
			metrics.HTTPRequestDuration.WithLabelValues(method, path).Observe(time.Since(started).Seconds())
		}()
		next.ServeHTTP(wrapped, r)
	})
}

var (
	metricUUIDPattern = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	metricNumericID   = regexp.MustCompile(`(/[0-9]+)(/|$)`)
	metricRouteParam  = regexp.MustCompile(`\{[^/]+\}`)
)

func normalizedHTTPMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodOptions, http.MethodHead:
		return method
	default:
		return "OTHER"
	}
}

func metricsExcludedPath(path string, rootAliasEnabled bool) bool {
	if rootAliasEnabled && (path == "/metrics" || strings.HasPrefix(path, "/metrics/")) {
		return true
	}
	return path == "/server/metrics" || strings.HasPrefix(path, "/server/metrics/") || path == "/api/metrics" || strings.HasPrefix(path, "/api/metrics/") || strings.HasSuffix(path, "/server/metrics") || strings.Contains(path, "/server/metrics/")
}

func normalizeMetricPath(r *http.Request) string {
	if routeContext := chi.RouteContext(r.Context()); routeContext != nil {
		if pattern := routeContext.RoutePattern(); pattern != "" {
			pattern = metricRouteParam.ReplaceAllString(pattern, ":id")
			return strings.TrimSuffix(pattern, "/")
		}
	}
	path := metricUUIDPattern.ReplaceAllString(r.URL.Path, ":id")
	return metricNumericID.ReplaceAllString(path, "/:id$2")
}
