package tools

import (
	"strings"
	"testing"
)

func TestExecShellDescriptionWindows(t *testing.T) {
	desc := execShellDescription("windows")

	for _, want := range []string{
		"Commands are executed with PowerShell on Windows",
		"PowerShell syntax",
		"Windows-compatible paths",
		"Windows environment variables",
		"Do not assume /tmp, grep -r, bash syntax, or Linux-only shell behavior",
		"Get-ChildItem",
		"Get-Content",
		"Select-String",
		"Test-Path",
		"Measure-Object",
		"Commands default to the workspace root",
		"set cwd to a subdirectory inside the workspace",
		"instead of prefixing commands with cd",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q:\n%s", want, desc)
		}
	}
}

func TestExecShellDescriptionLinux(t *testing.T) {
	desc := execShellDescription("linux")

	for _, want := range []string{
		"Commands are executed with /bin/sh",
		"Commands default to the workspace root",
		"set cwd to a subdirectory inside the workspace",
		"instead of prefixing commands with cd",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q:\n%s", want, desc)
		}
	}

	for _, forbidden := range []string{
		"PowerShell",
		"Windows-compatible",
		"Windows environment variables",
		"Get-ChildItem",
		"Select-String",
		"Measure-Object",
		"Do not assume /tmp, grep -r, bash syntax, or Linux-only shell behavior",
	} {
		if strings.Contains(desc, forbidden) {
			t.Fatalf("description contains Windows-only text %q:\n%s", forbidden, desc)
		}
	}
}

func TestShellReadOnlyCheckWindows(t *testing.T) {
	for _, command := range []string{
		"Select-String TODO -Path *.go",
		"Get-Content README.md -Head 20",
		"Get-ChildItem -Recurse",
		"get-childitem -recurse",
		"git status --short",
		"go test ./...",
		"python --version",
		"node --version",
		"npm --version",
		"npx --version",
	} {
		if !shellReadOnlyCheckForGOOS("windows", map[string]any{"command": command}) {
			t.Fatalf("expected Windows command to be read-only: %s", command)
		}
	}

	for _, command := range []string{
		"grep TODO README.md",
		"cat README.md",
		"cargo test",
		"python3 --version",
		"Set-Content README.md value",
	} {
		if shellReadOnlyCheckForGOOS("windows", map[string]any{"command": command}) {
			t.Fatalf("expected Windows command not to be read-only: %s", command)
		}
	}
}

func TestShellReadOnlyCheckLinux(t *testing.T) {
	for _, command := range []string{
		"grep TODO README.md",
		"cat README.md",
		"cargo test",
		"cargo check --all-targets",
		"cargo clippy",
		"rustc --version",
		"python3 --version",
		"git diff --stat",
		"go vet ./...",
	} {
		if !shellReadOnlyCheckForGOOS("linux", map[string]any{"command": command}) {
			t.Fatalf("expected Linux command to be read-only: %s", command)
		}
	}

	for _, command := range []string{
		"Select-String TODO -Path *.go",
		"Get-Content README.md -Head 20",
		"Get-ChildItem -Recurse",
		"rm README.md",
	} {
		if shellReadOnlyCheckForGOOS("linux", map[string]any{"command": command}) {
			t.Fatalf("expected Linux command not to be read-only: %s", command)
		}
	}
}
