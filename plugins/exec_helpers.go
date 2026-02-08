package plugins

import (
	"errors"
	"os/exec"
	"time"
)

// safeOutput runs cmd.Output() with WaitDelay to prevent pipe-inheritance hangs.
//
// Some commands (brew, brctl, nix-collect-garbage, etc.) fork child processes
// that inherit the parent's stdout/stderr pipe file descriptors. When the main
// process exits, Output() blocks forever waiting for these orphaned children
// to close the inherited pipes. WaitDelay tells Go's exec package to give up
// waiting for pipe goroutines after the specified duration once the main
// process has exited.
//
// If WaitDelay fires, the output buffer already contains all data written by
// the main process (children write to their own copies of the FDs), so we
// treat ErrWaitDelay as success.
func safeOutput(cmd *exec.Cmd) ([]byte, error) {
	cmd.WaitDelay = 10 * time.Second
	out, err := cmd.Output()
	if err != nil && errors.Is(err, exec.ErrWaitDelay) {
		return out, nil
	}
	return out, err
}

// safeCombinedOutput runs cmd.CombinedOutput() with WaitDelay to prevent
// pipe-inheritance hangs. See safeOutput for details.
func safeCombinedOutput(cmd *exec.Cmd) ([]byte, error) {
	cmd.WaitDelay = 10 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil && errors.Is(err, exec.ErrWaitDelay) {
		return out, nil
	}
	return out, err
}
