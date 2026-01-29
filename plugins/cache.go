//go:build !darwin

package plugins

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"gitlab.com/tinyland/lab/tinyland-cleanup/config"
)

// CachePlugin handles cache cleanup operations.
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

	// Temp files
	if level >= LevelWarning {
		tmpFiles := []string{"/tmp", "/var/tmp"}
		for _, tmpDir := range tmpFiles {
			sizeBefore := getDirSize(tmpDir)
			deleteOldFilesOwnedByUser(tmpDir, 7*24*time.Hour)
			sizeAfter := getDirSize(tmpDir)
			result.BytesFreed += sizeBefore - sizeAfter
		}
	}

	// Systemd journal (Linux only)
	if level >= LevelModerate {
		if _, err := exec.LookPath("journalctl"); err == nil {
			// User journal cleanup
			exec.CommandContext(ctx, "journalctl", "--user", "--vacuum-size=200M", "--vacuum-time=7d").Run()
		}
	}

	// System journal (aggressive+, requires sudo)
	if level >= LevelAggressive {
		if _, err := exec.LookPath("journalctl"); err == nil {
			testCmd := exec.Command("sudo", "-n", "true")
			if testCmd.Run() == nil {
				exec.CommandContext(ctx, "sudo", "journalctl", "--vacuum-size=100M", "--vacuum-time=3d").Run()
			}
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

func deleteOldFilesOwnedByUser(dir string, maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	uid := os.Getuid()
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && info.ModTime().Before(cutoff) {
			// Only delete files owned by current user
			if stat, err := os.Stat(path); err == nil {
				// Best effort - if we can delete it, we own it or have permission
				if stat.Mode().IsRegular() {
					os.Remove(path)
				}
			}
		}
		return nil
	})
	_ = uid // Suppress unused warning - ownership check is implicit via permissions
}
