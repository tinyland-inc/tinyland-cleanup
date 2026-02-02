// tinyland-cleanup is a cross-platform disk cleanup daemon.
//
// It monitors disk usage and performs graduated cleanup actions based on
// configurable thresholds. Supports Docker, Nix, Homebrew, Lima, iOS Simulator,
// and various cache cleanup operations.
//
// Usage:
//
//	tinyland-cleanup [flags]
//
// Flags:
//
//	-config string    Path to configuration file (default: ~/.config/tinyland-cleanup/config.yaml)
//	-daemon           Run as a daemon (default: false)
//	-once             Run cleanup once and exit (default: false)
//	-level string     Force cleanup level: none, warning, moderate, aggressive, critical
//	-dry-run          Show what would be cleaned without actually cleaning
//	-verbose          Enable verbose logging
//	-version          Print version and exit
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"gitlab.com/tinyland/lab/tinyland-cleanup/config"
	"gitlab.com/tinyland/lab/tinyland-cleanup/monitor"
	"gitlab.com/tinyland/lab/tinyland-cleanup/plugins"
)

var (
	version = "0.1.0"
	commit  = "dev"
	date    = "unknown"
)

func main() {
	// Parse command line flags
	var (
		configPath  = flag.String("config", "", "Path to configuration file")
		runDaemon   = flag.Bool("daemon", false, "Run as a daemon")
		once        = flag.Bool("once", false, "Run cleanup once and exit")
		level       = flag.String("level", "", "Force cleanup level")
		dryRun      = flag.Bool("dry-run", false, "Show what would be cleaned")
		verbose     = flag.Bool("verbose", false, "Enable verbose logging")
		showVersion = flag.Bool("version", false, "Print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("tinyland-cleanup %s (%s) built %s\n", version, commit, date)
		os.Exit(0)
	}

	// Load configuration first to get log file path
	if *configPath == "" {
		home, _ := os.UserHomeDir()
		*configPath = filepath.Join(home, ".config", "tinyland-cleanup", "config.yaml")
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		// Fall back to stderr logging if config fails
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Setup log file directory
	if err := ensureLogDir(cfg.LogFile); err != nil {
		fmt.Fprintf(os.Stderr, "failed to create log directory: %v\n", err)
		os.Exit(1)
	}

	// Setup logging - write to both stderr and log file
	logLevel := slog.LevelInfo
	if *verbose {
		logLevel = slog.LevelDebug
	}

	// Open log file for writing
	logFile, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open log file: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()

	// Create multi-writer for both stderr and log file
	multiWriter := io.MultiWriter(os.Stderr, logFile)
	logger := slog.New(slog.NewTextHandler(multiWriter, &slog.HandlerOptions{
		Level: logLevel,
	}))

	// Create plugin registry and register all plugins
	registry := plugins.NewRegistry()
	registerPlugins(registry)

	// Create disk monitor
	diskMon := monitor.NewDiskMonitor(
		cfg.Thresholds.Warning,
		cfg.Thresholds.Moderate,
		cfg.Thresholds.Aggressive,
		cfg.Thresholds.Critical,
	)

	// Create cleanup daemon
	d := &daemon{
		config:   cfg,
		registry: registry,
		monitor:  diskMon,
		logger:   logger,
		dryRun:   *dryRun,
	}

	// Determine operation mode
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		logger.Info("received shutdown signal")
		cancel()
	}()

	// If level is specified, force that level
	if *level != "" {
		forcedLevel := parseLevel(*level)
		if err := d.runOnce(ctx, forcedLevel); err != nil {
			logger.Error("cleanup failed", "error", err)
			os.Exit(1)
		}
		return
	}

	// Run once or as daemon
	if *once || !*runDaemon {
		if err := d.runOnce(ctx, monitor.LevelNone); err != nil {
			logger.Error("cleanup failed", "error", err)
			os.Exit(1)
		}
		return
	}

	// Run as daemon
	logger.Info("starting cleanup daemon",
		"poll_interval", cfg.PollInterval,
		"warning", cfg.Thresholds.Warning,
		"moderate", cfg.Thresholds.Moderate,
		"aggressive", cfg.Thresholds.Aggressive,
		"critical", cfg.Thresholds.Critical,
	)

	if err := d.run(ctx); err != nil && err != context.Canceled {
		logger.Error("daemon error", "error", err)
		os.Exit(1)
	}
}

type daemon struct {
	config   *config.Config
	registry *plugins.Registry
	monitor  *monitor.DiskMonitor
	logger   *slog.Logger
	dryRun   bool
}

func (d *daemon) run(ctx context.Context) error {
	ticker := time.NewTicker(time.Duration(d.config.PollInterval) * time.Second)
	defer ticker.Stop()

	// Run immediately on start
	if err := d.runOnce(ctx, monitor.LevelNone); err != nil {
		d.logger.Error("initial cleanup failed", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := d.runOnce(ctx, monitor.LevelNone); err != nil {
				d.logger.Error("cleanup cycle failed", "error", err)
			}
		}
	}
}

func (d *daemon) runOnce(ctx context.Context, forcedLevel monitor.CleanupLevel) error {
	// Check disk usage - use home directory to get correct volume on macOS APFS
	// On macOS, "/" is the sealed system volume, but user data is on /System/Volumes/Data
	// Using $HOME ensures we monitor the volume where data actually lives
	monitorPath := "/"
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		monitorPath = home
	}

	stats, detectedLevel, err := d.monitor.Check(monitorPath)
	if err != nil {
		return fmt.Errorf("failed to check disk: %w", err)
	}

	// Use forced level if specified, otherwise use detected level
	level := detectedLevel
	if forcedLevel != monitor.LevelNone {
		level = forcedLevel
	}

	d.logger.Info("disk status",
		"used_percent", fmt.Sprintf("%.1f%%", stats.UsedPercent),
		"free_gb", fmt.Sprintf("%.1fGB", stats.FreeGB),
		"level", level.String(),
	)

	if level == monitor.LevelNone {
		return nil
	}

	// Convert monitor level to plugin level
	pluginLevel := plugins.CleanupLevel(level)

	// Run cleanup plugins
	enabledPlugins := d.registry.GetEnabled(d.config)
	d.logger.Debug("running plugins", "count", len(enabledPlugins))

	var totalFreed int64
	for _, p := range enabledPlugins {
		if d.dryRun {
			d.logger.Info("would run plugin", "plugin", p.Name(), "level", level.String())
			continue
		}

		result := p.Cleanup(ctx, pluginLevel, d.config, d.logger)
		if result.Error != nil {
			d.logger.Error("plugin failed", "plugin", p.Name(), "error", result.Error)
			continue
		}

		if result.BytesFreed > 0 || result.ItemsCleaned > 0 {
			d.logger.Info("plugin completed",
				"plugin", p.Name(),
				"bytes_freed", result.BytesFreed,
				"items_cleaned", result.ItemsCleaned,
			)
			totalFreed += result.BytesFreed
		}
	}

	if !d.dryRun && totalFreed > 0 {
		d.logger.Info("cleanup complete",
			"total_freed_mb", totalFreed/(1024*1024),
		)
	}

	return nil
}

func registerPlugins(registry *plugins.Registry) {
	// Core plugins (all platforms)
	registry.Register(plugins.NewDockerPlugin())
	registry.Register(plugins.NewNixPlugin())
	registry.Register(plugins.NewCachePlugin())
	registry.Register(plugins.NewGitLabRunnerPlugin())

	// Kubernetes plugins (disabled by default, for future use)
	registry.Register(plugins.NewEtcdPlugin())
	registry.Register(plugins.NewRKE2Plugin())

	// Darwin-specific plugins (registered on all platforms but platform-checked)
	registerDarwinPlugins(registry)
}

func parseLevel(s string) monitor.CleanupLevel {
	switch s {
	case "warning":
		return monitor.LevelWarning
	case "moderate":
		return monitor.LevelModerate
	case "aggressive":
		return monitor.LevelAggressive
	case "critical":
		return monitor.LevelCritical
	default:
		return monitor.LevelNone
	}
}

func ensureLogDir(logFile string) error {
	dir := filepath.Dir(logFile)
	return os.MkdirAll(dir, 0755)
}
