package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/Jesssullivan/tinyland-cleanup/plugins"
)

const cleanupStateVersion = 1

type cleanupState struct {
	Version int                          `json:"version"`
	Plugins map[string]pluginStateRecord `json:"plugins"`
}

type pluginStateRecord struct {
	LastRun          string `json:"last_run"`
	LastLevel        string `json:"last_level"`
	LastLevelValue   int    `json:"last_level_value"`
	LastBytesFreed   int64  `json:"last_bytes_freed"`
	LastItemsCleaned int    `json:"last_items_cleaned"`
	LastError        string `json:"last_error,omitempty"`
}

func newCleanupState() *cleanupState {
	return &cleanupState{
		Version: cleanupStateVersion,
		Plugins: map[string]pluginStateRecord{},
	}
}

func loadCleanupState(path string) (*cleanupState, error) {
	if path == "" {
		return newCleanupState(), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return newCleanupState(), nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return newCleanupState(), nil
	}

	state := newCleanupState()
	if err := json.Unmarshal(data, state); err != nil {
		return nil, err
	}
	if state.Plugins == nil {
		state.Plugins = map[string]pluginStateRecord{}
	}
	if state.Version == 0 {
		state.Version = cleanupStateVersion
	}
	return state, nil
}

func saveCleanupState(path string, state *cleanupState) error {
	if path == "" || state == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

func (s *cleanupState) cooldownRemaining(plugin string, level plugins.CleanupLevel, now time.Time, cooldown time.Duration) time.Duration {
	if s == nil || cooldown <= 0 {
		return 0
	}
	record, ok := s.Plugins[plugin]
	if !ok || record.LastLevelValue < int(level) {
		return 0
	}
	lastRun, err := time.Parse(time.RFC3339, record.LastRun)
	if err != nil {
		return 0
	}
	elapsed := now.Sub(lastRun)
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed >= cooldown {
		return 0
	}
	return cooldown - elapsed
}

func (s *cleanupState) recordPluginRun(plugin string, level plugins.CleanupLevel, now time.Time, result plugins.CleanupResult) {
	if s == nil {
		return
	}
	if s.Plugins == nil {
		s.Plugins = map[string]pluginStateRecord{}
	}
	record := pluginStateRecord{
		LastRun:          now.UTC().Format(time.RFC3339),
		LastLevel:        level.String(),
		LastLevelValue:   int(level),
		LastBytesFreed:   result.BytesFreed,
		LastItemsCleaned: result.ItemsCleaned,
	}
	if result.Error != nil {
		record.LastError = result.Error.Error()
	}
	s.Plugins[plugin] = record
}
