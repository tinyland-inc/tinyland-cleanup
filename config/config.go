// Package config provides configuration parsing for tinyland-cleanup.
package config

import (
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"
)

// Config represents the cleanup daemon configuration.
type Config struct {
	// PollInterval in seconds between cleanup checks
	PollInterval int `yaml:"poll_interval"`

	// Thresholds for disk usage (percentage)
	Thresholds Thresholds `yaml:"thresholds"`

	// TargetFree percentage of disk space to achieve after cleanup
	TargetFree int `yaml:"target_free"`

	// LogFile path for cleanup logs
	LogFile string `yaml:"log_file"`

	// Enable flags for specific cleanup plugins
	Enable EnableFlags `yaml:"enable"`

	// Docker-specific settings
	Docker DockerConfig `yaml:"docker"`

	// Lima VM settings (Darwin)
	Lima LimaConfig `yaml:"lima"`

	// Podman-specific settings
	Podman PodmanConfig `yaml:"podman"`

	// iCloud-specific settings (Darwin)
	ICloud ICloudConfig `yaml:"icloud"`

	// GitHub Actions runner settings (Linux)
	GitHubRunner GitHubRunnerConfig `yaml:"github_runner"`

	// Monitored mount points (multi-volume support)
	MonitoredMounts []MountConfig `yaml:"monitored_mounts"`

	// Dev artifact cleanup settings
	DevArtifacts DevArtifactsConfig `yaml:"dev_artifacts"`

	// APFS snapshot settings (Darwin)
	APFS APFSConfig `yaml:"apfs"`

	// Notification settings
	Notify NotifyConfig `yaml:"notify"`

	// Safety constraints for disk operations
	Safety SafetyConfig `yaml:"safety"`

	// Optional backup before destructive operations
	Backup BackupConfig `yaml:"backup"`

	// Goroutine pool settings
	Pool PoolConfig `yaml:"pool"`

	// OpenTelemetry observability settings
	Observability ObservabilityConfig `yaml:"observability"`
}

// GitHubRunnerConfig holds GitHub Actions runner cleanup settings.
type GitHubRunnerConfig struct {
	// Home directory for the runner (default: /home/github-runner)
	Home string `yaml:"home"`
	// WorkDir is the work directory (default: <home>/_work)
	WorkDir string `yaml:"work_dir"`
}

// MountConfig defines a mount point to monitor with optional custom thresholds.
type MountConfig struct {
	// Path is the mount point path
	Path string `yaml:"path"`
	// Label is a human-readable label for logging
	Label string `yaml:"label"`
	// ThresholdWarning overrides the global warning threshold
	ThresholdWarning int `yaml:"threshold_warning,omitempty"`
	// ThresholdCritical overrides the global critical threshold
	ThresholdCritical int `yaml:"threshold_critical,omitempty"`
}

// Thresholds defines disk usage thresholds for graduated cleanup.
type Thresholds struct {
	// Warning triggers level 1 cleanup (caches)
	Warning int `yaml:"warning"`
	// Moderate triggers level 2 cleanup (container images)
	Moderate int `yaml:"moderate"`
	// Aggressive triggers level 3 cleanup (volumes)
	Aggressive int `yaml:"aggressive"`
	// Critical triggers level 4 cleanup (emergency)
	Critical int `yaml:"critical"`
}

// EnableFlags controls which cleanup plugins are enabled.
type EnableFlags struct {
	// Cache cleanup (pip, npm, homebrew caches)
	Cache bool `yaml:"cache"`
	// NixGC for nix-collect-garbage
	NixGC bool `yaml:"nix_gc"`
	// Docker for container image/volume cleanup
	Docker bool `yaml:"docker"`
	// Podman for Podman container/image/volume cleanup
	Podman bool `yaml:"podman"`
	// Lima for Lima VM cleanup (Darwin)
	Lima bool `yaml:"lima"`
	// Homebrew for brew cleanup (Darwin)
	Homebrew bool `yaml:"homebrew"`
	// IOSSimulator for iOS Simulator cleanup (Darwin)
	IOSSimulator bool `yaml:"ios_simulator"`
	// GitLabRunner for GitLab CI cache cleanup
	GitLabRunner bool `yaml:"gitlab_runner"`
	// GitHubRunner for GitHub Actions runner cleanup (Linux)
	GitHubRunner bool `yaml:"github_runner"`
	// Yum for DNF/YUM package cache cleanup (Linux)
	Yum bool `yaml:"yum"`
	// ICloud for iCloud Drive eviction (Darwin)
	ICloud bool `yaml:"icloud"`
	// Photos for Photos library cache cleanup (Darwin)
	Photos bool `yaml:"photos"`
	// DevArtifacts for stale development artifact cleanup
	DevArtifacts bool `yaml:"dev_artifacts"`
	// APFSSnapshots for APFS snapshot thinning (Darwin)
	APFSSnapshots bool `yaml:"apfs_snapshots"`
}

// DockerConfig holds Docker-specific cleanup settings.
type DockerConfig struct {
	// Socket path (unix:///var/run/docker.sock or ~/.colima/default/docker.sock)
	Socket string `yaml:"socket"`
	// PruneImagesAge for images older than this duration
	PruneImagesAge string `yaml:"prune_images_age"`
	// ProtectRunningContainers prevents pruning images used by running containers
	ProtectRunningContainers bool `yaml:"protect_running_containers"`
}

// LimaConfig holds Lima VM cleanup settings.
type LimaConfig struct {
	// VMNames to check for Docker cleanup
	VMNames []string `yaml:"vm_names"`
	// CompactOffline enables offline qcow2 compaction at Critical level
	CompactOffline bool `yaml:"compact_offline"`
	// CompactMethod is the disk compaction method: "in-place" (default) or "copy".
	// "in-place" uses zero-fill + hole-punching (safe, no extra disk space needed).
	// "copy" uses qemu-img convert (legacy, needs 2x disk space).
	CompactMethod string `yaml:"compact_method"`
	// DynamicResizeEnabled enables stop/resize/restart cycle to shrink VM disks
	// Only works with raw format disks (krunkit). Requires VM downtime.
	DynamicResizeEnabled bool `yaml:"dynamic_resize_enabled"`
	// DynamicResizeThreshold is the max guest disk usage % at which resize is worthwhile (default: 75).
	// Resize triggers when guest usage is AT OR BELOW this value (lots of wasted space to reclaim).
	DynamicResizeThreshold int `yaml:"dynamic_resize_threshold"`
	// DynamicResizeMinCooldownHours is the minimum hours between resize operations (default: 24)
	DynamicResizeMinCooldownHours int `yaml:"dynamic_resize_min_cooldown_hours"`
	// DynamicResizeHeadroomGB is GB of free space to preserve after resize (default: 5)
	DynamicResizeHeadroomGB int `yaml:"dynamic_resize_headroom_gb"`
	// DynamicResizeAllowK8s allows resize even when Kubernetes is detected inside the VM.
	// K8s will be temporarily unavailable during the stop/resize/restart cycle.
	DynamicResizeAllowK8s bool `yaml:"dynamic_resize_allow_k8s"`
}

// PodmanConfig holds Podman-specific cleanup settings.
type PodmanConfig struct {
	// PruneImagesAge for images older than this duration
	PruneImagesAge string `yaml:"prune_images_age"`
	// ProtectRunningContainers prevents stopping running containers during critical cleanup
	ProtectRunningContainers bool `yaml:"protect_running_containers"`
	// CleanInsideVM enables cleanup inside Podman machine VM (Darwin)
	CleanInsideVM bool `yaml:"clean_inside_vm"`
	// TrimVMDisk enables fstrim inside VM to reclaim sparse disk space (Darwin)
	TrimVMDisk bool `yaml:"trim_vm_disk"`
	// CompactDiskOffline enables offline raw disk compaction at Critical level
	CompactDiskOffline bool `yaml:"compact_disk_offline"`
	// CompactMethod is the disk compaction method: "in-place" (default) or "copy".
	// "in-place" uses zero-fill + hole-punching (safe, no extra disk space needed).
	// "copy" uses qemu-img convert (legacy, needs 2x disk space).
	CompactMethod string `yaml:"compact_method"`
}

// ICloudConfig holds iCloud-specific cleanup settings (Darwin).
type ICloudConfig struct {
	// EvictAfterDays - only evict files not accessed for this many days
	EvictAfterDays int `yaml:"evict_after_days"`
	// ExcludePaths - paths within iCloud Drive to never evict
	ExcludePaths []string `yaml:"exclude_paths"`
	// MinFileSizeMB - only evict files larger than this (MB)
	MinFileSizeMB int `yaml:"min_file_size_mb"`
}

// DevArtifactsConfig holds development artifact cleanup settings.
type DevArtifactsConfig struct {
	// ScanPaths is the list of directories to scan for dev artifacts
	ScanPaths []string `yaml:"scan_paths"`
	// NodeModules enables node_modules cleanup
	NodeModules bool `yaml:"node_modules"`
	// PythonVenvs enables .venv cleanup
	PythonVenvs bool `yaml:"python_venvs"`
	// RustTargets enables Rust target/ cleanup
	RustTargets bool `yaml:"rust_targets"`
	// GoBuildCache enables Go build cache cleanup
	GoBuildCache bool `yaml:"go_build_cache"`
	// HaskellCache enables .ghcup/cache and .cabal/store cleanup
	HaskellCache bool `yaml:"haskell_cache"`
	// LMStudioModels enables .lmstudio model cleanup (opt-in)
	LMStudioModels bool `yaml:"lmstudio_models"`
	// ProtectPaths are paths that should never be cleaned
	ProtectPaths []string `yaml:"protect_paths"`
}

// APFSConfig holds APFS snapshot cleanup settings (Darwin).
type APFSConfig struct {
	// ThinEnabled enables APFS snapshot thinning
	ThinEnabled bool `yaml:"thin_enabled"`
	// MaxThinGB is the maximum GB to request for thinning
	MaxThinGB int `yaml:"max_thin_gb"`
	// KeepRecentDays keeps snapshots newer than this many days
	KeepRecentDays int `yaml:"keep_recent_days"`
	// DeleteOSUpdates allows deleting pre-update snapshots at Critical level
	DeleteOSUpdates bool `yaml:"delete_os_updates"`
}

// NotifyConfig holds notification settings.
type NotifyConfig struct {
	// Enabled for notifications
	Enabled bool `yaml:"enabled"`
	// WebhookURL for Slack/Discord notifications
	WebhookURL string `yaml:"webhook_url"`
}

// SafetyConfig holds safety constraints for disk operations.
type SafetyConfig struct {
	// OnlyShrink enforces that all disk operations only free space, never consume it.
	// When true, any operation that would increase disk usage is blocked.
	OnlyShrink bool `yaml:"only_shrink"`
	// PreflightSpaceMultiplier is the required ratio of free space to estimated temp usage.
	// E.g., 2.0 means we need 2x the estimated temp file size in free space.
	PreflightSpaceMultiplier float64 `yaml:"preflight_space_multiplier"`
	// MaxTempFileGB is the maximum allowed temporary file size in GB. 0 = no temp files allowed.
	MaxTempFileGB float64 `yaml:"max_temp_file_gb"`
}

// BackupConfig holds optional backup settings for disk operations.
type BackupConfig struct {
	// Enabled turns on backup creation before destructive operations (default: false).
	Enabled bool `yaml:"enabled"`
	// MaxCount is the maximum number of backups to keep (LRU eviction).
	MaxCount int `yaml:"max_count"`
	// Compression algorithm: "zstd", "lz4", "gzip", or "none".
	Compression string `yaml:"compression"`
	// MaxTotalGB is the maximum total backup storage in GB.
	MaxTotalGB float64 `yaml:"max_total_gb"`
	// MinFreeGBToBackup is the minimum free GB required before creating a backup.
	MinFreeGBToBackup float64 `yaml:"min_free_gb_to_backup"`
}

// PoolConfig holds goroutine pool settings for concurrent plugin execution.
type PoolConfig struct {
	// MaxWorkers is the maximum concurrent plugin goroutines (default: 4).
	MaxWorkers int `yaml:"max_workers"`
	// PluginTimeoutMinutes is the default timeout for each plugin in minutes (default: 30).
	PluginTimeoutMinutes int `yaml:"plugin_timeout_minutes"`
	// EventBufferSize is the channel buffer size for the event bus (default: 256).
	EventBufferSize int `yaml:"event_buffer_size"`
}

// ObservabilityConfig holds OpenTelemetry settings.
type ObservabilityConfig struct {
	// Enabled turns on OpenTelemetry instrumentation.
	Enabled bool `yaml:"enabled"`
	// OTLPEndpoint is the OTLP HTTP endpoint (e.g., "http://localhost:4318").
	OTLPEndpoint string `yaml:"otlp_endpoint"`
	// MetricsEnabled enables metric export.
	MetricsEnabled bool `yaml:"metrics_enabled"`
	// TracesEnabled enables trace export.
	TracesEnabled bool `yaml:"traces_enabled"`
	// HeartbeatEnabled enables heartbeat file + watchdog.
	HeartbeatEnabled bool `yaml:"heartbeat_enabled"`
	// HeartbeatPath is the path for the heartbeat JSON file.
	HeartbeatPath string `yaml:"heartbeat_path"`
	// HealthPort is the localhost port for /healthz /readyz (0 = disabled).
	HealthPort int `yaml:"health_port"`
	// FallbackPath is the JSON file path when collector is unavailable.
	FallbackPath string `yaml:"fallback_path"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	logFile := filepath.Join(home, ".local", "log", "disk-cleanup.log")

	defaultScanPaths := []string{
		filepath.Join(home, "git"),
		filepath.Join(home, "src"),
		filepath.Join(home, "projects"),
	}

	config := &Config{
		PollInterval: 60,
		Thresholds: Thresholds{
			Warning:    80,
			Moderate:   85,
			Aggressive: 90,
			Critical:   95,
		},
		TargetFree: 70,
		LogFile:    logFile,
		Enable: EnableFlags{
			Cache:        true,
			NixGC:        true,
			Docker:       true,
			Podman:       true,
			Lima:         runtime.GOOS == "darwin",
			Homebrew:     runtime.GOOS == "darwin",
			IOSSimulator: runtime.GOOS == "darwin",
			GitLabRunner: true,
			ICloud:        runtime.GOOS == "darwin",
			Photos:        runtime.GOOS == "darwin",
			DevArtifacts:  true,
			APFSSnapshots: runtime.GOOS == "darwin",
		},
		Docker: DockerConfig{
			PruneImagesAge:           "24h",
			ProtectRunningContainers: true,
		},
		Podman: PodmanConfig{
			PruneImagesAge:           "24h",
			ProtectRunningContainers: true,
			CleanInsideVM:            true,
			TrimVMDisk:               true,
			CompactMethod:            "in-place",
		},
		Lima: LimaConfig{
			VMNames:                       []string{"colima", "unified"},
			CompactMethod:                 "in-place",
			DynamicResizeThreshold:        75,
			DynamicResizeMinCooldownHours: 24,
			DynamicResizeHeadroomGB:       5,
		},
		ICloud: ICloudConfig{
			EvictAfterDays: 30,
			ExcludePaths:   []string{},
			MinFileSizeMB:  10,
		},
		DevArtifacts: DevArtifactsConfig{
			ScanPaths:      defaultScanPaths,
			NodeModules:    true,
			PythonVenvs:    true,
			RustTargets:    true,
			GoBuildCache:   true,
			HaskellCache:   true,
			LMStudioModels: false,
			ProtectPaths:   []string{},
		},
		APFS: APFSConfig{
			ThinEnabled:    true,
			MaxThinGB:      50,
			KeepRecentDays: 1,
			DeleteOSUpdates: true,
		},
		Notify: NotifyConfig{
			Enabled: false,
		},
		Safety: SafetyConfig{
			OnlyShrink:               true,
			PreflightSpaceMultiplier: 2.0,
			MaxTempFileGB:            0,
		},
		Backup: BackupConfig{
			Enabled:           false,
			MaxCount:          1,
			Compression:       "zstd",
			MaxTotalGB:        10,
			MinFreeGBToBackup: 20,
		},
		Pool: PoolConfig{
			MaxWorkers:           4,
			PluginTimeoutMinutes: 30,
			EventBufferSize:      256,
		},
		Observability: ObservabilityConfig{
			Enabled:          false,
			MetricsEnabled:   true,
			TracesEnabled:    true,
			HeartbeatEnabled: true,
			HeartbeatPath:    filepath.Join(home, ".local", "state", "tinyland-cleanup", "heartbeat"),
			FallbackPath:     filepath.Join(home, ".local", "log", "tinyland-cleanup-otel.json"),
		},
	}

	// Platform-specific socket defaults
	if runtime.GOOS == "darwin" {
		config.Docker.Socket = filepath.Join(home, ".colima", "default", "docker.sock")
	} else {
		config.Docker.Socket = "/var/run/docker.sock"
	}

	return config
}

// LoadConfig loads configuration from a YAML file, merging with defaults.
func LoadConfig(path string) (*Config, error) {
	config := DefaultConfig()

	if path == "" {
		return config, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return config, nil
		}
		return nil, err
	}

	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, err
	}

	return config, nil
}

// SaveConfig saves configuration to a YAML file.
func SaveConfig(config *Config, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}
