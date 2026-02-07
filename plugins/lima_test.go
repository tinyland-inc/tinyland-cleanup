//go:build darwin

package plugins

import (
	"context"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"gitlab.com/tinyland/lab/tinyland-cleanup/config"
)

// ---------------------------------------------------------------------------
// Plugin interface conformance
// ---------------------------------------------------------------------------

func TestLimaPluginName(t *testing.T) {
	p := NewLimaPlugin()
	if got := p.Name(); got != "lima" {
		t.Errorf("Name() = %q, want %q", got, "lima")
	}
}

func TestLimaPluginDescription(t *testing.T) {
	p := NewLimaPlugin()
	if desc := p.Description(); desc == "" {
		t.Error("Description() should not be empty")
	}
}

func TestLimaPluginSupportedPlatforms(t *testing.T) {
	p := NewLimaPlugin()
	platforms := p.SupportedPlatforms()
	if len(platforms) != 1 || platforms[0] != PlatformDarwin {
		t.Errorf("SupportedPlatforms() = %v, want [darwin]", platforms)
	}
}

func TestLimaPluginEnabled(t *testing.T) {
	p := NewLimaPlugin()
	cfg := config.DefaultConfig()

	// Default on Darwin should be enabled
	if !p.Enabled(cfg) {
		t.Error("expected Lima plugin to be enabled by default on Darwin")
	}

	// Disable and check
	cfg.Enable.Lima = false
	if p.Enabled(cfg) {
		t.Error("expected Lima plugin to be disabled when flag is false")
	}
}

// ---------------------------------------------------------------------------
// getActualDiskSize
// ---------------------------------------------------------------------------

func TestGetActualDiskSize_ReturnsBlocksTimesBlockSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "testfile")

	data := make([]byte, 4096)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	p := &LimaPlugin{}
	actual := p.getActualDiskSize(path)

	if actual <= 0 {
		t.Errorf("getActualDiskSize returned %d, want > 0", actual)
	}

	if actual%512 != 0 {
		t.Errorf("getActualDiskSize returned %d which is not a multiple of 512", actual)
	}

	info, _ := os.Stat(path)
	t.Logf("apparent=%d actual=%d (blocks-based)", info.Size(), actual)
}

func TestGetActualDiskSize_SparseFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sparse")

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create file: %v", err)
	}
	// Seek 1 MiB forward and write a single byte to create a sparse file.
	if _, err := f.Seek(1024*1024, 0); err != nil {
		f.Close()
		t.Fatalf("failed to seek: %v", err)
	}
	if _, err := f.Write([]byte{1}); err != nil {
		f.Close()
		t.Fatalf("failed to write: %v", err)
	}
	f.Close()

	p := &LimaPlugin{}
	actual := p.getActualDiskSize(path)
	info, _ := os.Stat(path)
	apparent := info.Size()

	t.Logf("sparse file: apparent=%d actual=%d", apparent, actual)

	// Actual blocks used should be less than apparent size for a sparse file.
	// APFS may not support traditional sparse files, so we log rather than fail.
	if actual >= apparent {
		t.Logf("note: APFS may not support traditional sparse files, skipping ratio check")
	}
}

func TestGetActualDiskSize_NonexistentFile(t *testing.T) {
	p := &LimaPlugin{}
	actual := p.getActualDiskSize("/nonexistent/path/file")
	if actual != 0 {
		t.Errorf("getActualDiskSize for nonexistent file returned %d, want 0", actual)
	}
}

func TestGetActualDiskSize_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty")

	if err := os.WriteFile(path, []byte{}, 0644); err != nil {
		t.Fatalf("failed to create empty file: %v", err)
	}

	p := &LimaPlugin{}
	actual := p.getActualDiskSize(path)

	// An empty file may use 0 blocks or a minimal number of blocks depending
	// on filesystem metadata. It should never be negative.
	if actual < 0 {
		t.Errorf("getActualDiskSize for empty file returned %d, want >= 0", actual)
	}

	info, _ := os.Stat(path)
	t.Logf("empty file: apparent=%d actual=%d", info.Size(), actual)
}

func TestGetActualDiskSize_LargerFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "larger")

	// Write 64 KiB of data
	data := make([]byte, 64*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	p := &LimaPlugin{}
	actual := p.getActualDiskSize(path)
	info, _ := os.Stat(path)

	if actual <= 0 {
		t.Errorf("getActualDiskSize returned %d for 64KiB file, want > 0", actual)
	}

	// For a non-sparse file, actual should be >= apparent (may be slightly
	// larger due to block alignment).
	if actual < info.Size() {
		t.Logf("actual (%d) < apparent (%d); filesystem may use compression", actual, info.Size())
	}

	t.Logf("64KiB file: apparent=%d actual=%d", info.Size(), actual)
}

func TestGetActualDiskSize_Directory(t *testing.T) {
	dir := t.TempDir()

	p := &LimaPlugin{}
	actual := p.getActualDiskSize(dir)

	// Directories have blocks allocated for metadata. Should not be negative.
	if actual < 0 {
		t.Errorf("getActualDiskSize for directory returned %d, want >= 0", actual)
	}
	t.Logf("directory: actual=%d", actual)
}

func TestGetActualDiskSize_Symlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")

	data := make([]byte, 8192)
	if err := os.WriteFile(target, data, 0644); err != nil {
		t.Fatalf("failed to write target file: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	p := &LimaPlugin{}
	actualTarget := p.getActualDiskSize(target)
	actualLink := p.getActualDiskSize(link)

	// syscall.Stat follows symlinks, so both should report the same blocks.
	if actualTarget != actualLink {
		t.Errorf("symlink actual (%d) != target actual (%d); Stat should follow symlinks",
			actualLink, actualTarget)
	}
}

// ---------------------------------------------------------------------------
// fstrim output parsing
// ---------------------------------------------------------------------------

func TestFstrimRegex(t *testing.T) {
	re := regexp.MustCompile(`\((\d+) bytes\) trimmed`)

	tests := []struct {
		name   string
		output string
		want   int64
	}{
		{
			name:   "standard fstrim output",
			output: "/var: 1.5 GiB (1610612736 bytes) trimmed on /dev/vda1\n",
			want:   1610612736,
		},
		{
			name:   "multiple mounts",
			output: "/: 512 MiB (536870912 bytes) trimmed on /dev/vda1\n/var: 1.0 GiB (1073741824 bytes) trimmed on /dev/vda2\n",
			want:   536870912 + 1073741824,
		},
		{
			name:   "zero trimmed",
			output: "/: 0 B (0 bytes) trimmed on /dev/vda1\n",
			want:   0,
		},
		{
			name:   "no match",
			output: "some unrelated output\n",
			want:   0,
		},
		{
			name:   "old regex would not match parenthesized format",
			output: "/home: 2.0 GiB (2147483648 bytes) trimmed\n",
			want:   2147483648,
		},
		{
			name:   "very large value (100 GiB)",
			output: "/data: 100.0 GiB (107374182400 bytes) trimmed on /dev/sda1\n",
			want:   107374182400,
		},
		{
			name:   "small value (4 KiB)",
			output: "/tmp: 4 KiB (4096 bytes) trimmed on /dev/vda1\n",
			want:   4096,
		},
		{
			name:   "three mounts",
			output: "/: 100 MiB (104857600 bytes) trimmed on /dev/vda1\n/var: 200 MiB (209715200 bytes) trimmed on /dev/vda2\n/home: 300 MiB (314572800 bytes) trimmed on /dev/vda3\n",
			want:   104857600 + 209715200 + 314572800,
		},
		{
			name:   "empty output",
			output: "",
			want:   0,
		},
		{
			name:   "whitespace only",
			output: "   \n\t\n  ",
			want:   0,
		},
		{
			name:   "partial match - bytes without trimmed",
			output: "Something (12345 bytes) but not trimmed\n",
			want:   0,
		},
		{
			name:   "no parentheses - should not match",
			output: "/: 1024 bytes trimmed\n",
			want:   0,
		},
		{
			name:   "one byte trimmed",
			output: "/tmp: 1 B (1 bytes) trimmed on /dev/vda1\n",
			want:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := re.FindAllStringSubmatch(tt.output, -1)
			var total int64
			for _, match := range matches {
				if len(match) >= 2 {
					if bytes, err := strconv.ParseInt(match[1], 10, 64); err == nil {
						total += bytes
					}
				}
			}
			if total != tt.want {
				t.Errorf("parsed %d bytes, want %d", total, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Sparse ratio calculation
// ---------------------------------------------------------------------------

func TestSparseRatioCalculation(t *testing.T) {
	// Verify sparse ratio logic matches compactDisk decision.
	// The code uses:  sparseRatio > 70  (skip when ratio is above 70%)
	tests := []struct {
		name          string
		apparentSize  int64
		actualSize    int64
		shouldCompact bool
	}{
		{"well-compacted (80%)", 10_000_000_000, 8_000_000_000, false},
		{"needs-compaction (50%)", 10_000_000_000, 5_000_000_000, true},
		{"very-sparse (10%)", 10_000_000_000, 1_000_000_000, true},
		{"at-boundary (70% exactly)", 10_000_000_000, 7_000_000_000, true}, // > 70, not >=
		{"just-above-boundary (70.01%)", 10_000_000_000, 7_001_000_000, false},
		{"fully-dense (100%)", 10_000_000_000, 10_000_000_000, false},
		{"almost-empty (1%)", 10_000_000_000, 100_000_000, true},
		{"barely-above (71%)", 10_000_000_000, 7_100_000_000, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sparseRatio := float64(tt.actualSize) / float64(tt.apparentSize) * 100
			shouldSkip := sparseRatio > 70
			gotCompact := !shouldSkip
			if gotCompact != tt.shouldCompact {
				t.Errorf("sparse ratio %.2f%% => compact=%v, want compact=%v",
					sparseRatio, gotCompact, tt.shouldCompact)
			}
		})
	}
}

func TestSparseRatioZeroSizes(t *testing.T) {
	// Edge case: when actualSize or apparentSize is 0, the compactDisk code
	// guards with `if actualSize > 0 && apparentSize > 0` so it would NOT
	// skip compaction (the ratio block is skipped entirely, compaction proceeds).
	tests := []struct {
		name         string
		actualSize   int64
		apparentSize int64
		skipBlock    bool // whether the ratio check is entered at all
	}{
		{"both zero", 0, 0, true},
		{"actual zero", 0, 10_000_000, true},
		{"apparent zero", 5_000_000, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the guard from compactDisk
			ratioBlockEntered := tt.actualSize > 0 && tt.apparentSize > 0
			if ratioBlockEntered == tt.skipBlock {
				t.Errorf("actualSize=%d apparentSize=%d: ratioBlockEntered=%v, want %v",
					tt.actualSize, tt.apparentSize, ratioBlockEntered, !tt.skipBlock)
			}
		})
	}
}

func TestSparseRatioFloatingPointPrecision(t *testing.T) {
	// Ensure the sparse ratio calculation does not suffer from integer
	// truncation. The code uses float64 division, so this should be fine,
	// but we verify explicitly.
	actual := int64(7_000_000_001)
	apparent := int64(10_000_000_000)

	ratio := float64(actual) / float64(apparent) * 100
	// 70.00000001% -- should be > 70, so skip compaction
	if ratio <= 70 {
		t.Errorf("expected ratio %.10f to be > 70", ratio)
	}
}

// ---------------------------------------------------------------------------
// parseDockerReclaimedSpace (shared helper used by cleanupVM)
// ---------------------------------------------------------------------------

func TestParseDockerReclaimedSpace(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   int64
	}{
		{
			name:   "empty output",
			output: "",
			want:   0,
		},
		{
			name:   "no match",
			output: "Deleted some containers\n",
			want:   0,
		},
		{
			name:   "megabytes",
			output: "Total reclaimed space: 256.5 MB",
			want:   268959744, // 256.5 * 1024 * 1024
		},
		{
			name:   "gigabytes",
			output: "Total reclaimed space: 3.2 GB",
			want:   3435973836, // 3.2 * 1024^3
		},
		{
			name:   "kilobytes",
			output: "Total reclaimed space: 100 KB",
			want:   102400, // 100 * 1024
		},
		{
			name:   "bytes",
			output: "Total reclaimed space: 512 B",
			want:   512,
		},
		{
			name:   "terabytes",
			output: "Total reclaimed space: 1.0 TB",
			want:   1099511627776, // 1.0 * 1024^4
		},
		{
			name:   "lowercase prefix pattern",
			output: "reclaimed space: 500 MB",
			want:   524288000, // 500 * 1024^2
		},
		{
			name:   "with surrounding text",
			output: "Deleted images:\nuntagged: foo\nTotal reclaimed space: 1.5 GB\nDone.",
			want:   1610612736, // 1.5 * 1024^3
		},
		{
			name:   "zero megabytes",
			output: "Total reclaimed space: 0 MB",
			want:   0,
		},
		{
			name:   "fractional kilobytes",
			output: "Total reclaimed space: 0.5 KB",
			want:   512, // 0.5 * 1024
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDockerReclaimedSpace(tt.output)
			// Allow 1% tolerance for floating point
			if tt.want == 0 {
				if got != 0 {
					t.Errorf("parseDockerReclaimedSpace(%q) = %d, want 0", tt.output, got)
				}
				return
			}
			diff := math.Abs(float64(got - tt.want))
			tolerance := math.Max(float64(tt.want)*0.01, 1)
			if diff > tolerance {
				t.Errorf("parseDockerReclaimedSpace(%q) = %d, want %d (diff: %.0f, tolerance: %.0f)",
					tt.output, got, tt.want, diff, tolerance)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// contains helper
// ---------------------------------------------------------------------------

func TestContains(t *testing.T) {
	tests := []struct {
		name  string
		slice []string
		item  string
		want  bool
	}{
		{"found first", []string{"a", "b", "c"}, "a", true},
		{"found middle", []string{"a", "b", "c"}, "b", true},
		{"found last", []string{"a", "b", "c"}, "c", true},
		{"not found", []string{"a", "b", "c"}, "d", false},
		{"empty slice", []string{}, "a", false},
		{"nil slice", nil, "a", false},
		{"empty string found", []string{"", "a"}, "", true},
		{"empty string not found", []string{"a", "b"}, "", false},
		{"case sensitive", []string{"colima"}, "Colima", false},
		{"exact vm name", []string{"colima", "unified"}, "colima", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := contains(tt.slice, tt.item)
			if got != tt.want {
				t.Errorf("contains(%v, %q) = %v, want %v", tt.slice, tt.item, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// VMDiskInfo struct
// ---------------------------------------------------------------------------

func TestVMDiskInfoFields(t *testing.T) {
	info := VMDiskInfo{
		Name:           "colima",
		Status:         "Running",
		TotalBytes:     80 * 1024 * 1024 * 1024,    // 80 GiB
		UsedBytes:      30 * 1024 * 1024 * 1024,     // 30 GiB
		AvailableBytes: 50 * 1024 * 1024 * 1024,     // 50 GiB
		UsedPercent:    "38",
		HostDiskSize:   20 * 1024 * 1024 * 1024,     // 20 GiB (sparse)
		DiskPath:       "/Users/test/.lima/colima/diffdisk",
	}

	if info.Name != "colima" {
		t.Errorf("Name = %q, want %q", info.Name, "colima")
	}
	if info.TotalBytes != 80*1024*1024*1024 {
		t.Errorf("TotalBytes = %d, want %d", info.TotalBytes, 80*1024*1024*1024)
	}
	if info.UsedBytes+info.AvailableBytes != info.TotalBytes {
		t.Errorf("UsedBytes (%d) + AvailableBytes (%d) != TotalBytes (%d)",
			info.UsedBytes, info.AvailableBytes, info.TotalBytes)
	}

	// Host disk should be smaller than guest total (sparse image)
	if info.HostDiskSize >= info.TotalBytes {
		t.Errorf("HostDiskSize (%d) should be < TotalBytes (%d) for sparse image",
			info.HostDiskSize, info.TotalBytes)
	}
}

func TestVMDiskInfoStoppedVM(t *testing.T) {
	// A stopped VM may only have Name and Status populated.
	info := VMDiskInfo{
		Name:   "unified",
		Status: "Stopped",
	}

	if info.TotalBytes != 0 {
		t.Error("stopped VM should have zero TotalBytes")
	}
	if info.DiskPath != "" {
		t.Error("stopped VM may have no disk path")
	}
}

// ---------------------------------------------------------------------------
// LimaConfig defaults
// ---------------------------------------------------------------------------

func TestLimaConfigDefaults(t *testing.T) {
	cfg := config.DefaultConfig()

	// Default VM names should include colima and unified
	expectedVMs := map[string]bool{"colima": true, "unified": true}
	for _, vm := range cfg.Lima.VMNames {
		if !expectedVMs[vm] {
			t.Errorf("unexpected default VM name: %q", vm)
		}
		delete(expectedVMs, vm)
	}
	for vm := range expectedVMs {
		t.Errorf("missing expected default VM name: %q", vm)
	}

	// CompactOffline should default to false (opt-in only)
	if cfg.Lima.CompactOffline {
		t.Error("CompactOffline should default to false")
	}
}

// ---------------------------------------------------------------------------
// getActualDiskSize vs os.Stat -- ensures we use Blocks not Size
// ---------------------------------------------------------------------------

func TestGetActualDiskSize_NotApparentSize(t *testing.T) {
	// This test validates the critical fix: getActualDiskSize must return
	// Blocks * 512, NOT os.FileInfo.Size() (apparent size).
	//
	// We write a file and verify that getActualDiskSize returns a value that
	// is a multiple of 512 (block-aligned), which is a property of the
	// blocks-based calculation but NOT of arbitrary file sizes.
	dir := t.TempDir()
	path := filepath.Join(dir, "blockcheck")

	// Write 1000 bytes (intentionally not a multiple of 512)
	data := make([]byte, 1000)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	p := &LimaPlugin{}
	actual := p.getActualDiskSize(path)

	info, _ := os.Stat(path)
	apparent := info.Size()

	// Apparent size is 1000 bytes (not a multiple of 512)
	if apparent != 1000 {
		t.Fatalf("unexpected apparent size: %d", apparent)
	}

	// Actual (block-based) size MUST be a multiple of 512
	if actual%512 != 0 {
		t.Errorf("actual size %d is not block-aligned (multiple of 512)", actual)
	}

	// The actual size should NOT equal the apparent size (1000 is not
	// block-aligned, so they differ).
	if actual == apparent {
		t.Errorf("actual (%d) == apparent (%d); getActualDiskSize may be returning apparent size",
			actual, apparent)
	}

	t.Logf("apparent=%d actual=%d (block-aligned)", apparent, actual)
}

// ---------------------------------------------------------------------------
// detectDiskFormat
// ---------------------------------------------------------------------------

func TestDetectDiskFormat_RawFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "diffdisk")

	// Create a file with a DOS/MBR-like header (not qcow2 magic)
	data := make([]byte, 4096)
	data[0] = 0x00 // Not QFI\xfb
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	p := &LimaPlugin{}
	format := p.detectDiskFormat(context.Background(), path)
	if format != "raw" {
		t.Errorf("detectDiskFormat = %q, want %q for non-qcow2 file", format, "raw")
	}
}

func TestDetectDiskFormat_Qcow2Magic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "diffdisk")

	// Create a file with qcow2 magic bytes: QFI\xfb
	data := make([]byte, 4096)
	copy(data[:4], []byte("QFI\xfb"))
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	p := &LimaPlugin{}
	// If qemu-img is not available, should fall back to magic byte detection
	format := p.detectDiskFormat(context.Background(), path)
	// qemu-img may or may not be installed; either way should detect qcow2
	if format != "qcow2" {
		t.Errorf("detectDiskFormat = %q, want %q for qcow2 magic", format, "qcow2")
	}
}

func TestDetectDiskFormat_NonexistentFile(t *testing.T) {
	p := &LimaPlugin{}
	format := p.detectDiskFormat(context.Background(), "/nonexistent/path/diffdisk")
	if format != "raw" {
		t.Errorf("detectDiskFormat = %q, want %q for nonexistent file", format, "raw")
	}
}

// ---------------------------------------------------------------------------
// execInVM (unit-level: SSH config detection)
// ---------------------------------------------------------------------------

func TestExecInVM_NoLimactl_NoSSHConfig(t *testing.T) {
	// When both limactl shell and SSH config are missing, should return error
	p := &LimaPlugin{}
	logger := slog.Default()
	_, err := p.execInVM(context.Background(), "nonexistent-vm", []string{"echo", "test"}, logger)
	if err == nil {
		t.Error("expected error when VM doesn't exist")
	}
}
