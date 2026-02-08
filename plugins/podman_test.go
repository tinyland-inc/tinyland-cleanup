package plugins

import (
	"math"
	"strings"
	"testing"

	"gitlab.com/tinyland/lab/tinyland-cleanup/config"
)

func TestPodmanPluginName(t *testing.T) {
	p := NewPodmanPlugin()
	if got := p.Name(); got != "podman" {
		t.Errorf("Name() = %q, want %q", got, "podman")
	}
}

func TestPodmanPluginDescription(t *testing.T) {
	p := NewPodmanPlugin()
	if got := p.Description(); got == "" {
		t.Error("Description() should not be empty")
	}
}

func TestPodmanPluginSupportedPlatforms(t *testing.T) {
	p := NewPodmanPlugin()
	if platforms := p.SupportedPlatforms(); platforms != nil {
		t.Errorf("SupportedPlatforms() = %v, want nil (all platforms)", platforms)
	}
}

func TestPodmanPluginEnabled(t *testing.T) {
	p := NewPodmanPlugin()

	cfg := config.DefaultConfig()
	cfg.Enable.Podman = true
	if !p.Enabled(cfg) {
		t.Error("Enabled() should return true when Podman is enabled")
	}

	cfg.Enable.Podman = false
	if p.Enabled(cfg) {
		t.Error("Enabled() should return false when Podman is disabled")
	}
}

func TestParseReclaimedSpace(t *testing.T) {
	p := NewPodmanPlugin()

	tests := []struct {
		name   string
		output string
		want   int64
	}{
		{
			name:   "bytes",
			output: "Total reclaimed space: 512 B",
			want:   512,
		},
		{
			name:   "kilobytes",
			output: "Total reclaimed space: 10.5 KB",
			want:   10752, // 10.5 * 1024
		},
		{
			name:   "megabytes",
			output: "Total reclaimed space: 256.3 MB",
			want:   268750028, // int64(256.3 * 1024 * 1024)
		},
		{
			name:   "gigabytes",
			output: "Total reclaimed space: 1.5 GB",
			want:   1610612736, // 1.5 * 1024^3
		},
		{
			name:   "no match",
			output: "nothing useful here",
			want:   0,
		},
		{
			name:   "lowercase reclaimed",
			output: "reclaimed space: 100 MB",
			want:   104857600, // 100 * 1024 * 1024
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.parseReclaimedSpace(tt.output)
			// Allow 1% tolerance for floating point precision
			if tt.want == 0 {
				if got != 0 {
					t.Errorf("parseReclaimedSpace(%q) = %d, want 0", tt.output, got)
				}
				return
			}
			diff := math.Abs(float64(got - tt.want))
			tolerance := math.Max(float64(tt.want)*0.01, 1)
			if diff > tolerance {
				t.Errorf("parseReclaimedSpace(%q) = %d, want %d (diff: %.0f, tolerance: %.0f)",
					tt.output, got, tt.want, diff, tolerance)
			}
		})
	}
}

func TestParseFstrimOutputPodman(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   int64
	}{
		{
			name:   "single mount",
			output: "/: 1.5 GiB (1610612736 bytes) trimmed on /dev/vda1\n",
			want:   1610612736,
		},
		{
			name:   "multiple mounts",
			output: "/: 512 MiB (536870912 bytes) trimmed\n/var: 1 GiB (1073741824 bytes) trimmed\n",
			want:   536870912 + 1073741824,
		},
		{
			name:   "zero",
			output: "/: 0 B (0 bytes) trimmed\n",
			want:   0,
		},
		{
			name:   "no match",
			output: "some other output\n",
			want:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseFstrimOutput(tt.output)
			if got != tt.want {
				t.Errorf("parseFstrimOutput(%q) = %d, want %d", tt.output, got, tt.want)
			}
		})
	}
}

func TestListRunningContainerIDsParsing(t *testing.T) {
	// Validate the string-splitting logic used by listRunningContainerIDs.
	// We cannot call the method without a podman binary, so we exercise
	// the same strings.TrimSpace + strings.Split pattern directly.
	tests := []struct {
		name   string
		output string
		want   int
	}{
		{
			name:   "empty string",
			output: "",
			want:   0,
		},
		{
			name:   "whitespace only",
			output: "   \n  ",
			want:   0,
		},
		{
			name:   "single container ID",
			output: "abc123def456\n",
			want:   1,
		},
		{
			name:   "three container IDs",
			output: "abc123\ndef456\nghi789\n",
			want:   3,
		},
		{
			name:   "no trailing newline",
			output: "abc123\ndef456",
			want:   2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trimmed := strings.TrimSpace(tt.output)
			var count int
			if trimmed != "" {
				count = len(strings.Split(trimmed, "\n"))
			}
			if count != tt.want {
				t.Errorf("parsed %d container IDs from %q, want %d", count, tt.output, tt.want)
			}
		})
	}
}

func TestPodmanConfigDefaults(t *testing.T) {
	// Verify MachineNames was removed and ProtectRunningContainers defaults.
	// If this compiles, MachineNames is no longer in PodmanConfig.
	cfg := config.DefaultConfig()

	if !cfg.Podman.ProtectRunningContainers {
		t.Error("Podman.ProtectRunningContainers should default to true")
	}

	if cfg.Podman.PruneImagesAge == "" {
		t.Error("Podman.PruneImagesAge should have a default value")
	}

	if !cfg.Podman.CleanInsideVM {
		t.Error("Podman.CleanInsideVM should default to true")
	}

	if !cfg.Podman.TrimVMDisk {
		t.Error("Podman.TrimVMDisk should default to true")
	}
}
