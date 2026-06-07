//go:build !unix

package tools

import (
	"context"
	"errors"
	"os/exec"
)

func runShellPTY(_ context.Context, _ *exec.Cmd, _ *shellTask) error {
	return errors.New("PTY shell transport is not supported on this platform")
}

func shellPTYSupported() bool {
	return false
}

func runShellExecBoundaryPTY(ctx context.Context, dir, command string, task *shellTask) error {
	return runShellPTY(ctx, nil, task)
}

func shellExecBoundaryEnabled() bool {
	return false
}
