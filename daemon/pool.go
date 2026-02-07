package daemon

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"gitlab.com/tinyland/lab/tinyland-cleanup/config"
	"gitlab.com/tinyland/lab/tinyland-cleanup/plugins"
)

// PluginResult holds the result of running a plugin.
type PluginResult struct {
	Plugin     string
	Group      string
	Result     plugins.CleanupResult
	Duration   time.Duration
	Skipped    bool
	SkipReason string
}

// Pool executes plugins concurrently with resource group constraints.
// Plugins in the same resource group run serially.
// Different resource groups run in parallel up to maxWorkers.
type Pool struct {
	maxWorkers int
	timeout    time.Duration
	logger     *slog.Logger
	bus        *EventBus
}

// NewPool creates a new plugin execution pool.
func NewPool(maxWorkers int, timeout time.Duration, logger *slog.Logger, bus *EventBus) *Pool {
	if maxWorkers <= 0 {
		maxWorkers = 4
	}
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	return &Pool{
		maxWorkers: maxWorkers,
		timeout:    timeout,
		logger:     logger,
		bus:        bus,
	}
}

// Execute runs all plugins with resource group awareness.
// Returns results for all plugins (including skipped/errored ones).
func (p *Pool) Execute(ctx context.Context, pluginList []plugins.Plugin, level plugins.CleanupLevel, cfg *config.Config, cycleID int64) []PluginResult {
	// Group plugins by resource group
	groups := p.groupPlugins(pluginList)

	// Create a semaphore to limit concurrent groups
	sem := make(chan struct{}, p.maxWorkers)

	var mu sync.Mutex
	var results []PluginResult
	var wg sync.WaitGroup

	// Launch one goroutine per resource group
	for groupName, groupPlugins := range groups {
		wg.Add(1)
		go func(gName string, gPlugins []plugins.Plugin) {
			defer wg.Done()

			// Acquire semaphore slot
			sem <- struct{}{}
			defer func() { <-sem }()

			// Run plugins in this group serially
			for _, plugin := range gPlugins {
				select {
				case <-ctx.Done():
					mu.Lock()
					results = append(results, PluginResult{
						Plugin:     plugin.Name(),
						Group:      gName,
						Skipped:    true,
						SkipReason: "context cancelled",
					})
					mu.Unlock()
					return
				default:
				}

				result := p.runPlugin(ctx, plugin, level, cfg, cycleID, gName)
				mu.Lock()
				results = append(results, result)
				mu.Unlock()
			}
		}(groupName, groupPlugins)
	}

	wg.Wait()
	return results
}

// groupPlugins organizes plugins by resource group.
func (p *Pool) groupPlugins(pluginList []plugins.Plugin) map[string][]plugins.Plugin {
	groups := make(map[string][]plugins.Plugin)
	for _, plugin := range pluginList {
		group := plugins.GetResourceGroup(plugin)
		groups[group] = append(groups[group], plugin)
	}
	return groups
}

// runPlugin executes a single plugin with timeout and event publishing.
func (p *Pool) runPlugin(ctx context.Context, plugin plugins.Plugin, level plugins.CleanupLevel, cfg *config.Config, cycleID int64, group string) PluginResult {
	result := PluginResult{
		Plugin: plugin.Name(),
		Group:  group,
	}

	// Run preflight check
	if err := plugins.RunPreflightCheck(ctx, plugin, cfg); err != nil {
		result.Skipped = true
		result.SkipReason = err.Error()
		if p.bus != nil {
			p.bus.PublishTyped(EventPreflightFailed, PreflightFailedPayload{
				PluginName: plugin.Name(),
				Reason:     err.Error(),
			})
		}
		return result
	}

	// Publish plugin start event
	if p.bus != nil {
		p.bus.PublishTyped(EventPluginStart, PluginStartPayload{
			CycleID:       cycleID,
			PluginName:    plugin.Name(),
			ResourceGroup: group,
		})
	}

	// Run plugin with timeout
	pluginCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	start := time.Now()
	cleanupResult := plugin.Cleanup(pluginCtx, level, cfg, p.logger)
	result.Duration = time.Since(start)
	result.Result = cleanupResult

	// Publish plugin end or error event
	if p.bus != nil {
		if cleanupResult.Error != nil {
			p.bus.PublishTyped(EventPluginError, PluginErrorPayload{
				CycleID:    cycleID,
				PluginName: plugin.Name(),
				Error:      cleanupResult.Error,
			})
		}
		p.bus.PublishTyped(EventPluginEnd, PluginEndPayload{
			CycleID:      cycleID,
			PluginName:   plugin.Name(),
			Duration:     result.Duration,
			BytesFreed:   cleanupResult.BytesFreed,
			ItemsCleaned: cleanupResult.ItemsCleaned,
		})
	}

	return result
}

// ExecuteSerial runs all plugins serially (fallback when pool.max_workers == 1).
// This preserves the original daemon behavior.
func (p *Pool) ExecuteSerial(ctx context.Context, pluginList []plugins.Plugin, level plugins.CleanupLevel, cfg *config.Config, cycleID int64) []PluginResult {
	var results []PluginResult
	for _, plugin := range pluginList {
		select {
		case <-ctx.Done():
			results = append(results, PluginResult{
				Plugin:     plugin.Name(),
				Skipped:    true,
				SkipReason: "context cancelled",
			})
			return results
		default:
		}

		group := plugins.GetResourceGroup(plugin)
		result := p.runPlugin(ctx, plugin, level, cfg, cycleID, group)
		results = append(results, result)
	}
	return results
}
