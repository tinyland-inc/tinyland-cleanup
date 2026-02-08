package fsops

import (
	"errors"
	"os"
	"syscall"
)

// DefaultBlockSize is the default block size for scanning zero regions
const DefaultBlockSize = 4096

// ZeroRegion represents a contiguous region of zero bytes in a file
type ZeroRegion struct {
	Offset int64
	Length int64
}

// ErrNotSupported is returned when hole punching is not supported on the platform
var ErrNotSupported = errors.New("hole punching not supported on this platform")

// ScanZeroRegions scans a file for contiguous regions of zero bytes.
// Returns a slice of ZeroRegion describing the location and size of each region.
func ScanZeroRegions(path string, blockSize int) ([]ZeroRegion, error) {
	return scanZeroRegions(path, blockSize)
}

// PunchHoles punches holes in a file for the specified zero regions.
// Returns the total number of bytes freed and any error encountered.
func PunchHoles(path string, regions []ZeroRegion) (int64, error) {
	if len(regions) == 0 {
		return 0, nil
	}

	// Open file with read-write access
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	fd := f.Fd()
	var totalFreed int64

	for _, region := range regions {
		if err := punchHole(fd, region.Offset, region.Length); err != nil {
			return totalFreed, err
		}
		totalFreed += region.Length
	}

	return totalFreed, nil
}

// CompactInPlace scans a file for zero regions and punches holes to free disk space.
// Returns the total number of bytes freed and any error encountered.
func CompactInPlace(path string, blockSize int) (int64, error) {
	regions, err := ScanZeroRegions(path, blockSize)
	if err != nil {
		return 0, err
	}

	return PunchHoles(path, regions)
}

// GetActualSize returns the actual disk space used by a file (accounting for sparse regions).
// This differs from the apparent size reported by os.Stat().Size().
func GetActualSize(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}

	// Get the underlying syscall.Stat_t to access block count
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		// Fallback to apparent size if we can't get the stat structure
		return fi.Size(), nil
	}

	// Calculate actual size: blocks * 512 bytes per block
	// (POSIX defines stat.st_blocks as 512-byte blocks)
	return int64(stat.Blocks) * 512, nil
}
