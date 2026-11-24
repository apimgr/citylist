package server

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/common/i18n"
	"github.com/apimgr/citylist/src/config"
	"github.com/apimgr/citylist/src/server/metrics"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func (s *Server) registerMetricsRoutes() {
	if !s.cfg.Server.Metrics.Enabled {
		return
	}
	prometheusHandler := s.metricsAuth("prometheus", promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{}))
	grafanaHandler := s.metricsAuth("grafana", http.HandlerFunc(s.handleGrafanaMetrics))
	lokiHandler := s.metricsAuth("loki", http.HandlerFunc(s.handleLokiMetrics))
	mount := func(prefix string) {
		s.router.Method(http.MethodGet, prefix, prometheusHandler)
		s.router.Method(http.MethodGet, prefix+"/prometheus", prometheusHandler)
		s.router.Method(http.MethodGet, prefix+"/grafana", grafanaHandler)
		s.router.Method(http.MethodGet, prefix+"/loki", lokiHandler)
	}
	mount("/server/metrics")
	mount("/api/" + s.cfg.Server.APIVersion + "/server/metrics")
	mount("/api/metrics")
	if s.cfg.Server.Metrics.Root.Enabled {
		mount("/metrics")
	}
}

func (s *Server) metricsAuth(service string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.Server.Metrics.Auth.AllowUnauthenticated {
			token := metricsServiceToken(s.cfg.Server.Metrics.Auth.Tokens, service)
			if token == "" {
				s.respondError(w, http.StatusForbidden, "FORBIDDEN", i18n.Translate(i18n.LangFromRequest(r), "errors.forbidden"))
				return
			}
			provided := strings.TrimSpace(r.Header.Get("Authorization"))
			expected := "Bearer " + token
			if len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
				s.respondError(w, http.StatusUnauthorized, "UNAUTHORIZED", i18n.Translate(i18n.LangFromRequest(r), "errors.unauthorized"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func metricsServiceToken(tokens config.MetricsServiceTokens, service string) string {
	switch service {
	case "prometheus":
		return tokens.Prometheus
	case "grafana":
		return tokens.Grafana
	case "loki":
		return tokens.Loki
	default:
		return ""
	}
}

func (s *Server) handleGrafanaMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(grafanaDashboard)
}

func (s *Server) handleLokiMetrics(w http.ResponseWriter, _ *http.Request) {
	maxAge := parseMetricsAge(s.cfg.Server.Metrics.Loki.MaxAge)
	maxEntries := s.cfg.Server.Metrics.Loki.MaxEntries
	if maxEntries < 1 {
		maxEntries = 1000
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(metrics.DefaultLogStore.LokiStreams(maxEntries, maxAge))
}

func parseMetricsAge(value string) time.Duration {
	maxAge, err := time.ParseDuration(value)
	if err != nil || maxAge <= 0 {
		return time.Hour
	}
	return maxAge
}

var grafanaDashboard = map[string]any{
	"id":            nil,
	"uid":           "citylist-metrics",
	"title":         "Citylist Metrics",
	"schemaVersion": 41,
	"version":       1,
	"refresh":       "30s",
	"time":          map[string]string{"from": "now-1h", "to": "now"},
	"templating":    map[string]any{"list": []any{map[string]any{"name": "datasource", "type": "datasource", "query": "prometheus", "current": map[string]string{"text": "Prometheus", "value": "Prometheus"}}}},
	"panels": []any{
		grafanaPanel(1, "Request rate", "timeseries", `sum(rate(citylist_http_requests_total[5m]))`, "req/s"),
		grafanaPanel(2, "HTTP error rate", "timeseries", `sum(rate(citylist_http_requests_total{status=~"5.."}[5m]))`, "req/s"),
		grafanaPanel(3, "Request latency p95", "timeseries", `histogram_quantile(0.95, sum by (le) (rate(citylist_http_request_duration_seconds_bucket[5m])))`, "s"),
		grafanaPanel(4, "Active requests", "stat", `citylist_http_active_requests`, "short"),
		grafanaPanel(5, "Database connections", "timeseries", `citylist_db_connections_open`, "short"),
		grafanaPanel(6, "Database query errors", "timeseries", `sum(rate(citylist_db_errors_total[5m]))`, "req/s"),
		grafanaPanel(7, "Cache hit rate", "timeseries", `sum(rate(citylist_cache_hits_total[5m])) / clamp_min(sum(rate(citylist_cache_hits_total[5m])) + sum(rate(citylist_cache_misses_total[5m])), 1)`, "percentunit"),
		grafanaPanel(8, "Scheduler failures", "timeseries", `sum by (task) (citylist_scheduler_tasks_total{status="error"})`, "short"),
		grafanaPanel(9, "Memory usage", "gauge", `citylist_system_memory_usage_percent`, "percent"),
		grafanaPanel(10, "Disk usage", "timeseries", `citylist_system_disk_usage_percent`, "percent"),
		grafanaPanel(11, "Go goroutines", "timeseries", `citylist_go_goroutines`, "short"),
		grafanaPanel(12, "City records", "stat", `citylist_cities_total`, "short"),
		grafanaPanel(13, "ZIP code records", "stat", `citylist_zipcodes_total`, "short"),
		grafanaPanel(14, "Uptime", "stat", `citylist_app_uptime_seconds`, "s"),
	},
}

func grafanaPanel(id int, title, panelType, expression, unit string) map[string]any {
	return map[string]any{
		"id": id, "title": title, "type": panelType,
		"datasource":  map[string]string{"type": "prometheus", "uid": "${datasource}"},
		"gridPos":     map[string]int{"h": 8, "w": 12, "x": 0, "y": 0},
		"targets":     []any{map[string]any{"expr": expression, "refId": "A"}},
		"fieldConfig": map[string]any{"defaults": map[string]any{"unit": unit}, "overrides": []any{}},
		"options":     map[string]any{},
	}
}
