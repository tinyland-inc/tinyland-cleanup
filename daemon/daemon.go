package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"gitlab.com/tinyland/lab/tinyland-cleanup/config"
	"gitlab.com/tinyland/lab/tinyland-cleanup/monitor"
	"gitlab.com/tinyland/lab/tinyland-cleanup/plugins"
)

// Daemon is the cleanup daemon that monitors disk usage and runs plugins.
type Daemon struct {
	Config   *config.Config
	Registry *plugins.Registry
	Monitor  *monitor.DiskMonitor
	Logger   *slog.Logger
	Bus      *EventBus
	Pool     *Pool
	DryRun   bool

	cycleID int64
}

// New creates a new cleanup daemon.
func New(cfg *config.Config, registry *plugins.Registry, diskMon *monitor.DiskMonitor, logger *slog.Logger) *Daemon {
	bus := NewEventBus(cfg.Pool.EventBufferSize)

	timeout := time.Duration(cfg.Pool.PluginTimeoutMinutes) * time.Minute
	pool := NewPool(cfg.Pool.MaxWorkers, timeout, logger, bus)

	return &Daemon{
		Config:   cfg,
		Registry: registry,
		Monitor:  diskMon,
		Logger:   logger,
		Bus:      bus,
		Pool:     pool,
	}
}

// SetupSubscribers attaches the default event subscribers.
func (d *Daemon) SetupSubscribers() *MetricsSubscriber {
	logSub := NewLogSubscriber(d.Logger)
	d.Bus.Subscribe("log", logSub.Handle)

	metrics := NewMetricsSubscriber()
	d.Bus.Subscribe("metrics", metrics.Handle)

	home, _ := os.UserHomeDir()
	hbPath := d.Config.Observability.HeartbeatPath
	if hbPath == "" {
		hbPath = home + "/.local/state/tinyland-cleanup/heartbeat"
	}
	hb := NewHeartbeatSubscriber(hbPath)
	d.Bus.Subscribe("heartbeat", hb.Handle)

	return metrics
}

// Run starts the daemon loop, checking disk usage at the configured interval.
func (d *Daemon) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Duration(d.Config.PollInterval) * time.Second)
	defer ticker.Stop()

	// Run immediately on start
	if err := d.RunOnce(ctx, monitor.LevelNone); err != nil {
		d.Logger.Error("initial cleanup failed", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := d.RunOnce(ctx, monitor.LevelNone); err != nil {
				d.Logger.Error("cleanup cycle failed", "error", err)
			}
		}
	}
}

// RunOnce performs a single cleanup cycle.
func (d *Daemon) RunOnce(ctx context.Context, forcedLevel monitor.CleanupLevel) error {
	level := forcedLevel

	if level == monitor.LevelNone {
		level = d.CheckMounts()
	}

	if level == monitor.LevelNone {
		return nil
	}

	cycleID := atomic.AddInt64(&d.cycleID, 1)
	pluginLevel := plugins.CleanupLevel(level)
	enabledPlugins := d.Registry.GetEnabled(d.Config)

	// Publish cycle start
	d.Bus.PublishTyped(EventCycleStart, CycleStartPayload{
		CycleID:     cycleID,
		Level:       level.String(),
		PluginCount: len(enabledPlugins),
	})

	start := time.Now()

	if d.DryRun {
		for _, p := range enabledPlugins {
			d.Logger.Info("would run plugin", "plugin", p.Name(), "level", level.String())
		}
		return nil
	}

	// Execute plugins via pool
	var results []PluginResult
	if d.Config.Pool.MaxWorkers <= 1 {
		results = d.Pool.ExecuteSerial(ctx, enabledPlugins, pluginLevel, d.Config, cycleID)
	} else {
		results = d.Pool.Execute(ctx, enabledPlugins, pluginLevel, d.Config, cycleID)
	}

	// Aggregate results
	var totalFreed int64
	var pluginsRun, pluginErrors int
	for _, r := range results {
		if r.Skipped {
			continue
		}
		pluginsRun++
		totalFreed += r.Result.BytesFreed
		if r.Result.Error != nil {
			pluginErrors++
		}
	}

	// Publish cycle end
	d.Bus.PublishTyped(EventCycleEnd, CycleEndPayload{
		CycleID:      cycleID,
		Duration:     time.Since(start),
		TotalFreed:   totalFreed,
		PluginsRun:   pluginsRun,
		PluginErrors: pluginErrors,
	})

	return nil
}

// CheckMounts monitors all configured mount points and returns the highest
// cleanup level detected. Falls back to home directory if no mounts configured.
func (d *Daemon) CheckMounts() monitor.CleanupLevel {
	highestLevel := monitor.LevelNone

	if len(d.Config.MonitoredMounts) > 0 {
		for _, mount := range d.Config.MonitoredMounts {
			stats, err := monitor.GetDiskStats(mount.Path)
			if err != nil {
				d.Logger.Warn("failed to check mount", "path", mount.Path, "label", mount.Label, "error", err)
				continue
			}

			mountMonitor := d.Monitor
			if mount.ThresholdWarning > 0 || mount.ThresholdCritical > 0 {
				warning := d.Config.Thresholds.Warning
				moderate := d.Config.Thresholds.Moderate
				aggressive := d.Config.Thresholds.Aggressive
				critical := d.Config.Thresholds.Critical
				if mount.ThresholdWarning > 0 {
					warning = mount.ThresholdWarning
				}
				if mount.ThresholdCritical > 0 {
					critical = mount.ThresholdCritical
				}
				mountMonitor = monitor.NewDiskMonitor(warning, moderate, aggressive, critical)
			}

			mountLevel := mountMonitor.CheckLevel(stats)
			label := mount.Label
			if label == "" {
				label = mount.Path
			}

			d.Logger.Info("disk status",
				"mount", label,
				"path", mount.Path,
				"used_percent", fmt.Sprintf("%.1f%%", stats.UsedPercent),
				"free_gb", fmt.Sprintf("%.1fGB", stats.FreeGB),
				"level", mountLevel.String(),
			)

			if mountLevel > highestLevel {
				highestLevel = mountLevel
			}
		}
	} else {
		monitorPath := "/"
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			monitorPath = home
		}

		stats, detectedLevel, err := d.Monitor.Check(monitorPath)
		if err != nil {
			d.Logger.Error("failed to check disk", "error", err)
			return monitor.LevelNone
		}

		d.Logger.Info("disk status",
			"used_percent", fmt.Sprintf("%.1f%%", stats.UsedPercent),
			"free_gb", fmt.Sprintf("%.1fGB", stats.FreeGB),
			"level", detectedLevel.String(),
		)

		highestLevel = detectedLevel
	}

	return highestLevel
}

// Close shuts down the daemon and its event bus.
func (d *Daemon) Close() {
	if d.Bus != nil {
		d.Bus.Close()
	}
}
