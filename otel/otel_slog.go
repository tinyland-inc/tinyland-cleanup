package otel

import (
	"context"
	"log/slog"
)

// traceContextKey is the context key for trace information.
type traceContextKey struct{}

// TraceContext holds trace/span IDs for log correlation.
type TraceContext struct {
	TraceID string
	SpanID  string
}

// WithTraceContext adds trace context to a context.Context.
func WithTraceContext(ctx context.Context, tc TraceContext) context.Context {
	return context.WithValue(ctx, traceContextKey{}, tc)
}

// GetTraceContext extracts trace context from a context.Context.
func GetTraceContext(ctx context.Context) (TraceContext, bool) {
	tc, ok := ctx.Value(traceContextKey{}).(TraceContext)
	return tc, ok
}

// TracingHandler wraps a slog.Handler to inject trace_id and span_id attributes.
type TracingHandler struct {
	inner slog.Handler
}

// NewTracingHandler creates a new handler that enriches logs with trace context.
func NewTracingHandler(inner slog.Handler) *TracingHandler {
	return &TracingHandler{inner: inner}
}

// Enabled delegates to the inner handler.
func (h *TracingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle adds trace context attributes to the log record.
func (h *TracingHandler) Handle(ctx context.Context, record slog.Record) error {
	if tc, ok := GetTraceContext(ctx); ok {
		if tc.TraceID != "" {
			record.AddAttrs(slog.String("trace_id", tc.TraceID))
		}
		if tc.SpanID != "" {
			record.AddAttrs(slog.String("span_id", tc.SpanID))
		}
	}
	return h.inner.Handle(ctx, record)
}

// WithAttrs delegates to the inner handler.
func (h *TracingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &TracingHandler{inner: h.inner.WithAttrs(attrs)}
}

// WithGroup delegates to the inner handler.
func (h *TracingHandler) WithGroup(name string) slog.Handler {
	return &TracingHandler{inner: h.inner.WithGroup(name)}
}
