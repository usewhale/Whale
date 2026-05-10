package agent

import (
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func TestRenderRuntimeEnvironmentBlockWindows(t *testing.T) {
	block := renderRuntimeEnvironmentBlock("windows", "PowerShell", `C:\work\whale`)

	for _, want := range []string{
		"Runtime environment:",
		"Current OS: windows",
		"Current shell for exec_shell: PowerShell",
		`Current Whale workspace root: C:\work\whale`,
		"PowerShell syntax",
		"Windows-compatible paths",
		"Windows environment variables",
		"/tmp",
		"grep -r",
		"bash syntax",
		"POSIX variable assignment",
		"Linux-only shell behavior",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("Windows runtime block missing %q:\n%s", want, block)
		}
	}
}

func TestRenderRuntimeEnvironmentBlockLinux(t *testing.T) {
	block := renderRuntimeEnvironmentBlock("linux", "/bin/sh", "/home/me/whale")

	for _, want := range []string{
		"Runtime environment:",
		"Current OS: linux",
		"Current shell for exec_shell: /bin/sh",
		"Current Whale workspace root: /home/me/whale",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("Linux runtime block missing %q:\n%s", want, block)
		}
	}
	for _, unwanted := range []string{
		"PowerShell",
		"grep -r",
		"bash syntax",
		"Windows-compatible paths",
		"Windows environment variables",
	} {
		if strings.Contains(block, unwanted) {
			t.Fatalf("Linux runtime block should not contain %q:\n%s", unwanted, block)
		}
	}
}

func TestImmutableSystemPromptIncludesRuntimeEnvironment(t *testing.T) {
	a := &Agent{
		tools:                core.NewToolRegistry(nil),
		projectMemoryEnabled: false,
		workspaceRoot:        "/repo",
	}
	joined := strings.Join(a.buildImmutableSystemBlocks(), "\n\n")

	for _, want := range []string{
		"Runtime environment:",
		"Current shell for exec_shell:",
		"Current Whale workspace root: /repo",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, joined)
		}
	}
}
