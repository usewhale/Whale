package shell

import (
	"errors"
	"strings"
	"testing"
)

func TestRuntimeDescriptionPOSIXGuidance(t *testing.T) {
	rt := Resolver{
		GOOS: "linux",
		LookPath: func(file string) (string, error) {
			t.Fatalf("LookPath should not be called for POSIX runtime")
			return "", errors.New("unexpected lookup")
		},
		Env: []string{"ComSpec=C:\\Windows\\System32\\cmd.exe"},
	}.DescribeRuntime()

	if rt.GOOS != "linux" || rt.Spec.Kind != KindPOSIX {
		t.Fatalf("unexpected runtime: %+v", rt)
	}
	assertContainsAll(t, rt.ToolGuidance(), []string{
		"OS: linux.",
		"Runtime shell: /bin/sh (/bin/sh -lc).",
		"Use POSIX /bin/sh syntax",
		"shell_run cwd",
	})
}

func TestRuntimeDescriptionPowerShellGuidance(t *testing.T) {
	rt := Resolver{
		GOOS: "windows",
		LookPath: func(file string) (string, error) {
			if file == "pwsh" {
				return `C:\Program Files\PowerShell\7\pwsh.exe`, nil
			}
			return "", errors.New("not found")
		},
		Env: []string{},
	}.DescribeRuntime()

	if rt.GOOS != "windows" || rt.Spec.Kind != KindPowerShell {
		t.Fatalf("unexpected runtime: %+v", rt)
	}
	assertContainsAll(t, rt.ToolGuidance(), []string{
		"OS: windows.",
		"Runtime shell: PowerShell (PowerShell -NoLogo -NoProfile -NonInteractive -Command).",
		"Use PowerShell syntax",
		"Get-ChildItem",
		"Select-String",
		"$env:TEMP",
		"$env:FOO",
		"read_file",
		"list_dir",
		"Ask/Plan mode",
		"Avoid POSIX-only assumptions",
	})
}

func TestRuntimeDescriptionCmdGuidance(t *testing.T) {
	rt := Resolver{
		GOOS: "windows",
		LookPath: func(file string) (string, error) {
			return "", errors.New("not found")
		},
		Env: []string{"ComSpec=C:\\Windows\\System32\\cmd.exe"},
	}.DescribeRuntime()

	if rt.GOOS != "windows" || rt.Spec.Kind != KindCmd {
		t.Fatalf("unexpected runtime: %+v", rt)
	}
	assertContainsAll(t, rt.ToolGuidance(), []string{
		"OS: windows.",
		"Runtime shell: cmd.exe (cmd.exe /d /c).",
		"Use cmd.exe syntax",
		"dir",
		"findstr",
		"%TEMP%",
		"%FOO%",
		"read_file",
		"list_dir",
		"Ask/Plan mode",
		"Avoid PowerShell-only syntax",
	})
}

func assertContainsAll(t *testing.T, got string, wants []string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}
