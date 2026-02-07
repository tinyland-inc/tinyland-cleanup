package otel

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"gitlab.com/tinyland/lab/tinyland-cleanup/config"
)

func TestFromConfig(t *testing.T) {
	cfg := &config.ObservabilityConfig{
		Enabled:          true,
		OTLPEndpoint:     "http://localhost:4318",
		MetricsEnabled:   true,
		TracesEnabled:    true,
		HeartbeatEnabled: true,
		HeartbeatPath:    "/tmp/test-heartbeat",
	}

	oc := FromConfig(cfg)
	if !oc.Enabled || !oc.MetricsEnabled || !oc.TracesEnabled {
		t.Error("config not properly converted")
	}
}

func TestFromConfigNil(t *testing.T) {
	oc := FromConfig(nil)
	if oc.Enabled {
		t.Error("nil config should produce disabled config")
	}
}

func TestProviderDisabled(t *testing.T) {
	cfg := &Config{Enabled: false}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	p := NewProvider(cfg, logger)
	if p.Metrics() != nil {
		t.Error("disabled provider should have nil metrics")
	}
	if p.Tracer() != nil {
		t.Error("disabled provider should have nil tracer")
	}
	p.Shutdown() // should not panic
}

func TestProviderEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &Config{
		Enabled:          true,
		MetricsEnabled:   true,
		TracesEnabled:    true,
		HeartbeatEnabled: true,
		HeartbeatPath:    tmpDir + "/heartbeat",
		FallbackPath:     tmpDir + "/otel.json",
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	p := NewProvider(cfg, logger)
	defer p.Shutdown()

	if p.Metrics() == nil {
		t.Error("expected metrics collector")
	}
	if p.Tracer() == nil {
		t.Error("expected tracer")
	}
}

func TestMetricsCollector(t *testing.T) {
	m := NewMetricsCollector()
	m.RecordBytesFreed("docker", "/", 1024*1024)
	m.RecordItemsCleaned("docker", 5)
	m.RecordCycle("success")
	m.RecordPluginError("nix")
	m.SetDiskUsage("/", "root", 85.5, 1024*1024*1024)
	m.RecordPluginDuration("docker", 5*time.Second)
	m.RecordCycleDuration(10 * time.Second)
	m.SetPoolActive(3)

	snap := m.Snapshot()
	if snap["bytes_freed_total"].(int64) != 1024*1024 {
		t.Error("bytes_freed_total incorrect")
	}
	if snap["cycles_total"].(int64) != 1 {
		t.Error("cycles_total incorrect")
	}
	if snap["plugin_errors_total"].(int64) != 1 {
		t.Error("plugin_errors_total incorrect")
	}
}

func TestTracer(t *testing.T) {
	tmpDir := t.TempDir()
	tracer := NewTracer(tmpDir + "/traces.json")

	span := tracer.StartSpan("test_op", "trace-1", "")
	span.Attrs["key"] = "value"
	tracer.EndSpan(span, "ok")
	tracer.Flush()

	data, err := os.ReadFile(tmpDir + "/traces.json")
	if err != nil {
		t.Fatalf("trace file not written: %v", err)
	}
	if len(data) == 0 {
		t.Error("trace file is empty")
	}
}

func TestTracerNil(t *testing.T) {
	var tracer *Tracer
	span := tracer.StartSpan("test", "trace-1", "")
	tracer.EndSpan(span, "ok")
	tracer.Flush()
	// Should not panic.
}

func TestHeartbeat(t *testing.T) {
	tmpDir := t.TempDir()
	hb := NewHeartbeat(tmpDir + "/heartbeat.json")
	hb.Tick()

	data, err := os.ReadFile(tmpDir + "/heartbeat.json")
	if err != nil {
		t.Fatalf("heartbeat not written: %v", err)
	}
	if len(data) == 0 {
		t.Error("heartbeat file is empty")
	}
}

func TestHeartbeatNil(t *testing.T) {
	var hb *Heartbeat
	hb.Tick() // should not panic
	hb.Path() // should not panic
}

func TestFallbackExporter(t *testing.T) {
	tmpDir := t.TempDir()
	f := NewFallbackExporter(tmpDir + "/fallback.json")

	err := f.ExportMetrics(map[string]interface{}{"test": 42})
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}

	data, err := os.ReadFile(tmpDir + "/fallback.json")
	if err != nil {
		t.Fatalf("fallback file not written: %v", err)
	}
	if len(data) == 0 {
		t.Error("fallback file is empty")
	}
}

func TestTracingHandler(t *testing.T) {
	// Verify it implements slog.Handler.
	inner := slog.NewTextHandler(os.Stderr, nil)
	h := NewTracingHandler(inner)
	_ = h.WithAttrs(nil)
	_ = h.WithGroup("test")
}
