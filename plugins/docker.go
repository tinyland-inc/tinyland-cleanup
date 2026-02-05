// Package plugins provides cleanup plugin implementations.
package plugins

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gitlab.com/tinyland/lab/tinyland-cleanup/config"
)

// DockerPlugin handles Docker cleanup operations.
type DockerPlugin struct{}

// NewDockerPlugin creates a new Docker cleanup plugin.
func NewDockerPlugin() *DockerPlugin {
	return &DockerPlugin{}
}

// Name returns the plugin identifier.
func (p *DockerPlugin) Name() string {
	return "docker"
}

// Description returns the plugin description.
func (p *DockerPlugin) Description() string {
	return "Cleans Docker images, containers, volumes, and build cache"
}

// SupportedPlatforms returns supported platforms (all).
func (p *DockerPlugin) SupportedPlatforms() []string {
	return nil // All platforms
}

// Enabled checks if Docker cleanup is enabled.
func (p *DockerPlugin) Enabled(cfg *config.Config) bool {
	return cfg.Enable.Docker
}

// Cleanup performs Docker cleanup at the specified level.
func (p *DockerPlugin) Cleanup(ctx context.Context, level CleanupLevel, cfg *config.Config, logger *slog.Logger) CleanupResult {
	result := CleanupResult{
		Plugin: p.Name(),
		Level:  level,
	}

	// Check if docker is available
	if !p.isDockerAvailable() {
		logger.Debug("docker not available, skipping")
		return result
	}

	switch level {
	case LevelWarning:
		// Light cleanup: just dangling images
		result = p.cleanDangling(ctx, logger)
	case LevelModerate:
		// Moderate: dangling + old images + old containers
		result = p.cleanModerate(ctx, cfg, logger)
	case LevelAggressive:
		// Aggressive: + volumes + build cache
		result = p.cleanAggressive(ctx, cfg, logger)
	case LevelCritical:
		// Emergency: full system prune with volumes
		result = p.cleanCritical(ctx, logger)
	}

	return result
}

func (p *DockerPlugin) isDockerAvailable() bool {
	cmd := exec.Command("docker", "info")
	return cmd.Run() == nil
}

func (p *DockerPlugin) cleanDangling(ctx context.Context, logger *slog.Logger) CleanupResult {
	result := CleanupResult{Plugin: p.Name(), Level: LevelWarning}

	logger.Debug("cleaning dangling images")
	output, err := p.runDockerCommand(ctx, "image", "prune", "-f")
	if err != nil {
		result.Error = err
		return result
	}

	result.BytesFreed = p.parseReclaimedSpace(output)
	return result
}

func (p *DockerPlugin) cleanModerate(ctx context.Context, cfg *config.Config, logger *slog.Logger) CleanupResult {
	result := CleanupResult{Plugin: p.Name(), Level: LevelModerate}

	// Clean dangling images
	logger.Debug("cleaning dangling images")
	if output, err := p.runDockerCommand(ctx, "image", "prune", "-f"); err == nil {
		result.BytesFreed += p.parseReclaimedSpace(output)
	}

	// Clean old images
	logger.Debug("cleaning old images", "age", cfg.Docker.PruneImagesAge)
	args := []string{"image", "prune", "-af", "--filter", fmt.Sprintf("until=%s", cfg.Docker.PruneImagesAge)}
	if output, err := p.runDockerCommand(ctx, args...); err == nil {
		result.BytesFreed += p.parseReclaimedSpace(output)
	}

	// Clean old stopped containers
	logger.Debug("cleaning old containers")
	if output, err := p.runDockerCommand(ctx, "container", "prune", "-f", "--filter", "until=1h"); err == nil {
		result.BytesFreed += p.parseReclaimedSpace(output)
	}

	// Clean old buildx cache
	logger.Debug("cleaning buildx cache")
	if output, err := p.runDockerCommand(ctx, "buildx", "prune", "-f", "--filter", "until=24h"); err == nil {
		result.BytesFreed += p.parseReclaimedSpace(output)
	}

	return result
}

func (p *DockerPlugin) cleanAggressive(ctx context.Context, cfg *config.Config, logger *slog.Logger) CleanupResult {
	result := p.cleanModerate(ctx, cfg, logger)
	result.Level = LevelAggressive

	// Clean unused volumes
	logger.Debug("cleaning unused volumes")
	if output, err := p.runDockerCommand(ctx, "volume", "prune", "-f"); err == nil {
		result.BytesFreed += p.parseReclaimedSpace(output)
	}

	// Clean all build cache
	logger.Debug("cleaning all build cache")
	if output, err := p.runDockerCommand(ctx, "builder", "prune", "-af"); err == nil {
		result.BytesFreed += p.parseReclaimedSpace(output)
	}

	return result
}

func (p *DockerPlugin) cleanCritical(ctx context.Context, logger *slog.Logger) CleanupResult {
	result := CleanupResult{Plugin: p.Name(), Level: LevelCritical}

	// Full system prune with volumes
	logger.Warn("CRITICAL: running full Docker system prune with volumes")
	output, err := p.runDockerCommand(ctx, "system", "prune", "-af", "--volumes")
	if err != nil {
		result.Error = err
		return result
	}

	result.BytesFreed = p.parseReclaimedSpace(output)
	return result
}

func (p *DockerPlugin) runDockerCommand(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func (p *DockerPlugin) parseReclaimedSpace(output string) int64 {
	// Parse "Total reclaimed space: X.XXY" or similar patterns
	// Examples:
	//   "Total reclaimed space: 1.234GB"
	//   "Total reclaimed space: 567.8MB"
	//   "reclaimed space: 123.4kB"

	patterns := []string{
		`reclaimed space:\s*([\d.]+)\s*([KMGT]?B)`,
		`Total reclaimed space:\s*([\d.]+)\s*([KMGT]?B)`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(output)
		if len(matches) >= 3 {
			value, err := strconv.ParseFloat(matches[1], 64)
			if err != nil {
				continue
			}

			unit := matches[2]
			switch strings.ToUpper(unit) {
			case "KB":
				return int64(value * 1024)
			case "MB":
				return int64(value * 1024 * 1024)
			case "GB":
				return int64(value * 1024 * 1024 * 1024)
			case "TB":
				return int64(value * 1024 * 1024 * 1024 * 1024)
			case "B":
				return int64(value)
			}
		}
	}

	return 0
}

// ProactiveCleanup checks Docker reclaimable space and cleans if needed.
// This is useful for Docker Desktop VMs that have separate disk from host.
func (p *DockerPlugin) ProactiveCleanup(ctx context.Context, logger *slog.Logger) CleanupResult {
	result := CleanupResult{Plugin: p.Name() + "-proactive"}

	// Get Docker system df info
	output, err := p.runDockerCommand(ctx, "system", "df", "--format", "{{.Reclaimable}}")
	if err != nil {
		return result
	}

	// Parse reclaimable space
	reclaimableGB := p.parseReclaimableGB(output)
	if reclaimableGB < 10 {
		return result // Less than 10GB reclaimable, skip
	}

	logger.Info("proactive Docker cleanup", "reclaimable_gb", reclaimableGB)

	// Clean dangling images
	if _, err := p.runDockerCommand(ctx, "image", "prune", "-f"); err == nil {
		result.ItemsCleaned++
	}

	// Clean old containers
	if _, err := p.runDockerCommand(ctx, "container", "prune", "-f", "--filter", "until=1h"); err == nil {
		result.ItemsCleaned++
	}

	// Clean old build cache
	if _, err := p.runDockerCommand(ctx, "builder", "prune", "-f", "--filter", "until=24h"); err == nil {
		result.ItemsCleaned++
	}

	// Clean dangling volumes
	if _, err := p.runDockerCommand(ctx, "volume", "prune", "-f"); err == nil {
		result.ItemsCleaned++
	}

	return result
}

func (p *DockerPlugin) parseReclaimableGB(output string) int {
	// Parse first line which should be something like "10.5GB (50%)"
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return 0
	}

	line := lines[0]
	// Extract number and unit
	re := regexp.MustCompile(`([\d.]+)\s*([KMGT]?B)`)
	matches := re.FindStringSubmatch(line)
	if len(matches) < 3 {
		return 0
	}

	value, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0
	}

	switch strings.ToUpper(matches[2]) {
	case "GB":
		return int(value)
	case "TB":
		return int(value * 1000)
	case "MB":
		return 0 // Less than 1GB
	default:
		return 0
	}
}
