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
	"strconv"
	"strings"
	"time"

	"gitlab.com/tinyland/lab/tinyland-cleanup/config"
)

// LimaPlugin handles Lima VM cleanup and disk resize operations.
// Lima VMs use sparse qcow2 disk images that grow automatically but don't
// shrink when data is deleted. This plugin:
// - Cleans Docker/Podman containers inside VMs
// - Runs fstrim to reclaim space in the disk image
// - Monitors disk usage and triggers resize when needed
// - Supports additional disks with limactl disk resize
type LimaPlugin struct{}

// NewLimaPlugin creates a new Lima VM cleanup plugin.
func NewLimaPlugin() *LimaPlugin {
	return &LimaPlugin{}
}

// Name returns the plugin identifier.
func (p *LimaPlugin) Name() string {
	return "lima"
}

// Description returns the plugin description.
func (p *LimaPlugin) Description() string {
	return "Cleans Lima VMs and manages disk resize operations"
}

// SupportedPlatforms returns supported platforms (Darwin only).
func (p *LimaPlugin) SupportedPlatforms() []string {
	return []string{PlatformDarwin}
}

// Enabled checks if Lima cleanup is enabled.
func (p *LimaPlugin) Enabled(cfg *config.Config) bool {
	return cfg.Enable.Lima
}

// Cleanup performs Lima VM cleanup at the specified level.
func (p *LimaPlugin) Cleanup(ctx context.Context, level CleanupLevel, cfg *config.Config, logger *slog.Logger) CleanupResult {
	result := CleanupResult{
		Plugin: p.Name(),
		Level:  level,
	}

	// Check if limactl is available
	if !p.isLimaAvailable() {
		logger.Debug("limactl not available, skipping")
		return result
	}

	// Get running VMs
	runningVMs, err := p.getRunningVMs(ctx)
	if err != nil {
		result.Error = err
		return result
	}

	if len(runningVMs) == 0 {
		logger.Debug("no running Lima VMs found")
		return result
	}

	// Process configured VMs
	for _, vmName := range cfg.Lima.VMNames {
		if !contains(runningVMs, vmName) {
			logger.Debug("VM not running", "vm", vmName)
			continue
		}

		logger.Debug("processing Lima VM", "vm", vmName, "level", level.String())

		// Check disk usage before cleanup
		diskUsageBefore := p.getVMDiskUsage(ctx, vmName, logger)

		// Perform cleanup based on level
		vmResult := p.cleanupVM(ctx, vmName, level, cfg, logger)
		result.BytesFreed += vmResult.BytesFreed
		result.ItemsCleaned += vmResult.ItemsCleaned

		// Run fstrim to reclaim space
		logger.Debug("running fstrim in Lima VM", "vm", vmName)
		fstrimResult := p.runFSTrim(ctx, vmName, logger)
		result.BytesFreed += fstrimResult.BytesFreed

		// Check disk usage after cleanup
		diskUsageAfter := p.getVMDiskUsage(ctx, vmName, logger)

		// Log disk space reclaimed
		if diskUsageBefore > 0 && diskUsageAfter > 0 {
			spaceReclaimed := diskUsageBefore - diskUsageAfter
			if spaceReclaimed > 0 {
				logger.Info("VM disk space reclaimed",
					"vm", vmName,
					"reclaimed_gb", fmt.Sprintf("%.2f", float64(spaceReclaimed)/(1024*1024*1024)),
					"before_gb", fmt.Sprintf("%.2f", float64(diskUsageBefore)/(1024*1024*1024)),
					"after_gb", fmt.Sprintf("%.2f", float64(diskUsageAfter)/(1024*1024*1024)),
				)
			}
		}
	}

	return result
}

func (p *LimaPlugin) isLimaAvailable() bool {
	_, err := exec.LookPath("limactl")
	return err == nil
}

func (p *LimaPlugin) getRunningVMs(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "limactl", "list", "--format", "{{.Name}}\t{{.Status}}")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list VMs: %w", err)
	}

	var running []string
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) >= 2 && parts[1] == "Running" {
			running = append(running, parts[0])
		}
	}

	return running, nil
}

func (p *LimaPlugin) cleanupVM(ctx context.Context, vmName string, level CleanupLevel, cfg *config.Config, logger *slog.Logger) CleanupResult {
	result := CleanupResult{Plugin: p.Name() + "-" + vmName}

	// Commands to run inside the VM based on cleanup level
	var commands [][]string

	switch level {
	case LevelWarning:
		// Light cleanup: just dangling resources
		commands = [][]string{
			{"docker", "image", "prune", "-f"},
			{"docker", "buildx", "prune", "-f", "--filter", "until=24h"},
		}

	case LevelModerate:
		// Moderate: add old containers and volumes
		commands = [][]string{
			{"docker", "image", "prune", "-af", "--filter", "until=24h"},
			{"docker", "container", "prune", "-f", "--filter", "until=1h"},
			{"docker", "buildx", "prune", "-f", "--filter", "until=24h"},
		}

	case LevelAggressive:
		// Aggressive: add volumes and build cache
		commands = [][]string{
			{"docker", "image", "prune", "-af", "--filter", "until=24h"},
			{"docker", "container", "prune", "-f"},
			{"docker", "volume", "prune", "-f"},
			{"docker", "builder", "prune", "-af"},
		}

	case LevelCritical:
		// Critical: full system prune
		commands = [][]string{
			{"docker", "system", "prune", "-af", "--volumes"},
		}
	}

	// Execute commands inside VM
	for _, args := range commands {
		cmdArgs := append([]string{"shell", vmName, "--"}, args...)
		cmd := exec.CommandContext(ctx, "limactl", cmdArgs...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			logger.Debug("VM command failed", "vm", vmName, "cmd", strings.Join(args, " "), "error", err)
			continue
		}

		// Parse reclaimed space from Docker output
		if bytesFreed := parseDockerReclaimedSpace(string(output)); bytesFreed > 0 {
			result.BytesFreed += bytesFreed
			result.ItemsCleaned++
		}
	}

	return result
}

func (p *LimaPlugin) runFSTrim(ctx context.Context, vmName string, logger *slog.Logger) CleanupResult {
	result := CleanupResult{Plugin: p.Name() + "-fstrim"}

	// Run fstrim -av to reclaim all space
	cmd := exec.CommandContext(ctx, "limactl", "shell", vmName, "--", "sudo", "fstrim", "-av")
	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Debug("fstrim failed", "vm", vmName, "error", err)
		return result
	}

	// Parse fstrim output for bytes trimmed
	// Example: "/var: 1.5 GiB (1610612736 bytes) trimmed on /dev/vda1"
	re := regexp.MustCompile(`(\d+)\s+bytes?\s+trimmed`)
	matches := re.FindAllStringSubmatch(string(output), -1)
	var totalTrimmed int64
	for _, match := range matches {
		if len(match) >= 2 {
			if bytes, err := strconv.ParseInt(match[1], 10, 64); err == nil {
				totalTrimmed += bytes
			}
		}
	}

	if totalTrimmed > 0 {
		result.BytesFreed = totalTrimmed
		logger.Debug("fstrim completed", "vm", vmName, "trimmed_mb", totalTrimmed/(1024*1024))
	}

	return result
}

func (p *LimaPlugin) getVMDiskUsage(ctx context.Context, vmName string, logger *slog.Logger) int64 {
	// Get disk usage via df command inside VM
	cmd := exec.CommandContext(ctx, "limactl", "shell", vmName, "--", "df", "--output=used", "/")
	output, err := cmd.Output()
	if err != nil {
		logger.Debug("failed to get VM disk usage", "vm", vmName, "error", err)
		return 0
	}

	// Parse df output - skip header line
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < 2 {
		return 0
	}

	// Second line is the usage in 1K blocks
	usedStr := strings.TrimSpace(lines[1])
	usedKB, err := strconv.ParseInt(usedStr, 10, 64)
	if err != nil {
		return 0
	}

	return usedKB * 1024 // Convert to bytes
}

// GetVMDiskInfo returns detailed disk information for a Lima VM.
// This is useful for monitoring and determining if resize is needed.
func (p *LimaPlugin) GetVMDiskInfo(ctx context.Context, vmName string) (*VMDiskInfo, error) {
	if !p.isLimaAvailable() {
		return nil, fmt.Errorf("limactl not available")
	}

	// Get VM status
	statusCmd := exec.CommandContext(ctx, "limactl", "list", vmName, "--format", "{{.Status}}")
	statusOutput, err := statusCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get VM status: %w", err)
	}

	status := strings.TrimSpace(string(statusOutput))
	if status != "Running" {
		return &VMDiskInfo{Name: vmName, Status: status}, nil
	}

	// Get disk usage from inside VM
	dfCmd := exec.CommandContext(ctx, "limactl", "shell", vmName, "--",
		"df", "--output=size,used,avail,pcent", "/")
	dfOutput, err := dfCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get disk usage: %w", err)
	}

	// Parse df output
	lines := strings.Split(strings.TrimSpace(string(dfOutput)), "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("unexpected df output")
	}

	fields := strings.Fields(lines[1])
	if len(fields) < 4 {
		return nil, fmt.Errorf("unexpected df format")
	}

	// Parse sizes (in 1K blocks)
	totalKB, _ := strconv.ParseInt(fields[0], 10, 64)
	usedKB, _ := strconv.ParseInt(fields[1], 10, 64)
	availKB, _ := strconv.ParseInt(fields[2], 10, 64)
	usedPercent := strings.TrimSuffix(fields[3], "%")

	// Get disk image file size on host
	home, _ := os.UserHomeDir()
	diskPath := filepath.Join(home, ".lima", vmName, "diffdisk")
	hostSize := int64(0)
	if stat, err := os.Stat(diskPath); err == nil {
		hostSize = stat.Size()
	}

	return &VMDiskInfo{
		Name:           vmName,
		Status:         status,
		TotalBytes:     totalKB * 1024,
		UsedBytes:      usedKB * 1024,
		AvailableBytes: availKB * 1024,
		UsedPercent:    usedPercent,
		HostDiskSize:   hostSize,
		DiskPath:       diskPath,
	}, nil
}

// VMDiskInfo contains disk information for a Lima VM.
type VMDiskInfo struct {
	Name           string
	Status         string
	TotalBytes     int64
	UsedBytes      int64
	AvailableBytes int64
	UsedPercent    string
	HostDiskSize   int64 // Size of diffdisk on host
	DiskPath       string
}

func parseDockerReclaimedSpace(output string) int64 {
	// Parse "Total reclaimed space: X.XXY" patterns
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

			unit := strings.ToUpper(matches[2])
			switch unit {
			case "B":
				return int64(value)
			case "KB":
				return int64(value * 1024)
			case "MB":
				return int64(value * 1024 * 1024)
			case "GB":
				return int64(value * 1024 * 1024 * 1024)
			case "TB":
				return int64(value * 1024 * 1024 * 1024 * 1024)
			}
		}
	}

	return 0
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
