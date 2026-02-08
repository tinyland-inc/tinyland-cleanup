package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// LogSubscriber logs events using slog.
type LogSubscriber struct {
	logger *slog.Logger
}

// NewLogSubscriber creates a subscriber that logs events.
func NewLogSubscriber(logger *slog.Logger) *LogSubscriber {
	return &LogSubscriber{logger: logger}
}

// Handle processes an event by logging it.
func (s *LogSubscriber) Handle(event Event) {
	switch p := event.Payload.(type) {
	case CycleStartPayload:
		s.logger.Info("cleanup cycle started",
			"cycle_id", p.CycleID,
			"level", p.Level,
			"plugins", p.PluginCount)
	case CycleEndPayload:
		s.logger.Info("cleanup cycle ended",
			"cycle_id", p.CycleID,
			"duration", p.Duration.Round(time.Millisecond),
			"total_freed_mb", p.TotalFreed/(1024*1024),
			"plugins_run", p.PluginsRun,
			"errors", p.PluginErrors)
	case PluginStartPayload:
		s.logger.Debug("plugin started",
			"plugin", p.PluginName,
			"resource_group", p.ResourceGroup)
	case PluginEndPayload:
		if p.BytesFreed > 0 || p.ItemsCleaned > 0 {
			s.logger.Info("plugin completed",
				"plugin", p.PluginName,
				"duration", p.Duration.Round(time.Millisecond),
				"bytes_freed", p.BytesFreed,
				"items_cleaned", p.ItemsCleaned)
		} else {
			s.logger.Debug("plugin completed (nothing freed)",
				"plugin", p.PluginName,
				"duration", p.Duration.Round(time.Millisecond))
		}
	case PluginErrorPayload:
		s.logger.Error("plugin failed",
			"plugin", p.PluginName,
			"error", p.Error)
	case LevelChangedPayload:
		s.logger.Info("cleanup level changed",
			"previous", p.PreviousLevel,
			"new", p.NewLevel,
			"mount", p.Mount,
			"used_percent", fmt.Sprintf("%.1f%%", p.UsedPercent))
	case PluginSkippedPayload:
		s.logger.Debug("plugin skipped",
			"plugin", p.PluginName,
			"reason", p.Reason)
	case PreflightFailedPayload:
		s.logger.Warn("preflight check failed",
			"plugin", p.PluginName,
			"reason", p.Reason,
			"free_gb", fmt.Sprintf("%.1f", p.FreeGB),
			"needed_gb", fmt.Sprintf("%.1f", p.NeededGB))
	}
}

// MetricsSubscriber tracks internal counters for cleanup operations.
type MetricsSubscriber struct {
	mu              sync.RWMutex
	totalFreed      int64
	totalCycles     int64
	totalErrors     int64
	pluginDurations map[string]time.Duration
	pluginBytes     map[string]int64
}

// NewMetricsSubscriber creates a subscriber that tracks metrics.
func NewMetricsSubscriber() *MetricsSubscriber {
	return &MetricsSubscriber{
		pluginDurations: make(map[string]time.Duration),
		pluginBytes:     make(map[string]int64),
	}
}

// Handle processes an event by updating metrics.
func (s *MetricsSubscriber) Handle(event Event) {
	switch p := event.Payload.(type) {
	case CycleEndPayload:
		atomic.AddInt64(&s.totalCycles, 1)
		atomic.AddInt64(&s.totalFreed, p.TotalFreed)
		if p.PluginErrors > 0 {
			atomic.AddInt64(&s.totalErrors, int64(p.PluginErrors))
		}
	case PluginEndPayload:
		s.mu.Lock()
		s.pluginDurations[p.PluginName] = p.Duration
		s.pluginBytes[p.PluginName] += p.BytesFreed
		s.mu.Unlock()
	}
}

// GetTotalFreed returns the total bytes freed across all cycles.
func (s *MetricsSubscriber) GetTotalFreed() int64 {
	return atomic.LoadInt64(&s.totalFreed)
}

// GetTotalCycles returns the total number of cleanup cycles.
func (s *MetricsSubscriber) GetTotalCycles() int64 {
	return atomic.LoadInt64(&s.totalCycles)
}

// GetTotalErrors returns the total number of plugin errors.
func (s *MetricsSubscriber) GetTotalErrors() int64 {
	return atomic.LoadInt64(&s.totalErrors)
}

// GetPluginStats returns duration and bytes freed for each plugin.
func (s *MetricsSubscriber) GetPluginStats() map[string]PluginStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[string]PluginStats, len(s.pluginDurations))
	for name, dur := range s.pluginDurations {
		result[name] = PluginStats{
			LastDuration: dur,
			TotalFreed:   s.pluginBytes[name],
		}
	}
	return result
}

// PluginStats holds per-plugin statistics.
type PluginStats struct {
	LastDuration time.Duration
	TotalFreed   int64
}

// HeartbeatSubscriber writes a JSON heartbeat file periodically.
type HeartbeatSubscriber struct {
	path      string
	startTime time.Time
	mu        sync.Mutex
	lastCycle time.Time
	cyclesRun int64
	totalFreed int64
}

// NewHeartbeatSubscriber creates a subscriber that maintains a heartbeat file.
func NewHeartbeatSubscriber(path string) *HeartbeatSubscriber {
	return &HeartbeatSubscriber{
		path:      path,
		startTime: time.Now(),
	}
}

// Handle processes events to update heartbeat state.
func (s *HeartbeatSubscriber) Handle(event Event) {
	switch p := event.Payload.(type) {
	case CycleEndPayload:
		s.mu.Lock()
		s.lastCycle = event.Timestamp
		s.cyclesRun++
		s.totalFreed += p.TotalFreed
		s.mu.Unlock()
		s.writeHeartbeat()
	case HeartbeatPayload:
		s.writeHeartbeat()
	}
}

// writeHeartbeat writes the heartbeat JSON file atomically.
func (s *HeartbeatSubscriber) writeHeartbeat() {
	s.mu.Lock()
	data := map[string]interface{}{
		"timestamp":      time.Now().UTC().Format(time.RFC3339),
		"uptime_seconds": time.Since(s.startTime).Seconds(),
		"cycles_run":     s.cyclesRun,
		"total_freed":    s.totalFreed,
		"last_cycle_at":  s.lastCycle.UTC().Format(time.RFC3339),
		"pid":            os.Getpid(),
	}
	s.mu.Unlock()

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return
	}

	// Ensure directory exists
	dir := filepath.Dir(s.path)
	os.MkdirAll(dir, 0755)

	// Atomic write: write to temp file, then rename
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, jsonData, 0644); err != nil {
		return
	}
	os.Rename(tmpPath, s.path)
}
