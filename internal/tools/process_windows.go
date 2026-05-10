//go:build windows

package tools

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func configureShellCommand(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		return killShellCommandGroup(cmd)
	}
	cmd.WaitDelay = 2 * time.Second
}

func killShellCommandGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return os.ErrProcessDone
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return os.ErrProcessDone
	}
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := kill.Run(); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
