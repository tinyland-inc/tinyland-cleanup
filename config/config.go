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

	// Notification settings
	Notify NotifyConfig `yaml:"notify"`
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
}

// PodmanConfig holds Podman-specific cleanup settings.
type PodmanConfig struct {
	// PruneImagesAge for images older than this duration
	PruneImagesAge string `yaml:"prune_images_age"`
	// ProtectRunningContainers prevents pruning images used by running containers
	ProtectRunningContainers bool `yaml:"protect_running_containers"`
	// MachineNames to check for cleanup (Darwin)
	MachineNames []string `yaml:"machine_names"`
	// CleanInsideVM enables cleanup inside Podman machine VM (Darwin)
	CleanInsideVM bool `yaml:"clean_inside_vm"`
	// TrimVMDisk enables fstrim inside VM to reclaim sparse disk space (Darwin)
	TrimVMDisk bool `yaml:"trim_vm_disk"`
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

// NotifyConfig holds notification settings.
type NotifyConfig struct {
	// Enabled for notifications
	Enabled bool `yaml:"enabled"`
	// WebhookURL for Slack/Discord notifications
	WebhookURL string `yaml:"webhook_url"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	logFile := filepath.Join(home, ".local", "log", "disk-cleanup.log")

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
			ICloud:       runtime.GOOS == "darwin",
			Photos:       runtime.GOOS == "darwin",
		},
		Docker: DockerConfig{
			PruneImagesAge:           "24h",
			ProtectRunningContainers: true,
		},
		Podman: PodmanConfig{
			PruneImagesAge:           "24h",
			ProtectRunningContainers: true,
			MachineNames:             []string{"podman-machine-default"},
			CleanInsideVM:            true,
			TrimVMDisk:               true,
		},
		Lima: LimaConfig{
			VMNames: []string{"colima", "unified"},
		},
		ICloud: ICloudConfig{
			EvictAfterDays: 30,
			ExcludePaths:   []string{},
			MinFileSizeMB:  10,
		},
		Notify: NotifyConfig{
			Enabled: false,
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
