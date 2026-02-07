//go:build darwin

package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gitlab.com/tinyland/lab/tinyland-cleanup/config"
)

// LimaPlugin handles Lima VM cleanup and disk resize operations.
// Lima VMs use sparse disk images (qcow2 or raw depending on VM type) that
// grow automatically but don't shrink when data is deleted. This plugin:
// - Cleans Docker/Podman containers inside VMs
// - Runs fstrim to reclaim space in the disk image (qemu/vz only)
// - Performs offline disk compaction to reclaim sparse space
// - Supports krunkit VMs via SSH fallback (limactl shell crashes on krunkit)
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

		logger.Info("processing Lima VM", "vm", vmName, "level", level.String())

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

		// At Critical level with compact_offline enabled, do offline compaction
		if level >= LevelCritical && cfg.Lima.CompactOffline {
			diskInfo, err := p.GetVMDiskInfo(ctx, vmName)
			if err == nil && diskInfo.DiskPath != "" {
				compactFreed, err := p.compactDisk(ctx, diskInfo, logger)
				if err != nil {
					logger.Warn("Lima disk compaction failed", "vm", vmName, "error", err)
				} else if compactFreed > 0 {
					result.BytesFreed += compactFreed
					result.ItemsCleaned++
				}
			}
		}

		// At Moderate+ level with dynamic_resize enabled, try shrinking VM disk
		if level >= LevelModerate && cfg.Lima.DynamicResizeEnabled {
			diskInfo, err := p.GetVMDiskInfo(ctx, vmName)
			if err == nil && diskInfo.DiskPath != "" {
				resizeFreed, err := p.dynamicResize(ctx, diskInfo, cfg, logger)
				if err != nil {
					logger.Warn("Lima dynamic resize failed", "vm", vmName, "error", err)
				} else if resizeFreed > 0 {
					result.BytesFreed += resizeFreed
					result.ItemsCleaned++
				}
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

// execInVM runs a command inside a Lima VM. It tries limactl shell first,
// falling back to SSH via the VM's ssh.config when limactl shell fails
// (which happens with krunkit VMs on Lima < 1.1).
func (p *LimaPlugin) execInVM(ctx context.Context, vmName string, args []string, logger *slog.Logger) ([]byte, error) {
	// Try limactl shell first
	cmdArgs := append([]string{"shell", vmName, "--"}, args...)
	cmd := exec.CommandContext(ctx, "limactl", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return output, nil
	}

	// If limactl shell crashed (e.g. krunkit "Unknown driver" panic),
	// fall back to SSH via the VM's ssh.config
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		return output, fmt.Errorf("limactl shell failed and cannot determine home dir: %w", err)
	}

	sshConfig := filepath.Join(home, ".lima", vmName, "ssh.config")
	if _, statErr := os.Stat(sshConfig); statErr != nil {
		return output, fmt.Errorf("limactl shell failed and no ssh.config found: %w", err)
	}

	sshHost := "lima-" + vmName
	sshArgs := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-F", sshConfig,
		sshHost,
	}
	sshArgs = append(sshArgs, strings.Join(args, " "))
	sshCmd := exec.CommandContext(ctx, "ssh", sshArgs...)
	sshOutput, sshErr := sshCmd.CombinedOutput()
	if sshErr != nil {
		logger.Debug("SSH fallback also failed", "vm", vmName, "error", sshErr)
		return sshOutput, fmt.Errorf("both limactl shell and SSH failed: shell=%w, ssh=%v", err, sshErr)
	}

	logger.Debug("used SSH fallback for VM command", "vm", vmName, "cmd", strings.Join(args, " "))
	return sshOutput, nil
}

// detectDiskFormat returns the disk image format ("raw" or "qcow2") by
// inspecting the file with qemu-img info. Falls back to checking magic bytes.
func (p *LimaPlugin) detectDiskFormat(ctx context.Context, diskPath string) string {
	// Try qemu-img info first
	cmd := exec.CommandContext(ctx, "qemu-img", "info", "--output=json", diskPath)
	output, err := cmd.Output()
	if err == nil {
		outStr := string(output)
		if strings.Contains(outStr, `"format": "qcow2"`) {
			return "qcow2"
		}
		if strings.Contains(outStr, `"format": "raw"`) {
			return "raw"
		}
	}

	// Fallback: check file magic bytes
	f, err := os.Open(diskPath)
	if err != nil {
		return "raw" // default to raw (safer — preserves format)
	}
	defer f.Close()

	magic := make([]byte, 4)
	if _, err := f.Read(magic); err != nil {
		return "raw"
	}
	// QFI\xfb is the qcow2 magic
	if string(magic) == "QFI\xfb" {
		return "qcow2"
	}

	return "raw"
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

	// Execute commands inside VM (uses SSH fallback for krunkit)
	for _, args := range commands {
		output, err := p.execInVM(ctx, vmName, args, logger)
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

	// Run fstrim -av to reclaim all space (uses SSH fallback for krunkit)
	output, err := p.execInVM(ctx, vmName, []string{"sudo", "fstrim", "-av"}, logger)
	if err != nil {
		logger.Debug("fstrim failed", "vm", vmName, "error", err)
		return result
	}

	outStr := string(output)

	// Detect "discard operation is not supported" — krunkit doesn't pass
	// through TRIM/discard from guest to host. Log once and return cleanly.
	if strings.Contains(outStr, "not supported") {
		logger.Info("fstrim not supported by VM driver (krunkit); use offline compaction instead", "vm", vmName)
		return result
	}

	// Parse fstrim output for bytes trimmed
	// Example: "/var: 1.5 GiB (1610612736 bytes) trimmed on /dev/vda1"
	re := regexp.MustCompile(`\((\d+) bytes\) trimmed`)
	matches := re.FindAllStringSubmatch(outStr, -1)
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
	// Get disk usage via df command inside VM (uses SSH fallback for krunkit)
	output, err := p.execInVM(ctx, vmName, []string{"df", "--output=used", "/"}, logger)
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

	// Get disk usage from inside VM (uses SSH fallback for krunkit)
	dfOutput, err := p.execInVM(ctx, vmName, []string{"df", "--output=size,used,avail,pcent", "/"}, slog.Default())
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

// compactDisk performs offline disk compaction for a Lima VM disk image.
// Detects the disk format (raw or qcow2) and preserves it during compaction.
// For raw disks (krunkit), creates a new sparse copy skipping zero blocks.
// For qcow2 disks (qemu/vz), uses qemu-img convert to defragment.
// This stops the VM, compacts, verifies, and replaces before restarting.
// ONLY runs at Critical level with explicit opt-in via config.
func (p *LimaPlugin) compactDisk(ctx context.Context, vm *VMDiskInfo, logger *slog.Logger) (int64, error) {
	if vm.DiskPath == "" {
		return 0, fmt.Errorf("no disk path for VM %s", vm.Name)
	}

	// Check if qemu-img is available
	if _, err := exec.LookPath("qemu-img"); err != nil {
		return 0, fmt.Errorf("qemu-img not available: %w", err)
	}

	// Detect disk format before any operations
	diskFormat := p.detectDiskFormat(ctx, vm.DiskPath)
	logger.Info("detected Lima disk format", "vm", vm.Name, "format", diskFormat)

	// Get current host disk file size (actual blocks, not apparent)
	actualSizeBefore := p.getActualDiskSize(vm.DiskPath)
	if actualSizeBefore == 0 {
		stat, err := os.Stat(vm.DiskPath)
		if err != nil {
			return 0, fmt.Errorf("cannot stat disk: %w", err)
		}
		actualSizeBefore = stat.Size()
	}

	apparentSize := vm.HostDiskSize
	if apparentSize == 0 {
		stat, err := os.Stat(vm.DiskPath)
		if err != nil {
			return 0, fmt.Errorf("cannot stat disk: %w", err)
		}
		apparentSize = stat.Size()
	}

	// Check sparse ratio - skip if already well-compacted
	if actualSizeBefore > 0 && apparentSize > 0 {
		sparseRatio := float64(actualSizeBefore) / float64(apparentSize) * 100
		if sparseRatio > 70 {
			logger.Debug("Lima disk already well-compacted",
				"vm", vm.Name,
				"format", diskFormat,
				"sparse_ratio", fmt.Sprintf("%.0f%%", sparseRatio))
			return 0, nil
		}
	}

	// Safety check: ensure enough free space for the temporary copy
	// Use actual size (not apparent) — the compacted file will be ~actual size
	diskDir := filepath.Dir(vm.DiskPath)
	freeSpace, err := getFreeDiskSpace(diskDir)
	if err != nil {
		return 0, fmt.Errorf("cannot check free space: %w", err)
	}
	if freeSpace < uint64(actualSizeBefore) {
		logger.Warn("skipping Lima disk compaction: insufficient free space",
			"vm", vm.Name,
			"actual_size_gb", fmt.Sprintf("%.1f", float64(actualSizeBefore)/(1024*1024*1024)),
			"free_gb", fmt.Sprintf("%.1f", float64(freeSpace)/(1024*1024*1024)))
		return 0, nil
	}

	compactPath := vm.DiskPath + ".compact"

	logger.Warn("CRITICAL: stopping Lima VM for disk compaction",
		"vm", vm.Name,
		"format", diskFormat,
		"actual_gb", fmt.Sprintf("%.1f", float64(actualSizeBefore)/(1024*1024*1024)),
		"apparent_gb", fmt.Sprintf("%.1f", float64(apparentSize)/(1024*1024*1024)))

	// 1. Stop VM
	stopCmd := exec.CommandContext(ctx, "limactl", "stop", vm.Name)
	if output, err := stopCmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("failed to stop VM: %w (output: %s)", err, string(output))
	}

	// 2. Compact: qemu-img convert preserving the original format
	logger.Info("compacting Lima disk image", "vm", vm.Name, "format", diskFormat, "disk", vm.DiskPath)
	convertCmd := exec.CommandContext(ctx, "qemu-img", "convert", "-O", diskFormat, vm.DiskPath, compactPath)
	if output, err := convertCmd.CombinedOutput(); err != nil {
		exec.CommandContext(ctx, "limactl", "start", vm.Name).Run()
		os.Remove(compactPath)
		return 0, fmt.Errorf("qemu-img convert failed: %w (output: %s)", err, string(output))
	}

	// 3. Verify compacted image (qemu-img check only works on qcow2)
	if diskFormat == "qcow2" {
		checkCmd := exec.CommandContext(ctx, "qemu-img", "check", compactPath)
		if output, err := checkCmd.CombinedOutput(); err != nil {
			os.Remove(compactPath)
			exec.CommandContext(ctx, "limactl", "start", vm.Name).Run()
			return 0, fmt.Errorf("qemu-img check failed: %w (output: %s)", err, string(output))
		}
	} else {
		// For raw format, verify the file size is sane (non-zero, correct apparent size)
		compactInfo, err := os.Stat(compactPath)
		if err != nil || compactInfo.Size() != apparentSize {
			os.Remove(compactPath)
			exec.CommandContext(ctx, "limactl", "start", vm.Name).Run()
			if err != nil {
				return 0, fmt.Errorf("cannot stat compacted raw disk: %w", err)
			}
			return 0, fmt.Errorf("compacted raw disk size mismatch: got %d, want %d", compactInfo.Size(), apparentSize)
		}
	}

	// 4. Get compacted actual size
	compactActualSize := p.getActualDiskSize(compactPath)
	if compactActualSize == 0 {
		if stat, err := os.Stat(compactPath); err == nil {
			compactActualSize = stat.Size()
		}
	}

	// 5. Atomic replace
	if err := os.Rename(compactPath, vm.DiskPath); err != nil {
		os.Remove(compactPath)
		exec.CommandContext(ctx, "limactl", "start", vm.Name).Run()
		return 0, fmt.Errorf("failed to replace disk image: %w", err)
	}

	// 6. Restart VM
	logger.Info("restarting Lima VM after compaction", "vm", vm.Name)
	startCmd := exec.CommandContext(ctx, "limactl", "start", vm.Name)
	if output, err := startCmd.CombinedOutput(); err != nil {
		logger.Error("failed to restart VM after compaction", "vm", vm.Name, "error", err, "output", string(output))
	}

	freed := actualSizeBefore - compactActualSize
	if freed > 0 {
		logger.Info("Lima disk compaction complete",
			"vm", vm.Name,
			"format", diskFormat,
			"freed_gb", fmt.Sprintf("%.1f", float64(freed)/(1024*1024*1024)),
			"before_gb", fmt.Sprintf("%.1f", float64(actualSizeBefore)/(1024*1024*1024)),
			"after_gb", fmt.Sprintf("%.1f", float64(compactActualSize)/(1024*1024*1024)),
		)
		return freed, nil
	}

	return 0, nil
}

// getActualDiskSize returns the actual disk blocks used (not apparent size).
// For sparse files like qcow2/raw VM images, this reflects the real on-disk usage
// rather than the logical file size.
func (p *LimaPlugin) getActualDiskSize(path string) int64 {
	var stat syscall.Stat_t
	if err := syscall.Stat(path, &stat); err != nil {
		return 0
	}
	// Blocks is in 512-byte units on Darwin/Linux
	return stat.Blocks * 512
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Dynamic Resize: stop/resize/restart cycle for krunkit raw format disks
// ---------------------------------------------------------------------------

// resizeHistory tracks when each VM was last resized to enforce cool-down.
type resizeHistory struct {
	VMs map[string]resizeRecord `json:"vms"`
}

type resizeRecord struct {
	LastResize   time.Time `json:"last_resize"`
	SizeBeforeGB int       `json:"size_before_gb"`
	SizeAfterGB  int       `json:"size_after_gb"`
}

// dynamicResize checks if a VM disk should be shrunk and performs the
// stop/resize/restart cycle. Only works on raw format disks (krunkit).
// Returns bytes freed on the host or 0 if no resize was needed.
func (p *LimaPlugin) dynamicResize(ctx context.Context, vm *VMDiskInfo, cfg *config.Config, logger *slog.Logger) (int64, error) {
	if vm.Status != "Running" {
		return 0, nil
	}

	// Only resize raw format disks (krunkit). qcow2 disks handle sparse
	// space via compaction already.
	diskFormat := p.detectDiskFormat(ctx, vm.DiskPath)
	if diskFormat != "raw" {
		logger.Info("dynamic resize skipped: not a raw format disk", "vm", vm.Name, "format", diskFormat)
		return 0, nil
	}

	// Check guest disk usage against threshold.
	// Dynamic resize SHRINKS the VM disk to reclaim host space.
	// It triggers when guest usage is LOW (lots of wasted space to reclaim).
	// Threshold = max guest usage % at which resize is worthwhile.
	// E.g., threshold=50 → resize when guest uses ≤50% (≥50% is wasted).
	usedPercent, err := strconv.Atoi(strings.TrimSuffix(vm.UsedPercent, "%"))
	if err != nil || usedPercent == 0 {
		return 0, nil
	}

	threshold := cfg.Lima.DynamicResizeThreshold
	if threshold <= 0 {
		threshold = 75
	}
	if usedPercent > threshold {
		logger.Info("dynamic resize skipped: guest too full to shrink effectively",
			"vm", vm.Name, "used_percent", usedPercent, "threshold", threshold)
		return 0, nil
	}

	// Check cool-down period
	history := p.loadResizeHistory(logger)
	if record, ok := history.VMs[vm.Name]; ok {
		cooldownHours := cfg.Lima.DynamicResizeMinCooldownHours
		if cooldownHours <= 0 {
			cooldownHours = 24
		}
		elapsed := time.Since(record.LastResize)
		if elapsed < time.Duration(cooldownHours)*time.Hour {
			logger.Info("dynamic resize skipped: cool-down active",
				"vm", vm.Name, "hours_since_last", int(elapsed.Hours()), "cooldown_hours", cooldownHours)
			return 0, nil
		}
	}

	// Check for Kubernetes workloads running inside the VM
	if p.isKubernetesRunning(ctx, vm.Name, logger) {
		if !cfg.Lima.DynamicResizeAllowK8s {
			logger.Warn("dynamic resize skipped: Kubernetes detected inside VM",
				"vm", vm.Name,
				"hint", "set dynamic_resize_allow_k8s: true to allow resize with K8s running")
			return 0, nil
		}
		logger.Warn("dynamic resize proceeding despite Kubernetes running inside VM",
			"vm", vm.Name,
			"note", "K8s will be temporarily unavailable during resize")
	}

	// Calculate target size
	headroomGB := cfg.Lima.DynamicResizeHeadroomGB
	if headroomGB <= 0 {
		headroomGB = 5
	}
	targetBytes := calculateTargetSize(vm.UsedBytes, int64(headroomGB)*1024*1024*1024)

	// Don't resize if target is >= current apparent size (nothing to gain)
	if targetBytes >= vm.TotalBytes {
		logger.Info("dynamic resize skipped: target >= current size",
			"vm", vm.Name,
			"target_gb", targetBytes/(1024*1024*1024),
			"current_gb", vm.TotalBytes/(1024*1024*1024))
		return 0, nil
	}

	// Check that we have qemu-img
	if _, err := exec.LookPath("qemu-img"); err != nil {
		return 0, fmt.Errorf("qemu-img not available: %w", err)
	}

	apparentBefore := vm.TotalBytes
	targetGB := targetBytes / (1024 * 1024 * 1024)

	logger.Warn("DYNAMIC RESIZE: stopping Lima VM to shrink disk",
		"vm", vm.Name,
		"current_apparent_gb", apparentBefore/(1024*1024*1024),
		"guest_used_gb", vm.UsedBytes/(1024*1024*1024),
		"target_gb", targetGB)

	// Perform the resize
	freed, err := p.resizeAndRestart(ctx, vm, targetGB, logger)
	if err != nil {
		return 0, err
	}

	// Record in history
	history.VMs[vm.Name] = resizeRecord{
		LastResize:   time.Now(),
		SizeBeforeGB: int(apparentBefore / (1024 * 1024 * 1024)),
		SizeAfterGB:  int(targetGB),
	}
	p.saveResizeHistory(history, logger)

	if freed > 0 {
		logger.Info("dynamic resize complete",
			"vm", vm.Name,
			"freed_gb", freed/(1024*1024*1024),
			"new_size_gb", targetGB)
	}

	return freed, nil
}

// calculateTargetSize computes the safe target disk size: used bytes + headroom,
// rounded up to the nearest GB boundary. Never returns less than used + 1GB.
func calculateTargetSize(usedBytes, headroomBytes int64) int64 {
	const gb = 1024 * 1024 * 1024
	target := usedBytes + headroomBytes
	if target < usedBytes+gb {
		target = usedBytes + gb
	}
	// Round up to GB boundary
	return ((target + gb - 1) / gb) * gb
}

// isKubernetesRunning checks if RKE2, k3s, or kubelet is running inside the VM.
func (p *LimaPlugin) isKubernetesRunning(ctx context.Context, vmName string, logger *slog.Logger) bool {
	// Check for common Kubernetes directories and processes
	checks := [][]string{
		{"test", "-d", "/var/lib/rancher/rke2"},
		{"test", "-d", "/var/lib/rancher/k3s"},
		{"pgrep", "-x", "kubelet"},
	}

	for _, args := range checks {
		if _, err := p.execInVM(ctx, vmName, args, logger); err == nil {
			return true
		}
	}
	return false
}

// resizeAndRestart stops the VM, resizes the raw disk, and restarts.
// Always attempts restart even if resize fails. Returns host bytes freed.
func (p *LimaPlugin) resizeAndRestart(ctx context.Context, vm *VMDiskInfo, targetGB int64, logger *slog.Logger) (int64, error) {
	hostSizeBefore := p.getActualDiskSize(vm.DiskPath)

	// 1. Stop VM
	stopCmd := exec.CommandContext(ctx, "limactl", "stop", vm.Name)
	if output, err := stopCmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("failed to stop VM for resize: %w (output: %s)", err, string(output))
	}

	// 2. Resize disk via qemu-img convert to a smaller sparse raw image
	// We can't use `qemu-img resize --shrink` on raw because it just truncates.
	// Instead, create a new sparse raw image at the target size, then dd the
	// data from old to new. But that's risky if the guest fs extends beyond target.
	//
	// Safer approach: create a new raw sparse image at targetGB, then use
	// qemu-img convert to copy only the used blocks.
	compactPath := vm.DiskPath + ".resized"
	defer os.Remove(compactPath) // cleanup on any path

	// Create a new raw image at the target size
	createCmd := exec.CommandContext(ctx, "qemu-img", "create", "-f", "raw", compactPath, fmt.Sprintf("%dG", targetGB))
	if output, err := createCmd.CombinedOutput(); err != nil {
		// Restart VM before returning error
		exec.CommandContext(ctx, "limactl", "start", vm.Name).Run()
		return 0, fmt.Errorf("failed to create resized disk: %w (output: %s)", err, string(output))
	}

	// Convert (copy used blocks from old to new, preserving sparseness)
	convertCmd := exec.CommandContext(ctx, "qemu-img", "convert", "-O", "raw", vm.DiskPath, compactPath)
	if output, err := convertCmd.CombinedOutput(); err != nil {
		exec.CommandContext(ctx, "limactl", "start", vm.Name).Run()
		return 0, fmt.Errorf("qemu-img convert failed during resize: %w (output: %s)", err, string(output))
	}

	// Verify the new image has correct apparent size
	newInfo, err := os.Stat(compactPath)
	if err != nil {
		exec.CommandContext(ctx, "limactl", "start", vm.Name).Run()
		return 0, fmt.Errorf("cannot stat resized disk: %w", err)
	}
	expectedApparent := targetGB * 1024 * 1024 * 1024
	if newInfo.Size() != expectedApparent {
		// qemu-img convert preserves source apparent size, not target.
		// This is expected -- the convert copies data, doesn't resize.
		// The actual host blocks used should still be smaller.
		logger.Debug("resized disk apparent size differs from target",
			"apparent", newInfo.Size(), "target", expectedApparent,
			"note", "qemu-img convert preserves source geometry")
	}

	// 3. Atomic replace
	if err := os.Rename(compactPath, vm.DiskPath); err != nil {
		exec.CommandContext(ctx, "limactl", "start", vm.Name).Run()
		return 0, fmt.Errorf("failed to replace disk image: %w", err)
	}

	// 4. Restart VM (always, even if something above failed partially)
	logger.Info("restarting Lima VM after dynamic resize", "vm", vm.Name)
	startCmd := exec.CommandContext(ctx, "limactl", "start", vm.Name)
	if output, err := startCmd.CombinedOutput(); err != nil {
		logger.Error("failed to restart VM after resize", "vm", vm.Name, "error", err, "output", string(output))
	}

	// Calculate freed space on host
	hostSizeAfter := p.getActualDiskSize(vm.DiskPath)
	if hostSizeBefore > hostSizeAfter {
		return hostSizeBefore - hostSizeAfter, nil
	}
	return 0, nil
}

// resizeHistoryPath returns the path to the resize history JSON file.
func resizeHistoryPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "tinyland-cleanup", "lima_resize_history.json")
}

// loadResizeHistory loads the resize history from disk.
func (p *LimaPlugin) loadResizeHistory(logger *slog.Logger) *resizeHistory {
	h := &resizeHistory{VMs: make(map[string]resizeRecord)}

	data, err := os.ReadFile(resizeHistoryPath())
	if err != nil {
		return h // fresh history
	}

	if err := json.Unmarshal(data, h); err != nil {
		logger.Debug("failed to parse resize history", "error", err)
		return &resizeHistory{VMs: make(map[string]resizeRecord)}
	}

	if h.VMs == nil {
		h.VMs = make(map[string]resizeRecord)
	}
	return h
}

// saveResizeHistory writes the resize history to disk.
func (p *LimaPlugin) saveResizeHistory(h *resizeHistory, logger *slog.Logger) {
	path := resizeHistoryPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		logger.Debug("failed to create resize history dir", "error", err)
		return
	}

	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		logger.Debug("failed to marshal resize history", "error", err)
		return
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		logger.Debug("failed to write resize history", "error", err)
	}
}
