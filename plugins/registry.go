package plugins

import (
	"context"
	"time"

	"gitlab.com/tinyland/lab/tinyland-cleanup/config"
)

// Resource group constants for concurrent execution.
// Plugins in the same group run serially; different groups run in parallel.
const (
	GroupContainerDocker = "container-docker"
	GroupContainerPodman = "container-podman"
	GroupVMControl       = "vm-control"
	GroupNixStore        = "nix-store"
	GroupFilesystemScan  = "filesystem-scan"
	GroupPackageManager  = "package-manager"
	GroupSystemDarwin    = "system-darwin"
	GroupKubernetes      = "kubernetes"
	GroupDefault         = "default"
)

// pluginGroupMap maps plugin names to their resource groups.
var pluginGroupMap = map[string]string{
	"docker":         GroupContainerDocker,
	"gitlab-runner":  GroupContainerDocker,
	"podman":         GroupContainerPodman,
	"lima":           GroupVMControl,
	"nix":            GroupNixStore,
	"cache":          GroupFilesystemScan,
	"dev-artifacts":  GroupFilesystemScan,
	"github-runner":  GroupFilesystemScan,
	"homebrew":       GroupPackageManager,
	"yum":            GroupPackageManager,
	"apfs-snapshots": GroupSystemDarwin,
	"icloud":         GroupSystemDarwin,
	"photos":         GroupSystemDarwin,
	"ios-simulator":  GroupSystemDarwin,
	"xcode":          GroupSystemDarwin,
	"etcd":           GroupKubernetes,
	"rke2":           GroupKubernetes,
}

// GetResourceGroup returns the resource group for a plugin.
// If the plugin implements PluginV2, its ResourceGroup() method is used.
// Otherwise, falls back to the static map, then "default".
func GetResourceGroup(p Plugin) string {
	if v2, ok := p.(PluginV2); ok {
		return v2.ResourceGroup()
	}
	if group, ok := pluginGroupMap[p.Name()]; ok {
		return group
	}
	return GroupDefault
}

// GetEstimatedDuration returns the estimated duration for a plugin.
// If the plugin implements PluginV2, its EstimatedDuration() method is used.
// Otherwise returns 30 seconds as a default.
func GetEstimatedDuration(p Plugin) time.Duration {
	if v2, ok := p.(PluginV2); ok {
		return v2.EstimatedDuration()
	}
	return 30 * time.Second
}

// RunPreflightCheck runs the preflight check for a plugin if it implements PluginV2.
// Returns nil for plugins that don't implement PluginV2.
func RunPreflightCheck(ctx context.Context, p Plugin, cfg *config.Config) error {
	if v2, ok := p.(PluginV2); ok {
		return v2.PreflightCheck(ctx, cfg)
	}
	return nil
}
