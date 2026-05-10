package shell

import (
	"errors"
	"os/exec"
	"runtime"
)

const (
	windowsPowerShellDisplayName = "PowerShell"
	unixShellDisplayName         = "/bin/sh"
)

var errPowerShellRequired = errors.New("PowerShell is required on Windows: install pwsh or ensure powershell.exe is available in PATH")

// ShellSpec describes the executable and arguments used to run a shell command.
type ShellSpec struct {
	Name        string
	DisplayName string
	Bin         string
	Args        []string
}

// LookPathFunc matches exec.LookPath and allows tests to control shell discovery.
type LookPathFunc func(file string) (string, error)

// Resolver resolves shell commands for a target operating system.
type Resolver struct {
	GOOS     string
	LookPath LookPathFunc
}

// Resolve returns the shell command specification for the current platform.
func Resolve(command string) (ShellSpec, error) {
	return Resolver{}.Resolve(command)
}

// ShellDisplayName returns the shell name that should be shown to users on the
// current platform.
func ShellDisplayName() string {
	return ShellDisplayNameForGOOS(runtime.GOOS)
}

// ShellDisplayNameForGOOS returns the shell display name for a target platform.
func ShellDisplayNameForGOOS(goos string) string {
	if goos == "windows" {
		return windowsPowerShellDisplayName
	}
	return unixShellDisplayName
}

// Resolve returns the shell command specification for the resolver platform.
func (r Resolver) Resolve(command string) (ShellSpec, error) {
	if r.goos() == "windows" {
		return r.resolveWindows(command)
	}
	return ShellSpec{
		Name:        "sh",
		DisplayName: unixShellDisplayName,
		Bin:         "/bin/sh",
		Args:        []string{"-lc", command},
	}, nil
}

func (r Resolver) resolveWindows(command string) (ShellSpec, error) {
	for _, name := range []string{"pwsh", "powershell.exe"} {
		bin, err := r.lookPath(name)
		if err == nil {
			return ShellSpec{
				Name:        name,
				DisplayName: windowsPowerShellDisplayName,
				Bin:         bin,
				Args: []string{
					"-NoLogo",
					"-NoProfile",
					"-NonInteractive",
					"-ExecutionPolicy",
					"Bypass",
					"-Command",
					command,
				},
			}, nil
		}
	}
	return ShellSpec{}, errPowerShellRequired
}

func (r Resolver) goos() string {
	if r.GOOS != "" {
		return r.GOOS
	}
	return runtime.GOOS
}

func (r Resolver) lookPath(name string) (string, error) {
	if r.LookPath != nil {
		return r.LookPath(name)
	}
	return exec.LookPath(name)
}
