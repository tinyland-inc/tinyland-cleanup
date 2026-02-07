//go:build linux

package fsops

import (
	"golang.org/x/sys/unix"
)

// punchHole punches a hole in a file on Linux systems using fallocate.
// This deallocates the specified region, freeing disk space while preserving
// the file's apparent size. Reads from the punched region will return zeros.
func punchHole(fd uintptr, offset, length int64) error {
	// Use fallocate with FALLOC_FL_PUNCH_HOLE | FALLOC_FL_KEEP_SIZE
	err := unix.Fallocate(
		int(fd),
		unix.FALLOC_FL_PUNCH_HOLE|unix.FALLOC_FL_KEEP_SIZE,
		offset,
		length,
	)
	if err != nil {
		return err
	}

	return nil
}
