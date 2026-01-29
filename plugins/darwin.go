//go:build darwin

package plugins

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gitlab.com/tinyland/lab/tinyland-cleanup/config"
)

// HomebrewPlugin handles Homebrew cleanup operations.
type HomebrewPlugin struct{}

// NewHomebrewPlugin creates a new Homebrew cleanup plugin.
func NewHomebrewPlugin() *HomebrewPlugin {
	return &HomebrewPlugin{}
}

// Name returns the plugin identifier.
func (p *HomebrewPlugin) Name() string {
	return "homebrew"
}

// Description returns the plugin description.
func (p *HomebrewPlugin) Description() string {
	return "Cleans Homebrew caches and old formula versions"
}

// SupportedPlatforms returns supported platforms (Darwin only).
func (p *HomebrewPlugin) SupportedPlatforms() []string {
	return []string{PlatformDarwin}
}

// Enabled checks if Homebrew cleanup is enabled.
func (p *HomebrewPlugin) Enabled(cfg *config.Config) bool {
	return cfg.Enable.Homebrew
}

// Cleanup performs Homebrew cleanup at the specified level.
func (p *HomebrewPlugin) Cleanup(ctx context.Context, level CleanupLevel, cfg *config.Config, logger *slog.Logger) CleanupResult {
	result := CleanupResult{
		Plugin: p.Name(),
		Level:  level,
	}

	// Check if brew is available
	if _, err := exec.LookPath("brew"); err != nil {
		logger.Debug("brew not available, skipping")
		return result
	}

	switch level {
	case LevelWarning:
		// Light: just remove downloads cache
		result = p.cleanCache(ctx, logger)
	case LevelModerate, LevelAggressive:
		// Moderate/Aggressive: cleanup --prune=0 (remove all old versions)
		result = p.cleanupPrune(ctx, logger)
	case LevelCritical:
		// Critical: autoremove + full cleanup
		result = p.cleanupCritical(ctx, logger)
	}

	return result
}

func (p *HomebrewPlugin) cleanCache(ctx context.Context, logger *slog.Logger) CleanupResult {
	result := CleanupResult{Plugin: p.Name(), Level: LevelWarning}

	// Get cache size before
	home, _ := os.UserHomeDir()
	cachePath := filepath.Join(home, "Library", "Caches", "Homebrew")
	sizeBefore := getDirSize(cachePath)

	logger.Debug("cleaning Homebrew cache")
	cmd := exec.CommandContext(ctx, "brew", "cleanup", "-s")
	cmd.Run() // Ignore errors

	sizeAfter := getDirSize(cachePath)
	result.BytesFreed = sizeBefore - sizeAfter
	return result
}

func (p *HomebrewPlugin) cleanupPrune(ctx context.Context, logger *slog.Logger) CleanupResult {
	result := CleanupResult{Plugin: p.Name(), Level: LevelModerate}

	logger.Debug("running brew cleanup --prune=0")
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "brew", "cleanup", "--prune=0")
	output, _ := cmd.CombinedOutput()

	// Parse "Removing: /path/to/file... (X.X MB)"
	result.BytesFreed = parseBrewCleanupOutput(string(output))
	return result
}

func (p *HomebrewPlugin) cleanupCritical(ctx context.Context, logger *slog.Logger) CleanupResult {
	result := CleanupResult{Plugin: p.Name(), Level: LevelCritical}

	// First autoremove unused dependencies
	logger.Warn("CRITICAL: running brew autoremove")
	autoremoveCmd := exec.CommandContext(ctx, "brew", "autoremove")
	autoremoveCmd.Run()

	// Then full cleanup
	logger.Warn("CRITICAL: running brew cleanup --prune=0")
	cleanupCmd := exec.CommandContext(ctx, "brew", "cleanup", "--prune=0")
	output, _ := cleanupCmd.CombinedOutput()

	result.BytesFreed = parseBrewCleanupOutput(string(output))
	return result
}

// IOSSimulatorPlugin handles iOS Simulator cleanup operations.
type IOSSimulatorPlugin struct{}

// NewIOSSimulatorPlugin creates a new iOS Simulator cleanup plugin.
func NewIOSSimulatorPlugin() *IOSSimulatorPlugin {
	return &IOSSimulatorPlugin{}
}

// Name returns the plugin identifier.
func (p *IOSSimulatorPlugin) Name() string {
	return "ios-simulator"
}

// Description returns the plugin description.
func (p *IOSSimulatorPlugin) Description() string {
	return "Cleans iOS Simulator devices and runtimes"
}

// SupportedPlatforms returns supported platforms (Darwin only).
func (p *IOSSimulatorPlugin) SupportedPlatforms() []string {
	return []string{PlatformDarwin}
}

// Enabled checks if iOS Simulator cleanup is enabled.
func (p *IOSSimulatorPlugin) Enabled(cfg *config.Config) bool {
	return cfg.Enable.IOSSimulator
}

// Cleanup performs iOS Simulator cleanup at the specified level.
func (p *IOSSimulatorPlugin) Cleanup(ctx context.Context, level CleanupLevel, cfg *config.Config, logger *slog.Logger) CleanupResult {
	result := CleanupResult{
		Plugin: p.Name(),
		Level:  level,
	}

	// Check if xcrun is available
	if _, err := exec.LookPath("xcrun"); err != nil {
		logger.Debug("xcrun not available, skipping")
		return result
	}

	switch level {
	case LevelWarning, LevelModerate:
		// Light/moderate: delete unavailable devices
		result = p.deleteUnavailable(ctx, logger)
	case LevelAggressive:
		// Aggressive: + delete device data
		result = p.cleanAggressive(ctx, logger)
	case LevelCritical:
		// Critical: + delete runtimes
		result = p.cleanCritical(ctx, logger)
	}

	return result
}

func (p *IOSSimulatorPlugin) deleteUnavailable(ctx context.Context, logger *slog.Logger) CleanupResult {
	result := CleanupResult{Plugin: p.Name(), Level: LevelWarning}

	logger.Debug("deleting unavailable iOS Simulators")
	cmd := exec.CommandContext(ctx, "xcrun", "simctl", "delete", "unavailable")
	if err := cmd.Run(); err != nil {
		// Not a hard error - may have no unavailable devices
		logger.Debug("xcrun simctl delete unavailable completed", "error", err)
	}

	return result
}

func (p *IOSSimulatorPlugin) cleanAggressive(ctx context.Context, logger *slog.Logger) CleanupResult {
	result := p.deleteUnavailable(ctx, logger)
	result.Level = LevelAggressive

	// Clean up simulator device data directory
	home, _ := os.UserHomeDir()
	devicePath := filepath.Join(home, "Library", "Developer", "CoreSimulator", "Devices")

	if info, err := os.Stat(devicePath); err == nil && info.IsDir() {
		sizeBefore := getDirSize(devicePath)

		// Delete old log files
		filepath.Walk(devicePath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !info.IsDir() && strings.HasSuffix(path, ".log") {
				os.Remove(path)
			}
			return nil
		})

		sizeAfter := getDirSize(devicePath)
		result.BytesFreed = sizeBefore - sizeAfter
	}

	return result
}

func (p *IOSSimulatorPlugin) cleanCritical(ctx context.Context, logger *slog.Logger) CleanupResult {
	result := p.cleanAggressive(ctx, logger)
	result.Level = LevelCritical

	// Check runtime size
	runtimesPath := "/Library/Developer/CoreSimulator/Volumes"
	runtimeSize := getDirSize(runtimesPath)

	// Only delete runtimes if they're taking up significant space (>1GB)
	if runtimeSize > 1024*1024*1024 {
		logger.Warn("CRITICAL: iOS Simulator runtimes",
			"size_gb", fmt.Sprintf("%.1f", float64(runtimeSize)/(1024*1024*1024)))

		// Try passwordless sudo first
		testCmd := exec.Command("sudo", "-n", "true")
		if testCmd.Run() == nil {
			logger.Warn("CRITICAL: deleting all iOS Simulator runtimes")
			deleteCmd := exec.CommandContext(ctx, "sudo", "xcrun", "simctl", "runtime", "delete", "all")
			if err := deleteCmd.Run(); err != nil {
				logger.Error("failed to delete runtimes", "error", err)
			} else {
				result.BytesFreed += runtimeSize
			}
		} else {
			logger.Warn("sudo not available, skipping runtime deletion")
		}
	}

	return result
}

// XcodePlugin handles Xcode cleanup operations.
type XcodePlugin struct{}

// NewXcodePlugin creates a new Xcode cleanup plugin.
func NewXcodePlugin() *XcodePlugin {
	return &XcodePlugin{}
}

// Name returns the plugin identifier.
func (p *XcodePlugin) Name() string {
	return "xcode"
}

// Description returns the plugin description.
func (p *XcodePlugin) Description() string {
	return "Cleans Xcode DerivedData, archives, and device support"
}

// SupportedPlatforms returns supported platforms (Darwin only).
func (p *XcodePlugin) SupportedPlatforms() []string {
	return []string{PlatformDarwin}
}

// Enabled checks if Xcode cleanup is enabled (uses iOS Simulator flag).
func (p *XcodePlugin) Enabled(cfg *config.Config) bool {
	return cfg.Enable.IOSSimulator // Bundled with iOS Simulator cleanup
}

// Cleanup performs Xcode cleanup at the specified level.
func (p *XcodePlugin) Cleanup(ctx context.Context, level CleanupLevel, cfg *config.Config, logger *slog.Logger) CleanupResult {
	result := CleanupResult{
		Plugin: p.Name(),
		Level:  level,
	}

	home, _ := os.UserHomeDir()
	xcodeDevDir := filepath.Join(home, "Library", "Developer", "Xcode")

	if _, err := os.Stat(xcodeDevDir); os.IsNotExist(err) {
		return result
	}

	switch level {
	case LevelWarning, LevelModerate:
		// Light: clean old logs
		result.BytesFreed = p.cleanLogs(xcodeDevDir, logger)
	case LevelAggressive:
		// Aggressive: + clean old DerivedData
		result.BytesFreed = p.cleanDerivedData(xcodeDevDir, logger)
	case LevelCritical:
		// Critical: + clean archives and device support
		result.BytesFreed = p.cleanCritical(xcodeDevDir, logger)
	}

	return result
}

func (p *XcodePlugin) cleanLogs(xcodeDir string, logger *slog.Logger) int64 {
	var freed int64

	logsDir := filepath.Join(xcodeDir, "Logs")
	if _, err := os.Stat(logsDir); err == nil {
		sizeBefore := getDirSize(logsDir)
		// Delete logs older than 7 days
		deleteOldFiles(logsDir, 7*24*time.Hour)
		sizeAfter := getDirSize(logsDir)
		freed = sizeBefore - sizeAfter
		logger.Debug("cleaned Xcode logs", "freed_mb", freed/(1024*1024))
	}

	return freed
}

func (p *XcodePlugin) cleanDerivedData(xcodeDir string, logger *slog.Logger) int64 {
	freed := p.cleanLogs(xcodeDir, logger)

	derivedData := filepath.Join(xcodeDir, "DerivedData")
	if info, err := os.Stat(derivedData); err == nil && info.IsDir() {
		sizeBefore := getDirSize(derivedData)
		if sizeBefore > 500*1024*1024 { // Only if > 500MB
			logger.Debug("cleaning Xcode DerivedData", "size_mb", sizeBefore/(1024*1024))
			os.RemoveAll(derivedData)
			freed += sizeBefore
		}
	}

	return freed
}

func (p *XcodePlugin) cleanCritical(xcodeDir string, logger *slog.Logger) int64 {
	freed := p.cleanDerivedData(xcodeDir, logger)

	// Clean archives > 500MB
	archivesDir := filepath.Join(xcodeDir, "Archives")
	if info, err := os.Stat(archivesDir); err == nil && info.IsDir() {
		size := getDirSize(archivesDir)
		if size > 500*1024*1024 {
			logger.Warn("CRITICAL: cleaning Xcode Archives", "size_mb", size/(1024*1024))
			os.RemoveAll(archivesDir)
			freed += size
		}
	}

	// Clean iOS DeviceSupport, keeping only 2 most recent
	deviceSupportDir := filepath.Join(xcodeDir, "iOS DeviceSupport")
	freed += p.cleanDeviceSupport(deviceSupportDir, 2, logger)

	return freed
}

func (p *XcodePlugin) cleanDeviceSupport(dir string, keepCount int, logger *slog.Logger) int64 {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return 0
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}

	// Only clean if we have more than keepCount entries
	if len(entries) <= keepCount {
		return 0
	}

	// Sort by modification time (newest first)
	type dirEntry struct {
		name    string
		modTime time.Time
	}
	dirs := make([]dirEntry, 0)
	for _, e := range entries {
		if e.IsDir() {
			info, err := e.Info()
			if err == nil {
				dirs = append(dirs, dirEntry{name: e.Name(), modTime: info.ModTime()})
			}
		}
	}

	sort.Slice(dirs, func(i, j int) bool {
		return dirs[i].modTime.After(dirs[j].modTime)
	})

	var freed int64
	for i := keepCount; i < len(dirs); i++ {
		fullPath := filepath.Join(dir, dirs[i].name)
		size := getDirSize(fullPath)
		if err := os.RemoveAll(fullPath); err == nil {
			freed += size
			logger.Debug("removed old iOS DeviceSupport", "version", dirs[i].name)
		}
	}

	return freed
}

// CachePlugin handles macOS cache cleanup.
type CachePlugin struct{}

// NewCachePlugin creates a new cache cleanup plugin.
func NewCachePlugin() *CachePlugin {
	return &CachePlugin{}
}

// Name returns the plugin identifier.
func (p *CachePlugin) Name() string {
	return "cache"
}

// Description returns the plugin description.
func (p *CachePlugin) Description() string {
	return "Cleans various application caches (pip, npm, go, etc.)"
}

// SupportedPlatforms returns supported platforms (all).
func (p *CachePlugin) SupportedPlatforms() []string {
	return nil // All platforms
}

// Enabled checks if cache cleanup is enabled.
func (p *CachePlugin) Enabled(cfg *config.Config) bool {
	return cfg.Enable.Cache
}

// Cleanup performs cache cleanup at the specified level.
func (p *CachePlugin) Cleanup(ctx context.Context, level CleanupLevel, cfg *config.Config, logger *slog.Logger) CleanupResult {
	result := CleanupResult{
		Plugin: p.Name(),
		Level:  level,
	}

	home, _ := os.UserHomeDir()

	// pip cache
	pipCache := filepath.Join(home, ".cache", "pip")
	if size := getDirSize(pipCache); size > 0 {
		if level >= LevelWarning {
			os.RemoveAll(pipCache)
			result.BytesFreed += size
			logger.Debug("cleaned pip cache", "freed_mb", size/(1024*1024))
		}
	}

	// npm cache
	npmCache := filepath.Join(home, ".npm", "_cacache")
	if size := getDirSize(npmCache); size > 0 {
		if level >= LevelWarning {
			os.RemoveAll(npmCache)
			result.BytesFreed += size
			logger.Debug("cleaned npm cache", "freed_mb", size/(1024*1024))
		}
	}

	// go module cache (only at aggressive or higher)
	if level >= LevelAggressive {
		goModCache := filepath.Join(home, "go", "pkg", "mod", "cache")
		if size := getDirSize(goModCache); size > 0 {
			exec.CommandContext(ctx, "go", "clean", "-modcache").Run()
			result.BytesFreed += size
			logger.Debug("cleaned go mod cache", "freed_mb", size/(1024*1024))
		}
	}

	// Cargo cache (only old .crate files at moderate+)
	if level >= LevelModerate {
		cargoCache := filepath.Join(home, ".cargo", "registry", "cache")
		if _, err := os.Stat(cargoCache); err == nil {
			sizeBefore := getDirSize(cargoCache)
			deleteOldFiles(cargoCache, 30*24*time.Hour)
			sizeAfter := getDirSize(cargoCache)
			result.BytesFreed += sizeBefore - sizeAfter
		}
	}

	// macOS Library/Caches (only at critical)
	if level >= LevelCritical {
		libraryCaches := filepath.Join(home, "Library", "Caches")
		if _, err := os.Stat(libraryCaches); err == nil {
			sizeBefore := getDirSize(libraryCaches)
			// Delete files older than 30 days
			deleteOldFiles(libraryCaches, 30*24*time.Hour)
			sizeAfter := getDirSize(libraryCaches)
			result.BytesFreed += sizeBefore - sizeAfter
			logger.Debug("cleaned macOS Library/Caches", "freed_mb", (sizeBefore-sizeAfter)/(1024*1024))
		}
	}

	return result
}

// Helper functions

func getDirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size
}

func deleteOldFiles(dir string, maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && info.ModTime().Before(cutoff) {
			os.Remove(path)
		}
		return nil
	})
}

func parseBrewCleanupOutput(output string) int64 {
	// Parse lines like "Removing: /path/to/file... (1.2 MB)"
	re := regexp.MustCompile(`\((\d+\.?\d*)\s*([KMGT]?B)\)`)
	var total int64

	for _, match := range re.FindAllStringSubmatch(output, -1) {
		if len(match) >= 3 {
			value, err := strconv.ParseFloat(match[1], 64)
			if err != nil {
				continue
			}
			switch strings.ToUpper(match[2]) {
			case "KB":
				total += int64(value * 1024)
			case "MB":
				total += int64(value * 1024 * 1024)
			case "GB":
				total += int64(value * 1024 * 1024 * 1024)
			case "B":
				total += int64(value)
			}
		}
	}

	return total
}
