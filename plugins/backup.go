package plugins

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"gitlab.com/tinyland/lab/tinyland-cleanup/config"
)

// BackupManager handles optional backup creation before destructive disk operations.
// DEFAULT OFF. Only creates backups when explicitly enabled and sufficient space exists.
type BackupManager struct {
	cfg    *config.BackupConfig
	logger *slog.Logger
}

// NewBackupManager creates a new backup manager.
func NewBackupManager(cfg *config.BackupConfig, logger *slog.Logger) *BackupManager {
	return &BackupManager{cfg: cfg, logger: logger}
}

// CreateBackup creates a compressed backup of the given disk file.
// Returns the backup path and any error. Respects all safety constraints:
// - Only runs if Enabled is true
// - Checks MinFreeGBToBackup before creating
// - Enforces MaxCount with LRU eviction
// - Enforces MaxTotalGB storage limit
func (m *BackupManager) CreateBackup(diskPath string) (string, error) {
	if m.cfg == nil || !m.cfg.Enabled {
		return "", nil
	}

	// Check free space before backup
	freeBytes, err := getFreeDiskSpace(filepath.Dir(diskPath))
	if err != nil {
		return "", fmt.Errorf("cannot check free space: %w", err)
	}
	freeGB := float64(freeBytes) / (1024 * 1024 * 1024)
	if freeGB < m.cfg.MinFreeGBToBackup {
		m.logger.Warn("skipping backup: insufficient free space",
			"free_gb", fmt.Sprintf("%.1f", freeGB),
			"min_required_gb", m.cfg.MinFreeGBToBackup)
		return "", nil
	}

	// Determine backup directory and filename
	backupDir := filepath.Join(filepath.Dir(diskPath), "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return "", fmt.Errorf("cannot create backup dir: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405")
	baseName := filepath.Base(diskPath)
	ext := backupExtension(m.cfg.Compression)
	backupPath := filepath.Join(backupDir, fmt.Sprintf("%s.%s%s", baseName, timestamp, ext))

	// Evict old backups before creating new one
	m.evictOldBackups(backupDir, baseName)

	// Create compressed backup
	m.logger.Info("creating backup", "source", diskPath, "dest", backupPath, "compression", m.cfg.Compression)
	if err := m.compressFile(diskPath, backupPath); err != nil {
		os.Remove(backupPath)
		return "", fmt.Errorf("backup compression failed: %w", err)
	}

	return backupPath, nil
}

// evictOldBackups removes backups exceeding MaxCount or MaxTotalGB (LRU eviction).
func (m *BackupManager) evictOldBackups(backupDir, baseName string) {
	pattern := filepath.Join(backupDir, baseName+".*")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return
	}

	// Sort by modification time (oldest first)
	sort.Slice(matches, func(i, j int) bool {
		fi, _ := os.Stat(matches[i])
		fj, _ := os.Stat(matches[j])
		if fi == nil || fj == nil {
			return false
		}
		return fi.ModTime().Before(fj.ModTime())
	})

	// Evict by count (keep MaxCount - 1 to make room for new backup)
	maxKeep := m.cfg.MaxCount - 1
	if maxKeep < 0 {
		maxKeep = 0
	}
	for len(matches) > maxKeep {
		m.logger.Info("evicting old backup", "path", matches[0])
		os.Remove(matches[0])
		matches = matches[1:]
	}

	// Evict by total size
	maxBytes := int64(m.cfg.MaxTotalGB * 1024 * 1024 * 1024)
	var totalSize int64
	for _, path := range matches {
		if fi, err := os.Stat(path); err == nil {
			totalSize += fi.Size()
		}
	}
	for totalSize > maxBytes && len(matches) > 0 {
		if fi, err := os.Stat(matches[0]); err == nil {
			totalSize -= fi.Size()
		}
		m.logger.Info("evicting backup (size limit)", "path", matches[0])
		os.Remove(matches[0])
		matches = matches[1:]
	}
}

// compressFile creates a compressed copy of src at dst.
func (m *BackupManager) compressFile(src, dst string) error {
	switch m.cfg.Compression {
	case "zstd":
		cmd := exec.Command("zstd", "-q", "-o", dst, src)
		return cmd.Run()
	case "lz4":
		cmd := exec.Command("lz4", "-q", src, dst)
		return cmd.Run()
	case "gzip":
		cmd := exec.Command("gzip", "-c", src)
		outFile, err := os.Create(dst)
		if err != nil {
			return err
		}
		defer outFile.Close()
		cmd.Stdout = outFile
		return cmd.Run()
	case "none", "":
		// Simple copy
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0644)
	default:
		return fmt.Errorf("unsupported compression: %s", m.cfg.Compression)
	}
}

// backupExtension returns the file extension for the compression type.
func backupExtension(compression string) string {
	switch compression {
	case "zstd":
		return ".zst"
	case "lz4":
		return ".lz4"
	case "gzip":
		return ".gz"
	default:
		return ".bak"
	}
}
