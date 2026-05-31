package agent

import (
	"context"
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
		"request file access approval for external read paths",
		"do not retry the same external operation through another tool",
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

func TestImmutableSystemBlocksIncludeDynamicSystemBlocks(t *testing.T) {
	a := NewAgentWithRegistry(nil, nil, core.NewToolRegistry(nil),
		WithDynamicSystemBlocks(func() string {
			return "Available workflows.\n\n- dead-code-scan [project]: Scan for dead code."
		}),
	)
	joined := strings.Join(a.buildImmutableSystemBlocks(), "\n\n")

	for _, want := range []string{
		"Available workflows.",
		"dead-code-scan [project]",
		"Current session mode: agent",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("system blocks missing %q:\n%s", want, joined)
		}
	}
}

func TestNaturalLanguageWorkflowPromptCanUseNamedWorkflowTool(t *testing.T) {
	provider := &catalogWorkflowProvider{}
	tool := &recordingWorkflowTool{}
	a := NewAgentWithRegistry(provider, NewInMemoryStore(), core.NewToolRegistry([]core.Tool{tool}),
		WithDynamicSystemBlocks(func() string {
			return "Available workflows.\n\nUse the workflow tool when the user names one of these workflows.\n\n- dead-code-scan [project]: Scan for dead code."
		}),
	)

	for range mustRunStreamWithOptions(t, a, "s-workflow-catalog", "run dead-code-scan workflow on this repo", RunOptions{}) {
	}

	if !provider.sawCatalog {
		t.Fatalf("provider did not see workflow catalog in system prompt")
	}
	if !provider.sawWorkflowTool {
		t.Fatalf("provider did not see workflow tool")
	}
	if !strings.Contains(tool.input, `"name":"dead-code-scan"`) {
		t.Fatalf("workflow tool was not called with named workflow, input=%q", tool.input)
	}
}

type catalogWorkflowProvider struct {
	calls           int
	sawCatalog      bool
	sawWorkflowTool bool
}

func (p *catalogWorkflowProvider) StreamResponse(_ context.Context, history []Message, tools []Tool) <-chan ProviderEvent {
	p.calls++
	for _, msg := range history {
		if msg.Role == RoleSystem && strings.Contains(msg.Text, "dead-code-scan [project]") {
			p.sawCatalog = true
		}
	}
	for _, tool := range tools {
		if tool.Name() == "workflow" {
			p.sawWorkflowTool = true
		}
	}
	if p.calls == 1 {
		return eventStream(toolUseEvent(toolCall("wf-1", "workflow", `{"name":"dead-code-scan","args":"run dead-code-scan workflow on this repo"}`)))
	}
	return eventStream(endTurnEvent("workflow launched"))
}

type recordingWorkflowTool struct {
	input string
}

func (t *recordingWorkflowTool) Name() string { return "workflow" }
func (t *recordingWorkflowTool) Description() string {
	return "Launch a workflow by name."
}
func (t *recordingWorkflowTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
			"args": map[string]any{},
		},
	}
}
func (t *recordingWorkflowTool) Run(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	t.input = call.Input
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "ok"}, nil
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
		"do not retry the same tool call or the same shell operation with another shell command",
		"Mode switching commands are /agent, /ask, and /plan",
		"Do not tell users to run /mode agent",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("system blocks missing %q:\n%s", want, joined)
		}
	}
}

func TestPlanModeInstructionsTreatExecutionRequestsAsPlanning(t *testing.T) {
	a := NewAgentWithRegistry(nil, nil, core.NewToolRegistry(nil), WithSessionMode(session.ModePlan))
	joined := strings.Join(a.buildImmutableSystemBlocks(), "\n\n")

	for _, want := range []string{
		"User intent, imperative wording",
		"create a branch",
		"treat it as a request to plan the execution",
		"do not retry the same tool call or the same shell operation with another shell command",
		"Do not run side-effectful commands",
		"Do not output slash commands such as /agent",
		"Only the user or UI can switch modes",
		"<proposed_plan>",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("plan mode instructions missing %q:\n%s", want, joined)
		}
	}
}

func TestImmutableSystemBlocksIncludeFocusOutputStyleOnlyInFocusView(t *testing.T) {
	a := NewAgentWithRegistry(nil, nil, core.NewToolRegistry(nil))

	defaultPrompt := strings.Join(a.buildImmutableSystemBlocks(RunOptions{ViewMode: "default"}), "\n\n")
	if strings.Contains(defaultPrompt, "Focus view is active") {
		t.Fatalf("default view should not include focus output style:\n%s", defaultPrompt)
	}

	focusPrompt := strings.Join(a.buildImmutableSystemBlocks(RunOptions{ViewMode: "focus"}), "\n\n")
	for _, want := range []string{
		"Focus view is active in the terminal.",
		"Emit text only when it changes what the user needs to know",
		"result, finding, blocker, risk, decision point",
		"Do not write assistant text merely to announce routine tool use",
		"file inspection, searching, reading, or continuing with the next obvious step",
	} {
		if !strings.Contains(focusPrompt, want) {
			t.Fatalf("focus output style missing %q:\n%s", want, focusPrompt)
		}
	}
}

func TestRenderToolSpecsMarksDynamicReadOnlyTools(t *testing.T) {
	block := renderToolSpecsBlock([]core.ToolSpec{
		{
			Name:          "shell_run",
			Description:   "Run a shell command",
			ReadOnlyCheck: func(map[string]any) bool { return true },
		},
		{
			Name:     "write",
			ReadOnly: false,
		},
	})

	for _, want := range []string{
		"shell_run [conditional read-only]",
		"some calls are allowed in read-only modes",
		"mutating inputs are blocked",
		"write [write]",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("tool specs block missing %q:\n%s", want, block)
		}
	}
}
