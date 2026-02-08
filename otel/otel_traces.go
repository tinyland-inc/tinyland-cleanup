package otel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Span represents a traced operation.
type Span struct {
	Name      string            `json:"name"`
	TraceID   string            `json:"trace_id"`
	SpanID    string            `json:"span_id"`
	ParentID  string            `json:"parent_id,omitempty"`
	StartTime time.Time         `json:"start_time"`
	EndTime   time.Time         `json:"end_time,omitempty"`
	Attrs     map[string]string `json:"attributes,omitempty"`
	Status    string            `json:"status,omitempty"`
}

// Tracer collects spans and exports them to a JSON fallback file.
type Tracer struct {
	mu       sync.Mutex
	spans    []Span
	path     string
	maxSpans int
}

// NewTracer creates a new tracer with JSON fallback export.
func NewTracer(fallbackPath string) *Tracer {
	return &Tracer{
		path:     fallbackPath,
		maxSpans: 2048,
	}
}

// StartSpan begins a new span and returns it for later ending.
func (t *Tracer) StartSpan(name, traceID, parentID string) *Span {
	if t == nil {
		return nil
	}
	return &Span{
		Name:      name,
		TraceID:   traceID,
		SpanID:    generateID(),
		ParentID:  parentID,
		StartTime: time.Now(),
		Attrs:     make(map[string]string),
	}
}

// EndSpan completes a span and records it.
func (t *Tracer) EndSpan(span *Span, status string) {
	if t == nil || span == nil {
		return
	}
	span.EndTime = time.Now()
	span.Status = status

	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.spans) >= t.maxSpans {
		// Flush before overflow.
		t.flushLocked()
	}
	t.spans = append(t.spans, *span)
}

// Flush writes accumulated spans to the fallback file.
func (t *Tracer) Flush() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.flushLocked()
}

func (t *Tracer) flushLocked() {
	if len(t.spans) == 0 {
		return
	}

	dir := filepath.Dir(t.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return
	}

	data, err := json.MarshalIndent(t.spans, "", "  ")
	if err != nil {
		return
	}

	f, err := os.OpenFile(t.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	f.Write(data)
	f.Write([]byte("\n"))
	t.spans = t.spans[:0]
}

// generateID produces a simple unique ID (not cryptographically secure).
func generateID() string {
	return time.Now().Format("20060102150405.000000000")
}
