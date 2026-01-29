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
