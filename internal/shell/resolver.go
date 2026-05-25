package shell

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Kind identifies the command language a resolved shell expects.
type Kind string

const (
	KindPOSIX      Kind = "posix"
	KindPowerShell Kind = "powershell"
	KindCmd        Kind = "cmd"
)

// Spec describes the executable and arguments used to run a shell command.
type Spec struct {
	Kind        Kind
	DisplayName string
	Bin         string
	Args        []string
}

// LookPathFunc matches exec.LookPath and allows tests to control discovery.
type LookPathFunc func(file string) (string, error)

// Resolver resolves shell commands for a target platform.
type Resolver struct {
	GOOS     string
	LookPath LookPathFunc
	Env      []string
}

// Resolve returns the shell command specification for the current platform.
func Resolve(command string) (Spec, error) {
	return Resolver{}.Resolve(command)
}

// Resolve returns the shell command specification for the resolver platform.
func (r Resolver) Resolve(command string) (Spec, error) {
	if r.goos() == "windows" {
		return r.resolveWindows(command), nil
	}
	return Spec{
		Kind:        KindPOSIX,
		DisplayName: "/bin/sh",
		Bin:         "/bin/sh",
		Args:        []string{"-lc", command},
	}, nil
}

func (r Resolver) resolveWindows(command string) Spec {
	if bin, err := r.lookPath("pwsh"); err == nil && strings.TrimSpace(bin) != "" {
		return powerShellSpec(bin, command)
	}
	if bin := envValue(r.env(), "ComSpec"); strings.TrimSpace(bin) != "" {
		return cmdSpec(bin, command)
	}
	if bin, err := r.lookPath("cmd.exe"); err == nil && strings.TrimSpace(bin) != "" {
		return cmdSpec(bin, command)
	}
	return cmdSpec("cmd.exe", command)
}

func powerShellSpec(bin, command string) Spec {
	return Spec{
		Kind:        KindPowerShell,
		DisplayName: "PowerShell",
		Bin:         bin,
		Args: []string{
			"-NoLogo",
			"-NoProfile",
			"-NonInteractive",
			"-Command",
			withPowerShellUTF8Output(command),
		},
	}
}

func cmdSpec(bin, command string) Spec {
	return Spec{
		Kind:        KindCmd,
		DisplayName: "cmd.exe",
		Bin:         bin,
		Args:        []string{"/d", "/c", withCmdUTF8Codepage(command)},
	}
}

const powerShellUTF8OutputPrefix = "[Console]::OutputEncoding=[System.Text.Encoding]::UTF8;$OutputEncoding=[System.Text.Encoding]::UTF8;"

func withPowerShellUTF8Output(command string) string {
	trimmed := strings.TrimSpace(command)
	if strings.HasPrefix(trimmed, powerShellUTF8OutputPrefix) {
		return command
	}
	if powerShellRequiresFirstStatement(trimmed) {
		return command
	}
	return powerShellUTF8OutputPrefix + "& {\n" + command + "\n}"
}

func powerShellRequiresFirstStatement(command string) bool {
	lower := strings.ToLower(command)
	return strings.HasPrefix(lower, "using ") ||
		strings.HasPrefix(lower, "using\t") ||
		strings.HasPrefix(lower, "param(") ||
		strings.HasPrefix(lower, "param ")
}

func withCmdUTF8Codepage(command string) string {
	trimmed := strings.TrimSpace(command)
	if strings.HasPrefix(strings.ToLower(trimmed), "chcp 65001") {
		return command
	}
	return "chcp 65001 >nul & " + command
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

func (r Resolver) env() []string {
	if r.Env != nil {
		return r.Env
	}
	return os.Environ()
}

func envValue(env []string, key string) string {
	for _, item := range env {
		name, value, ok := strings.Cut(item, "=")
		if ok && strings.EqualFold(name, key) {
			return value
		}
	}
	return ""
}
