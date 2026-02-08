package fsops

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestIsZeroBlock(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  bool
	}{
		{
			name:  "all zeros",
			input: make([]byte, 4096),
			want:  true,
		},
		{
			name:  "single non-zero at start",
			input: append([]byte{1}, make([]byte, 4095)...),
			want:  false,
		},
		{
			name:  "single non-zero at end",
			input: append(make([]byte, 4095), 1),
			want:  false,
		},
		{
			name:  "single non-zero in middle",
			input: func() []byte {
				b := make([]byte, 4096)
				b[2048] = 1
				return b
			}(),
			want: false,
		},
		{
			name:  "empty buffer",
			input: []byte{},
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isZeroBlock(tt.input); got != tt.want {
				t.Errorf("isZeroBlock() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScanZeroRegions(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "testfile")

	// Create a file with known zero/non-zero pattern
	// Pattern: 4KB zeros, 4KB data, 8KB zeros, 4KB data, 4KB zeros
	f, err := os.Create(testFile)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Write 4KB zeros
	if _, err := f.Write(make([]byte, 4096)); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	// Write 4KB non-zero data
	data := make([]byte, 4096)
	for i := range data {
		data[i] = byte(i % 256)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	// Write 8KB zeros
	if _, err := f.Write(make([]byte, 8192)); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	// Write 4KB non-zero data
	if _, err := f.Write(data); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	// Write 4KB zeros
	if _, err := f.Write(make([]byte, 4096)); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	f.Close()

	// Scan for zero regions
	regions, err := ScanZeroRegions(testFile, 4096)
	if err != nil {
		t.Fatalf("ScanZeroRegions failed: %v", err)
	}

	// Expected regions: [0, 4096), [8192, 16384), [20480, 24576)
	expected := []ZeroRegion{
		{Offset: 0, Length: 4096},
		{Offset: 8192, Length: 8192},
		{Offset: 20480, Length: 4096},
	}

	if len(regions) != len(expected) {
		t.Fatalf("expected %d regions, got %d", len(expected), len(regions))
	}

	for i, region := range regions {
		if region.Offset != expected[i].Offset || region.Length != expected[i].Length {
			t.Errorf("region %d: got {Offset: %d, Length: %d}, want {Offset: %d, Length: %d}",
				i, region.Offset, region.Length, expected[i].Offset, expected[i].Length)
		}
	}
}

func TestPunchHoles(t *testing.T) {
	// Skip on unsupported platforms
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("hole punching not supported on this platform")
	}

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "sparse")

	// Create a 16KB file filled with data
	data := make([]byte, 16384)
	for i := range data {
		data[i] = byte(i % 256)
	}

	if err := os.WriteFile(testFile, data, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Get initial actual size
	initialSize, err := GetActualSize(testFile)
	if err != nil {
		t.Fatalf("failed to get initial size: %v", err)
	}

	// Punch holes in the middle 8KB
	regions := []ZeroRegion{
		{Offset: 4096, Length: 8192},
	}

	freed, err := PunchHoles(testFile, regions)
	if err != nil {
		t.Fatalf("PunchHoles failed: %v", err)
	}

	if freed != 8192 {
		t.Errorf("expected to free 8192 bytes, got %d", freed)
	}

	// Get new actual size
	newSize, err := GetActualSize(testFile)
	if err != nil {
		t.Fatalf("failed to get new size: %v", err)
	}

	// The actual size should be smaller (we freed 8KB)
	// Note: actual size might not decrease by exactly 8KB due to block alignment
	if newSize >= initialSize {
		t.Errorf("expected actual size to decrease from %d, got %d", initialSize, newSize)
	}

	// Verify apparent size hasn't changed
	fi, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("failed to stat file: %v", err)
	}
	if fi.Size() != 16384 {
		t.Errorf("apparent size changed: expected 16384, got %d", fi.Size())
	}
}

func TestCompactInPlace(t *testing.T) {
	// Skip on unsupported platforms
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("hole punching not supported on this platform")
	}

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "compact")

	// Create a file with zero regions: 4KB data, 8KB zeros, 4KB data
	f, err := os.Create(testFile)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Write 4KB non-zero data
	data := make([]byte, 4096)
	for i := range data {
		data[i] = byte(i % 256)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	// Write 8KB zeros
	if _, err := f.Write(make([]byte, 8192)); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	// Write 4KB non-zero data
	if _, err := f.Write(data); err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	f.Close()

	// Get initial actual size
	initialSize, err := GetActualSize(testFile)
	if err != nil {
		t.Fatalf("failed to get initial size: %v", err)
	}

	// Compact the file
	freed, err := CompactInPlace(testFile, 4096)
	if err != nil {
		t.Fatalf("CompactInPlace failed: %v", err)
	}

	if freed != 8192 {
		t.Errorf("expected to free 8192 bytes, got %d", freed)
	}

	// Get new actual size
	newSize, err := GetActualSize(testFile)
	if err != nil {
		t.Fatalf("failed to get new size: %v", err)
	}

	// The actual size should be smaller
	if newSize >= initialSize {
		t.Errorf("expected actual size to decrease from %d, got %d", initialSize, newSize)
	}

	// Verify apparent size hasn't changed
	fi, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("failed to stat file: %v", err)
	}
	if fi.Size() != 16384 {
		t.Errorf("apparent size changed: expected 16384, got %d", fi.Size())
	}
}

func TestGetActualSize(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "size_test")

	// Create a 16KB file
	data := make([]byte, 16384)
	for i := range data {
		data[i] = byte(i % 256)
	}

	if err := os.WriteFile(testFile, data, 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Get actual size
	actualSize, err := GetActualSize(testFile)
	if err != nil {
		t.Fatalf("GetActualSize failed: %v", err)
	}

	// Actual size should be at least the apparent size (or close to it)
	// Due to filesystem block alignment, it might be slightly larger
	if actualSize < 16384 {
		t.Errorf("actual size %d is less than expected minimum 16384", actualSize)
	}

	// It shouldn't be wildly different (allow 10% margin for block alignment)
	if actualSize > 18000 {
		t.Errorf("actual size %d is unexpectedly large (expected ~16384)", actualSize)
	}
}
