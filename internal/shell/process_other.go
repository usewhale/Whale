//go:build !unix && !windows

package shell

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"time"
)

// ConfigureCommand applies platform process settings for shell commands.
func ConfigureCommand(cmd *exec.Cmd) {
	cmd.WaitDelay = 2 * time.Second
}

func RunCommand(ctx context.Context, cmd *exec.Cmd) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ConfigureCommand(cmd)
	cancel := cmd.Cancel
	if err := cmd.Start(); err != nil {
		return err
	}
	return waitCommandContext(ctx, cmd, cancel)
}

type CommandCleanup struct {
	cmd  *exec.Cmd
	once sync.Once
	err  error
}

func AttachCommandCleanup(cmd *exec.Cmd) *CommandCleanup {
	return &CommandCleanup{cmd: cmd}
}

func (c *CommandCleanup) Cleanup() error {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return os.ErrProcessDone
	}
	c.once.Do(func() {
		c.err = c.cmd.Process.Kill()
	})
	return c.err
}
