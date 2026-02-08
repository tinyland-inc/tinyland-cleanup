package otel

import (
	"sync"
	"sync/atomic"
	"time"
)

// MetricsCollector tracks cleanup metrics internally.
// When OTel SDK is added later, these feed directly into OTel instruments.
type MetricsCollector struct {
	// Counters (atomic).
	bytesFreedTotal   int64
	itemsCleanedTotal int64
	cyclesTotal       int64
	pluginErrorsTotal int64

	// Gauges and histograms (mutex-protected).
	mu                  sync.RWMutex
	diskUsagePercent    map[string]float64
	diskFreeBytes       map[string]int64
	pluginDuration      map[string]time.Duration
	goroutinePoolActive int32

	// Histograms (sliding window for percentile estimation).
	pluginDurationHist map[string][]float64
	cycleDurationHist  []float64
}

// NewMetricsCollector creates a new metrics collector.
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		diskUsagePercent:   make(map[string]float64),
		diskFreeBytes:      make(map[string]int64),
		pluginDuration:     make(map[string]time.Duration),
		pluginDurationHist: make(map[string][]float64),
	}
}

// RecordBytesFreed adds to the bytes freed counter.
func (m *MetricsCollector) RecordBytesFreed(plugin, mount string, bytes int64) {
	atomic.AddInt64(&m.bytesFreedTotal, bytes)
}

// RecordItemsCleaned adds to the items cleaned counter.
func (m *MetricsCollector) RecordItemsCleaned(plugin string, count int64) {
	atomic.AddInt64(&m.itemsCleanedTotal, count)
}

// RecordCycle increments the cycle counter.
func (m *MetricsCollector) RecordCycle(status string) {
	atomic.AddInt64(&m.cyclesTotal, 1)
}

// RecordPluginError increments the plugin error counter.
func (m *MetricsCollector) RecordPluginError(plugin string) {
	atomic.AddInt64(&m.pluginErrorsTotal, 1)
}

// SetDiskUsage updates the disk usage gauge for a mount.
func (m *MetricsCollector) SetDiskUsage(mount, label string, percent float64, freeBytes int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := mount
	if label != "" {
		key = label
	}
	m.diskUsagePercent[key] = percent
	m.diskFreeBytes[key] = freeBytes
}

// RecordPluginDuration records a plugin execution duration.
func (m *MetricsCollector) RecordPluginDuration(plugin string, d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pluginDuration[plugin] = d
	hist := m.pluginDurationHist[plugin]
	if len(hist) > 100 {
		hist = hist[1:] // sliding window
	}
	m.pluginDurationHist[plugin] = append(hist, d.Seconds())
}

// RecordCycleDuration records a cycle execution duration.
func (m *MetricsCollector) RecordCycleDuration(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.cycleDurationHist) > 100 {
		m.cycleDurationHist = m.cycleDurationHist[1:]
	}
	m.cycleDurationHist = append(m.cycleDurationHist, d.Seconds())
}

// SetPoolActive updates the active goroutine pool gauge.
func (m *MetricsCollector) SetPoolActive(n int32) {
	atomic.StoreInt32(&m.goroutinePoolActive, n)
}

// Snapshot returns a point-in-time copy of all metrics.
func (m *MetricsCollector) Snapshot() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	diskUsage := make(map[string]float64, len(m.diskUsagePercent))
	for k, v := range m.diskUsagePercent {
		diskUsage[k] = v
	}
	diskFree := make(map[string]int64, len(m.diskFreeBytes))
	for k, v := range m.diskFreeBytes {
		diskFree[k] = v
	}

	return map[string]interface{}{
		"bytes_freed_total":     atomic.LoadInt64(&m.bytesFreedTotal),
		"items_cleaned_total":   atomic.LoadInt64(&m.itemsCleanedTotal),
		"cycles_total":          atomic.LoadInt64(&m.cyclesTotal),
		"plugin_errors_total":   atomic.LoadInt64(&m.pluginErrorsTotal),
		"disk_usage_percent":    diskUsage,
		"disk_free_bytes":       diskFree,
		"goroutine_pool_active": atomic.LoadInt32(&m.goroutinePoolActive),
	}
}
