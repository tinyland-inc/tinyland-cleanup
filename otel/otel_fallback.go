package otel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FallbackExporter writes telemetry data to JSON files when the OTel collector
// is unavailable. This ensures no data loss even without infrastructure.
type FallbackExporter struct {
	path string
	mu   sync.Mutex
}

// NewFallbackExporter creates a new fallback exporter.
func NewFallbackExporter(path string) *FallbackExporter {
	return &FallbackExporter{path: path}
}

// ExportMetrics writes a metrics snapshot to the fallback file.
func (f *FallbackExporter) ExportMetrics(snapshot map[string]interface{}) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	entry := map[string]interface{}{
		"type":      "metrics",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"data":      snapshot,
	}

	return f.appendJSON(entry)
}

// ExportSpans writes spans to the fallback file.
func (f *FallbackExporter) ExportSpans(spans []Span) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	entry := map[string]interface{}{
		"type":      "traces",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"data":      spans,
	}

	return f.appendJSON(entry)
}

func (f *FallbackExporter) appendJSON(entry interface{}) error {
	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	file, err := os.OpenFile(f.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	data = append(data, '\n')
	_, err = file.Write(data)
	return err
}
