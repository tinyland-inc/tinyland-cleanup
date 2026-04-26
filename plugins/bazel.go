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
	"syscall"
	"time"

	"github.com/Jesssullivan/tinyland-cleanup/config"
)

const bazelGiB = int64(1024 * 1024 * 1024)

var bazelCommandPattern = regexp.MustCompile(`\b(bazel|bazelisk)\s+(build|test|run|coverage|query|sync|fetch|clean|mobile-install|aquery|cquery)\b`)

// BazelPlugin reports Bazel output bases and cache tiers.
type BazelPlugin struct{}

type bazelCandidate struct {
	Type      string
	Name      string
	Path      string
	ModTime   time.Time
	Logical   int64
	Physical  int64
	Active    bool
	Protected bool
	Action    string
	Reason    string
}

// NewBazelPlugin creates a new Bazel cleanup plugin.
func NewBazelPlugin() *BazelPlugin {
	return &BazelPlugin{}
}

// Name returns the plugin identifier.
func (p *BazelPlugin) Name() string {
	return "bazel"
}

// Description returns the plugin description.
func (p *BazelPlugin) Description() string {
	return "Plans Bazel output base, repository cache, disk cache, and Bazelisk cleanup"
}

// SupportedPlatforms returns supported platforms (all).
func (p *BazelPlugin) SupportedPlatforms() []string {
	return nil
}

// Enabled checks if Bazel cleanup planning is enabled.
func (p *BazelPlugin) Enabled(cfg *config.Config) bool {
	return cfg.Enable.Bazel
}

// PlanCleanup returns a dry-run plan without mutating Bazel state.
func (p *BazelPlugin) PlanCleanup(ctx context.Context, level CleanupLevel, cfg *config.Config, logger *slog.Logger) CleanupPlan {
	_ = logger

	plan := CleanupPlan{
		Plugin:   p.Name(),
		Level:    level.String(),
		Summary:  "Bazel cache and output-base review plan",
		WouldRun: true,
		Steps: []string{
			"Discover Bazel output bases, repository caches, disk caches, and Bazelisk downloads",
			"Measure logical and physical bytes without following repo-local bazel-* symlinks",
			"Protect active output bases, protected workspace output bases, and newest output bases",
			"Report cleanup candidates with reasons before enabling deletion",
		},
		Metadata: map[string]string{
			"cleanup_level":                    level.String(),
			"max_total_gb":                     strconv.Itoa(cfg.Bazel.MaxTotalGB),
			"keep_recent_output_bases":         strconv.Itoa(cfg.Bazel.KeepRecentOutputBases),
			"stale_after":                      cfg.Bazel.StaleAfter,
			"critical_stale_after":             cfg.Bazel.CriticalStaleAfter,
			"allow_stop_idle_servers":          strconv.FormatBool(cfg.Bazel.AllowStopIdleServers),
			"allow_delete_active_output_bases": strconv.FormatBool(cfg.Bazel.AllowDeleteActiveOutputBases),
		},
	}

	home, _ := os.UserHomeDir()
	activeReasons, activeErr := p.activeBazelProcesses(ctx)
	if activeErr != nil {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("could not inspect active Bazel processes: %v", activeErr))
	} else if len(activeReasons) > 0 {
		plan.Metadata["active_bazel_processes"] = strings.Join(activeReasons, ", ")
	}

	candidates := p.discoverCandidates(home, cfg.Bazel)
	targets, totalPhysical := bazelPlanTargets(candidates, cfg.Bazel, level, time.Now(), len(activeReasons) > 0)
	plan.Targets = targets
	plan.EstimatedBytesFreed = bazelEstimatedCandidateBytes(targets)
	plan.Metadata["target_count"] = strconv.Itoa(len(targets))
	plan.Metadata["total_physical_bytes"] = strconv.FormatInt(totalPhysical, 10)

	if cfg.Bazel.MaxTotalGB > 0 && totalPhysical > int64(cfg.Bazel.MaxTotalGB)*bazelGiB {
		plan.Warnings = append(plan.Warnings, "detected Bazel cache footprint exceeds configured review budget")
	}
	plan.Warnings = append(plan.Warnings, "Bazel cleanup is planning-only in this slice; deletion and permission normalization remain follow-up work")
	plan.Warnings = append(plan.Warnings, "Bazel byte counts use bounded top-level allocation estimates so dry-run stays responsive on very large output bases")

	return plan
}

// Cleanup is intentionally planning-only until Bazel deletion policy is proven.
func (p *BazelPlugin) Cleanup(ctx context.Context, level CleanupLevel, cfg *config.Config, logger *slog.Logger) CleanupResult {
	_ = ctx
	_ = cfg
	logger.Info("Bazel cleanup is planning-only; use --dry-run --output json to review candidates", "level", level.String())
	return CleanupResult{Plugin: p.Name(), Level: level}
}

func (p *BazelPlugin) activeBazelProcesses(ctx context.Context) ([]string, error) {
	psCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(psCtx, "ps", "-axo", "comm=,args=")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return bazelBusyProcessReasons(string(output)), nil
}

func (p *BazelPlugin) discoverCandidates(home string, cfg config.BazelConfig) []bazelCandidate {
	var candidates []bazelCandidate
	seen := map[string]bool{}

	add := func(candidate bazelCandidate) {
		if candidate.Path == "" || seen[candidate.Path] {
			return
		}
		seen[candidate.Path] = true
		candidates = append(candidates, candidate)
	}

	for _, root := range cfg.Roots {
		expanded := expandHome(root, home)
		for _, candidate := range discoverBazelRootCandidates(expanded) {
			add(candidate)
		}
	}

	if cfg.BazeliskCache != "" {
		for _, candidate := range discoverBazeliskCandidates(expandHome(cfg.BazeliskCache, home)) {
			add(candidate)
		}
	}

	protectedOutputBases := outputBasesProtectedByWorkspaces(cfg.ProtectWorkspaces, home)
	for i := range candidates {
		if candidates[i].Type == "output_base" && protectedOutputBases[candidates[i].Path] {
			candidates[i].Protected = true
			candidates[i].Reason = "reachable from configured protected workspace"
		}
		if candidates[i].Type == "output_base" && bazelOutputBaseHasActiveLock(candidates[i].Path) {
			candidates[i].Active = true
		}
	}

	return candidates
}

func discoverBazelRootCandidates(root string) []bazelCandidate {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil
	}

	var candidates []bazelCandidate
	if isBazelOutputBase(root) {
		return []bazelCandidate{newBazelCandidate("output_base", filepath.Base(root), root, info.ModTime())}
	}

	base := filepath.Base(root)
	if strings.HasPrefix(base, "_bazel_") {
		return discoverBazelOutputBases(root)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name())
		switch {
		case strings.HasPrefix(entry.Name(), "_bazel_"):
			candidates = append(candidates, discoverBazelOutputBases(path)...)
		case entry.Name() == "repository_cache":
			if info, err := entry.Info(); err == nil {
				candidates = append(candidates, newBazelCandidate("repository_cache", entry.Name(), path, info.ModTime()))
			}
		case entry.Name() == "disk_cache":
			if info, err := entry.Info(); err == nil {
				candidates = append(candidates, newBazelCandidate("disk_cache", entry.Name(), path, info.ModTime()))
			}
		}
	}

	return candidates
}

func discoverBazelOutputBases(outputUserRoot string) []bazelCandidate {
	entries, err := os.ReadDir(outputUserRoot)
	if err != nil {
		return nil
	}

	var candidates []bazelCandidate
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(outputUserRoot, entry.Name())
		if !isBazelOutputBase(path) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, newBazelCandidate("output_base", entry.Name(), path, info.ModTime()))
	}
	return candidates
}

func discoverBazeliskCandidates(root string) []bazelCandidate {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	var candidates []bazelCandidate
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		candidates = append(candidates, newBazelCandidate("bazelisk", entry.Name(), path, info.ModTime()))
	}
	return candidates
}

func newBazelCandidate(candidateType, name, path string, modTime time.Time) bazelCandidate {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	logical, physical := estimateBazelCandidateBytes(path)
	return bazelCandidate{
		Type:     candidateType,
		Name:     name,
		Path:     path,
		ModTime:  modTime,
		Logical:  logical,
		Physical: physical,
	}
}

func estimateBazelCandidateBytes(path string) (int64, int64) {
	var logical int64
	var physical int64

	if info, err := os.Stat(path); err == nil {
		logical += info.Size()
		if allocated, err := getFileAllocatedBytes(path); err == nil {
			physical += allocated
		}
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return logical, physical
	}
	for _, entry := range entries {
		entryPath := filepath.Join(path, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		logical += info.Size()
		if allocated, err := getFileAllocatedBytes(entryPath); err == nil {
			physical += allocated
		}
	}

	return logical, physical
}

func isBazelOutputBase(path string) bool {
	required := []string{"execroot", "action_cache", "server"}
	for _, name := range required {
		if !pathExistsAndIsDir(filepath.Join(path, name)) {
			return false
		}
	}
	return true
}

func bazelOutputBaseHasActiveLock(path string) bool {
	if bazelServerPIDIsAlive(filepath.Join(path, "server", "server.pid")) {
		return true
	}
	if info, err := os.Stat(filepath.Join(path, "lock")); err == nil {
		return time.Since(info.ModTime()) < 15*time.Minute
	}
	return false
}

func bazelServerPIDIsAlive(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return false
	}
	err = syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if err == syscall.EPERM {
		return true
	}
	return false
}

func outputBasesProtectedByWorkspaces(workspaces []string, home string) map[string]bool {
	protected := map[string]bool{}
	for _, workspace := range workspaces {
		expanded := expandHome(workspace, home)
		for _, linkName := range []string{"bazel-bin", "bazel-out", "bazel-testlogs", "bazel-" + filepath.Base(expanded)} {
			target, err := filepath.EvalSymlinks(filepath.Join(expanded, linkName))
			if err != nil {
				continue
			}
			if outputBase := outputBaseFromSymlinkTarget(target); outputBase != "" {
				protected[outputBase] = true
			}
		}
	}
	return protected
}

func outputBaseFromSymlinkTarget(target string) string {
	target = filepath.Clean(target)
	parts := strings.Split(target, string(os.PathSeparator))
	for i, part := range parts {
		if part != "execroot" || i == 0 {
			continue
		}
		outputBase := strings.Join(parts[:i], string(os.PathSeparator))
		if strings.HasPrefix(target, string(os.PathSeparator)) {
			outputBase = string(os.PathSeparator) + outputBase
		}
		return filepath.Clean(outputBase)
	}
	return ""
}

func bazelPlanTargets(candidates []bazelCandidate, cfg config.BazelConfig, level CleanupLevel, now time.Time, globalActive bool) ([]CleanupTarget, int64) {
	staleAfter := parseNixPolicyDuration(cfg.StaleAfter, 14*24*time.Hour)
	if level == LevelCritical {
		staleAfter = parseNixPolicyDuration(cfg.CriticalStaleAfter, 3*24*time.Hour)
	}

	recentOutputBases := newestBazelOutputBases(candidates, cfg.KeepRecentOutputBases)
	var totalPhysical int64
	targets := make([]CleanupTarget, 0, len(candidates))
	for _, candidate := range candidates {
		totalPhysical += candidate.Physical
		target := bazelTargetForCandidate(candidate, staleAfter, now, recentOutputBases[candidate.Path], globalActive, level, cfg)
		targets = append(targets, target)
	}

	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Bytes == targets[j].Bytes {
			return targets[i].Path < targets[j].Path
		}
		return targets[i].Bytes > targets[j].Bytes
	})
	return targets, totalPhysical
}

func newestBazelOutputBases(candidates []bazelCandidate, keep int) map[string]bool {
	protected := map[string]bool{}
	if keep <= 0 {
		return protected
	}

	var outputBases []bazelCandidate
	for _, candidate := range candidates {
		if candidate.Type == "output_base" {
			outputBases = append(outputBases, candidate)
		}
	}

	sort.Slice(outputBases, func(i, j int) bool {
		return outputBases[i].ModTime.After(outputBases[j].ModTime)
	})
	for idx, candidate := range outputBases {
		if idx >= keep {
			break
		}
		protected[candidate.Path] = true
	}
	return protected
}

func bazelTargetForCandidate(candidate bazelCandidate, staleAfter time.Duration, now time.Time, protectedByRecent bool, globalActive bool, level CleanupLevel, cfg config.BazelConfig) CleanupTarget {
	active := candidate.Active || globalActive
	protected := candidate.Protected || protectedByRecent || (active && !cfg.AllowDeleteActiveOutputBases)
	action := "review"
	reason := "Bazel cache candidate requires operator review"

	switch {
	case candidate.Protected:
		action = "keep"
		reason = candidate.Reason
	case protectedByRecent:
		action = "keep"
		reason = "within configured newest output-base retention"
	case active && !cfg.AllowDeleteActiveOutputBases:
		action = "keep"
		reason = "active Bazel process or output-base lock detected"
	case level == LevelWarning:
		action = "review"
		reason = "warning level reports Bazel cache footprint only"
	case candidate.Type != "output_base":
		action = "review_cache_budget"
		reason = "cache tier budget enforcement is not enabled yet"
	case candidate.ModTime.After(now.Add(-staleAfter)):
		action = "keep"
		protected = true
		reason = "newer than configured Bazel stale threshold"
	default:
		action = "delete_output_base"
		reason = "stale inactive output base outside retention policy"
	}

	return CleanupTarget{
		Type:      candidate.Type,
		Name:      candidate.Name,
		Path:      candidate.Path,
		Bytes:     candidate.Physical,
		Active:    active,
		Protected: protected,
		Action:    action,
		Reason:    reason,
	}
}

func bazelEstimatedCandidateBytes(targets []CleanupTarget) int64 {
	var total int64
	for _, target := range targets {
		if strings.HasPrefix(target.Action, "delete_") && !target.Protected && !target.Active {
			total += target.Bytes
		}
	}
	return total
}

func bazelBusyProcessReasons(output string) []string {
	seen := map[string]bool{}
	var reasons []string
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.ToLower(line))
		if len(fields) < 2 {
			continue
		}
		command := filepath.Base(fields[0])
		arg0 := filepath.Base(fields[1])
		if command != "bazel" && command != "bazelisk" && arg0 != "bazel" && arg0 != "bazelisk" {
			continue
		}
		normalized := strings.Join(fields[1:], " ")
		matches := bazelCommandPattern.FindStringSubmatch(normalized)
		if len(matches) < 3 {
			continue
		}
		reason := matches[1] + " " + matches[2]
		if !seen[reason] {
			seen[reason] = true
			reasons = append(reasons, reason)
		}
	}
	sort.Strings(reasons)
	return reasons
}
