// Package otel provides lightweight observability for tinyland-cleanup.
//
// This is a stub implementation that collects metrics, traces, and heartbeats
// internally and falls back to JSON file export. When the full OTel SDK is
// added to go.mod, the Provider can be wired to real OTLP exporters without
// changing call sites.
package otel

import (
	"gitlab.com/tinyland/lab/tinyland-cleanup/config"
)

// Config wraps the observability config for validation.
type Config struct {
	Enabled          bool
	OTLPEndpoint     string
	MetricsEnabled   bool
	TracesEnabled    bool
	HeartbeatEnabled bool
	HeartbeatPath    string
	HealthPort       int
	FallbackPath     string
}

// FromConfig converts config.ObservabilityConfig to otel.Config.
func FromConfig(cfg *config.ObservabilityConfig) *Config {
	if cfg == nil {
		return &Config{}
	}
	return &Config{
		Enabled:          cfg.Enabled,
		OTLPEndpoint:     cfg.OTLPEndpoint,
		MetricsEnabled:   cfg.MetricsEnabled,
		TracesEnabled:    cfg.TracesEnabled,
		HeartbeatEnabled: cfg.HeartbeatEnabled,
		HeartbeatPath:    cfg.HeartbeatPath,
		HealthPort:       cfg.HealthPort,
		FallbackPath:     cfg.FallbackPath,
	}
}
