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
