//go:build windows

package shell

import (
	"os/exec"
	"strings"
	"syscall"
)

// Command creates an exec.Cmd from a resolved shell spec.
func Command(spec Spec) *exec.Cmd {
	cmd := exec.Command(spec.Bin, spec.Args...)
	if spec.Kind == KindCmd {
		cmd = exec.Command(spec.Bin)
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: strings.Join(spec.Args, " ")}
	}
	return cmd
}
