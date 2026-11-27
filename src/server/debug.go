package server

import (
	"encoding/json"
	"expvar"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/apimgr/citylist/src/mode"
	"github.com/apimgr/citylist/src/server/metrics"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"gopkg.in/yaml.v3"
)

func (s *Server) registerDebugRoutes(r chi.Router) {
	if !mode.IsDebugEnabled() {
		return
	}
	r.Route("/debug", func(r chi.Router) {
		r.HandleFunc("/pprof", pprof.Index)
		r.HandleFunc("/pprof/cmdline", s.handleDebugCmdline)
		r.HandleFunc("/pprof/profile", pprof.Profile)
		r.HandleFunc("/pprof/symbol", pprof.Symbol)
		r.HandleFunc("/pprof/trace", pprof.Trace)
		for _, profile := range []string{"heap", "goroutine", "allocs", "block", "mutex", "threadcreate"} {
			r.Handle("/pprof/"+profile, pprof.Handler(profile))
		}
		r.Get("/vars", s.handleDebugVars)
		r.Get("/config", s.handleDebugConfig)
		r.Get("/routes", s.handleDebugRoutes)
		r.Get("/cache", s.handleDebugCache)
		r.Get("/db", s.handleDebugDB)
		r.Get("/scheduler", s.handleDebugScheduler)
		r.Get("/memory", s.handleDebugMemory)
		r.Get("/goroutines", s.handleDebugGoroutines)
		r.Get("/error500", s.handleDebugError500)
	})
}

func (s *Server) debugAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode.IsDebugEnabled() && (r.URL.Path == "/debug" || strings.HasPrefix(r.URL.Path, "/debug/")) {
			s.requireOperatorToken(next).ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireOperatorToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.validOperatorBearer(r) {
			s.respondError(w, http.StatusUnauthorized, "UNAUTHORIZED", "An operator Bearer token is required.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleDebugCmdline(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprintln(w, filepath.Base(os.Args[0]))
}

func (s *Server) handleDebugVars(w http.ResponseWriter, _ *http.Request) {
	variables := make(map[string]any)
	expvar.Do(func(value expvar.KeyValue) {
		if value.Key == "cmdline" {
			variables[value.Key] = []string{filepath.Base(os.Args[0])}
			return
		}
		raw := json.RawMessage(value.Value.String())
		if json.Valid(raw) {
			variables[value.Key] = raw
		} else {
			variables[value.Key] = value.Value.String()
		}
	})
	s.respondJSON(w, http.StatusOK, variables)
}

func (s *Server) handleDebugConfig(w http.ResponseWriter, _ *http.Request) {
	s.configMu.RLock()
	serialized, err := yaml.Marshal(s.cfg)
	s.configMu.RUnlock()
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "DEBUG_CONFIG_FAILED", "Configuration could not be read.")
		return
	}
	var value map[string]any
	if err := yaml.Unmarshal(serialized, &value); err != nil {
		s.respondError(w, http.StatusInternalServerError, "DEBUG_CONFIG_FAILED", "Configuration could not be read.")
		return
	}
	s.respondJSON(w, http.StatusOK, redactDebugConfig(value))
}

func redactDebugConfig(value any) any {
	switch current := value.(type) {
	case map[string]any:
		for key, item := range current {
			if debugSensitiveKey(key) {
				current[key] = "********"
			} else {
				current[key] = redactDebugConfig(item)
			}
		}
		return current
	case []any:
		for index, item := range current {
			current[index] = redactDebugConfig(item)
		}
		return current
	default:
		return value
	}
}

func debugSensitiveKey(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), ".", "_"))
	if strings.HasSuffix(key, "_key") {
		return true
	}
	for _, marker := range []string{"token", "password", "secret", "credential", "private_key", "api_key", "access_key", "authorization", "webhook", "dns_credentials", "url", "endpoint"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func (s *Server) handleDebugRoutes(w http.ResponseWriter, _ *http.Request) {
	routes := make([]map[string]string, 0)
	err := chi.Walk(s.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, map[string]string{"method": method, "route": route})
		return nil
	})
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "DEBUG_ROUTES_FAILED", "Routes could not be listed.")
		return
	}
	s.respondJSON(w, http.StatusOK, map[string]any{"count": len(routes), "routes": routes})
}

func (s *Server) handleDebugCache(w http.ResponseWriter, _ *http.Request) {
	stats := map[string]float64{"hits": 0, "misses": 0, "evictions": 0, "items": 0, "bytes": 0}
	families, err := metrics.Registry.Gather()
	if err == nil {
		for _, family := range families {
			name := family.GetName()
			if !strings.HasPrefix(name, "citylist_cache_") {
				continue
			}
			field := strings.TrimPrefix(name, "citylist_cache_")
			for _, sample := range family.GetMetric() {
				if sample.GetCounter() != nil {
					stats[field] += sample.GetCounter().GetValue()
				} else if sample.GetGauge() != nil {
					stats[field] += sample.GetGauge().GetValue()
				}
			}
		}
	}
	s.respondJSON(w, http.StatusOK, map[string]any{"configured_type": s.cfg.Server.Cache.Type, "stats": stats})
}

func (s *Server) handleDebugDB(w http.ResponseWriter, _ *http.Request) {
	open, inUse := s.store.PoolStats()
	s.respondJSON(w, http.StatusOK, map[string]any{"open_connections": open, "in_use": inUse})
}

func (s *Server) handleDebugScheduler(w http.ResponseWriter, _ *http.Request) {
	type taskState struct {
		Name       string `json:"name"`
		Enabled    bool   `json:"enabled"`
		Running    bool   `json:"running"`
		LastRun    string `json:"last_run,omitempty"`
		NextRun    string `json:"next_run,omitempty"`
		LastStatus string `json:"last_status,omitempty"`
		RunCount   uint64 `json:"run_count"`
		FailCount  uint64 `json:"fail_count"`
	}
	tasks := make([]taskState, 0)
	if s.scheduler != nil {
		for _, task := range s.scheduler.GetTasks() {
			state := taskState{Name: task.Name, Enabled: task.Enabled, Running: task.IsRunning(), LastStatus: task.LastStatus, RunCount: task.RunCount, FailCount: task.FailCount}
			if !task.LastRun.IsZero() {
				state.LastRun = task.LastRun.UTC().Format(time.RFC3339)
			}
			if !task.NextRun.IsZero() {
				state.NextRun = task.NextRun.UTC().Format(time.RFC3339)
			}
			tasks = append(tasks, state)
		}
	}
	s.respondJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (s *Server) handleDebugMemory(w http.ResponseWriter, _ *http.Request) {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	s.respondJSON(w, http.StatusOK, map[string]any{
		"alloc_mb": memory.Alloc / 1024 / 1024, "total_alloc_mb": memory.TotalAlloc / 1024 / 1024,
		"sys_mb": memory.Sys / 1024 / 1024, "num_gc": memory.NumGC, "heap_objects": memory.HeapObjects,
		"goroutines": runtime.NumGoroutine(),
	})
}

func (s *Server) handleDebugGoroutines(w http.ResponseWriter, _ *http.Request) {
	buffer := make([]byte, 1024*1024)
	length := runtime.Stack(buffer, true)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buffer[:length])
}

func (s *Server) handleDebugError500(w http.ResponseWriter, r *http.Request) {
	s.renderError(w, r, http.StatusInternalServerError, "This is the deterministic debug error page.")
}

func (s *Server) debugMiddleware(next http.Handler) http.Handler {
	if !mode.IsDebugEnabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(wrapped, r)
		s.debugLog(r, wrapped.Status(), wrapped.BytesWritten(), time.Since(started))
	})
}

func (s *Server) debugLog(r *http.Request, status, size int, duration time.Duration) {
	if mode.IsDebugEnabled() {
		// Request query values, headers, cookies and bodies are omitted to prevent credential leakage.
		slog.Debug("request", "method", r.Method, "path", r.URL.Path, "status", status, "duration_ms", duration.Milliseconds(), "size", size, "remote_addr", r.RemoteAddr, "user_agent", r.UserAgent())
	}
}
