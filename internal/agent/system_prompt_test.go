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

func TestRuntimeSystemBlocksIncludeRuntimeEnvironment(t *testing.T) {
	a := NewAgentWithRegistry(nil, nil, core.NewToolRegistry(nil), WithProjectMemory(false, 0, nil, "/repo"))
	immutable := strings.Join(a.buildImmutableSystemBlocks(), "\n\n")
	if strings.Contains(immutable, "Current Whale runtime:") || strings.Contains(immutable, "Current Whale workspace root: /repo") {
		t.Fatalf("runtime environment leaked into immutable system blocks:\n%s", immutable)
	}

	joined := strings.Join(a.buildRuntimeSystemBlocks(), "\n\n")

	if !strings.Contains(joined, "Current Whale runtime:") {
		t.Fatalf("runtime system blocks missing runtime environment:\n%s", joined)
	}
	if !strings.Contains(joined, "Current Whale workspace root: /repo") {
		t.Fatalf("runtime system blocks missing workspace root:\n%s", joined)
	}
	if !strings.Contains(joined, "shell_run cwd parameter") {
		t.Fatalf("runtime system blocks missing shell_run cwd guidance:\n%s", joined)
	}
}

func TestRuntimeSystemBlocksIncludeDynamicSystemBlocks(t *testing.T) {
	a := NewAgentWithRegistry(nil, nil, core.NewToolRegistry(nil),
		WithDynamicSystemBlocks(func() string {
			return "Available workflows.\n\n- dead-code-scan [project]: Scan for dead code."
		}),
	)
	immutable := strings.Join(a.buildImmutableSystemBlocks(), "\n\n")
	if strings.Contains(immutable, "Available workflows.") || strings.Contains(immutable, "dead-code-scan [project]") || strings.Contains(immutable, "Current session mode: agent") {
		t.Fatalf("dynamic system blocks leaked into immutable system blocks:\n%s", immutable)
	}

	joined := strings.Join(a.buildRuntimeSystemBlocks(), "\n\n")

	for _, want := range []string{
		"Available workflows.",
		"dead-code-scan [project]",
		"Current session mode: agent",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("runtime system blocks missing %q:\n%s", want, joined)
		}
	}
}

func TestImmutableSystemBlocksIncludeWorkflowAuthoringGuidance(t *testing.T) {
	a := NewAgentWithRegistry(nil, nil, core.NewToolRegistry(nil), WithProjectMemory(false, 0, nil, "/repo"))
	joined := strings.Join(a.buildImmutableSystemBlocks(), "\n\n")

	for _, want := range []string{
		"Workflow authoring.",
		"use the workflow tool with both script and saveAs",
		"Do not inspect existing workflow directories, load skills, or pre-read repository files",
		"Claude Code-compatible raw JavaScript workflow",
		"array of objects such as { title:",
		"tool-scoped workers",
		"Call phase(\"Name\") only as a statement",
		"Await async workflow primitives before reading their results",
		"Call agent(prompt, { label, phase, schema",
		"Never hard-code Claude model names",
		"saveAs must exactly match meta.name",
		"enum, include an explicit type",
		"returning a final JSON-serializable result",
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

func TestRuntimeSystemBlocksDeclareCurrentModeAuthoritatively(t *testing.T) {
	a := NewAgentWithRegistry(nil, nil, core.NewToolRegistry(nil), WithSessionMode(session.ModeAsk))
	immutable := strings.Join(a.buildImmutableSystemBlocks(), "\n\n")
	if strings.Contains(immutable, "Current session mode: ask") || strings.Contains(immutable, "Ask mode is active.") {
		t.Fatalf("mode authority leaked into immutable system blocks:\n%s", immutable)
	}
	for _, want := range []string{
		"Mode switching commands are /agent, /ask, and /plan",
		"Do not tell users to run /mode agent",
	} {
		if !strings.Contains(immutable, want) {
			t.Fatalf("immutable mode switching guidance missing %q:\n%s", want, immutable)
		}
	}

	joined := strings.Join(a.buildRuntimeSystemBlocks(), "\n\n")

	for _, want := range []string{
		"Current session mode: ask",
		"claim the current mode is any other value as stale",
		"Ask mode is active.",
		"do not retry the same tool call or the same shell operation with another shell command",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("runtime system blocks missing %q:\n%s", want, joined)
		}
	}
}

func TestPlanModeInstructionsTreatExecutionRequestsAsPlanning(t *testing.T) {
	a := NewAgentWithRegistry(nil, nil, core.NewToolRegistry(nil), WithSessionMode(session.ModePlan))
	immutable := strings.Join(a.buildImmutableSystemBlocks(), "\n\n")
	if strings.Contains(immutable, "PLAN mode") || strings.Contains(immutable, "Do not run side-effectful commands") {
		t.Fatalf("plan mode instructions leaked into immutable system blocks:\n%s", immutable)
	}

	joined := strings.Join(a.buildRuntimeSystemBlocks(), "\n\n")

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
			t.Fatalf("runtime plan mode instructions missing %q:\n%s", want, joined)
		}
	}
}

func TestRuntimeSystemBlocksIncludeFocusOutputStyleOnlyInFocusView(t *testing.T) {
	a := NewAgentWithRegistry(nil, nil, core.NewToolRegistry(nil))

	immutable := strings.Join(a.buildImmutableSystemBlocks(RunOptions{ViewMode: "focus"}), "\n\n")
	if strings.Contains(immutable, "Focus view is active") {
		t.Fatalf("focus output style leaked into immutable system blocks:\n%s", immutable)
	}

	defaultPrompt := strings.Join(a.buildRuntimeSystemBlocks(RunOptions{ViewMode: "default"}), "\n\n")
	if strings.Contains(defaultPrompt, "Focus view is active") {
		t.Fatalf("default runtime should not include focus output style:\n%s", defaultPrompt)
	}

	focusPrompt := strings.Join(a.buildRuntimeSystemBlocks(RunOptions{ViewMode: "focus"}), "\n\n")
	for _, want := range []string{
		"Focus view is active in the terminal.",
		"Emit text only when it changes what the user needs to know",
		"result, finding, blocker, risk, decision point",
		"Do not write assistant text merely to announce routine tool use",
		"file inspection, searching, reading, or continuing with the next obvious step",
	} {
		if !strings.Contains(focusPrompt, want) {
			t.Fatalf("runtime focus output style missing %q:\n%s", want, focusPrompt)
		}
	}
}

func TestImmutableSystemPromptUsesStableToolPolicyWithoutToolCatalog(t *testing.T) {
	tools := core.NewToolRegistry([]core.Tool{&recordingWorkflowTool{}})
	a := NewAgentWithRegistry(nil, nil, tools)
	joined := strings.Join(a.buildImmutableSystemBlocks(), "\n\n")

	for _, want := range []string{
		"Tool use policy.",
		"provider tool schema",
		"Choose tools by exact name and schema",
		"do not invent tools",
		"Prefer read-only inspection tools",
		"read_file, list_dir, grep, search_files",
		"Mutating tools such as apply_patch, edit, write",
		"may be blocked by mode, policy, or user approval",
		"shell_run can be read-only only for safe inspection commands accepted by policy",
		"do not retry the same action through another tool",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("tool policy missing %q:\n%s", want, joined)
		}
	}
	for _, notWant := range []string{
		"Available tools",
		"No tools are available.",
		"workflow [",
		"Launch a workflow by name.",
		" args:",
		" approval:",
	} {
		if strings.Contains(joined, notWant) {
			t.Fatalf("system prompt leaked tool catalog text %q:\n%s", notWant, joined)
		}
	}
}

func TestImmutableSystemPromptToolPolicyDoesNotDependOnToolRegistry(t *testing.T) {
	withoutTools := NewAgentWithRegistry(nil, nil, core.NewToolRegistry(nil))
	withTools := NewAgentWithRegistry(nil, nil, core.NewToolRegistry([]core.Tool{&recordingWorkflowTool{}}))

	a := strings.Join(withoutTools.buildImmutableSystemBlocks(), "\n\n")
	b := strings.Join(withTools.buildImmutableSystemBlocks(), "\n\n")
	if a != b {
		t.Fatalf("immutable system prompt changed with tool registry\nwithout tools:\n%s\n\nwith tools:\n%s", a, b)
	}
}
