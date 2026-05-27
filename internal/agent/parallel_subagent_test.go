package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
)

func TestEligibleParallelSubagentGroupsConsecutiveSpawnSubagents(t *testing.T) {
	calls := []core.ToolCall{
		{ID: "1", Name: "spawn_subagent"},
		{ID: "2", Name: "spawn_subagent"},
		{ID: "3", Name: "spawn_subagent"},
	}

	groups := eligibleParallelSubagentGroups(calls)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].Start != 0 {
		t.Fatalf("expected group start 0, got %d", groups[0].Start)
	}
	if len(groups[0].Calls) != 3 {
		t.Fatalf("expected 3 calls in group, got %d", len(groups[0].Calls))
	}
}

func TestEligibleParallelSubagentGroupsMixedToolsCreateBoundaries(t *testing.T) {
	calls := []core.ToolCall{
		{ID: "1", Name: "spawn_subagent"},
		{ID: "2", Name: "read_file"},
		{ID: "3", Name: "spawn_subagent"},
		{ID: "4", Name: "spawn_subagent"},
		{ID: "5", Name: "shell"},
		{ID: "6", Name: "spawn_subagent"},
		{ID: "7", Name: "spawn_subagent"},
		{ID: "8", Name: "apply_patch"},
		{ID: "9", Name: "todo_add"},
		{ID: "10", Name: "request_user_input"},
		{ID: "11", Name: "spawn_subagent"},
		{ID: "12", Name: "spawn_subagent"},
	}

	groups := eligibleParallelSubagentGroups(calls)
	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(groups))
	}
	if groups[0].Start != 2 || len(groups[0].Calls) != 2 {
		t.Fatalf("unexpected first group: %+v", groups[0])
	}
	if groups[1].Start != 5 || len(groups[1].Calls) != 2 {
		t.Fatalf("unexpected second group: %+v", groups[1])
	}
	if groups[2].Start != 10 || len(groups[2].Calls) != 2 {
		t.Fatalf("unexpected third group: %+v", groups[2])
	}
}

func TestEligibleParallelSubagentGroupsParallelReasonIsBoundary(t *testing.T) {
	calls := []core.ToolCall{
		{ID: "1", Name: "spawn_subagent"},
		{ID: "2", Name: "parallel_reason"},
		{ID: "3", Name: "spawn_subagent"},
		{ID: "4", Name: "spawn_subagent"},
	}

	groups := eligibleParallelSubagentGroups(calls)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if groups[0].Start != 2 || len(groups[0].Calls) != 2 {
		t.Fatalf("unexpected group: %+v", groups[0])
	}
}

func TestEligibleParallelSubagentGroupsRequiresAtLeastTwoReadyCalls(t *testing.T) {
	calls := []core.ToolCall{
		{ID: "1", Name: "spawn_subagent"},
		{ID: "2", Name: "read_file"},
		{ID: "3", Name: "spawn_subagent"},
	}

	groups := eligibleParallelSubagentGroups(calls)
	if len(groups) != 0 {
		t.Fatalf("expected no groups for single ready spawn_subagent calls, got %+v", groups)
	}
}

func TestEligibleReadyParallelSubagentGroupsSkipsBlockedGaps(t *testing.T) {
	ready := []readyParallelSubagentCall{
		{Index: 0, Call: core.ToolCall{ID: "1", Name: "spawn_subagent"}},
		{Index: 2, Call: core.ToolCall{ID: "3", Name: "spawn_subagent"}},
		{Index: 3, Call: core.ToolCall{ID: "4", Name: "spawn_subagent"}},
	}

	groups := eligibleReadyParallelSubagentGroups(ready)
	if len(groups) != 1 {
		t.Fatalf("expected only one group after blocked gap, got %+v", groups)
	}
	if groups[0].Start != 2 || len(groups[0].Calls) != 2 {
		t.Fatalf("unexpected ready group: %+v", groups[0])
	}
}

func TestMaybeReadyParallelSubagentCallOnlyMarksSpawnSubagent(t *testing.T) {
	if ready, ok := maybeReadyParallelSubagentCall(4, core.ToolCall{ID: "s", Name: "spawn_subagent"}); !ok || ready.Index != 4 || ready.Call.ID != "s" {
		t.Fatalf("expected spawn_subagent to be marked ready, got ready=%+v ok=%v", ready, ok)
	}
	if ready, ok := maybeReadyParallelSubagentCall(5, core.ToolCall{ID: "r", Name: "read_file"}); ok {
		t.Fatalf("read_file should not be a parallel subagent candidate: %+v", ready)
	}
}

type spawnThenReadProvider struct {
	calls int
}

func (p *spawnThenReadProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	if p.calls > 1 {
		return eventStream(endTurnEvent("done"))
	}
	return eventStream(toolUseEvent(
		toolCall("tc-subagent-1", "spawn_subagent", `{"role":"explore","task":"read files"}`),
		toolCall("tc-read-1", "read_counter", `{}`),
	))
}

type readOnlyCountingTool struct {
	calls int
}

func (c *readOnlyCountingTool) Name() string { return "read_counter" }
func (c *readOnlyCountingTool) ReadOnly() bool {
	return true
}
func (c *readOnlyCountingTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	c.calls++
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "ok"}, nil
}

func TestSpawnSubagentHookBlockDoesNotSkipFollowingSerialCall(t *testing.T) {
	store := NewInMemoryStore()
	counter := &readOnlyCountingTool{}
	a := NewAgentWithRegistry(
		&spawnThenReadProvider{},
		store,
		NewToolRegistry([]Tool{namedNoopTool("spawn_subagent"), counter}),
		WithHooks([]ResolvedHook{{HookConfig: HookConfig{Command: "gate"}, Event: HookEventPreToolUse}}, "."),
	)
	a.hooks.spawner = func(_ context.Context, in HookSpawnInput) HookSpawnResult {
		var payload HookPayload
		if err := json.Unmarshal([]byte(in.Stdin), &payload); err != nil {
			return HookSpawnResult{ExitCode: 2, Stderr: err.Error()}
		}
		if payload.ToolName == "spawn_subagent" {
			return HookSpawnResult{ExitCode: 2, Stderr: "blocked"}
		}
		return HookSpawnResult{ExitCode: 0}
	}

	events, err := a.RunStream(context.Background(), "s-subagent-hook-block", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawHookBlock bool
	for ev := range events {
		if ev.Type == AgentEventTypeToolResult && ev.Result != nil && ev.Result.ToolCallID == "tc-subagent-1" && ev.Result.IsError {
			sawHookBlock = true
		}
	}
	if !sawHookBlock {
		t.Fatal("expected spawn_subagent hook block result")
	}
	if counter.calls != 1 {
		t.Fatalf("expected following read-only call to execute serially, got %d", counter.calls)
	}
}

func TestSpawnSubagentPolicyBlockDoesNotSkipFollowingSerialCall(t *testing.T) {
	store := NewInMemoryStore()
	counter := &readOnlyCountingTool{}
	a := NewAgentWithRegistry(
		&spawnThenReadProvider{},
		store,
		NewToolRegistry([]Tool{namedNoopTool("spawn_subagent"), counter}),
		WithToolPolicy(RulePolicy{
			Default: PermissionAllow,
			Rules:   []PermissionRule{{Permission: "task", Pattern: "*", Action: policy.PermissionDeny}},
		}),
	)

	events, err := a.RunStream(context.Background(), "s-subagent-policy-block", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawPolicyBlock bool
	for ev := range events {
		if ev.Type == AgentEventTypeToolPolicyDecision && ev.Policy != nil && ev.Policy.ToolCallID == "tc-subagent-1" && !ev.Policy.Allow {
			sawPolicyBlock = true
		}
	}
	if !sawPolicyBlock {
		t.Fatal("expected spawn_subagent policy block")
	}
	if counter.calls != 1 {
		t.Fatalf("expected following read-only call to execute serially, got %d", counter.calls)
	}
}

func TestSpawnSubagentModeBlockDoesNotSkipFollowingSerialCall(t *testing.T) {
	store := NewInMemoryStore()
	counter := &readOnlyCountingTool{}
	a := NewAgentWithRegistry(
		&spawnThenReadProvider{},
		store,
		NewToolRegistry([]Tool{namedNoopTool("spawn_subagent"), counter}),
		WithSessionMode(session.ModeAsk),
	)

	events, err := a.RunStream(context.Background(), "s-subagent-mode-block", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawModeBlock bool
	for ev := range events {
		if ev.Type == AgentEventTypeToolModeBlocked && ev.ToolBlocked != nil && ev.ToolBlocked.ToolCallID == "tc-subagent-1" {
			sawModeBlock = true
		}
	}
	if !sawModeBlock {
		t.Fatal("expected spawn_subagent mode block")
	}
	if counter.calls != 1 {
		t.Fatalf("expected following read-only call to execute serially, got %d", counter.calls)
	}
}
