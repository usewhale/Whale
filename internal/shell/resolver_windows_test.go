//go:build windows

package shell

import (
	"os"
	"os/exec"
	"path/filepath"
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

	out, err := Command(spec).CombinedOutput()
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

	out, err := Command(spec).CombinedOutput()
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

	out, err := Command(spec).CombinedOutput()
	if err != nil {
		t.Fatalf("resolved PowerShell command failed: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(string(out), "marker.txt") {
		t.Fatalf("output %q does not contain expected filename", string(out))
	}
}

func TestWindowsCmdSpecRunsQuotedChineseDirectoryCommand(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "ai项目", "whale开发")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	spec := cmdSpec("cmd.exe", `cd /d "`+workspace+`" && cd`)

	out, err := Command(spec).CombinedOutput()
	if err != nil {
		t.Fatalf("resolved cmd command failed: %v\nargs=%#v\noutput:\n%s", err, spec.Args, out)
	}
	if !strings.Contains(strings.ToLower(string(out)), strings.ToLower(workspace)) {
		t.Fatalf("output %q does not contain workspace %q", string(out), workspace)
	}
}
