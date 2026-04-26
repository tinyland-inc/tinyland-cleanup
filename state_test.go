package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Jesssullivan/tinyland-cleanup/plugins"
)

func TestCleanupStateRoundTripAndCooldown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	state := newCleanupState()
	state.recordPluginRun("nix", plugins.LevelModerate, now.Add(-5*time.Minute), plugins.CleanupResult{
		Plugin:       "nix",
		Level:        plugins.LevelModerate,
		BytesFreed:   42,
		ItemsCleaned: 2,
	})

	if err := saveCleanupState(path, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadCleanupState(path)
	if err != nil {
		t.Fatal(err)
	}

	remaining := loaded.cooldownRemaining("nix", plugins.LevelModerate, now, 30*time.Minute)
	if remaining != 25*time.Minute {
		t.Fatalf("remaining = %s, want 25m", remaining)
	}
	if loaded.cooldownRemaining("nix", plugins.LevelAggressive, now, 30*time.Minute) != 0 {
		t.Fatal("higher cleanup level should bypass prior lower-level cooldown")
	}
}

func TestLoadCleanupStateMissingFile(t *testing.T) {
	state, err := loadCleanupState(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if state.Version != cleanupStateVersion {
		t.Fatalf("version = %d, want %d", state.Version, cleanupStateVersion)
	}
	if len(state.Plugins) != 0 {
		t.Fatalf("expected empty plugin state, got %#v", state.Plugins)
	}
}
