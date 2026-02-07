package otel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Heartbeat writes periodic heartbeat files for watchdog monitoring.
type Heartbeat struct {
	path      string
	startTime time.Time
	mu        sync.Mutex
	lastTick  time.Time
	tickCount int64
}

// NewHeartbeat creates a new heartbeat writer.
func NewHeartbeat(path string) *Heartbeat {
	return &Heartbeat{
		path:      path,
		startTime: time.Now(),
	}
}

// Tick records a heartbeat and writes the file atomically.
func (h *Heartbeat) Tick() {
	if h == nil {
		return
	}

	h.mu.Lock()
	h.lastTick = time.Now()
	h.tickCount++
	data := map[string]interface{}{
		"timestamp":      h.lastTick.UTC().Format(time.RFC3339),
		"uptime_seconds": time.Since(h.startTime).Seconds(),
		"tick_count":     h.tickCount,
		"pid":            os.Getpid(),
	}
	h.mu.Unlock()

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return
	}

	dir := filepath.Dir(h.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}

	// Atomic write: write to temp file, then rename.
	tmpPath := h.path + ".tmp"
	if err := os.WriteFile(tmpPath, jsonData, 0644); err != nil {
		return
	}
	os.Rename(tmpPath, h.path)
}

// Path returns the heartbeat file path.
func (h *Heartbeat) Path() string {
	if h == nil {
		return ""
	}
	return h.path
}
