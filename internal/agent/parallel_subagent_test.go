package agent

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

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

type parallelSpawnProvider struct {
	calls int
}

func (p *parallelSpawnProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	if p.calls > 1 {
		return eventStream(endTurnEvent("done"))
	}
	return eventStream(toolUseEvent(
		toolCall("tc-subagent-1", "spawn_subagent", `{"role":"explore","task":"read a"}`),
		toolCall("tc-subagent-2", "spawn_subagent", `{"role":"review","task":"read b"}`),
		toolCall("tc-subagent-3", "spawn_subagent", `{"role":"audit","task":"read c"}`),
	))
}

type delayedSpawnSubagentTool struct {
	delay   time.Duration
	calls   atomic.Int32
	running atomic.Int32
	max     atomic.Int32
}

func (t *delayedSpawnSubagentTool) Name() string { return "spawn_subagent" }
func (t *delayedSpawnSubagentTool) Run(ctx context.Context, call ToolCall) (ToolResult, error) {
	t.calls.Add(1)
	running := t.running.Add(1)
	for {
		max := t.max.Load()
		if running <= max || t.max.CompareAndSwap(max, running) {
			break
		}
	}
	defer t.running.Add(-1)

	timer := time.NewTimer(t.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ToolResult{}, ctx.Err()
	case <-timer.C:
	}
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "ok:" + call.ID}, nil
}

type reverseDelaySpawnSubagentTool struct {
	calls   atomic.Int32
	running atomic.Int32
	max     atomic.Int32
}

func (t *reverseDelaySpawnSubagentTool) Name() string { return "spawn_subagent" }
func (t *reverseDelaySpawnSubagentTool) Run(ctx context.Context, call ToolCall) (ToolResult, error) {
	t.calls.Add(1)
	running := t.running.Add(1)
	for {
		max := t.max.Load()
		if running <= max || t.max.CompareAndSwap(max, running) {
			break
		}
	}
	defer t.running.Add(-1)

	delay := 10 * time.Millisecond
	switch call.ID {
	case "tc-subagent-1":
		delay = 120 * time.Millisecond
	case "tc-subagent-2":
		delay = 70 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ToolResult{}, ctx.Err()
	case <-timer.C:
	}
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "ok:" + call.ID}, nil
}

func TestReadySpawnSubagentGroupRunsConcurrently(t *testing.T) {
	store := NewInMemoryStore()
	spawn := &delayedSpawnSubagentTool{delay: 300 * time.Millisecond}
	a := NewAgentWithRegistry(
		&parallelSpawnProvider{},
		store,
		NewToolRegistry([]Tool{spawn}),
	)

	start := time.Now()
	events, err := a.RunStream(context.Background(), "s-parallel-subagents", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	for ev := range events {
		if ev.Type == AgentEventTypeError && ev.Err != nil {
			t.Fatalf("run stream emitted error: %v", ev.Err)
		}
	}
	elapsed := time.Since(start)

	if got := spawn.calls.Load(); got != 3 {
		t.Fatalf("expected 3 spawn_subagent calls, got %d", got)
	}
	if got := spawn.max.Load(); got < 2 {
		t.Fatalf("expected overlapping spawn_subagent calls, max concurrency was %d", got)
	}
	if elapsed >= 650*time.Millisecond {
		t.Fatalf("expected concurrent wall-clock under 650ms for three 300ms calls, got %s", elapsed)
	}

	msgs, err := store.List(context.Background(), "s-parallel-subagents")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(msgs) < 3 {
		t.Fatalf("expected at least user, assistant, and tool messages, got %d", len(msgs))
	}
	toolMsg := msgs[2]
	if len(toolMsg.ToolResults) != 3 {
		t.Fatalf("expected 3 tool results, got %d", len(toolMsg.ToolResults))
	}
	for i, wantID := range []string{"tc-subagent-1", "tc-subagent-2", "tc-subagent-3"} {
		if toolMsg.ToolResults[i].ToolCallID != wantID {
			t.Fatalf("tool result %d id = %q, want %q", i, toolMsg.ToolResults[i].ToolCallID, wantID)
		}
	}
}

func TestParallelSpawnSubagentResultsUseOriginalOrderAfterOutOfOrderCompletion(t *testing.T) {
	store := NewInMemoryStore()
	spawn := &reverseDelaySpawnSubagentTool{}
	postHookOrder := []string{}
	a := NewAgentWithRegistry(
		&parallelSpawnProvider{},
		store,
		NewToolRegistry([]Tool{spawn}),
		WithHookHandlers(HookHandler{
			Event: HookEventPostToolUse,
			Match: "spawn_subagent",
			Name:  "record-post-order",
			Run: func(_ context.Context, payload HookPayload) HookResult {
				if payload.ToolCall != nil {
					postHookOrder = append(postHookOrder, payload.ToolCall.ID)
				}
				return HookResult{}
			},
		}),
	)

	events, err := a.RunStream(context.Background(), "s-parallel-subagents-order", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var resultEventOrder []string
	for ev := range events {
		if ev.Type == AgentEventTypeError && ev.Err != nil {
			t.Fatalf("run stream emitted error: %v", ev.Err)
		}
		if ev.Type == AgentEventTypeToolResult && ev.Result != nil {
			resultEventOrder = append(resultEventOrder, ev.Result.ToolCallID)
		}
	}

	if got := spawn.calls.Load(); got != 3 {
		t.Fatalf("expected 3 spawn_subagent calls, got %d", got)
	}
	if got := spawn.max.Load(); got < 2 {
		t.Fatalf("expected overlapping spawn_subagent calls, max concurrency was %d", got)
	}

	wantOrder := []string{"tc-subagent-1", "tc-subagent-2", "tc-subagent-3"}
	if !sameStringSlice(postHookOrder, wantOrder) {
		t.Fatalf("post hook order = %v, want %v", postHookOrder, wantOrder)
	}
	if !sameStringSlice(resultEventOrder, wantOrder) {
		t.Fatalf("tool result event order = %v, want %v", resultEventOrder, wantOrder)
	}

	msgs, err := store.List(context.Background(), "s-parallel-subagents-order")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(msgs) < 3 {
		t.Fatalf("expected at least user, assistant, and tool messages, got %d", len(msgs))
	}
	toolMessages := 0
	var toolMsg Message
	for _, msg := range msgs {
		if msg.Role == RoleTool {
			toolMessages++
			toolMsg = msg
		}
	}
	if toolMessages != 1 {
		t.Fatalf("expected one persisted tool message, got %d", toolMessages)
	}
	if len(toolMsg.ToolResults) != 3 {
		t.Fatalf("expected 3 tool results, got %d", len(toolMsg.ToolResults))
	}
	gotOrder := make([]string, 0, len(toolMsg.ToolResults))
	for _, result := range toolMsg.ToolResults {
		gotOrder = append(gotOrder, result.ToolCallID)
	}
	if !sameStringSlice(gotOrder, wantOrder) {
		t.Fatalf("persisted tool result order = %v, want %v", gotOrder, wantOrder)
	}
}

func sameStringSlice(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
