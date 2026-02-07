package fsops

import (
	"io"
	"os"
)

// scanZeroRegions scans a file for contiguous regions of zero bytes.
// It reads the file in chunks of blockSize and identifies zero-filled blocks.
// Adjacent zero blocks are merged into single contiguous regions.
func scanZeroRegions(path string, blockSize int) ([]ZeroRegion, error) {
	if blockSize <= 0 {
		blockSize = DefaultBlockSize
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Pre-allocate buffer for reading
	buf := make([]byte, blockSize)
	var regions []ZeroRegion
	var currentRegion *ZeroRegion
	offset := int64(0)

	for {
		n, err := io.ReadFull(f, buf)

		// Check if we read any data
		if n > 0 {
			// Check if this block is all zeros
			if isZeroBlock(buf[:n]) {
				if currentRegion == nil {
					// Start a new zero region
					currentRegion = &ZeroRegion{
						Offset: offset,
						Length: int64(n),
					}
				} else {
					// Extend the current zero region
					currentRegion.Length += int64(n)
				}
			} else {
				// Non-zero block: close current region if exists
				if currentRegion != nil {
					regions = append(regions, *currentRegion)
					currentRegion = nil
				}
			}

			offset += int64(n)
		}

		// Handle read errors
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				// End of file reached
				break
			}
			return nil, err
		}
	}

	// Don't forget to add the last region if we ended on zeros
	if currentRegion != nil {
		regions = append(regions, *currentRegion)
	}

	return regions, nil
}

// isZeroBlock checks if all bytes in the buffer are zero.
// Uses early-exit loop for efficiency.
func isZeroBlock(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}
