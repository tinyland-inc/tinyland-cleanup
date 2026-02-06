package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	// Test basic defaults
	if cfg.PollInterval != 60 {
		t.Errorf("expected PollInterval=60, got %d", cfg.PollInterval)
	}

	// Test thresholds
	if cfg.Thresholds.Warning != 80 {
		t.Errorf("expected Warning=80, got %d", cfg.Thresholds.Warning)
	}
	if cfg.Thresholds.Moderate != 85 {
		t.Errorf("expected Moderate=85, got %d", cfg.Thresholds.Moderate)
	}
	if cfg.Thresholds.Aggressive != 90 {
		t.Errorf("expected Aggressive=90, got %d", cfg.Thresholds.Aggressive)
	}
	if cfg.Thresholds.Critical != 95 {
		t.Errorf("expected Critical=95, got %d", cfg.Thresholds.Critical)
	}

	// Test enable flags
	if !cfg.Enable.Cache {
		t.Error("expected Cache enabled")
	}
	if !cfg.Enable.NixGC {
		t.Error("expected NixGC enabled")
	}
	if !cfg.Enable.Docker {
		t.Error("expected Docker enabled")
	}

	// Platform-specific defaults
	if runtime.GOOS == "darwin" {
		if !cfg.Enable.Lima {
			t.Error("expected Lima enabled on Darwin")
		}
		if !cfg.Enable.Homebrew {
			t.Error("expected Homebrew enabled on Darwin")
		}
		if !cfg.Enable.IOSSimulator {
			t.Error("expected IOSSimulator enabled on Darwin")
		}
	}
}

func TestLoadConfigNonExistent(t *testing.T) {
	cfg, err := LoadConfig("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("unexpected error for non-existent file: %v", err)
	}
	// Should return defaults
	if cfg.PollInterval != 60 {
		t.Errorf("expected default PollInterval=60, got %d", cfg.PollInterval)
	}
}

func TestLoadConfigEmpty(t *testing.T) {
	// Create temp file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Empty file should use defaults
	if cfg.PollInterval != 60 {
		t.Errorf("expected default PollInterval=60, got %d", cfg.PollInterval)
	}
}

func TestLoadConfigPartial(t *testing.T) {
	// Create temp file with partial config
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	content := `
poll_interval: 30
thresholds:
  warning: 70
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should use specified values
	if cfg.PollInterval != 30 {
		t.Errorf("expected PollInterval=30, got %d", cfg.PollInterval)
	}
	if cfg.Thresholds.Warning != 70 {
		t.Errorf("expected Warning=70, got %d", cfg.Thresholds.Warning)
	}

	// Should use defaults for unspecified values
	if cfg.Thresholds.Moderate != 85 {
		t.Errorf("expected default Moderate=85, got %d", cfg.Thresholds.Moderate)
	}
}

func TestSaveConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "subdir", "config.yaml")

	cfg := DefaultConfig()
	cfg.PollInterval = 120

	if err := SaveConfig(cfg, configPath); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	// Reload and verify
	loaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load saved config: %v", err)
	}

	if loaded.PollInterval != 120 {
		t.Errorf("expected PollInterval=120, got %d", loaded.PollInterval)
	}
}

func TestLoadConfigInvalid(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	content := `
poll_interval: "not a number"
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(configPath)
	if err == nil {
		t.Error("expected error for invalid config")
	}
}

func TestDevArtifactsConfigDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if len(cfg.DevArtifacts.ScanPaths) == 0 {
		t.Error("DevArtifacts.ScanPaths should have default paths")
	}
	if !cfg.DevArtifacts.NodeModules {
		t.Error("DevArtifacts.NodeModules should be true by default")
	}
	if !cfg.DevArtifacts.PythonVenvs {
		t.Error("DevArtifacts.PythonVenvs should be true by default")
	}
	if !cfg.DevArtifacts.RustTargets {
		t.Error("DevArtifacts.RustTargets should be true by default")
	}
	if !cfg.DevArtifacts.GoBuildCache {
		t.Error("DevArtifacts.GoBuildCache should be true by default")
	}
	if !cfg.DevArtifacts.HaskellCache {
		t.Error("DevArtifacts.HaskellCache should be true by default")
	}
	if cfg.DevArtifacts.LMStudioModels {
		t.Error("DevArtifacts.LMStudioModels should be false by default (opt-in)")
	}
}

func TestAPFSConfigDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if !cfg.APFS.ThinEnabled {
		t.Error("APFS.ThinEnabled should be true by default")
	}
	if cfg.APFS.MaxThinGB != 50 {
		t.Errorf("APFS.MaxThinGB should be 50, got %d", cfg.APFS.MaxThinGB)
	}
	if cfg.APFS.KeepRecentDays != 1 {
		t.Errorf("APFS.KeepRecentDays should be 1, got %d", cfg.APFS.KeepRecentDays)
	}
	if !cfg.APFS.DeleteOSUpdates {
		t.Error("APFS.DeleteOSUpdates should be true by default")
	}
}

func TestEnableFlagsNewPlugins(t *testing.T) {
	cfg := DefaultConfig()

	if !cfg.Enable.DevArtifacts {
		t.Error("Enable.DevArtifacts should be true by default")
	}
	if runtime.GOOS == "darwin" && !cfg.Enable.APFSSnapshots {
		t.Error("Enable.APFSSnapshots should be true on Darwin")
	}
}

func TestLimaCompactDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Lima.CompactOffline {
		t.Error("Lima.CompactOffline should be false by default (opt-in)")
	}
}

func TestPodmanCompactDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Podman.CompactDiskOffline {
		t.Error("Podman.CompactDiskOffline should be false by default (opt-in)")
	}
}

func TestLoadConfigWithNewFields(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	content := `
enable:
  dev_artifacts: true
  apfs_snapshots: true
dev_artifacts:
  scan_paths:
    - ~/git
    - ~/src
  node_modules: true
  python_venvs: true
  rust_targets: false
  go_build_cache: true
  haskell_cache: false
  lmstudio_models: true
  protect_paths:
    - ~/git/important
apfs:
  thin_enabled: true
  max_thin_gb: 30
  keep_recent_days: 2
  delete_os_updates: false
lima:
  compact_offline: true
podman:
  compact_disk_offline: true
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !cfg.Enable.DevArtifacts {
		t.Error("Enable.DevArtifacts should be true")
	}
	if !cfg.Enable.APFSSnapshots {
		t.Error("Enable.APFSSnapshots should be true")
	}
	if len(cfg.DevArtifacts.ScanPaths) != 2 {
		t.Errorf("expected 2 scan paths, got %d", len(cfg.DevArtifacts.ScanPaths))
	}
	if cfg.DevArtifacts.RustTargets {
		t.Error("DevArtifacts.RustTargets should be false per config")
	}
	if !cfg.DevArtifacts.LMStudioModels {
		t.Error("DevArtifacts.LMStudioModels should be true per config")
	}
	if len(cfg.DevArtifacts.ProtectPaths) != 1 {
		t.Errorf("expected 1 protect path, got %d", len(cfg.DevArtifacts.ProtectPaths))
	}
	if cfg.APFS.MaxThinGB != 30 {
		t.Errorf("APFS.MaxThinGB should be 30, got %d", cfg.APFS.MaxThinGB)
	}
	if cfg.APFS.KeepRecentDays != 2 {
		t.Errorf("APFS.KeepRecentDays should be 2, got %d", cfg.APFS.KeepRecentDays)
	}
	if cfg.APFS.DeleteOSUpdates {
		t.Error("APFS.DeleteOSUpdates should be false per config")
	}
	if !cfg.Lima.CompactOffline {
		t.Error("Lima.CompactOffline should be true per config")
	}
	if !cfg.Podman.CompactDiskOffline {
		t.Error("Podman.CompactDiskOffline should be true per config")
	}
}
