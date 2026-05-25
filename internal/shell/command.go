//go:build !windows

package shell

import "os/exec"

// Command creates an exec.Cmd from a resolved shell spec.
func Command(spec Spec) *exec.Cmd {
	return exec.Command(spec.Bin, spec.Args...)
}
