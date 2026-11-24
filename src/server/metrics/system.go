package metrics

import (
	"runtime"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

// SystemCollector periodically samples host and Go runtime resource metrics.
type SystemCollector struct {
	dataDir        string
	includeSystem  bool
	includeRuntime bool
	interval       time.Duration
	stop           chan struct{}
	startOnce      sync.Once
	stopOnce       sync.Once
	lastGC         uint32
	lastPause      uint64
	additional     func()
}

// SetAdditionalCollector adds application-specific gauges to each collection cycle.
func (c *SystemCollector) SetAdditionalCollector(collect func()) {
	c.additional = collect
}

// NewSystemCollector returns a collector that may be started once per server process.
func NewSystemCollector(dataDir string, includeSystem, includeRuntime bool) *SystemCollector {
	return &SystemCollector{dataDir: dataDir, includeSystem: includeSystem, includeRuntime: includeRuntime, interval: 15 * time.Second, stop: make(chan struct{})}
}

// Start samples immediately and then at a fixed interval.
func (c *SystemCollector) Start() {
	c.startOnce.Do(func() {
		c.collect()
		go func() {
			ticker := time.NewTicker(c.interval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					c.collect()
				case <-c.stop:
					return
				}
			}
		}()
	})
}

// Stop terminates background collection safely.
func (c *SystemCollector) Stop() {
	c.stopOnce.Do(func() { close(c.stop) })
}

func (c *SystemCollector) collect() {
	if c.includeSystem {
		if values, err := cpu.Percent(0, false); err == nil && len(values) > 0 {
			SystemCPUUsage.Set(values[0])
		}
		if values, err := mem.VirtualMemory(); err == nil {
			SystemMemoryUsage.Set(values.UsedPercent)
			SystemMemoryUsed.Set(float64(values.Used))
			SystemMemoryTotal.Set(float64(values.Total))
		}
		if c.dataDir != "" {
			if values, err := disk.Usage(c.dataDir); err == nil {
				SystemDiskUsage.WithLabelValues(c.dataDir).Set(values.UsedPercent)
				SystemDiskUsed.WithLabelValues(c.dataDir).Set(float64(values.Used))
				SystemDiskTotal.WithLabelValues(c.dataDir).Set(float64(values.Total))
			}
		}
	}
	if c.includeRuntime {
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		GoGoroutines.Set(float64(runtime.NumGoroutine()))
		GoMemAlloc.Set(float64(stats.Alloc))
		GoMemSys.Set(float64(stats.Sys))
		if stats.NumGC > c.lastGC {
			GoGCRuns.Add(float64(stats.NumGC - c.lastGC))
			c.lastGC = stats.NumGC
		}
		if stats.PauseTotalNs > c.lastPause {
			GoGCPauseTotal.Add(float64(stats.PauseTotalNs-c.lastPause) / float64(time.Second))
			c.lastPause = stats.PauseTotalNs
		}
	}
	if c.additional != nil {
		c.additional()
	}
}
