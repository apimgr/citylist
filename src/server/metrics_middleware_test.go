package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeMetricPathFallback(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/api/v1/cities/123", "/api/v1/cities/:id"},
		{"/api/v1/cities/123/zipcodes/456", "/api/v1/cities/:id/zipcodes/:id"},
		{"/api/v1/cities/123e4567-e89b-12d3-a456-426614174000", "/api/v1/cities/:id"},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, test.path, nil)
			if got := normalizeMetricPath(req); got != test.want {
				t.Errorf("normalizeMetricPath(%q) = %q, want %q", test.path, got, test.want)
			}
		})
	}
}

func TestMetricsScrapesAreExcludedButHealthRequestsAreCounted(t *testing.T) {
	if !metricsExcludedPath("/metrics/prometheus", true) || !metricsExcludedPath("/api/v1/server/metrics", false) {
		t.Fatal("metrics scrape aliases should be excluded from HTTP metrics")
	}
	if metricsExcludedPath("/metrics", false) {
		t.Fatal("disabled root alias should be counted as a normal HTTP response")
	}
	if metricsExcludedPath("/server/healthz", true) {
		t.Fatal("health probes should remain visible in Prometheus HTTP metrics")
	}
}
