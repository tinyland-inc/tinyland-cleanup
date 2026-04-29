package plugins

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/Jesssullivan/tinyland-cleanup/config"
)

func TestParseBuildKitDUSummary(t *testing.T) {
	output := `
ID									RECLAIMABLE	SIZE
example								true		10.1GB
Reclaimable:	23.60GB
Total:		24.00GB
`
	reclaimable, total := parseBuildKitDUSummary(output)

	expectedReclaimable := buildKitTestBytes(23.60)
	expectedTotal := buildKitTestBytes(24.00)
	if reclaimable != expectedReclaimable {
		t.Fatalf("expected reclaimable %d, got %d", expectedReclaimable, reclaimable)
	}
	if total != expectedTotal {
		t.Fatalf("expected total %d, got %d", expectedTotal, total)
	}
}

func TestParseBuildKitPruneSummary(t *testing.T) {
	output := `
reclaimed records...
Total:	12.90GB
`
	expected := buildKitTestBytes(12.90)
	if got := parseBuildKitPruneSummary(output); got != expected {
		t.Fatalf("expected %d, got %d", expected, got)
	}
}

func buildKitTestBytes(value float64) int64 {
	return int64(value * float64(podmanCompactionGiB))
}

func TestBuildPodmanBuildKitCachePlanEligible(t *testing.T) {
	plan := buildPodmanBuildKitCachePlan(podmanBuildKitCachePlanInput{
		Enabled:          true,
		ContainerID:      "abc123",
		ContainerName:    "buildx_buildkit_default",
		KeepDuration:     "24h",
		KeepStorageMB:    8192,
		MinReclaimBytes:  4 * podmanCompactionGiB,
		ReclaimableBytes: 23 * podmanCompactionGiB,
		TotalBytes:       24 * podmanCompactionGiB,
	})

	if !plan.CanPrune {
		t.Fatalf("expected BuildKit cache plan to be eligible, got %q", plan.SkipReason)
	}
	if plan.SkipReason != "" {
		t.Fatalf("expected empty skip reason, got %q", plan.SkipReason)
	}

	targets := podmanBuildKitCacheTargets(plan)
	if len(targets) != 1 {
		t.Fatalf("expected one target, got %#v", targets)
	}
	target := targets[0]
	if target.Type != "podman_buildkit_cache" || target.Action != "prune_buildkit_cache" {
		t.Fatalf("unexpected BuildKit target: %#v", target)
	}
	if target.Protected {
		t.Fatalf("eligible BuildKit target should not be protected: %#v", target)
	}
	if target.Reclaim != CleanupReclaimDeferred || target.HostReclaimsSpace == nil || *target.HostReclaimsSpace {
		t.Fatalf("BuildKit target should be deferred host reclaim: %#v", target)
	}
}

func TestPodmanBuildKitPruneArgsUseNumericKeepStorage(t *testing.T) {
	plan := podmanBuildKitCachePlan{
		ContainerID:   "abc123",
		KeepDuration:  "24h",
		KeepStorageMB: 8192,
	}

	args := podmanBuildKitPruneArgs(plan)
	expected := []string{
		"exec", "abc123",
		"buildctl", "prune",
		"--keep-duration", "24h",
		"--keep-storage", "8192",
	}
	if !reflect.DeepEqual(args, expected) {
		t.Fatalf("unexpected BuildKit prune args: %#v", args)
	}
	for _, arg := range args {
		if strings.Contains(arg, "MB") {
			t.Fatalf("buildctl keep-storage expects a numeric MB value, got arg %q in %#v", arg, args)
		}
	}
}

func TestPodmanCriticalPlanProtectsSystemPruneByDefault(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Podman.BuildKitPrune = false
	p := &PodmanPlugin{environment: &PodmanEnvironment{Runtime: "podman"}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	plan := p.PlanCleanup(context.Background(), LevelCritical, cfg, logger)
	if plan.Metadata["critical_system_prune_enabled"] != "false" {
		t.Fatalf("expected critical system prune disabled metadata, got %#v", plan.Metadata)
	}

	target := findPodmanBuildKitTestTarget(t, plan.Targets, "podman_system_prune")
	if target.Action != "protect_system_prune" || !target.Protected {
		t.Fatalf("critical system prune should be protected by default: %#v", target)
	}
	if target.Reclaim != CleanupReclaimNone {
		t.Fatalf("protected system prune should not claim reclaim: %#v", target)
	}
	if !strings.Contains(strings.Join(plan.Steps, "\n"), "critical_system_prune=false") {
		t.Fatalf("plan steps should explain why broad prune is skipped: %#v", plan.Steps)
	}
}

func TestPodmanCriticalPlanAllowsOptInSystemPrune(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Podman.BuildKitPrune = false
	cfg.Podman.CriticalSystemPrune = true
	p := &PodmanPlugin{environment: &PodmanEnvironment{Runtime: "podman"}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	plan := p.PlanCleanup(context.Background(), LevelCritical, cfg, logger)
	if plan.Metadata["critical_system_prune_enabled"] != "true" {
		t.Fatalf("expected critical system prune enabled metadata, got %#v", plan.Metadata)
	}

	target := findPodmanBuildKitTestTarget(t, plan.Targets, "podman_system_prune")
	if target.Action != "run_system_prune" || target.Protected {
		t.Fatalf("critical system prune should be opt-in runnable: %#v", target)
	}
	if target.Reclaim != CleanupReclaimDeferred {
		t.Fatalf("system prune reclaim should stay deferred in dry-run: %#v", target)
	}
	if !strings.Contains(strings.Join(plan.Steps, "\n"), "Run full Podman system prune with volumes") {
		t.Fatalf("plan steps should include broad prune when opted in: %#v", plan.Steps)
	}
}

func findPodmanBuildKitTestTarget(t *testing.T, targets []CleanupTarget, targetType string) CleanupTarget {
	t.Helper()
	for _, target := range targets {
		if target.Type == targetType {
			return target
		}
	}
	t.Fatalf("target %q not found in %#v", targetType, targets)
	return CleanupTarget{}
}

func TestBuildPodmanBuildKitCachePlanBelowMinimum(t *testing.T) {
	plan := buildPodmanBuildKitCachePlan(podmanBuildKitCachePlanInput{
		Enabled:          true,
		ContainerID:      "abc123",
		ContainerName:    "buildx_buildkit_default",
		MinReclaimBytes:  4 * podmanCompactionGiB,
		ReclaimableBytes: 2 * podmanCompactionGiB,
	})

	if plan.CanPrune {
		t.Fatal("expected below-minimum BuildKit cache to be protected")
	}
	if plan.SkipReason != "below_minimum_buildkit_reclaim" {
		t.Fatalf("expected below_minimum_buildkit_reclaim, got %q", plan.SkipReason)
	}
	target := podmanBuildKitCacheTargets(plan)[0]
	if target.Action != "protect_buildkit_cache" || !target.Protected {
		t.Fatalf("expected protected BuildKit target: %#v", target)
	}
	if target.Reclaim != CleanupReclaimNone {
		t.Fatalf("protected BuildKit target should not count reclaim: %#v", target)
	}
}

func TestBuildPodmanBuildKitCachePlanInspectionFailure(t *testing.T) {
	plan := buildPodmanBuildKitCachePlan(podmanBuildKitCachePlanInput{
		Enabled:         true,
		InspectionError: "buildctl missing",
	})

	if plan.CanPrune {
		t.Fatal("expected inspection failure to block BuildKit prune")
	}
	if plan.SkipReason != "buildkit_cache_inspection_failed" {
		t.Fatalf("expected buildkit_cache_inspection_failed, got %q", plan.SkipReason)
	}
	target := podmanBuildKitCacheTargets(plan)[0]
	if target.Action != "protect_buildkit_inspection" || !target.Protected {
		t.Fatalf("expected protected inspection target: %#v", target)
	}
}
