//go:build darwin

package fsops

import (
	"golang.org/x/sys/unix"
)

// punchHole punches a hole in a file on Darwin systems using F_PUNCHHOLE.
// This deallocates the specified region, freeing disk space while preserving
// the file's apparent size. Reads from the punched region will return zeros.
func punchHole(fd uintptr, offset, length int64) error {
	// Set up the fstore structure for F_PUNCHHOLE
	fstore := unix.Fstore_t{
		Flags:      0,      // No special flags
		Posmode:    0,      // Absolute offset
		Offset:     offset, // Start of hole
		Length:     length, // Length of hole
		Bytesalloc: 0,      // Unused for F_PUNCHHOLE
	}

	// Call fcntl with F_PUNCHHOLE
	err := unix.FcntlFstore(fd, unix.F_PUNCHHOLE, &fstore)
	if err != nil {
		return err
	}

	return nil
}
