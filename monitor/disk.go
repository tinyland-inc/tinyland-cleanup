// Package monitor provides disk usage monitoring functionality.
package monitor

import (
	"github.com/shirou/gopsutil/v3/disk"
)

// DiskStats represents disk usage statistics.
type DiskStats struct {
	// Path is the mount point being monitored
	Path string
	// Total bytes on the disk
	Total uint64
	// Used bytes on the disk
	Used uint64
	// Free bytes on the disk
	Free uint64
	// UsedPercent is the percentage of disk used
	UsedPercent float64
	// FreePercent is the percentage of disk free
	FreePercent float64
	// FreeGB is free space in gigabytes
	FreeGB float64
}

// GetDiskStats returns disk statistics for the specified path.
func GetDiskStats(path string) (*DiskStats, error) {
	usage, err := disk.Usage(path)
	if err != nil {
		return nil, err
	}

	return &DiskStats{
		Path:        path,
		Total:       usage.Total,
		Used:        usage.Used,
		Free:        usage.Free,
		UsedPercent: usage.UsedPercent,
		FreePercent: 100.0 - usage.UsedPercent,
		FreeGB:      float64(usage.Free) / (1024 * 1024 * 1024),
	}, nil
}

// GetRootDiskStats returns disk statistics for the root filesystem.
func GetRootDiskStats() (*DiskStats, error) {
	return GetDiskStats("/")
}

// DiskMonitor provides disk monitoring with threshold detection.
type DiskMonitor struct {
	// ThresholdWarning percentage for warning level
	ThresholdWarning float64
	// ThresholdModerate percentage for moderate level
	ThresholdModerate float64
	// ThresholdAggressive percentage for aggressive level
	ThresholdAggressive float64
	// ThresholdCritical percentage for critical level
	ThresholdCritical float64
}

// NewDiskMonitor creates a new disk monitor with the specified thresholds.
func NewDiskMonitor(warning, moderate, aggressive, critical int) *DiskMonitor {
	return &DiskMonitor{
		ThresholdWarning:    float64(warning),
		ThresholdModerate:   float64(moderate),
		ThresholdAggressive: float64(aggressive),
		ThresholdCritical:   float64(critical),
	}
}

// CleanupLevel represents the cleanup severity level needed.
type CleanupLevel int

const (
	// LevelNone means no cleanup needed
	LevelNone CleanupLevel = iota
	// LevelWarning triggers light cleanup
	LevelWarning
	// LevelModerate triggers moderate cleanup
	LevelModerate
	// LevelAggressive triggers aggressive cleanup
	LevelAggressive
	// LevelCritical triggers emergency cleanup
	LevelCritical
)

// String returns the string representation of the cleanup level.
func (l CleanupLevel) String() string {
	switch l {
	case LevelNone:
		return "none"
	case LevelWarning:
		return "warning"
	case LevelModerate:
		return "moderate"
	case LevelAggressive:
		return "aggressive"
	case LevelCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// CheckLevel determines the cleanup level needed based on disk usage.
func (m *DiskMonitor) CheckLevel(stats *DiskStats) CleanupLevel {
	if stats.UsedPercent >= m.ThresholdCritical {
		return LevelCritical
	}
	if stats.UsedPercent >= m.ThresholdAggressive {
		return LevelAggressive
	}
	if stats.UsedPercent >= m.ThresholdModerate {
		return LevelModerate
	}
	if stats.UsedPercent >= m.ThresholdWarning {
		return LevelWarning
	}
	return LevelNone
}

// Check performs a disk check and returns the current stats and required level.
func (m *DiskMonitor) Check(path string) (*DiskStats, CleanupLevel, error) {
	stats, err := GetDiskStats(path)
	if err != nil {
		return nil, LevelNone, err
	}
	return stats, m.CheckLevel(stats), nil
}
