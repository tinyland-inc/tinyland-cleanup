package otel

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Provider manages observability resources (metrics, traces, heartbeat).
// When OTel SDK is not available or disabled, it operates in fallback mode
// writing structured JSON to files.
type Provider struct {
	cfg      *Config
	logger   *slog.Logger
	metrics  *MetricsCollector
	tracer   *Tracer
	hb       *Heartbeat
	health   *HealthServer
	mu       sync.Mutex
	shutdown bool
}

// NewProvider creates a new observability provider.
// Returns a no-op provider if disabled.
func NewProvider(cfg *Config, logger *slog.Logger) *Provider {
	p := &Provider{
		cfg:    cfg,
		logger: logger,
	}

	if !cfg.Enabled {
		logger.Debug("observability disabled")
		return p
	}

	// Initialize metrics collector.
	if cfg.MetricsEnabled {
		p.metrics = NewMetricsCollector()
		logger.Info("metrics collector initialized (fallback mode)")
	}

	// Initialize tracer.
	if cfg.TracesEnabled {
		p.tracer = NewTracer(cfg.FallbackPath)
		logger.Info("tracer initialized (fallback mode)", "path", cfg.FallbackPath)
	}

	// Initialize heartbeat.
	if cfg.HeartbeatEnabled && cfg.HeartbeatPath != "" {
		p.hb = NewHeartbeat(cfg.HeartbeatPath)
		logger.Info("heartbeat initialized", "path", cfg.HeartbeatPath)
	}

	// Initialize health server.
	if cfg.HealthPort > 0 {
		p.health = NewHealthServer(cfg.HealthPort, logger)
		go p.health.Start()
		logger.Info("health server started", "port", cfg.HealthPort)
	}

	return p
}

// Metrics returns the metrics collector (may be nil if disabled).
func (p *Provider) Metrics() *MetricsCollector {
	return p.metrics
}

// Tracer returns the tracer (may be nil if disabled).
func (p *Provider) Tracer() *Tracer {
	return p.tracer
}

// Heartbeat returns the heartbeat writer (may be nil if disabled).
func (p *Provider) Heartbeat() *Heartbeat {
	return p.hb
}

// RecordHeartbeat writes a heartbeat tick.
func (p *Provider) RecordHeartbeat() {
	if p.hb != nil {
		p.hb.Tick()
	}
}

// Shutdown cleanly shuts down all observability components.
func (p *Provider) Shutdown() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.shutdown {
		return
	}
	p.shutdown = true

	if p.health != nil {
		p.health.Stop()
	}

	// Flush metrics to fallback file.
	if p.metrics != nil && p.cfg.FallbackPath != "" {
		p.flushMetrics()
	}

	// Flush traces to fallback file.
	if p.tracer != nil {
		p.tracer.Flush()
	}

	p.logger.Info("observability shutdown complete")
}

// flushMetrics writes current metrics to the fallback JSON file.
func (p *Provider) flushMetrics() {
	snapshot := p.metrics.Snapshot()
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		p.logger.Warn("failed to marshal metrics", "error", err)
		return
	}

	dir := filepath.Dir(p.cfg.FallbackPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		p.logger.Warn("failed to create fallback directory", "error", err)
		return
	}

	metricsPath := p.cfg.FallbackPath + ".metrics"
	if err := os.WriteFile(metricsPath, data, 0644); err != nil {
		p.logger.Warn("failed to write metrics fallback", "error", err)
	}
}

// ResourceAttributes returns common attributes for all telemetry.
func ResourceAttributes() map[string]string {
	hostname, _ := os.Hostname()
	return map[string]string{
		"service.name":    "tinyland-cleanup",
		"service.version": "0.1.0",
		"host.name":       hostname,
		"timestamp":       time.Now().UTC().Format(time.RFC3339),
	}
}
