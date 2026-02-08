package daemon

import (
	"context"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.com/tinyland/lab/tinyland-cleanup/config"
	"gitlab.com/tinyland/lab/tinyland-cleanup/plugins"
)

// mockPlugin implements plugins.Plugin for testing.
type mockPlugin struct {
	name     string
	platform string
	enabled  bool
	duration time.Duration
	freed    int64
	err      error
}

func (m *mockPlugin) Name() string                 { return m.name }
func (m *mockPlugin) Description() string           { return "test plugin" }
func (m *mockPlugin) SupportedPlatforms() []string  { return nil }
func (m *mockPlugin) Enabled(cfg *config.Config) bool { return m.enabled }
func (m *mockPlugin) Cleanup(ctx context.Context, level plugins.CleanupLevel, cfg *config.Config, logger *slog.Logger) plugins.CleanupResult {
	if m.duration > 0 {
		select {
		case <-time.After(m.duration):
		case <-ctx.Done():
			return plugins.CleanupResult{
				Plugin: m.name,
				Error:  ctx.Err(),
			}
		}
	}
	return plugins.CleanupResult{
		Plugin:     m.name,
		BytesFreed: m.freed,
		Error:      m.err,
	}
}

// mockPluginV2 implements PluginV2 for testing resource groups.
type mockPluginV2 struct {
	mockPlugin
	group string
}

func (m *mockPluginV2) ResourceGroup() string                                    { return m.group }
func (m *mockPluginV2) EstimatedDuration() time.Duration                         { return m.duration }
func (m *mockPluginV2) PreflightCheck(ctx context.Context, cfg *config.Config) error { return nil }

func TestPoolExecuteSerial(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	bus := NewEventBus(16)
	defer bus.Close()

	pool := NewPool(1, time.Minute, logger, bus)
	cfg := config.DefaultConfig()

	pluginList := []plugins.Plugin{
		&mockPlugin{name: "p1", enabled: true, freed: 1024},
		&mockPlugin{name: "p2", enabled: true, freed: 2048},
	}

	results := pool.ExecuteSerial(context.Background(), pluginList, plugins.LevelWarning, cfg, 1)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	totalFreed := int64(0)
	for _, r := range results {
		totalFreed += r.Result.BytesFreed
	}
	if totalFreed != 3072 {
		t.Errorf("expected 3072 total freed, got %d", totalFreed)
	}
}

func TestPoolExecuteParallel(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	bus := NewEventBus(16)
	defer bus.Close()

	pool := NewPool(4, time.Minute, logger, bus)
	cfg := config.DefaultConfig()

	// Create plugins in different resource groups
	pluginList := []plugins.Plugin{
		&mockPluginV2{mockPlugin: mockPlugin{name: "docker", enabled: true, freed: 100, duration: 50 * time.Millisecond}, group: "container-docker"},
		&mockPluginV2{mockPlugin: mockPlugin{name: "nix", enabled: true, freed: 200, duration: 50 * time.Millisecond}, group: "nix-store"},
		&mockPluginV2{mockPlugin: mockPlugin{name: "cache", enabled: true, freed: 300, duration: 50 * time.Millisecond}, group: "filesystem-scan"},
	}

	start := time.Now()
	results := pool.Execute(context.Background(), pluginList, plugins.LevelWarning, cfg, 1)
	elapsed := time.Since(start)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// With 4 workers and 3 different groups, all should run in parallel
	// Total time should be ~50ms, not ~150ms
	if elapsed > 200*time.Millisecond {
		t.Errorf("parallel execution took too long: %v (expected ~50ms)", elapsed)
	}
}

func TestPoolResourceGroupSerialization(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	bus := NewEventBus(16)
	defer bus.Close()

	pool := NewPool(4, time.Minute, logger, bus)
	cfg := config.DefaultConfig()

	// Track execution order within the same group
	var order int32
	var p1Order, p2Order int32

	p1 := &mockPlugin{name: "group-a-1", enabled: true, duration: 20 * time.Millisecond}
	p2 := &mockPlugin{name: "group-a-2", enabled: true, duration: 20 * time.Millisecond}

	// We need PluginV2 for explicit groups
	pluginList := []plugins.Plugin{
		&mockPluginV2{mockPlugin: *p1, group: "test-group"},
		&mockPluginV2{mockPlugin: *p2, group: "test-group"},
	}

	// Subscribe to track order
	bus.Subscribe("order", func(e Event) {
		if e.Type == EventPluginStart {
			p := e.Payload.(PluginStartPayload)
			idx := atomic.AddInt32(&order, 1)
			if p.PluginName == "group-a-1" {
				atomic.StoreInt32(&p1Order, idx)
			} else if p.PluginName == "group-a-2" {
				atomic.StoreInt32(&p2Order, idx)
			}
		}
	})

	pool.Execute(context.Background(), pluginList, plugins.LevelWarning, cfg, 1)
	time.Sleep(50 * time.Millisecond) // wait for event processing

	// Both should have run (order doesn't matter for same group, just that they ran)
	if atomic.LoadInt32(&p1Order) == 0 || atomic.LoadInt32(&p2Order) == 0 {
		t.Error("both plugins in same group should have run")
	}

	// Within the same group, p1 should start before p2 (serial execution)
	if atomic.LoadInt32(&p1Order) >= atomic.LoadInt32(&p2Order) {
		t.Errorf("expected p1 (order=%d) to start before p2 (order=%d) in same group",
			atomic.LoadInt32(&p1Order), atomic.LoadInt32(&p2Order))
	}
}

func TestPoolContextCancellation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	bus := NewEventBus(16)
	defer bus.Close()

	pool := NewPool(1, time.Minute, logger, bus)
	cfg := config.DefaultConfig()

	// Cancel context immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	pluginList := []plugins.Plugin{
		&mockPlugin{name: "p1", enabled: true, duration: time.Second},
	}

	results := pool.ExecuteSerial(ctx, pluginList, plugins.LevelWarning, cfg, 1)

	if len(results) != 1 || !results[0].Skipped {
		t.Error("plugin should be skipped on cancelled context")
	}
}

func TestPoolEventPublishing(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	bus := NewEventBus(64)

	var starts, ends int32
	bus.Subscribe("counter", func(e Event) {
		switch e.Type {
		case EventPluginStart:
			atomic.AddInt32(&starts, 1)
		case EventPluginEnd:
			atomic.AddInt32(&ends, 1)
		}
	})

	pool := NewPool(4, time.Minute, logger, bus)
	cfg := config.DefaultConfig()

	pluginList := []plugins.Plugin{
		&mockPlugin{name: "p1", enabled: true, freed: 100},
		&mockPlugin{name: "p2", enabled: true, freed: 200},
	}

	pool.ExecuteSerial(context.Background(), pluginList, plugins.LevelWarning, cfg, 1)
	time.Sleep(50 * time.Millisecond)
	bus.Close()

	if atomic.LoadInt32(&starts) != 2 || atomic.LoadInt32(&ends) != 2 {
		t.Errorf("expected 2 starts and 2 ends, got %d starts %d ends",
			atomic.LoadInt32(&starts), atomic.LoadInt32(&ends))
	}
}

func TestPoolDefaultValues(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	pool := NewPool(0, 0, logger, nil)
	if pool.maxWorkers != 4 {
		t.Errorf("expected default maxWorkers 4, got %d", pool.maxWorkers)
	}
	if pool.timeout != 30*time.Minute {
		t.Errorf("expected default timeout 30m, got %v", pool.timeout)
	}

	pool2 := NewPool(-1, -1, logger, nil)
	if pool2.maxWorkers != 4 {
		t.Errorf("expected default maxWorkers 4 for negative input, got %d", pool2.maxWorkers)
	}
}

func TestPoolNilBus(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Pool with nil bus should not panic
	pool := NewPool(1, time.Minute, logger, nil)
	cfg := config.DefaultConfig()

	pluginList := []plugins.Plugin{
		&mockPlugin{name: "p1", enabled: true, freed: 1024},
	}

	results := pool.ExecuteSerial(context.Background(), pluginList, plugins.LevelWarning, cfg, 1)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Result.BytesFreed != 1024 {
		t.Errorf("expected 1024 bytes freed, got %d", results[0].Result.BytesFreed)
	}
}

func TestPoolGroupPlugins(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	pool := NewPool(4, time.Minute, logger, nil)

	pluginList := []plugins.Plugin{
		&mockPluginV2{mockPlugin: mockPlugin{name: "a1"}, group: "group-a"},
		&mockPluginV2{mockPlugin: mockPlugin{name: "a2"}, group: "group-a"},
		&mockPluginV2{mockPlugin: mockPlugin{name: "b1"}, group: "group-b"},
		&mockPlugin{name: "solo"},
	}

	groups := pool.groupPlugins(pluginList)

	if len(groups["group-a"]) != 2 {
		t.Errorf("expected 2 plugins in group-a, got %d", len(groups["group-a"]))
	}
	if len(groups["group-b"]) != 1 {
		t.Errorf("expected 1 plugin in group-b, got %d", len(groups["group-b"]))
	}
	// mockPlugin without PluginV2 falls back to registry map or "default"
	if groups["default"] == nil {
		t.Error("expected solo plugin in default group")
	}
}

func TestPoolParallelContextCancellation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	bus := NewEventBus(16)
	defer bus.Close()

	pool := NewPool(4, time.Minute, logger, bus)
	cfg := config.DefaultConfig()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	pluginList := []plugins.Plugin{
		&mockPluginV2{mockPlugin: mockPlugin{name: "p1", enabled: true, duration: time.Second}, group: "g1"},
		&mockPluginV2{mockPlugin: mockPlugin{name: "p2", enabled: true, duration: time.Second}, group: "g2"},
	}

	results := pool.Execute(ctx, pluginList, plugins.LevelWarning, cfg, 1)

	allSkipped := true
	for _, r := range results {
		if !r.Skipped {
			allSkipped = false
		}
	}
	if !allSkipped {
		t.Error("all plugins should be skipped when context is already cancelled")
	}
}
