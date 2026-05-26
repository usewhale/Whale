package agent

import (
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/shell"
)

func TestRuntimeEnvironmentBlockIncludesWorkspaceAndShellRunCWD(t *testing.T) {
	block := renderRuntimeBlock("/repo", runtimeWorktreeContext{}, shell.RuntimeDescription{
		GOOS: "linux",
		Spec: shell.Spec{Kind: shell.KindPOSIX, DisplayName: "/bin/sh"},
	})

	for _, want := range []string{
		"Current Whale runtime:",
		"OS: linux",
		"Current Whale workspace root: /repo",
		"Shell: /bin/sh (/bin/sh -lc)",
		"Shell commands run from the current Whale workspace by default",
		"shell_run cwd parameter",
		"path:\"codex\" means a codex entry under this workspace",
		"git -C ../codex",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("runtime block missing %q:\n%s", want, block)
		}
	}
}

func TestRuntimeEnvironmentBlockWindowsUsesCurrentShellRunName(t *testing.T) {
	block := renderRuntimeBlock(`C:\repo`, runtimeWorktreeContext{}, shell.RuntimeDescription{
		GOOS: "windows",
		Spec: shell.Spec{Kind: shell.KindPowerShell, DisplayName: "PowerShell"},
	})

	for _, want := range []string{
		"OS: windows",
		"Shell: PowerShell",
		`Current Whale workspace root: C:\repo`,
		"Use PowerShell syntax",
		"read_file",
		"Ask/Plan mode",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("windows runtime block missing %q:\n%s", want, block)
		}
	}
	for _, old := range []string{"exec" + "_shell", "always " + "PowerShell"} {
		if strings.Contains(block, old) {
			t.Fatalf("windows runtime block contains stale wording %q:\n%s", old, block)
		}
	}
}

func TestImmutableSystemBlocksIncludeRuntimeEnvironment(t *testing.T) {
	a := NewAgentWithRegistry(nil, nil, core.NewToolRegistry(nil), WithProjectMemory(false, 0, nil, "/repo"))
	joined := strings.Join(a.buildImmutableSystemBlocks(), "\n\n")

	if !strings.Contains(joined, "Current Whale runtime:") {
		t.Fatalf("system blocks missing runtime environment:\n%s", joined)
	}
	if !strings.Contains(joined, "Current Whale workspace root: /repo") {
		t.Fatalf("system blocks missing workspace root:\n%s", joined)
	}
	if !strings.Contains(joined, "shell_run cwd parameter") {
		t.Fatalf("system blocks missing shell_run cwd guidance:\n%s", joined)
	}
}

func TestRuntimeEnvironmentBlockIncludesWorktreeContext(t *testing.T) {
	block := renderRuntimeBlock("/repo/.whale/worktrees/feature", runtimeWorktreeContext{
		WorktreeRoot:      "/repo/.whale/worktrees/feature",
		OriginalWorkspace: "/repo",
	}, shell.RuntimeDescription{
		GOOS: "linux",
		Spec: shell.Spec{Kind: shell.KindPOSIX, DisplayName: "/bin/sh"},
	})

	for _, want := range []string{
		"Current worktree root: /repo/.whale/worktrees/feature",
		"Original workspace: /repo",
		"original workspace as reference-only",
		"do not cd to it",
		"run git -C against it",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("worktree runtime block missing %q:\n%s", want, block)
		}
	}
}

func TestImmutableSystemBlocksDeclareCurrentModeAuthoritatively(t *testing.T) {
	a := NewAgentWithRegistry(nil, nil, core.NewToolRegistry(nil), WithSessionMode(session.ModeAsk))
	joined := strings.Join(a.buildImmutableSystemBlocks(), "\n\n")

	for _, want := range []string{
		"Current session mode: ask",
		"claim the current mode is any other value as stale",
		"Ask mode is active.",
		"Mode switching commands are /agent, /ask, and /plan",
		"Do not tell users to run /mode agent",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("system blocks missing %q:\n%s", want, joined)
		}
	}
}
