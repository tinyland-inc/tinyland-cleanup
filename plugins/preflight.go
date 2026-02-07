package plugins

import (
	"fmt"
	"gitlab.com/tinyland/lab/tinyland-cleanup/config"
)

// PreflightResult contains the result of a pre-flight safety check.
type PreflightResult struct {
	Safe     bool
	Reason   string
	FreeGB   float64
	NeededGB float64
}

// PreflightOnlyShrink checks if a disk operation is safe under the only-shrink paradigm.
// It verifies that sufficient free space exists and that the estimated temporary space
// usage won't exceed safety limits.
func PreflightOnlyShrink(diskPath string, estimatedTempGB float64, cfg *config.SafetyConfig) PreflightResult {
	if cfg == nil || !cfg.OnlyShrink {
		return PreflightResult{Safe: true, Reason: "only-shrink not enforced"}
	}

	// Check max temp file limit
	if cfg.MaxTempFileGB > 0 && estimatedTempGB > cfg.MaxTempFileGB {
		return PreflightResult{
			Safe:     false,
			Reason:   fmt.Sprintf("estimated temp %.1fGB exceeds max_temp_file_gb %.1fGB", estimatedTempGB, cfg.MaxTempFileGB),
			NeededGB: estimatedTempGB,
		}
	}

	// For in-place operations, estimated temp should be 0
	if estimatedTempGB == 0 {
		return PreflightResult{Safe: true, Reason: "in-place operation, no temp space needed"}
	}

	// Check free space
	freeBytes, err := getFreeDiskSpace(diskPath)
	if err != nil {
		return PreflightResult{Safe: false, Reason: fmt.Sprintf("cannot check free space: %v", err)}
	}

	freeGB := float64(freeBytes) / (1024 * 1024 * 1024)
	neededGB := estimatedTempGB * cfg.PreflightSpaceMultiplier

	if freeGB < neededGB {
		return PreflightResult{
			Safe:     false,
			Reason:   fmt.Sprintf("insufficient free space: %.1fGB free, need %.1fGB (%.1fGB * %.1fx)", freeGB, neededGB, estimatedTempGB, cfg.PreflightSpaceMultiplier),
			FreeGB:   freeGB,
			NeededGB: neededGB,
		}
	}

	return PreflightResult{Safe: true, FreeGB: freeGB, NeededGB: neededGB}
}

// AssertOnlyShrink verifies that an operation only freed space (never consumed more).
// Returns an error if the after size is larger than the before size.
func AssertOnlyShrink(beforeBytes, afterBytes int64, opName string) error {
	if afterBytes > beforeBytes {
		return fmt.Errorf("ONLY-SHRINK violation in %s: size grew from %d to %d bytes (+%d)",
			opName, beforeBytes, afterBytes, afterBytes-beforeBytes)
	}
	return nil
}
