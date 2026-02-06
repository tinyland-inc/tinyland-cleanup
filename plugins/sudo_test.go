package plugins

import (
	"context"
	"testing"
)

func TestDetectSudo(t *testing.T) {
	// Just verify it doesn't panic - actual sudo availability depends on environment
	cap := DetectSudo(context.Background())

	// On most test environments, sudo binary should exist
	// but passwordless may not be configured
	_ = cap.Available
	_ = cap.Passwordless
}

func TestSudoCapabilityHasGroup(t *testing.T) {
	cap := SudoCapability{
		Groups: []string{"admin", "staff", "wheel"},
	}

	if !cap.HasGroup("admin") {
		t.Error("should find 'admin' group")
	}
	if !cap.HasGroup("ADMIN") {
		t.Error("HasGroup should be case-insensitive")
	}
	if !cap.HasGroup("wheel") {
		t.Error("should find 'wheel' group")
	}
	if cap.HasGroup("root") {
		t.Error("should not find 'root' group")
	}
}

func TestSudoCapabilityHasGroupEmpty(t *testing.T) {
	cap := SudoCapability{
		Groups: nil,
	}

	if cap.HasGroup("admin") {
		t.Error("should not find any group in empty list")
	}
}
