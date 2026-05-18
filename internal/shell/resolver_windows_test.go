//go:build windows

package shell

import (
	"os/exec"
	"strings"
	"testing"
)

func TestWindowsResolveRunsCommand(t *testing.T) {
	const marker = "whale_windows_shell_resolver"

	spec, err := Resolve("echo " + marker)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if spec.Kind != KindPowerShell && spec.Kind != KindCmd {
		t.Fatalf("Kind = %q, want %q or %q", spec.Kind, KindPowerShell, KindCmd)
	}

	out, err := exec.Command(spec.Bin, spec.Args...).CombinedOutput()
	if err != nil {
		t.Fatalf("resolved shell command failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(string(out), marker) {
		t.Fatalf("output %q does not contain marker %q", string(out), marker)
	}
}

func TestWindowsResolveRunsUTF8CommandOutput(t *testing.T) {
	const marker = "世界 һ Привет"

	spec, err := Resolve("")
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	command := "echo " + marker
	if spec.Kind == KindPowerShell {
		command = "Write-Output '" + marker + "'"
	}
	spec, err = Resolve(command)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	out, err := exec.Command(spec.Bin, spec.Args...).CombinedOutput()
	if err != nil {
		t.Fatalf("resolved shell command failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(string(out), marker) {
		t.Fatalf("output %q does not contain UTF-8 marker %q", string(out), marker)
	}
}

func TestWindowsResolvePreservesPowerShellFirstStatementSemantics(t *testing.T) {
	bin, err := exec.LookPath("pwsh")
	if err != nil {
		bin, err = exec.LookPath("powershell.exe")
		if err != nil {
			t.Skip("PowerShell is not available")
		}
	}
	spec := powerShellSpec(bin, `using namespace System.IO; [Path]::GetFileName('C:\tmp\marker.txt')`)

	out, err := exec.Command(spec.Bin, spec.Args...).CombinedOutput()
	if err != nil {
		t.Fatalf("resolved PowerShell command failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(string(out), "marker.txt") {
		t.Fatalf("output %q does not contain expected filename", string(out))
	}
}
