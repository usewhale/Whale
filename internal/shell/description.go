package shell

import "strings"

// RuntimeDescription describes the shell language the current runtime expects.
type RuntimeDescription struct {
	GOOS string
	Spec Spec
}

// DescribeRuntime describes the current host shell runtime.
func DescribeRuntime() RuntimeDescription {
	return Resolver{}.DescribeRuntime()
}

// DescribeRuntime describes the resolver's shell runtime.
func (r Resolver) DescribeRuntime() RuntimeDescription {
	spec, err := r.Resolve("")
	if err != nil {
		return RuntimeDescription{GOOS: r.goos()}
	}
	return RuntimeDescription{GOOS: r.goos(), Spec: spec}
}

// ShellSummary returns a compact user-facing shell execution summary.
func (d RuntimeDescription) ShellSummary() string {
	name := strings.TrimSpace(d.Spec.DisplayName)
	if name == "" {
		name = strings.TrimSpace(string(d.Spec.Kind))
	}
	if name == "" {
		name = "unknown shell"
	}
	if summary := d.executionSummary(); summary != "" {
		return name + " (" + summary + ")"
	}
	return name
}

// CommandGuidance returns model-facing command-language guidance for shell_run.
func (d RuntimeDescription) CommandGuidance() string {
	switch d.Spec.Kind {
	case KindPowerShell:
		return "Use PowerShell syntax for shell_run commands when approval is available. Use read_file, list_dir, grep, or search_files for no-approval inspection in Ask/Plan mode. For approved shell_run commands, use Get-ChildItem for listing files, Select-String or rg if installed for text search, $env:TEMP for temporary paths, and $env:FOO for environment variables. Avoid POSIX-only assumptions such as /tmp, grep -r, and /workspace."
	case KindCmd:
		return "Use cmd.exe syntax for shell_run commands when approval is available. Use read_file, list_dir, grep, or search_files for no-approval inspection in Ask/Plan mode. For approved shell_run commands, use dir for listing files, findstr for text search, %TEMP% for temporary paths, and %FOO% for environment variables. Avoid PowerShell-only syntax and POSIX-only assumptions such as /tmp, grep -r, and /workspace."
	case KindPOSIX:
		return "Use POSIX /bin/sh syntax for shell_run commands. Prefer relative paths or the shell_run cwd parameter for workspace subdirectories."
	default:
		return "Use syntax appropriate for the current shell runtime. Prefer relative paths or the shell_run cwd parameter for workspace subdirectories."
	}
}

// ToolGuidance returns a compact sentence suitable for a tool description.
func (d RuntimeDescription) ToolGuidance() string {
	parts := []string{}
	if d.GOOS != "" {
		parts = append(parts, "OS: "+d.GOOS+".")
	}
	parts = append(parts, "Runtime shell: "+d.ShellSummary()+".")
	if guidance := d.CommandGuidance(); guidance != "" {
		parts = append(parts, guidance)
	}
	return strings.Join(parts, " ")
}

func (d RuntimeDescription) executionSummary() string {
	switch d.Spec.Kind {
	case KindPowerShell:
		return "PowerShell -NoLogo -NoProfile -NonInteractive -Command"
	case KindCmd:
		return "cmd.exe /d /c"
	case KindPOSIX:
		return "/bin/sh -lc"
	default:
		return ""
	}
}
