package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/core"
)

func TestHookRunnerPreToolBlockByExitCode2(t *testing.T) {
	r := NewHookRunner([]ResolvedHook{{HookConfig: HookConfig{Command: "deny"}, Event: HookEventPreToolUse}}, ".")
	r.spawner = func(_ context.Context, _ HookSpawnInput) HookSpawnResult {
		return HookSpawnResult{ExitCode: 2, Stderr: "denied"}
	}
	report := r.Run(context.Background(), HookPayload{Event: HookEventPreToolUse, ToolName: "bash"})
	if !report.Blocked {
		t.Fatal("expected blocked report")
	}
	if len(report.Outcomes) != 1 || report.Outcomes[0].Decision != HookDecisionBlock {
		t.Fatalf("unexpected outcomes: %+v", report.Outcomes)
	}
}

func TestHookRunnerPostToolWarnByExitCode2(t *testing.T) {
	r := NewHookRunner([]ResolvedHook{{HookConfig: HookConfig{Command: "post"}, Event: HookEventPostToolUse}}, ".")
	r.spawner = func(_ context.Context, _ HookSpawnInput) HookSpawnResult {
		return HookSpawnResult{ExitCode: 2, Stderr: "warn"}
	}
	report := r.Run(context.Background(), HookPayload{Event: HookEventPostToolUse, ToolName: "echo"})
	if report.Blocked {
		t.Fatal("post hook should not block on exit 2")
	}
	if len(report.Outcomes) != 1 || report.Outcomes[0].Decision != HookDecisionWarn {
		t.Fatalf("unexpected outcomes: %+v", report.Outcomes)
	}
}

type preBlockProvider struct{ calls int }

func (p *preBlockProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 1)
	p.calls++
	if p.calls == 1 {
		out <- ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonToolUse, ToolCalls: []ToolCall{{ID: "tc-1", Name: "echo", Input: `{"x":1}`}}}}
		close(out)
		return out
	}
	out <- ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonEndTurn, Content: "done"}}
	close(out)
	return out
}

func TestAgentPreToolHookBlockSkipsDispatch(t *testing.T) {
	store := NewInMemoryStore()
	toolCalled := false
	tool := staticTool{name: "echo", run: func(_ context.Context, _ ToolCall) (ToolResult, error) {
		toolCalled = true
		return ToolResult{ToolCallID: "tc-1", Name: "echo", Content: "ok"}, nil
	}}
	a := NewAgentWithRegistry(&preBlockProvider{}, store, core.NewToolRegistry([]core.Tool{tool}), WithHooks([]ResolvedHook{{HookConfig: HookConfig{Command: "deny"}, Event: HookEventPreToolUse}}, "."))
	a.hooks.spawner = func(_ context.Context, _ HookSpawnInput) HookSpawnResult {
		return HookSpawnResult{ExitCode: 2, Stderr: "nope"}
	}
	_, err := a.Run(context.Background(), "s-pre-block", "hi")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if toolCalled {
		t.Fatal("tool should not be called when PreToolUse blocks")
	}
}

func TestReadOnlyTurnPolicyDeniesBeforePreToolHook(t *testing.T) {
	store := NewInMemoryStore()
	hookCalled := false
	a := NewAgentWithRegistry(
		&preBlockProvider{},
		store,
		core.NewToolRegistry([]core.Tool{writeLikeTool{}}),
		WithHooks([]ResolvedHook{{HookConfig: HookConfig{Command: "side-effect"}, Event: HookEventPreToolUse}}, "."),
	)
	a.hooks.spawner = func(_ context.Context, _ HookSpawnInput) HookSpawnResult {
		hookCalled = true
		return HookSpawnResult{ExitCode: 0}
	}
	events, err := a.RunStreamWithTurnOptions(context.Background(), "s-readonly-hook", "review", RunOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	var sawPolicyDeny bool
	for ev := range events {
		if ev.Type == AgentEventTypeHookStarted || ev.Type == AgentEventTypeHookCompleted || ev.Type == AgentEventTypeHookBlocked || ev.Type == AgentEventTypeHookFailed || ev.Type == AgentEventTypeHookWarned {
			t.Fatalf("PreToolUse hook should not run before read-only denial")
		}
		if ev.Type == AgentEventTypeToolPolicyDecision && ev.Policy != nil && ev.Policy.Code == "read_only_turn_denied" {
			sawPolicyDeny = true
		}
	}
	if hookCalled {
		t.Fatal("PreToolUse hook side effect ran before read-only denial")
	}
	if !sawPolicyDeny {
		t.Fatal("expected read-only policy denial")
	}
}

func TestLoadHooksProjectThenLocalThenGlobalOrder(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	ws := filepath.Join(root, "ws")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatalf("mkdir home hooks failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".whale"), 0o755); err != nil {
		t.Fatalf("mkdir workspace hooks failed: %v", err)
	}
	projectCfg := "[[hooks.PreToolUse]]\ncommand = \"echo project\"\n"
	projectLocalCfg := "[[hooks.PreToolUse]]\ncommand = \"echo project-local\"\n"
	globalCfg := "[[hooks.PreToolUse]]\ncommand = \"echo global\"\n"
	if err := os.WriteFile(filepath.Join(ws, ".whale", "config.toml"), []byte(projectCfg), 0o600); err != nil {
		t.Fatalf("write project config failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".whale", "config.local.toml"), []byte(projectLocalCfg), 0o600); err != nil {
		t.Fatalf("write project local config failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(globalCfg), 0o600); err != nil {
		t.Fatalf("write global config failed: %v", err)
	}
	hooks, loaded, err := LoadHooks(ws, home)
	if err != nil {
		t.Fatalf("load hooks failed: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 loaded sources, got %d", len(loaded))
	}
	if len(hooks) != 3 {
		t.Fatalf("expected 3 hooks, got %d", len(hooks))
	}
	if hooks[0].Command != "echo project" || hooks[1].Command != "echo project-local" || hooks[2].Command != "echo global" {
		t.Fatalf("unexpected order: %+v", hooks)
	}
}

func TestHookRunnerBlockShortCircuitsFollowingHooks(t *testing.T) {
	hooks := []ResolvedHook{
		{HookConfig: HookConfig{Command: "first"}, Event: HookEventPreToolUse},
		{HookConfig: HookConfig{Command: "second"}, Event: HookEventPreToolUse},
	}
	r := NewHookRunner(hooks, ".")
	calls := 0
	r.spawner = func(_ context.Context, in HookSpawnInput) HookSpawnResult {
		calls++
		if in.Command == "first" {
			return HookSpawnResult{ExitCode: 2, Stderr: "blocked"}
		}
		return HookSpawnResult{ExitCode: 0}
	}
	report := r.Run(context.Background(), HookPayload{Event: HookEventPreToolUse, ToolName: "bash"})
	if !report.Blocked {
		t.Fatal("expected blocked")
	}
	if calls != 1 {
		t.Fatalf("expected short-circuit after first hook, calls=%d", calls)
	}
}

func TestHookRunnerParsesStructuredJSONOutput(t *testing.T) {
	r := NewHookRunner([]ResolvedHook{{HookConfig: HookConfig{Command: "structured"}, Event: HookEventPreToolUse}}, ".")
	r.spawner = func(_ context.Context, _ HookSpawnInput) HookSpawnResult {
		return HookSpawnResult{ExitCode: 0, Stdout: `{"decision":"block","reason":"nope","additional_context":"ctx","updated_input":{"x":2},"metadata":{"k":"v"}}`}
	}
	report := r.Run(context.Background(), HookPayload{Event: HookEventPreToolUse, ToolName: "echo"})
	if !report.Blocked {
		t.Fatal("expected structured hook to block")
	}
	if report.AdditionalContext != "ctx" {
		t.Fatalf("additional context = %q", report.AdditionalContext)
	}
	if report.UpdatedInput != `{"x":2}` {
		t.Fatalf("updated input = %q", report.UpdatedInput)
	}
	if report.Metadata["k"] != "v" {
		t.Fatalf("metadata = %+v", report.Metadata)
	}
	if got := report.Outcomes[0].Message; got != "nope" {
		t.Fatalf("message = %q", got)
	}
}

func TestHookRunnerStructuredJSONPassOverridesExitCode(t *testing.T) {
	r := NewHookRunner([]ResolvedHook{{HookConfig: HookConfig{Command: "structured"}, Event: HookEventPreToolUse}}, ".")
	r.spawner = func(_ context.Context, _ HookSpawnInput) HookSpawnResult {
		return HookSpawnResult{ExitCode: 2, Stdout: `{"decision":"pass","reason":"allowed"}`, Stderr: "legacy block"}
	}
	report := r.Run(context.Background(), HookPayload{Event: HookEventPreToolUse, ToolName: "echo"})
	if report.Blocked {
		t.Fatalf("structured JSON pass should override blocking exit code: %+v", report)
	}
	if got := report.Outcomes[0].Decision; got != HookDecisionPass {
		t.Fatalf("decision = %q, want pass", got)
	}
	if got := report.Outcomes[0].Message; got != "allowed" {
		t.Fatalf("message = %q", got)
	}
}

func TestHookRunnerStructuredJSONPassDoesNotOverrideTimeout(t *testing.T) {
	r := NewHookRunner([]ResolvedHook{{HookConfig: HookConfig{Command: "structured"}, Event: HookEventPreToolUse}}, ".")
	r.spawner = func(_ context.Context, _ HookSpawnInput) HookSpawnResult {
		return HookSpawnResult{ExitCode: 0, TimedOut: true, Stdout: `{"decision":"pass","reason":"partial allow"}`}
	}
	report := r.Run(context.Background(), HookPayload{Event: HookEventPreToolUse, ToolName: "echo"})
	if !report.Blocked {
		t.Fatalf("timeout should fail closed even with partial JSON pass: %+v", report)
	}
	if got := report.Outcomes[0].Decision; got != HookDecisionBlock {
		t.Fatalf("decision = %q, want block", got)
	}
	if got := report.Outcomes[0].Message; got != "partial allow" {
		t.Fatalf("message = %q", got)
	}
}

func TestHookRunnerRefreshesShellHookPayloadAfterRewrite(t *testing.T) {
	hooks := []ResolvedHook{
		{HookConfig: HookConfig{Command: "rewrite"}, Event: HookEventPreToolUse},
		{HookConfig: HookConfig{Command: "observe"}, Event: HookEventPreToolUse},
	}
	r := NewHookRunner(hooks, ".")
	var secondStdin string
	r.spawner = func(_ context.Context, in HookSpawnInput) HookSpawnResult {
		if in.Command == "rewrite" {
			return HookSpawnResult{ExitCode: 0, Stdout: `{"updated_input":{"x":2}}`}
		}
		secondStdin = in.Stdin
		return HookSpawnResult{ExitCode: 0}
	}
	report := r.Run(context.Background(), HookPayload{Event: HookEventPreToolUse, ToolName: "echo", ToolArgs: map[string]any{"x": float64(1)}, ToolCall: &ToolCall{ID: "tc-1", Name: "echo", Input: `{"x":1}`}})
	if report.Blocked {
		t.Fatalf("unexpected block: %+v", report)
	}
	if !strings.Contains(secondStdin, `"x":2`) || strings.Contains(secondStdin, `"x":1`) {
		t.Fatalf("second shell hook saw stale payload: %s", secondStdin)
	}
}

func TestHookRunnerRefreshesPromptPayloadAfterRewrite(t *testing.T) {
	hooks := []ResolvedHook{
		{HookConfig: HookConfig{Command: "rewrite"}, Event: HookEventUserPromptSubmit},
		{HookConfig: HookConfig{Command: "observe"}, Event: HookEventUserPromptSubmit},
	}
	r := NewHookRunner(hooks, ".")
	var secondStdin string
	r.spawner = func(_ context.Context, in HookSpawnInput) HookSpawnResult {
		if in.Command == "rewrite" {
			return HookSpawnResult{ExitCode: 0, Stdout: `{"updated_input":"rewritten prompt"}`}
		}
		secondStdin = in.Stdin
		return HookSpawnResult{ExitCode: 0}
	}
	report := r.Run(context.Background(), NewUserPromptSubmitPayload("s1", ".", "original prompt"))
	if report.Blocked {
		t.Fatalf("unexpected block: %+v", report)
	}
	if report.UpdatedInput != "rewritten prompt" {
		t.Fatalf("report updated input = %q", report.UpdatedInput)
	}
	if !strings.Contains(secondStdin, `"prompt":"rewritten prompt"`) || strings.Contains(secondStdin, `"prompt":"original prompt"`) {
		t.Fatalf("second prompt hook saw stale payload: %s", secondStdin)
	}
}

func TestAgentPreToolHookHandlerCanRewriteInputAndAddContext(t *testing.T) {
	store := NewInMemoryStore()
	tool := staticTool{name: "echo", run: func(_ context.Context, call ToolCall) (ToolResult, error) {
		if !strings.Contains(call.Input, `"x":2`) {
			t.Fatalf("tool input was not rewritten: %s", call.Input)
		}
		content, err := core.MarshalToolEnvelope(core.NewToolSuccessEnvelope(map[string]any{"result": "tool ok"}))
		if err != nil {
			t.Fatalf("marshal envelope: %v", err)
		}
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content}, nil
	}}
	a := NewAgentWithRegistry(&preBlockProvider{}, store, core.NewToolRegistry([]core.Tool{tool}),
		WithHookHandlers(HookHandler{
			Event:  HookEventPreToolUse,
			Match:  "echo",
			Name:   "rewrite",
			Source: "test",
			Run: func(context.Context, HookPayload) HookResult {
				return HookResult{Decision: HookDecisionPass, UpdatedInput: `{"x":2}`, AdditionalContext: "hook ctx"}
			},
		}),
	)
	if _, err := a.Run(context.Background(), "s-rewrite", "hi"); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	msgs, err := store.List(context.Background(), "s-rewrite")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, msg := range msgs {
		for _, result := range msg.ToolResults {
			env, ok := core.ParseToolEnvelope(result.Content)
			if !ok {
				t.Fatalf("tool envelope corrupted by hook context: %s", result.Content)
			}
			if strings.Contains(result.Content, "hook ctx") && env.Metadata["hook_context"] != nil {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("hook additional context was not appended to tool result: %+v", msgs)
	}
}

func TestHookRunnerPluginHandlerTimeoutDoesNotWaitForReturn(t *testing.T) {
	r := NewHookRunner(nil, ".")
	entered := make(chan struct{})
	release := make(chan struct{})
	r.AddHandlers(HookHandler{
		Event:     HookEventPreToolUse,
		Name:      "slow-plugin-hook",
		Source:    "plugin:test",
		TimeoutMS: 10,
		Run: func(context.Context, HookPayload) HookResult {
			close(entered)
			<-release
			return HookResult{Decision: HookDecisionPass}
		},
	})
	start := time.Now()
	report := r.Run(context.Background(), HookPayload{Event: HookEventPreToolUse, ToolName: "echo"})
	elapsed := time.Since(start)
	close(release)
	<-entered
	if elapsed > 250*time.Millisecond {
		t.Fatalf("plugin hook timeout waited for handler return: %s", elapsed)
	}
	if len(report.Outcomes) != 1 || report.Outcomes[0].Decision != HookDecisionTimeout {
		t.Fatalf("expected timeout outcome, got %+v", report.Outcomes)
	}
	if !report.Blocked {
		t.Fatalf("PreToolUse plugin hook timeout should fail closed: %+v", report)
	}
}

func TestAgentPreToolShellHookJSONCanRewriteInput(t *testing.T) {
	store := NewInMemoryStore()
	tool := staticTool{name: "echo", run: func(_ context.Context, call ToolCall) (ToolResult, error) {
		if !strings.Contains(call.Input, `"x":2`) {
			t.Fatalf("tool input was not rewritten by shell hook: %s", call.Input)
		}
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "tool ok"}, nil
	}}
	a := NewAgentWithRegistry(&preBlockProvider{}, store, core.NewToolRegistry([]core.Tool{tool}),
		WithHooks([]ResolvedHook{{HookConfig: HookConfig{Command: "rewrite"}, Event: HookEventPreToolUse}}, "."),
	)
	a.hooks.spawner = func(_ context.Context, _ HookSpawnInput) HookSpawnResult {
		return HookSpawnResult{ExitCode: 0, Stdout: `{"updated_input":{"x":2},"additional_context":"shell ctx"}`}
	}
	if _, err := a.Run(context.Background(), "s-shell-rewrite", "hi"); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	msgs, err := store.List(context.Background(), "s-shell-rewrite")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, msg := range msgs {
		for _, result := range msg.ToolResults {
			if strings.Contains(result.Content, "shell ctx") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("shell hook additional context was not appended to tool result: %+v", msgs)
	}
}

func TestAgentPostToolShellHookWarnDoesNotFailTurn(t *testing.T) {
	store := NewInMemoryStore()
	toolCalled := false
	tool := staticTool{name: "echo", run: func(_ context.Context, call ToolCall) (ToolResult, error) {
		toolCalled = true
		return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "tool ok"}, nil
	}}
	a := NewAgentWithRegistry(&preBlockProvider{}, store, core.NewToolRegistry([]core.Tool{tool}),
		WithHooks([]ResolvedHook{{HookConfig: HookConfig{Command: "post-warn"}, Event: HookEventPostToolUse}}, "."),
	)
	a.hooks.spawner = func(_ context.Context, _ HookSpawnInput) HookSpawnResult {
		return HookSpawnResult{ExitCode: 2, Stderr: "post warning"}
	}
	final, err := a.Run(context.Background(), "s-post-warn", "hi")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !toolCalled {
		t.Fatal("tool should still execute when PostToolUse warns")
	}
	if final.Text != "done" {
		t.Fatalf("turn did not complete normally: %+v", final)
	}
}

func TestAgentDoesNotTriggerUserPromptOrStopHooks(t *testing.T) {
	store := NewInMemoryStore()
	provider := &noToolProvider{}
	hooks := []ResolvedHook{
		{HookConfig: HookConfig{Command: "exit 2"}, Event: HookEventUserPromptSubmit},
		{HookConfig: HookConfig{Command: "exit 2"}, Event: HookEventStop},
	}
	a := NewAgentWithRegistry(provider, store, core.NewToolRegistry(nil), WithHooks(hooks, "."))
	calls := 0
	a.hooks.spawner = func(_ context.Context, _ HookSpawnInput) HookSpawnResult {
		calls++
		return HookSpawnResult{ExitCode: 2}
	}
	_, err := a.Run(context.Background(), "s-no-app-hooks", "hello")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if calls != 0 {
		t.Fatalf("expected 0 hook invocations in agent for UserPromptSubmit/Stop, got %d", calls)
	}
}

type noToolProvider struct{}

func (p *noToolProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 1)
	out <- ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonEndTurn, Content: "ok"}}
	close(out)
	return out
}

func TestHookRunnerRealShellPreToolBlock(t *testing.T) {
	r := NewHookRunner([]ResolvedHook{{HookConfig: HookConfig{Command: "echo blocked >&2; exit 2"}, Event: HookEventPreToolUse}}, ".")
	report := r.Run(context.Background(), HookPayload{Event: HookEventPreToolUse, ToolName: "bash"})
	if !report.Blocked {
		t.Fatal("expected blocked")
	}
	if len(report.Outcomes) != 1 || report.Outcomes[0].Decision != HookDecisionBlock {
		t.Fatalf("unexpected outcomes: %+v", report.Outcomes)
	}
}

func TestHookRunnerRealShellPostToolWarn(t *testing.T) {
	r := NewHookRunner([]ResolvedHook{{HookConfig: HookConfig{Command: "echo post-warn >&2; exit 5"}, Event: HookEventPostToolUse}}, ".")
	report := r.Run(context.Background(), HookPayload{Event: HookEventPostToolUse, ToolName: "echo"})
	if report.Blocked {
		t.Fatal("post tool should not block")
	}
	if len(report.Outcomes) != 1 || report.Outcomes[0].Decision != HookDecisionWarn {
		t.Fatalf("unexpected outcomes: %+v", report.Outcomes)
	}
}

func TestHookRunnerStopPayloadCarriesAssistantTextAndTurn(t *testing.T) {
	tmp := t.TempDir()
	capture := filepath.Join(tmp, "payload.json")
	cmd := "cat > " + capture + "; exit 0"
	r := NewHookRunner([]ResolvedHook{{HookConfig: HookConfig{Command: cmd}, Event: HookEventStop}}, ".")
	payload := NewStopPayload("s1", tmp, "final answer", 3)
	report := r.Run(context.Background(), payload)
	if report.Blocked {
		t.Fatal("stop should not block")
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("read payload failed: %v", err)
	}
	var got HookPayload
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal payload failed: %v", err)
	}
	if got.Event != HookEventStop || got.LastAssistantText != "final answer" || got.Turn != 3 || got.SessionID != "s1" {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

type staticTool struct {
	name string
	run  func(context.Context, ToolCall) (ToolResult, error)
}

func (t staticTool) Name() string { return t.name }
func (t staticTool) Run(ctx context.Context, call ToolCall) (ToolResult, error) {
	return t.run(ctx, call)
}
