package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/store"
)

func TestParallelReasonPreservesOrderAndAggregatesUsage(t *testing.T) {
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(ctx context.Context, history []core.Message, tools []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				if len(tools) != 0 {
					t.Errorf("parallel_reason tools: want none, got %d", len(tools))
				}
				prompt := history[len(history)-1].Text
				out <- llm.ProviderEvent{
					Type: llm.EventComplete,
					Response: &llm.ProviderResponse{
						Content: "answer:" + prompt,
						Usage:   llm.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
					},
				}
			}()
			return out
		}), nil
	}
	r := NewRunner(RunnerConfig{ProviderFactory: factory})
	res, err := r.ParallelReason(context.Background(), ParallelReasonRequest{Prompts: []string{"b", "a", "c"}})
	if err != nil {
		t.Fatalf("ParallelReason: %v", err)
	}
	for i, prompt := range []string{"b", "a", "c"} {
		if res.Results[i].Index != i || res.Results[i].Prompt != prompt || res.Results[i].Output != "answer:"+prompt {
			t.Fatalf("result[%d] = %+v", i, res.Results[i])
		}
	}
	if res.Usage.TotalTokens != 9 || res.Usage.PromptTokens != 3 || res.Usage.CompletionTokens != 6 {
		t.Fatalf("usage = %+v", res.Usage)
	}
}

func TestParallelReasonAllowsOptionsOnlyProviderFactory(t *testing.T) {
	factory := func(req ProviderRequest) (llm.Provider, error) {
		if req.Model == "" {
			t.Errorf("model was not passed through options factory")
		}
		return providerFunc(func(ctx context.Context, history []core.Message, tools []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				out <- llm.ProviderEvent{
					Type: llm.EventComplete,
					Response: &llm.ProviderResponse{
						Content: "ok",
						Usage:   llm.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
					},
				}
			}()
			return out
		}), nil
	}
	r := NewRunner(RunnerConfig{ProviderFactoryWithOptions: factory, DefaultModel: "deepseek-v4-flash"})
	res, err := r.ParallelReason(context.Background(), ParallelReasonRequest{Prompts: []string{"check"}})
	if err != nil {
		t.Fatalf("ParallelReason: %v", err)
	}
	if len(res.Results) != 1 || res.Results[0].Output != "ok" {
		t.Fatalf("results = %+v", res.Results)
	}
}

func TestSpawnSubagentDescriptionWarnsAboutFreshChildCost(t *testing.T) {
	desc := spawnSubagentTool{}.Description()
	for _, want := range []string{
		"Prefer direct tools",
		"10+ read/search steps",
		"prefix-cache miss",
		"full child loop",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q: %s", want, desc)
		}
	}
}

func TestSpawnSubagentSchemaOmitsInlineAgentDefinition(t *testing.T) {
	spec := core.DescribeTool(spawnSubagentTool{})
	props := spec.Parameters["properties"].(map[string]any)
	if _, ok := props["agent"]; ok {
		t.Fatalf("spawn_subagent schema should not expose inline agent definitions: %+v", props["agent"])
	}
	b, err := json.Marshal(spec.Parameters)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	for _, forbidden := range []string{"\"hooks\"", "\"generation\"", "\"mcpServers\"", "\"permissionMode\"", "\"assistantPrefix\""} {
		if strings.Contains(string(b), forbidden) {
			t.Fatalf("spawn_subagent schema leaked %s: %s", forbidden, string(b))
		}
	}
	if len(b) > 3000 {
		t.Fatalf("spawn_subagent schema is too large after slimming: %d bytes", len(b))
	}
}

func TestSpawnSubagentToolReturnsSessionBudgetHint(t *testing.T) {
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				out <- llm.ProviderEvent{
					Type: llm.EventComplete,
					Response: &llm.ProviderResponse{
						Content: "done",
						Usage:   llm.Usage{PromptTokens: 20_000, CompletionTokens: 1_000, TotalTokens: 21_000},
					},
				}
			}()
			return out
		}), nil
	}
	dir := t.TempDir()
	msgStore, err := store.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	parent := core.NewToolRegistry([]core.Tool{testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}}})
	r := NewRunner(RunnerConfig{
		ProviderFactory: factory,
		ParentTools:     parent,
		MessageStore:    msgStore,
		SessionsDir:     dir,
		ParentSessionID: "parent-session",
	})
	tool := spawnSubagentTool{runner: r}

	first := runSpawnSubagentToolForBudget(t, tool, "tc-budget-1")
	firstBudget := first["subagent_budget"].(map[string]any)
	if firstBudget["spawn_count"].(float64) != 1 || firstBudget["hint"] != nil {
		t.Fatalf("first budget = %+v, want count without hint", firstBudget)
	}

	second := runSpawnSubagentToolForBudget(t, tool, "tc-budget-2")
	secondBudget := second["subagent_budget"].(map[string]any)
	if secondBudget["spawn_count"].(float64) != 2 {
		t.Fatalf("second budget = %+v, want count 2", secondBudget)
	}
	hint, _ := secondBudget["hint"].(string)
	if !strings.Contains(hint, "prefix-cache miss") {
		t.Fatalf("second hint missing cache warning: %+v", secondBudget)
	}

	third := runSpawnSubagentToolForBudget(t, tool, "tc-budget-3")
	thirdBudget := third["subagent_budget"].(map[string]any)
	hint, _ = thirdBudget["hint"].(string)
	if !strings.Contains(hint, "budget is high") || thirdBudget["total_tokens"].(float64) != 63_000 {
		t.Fatalf("third budget = %+v, want strong high-budget hint at 63k tokens", thirdBudget)
	}
	if first["tool_mode"] != "model_only" {
		t.Fatalf("tool_mode = %v, want model_only", first["tool_mode"])
	}
	if got := jsonArrayLen(first["requested_tools"]); got != 0 {
		t.Fatalf("requested_tools length = %d, want 0: %+v", got, first["requested_tools"])
	}
	if got := jsonArrayLen(first["resolved_tools"]); got != 0 {
		t.Fatalf("resolved_tools length = %d, want 0: %+v", got, first["resolved_tools"])
	}
}

func runSpawnSubagentToolForBudget(t *testing.T, tool spawnSubagentTool, callID string) map[string]any {
	t.Helper()
	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    callID,
		Name:  "spawn_subagent",
		Input: `{"task":"inspect","role":"review","tools":[]}`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError() {
		t.Fatalf("unexpected tool error: %s", res.ModelText)
	}
	env, ok := core.ParseToolEnvelope(res.ModelText)
	if !ok {
		t.Fatalf("parse envelope failed: %s", res.ModelText)
	}
	return env.Data
}

func TestSpawnSubagentToolReadOnlyCheckGatesMutatingLaunches(t *testing.T) {
	spec := core.DescribeTool(spawnSubagentTool{})
	tests := []struct {
		name     string
		input    string
		readOnly bool
	}{
		{
			name:     "default child is read-only",
			input:    `{"task":"inspect files"}`,
			readOnly: true,
		},
		{
			name:     "workspace write capability needs approval",
			input:    `{"task":"edit files","tools":["workspace.write"]}`,
			readOnly: false,
		},
		{
			name:     "shell run capability needs approval",
			input:    `{"task":"run tests","tools":["shell.run"]}`,
			readOnly: false,
		},
		{
			name:     "inline agent write tool needs approval",
			input:    `{"task":"edit files","agent":{"tools":["workspace.write"]}}`,
			readOnly: false,
		},
		{
			name:     "inline read-only mode is not model-facing",
			input:    `{"task":"inspect files","agent":{"permissionMode":"read_only"}}`,
			readOnly: false,
		},
		{
			name:     "inline command hook needs approval",
			input:    `{"task":"inspect files","agent":{"hooks":{"PreToolUse":[{"type":"command","command":"touch owned"}]}}}`,
			readOnly: false,
		},
		{
			name:     "inline prompt hook needs approval",
			input:    `{"task":"inspect files","agent":{"hooks":{"PreToolUse":[{"type":"prompt","prompt":"decide whether to allow this tool"}]}}}`,
			readOnly: false,
		},
		{
			name:     "inline http hook needs approval",
			input:    `{"task":"inspect files","agent":{"hooks":{"PreToolUse":[{"type":"http","url":"https://example.com/hook"}]}}}`,
			readOnly: false,
		},
		{
			name:     "inline agent hook needs approval",
			input:    `{"task":"inspect files","agent":{"hooks":{"PreToolUse":[{"type":"agent","prompt":"review this tool call"}]}}}`,
			readOnly: false,
		},
		{
			name:     "ask mode needs approval",
			input:    `{"task":"maybe edit","agent":{"permissionMode":"ask"}}`,
			readOnly: false,
		},
		{
			name:     "auto mode needs approval",
			input:    `{"task":"edit files","agent":{"permissionMode":"auto"}}`,
			readOnly: false,
		},
		{
			name:     "trusted mode needs approval",
			input:    `{"task":"edit files","agent":{"permissionMode":"trusted"}}`,
			readOnly: false,
		},
		{
			name:     "worktree isolation needs approval",
			input:    `{"task":"inspect in isolation","agent":{"isolation":"worktree"}}`,
			readOnly: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			call := core.ToolCall{ID: "call-1", Name: spec.Name, Input: tc.input}
			if got := core.IsReadOnlyToolCall(spec, call); got != tc.readOnly {
				t.Fatalf("IsReadOnlyToolCall() = %v, want %v", got, tc.readOnly)
			}
		})
	}
}

func TestSpawnSubagentToolRejectsInlineAgentDefinitionInput(t *testing.T) {
	tool := spawnSubagentTool{runner: NewRunner(RunnerConfig{})}
	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    "tc-inline-agent",
		Name:  "spawn_subagent",
		Input: `{"task":"inspect","agent":{"prompt":"act as reviewer"}}`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError() {
		t.Fatalf("expected inline agent definition error: %+v", res)
	}
	env, ok := core.ParseToolEnvelope(res.ModelText)
	if !ok {
		t.Fatalf("parse envelope failed: %s", res.ModelText)
	}
	if env.Code != "invalid_input" || !strings.Contains(env.Message, "inline agent definitions") {
		t.Fatalf("unexpected envelope: %+v", env)
	}
}

func TestSpawnSubagentToolRejectsDeprecatedCapabilitiesInput(t *testing.T) {
	tool := spawnSubagentTool{runner: NewRunner(RunnerConfig{})}
	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    "tc-deprecated",
		Name:  "spawn_subagent",
		Input: `{"task":"inspect","capabilities":["workspace.read"]}`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError() {
		t.Fatalf("expected deprecated capabilities error: %+v", res)
	}
	env, ok := core.ParseToolEnvelope(res.ModelText)
	if !ok {
		t.Fatalf("parse envelope failed: %s", res.ModelText)
	}
	if env.Code != "invalid_input" || !strings.Contains(env.Message, "use tools instead") {
		t.Fatalf("unexpected envelope: %+v", env)
	}
}

func TestSpawnSubagentToolRejectsWildcardToolsSelector(t *testing.T) {
	factoryCalled := false
	factory := func(_ string, _ int) (llm.Provider, error) {
		factoryCalled = true
		return providerFunc(func(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent)
			close(out)
			return out
		}), nil
	}
	parent := core.NewToolRegistry([]core.Tool{testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}}})
	tool := spawnSubagentTool{runner: NewRunner(RunnerConfig{ProviderFactory: factory, ParentTools: parent})}
	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    "tc-wildcard",
		Name:  "spawn_subagent",
		Input: `{"task":"inspect","tools":["*"]}`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError() {
		t.Fatalf("expected wildcard selector error: %+v", res)
	}
	if factoryCalled {
		t.Fatal("provider should not run after wildcard selector rejection")
	}
	if !strings.Contains(res.ModelText, "only supported by fork/trusted child agents") {
		t.Fatalf("unexpected wildcard error: %s", res.ModelText)
	}
}

func TestSpawnSubagentToolReadOnlyCheckGatesNamedMutatingAgents(t *testing.T) {
	root := t.TempDir()
	agentsDir := filepath.Join(root, ".whale", "agents")
	if err := os.MkdirAll(agentsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "writer.md"), []byte(`---
name: writer
description: Writes files
tools: [workspace.write]
permissionMode: auto
---
Write files.
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "hooked.json"), []byte(`{
  "name": "hooked",
  "description": "Runs hooks",
  "hooks": {
    "PreToolUse": [
      { "type": "command", "command": "touch owned" }
    ]
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "prompt-hooked.json"), []byte(`{
  "name": "prompt-hooked",
  "description": "Runs prompt hooks",
  "hooks": {
    "PreToolUse": [
      { "type": "prompt", "prompt": "decide whether to allow this tool" }
    ]
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := core.DescribeTool(spawnSubagentTool{
		runner: &Runner{agentDefinitions: NewAgentDefinitionLibrary(root)},
	})
	tests := []struct {
		name     string
		input    string
		readOnly bool
	}{
		{
			name:     "builtin review remains read-only",
			input:    `{"task":"inspect files","role":"review"}`,
			readOnly: true,
		},
		{
			name:     "named writer needs approval",
			input:    `{"task":"edit files","role":"writer"}`,
			readOnly: false,
		},
		{
			name:     "named hooked agent needs approval",
			input:    `{"task":"inspect files","role":"hooked"}`,
			readOnly: false,
		},
		{
			name:     "named prompt hooked agent needs approval",
			input:    `{"task":"inspect files","role":"prompt-hooked"}`,
			readOnly: false,
		},
		{
			name:     "unknown role needs approval",
			input:    `{"task":"inspect files","role":"missing"}`,
			readOnly: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			call := core.ToolCall{ID: "call-1", Name: spec.Name, Input: tc.input}
			if got := core.IsReadOnlyToolCall(spec, call); got != tc.readOnly {
				t.Fatalf("IsReadOnlyToolCall() = %v, want %v", got, tc.readOnly)
			}
		})
	}
}

func TestParallelReasonCancellation(t *testing.T) {
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(ctx context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent)
			go func() {
				defer close(out)
				<-ctx.Done()
				out <- llm.ProviderEvent{Type: llm.EventError, Err: ctx.Err()}
			}()
			return out
		}), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := NewRunner(RunnerConfig{ProviderFactory: factory})
	res, err := r.ParallelReason(ctx, ParallelReasonRequest{Prompts: []string{"one"}})
	if err != nil {
		t.Fatalf("ParallelReason should return per-result cancellation, got error: %v", err)
	}
	if !strings.Contains(res.Results[0].Error, "context canceled") {
		t.Fatalf("cancel error = %q", res.Results[0].Error)
	}
}

func TestReadOnlyRegistryFiltersMutatingAndTaskTools(t *testing.T) {
	parent := core.NewToolRegistry([]core.Tool{
		testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}},
		testTool{name: "write", readOnly: false},
		testTool{name: "apply_patch", readOnly: false},
		testTool{name: "shell_run", readOnly: false, readOnlyCheck: func(args map[string]any) bool { return true }},
		testTool{name: "shell_wait", readOnly: true},
		testTool{name: "todo_add", readOnly: true},
		testTool{name: "parallel_reason", readOnly: true},
	})
	child, err := BuildReadOnlyRegistry(parent)
	if err != nil {
		t.Fatalf("BuildReadOnlyRegistry: %v", err)
	}
	if child.Get("read_file") == nil {
		t.Fatalf("expected read_file")
	}
	for _, name := range []string{"write", "apply_patch", "shell_run", "shell_wait", "todo_add", "parallel_reason"} {
		if child.Get(name) != nil {
			t.Fatalf("expected %s to be filtered", name)
		}
	}
}

func TestReadOnlyRegistryPreservesProgressRunner(t *testing.T) {
	parent := core.NewToolRegistry([]core.Tool{
		progressTool{testTool: testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}}},
	})
	child, err := BuildReadOnlyRegistry(parent)
	if err != nil {
		t.Fatalf("BuildReadOnlyRegistry: %v", err)
	}
	var progress []core.ToolProgress
	res, err := child.DispatchWithProgress(context.Background(), core.ToolCall{
		ID:    "read-1",
		Name:  "read_file",
		Input: `{}`,
	}, func(p core.ToolProgress) {
		progress = append(progress, p)
	})
	if err != nil {
		t.Fatalf("DispatchWithProgress: %v", err)
	}
	if res.IsError() {
		t.Fatalf("unexpected result error: %+v", res)
	}
	if len(progress) != 1 {
		t.Fatalf("expected wrapped read-only tool progress, got %+v", progress)
	}
	if progress[0].ToolCallID != "read-1" || progress[0].ToolName != "read_file" || progress[0].Summary != "progress from child read" {
		t.Fatalf("unexpected progress: %+v", progress[0])
	}
}

func TestCapabilityRegistryFiltersByCapability(t *testing.T) {
	parent := core.NewToolRegistry([]core.Tool{
		testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}},
		testTool{name: "web_search", readOnly: true, capabilities: []string{CapabilityWebSearch}},
		testTool{name: "web_fetch", readOnly: true, capabilities: []string{CapabilityWebFetch}},
		testTool{name: "write", readOnly: false, capabilities: []string{CapabilityWorkspaceRead}},
	})
	child, err := BuildCapabilityRegistry(parent, []string{CapabilityWebSearch})
	if err != nil {
		t.Fatalf("BuildCapabilityRegistry: %v", err)
	}
	if child.Get("web_search") == nil {
		t.Fatalf("expected web_search")
	}
	for _, name := range []string{"read_file", "web_fetch", "write"} {
		if child.Get(name) != nil {
			t.Fatalf("expected %s to be filtered", name)
		}
	}
}

func TestCapabilityRegistryEmptyCapabilitiesMeansModelOnly(t *testing.T) {
	parent := core.NewToolRegistry([]core.Tool{
		testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}},
	})
	child, err := BuildCapabilityRegistry(parent, []string{})
	if err != nil {
		t.Fatalf("BuildCapabilityRegistry: %v", err)
	}
	if len(child.Tools()) != 0 {
		t.Fatalf("expected no tools, got %d", len(child.Tools()))
	}
}

func TestCapabilityRegistryRejectsUnknownCapability(t *testing.T) {
	parent := core.NewToolRegistry([]core.Tool{
		testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}},
	})
	_, err := BuildCapabilityRegistry(parent, []string{"network.all"})
	if err == nil || !strings.Contains(err.Error(), "unknown agent tools selector") {
		t.Fatalf("expected unknown selector error, got %v", err)
	}
}

func TestAgentRegistryFiltersMCPToolsByNamedServer(t *testing.T) {
	parent := core.NewToolRegistry([]core.Tool{
		testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}},
		testTool{name: "mcp__docs__search", readOnly: true, capabilities: []string{CapabilityMCPRead}},
		testTool{name: "mcp__git_hub__issue", readOnly: true, capabilities: []string{CapabilityMCPRead}},
	})
	child, err := BuildAgentRegistryForMCPServers(parent, []string{CapabilityWorkspaceRead, CapabilityMCPRead}, AgentPermissionReadOnly, []string{"git-hub"})
	if err != nil {
		t.Fatalf("BuildAgentRegistryForMCPServers: %v", err)
	}
	if child.Get("read_file") == nil {
		t.Fatal("non-MCP capability tool should remain available")
	}
	if child.Get("mcp__git_hub__issue") == nil {
		t.Fatal("requested MCP server tool should remain available")
	}
	if child.Get("mcp__docs__search") != nil {
		t.Fatal("unrequested MCP server tool should be filtered")
	}
}

func TestAgentRegistryShellReadGuardsMutatingCommands(t *testing.T) {
	ran := 0
	parent := core.NewToolRegistry([]core.Tool{
		recordingTool{
			testTool: testTool{
				name:          "shell_run",
				readOnlyCheck: func(args map[string]any) bool { return args["command"] == "git status --short" },
				capabilities:  []string{CapabilityShellRead, CapabilityShellRun},
			},
			ran: &ran,
		},
	})
	child, err := BuildAgentRegistry(parent, []string{CapabilityShellRead}, AgentPermissionAsk)
	if err != nil {
		t.Fatalf("BuildAgentRegistry: %v", err)
	}
	tool := child.Get("shell_run")
	if tool == nil {
		t.Fatal("expected shell_run")
	}
	allowed, err := tool.Run(context.Background(), core.ToolCall{ID: "safe", Name: "shell_run", Input: `{"command":"git status --short"}`})
	if err != nil || allowed.IsError() {
		t.Fatalf("expected safe read-only shell to run, res=%+v err=%v", allowed, err)
	}
	blocked, err := tool.Run(context.Background(), core.ToolCall{ID: "write", Name: "shell_run", Input: `{"command":"go test ./..."}`})
	if err != nil {
		t.Fatalf("mutating shell returned dispatch error: %v", err)
	}
	if !blocked.IsError() || !strings.Contains(blocked.ModelText, "read_only_required") {
		t.Fatalf("expected read-only guard, got %+v", blocked)
	}
	if ran != 1 {
		t.Fatalf("expected only safe command to run, got %d runs", ran)
	}
}

func TestAgentRegistryShellReadDoesNotExposeWriteStdin(t *testing.T) {
	parent := core.NewToolRegistry([]core.Tool{
		testTool{name: "shell_wait", readOnly: true, capabilities: []string{CapabilityShellRead, CapabilityShellRun}},
		testTool{name: "write_stdin", readOnly: false, capabilities: []string{CapabilityTerminalWrite}},
	})
	child, err := BuildAgentRegistry(parent, []string{CapabilityShellRead}, AgentPermissionAsk)
	if err != nil {
		t.Fatalf("BuildAgentRegistry: %v", err)
	}
	if child.Get("shell_wait") == nil {
		t.Fatal("shell.read child should expose shell_wait")
	}
	if child.Get("write_stdin") != nil {
		t.Fatal("shell.read child must not expose write_stdin")
	}
}

func TestAgentRegistryShellRunDoesNotExposeTerminalWrite(t *testing.T) {
	parent := core.NewToolRegistry([]core.Tool{
		testTool{name: "shell_run", readOnly: false, capabilities: []string{CapabilityShellRun}},
		testTool{name: "write_stdin", readOnly: false, capabilities: []string{CapabilityTerminalWrite}},
	})
	child, err := BuildAgentRegistry(parent, []string{CapabilityShellRun}, AgentPermissionAsk)
	if err != nil {
		t.Fatalf("BuildAgentRegistry: %v", err)
	}
	if child.Get("shell_run") == nil {
		t.Fatal("shell.run child should expose shell_run")
	}
	if child.Get("write_stdin") != nil {
		t.Fatal("shell.run child must not expose write_stdin without terminal.write")
	}

	withTerminal, err := BuildAgentRegistry(parent, []string{CapabilityShellRun, CapabilityTerminalWrite}, AgentPermissionAsk)
	if err != nil {
		t.Fatalf("BuildAgentRegistry with terminal.write: %v", err)
	}
	if withTerminal.Get("write_stdin") == nil {
		t.Fatal("terminal.write child should expose write_stdin")
	}
}

func TestAgentRegistryShellRunAndWorkspaceWriteNeedNonReadOnlyPermission(t *testing.T) {
	parent := core.NewToolRegistry([]core.Tool{
		testTool{name: "shell_run", readOnlyCheck: func(map[string]any) bool { return false }, capabilities: []string{CapabilityShellRead, CapabilityShellRun}},
		plainTestTool{name: "write", capabilities: []string{CapabilityWorkspaceWrite}},
	})
	readonly, err := BuildAgentRegistry(parent, []string{CapabilityShellRun, CapabilityWorkspaceWrite}, AgentPermissionReadOnly)
	if err != nil {
		t.Fatalf("BuildAgentRegistry(readonly): %v", err)
	}
	if readonly.Get("write") != nil {
		t.Fatal("read_only permission should not expose workspace.write tools")
	}
	if readonly.Get("shell_run") == nil {
		t.Fatal("read_only permission should expose guarded shell_run when shell_run has a read-only check")
	}
	ask, err := BuildAgentRegistry(parent, []string{CapabilityShellRun, CapabilityWorkspaceWrite}, AgentPermissionAsk)
	if err != nil {
		t.Fatalf("BuildAgentRegistry(ask): %v", err)
	}
	for _, name := range []string{"shell_run", "write"} {
		if ask.Get(name) == nil {
			t.Fatalf("ask permission should expose %s", name)
		}
	}
}

func TestAgentRegistryAllowsExactToolSelectorsAndDisallowsExactTools(t *testing.T) {
	parent := core.NewToolRegistry([]core.Tool{
		testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}},
		testTool{name: "web_search", readOnly: true, capabilities: []string{CapabilityWebSearch}},
		testTool{name: "custom_lookup", readOnly: true},
	})
	child, err := BuildAgentRegistryForMCPServers(parent, []string{"custom_lookup", CapabilityWebSearch}, AgentPermissionReadOnly, nil, []string{"web_search"})
	if err != nil {
		t.Fatalf("BuildAgentRegistryForMCPServers: %v", err)
	}
	if child.Get("custom_lookup") == nil {
		t.Fatal("exact tool selector should expose custom_lookup")
	}
	if child.Get("web_search") != nil {
		t.Fatal("exact disallowedTools selector should remove web_search")
	}
	if child.Get("read_file") != nil {
		t.Fatal("default workspace.read should not be added when tools are explicit")
	}
}

func TestAgentRegistryDisallowsCapabilitySelectors(t *testing.T) {
	parent := core.NewToolRegistry([]core.Tool{
		testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}},
		testTool{name: "web_search", readOnly: true, capabilities: []string{CapabilityWebSearch}},
	})
	child, err := BuildAgentRegistryForMCPServers(parent, []string{CapabilityWorkspaceRead, CapabilityWebSearch}, AgentPermissionReadOnly, nil, []string{CapabilityWebSearch})
	if err != nil {
		t.Fatalf("BuildAgentRegistryForMCPServers: %v", err)
	}
	if child.Get("read_file") == nil {
		t.Fatal("workspace.read tool should remain")
	}
	if child.Get("web_search") != nil {
		t.Fatal("disallowed capability should remove web_search")
	}
}

func TestAgentRegistryRejectsUnknownToolSelectors(t *testing.T) {
	parent := core.NewToolRegistry([]core.Tool{
		testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}},
	})
	_, err := BuildAgentRegistry(parent, []string{"missing_tool"}, AgentPermissionReadOnly)
	if err == nil || !strings.Contains(err.Error(), "unknown agent tools selector") {
		t.Fatalf("expected unknown tool selector error, got %v", err)
	}
}

func TestMergeWorkspaceAndParentToolsPreservesNonWorkspaceTools(t *testing.T) {
	parent := core.NewToolRegistry([]core.Tool{
		testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}},
		testTool{name: "mcp__docs__search", readOnly: true, capabilities: []string{CapabilityMCPRead}},
		testTool{name: "plugin_lookup", readOnly: true},
	})
	workspace := core.NewToolRegistry([]core.Tool{
		testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}},
		testTool{name: "write", capabilities: []string{CapabilityWorkspaceWrite}},
	})

	merged, err := mergeWorkspaceAndParentTools(parent, workspace)
	if err != nil {
		t.Fatalf("mergeWorkspaceAndParentTools: %v", err)
	}
	child, err := BuildAgentRegistryForMCPServers(merged, []string{CapabilityWorkspaceRead, CapabilityMCPRead, "plugin_lookup"}, AgentPermissionReadOnly, []string{"docs"})
	if err != nil {
		t.Fatalf("BuildAgentRegistryForMCPServers: %v", err)
	}
	for _, name := range []string{"read_file", "mcp__docs__search", "plugin_lookup"} {
		if child.Get(name) == nil {
			t.Fatalf("expected %s to remain visible", name)
		}
	}
	if child.Get("write") != nil {
		t.Fatal("workspace write should not be visible under read-only permissions")
	}
}

func TestChildAskPolicyRequiresApprovalForMutableTools(t *testing.T) {
	p := childToolPolicy(nil, AgentPermissionAsk, "/repo", []string{CapabilityShellRun, CapabilityWorkspaceWrite, CapabilityTerminalWrite})
	shellDecision := p.Decide(
		core.ToolSpec{Name: "shell_run", Capabilities: []string{CapabilityShellRun}},
		core.ToolCall{Name: "shell_run", Input: `{"command":"go test ./..."}`},
	)
	if !shellDecision.Allow || !shellDecision.RequiresApproval {
		t.Fatalf("ask shell should require approval, got %+v", shellDecision)
	}
	editDecision := p.Decide(
		core.ToolSpec{Name: "write", Capabilities: []string{CapabilityWorkspaceWrite}},
		core.ToolCall{Name: "write", Input: `{"file_path":"out.txt","content":"x"}`},
	)
	if !editDecision.Allow || !editDecision.RequiresApproval {
		t.Fatalf("ask write should require approval, got %+v", editDecision)
	}
	stdinDecision := p.Decide(
		core.ToolSpec{Name: "write_stdin", Capabilities: []string{CapabilityTerminalWrite}},
		core.ToolCall{Name: "write_stdin", Input: `{"task_id":"task-1","keys":["enter"]}`},
	)
	if !stdinDecision.Allow || !stdinDecision.RequiresApproval {
		t.Fatalf("ask terminal write should require approval, got %+v", stdinDecision)
	}
	exactPolicy := childToolPolicy(nil, AgentPermissionAsk, "/repo", []string{"shell_run", "write_file"})
	exactShellDecision := exactPolicy.Decide(
		core.ToolSpec{Name: "shell_run"},
		core.ToolCall{Name: "shell_run", Input: `{"command":"go test ./..."}`},
	)
	if !exactShellDecision.Allow || !exactShellDecision.RequiresApproval {
		t.Fatalf("ask exact shell selector should require approval, got %+v", exactShellDecision)
	}
	exactWriteDecision := exactPolicy.Decide(
		core.ToolSpec{Name: "write_file"},
		core.ToolCall{Name: "write_file", Input: `{"file_path":"out.txt","content":"x"}`},
	)
	if !exactWriteDecision.Allow || !exactWriteDecision.RequiresApproval {
		t.Fatalf("ask exact write selector should require approval, got %+v", exactWriteDecision)
	}
	denied := p.Decide(
		core.ToolSpec{Name: "shell_run"},
		core.ToolCall{Name: "shell_run", Input: `{"command":"rm -rf /tmp/x"}`},
	)
	if denied.Allow {
		t.Fatalf("ask policy should preserve deny rules, got %+v", denied)
	}
}

func TestChildToolPolicyHonorsParentPermissionRules(t *testing.T) {
	parent := policy.RulePolicy{
		Default: policy.PermissionAllow,
		Rules: append(policy.DefaultRules(),
			policy.PermissionRule{Permission: "shell", Pattern: "git push*", Action: policy.PermissionDeny},
			policy.PermissionRule{Permission: "web_fetch", Pattern: "host:example.com", Action: policy.PermissionDeny},
		),
		WorkspaceRoot: "/parent",
	}
	p := childToolPolicy(parent, AgentPermissionTrusted, "/child", []string{CapabilityShellRun, CapabilityWebFetch})
	shellDecision := p.Decide(
		core.ToolSpec{Name: "shell_run"},
		core.ToolCall{Name: "shell_run", Input: `{"command":"git push origin main"}`},
	)
	if shellDecision.Allow || shellDecision.Permission != "shell" {
		t.Fatalf("child policy should preserve parent shell deny, got %+v", shellDecision)
	}
	fetchDecision := p.Decide(
		core.ToolSpec{Name: "web_fetch"},
		core.ToolCall{Name: "web_fetch", Input: `{"url":"https://example.com/docs"}`},
	)
	if fetchDecision.Allow || fetchDecision.Permission != "web_fetch" {
		t.Fatalf("child policy should preserve parent web_fetch deny, got %+v", fetchDecision)
	}
}

func TestSummarizeChildAgentEventProgress(t *testing.T) {
	tests := []struct {
		name        string
		event       agent.AgentEvent
		wantOK      bool
		wantStatus  string
		wantSummary string
	}{
		{
			name: "context compacted",
			event: agent.AgentEvent{Type: agent.AgentEventTypeContextCompacted, Compact: &agent.CompactInfo{
				Compacted:      true,
				MessagesBefore: 10,
				MessagesAfter:  3,
				BeforeEstimate: 1000,
				AfterEstimate:  300,
			}},
			wantOK:      true,
			wantStatus:  "compacted",
			wantSummary: "Compacted child context (10 -> 3 messages)",
		},
		{
			name: "recovery exhausted",
			event: agent.AgentEvent{Type: agent.AgentEventTypeToolRecoveryExhausted, Recovery: &agent.ToolRecoveryInfo{
				ToolName:     "read_file",
				FailureClass: "schema",
				Reason:       "invalid input",
				Attempt:      2,
				MaxAttempts:  2,
			}},
			wantOK:      true,
			wantStatus:  "tool_recovery_failed",
			wantSummary: "Recovery exhausted for read_file: invalid input",
		},
		{
			name: "fallback recovery executed",
			event: agent.AgentEvent{Type: agent.AgentEventTypeToolRecoveryExhausted, Recovery: &agent.ToolRecoveryInfo{
				ToolName:     "read_file",
				FailureClass: "not_found",
				Action:       "fallback_readonly",
				Attempt:      1,
				MaxAttempts:  1,
				Executed:     true,
			}},
			wantOK:      true,
			wantStatus:  "tool_recovered",
			wantSummary: "Recovered read_file via fallback_readonly",
		},
		{
			name: "replan recovery executed",
			event: agent.AgentEvent{Type: agent.AgentEventTypeToolRecoveryExhausted, Recovery: &agent.ToolRecoveryInfo{
				ToolName:       "grep",
				FailureClass:   "policy_denied",
				Action:         "request_replan",
				Attempt:        1,
				MaxAttempts:    1,
				Executed:       true,
				ReplanInjected: true,
			}},
			wantOK:      true,
			wantStatus:  "tool_recovery_replanned",
			wantSummary: "Requested replan for grep via request_replan",
		},
		{
			name:        "ignored event",
			event:       agent.AgentEvent{Type: agent.AgentEventTypePrefixCacheMetrics},
			wantOK:      false,
			wantStatus:  "",
			wantSummary: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, summary, _, ok := summarizeChildAgentEvent(tt.event)
			if ok != tt.wantOK || status != tt.wantStatus || summary != tt.wantSummary {
				t.Fatalf("summarizeChildAgentEvent = %q %q %v, want %q %q %v", status, summary, ok, tt.wantStatus, tt.wantSummary, tt.wantOK)
			}
		})
	}
}

func TestSpawnSubagentSummaryTruncation(t *testing.T) {
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{Content: strings.Repeat("x", 20)}}
			}()
			return out
		}), nil
	}
	parent := core.NewToolRegistry([]core.Tool{testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}}})
	r := NewRunner(RunnerConfig{ProviderFactory: factory, ParentTools: parent, SummaryMaxChars: 8})
	res, err := r.SpawnSubagent(context.Background(), SpawnSubagentRequest{Task: "inspect"})
	if err != nil {
		t.Fatalf("SpawnSubagent: %v", err)
	}
	if res.Summary != strings.Repeat("x", 8) || !res.Truncated {
		t.Fatalf("summary/truncated = %q %v", res.Summary, res.Truncated)
	}
	if res.Role != "explore" || res.SessionID == "" {
		t.Fatalf("metadata = %+v", res)
	}
}

func TestSpawnSubagentCompletionProgressUsesFinalSummary(t *testing.T) {
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{Content: "  " + strings.Repeat("x", 20) + "  "}}
			}()
			return out
		}), nil
	}
	parent := core.NewToolRegistry([]core.Tool{testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}}})
	r := NewRunner(RunnerConfig{ProviderFactory: factory, ParentTools: parent, SummaryMaxChars: 8})
	var progress []core.ToolProgress
	res, err := r.SpawnSubagentWithProgress(context.Background(), SpawnSubagentRequest{Task: "inspect"}, func(p core.ToolProgress) {
		progress = append(progress, p)
	})
	if err != nil {
		t.Fatalf("SpawnSubagentWithProgress: %v", err)
	}
	wantSummary := strings.Repeat("x", 8)
	if res.Summary != wantSummary || !res.Truncated {
		t.Fatalf("summary/truncated = %q %v", res.Summary, res.Truncated)
	}
	if len(progress) != 1 {
		t.Fatalf("expected completion progress, got %+v", progress)
	}
	if progress[0].Status != "completed" || progress[0].Summary != wantSummary {
		t.Fatalf("unexpected completion progress: %+v", progress[0])
	}
	if progress[0].Metadata["truncated"] != true {
		t.Fatalf("expected truncated metadata, got %+v", progress[0].Metadata)
	}
}

func TestSpawnSubagentBackgroundLifecycleStatusAndResultRecovery(t *testing.T) {
	release := make(chan struct{})
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(ctx context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				select {
				case <-ctx.Done():
					out <- llm.ProviderEvent{Type: llm.EventError, Err: ctx.Err()}
				case <-release:
					out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
						FinishReason: core.FinishReasonEndTurn,
						Content:      "background done",
					}}
				}
			}()
			return out
		}), nil
	}
	r := NewRunner(RunnerConfig{
		ProviderFactory: factory,
		ParentTools:     core.NewToolRegistry(nil),
		SessionsDir:     t.TempDir(),
		ParentSessionID: "parent",
	})
	res, err := r.SpawnSubagent(context.Background(), SpawnSubagentRequest{
		Task: "inspect",
		Agent: AgentDefinition{
			Name:       "background-reviewer",
			Background: true,
		},
	})
	if err != nil {
		t.Fatalf("SpawnSubagent: %v", err)
	}
	if res.Status != "running" || res.SessionID == "" {
		t.Fatalf("background launch response = %+v", res)
	}
	meta, err := r.SubagentStatus(res.SessionID)
	if err != nil {
		t.Fatalf("SubagentStatus: %v", err)
	}
	if meta.Status != "running" || meta.ParentSessionID != "parent" {
		t.Fatalf("running meta = %+v", meta)
	}
	close(release)
	var final session.SessionMeta
	for i := 0; i < 50; i++ {
		final, err = r.SubagentStatus(res.SessionID)
		if err != nil {
			t.Fatalf("SubagentStatus final: %v", err)
		}
		if final.Status == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if final.Status != "completed" || final.Summary != "background done" {
		t.Fatalf("completed meta = %+v", final)
	}
}

func TestSpawnSubagentBackgroundDetachesFromTurnProgress(t *testing.T) {
	release := make(chan struct{})
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(ctx context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				select {
				case <-ctx.Done():
					out <- llm.ProviderEvent{Type: llm.EventError, Err: ctx.Err()}
				case <-release:
					out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
						FinishReason: core.FinishReasonEndTurn,
						Content:      "background done",
					}}
				}
			}()
			return out
		}), nil
	}
	r := NewRunner(RunnerConfig{
		ProviderFactory: factory,
		ParentTools:     core.NewToolRegistry(nil),
		SessionsDir:     t.TempDir(),
	})
	var progress []core.ToolProgress
	res, err := r.SpawnSubagentWithProgress(context.Background(), SpawnSubagentRequest{
		Task: "inspect",
		Agent: AgentDefinition{
			Name:       "background-reviewer",
			Background: true,
		},
	}, func(p core.ToolProgress) {
		progress = append(progress, p)
	})
	if err != nil {
		t.Fatalf("SpawnSubagentWithProgress: %v", err)
	}
	if res.Status != "running" || res.SessionID == "" {
		t.Fatalf("background launch response = %+v", res)
	}
	if len(progress) != 1 || progress[0].Status != "background_started" {
		t.Fatalf("launch progress = %+v, want only background_started", progress)
	}
	close(release)
	var final session.SessionMeta
	for i := 0; i < 50; i++ {
		meta, err := r.SubagentStatus(res.SessionID)
		if err != nil {
			t.Fatalf("SubagentStatus: %v", err)
		}
		final = meta
		if meta.Status == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if final.Status != "completed" || final.Summary != "background done" {
		t.Fatalf("completed meta = %+v", final)
	}
	if len(progress) != 1 {
		t.Fatalf("background child wrote to parent progress after launch: %+v", progress)
	}
}

func TestSpawnSubagentBackgroundCanBeCancelled(t *testing.T) {
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(ctx context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				<-ctx.Done()
				out <- llm.ProviderEvent{Type: llm.EventError, Err: ctx.Err()}
			}()
			return out
		}), nil
	}
	r := NewRunner(RunnerConfig{
		ProviderFactory: factory,
		ParentTools:     core.NewToolRegistry(nil),
		SessionsDir:     t.TempDir(),
	})
	res, err := r.SpawnSubagent(context.Background(), SpawnSubagentRequest{
		Task:  "inspect",
		Agent: AgentDefinition{Name: "background-reviewer", Background: true},
	})
	if err != nil {
		t.Fatalf("SpawnSubagent: %v", err)
	}
	if _, cancelled, err := r.CancelBackgroundSubagent(res.SessionID); err != nil || !cancelled {
		t.Fatalf("CancelBackgroundSubagent cancelled=%v err=%v", cancelled, err)
	}
	var final session.SessionMeta
	for i := 0; i < 50; i++ {
		final, err = r.SubagentStatus(res.SessionID)
		if err != nil {
			t.Fatalf("SubagentStatus final: %v", err)
		}
		if final.Status == "cancelled" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if final.Status != "cancelled" || final.Error == "" {
		t.Fatalf("cancelled meta = %+v", final)
	}
}

func TestSpawnSubagentCapturesStructuredOutputToolResult(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"answer": map[string]any{"type": "string"},
		},
		"required":             []any{"answer"},
		"additionalProperties": false,
	}
	calls := 0
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, _ []core.Message, tools []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				calls++
				if calls == 1 {
					found := false
					for _, tool := range tools {
						if tool.Name() == structuredOutputToolName {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("structured_output tool was not injected")
					}
					out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
						FinishReason: core.FinishReasonToolUse,
						ToolCalls: []core.ToolCall{{
							ID:    "structured-1",
							Name:  structuredOutputToolName,
							Input: `{"answer":"done"}`,
						}},
					}}
					return
				}
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
					FinishReason: core.FinishReasonEndTurn,
					Content:      "Here is the result in prose.",
				}}
			}()
			return out
		}), nil
	}
	parent := core.NewToolRegistry([]core.Tool{testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}}})
	r := NewRunner(RunnerConfig{ProviderFactory: factory, ParentTools: parent})
	res, err := r.SpawnSubagent(context.Background(), SpawnSubagentRequest{Task: "inspect", OutputSchema: schema})
	if err != nil {
		t.Fatalf("SpawnSubagent: %v", err)
	}
	got, ok := res.StructuredResult.(map[string]any)
	if !ok || got["answer"] != "done" {
		t.Fatalf("structured result = %#v", res.StructuredResult)
	}
	if strings.Join(res.ToolCalls, ",") != "" {
		t.Fatalf("structured_output should not be user-visible tool call, got %+v", res.ToolCalls)
	}
	if res.Summary != "Here is the result in prose." {
		t.Fatalf("summary = %q", res.Summary)
	}
}

func TestSpawnSubagentSchemaRequiresStructuredOutputToolCall(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"answer": map[string]any{"type": "string"},
		},
		"required":             []any{"answer"},
		"additionalProperties": false,
	}
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
					FinishReason: core.FinishReasonEndTurn,
					Content:      `{"answer":"done"}`,
				}}
			}()
			return out
		}), nil
	}
	r := NewRunner(RunnerConfig{ProviderFactory: factory, ParentTools: core.NewToolRegistry(nil)})
	_, err := r.SpawnSubagent(context.Background(), SpawnSubagentRequest{Task: "inspect", OutputSchema: schema})
	var subErr *SpawnSubagentError
	if !errors.As(err, &subErr) || subErr.Code != "structured_output_missing" {
		t.Fatalf("error = %v, want structured_output_missing", err)
	}
}

func TestSpawnSubagentRepairsMissingStructuredOutputToolCall(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"answer": map[string]any{"type": "string"},
		},
		"required":             []any{"answer"},
		"additionalProperties": false,
	}
	calls := 0
	var repairPrompt string
	var repairToolNames []string
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, messages []core.Message, tools []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				calls++
				switch calls {
				case 1:
					out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
						FinishReason: core.FinishReasonEndTurn,
						Content:      `{"answer":"done"}`,
					}}
				case 2:
					repairPrompt = messages[len(messages)-1].Text
					for _, tool := range tools {
						repairToolNames = append(repairToolNames, tool.Name())
					}
					out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
						FinishReason: core.FinishReasonToolUse,
						ToolCalls: []core.ToolCall{{
							ID:    "structured-repair",
							Name:  structuredOutputToolName,
							Input: `{"answer":"repaired"}`,
						}},
					}}
				default:
					out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
						FinishReason: core.FinishReasonEndTurn,
						Content:      "repaired",
					}}
				}
			}()
			return out
		}), nil
	}
	r := NewRunner(RunnerConfig{ProviderFactory: factory, ParentTools: core.NewToolRegistry(nil)})
	res, err := r.SpawnSubagent(context.Background(), SpawnSubagentRequest{Task: "inspect", OutputSchema: schema})
	if err != nil {
		t.Fatalf("SpawnSubagent: %v", err)
	}
	got, ok := res.StructuredResult.(map[string]any)
	if !ok || got["answer"] != "repaired" {
		t.Fatalf("structured result = %#v", res.StructuredResult)
	}
	if calls != 3 {
		t.Fatalf("provider calls = %d, want repair tool call plus final", calls)
	}
	if !strings.Contains(repairPrompt, "did not satisfy the required structured output contract") {
		t.Fatalf("repair prompt = %q", repairPrompt)
	}
	if strings.Join(repairToolNames, ",") != structuredOutputToolName {
		t.Fatalf("repair tools = %+v, want only %s", repairToolNames, structuredOutputToolName)
	}
}

func TestSpawnSubagentSchemaReportsInvalidStructuredOutput(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"answer": map[string]any{"type": "string"},
		},
		"required":             []any{"answer"},
		"additionalProperties": false,
	}
	calls := 0
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				calls++
				if calls == 1 {
					out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
						FinishReason: core.FinishReasonToolUse,
						ToolCalls: []core.ToolCall{{
							ID:    "structured-1",
							Name:  structuredOutputToolName,
							Input: `{"answer":42}`,
						}},
					}}
					return
				}
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
					FinishReason: core.FinishReasonEndTurn,
					Content:      "done",
				}}
			}()
			return out
		}), nil
	}
	r := NewRunner(RunnerConfig{ProviderFactory: factory, ParentTools: core.NewToolRegistry(nil)})
	_, err := r.SpawnSubagent(context.Background(), SpawnSubagentRequest{Task: "inspect", OutputSchema: schema})
	var subErr *SpawnSubagentError
	if !errors.As(err, &subErr) || subErr.Code != "structured_output_invalid" {
		t.Fatalf("error = %v, want structured_output_invalid", err)
	}
}

func TestSpawnSubagentReportsChildToolProgress(t *testing.T) {
	calls := 0
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				calls++
				if calls == 1 {
					out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
						FinishReason: core.FinishReasonToolUse,
						ToolCalls: []core.ToolCall{{
							ID:    "child-read",
							Name:  "read_file",
							Input: `{"file_path":"internal/tasks/runner.go"}`,
						}},
					}}
					return
				}
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
					FinishReason: core.FinishReasonEndTurn,
					Content:      "review complete",
				}}
			}()
			return out
		}), nil
	}
	parent := core.NewToolRegistry([]core.Tool{testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}}})
	r := NewRunner(RunnerConfig{ProviderFactory: factory, ParentTools: parent})
	var progress []core.ToolProgress
	res, err := r.SpawnSubagentWithProgress(context.Background(), SpawnSubagentRequest{Task: "inspect", Role: "review"}, func(p core.ToolProgress) {
		progress = append(progress, p)
	})
	if err != nil {
		t.Fatalf("SpawnSubagentWithProgress: %v", err)
	}
	if res.Summary != "review complete" {
		t.Fatalf("summary = %q", res.Summary)
	}
	if len(progress) < 2 {
		t.Fatalf("expected child tool progress, got %+v", progress)
	}
	if progress[0].Status != "running" || progress[0].Role != "review" || !strings.Contains(progress[0].Summary, "internal/tasks/runner.go") {
		t.Fatalf("unexpected first progress: %+v", progress[0])
	}
	if progress[1].Summary != "Read internal/tasks/runner.go" {
		t.Fatalf("unexpected second progress: %+v", progress[1])
	}
}

func TestSpawnSubagentAllowsReadOnlyMCPToolsWithoutApprovalPath(t *testing.T) {
	calls := 0
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				calls++
				if calls == 1 {
					out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
						FinishReason: core.FinishReasonToolUse,
						ToolCalls: []core.ToolCall{{
							ID:    "child-mcp",
							Name:  "mcp__docs_search",
							Input: `{"query":"permissions"}`,
						}},
					}}
					return
				}
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
					FinishReason: core.FinishReasonEndTurn,
					Content:      "done",
				}}
			}()
			return out
		}), nil
	}
	ran := 0
	parent := core.NewToolRegistry([]core.Tool{recordingTool{
		testTool: testTool{name: "mcp__docs_search", readOnly: true, capabilities: []string{CapabilityMCPRead}},
		ran:      &ran,
	}})
	r := NewRunner(RunnerConfig{ProviderFactory: factory, ParentTools: parent})
	res, err := r.SpawnSubagent(context.Background(), SpawnSubagentRequest{Task: "look it up", Role: "explore", Tools: []string{CapabilityMCPRead}})
	if err != nil {
		t.Fatalf("SpawnSubagent: %v", err)
	}
	// DefaultRules routes mcp "*" through ask; a subagent has no interactive
	// approval path, so without an auto-approving callback the call would be
	// denied and the tool would never run.
	if ran != 1 {
		t.Fatalf("read-only MCP tool ran %d times, want 1", ran)
	}
	if res.Summary != "done" {
		t.Fatalf("summary = %q", res.Summary)
	}
}

func TestSpawnSubagentMCPServersLimitVisibleMCPTools(t *testing.T) {
	var visible []string
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, _ []core.Message, tools []core.Tool) <-chan llm.ProviderEvent {
			visible = toolNames(tools)
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
					FinishReason: core.FinishReasonEndTurn,
					Content:      "done",
				}}
			}()
			return out
		}), nil
	}
	parent := core.NewToolRegistry([]core.Tool{
		testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}},
		testTool{name: "mcp__docs__search", readOnly: true, capabilities: []string{CapabilityMCPRead}},
		testTool{name: "mcp__git_hub__issue", readOnly: true, capabilities: []string{CapabilityMCPRead}},
	})
	r := NewRunner(RunnerConfig{ProviderFactory: factory, ParentTools: parent})
	_, err := r.SpawnSubagent(context.Background(), SpawnSubagentRequest{
		Task: "look it up",
		Agent: AgentDefinition{
			Name:       "mcp-reader",
			MCPServers: []string{"git-hub"},
		},
	})
	if err != nil {
		t.Fatalf("SpawnSubagent: %v", err)
	}
	if !slices.Contains(visible, "mcp__git_hub__issue") {
		t.Fatalf("requested MCP tool not visible: %v", visible)
	}
	if slices.Contains(visible, "mcp__docs__search") {
		t.Fatalf("unrequested MCP tool should not be visible: %v", visible)
	}
	if !slices.Contains(visible, "read_file") {
		t.Fatalf("workspace read tool should remain visible: %v", visible)
	}
}

func TestSpawnSubagentAppliesAgentPreToolHooks(t *testing.T) {
	calls := 0
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				calls++
				if calls == 1 {
					out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
						FinishReason: core.FinishReasonToolUse,
						ToolCalls: []core.ToolCall{{
							ID:    "child-read",
							Name:  "read_file",
							Input: `{"file_path":"internal/tasks/runner.go"}`,
						}},
					}}
					return
				}
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
					FinishReason: core.FinishReasonEndTurn,
					Content:      "should not complete",
				}}
			}()
			return out
		}), nil
	}
	ran := 0
	parent := core.NewToolRegistry([]core.Tool{recordingTool{
		testTool: testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}},
		ran:      &ran,
	}})
	r := NewRunner(RunnerConfig{ProviderFactory: factory, ParentTools: parent})
	res, err := r.SpawnSubagent(context.Background(), SpawnSubagentRequest{
		Task: "inspect",
		Agent: AgentDefinition{
			Name:           "hooked-reviewer",
			PermissionMode: AgentPermissionAsk,
			Hooks: map[string]any{
				"PreToolUse": []any{map[string]any{
					"matcher": "read_file",
					"hooks": []any{map[string]any{
						"type":    "command",
						"command": `printf '{"decision":"block","message":"blocked by child hook"}\n'`,
					}},
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("SpawnSubagent: %v", err)
	}
	if ran != 0 {
		t.Fatalf("tool ran despite PreToolUse block: %d", ran)
	}
	if res.Summary != "should not complete" {
		t.Fatalf("summary = %q", res.Summary)
	}
}

func TestSpawnSubagentUsesSubagentStartHookContext(t *testing.T) {
	var capturedPrompt string
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, history []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			for _, msg := range history {
				capturedPrompt += msg.Text + "\n"
			}
			out := make(chan llm.ProviderEvent, 1)
			out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonEndTurn,
				Content:      "done",
			}}
			close(out)
			return out
		}), nil
	}
	r := NewRunner(RunnerConfig{ProviderFactory: factory, ParentTools: core.NewToolRegistry(nil), WorkspaceRoot: t.TempDir()})
	_, err := r.SpawnSubagent(context.Background(), SpawnSubagentRequest{
		Task: "inspect",
		Agent: AgentDefinition{
			Name:           "start-hooked",
			PermissionMode: AgentPermissionAsk,
			Hooks: map[string]any{
				"SubagentStart": []any{map[string]any{
					"command": `printf '{"additional_context":"extra start context"}\n'`,
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("SpawnSubagent: %v", err)
	}
	if !strings.Contains(capturedPrompt, "Subagent start hook context:\nextra start context") {
		t.Fatalf("start hook context missing from prompt:\n%s", capturedPrompt)
	}
}

func TestSpawnSubagentMaxTurnsForcesSummary(t *testing.T) {
	calls := 0
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				calls++
				if calls == 1 {
					out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
						FinishReason: core.FinishReasonToolUse,
						ToolCalls: []core.ToolCall{{
							ID:    "child-read",
							Name:  "read_file",
							Input: `{"file_path":"internal/tasks/runner.go"}`,
						}},
					}}
					return
				}
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
					FinishReason: core.FinishReasonEndTurn,
					Content:      "forced max turns summary",
				}}
			}()
			return out
		}), nil
	}
	parent := core.NewToolRegistry([]core.Tool{testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}}})
	r := NewRunner(RunnerConfig{ProviderFactory: factory, ParentTools: parent})
	var progress []core.ToolProgress
	res, err := r.SpawnSubagentWithProgress(context.Background(), SpawnSubagentRequest{
		Task: "inspect",
		Agent: AgentDefinition{
			Name:     "turn-capped",
			MaxTurns: 1,
		},
	}, func(p core.ToolProgress) {
		progress = append(progress, p)
	})
	if err != nil {
		t.Fatalf("SpawnSubagentWithProgress: %v", err)
	}
	if res.Summary != "forced max turns summary" {
		t.Fatalf("summary = %q", res.Summary)
	}
	if calls != 2 {
		t.Fatalf("provider calls = %d, want tool turn plus forced summary", calls)
	}
	if !slices.ContainsFunc(progress, func(p core.ToolProgress) bool {
		return p.Status == "forced_summary_started" && strings.Contains(p.Summary, "turn cap reached")
	}) {
		t.Fatalf("missing forced summary progress: %+v", progress)
	}
}

func TestSpawnSubagentInheritsAutoCompact(t *testing.T) {
	var histories [][]core.Message
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, history []core.Message, tools []core.Tool) <-chan llm.ProviderEvent {
			histories = append(histories, append([]core.Message(nil), history...))
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				content := "child done"
				if len(history) > 0 && strings.Contains(history[len(history)-1].Text, "Summarize the conversation") {
					content = "compact summary"
				}
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
					FinishReason: core.FinishReasonEndTurn,
					Content:      content,
				}}
			}()
			return out
		}), nil
	}
	msgStore := store.NewInMemoryStore()
	childSessionID := "parent-session--subagent-tool-1"
	for range 8 {
		_, _ = msgStore.Create(context.Background(), core.Message{
			SessionID: childSessionID,
			Role:      core.RoleUser,
			Text:      strings.Repeat("large prior context ", 80),
		})
	}
	parent := core.NewToolRegistry([]core.Tool{testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}}})
	r := NewRunner(RunnerConfig{
		ProviderFactory:      factory,
		ParentTools:          parent,
		MessageStore:         msgStore,
		ParentSessionID:      "parent-session",
		AutoCompact:          true,
		AutoCompactThreshold: 0.0001,
	})
	res, err := r.SpawnSubagentWithProgress(context.Background(), SpawnSubagentRequest{
		Task:             "continue inspection",
		Role:             "explore",
		Model:            "deepseek-v4-flash",
		ParentToolCallID: "tool-1",
	}, func(core.ToolProgress) {})
	if err != nil {
		t.Fatalf("SpawnSubagentWithProgress: %v", err)
	}
	if res.Summary != "child done" {
		t.Fatalf("summary = %q", res.Summary)
	}
	if len(histories) < 2 {
		t.Fatalf("expected compact summary call and child response call, got %d", len(histories))
	}
	first := histories[0]
	if len(first) == 0 || !strings.Contains(first[len(first)-1].Text, "Summarize the conversation") {
		t.Fatalf("expected first provider call to compact child history, got %+v", first)
	}
	msgs, err := msgStore.List(context.Background(), childSessionID)
	if err != nil {
		t.Fatalf("list child messages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected compact summary plus child response, got %+v", msgs)
	}
	if msgs[0].Role != core.RoleUser || msgs[0].Text != "compact summary" || msgs[0].FinishReason != core.FinishReasonEndTurn {
		t.Fatalf("unexpected compact summary message: %+v", msgs[0])
	}
	if msgs[1].Role != core.RoleAssistant || msgs[1].Text != "child done" {
		t.Fatalf("unexpected child response message: %+v", msgs[1])
	}
}

func TestSpawnSubagentDerivesAutoCompactWindowFromChildModel(t *testing.T) {
	var histories [][]core.Message
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, history []core.Message, tools []core.Tool) <-chan llm.ProviderEvent {
			histories = append(histories, append([]core.Message(nil), history...))
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				content := "child done"
				if len(history) > 0 && strings.Contains(history[len(history)-1].Text, "Summarize the conversation") {
					content = "compact summary"
				}
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
					FinishReason: core.FinishReasonEndTurn,
					Content:      content,
				}}
			}()
			return out
		}), nil
	}
	msgStore := store.NewInMemoryStore()
	childSessionID := "parent-session--subagent-tool-1"
	for range 8 {
		_, _ = msgStore.Create(context.Background(), core.Message{
			SessionID: childSessionID,
			Role:      core.RoleUser,
			Text:      strings.Repeat("large prior context ", 80),
		})
	}
	parent := core.NewToolRegistry([]core.Tool{testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}}})
	r := NewRunner(RunnerConfig{
		ProviderFactory:      factory,
		ParentTools:          parent,
		MessageStore:         msgStore,
		ParentSessionID:      "parent-session",
		AutoCompact:          true,
		AutoCompactThreshold: 0.01,
	})
	res, err := r.SpawnSubagentWithProgress(context.Background(), SpawnSubagentRequest{
		Task:             "continue inspection",
		Role:             "explore",
		Model:            defaults.DefaultModel,
		ParentToolCallID: "tool-1",
	}, func(core.ToolProgress) {})
	if err != nil {
		t.Fatalf("SpawnSubagentWithProgress: %v", err)
	}
	if res.Summary != "child done" {
		t.Fatalf("summary = %q", res.Summary)
	}
	if len(histories) != 1 {
		t.Fatalf("expected child response without premature compact, got %d provider calls", len(histories))
	}
	first := histories[0]
	if len(first) == 0 || strings.Contains(first[len(first)-1].Text, "Summarize the conversation") {
		t.Fatalf("expected first provider call to use child model window, got %+v", first)
	}
	msgs, err := msgStore.List(context.Background(), childSessionID)
	if err != nil {
		t.Fatalf("list child messages: %v", err)
	}
	if len(msgs) <= 2 {
		t.Fatalf("expected un-compacted child history to remain, got %+v", msgs)
	}
	if msgs[len(msgs)-1].Role != core.RoleAssistant || msgs[len(msgs)-1].Text != "child done" {
		t.Fatalf("unexpected child response message: %+v", msgs[len(msgs)-1])
	}
}

func TestSpawnSubagentPersistsDurableChildSessionAndMeta(t *testing.T) {
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
					FinishReason: core.FinishReasonEndTurn,
					Content:      "durable review complete",
				}}
			}()
			return out
		}), nil
	}
	dir := t.TempDir()
	msgStore, err := store.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	parent := core.NewToolRegistry([]core.Tool{testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}}})
	r := NewRunner(RunnerConfig{
		ProviderFactory: factory,
		ParentTools:     parent,
		MessageStore:    msgStore,
		SessionsDir:     dir,
		ParentSessionID: "parent-session",
	})
	res, err := r.SpawnSubagent(context.Background(), SpawnSubagentRequest{
		Task:             "review internal/tasks",
		Role:             "review",
		ParentToolCallID: "tc-subagent",
	})
	if err != nil {
		t.Fatalf("SpawnSubagent: %v", err)
	}
	if res.SessionID != "parent-session--subagent-tc-subagent" {
		t.Fatalf("session id = %q", res.SessionID)
	}
	if res.PermissionProfile != "read_only" || res.Status != "completed" {
		t.Fatalf("metadata = %+v", res)
	}
	msgs, err := msgStore.List(context.Background(), res.SessionID)
	if err != nil {
		t.Fatalf("list child messages: %v", err)
	}
	if len(msgs) < 2 || msgs[0].Role != core.RoleUser || msgs[len(msgs)-1].Role != core.RoleAssistant {
		t.Fatalf("unexpected child messages: %+v", msgs)
	}
	meta, err := session.LoadSessionMeta(dir, res.SessionID)
	if err != nil {
		t.Fatalf("load child meta: %v", err)
	}
	if meta.Kind != "subagent" || meta.ParentSessionID != "parent-session" || meta.Role != "review" || meta.Status != "completed" {
		t.Fatalf("unexpected meta: %+v", meta)
	}
	if meta.Task != "review internal/tasks" || meta.Summary != "durable review complete" || meta.StartedAt.IsZero() || meta.CompletedAt.IsZero() {
		t.Fatalf("incomplete meta: %+v", meta)
	}
}

func TestSpawnSubagentWorktreeIsolationUsesIsolatedWorkspace(t *testing.T) {
	workspaceRoot := initGitWorkspace(t)
	var capturedPrompt string
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, history []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			for _, msg := range history {
				capturedPrompt += msg.Text + "\n"
			}
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
					FinishReason: core.FinishReasonEndTurn,
					Content:      "isolated review complete",
				}}
			}()
			return out
		}), nil
	}
	dir := t.TempDir()
	msgStore, err := store.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	var toolWorkspace ToolWorkspace
	r := NewRunner(RunnerConfig{
		ProviderFactory: factory,
		ParentTools:     core.NewToolRegistry(nil),
		WorkspaceTools: func(workspace ToolWorkspace) (*core.ToolRegistry, error) {
			toolWorkspace = workspace
			return core.NewToolRegistry([]core.Tool{
				testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}},
			}), nil
		},
		MessageStore:    msgStore,
		SessionsDir:     dir,
		ParentSessionID: "parent-session",
		WorkspaceRoot:   workspaceRoot,
	})
	res, err := r.SpawnSubagent(context.Background(), SpawnSubagentRequest{
		Task: "review in isolation",
		Agent: AgentDefinition{
			Name:           "isolated-review",
			Description:    "review local changes",
			Tools:          []string{CapabilityWorkspaceRead},
			PermissionMode: AgentPermissionReadOnly,
			Isolation:      AgentIsolationWorktree,
		},
		ParentToolCallID: "tc-isolated",
	})
	if err != nil {
		t.Fatalf("SpawnSubagent: %v", err)
	}
	if res.Status != "completed" {
		t.Fatalf("status = %q", res.Status)
	}
	if toolWorkspace.WorktreeRoot == "" || toolWorkspace.WorkspaceRoot == workspaceRoot {
		t.Fatalf("expected isolated workspace, got %+v", toolWorkspace)
	}
	if _, err := os.Stat(filepath.Join(toolWorkspace.WorkspaceRoot, "README.md")); err != nil {
		t.Fatalf("isolated workspace missing checked-out file: %v", err)
	}
	meta, err := session.LoadSessionMeta(dir, res.SessionID)
	if err != nil {
		t.Fatalf("load child meta: %v", err)
	}
	if meta.Workspace != toolWorkspace.WorkspaceRoot || meta.WorktreePath != toolWorkspace.WorktreeRoot || meta.OriginalWorkspace != workspaceRoot {
		t.Fatalf("unexpected worktree meta: %+v workspace=%+v", meta, toolWorkspace)
	}
	for _, want := range []string{"Current worktree root: " + toolWorkspace.WorktreeRoot, "Original workspace: " + workspaceRoot} {
		if !strings.Contains(capturedPrompt, want) {
			t.Fatalf("child prompt missing %q:\n%s", want, capturedPrompt)
		}
	}
}

func TestSpawnSubagentAppliesAgentSkillsInitialPromptAndMemory(t *testing.T) {
	workspaceRoot := t.TempDir()
	writeTestSkill(t, workspaceRoot, "agent-skill", "Agent skill instructions.")
	memoryPath := filepath.Join(workspaceRoot, ".whale", "agent-memory", "skill-agent", "MEMORY.md")
	if err := os.MkdirAll(filepath.Dir(memoryPath), 0o755); err != nil {
		t.Fatalf("mkdir memory: %v", err)
	}
	if err := os.WriteFile(memoryPath, []byte("Remember to check accessibility.\n"), 0o644); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	var capturedPrompt string
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, history []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			for _, msg := range history {
				capturedPrompt += msg.Text + "\n"
			}
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
					FinishReason: core.FinishReasonEndTurn,
					Content:      "skill agent complete",
				}}
			}()
			return out
		}), nil
	}
	r := NewRunner(RunnerConfig{
		ProviderFactory: factory,
		ParentTools:     core.NewToolRegistry(nil),
		WorkspaceRoot:   workspaceRoot,
	})
	_, err := r.SpawnSubagent(context.Background(), SpawnSubagentRequest{
		Task: "review component",
		Agent: AgentDefinition{
			Name:          "skill-agent",
			Description:   "uses a skill",
			Skills:        []string{"agent-skill"},
			InitialPrompt: "Start by loading local context.",
			Memory:        "project",
		},
	})
	if err != nil {
		t.Fatalf("SpawnSubagent: %v", err)
	}
	for _, want := range []string{
		"Preloaded agent skills:",
		"Agent skill instructions.",
		"Persistent Agent Memory:",
		"Remember to check accessibility.",
		"Start by loading local context.\n\nreview component",
	} {
		if !strings.Contains(capturedPrompt, want) {
			t.Fatalf("child prompt missing %q:\n%s", want, capturedPrompt)
		}
	}
}

func TestSpawnSubagentUsesAgentGenerationPrefixCompletion(t *testing.T) {
	workspaceRoot := t.TempDir()
	provider := &subagentPrefixProvider{}
	r := NewRunner(RunnerConfig{
		ProviderFactory: func(_ string, _ int) (llm.Provider, error) {
			return provider, nil
		},
		ParentTools:   core.NewToolRegistry(nil),
		WorkspaceRoot: workspaceRoot,
	})
	res, err := r.SpawnSubagent(context.Background(), SpawnSubagentRequest{
		Task:  "return ok",
		Tools: []string{},
		Agent: AgentDefinition{
			Name:        "prefill-agent",
			Description: "uses prefix completion",
			Generation: AgentGenerationConfig{
				AssistantPrefix:  "ok:\n",
				PrefixCompletion: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("SpawnSubagent: %v", err)
	}
	if !provider.prefixCalled || provider.streamCalled {
		t.Fatalf("prefixCalled=%v streamCalled=%v", provider.prefixCalled, provider.streamCalled)
	}
	if provider.prefix != "ok:\n" {
		t.Fatalf("prefix = %q", provider.prefix)
	}
	if res.Summary != "prefixed" {
		t.Fatalf("summary = %q", res.Summary)
	}
	if res.Usage.PrefixCompletionRequests != 1 {
		t.Fatalf("prefix usage = %+v", res.Usage)
	}
}

func TestSpawnSubagentGenerationKeepsToolAgentsOnNormalStream(t *testing.T) {
	workspaceRoot := t.TempDir()
	provider := &subagentPrefixProvider{}
	r := NewRunner(RunnerConfig{
		ProviderFactory: func(_ string, _ int) (llm.Provider, error) {
			return provider, nil
		},
		ParentTools: core.NewToolRegistry([]core.Tool{
			testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}},
		}),
		WorkspaceRoot: workspaceRoot,
	})
	res, err := r.SpawnSubagent(context.Background(), SpawnSubagentRequest{
		Task: "return ok",
		Agent: AgentDefinition{
			Name:        "tool-agent",
			Description: "keeps tools",
			Tools:       []string{CapabilityWorkspaceRead},
			Generation: AgentGenerationConfig{
				AssistantPrefix:  "ok:",
				PrefixCompletion: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("SpawnSubagent: %v", err)
	}
	if provider.prefixCalled || !provider.streamCalled {
		t.Fatalf("prefixCalled=%v streamCalled=%v", provider.prefixCalled, provider.streamCalled)
	}
	if res.Summary != "streamed" {
		t.Fatalf("summary = %q", res.Summary)
	}
}

func TestSpawnSubagentGenerationFallsBackWithoutPrefixProvider(t *testing.T) {
	workspaceRoot := t.TempDir()
	var streamCalled bool
	r := NewRunner(RunnerConfig{
		ProviderFactory: func(_ string, _ int) (llm.Provider, error) {
			return providerFunc(func(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
				streamCalled = true
				out := make(chan llm.ProviderEvent, 1)
				go func() {
					defer close(out)
					out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
						FinishReason: core.FinishReasonEndTurn,
						Content:      "fallback",
					}}
				}()
				return out
			}), nil
		},
		ParentTools:   core.NewToolRegistry(nil),
		WorkspaceRoot: workspaceRoot,
	})
	res, err := r.SpawnSubagent(context.Background(), SpawnSubagentRequest{
		Task:  "return ok",
		Tools: []string{},
		Agent: AgentDefinition{
			Name:        "fallback-agent",
			Description: "falls back",
			Generation: AgentGenerationConfig{
				AssistantPrefix:  "ok:",
				PrefixCompletion: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("SpawnSubagent: %v", err)
	}
	if !streamCalled {
		t.Fatal("expected normal stream fallback")
	}
	if res.Summary != "fallback" {
		t.Fatalf("summary = %q", res.Summary)
	}
}

func TestSpawnSubagentToolFailureIncludesChildSessionID(t *testing.T) {
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				out <- llm.ProviderEvent{Type: llm.EventError, Err: errors.New("provider failed")}
			}()
			return out
		}), nil
	}
	dir := t.TempDir()
	msgStore, err := store.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	parent := core.NewToolRegistry([]core.Tool{testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}}})
	r := NewRunner(RunnerConfig{
		ProviderFactory: factory,
		ParentTools:     parent,
		MessageStore:    msgStore,
		SessionsDir:     dir,
		ParentSessionID: "parent-session",
	})
	tool := spawnSubagentTool{runner: r}
	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    "tc-fail",
		Name:  "spawn_subagent",
		Input: `{"task":"inspect","role":"review"}`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError() {
		t.Fatalf("expected tool error: %+v", res)
	}
	env, ok := core.ParseToolEnvelope(res.ModelText)
	if !ok {
		t.Fatalf("parse envelope failed: %s", res.ModelText)
	}
	if env.Data["child_session_id"] != "parent-session--subagent-tc-fail" {
		t.Fatalf("child session id missing: %+v", env.Data)
	}
	meta, err := session.LoadSessionMeta(dir, "parent-session--subagent-tc-fail")
	if err != nil {
		t.Fatalf("load child meta: %v", err)
	}
	if meta.Status != "failed" && meta.Status != "cancelled" {
		t.Fatalf("unexpected failure meta: %+v", meta)
	}
}

func TestSpawnSubagentToolSchemaUsesNamedRoleNotInlinePrompt(t *testing.T) {
	tool := spawnSubagentTool{}
	params := tool.Parameters()
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("tool properties missing: %+v", params)
	}
	if _, ok := props["agent"]; ok {
		t.Fatalf("spawn_subagent schema should not expose inline agent definitions: %+v", props)
	}
	roleSchema, ok := props["role"].(map[string]any)
	if !ok {
		t.Fatalf("spawn_subagent schema omits role: %+v", props)
	}
	desc, _ := roleSchema["description"].(string)
	if !strings.Contains(desc, ".whale/agents") {
		t.Fatalf("role description should point to named agent definitions: %q", desc)
	}
}

func TestSpawnSubagentToolSchemaUsesToolsNotCapabilities(t *testing.T) {
	tool := spawnSubagentTool{}
	params := tool.Parameters()
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("tool properties missing: %+v", params)
	}
	if _, ok := props["capabilities"]; ok {
		t.Fatalf("spawn_subagent schema should not expose capabilities: %+v", props)
	}
	toolsSchema, ok := props["tools"].(map[string]any)
	if !ok {
		t.Fatalf("spawn_subagent schema omits tools: %+v", props)
	}
	items, ok := toolsSchema["items"].(map[string]any)
	if !ok {
		t.Fatalf("tools items schema missing: %+v", toolsSchema)
	}
	if _, ok := items["enum"]; ok {
		t.Fatalf("tools items should allow exact tool names, got enum: %+v", items)
	}
}

func jsonArrayLen(value any) int {
	switch v := value.(type) {
	case []any:
		return len(v)
	case []string:
		return len(v)
	default:
		return -1
	}
}

func TestChildToolProgressSummariesIncludeTargetsAndResultMetrics(t *testing.T) {
	call := core.ToolCall{ID: "grep-1", Name: "grep", Input: `{"pattern":"TaskProgress","path":"internal/tui","include":"*.go"}`}
	action := summarizeChildToolCall(call)
	if action.Running != `Searching "TaskProgress" in internal/tui (*.go)` {
		t.Fatalf("running summary = %q", action.Running)
	}
	result := core.ToolResult{
		ToolCallID: "grep-1",
		Name:       "grep",
		ModelText:  `{"ok":true,"success":true,"data":{"metrics":{"total_matches":7,"files_matched":3},"payload":{"matches":[]}}}`,
	}
	if got := summarizeChildToolResult(result, action); got != `Searched "TaskProgress" in internal/tui (*.go) · 7 matches in 3 files` {
		t.Fatalf("result summary = %q", got)
	}

	web := summarizeChildToolCall(core.ToolCall{ID: "web-1", Name: "web_search", Input: `{"query":"codex subagent ps"}`})
	if web.Running != `Searching web "codex subagent ps"` {
		t.Fatalf("web running summary = %q", web.Running)
	}
	webResult := core.ToolResult{
		ToolCallID: "web-1",
		Name:       "web_search",
		ModelText:  `{"ok":true,"success":true,"data":{"count":4,"results":[]}}`,
	}
	if got := summarizeChildToolResult(webResult, web); got != `Searched web "codex subagent ps" · 4 results` {
		t.Fatalf("web result summary = %q", got)
	}

	fetch := summarizeChildToolCall(core.ToolCall{ID: "fetch-1", Name: "fetch", Input: `{"url":"https://docs.example.com/path/to/page?x=1"}`})
	if fetch.Running != "Fetching docs.example.com/path/to/page" {
		t.Fatalf("fetch running summary = %q", fetch.Running)
	}
	fetchResult := core.ToolResult{
		ToolCallID: "fetch-1",
		Name:       "fetch",
		ModelText:  `{"ok":true,"success":true,"data":{"status_code":200}}`,
	}
	if got := summarizeChildToolResult(fetchResult, fetch); got != "Fetched docs.example.com/path/to/page · HTTP 200" {
		t.Fatalf("fetch result summary = %q", got)
	}
}

func TestWorkflowApprovalMetadataAddsWorkflowContext(t *testing.T) {
	meta := workflowApprovalMetadata(map[string]any{"kind": "web"}, SpawnSubagentRequest{
		WorkflowRunID:     "run-1",
		WorkflowName:      "deep-research",
		WorkflowPhase:     "Research",
		WorkflowTaskID:    "task-1",
		WorkflowTaskLabel: "search:official",
	})
	if meta["kind"] != "web" || meta["workflow_run_id"] != "run-1" || meta["workflow_name"] != "deep-research" || meta["workflow_phase"] != "Research" || meta["workflow_task_id"] != "task-1" || meta["workflow_task_label"] != "search:official" {
		t.Fatalf("metadata = %+v", meta)
	}
}

func initGitWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitTestCommand(t, dir, "init")
	runGitTestCommand(t, dir, "config", "user.email", "test@example.com")
	runGitTestCommand(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGitTestCommand(t, dir, "add", "README.md")
	runGitTestCommand(t, dir, "commit", "-m", "initial")
	return dir
}

func writeTestSkill(t *testing.T, workspaceRoot, name, body string) {
	t.Helper()
	dir := filepath.Join(workspaceRoot, ".whale", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	content := "---\nname: " + name + "\ndescription: Test skill.\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
}

func runGitTestCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}

type providerFunc func(context.Context, []core.Message, []core.Tool) <-chan llm.ProviderEvent

func (f providerFunc) StreamResponse(ctx context.Context, history []core.Message, tools []core.Tool) <-chan llm.ProviderEvent {
	return f(ctx, history, tools)
}

type subagentPrefixProvider struct {
	prefixCalled bool
	streamCalled bool
	prefix       string
}

func (p *subagentPrefixProvider) StreamResponse(context.Context, []core.Message, []core.Tool) <-chan llm.ProviderEvent {
	p.streamCalled = true
	out := make(chan llm.ProviderEvent, 1)
	go func() {
		defer close(out)
		out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
			FinishReason: core.FinishReasonEndTurn,
			Content:      "streamed",
		}}
	}()
	return out
}

func (p *subagentPrefixProvider) StreamResponseWithPrefix(_ context.Context, _ []core.Message, prefix string, _ []string) <-chan llm.ProviderEvent {
	p.prefixCalled = true
	p.prefix = prefix
	out := make(chan llm.ProviderEvent, 1)
	go func() {
		defer close(out)
		out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
			FinishReason: core.FinishReasonEndTurn,
			Content:      "prefixed",
			Usage:        llm.Usage{PrefixCompletionRequests: 1},
		}}
	}()
	return out
}

type testTool struct {
	name          string
	readOnly      bool
	readOnlyCheck func(map[string]any) bool
	capabilities  []string
}

func (t testTool) Name() string        { return t.name }
func (t testTool) Description() string { return "test tool" }
func (t testTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}
func (t testTool) ReadOnly() bool { return t.readOnly }
func (t testTool) Capabilities() []string {
	return append([]string(nil), t.capabilities...)
}
func (t testTool) ReadOnlyCheck(args map[string]any) bool {
	if t.readOnlyCheck == nil {
		return t.readOnly
	}
	return t.readOnlyCheck(args)
}
func (t testTool) Run(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: `{"ok":true}`}, nil
}

type plainTestTool struct {
	name         string
	capabilities []string
}

func (t plainTestTool) Name() string        { return t.name }
func (t plainTestTool) Description() string { return "test tool" }
func (t plainTestTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}
func (t plainTestTool) Capabilities() []string {
	return append([]string(nil), t.capabilities...)
}
func (t plainTestTool) Run(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: `{"ok":true}`}, nil
}

// recordingTool wraps testTool to count how many times it actually executes,
// so a test can tell a real run apart from a policy denial (which never calls
// Run). The counter is a pointer so it survives the tool being copied into a
// registry by value.
type recordingTool struct {
	testTool
	ran *int
}

func (t recordingTool) Run(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	*t.ran++
	return t.testTool.Run(ctx, call)
}

type progressTool struct {
	testTool
}

func (t progressTool) RunWithProgress(ctx context.Context, call core.ToolCall, progress func(core.ToolProgress)) (core.ToolResult, error) {
	if progress != nil {
		progress(core.ToolProgress{
			ToolCallID: call.ID,
			ToolName:   call.Name,
			Status:     "running",
			Summary:    "progress from child read",
		})
	}
	return t.testTool.Run(ctx, call)
}

func tc(name string, args map[string]any) core.ToolCall {
	b, _ := json.Marshal(args)
	return core.ToolCall{ID: name + "-" + time.Now().Format("150405.000000"), Name: name, Input: string(b)}
}
