//go:build !darwin && !linux

package fsops

// punchHole returns ErrNotSupported on platforms that don't support hole punching.
func punchHole(fd uintptr, offset, length int64) error {
	return ErrNotSupported
}
