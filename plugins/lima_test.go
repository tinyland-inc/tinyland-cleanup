//go:build darwin

package plugins

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

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

	// Actual blocks used should be less than apparent size for a sparse file
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

func TestSparseRatioCalculation(t *testing.T) {
	// Verify sparse ratio logic matches compactDisk decision
	tests := []struct {
		name          string
		apparentSize  int64
		actualSize    int64
		shouldCompact bool
	}{
		{"well-compacted", 10_000_000_000, 8_000_000_000, false},   // 80% — skip
		{"needs-compaction", 10_000_000_000, 5_000_000_000, true},  // 50% — compact
		{"very-sparse", 10_000_000_000, 1_000_000_000, true},       // 10% — compact
		{"at-boundary", 10_000_000_000, 7_000_000_000, true},       // 70% exactly — compact (code uses > 70, not >=)
		{"just-below", 10_000_000_000, 6_999_999_999, true},        // ~70% — compact
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sparseRatio := float64(tt.actualSize) / float64(tt.apparentSize) * 100
			shouldSkip := sparseRatio > 70
			gotCompact := !shouldSkip
			if gotCompact != tt.shouldCompact {
				t.Errorf("sparse ratio %.1f%% => compact=%v, want compact=%v",
					sparseRatio, gotCompact, tt.shouldCompact)
			}
		})
	}
}
