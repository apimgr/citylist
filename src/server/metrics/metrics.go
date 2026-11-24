package metrics

import (
	"runtime"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const namespace = "citylist"

var (
	Registry = prometheus.NewRegistry()
	initOnce sync.Once

	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec
	HTTPRequestSize     *prometheus.HistogramVec
	HTTPResponseSize    *prometheus.HistogramVec
	HTTPActiveRequests  prometheus.Gauge

	DBQueriesTotal        *prometheus.CounterVec
	DBQueryDuration       *prometheus.HistogramVec
	DBConnectionsOpen     prometheus.Gauge
	DBConnectionsInUse    prometheus.Gauge
	DBErrors              *prometheus.CounterVec
	CacheHits             *prometheus.CounterVec
	CacheMisses           *prometheus.CounterVec
	CacheEvictions        *prometheus.CounterVec
	CacheSize             *prometheus.GaugeVec
	CacheBytes            *prometheus.GaugeVec
	SchedulerTasksTotal   *prometheus.CounterVec
	SchedulerTaskDuration *prometheus.HistogramVec
	SchedulerTasksRunning *prometheus.GaugeVec
	SchedulerLastRun      *prometheus.GaugeVec
	RateLimitRequests     *prometheus.CounterVec
	RateLimitBlocked      *prometheus.CounterVec
	AuthAttempts          *prometheus.CounterVec
	OwnerTokensActive     *prometheus.GaugeVec
	CitiesTotal           prometheus.Gauge
	ZipcodesTotal         prometheus.Gauge
	AppInfo               *prometheus.GaugeVec
	AppUptime             prometheus.Gauge
	AppStartTime          prometheus.Gauge
	SystemCPUUsage        prometheus.Gauge
	SystemMemoryUsage     prometheus.Gauge
	SystemMemoryUsed      prometheus.Gauge
	SystemMemoryTotal     prometheus.Gauge
	SystemDiskUsage       *prometheus.GaugeVec
	SystemDiskUsed        *prometheus.GaugeVec
	SystemDiskTotal       *prometheus.GaugeVec
	GoGoroutines          prometheus.Gauge
	GoMemAlloc            prometheus.Gauge
	GoMemSys              prometheus.Gauge
	GoGCRuns              prometheus.Counter
	GoGCPauseTotal        prometheus.Counter
)

var defaultDurationBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
var defaultSizeBuckets = []float64{100, 1000, 10000, 100000, 1000000, 10000000}

// Init registers the process-wide metrics collectors once, using configured histogram buckets.
func Init(durationBuckets, sizeBuckets []float64) {
	initOnce.Do(func() {
		if len(durationBuckets) == 0 {
			durationBuckets = defaultDurationBuckets
		}
		if len(sizeBuckets) == 0 {
			sizeBuckets = defaultSizeBuckets
		}
		HTTPRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: namespace, Name: "http_requests_total", Help: "Total number of HTTP requests"}, []string{"method", "path", "status"})
		HTTPRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: namespace, Name: "http_request_duration_seconds", Help: "HTTP request duration in seconds", Buckets: durationBuckets}, []string{"method", "path"})
		HTTPRequestSize = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: namespace, Name: "http_request_size_bytes", Help: "HTTP request size in bytes", Buckets: sizeBuckets}, []string{"method", "path"})
		HTTPResponseSize = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: namespace, Name: "http_response_size_bytes", Help: "HTTP response size in bytes", Buckets: sizeBuckets}, []string{"method", "path"})
		HTTPActiveRequests = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: namespace, Name: "http_active_requests", Help: "Number of active HTTP requests"})
		DBQueriesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: namespace, Name: "db_queries_total", Help: "Total database queries"}, []string{"operation", "table"})
		DBQueryDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: namespace, Name: "db_query_duration_seconds", Help: "Database query duration in seconds", Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1}}, []string{"operation", "table"})
		DBConnectionsOpen = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: namespace, Name: "db_connections_open", Help: "Number of open database connections"})
		DBConnectionsInUse = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: namespace, Name: "db_connections_in_use", Help: "Number of database connections in use"})
		DBErrors = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: namespace, Name: "db_errors_total", Help: "Total database errors"}, []string{"operation", "error_type"})
		CacheHits = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: namespace, Name: "cache_hits_total", Help: "Total cache hits"}, []string{"cache"})
		CacheMisses = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: namespace, Name: "cache_misses_total", Help: "Total cache misses"}, []string{"cache"})
		CacheEvictions = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: namespace, Name: "cache_evictions_total", Help: "Total cache evictions"}, []string{"cache"})
		CacheSize = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: namespace, Name: "cache_size", Help: "Current cache item count"}, []string{"cache"})
		CacheBytes = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: namespace, Name: "cache_bytes", Help: "Current cache size in bytes"}, []string{"cache"})
		SchedulerTasksTotal = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: namespace, Name: "scheduler_tasks_total", Help: "Total scheduled task executions"}, []string{"task", "status"})
		SchedulerTaskDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: namespace, Name: "scheduler_task_duration_seconds", Help: "Scheduled task duration in seconds", Buckets: []float64{0.1, 0.5, 1, 5, 10, 30, 60, 300, 600}}, []string{"task"})
		SchedulerTasksRunning = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: namespace, Name: "scheduler_tasks_running", Help: "Number of currently running scheduled tasks"}, []string{"task"})
		SchedulerLastRun = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: namespace, Name: "scheduler_last_run_timestamp", Help: "Timestamp of last task run"}, []string{"task"})
		RateLimitRequests = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: namespace, Name: "ratelimit_requests_total", Help: "Total requests handled by rate limits"}, []string{"limit", "status"})
		RateLimitBlocked = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: namespace, Name: "ratelimit_blocked_total", Help: "Requests blocked by rate limits"}, []string{"limit"})
		AuthAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: namespace, Name: "auth_attempts_total", Help: "Total authentication attempts"}, []string{"method", "status"})
		OwnerTokensActive = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: namespace, Name: "owner_tokens_active", Help: "Active owner tokens by token kind"}, []string{"kind"})
		CitiesTotal = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: namespace, Name: "cities_total", Help: "Number of city records available"})
		ZipcodesTotal = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: namespace, Name: "zipcodes_total", Help: "Number of ZIP code records available"})
		AppInfo = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: namespace, Name: "app_info", Help: "Application build information"}, []string{"version", "commit", "build_date", "go_version"})
		AppUptime = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: namespace, Name: "app_uptime_seconds", Help: "Application uptime in seconds"})
		AppStartTime = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: namespace, Name: "app_start_timestamp", Help: "Application start timestamp"})
		SystemCPUUsage = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: namespace, Name: "system_cpu_usage_percent", Help: "Current system CPU usage percentage"})
		SystemMemoryUsage = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: namespace, Name: "system_memory_usage_percent", Help: "Current system memory usage percentage"})
		SystemMemoryUsed = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: namespace, Name: "system_memory_used_bytes", Help: "System memory used in bytes"})
		SystemMemoryTotal = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: namespace, Name: "system_memory_total_bytes", Help: "Total system memory in bytes"})
		SystemDiskUsage = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: namespace, Name: "system_disk_usage_percent", Help: "Disk usage percentage"}, []string{"path"})
		SystemDiskUsed = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: namespace, Name: "system_disk_used_bytes", Help: "Disk used in bytes"}, []string{"path"})
		SystemDiskTotal = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: namespace, Name: "system_disk_total_bytes", Help: "Disk total bytes"}, []string{"path"})
		GoGoroutines = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: namespace, Name: "go_goroutines", Help: "Current number of goroutines"})
		GoMemAlloc = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: namespace, Name: "go_mem_alloc_bytes", Help: "Bytes allocated and in use"})
		GoMemSys = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: namespace, Name: "go_mem_sys_bytes", Help: "Bytes obtained from the system"})
		GoGCRuns = prometheus.NewCounter(prometheus.CounterOpts{Namespace: namespace, Name: "go_gc_runs_total", Help: "Total garbage collection runs"})
		GoGCPauseTotal = prometheus.NewCounter(prometheus.CounterOpts{Namespace: namespace, Name: "go_gc_pause_total_seconds", Help: "Total time spent in garbage collection pauses"})
		Registry.MustRegister(
			HTTPRequestsTotal, HTTPRequestDuration, HTTPRequestSize, HTTPResponseSize, HTTPActiveRequests,
			DBQueriesTotal, DBQueryDuration, DBConnectionsOpen, DBConnectionsInUse, DBErrors,
			CacheHits, CacheMisses, CacheEvictions, CacheSize, CacheBytes,
			SchedulerTasksTotal, SchedulerTaskDuration, SchedulerTasksRunning, SchedulerLastRun,
			RateLimitRequests, RateLimitBlocked, AuthAttempts, OwnerTokensActive, CitiesTotal, ZipcodesTotal,
			AppInfo, AppUptime, AppStartTime, SystemCPUUsage, SystemMemoryUsage, SystemMemoryUsed, SystemMemoryTotal,
			SystemDiskUsage, SystemDiskUsed, SystemDiskTotal, GoGoroutines, GoMemAlloc, GoMemSys, GoGCRuns, GoGCPauseTotal,
		)
		OwnerTokensActive.WithLabelValues("primary").Set(0)
		OwnerTokensActive.WithLabelValues("linked").Set(0)
		AppStartTime.SetToCurrentTime()
		SetBuildInfo("dev", "unknown", "unknown")
		StartUptimeUpdater()
		InstallLokiHandler()
	})
}

// SetBuildInfo updates the app info gauge with build and runtime metadata.
func SetBuildInfo(version, commit, buildDate string) {
	if AppInfo != nil {
		AppInfo.Reset()
		AppInfo.WithLabelValues(version, commit, buildDate, runtime.Version()).Set(1)
	}
}

// StartUptimeUpdater updates the uptime gauge for the life of the process.
func StartUptimeUpdater() {
	go func(started time.Time) {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			AppUptime.Set(time.Since(started).Seconds())
		}
	}(time.Now())
}
