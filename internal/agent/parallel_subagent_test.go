package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
)

func eligibleParallelSubagentGroups(calls []core.ToolCall) []parallelSubagentGroup {
	ready := make([]readyParallelSubagentCall, 0, len(calls))
	for i, call := range calls {
		ready = append(ready, readyParallelSubagentCall{Index: i, Call: call})
	}
	return eligibleReadyParallelSubagentGroups(ready)
}

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

func TestDefaultMaxParallelSubagentsCapsHighCPUCount(t *testing.T) {
	orig := runtimeNumCPU
	t.Cleanup(func() { runtimeNumCPU = orig })
	runtimeNumCPU = func() int { return 128 }

	if got := defaultMaxParallelSubagents(); got != defaultMaxParallelSubagentCap {
		t.Fatalf("default max parallel subagents = %d, want cap %d", got, defaultMaxParallelSubagentCap)
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

type mockSpawnSubagentTool struct {
	calls   atomic.Int32
	running atomic.Int32
	max     atomic.Int32

	delay        time.Duration
	delayByID    map[string]time.Duration
	failByID     map[string]bool
	progressByID map[string][]mockSpawnProgress
	result       func(ToolCall) ToolResult
	started      chan string
	cancelSeen   chan string
	waitCancel   bool
}

type mockSpawnProgress struct {
	Delay   time.Duration
	Status  string
	Summary string
	Role    string
	Model   string
	Count   int
}

func newCancelObservedSpawnSubagentTool(n int) *mockSpawnSubagentTool {
	return &mockSpawnSubagentTool{
		started:    make(chan string, n),
		cancelSeen: make(chan string, n),
		waitCancel: true,
	}
}

func (t *mockSpawnSubagentTool) Name() string { return "spawn_subagent" }
func (t *mockSpawnSubagentTool) ReadOnly() bool {
	return true
}
func (t *mockSpawnSubagentTool) Run(ctx context.Context, call ToolCall) (ToolResult, error) {
	return t.RunWithProgress(ctx, call, nil)
}
func (t *mockSpawnSubagentTool) RunWithProgress(ctx context.Context, call ToolCall, progress func(core.ToolProgress)) (ToolResult, error) {
	t.calls.Add(1)
	running := t.running.Add(1)
	for {
		max := t.max.Load()
		if running <= max || t.max.CompareAndSwap(max, running) {
			break
		}
	}
	defer t.running.Add(-1)

	if t.started != nil {
		t.started <- call.ID
	}
	if t.waitCancel {
		<-ctx.Done()
		if t.cancelSeen != nil {
			t.cancelSeen <- call.ID
		}
		return ToolResult{}, ctx.Err()
	}

	steps := t.progressByID[call.ID]
	for _, step := range steps {
		if err := waitOrCancel(ctx, step.Delay); err != nil {
			return ToolResult{}, err
		}
		if progress != nil {
			progress(core.ToolProgress{
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Status:     step.Status,
				Summary:    step.Summary,
				Role:       step.Role,
				Model:      step.Model,
				Count:      step.Count,
			})
		}
	}

	delay := t.delay
	if d, ok := t.delayByID[call.ID]; ok {
		delay = d
	}
	if err := waitOrCancel(ctx, delay); err != nil {
		return ToolResult{}, err
	}
	if t.failByID[call.ID] {
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Content:    `{"success":false,"error":"subagent failed","code":"spawn_subagent_failed"}`,
			IsError:    true,
		}, nil
	}
	if t.result != nil {
		return t.result(call), nil
	}
	if len(steps) > 0 {
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Content:    `{"success":true,"data":{"role":"review","summary":"subagent completed"}}`,
		}, nil
	}
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "ok:" + call.ID}, nil
}

func waitOrCancel(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func TestReadySpawnSubagentGroupRunsConcurrently(t *testing.T) {
	store := NewInMemoryStore()
	spawn := &mockSpawnSubagentTool{delay: 300 * time.Millisecond}
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

func TestParallelSpawnSubagentsRespectsConfiguredConcurrencyLimit(t *testing.T) {
	store := NewInMemoryStore()
	spawn := &mockSpawnSubagentTool{delay: 80 * time.Millisecond}
	a := NewAgentWithRegistry(
		&parallelSpawnProvider{},
		store,
		NewToolRegistry([]Tool{spawn}),
		WithMaxParallelSubagents(2),
	)

	events, err := a.RunStream(context.Background(), "s-parallel-subagents-limit", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	for ev := range events {
		if ev.Type == AgentEventTypeError && ev.Err != nil {
			t.Fatalf("run stream emitted error: %v", ev.Err)
		}
	}

	if got := spawn.calls.Load(); got != 3 {
		t.Fatalf("expected 3 spawn_subagent calls, got %d", got)
	}
	if got := spawn.max.Load(); got != 2 {
		t.Fatalf("max concurrency = %d, want 2", got)
	}
}

func TestParallelSpawnSubagentsLimitOneRunsSeriallyInOriginalOrder(t *testing.T) {
	store := NewInMemoryStore()
	spawn := &mockSpawnSubagentTool{delayByID: map[string]time.Duration{
		"tc-subagent-1": 120 * time.Millisecond,
		"tc-subagent-2": 70 * time.Millisecond,
		"tc-subagent-3": 10 * time.Millisecond,
	}}
	a := NewAgentWithRegistry(
		&parallelSpawnProvider{},
		store,
		NewToolRegistry([]Tool{spawn}),
		WithMaxParallelSubagents(1),
	)

	events, err := a.RunStream(context.Background(), "s-parallel-subagents-limit-one", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var resultEventOrder []string
	for ev := range events {
		if ev.Type == AgentEventTypeError && ev.Err != nil {
			t.Fatalf("run stream emitted error: %v", ev.Err)
		}
		if ev.Type == AgentEventTypeToolResult && ev.Result != nil && ev.Result.Name == "spawn_subagent" {
			resultEventOrder = append(resultEventOrder, ev.Result.ToolCallID)
		}
	}

	if got := spawn.calls.Load(); got != 3 {
		t.Fatalf("expected 3 spawn_subagent calls, got %d", got)
	}
	if got := spawn.max.Load(); got != 1 {
		t.Fatalf("max concurrency = %d, want 1", got)
	}
	wantOrder := []string{"tc-subagent-1", "tc-subagent-2", "tc-subagent-3"}
	if !sameStringSlice(resultEventOrder, wantOrder) {
		t.Fatalf("tool result event order = %v, want %v", resultEventOrder, wantOrder)
	}
}

func TestParallelSpawnSubagentsShareParentContextCancellation(t *testing.T) {
	store := NewInMemoryStore()
	spawn := newCancelObservedSpawnSubagentTool(3)
	a := NewAgentWithRegistry(
		&parallelSpawnProvider{},
		store,
		NewToolRegistry([]Tool{spawn}),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := a.RunStream(ctx, "s-parallel-subagents-cancel", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}

	started := map[string]bool{}
	for len(started) < 3 {
		select {
		case id := <-spawn.started:
			started[id] = true
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for parallel subagents to start; started=%v calls=%d max=%d", started, spawn.calls.Load(), spawn.max.Load())
		}
	}

	cancel()

	cancelSeen := map[string]bool{}
	for len(cancelSeen) < 3 {
		select {
		case id := <-spawn.cancelSeen:
			cancelSeen[id] = true
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for parallel subagents to observe cancellation; seen=%v", cancelSeen)
		}
	}

	seenCancelled := false
	var toolResultEvents []ToolResult
	var subagentDoneEvents []TaskActivityInfo
	for ev := range events {
		switch ev.Type {
		case AgentEventTypeError:
			if ev.Err != nil {
				t.Fatalf("run stream emitted error: %v", ev.Err)
			}
		case AgentEventTypeTurnCancelled:
			seenCancelled = true
		case AgentEventTypeToolResult:
			if ev.Result != nil && ev.Result.Name == "spawn_subagent" {
				toolResultEvents = append(toolResultEvents, *ev.Result)
			}
		case AgentEventTypeSubagentDone:
			if ev.Task != nil {
				subagentDoneEvents = append(subagentDoneEvents, *ev.Task)
			}
		case AgentEventTypeDone:
			t.Fatalf("unexpected done event after parent cancellation")
		}
	}

	if !seenCancelled {
		t.Fatal("expected turn_cancelled event")
	}
	if len(toolResultEvents) != 0 {
		t.Fatalf("expected no completed spawn_subagent tool results after cancellation, got %+v", toolResultEvents)
	}
	if len(subagentDoneEvents) != 0 {
		t.Fatalf("expected no subagent completed events after cancellation, got %+v", subagentDoneEvents)
	}
	if got := spawn.calls.Load(); got != 3 {
		t.Fatalf("expected 3 spawn_subagent calls, got %d", got)
	}
	if got := spawn.max.Load(); got < 2 {
		t.Fatalf("expected overlapping spawn_subagent calls, max concurrency was %d", got)
	}
	for _, wantID := range []string{"tc-subagent-1", "tc-subagent-2", "tc-subagent-3"} {
		if !started[wantID] {
			t.Fatalf("expected %s to start, started=%v", wantID, started)
		}
		if !cancelSeen[wantID] {
			t.Fatalf("expected %s to observe cancellation, seen=%v", wantID, cancelSeen)
		}
	}

	msgs, err := store.List(context.Background(), "s-parallel-subagents-cancel")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	for _, msg := range msgs {
		if msg.Role == RoleTool {
			t.Fatalf("expected cancellation to skip persisted fake tool results, got tool message %+v", msg)
		}
	}
	last := msgs[len(msgs)-1]
	if last.Role != RoleUser || !last.Hidden || last.FinishReason != FinishReasonCanceled || !strings.Contains(last.Text, "<turn_aborted>") {
		t.Fatalf("expected hidden interrupt marker, got: %+v", last)
	}
}

func TestParallelSpawnSubagentResultsUseOriginalOrderAfterOutOfOrderCompletion(t *testing.T) {
	store := NewInMemoryStore()
	spawn := &mockSpawnSubagentTool{delayByID: map[string]time.Duration{
		"tc-subagent-1": 120 * time.Millisecond,
		"tc-subagent-2": 70 * time.Millisecond,
		"tc-subagent-3": 10 * time.Millisecond,
	}}
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

func TestParallelSpawnSubagentFailureIsIsolatedToOriginalResultSlot(t *testing.T) {
	store := NewInMemoryStore()
	spawn := &mockSpawnSubagentTool{
		delayByID: map[string]time.Duration{
			"tc-subagent-1": 80 * time.Millisecond,
			"tc-subagent-2": 20 * time.Millisecond,
			"tc-subagent-3": 80 * time.Millisecond,
		},
		failByID: map[string]bool{"tc-subagent-2": true},
		result: func(call ToolCall) ToolResult {
			return ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Content:    `{"success":true,"data":{"summary":"ok:` + call.ID + `"}}`,
			}
		},
	}
	a := NewAgentWithRegistry(
		&parallelSpawnProvider{},
		store,
		NewToolRegistry([]Tool{spawn}),
	)

	events, err := a.RunStream(context.Background(), "s-parallel-subagents-isolated-failure", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var resultEvents []ToolResult
	for ev := range events {
		if ev.Type == AgentEventTypeError && ev.Err != nil {
			t.Fatalf("run stream emitted error: %v", ev.Err)
		}
		if ev.Type == AgentEventTypeToolResult && ev.Result != nil && ev.Result.Name == "spawn_subagent" {
			resultEvents = append(resultEvents, *ev.Result)
		}
	}

	if got := spawn.calls.Load(); got != 3 {
		t.Fatalf("expected 3 spawn_subagent calls, got %d", got)
	}
	if got := spawn.max.Load(); got < 2 {
		t.Fatalf("expected overlapping spawn_subagent calls, max concurrency was %d", got)
	}

	msgs, err := store.List(context.Background(), "s-parallel-subagents-isolated-failure")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	var toolMsg Message
	toolMessages := 0
	for _, msg := range msgs {
		if msg.Role == RoleTool {
			toolMessages++
			toolMsg = msg
		}
	}
	if toolMessages != 1 {
		t.Fatalf("expected one persisted tool message, got %d", toolMessages)
	}

	wantIDs := []string{"tc-subagent-1", "tc-subagent-2", "tc-subagent-3"}
	if len(toolMsg.ToolResults) != len(wantIDs) {
		t.Fatalf("expected %d persisted tool results, got %d", len(wantIDs), len(toolMsg.ToolResults))
	}
	if len(resultEvents) != len(wantIDs) {
		t.Fatalf("expected %d tool result events, got %d: %+v", len(wantIDs), len(resultEvents), resultEvents)
	}
	for i, wantID := range wantIDs {
		persisted := toolMsg.ToolResults[i]
		event := resultEvents[i]
		if persisted.ToolCallID != wantID {
			t.Fatalf("persisted result %d id = %q, want %q", i, persisted.ToolCallID, wantID)
		}
		if event.ToolCallID != wantID {
			t.Fatalf("tool result event %d id = %q, want %q", i, event.ToolCallID, wantID)
		}
		if persisted.IsError != event.IsError || persisted.Content != event.Content {
			t.Fatalf("event result %d = %+v, want persisted %+v", i, event, persisted)
		}
	}
	if !toolMsg.ToolResults[1].IsError {
		t.Fatalf("expected middle subagent result to be marked error: %+v", toolMsg.ToolResults[1])
	}
	if !strings.Contains(toolMsg.ToolResults[1].Content, `"code":"spawn_subagent_failed"`) {
		t.Fatalf("expected existing spawn_subagent_failed envelope, got %q", toolMsg.ToolResults[1].Content)
	}
	for _, idx := range []int{0, 2} {
		if toolMsg.ToolResults[idx].IsError {
			t.Fatalf("expected successful subagent result at index %d, got error %+v", idx, toolMsg.ToolResults[idx])
		}
		if !strings.Contains(toolMsg.ToolResults[idx].Content, `"success":true`) {
			t.Fatalf("expected successful envelope at index %d, got %q", idx, toolMsg.ToolResults[idx].Content)
		}
	}
}

func TestParallelSpawnSubagentProgressKeepsToolCallIDsAndCompletionCounts(t *testing.T) {
	store := NewInMemoryStore()
	spawn := &mockSpawnSubagentTool{progressByID: map[string][]mockSpawnProgress{
		"tc-subagent-1": {
			{Delay: 25 * time.Millisecond, Status: "running", Summary: "first progress:tc-subagent-1", Role: "explore", Model: "mock-progress-model", Count: 1},
			{Delay: 60 * time.Millisecond, Status: "done", Summary: "second progress:tc-subagent-1", Role: "review", Model: "mock-progress-model", Count: 2},
		},
		"tc-subagent-2": {
			{Delay: 10 * time.Millisecond, Status: "running", Summary: "first progress:tc-subagent-2", Role: "explore", Model: "mock-progress-model", Count: 1},
			{Delay: 40 * time.Millisecond, Status: "done", Summary: "second progress:tc-subagent-2", Role: "review", Model: "mock-progress-model", Count: 2},
		},
		"tc-subagent-3": {
			{Delay: 30 * time.Millisecond, Status: "running", Summary: "first progress:tc-subagent-3", Role: "explore", Model: "mock-progress-model", Count: 1},
			{Delay: 20 * time.Millisecond, Status: "done", Summary: "second progress:tc-subagent-3", Role: "review", Model: "mock-progress-model", Count: 2},
		},
	}}
	a := NewAgentWithRegistry(
		&parallelSpawnProvider{},
		store,
		NewToolRegistry([]Tool{spawn}),
	)

	events, err := a.RunStream(context.Background(), "s-parallel-subagents-progress", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}

	type progressRecord struct {
		toolCallID string
		toolName   string
		role       string
		model      string
		count      int
		summary    string
		status     string
	}
	var progressEvents []progressRecord
	subagentDoneCounts := map[string]int{}
	toolResultCounts := map[string]int{}
	for ev := range events {
		if ev.Type == AgentEventTypeError && ev.Err != nil {
			t.Fatalf("run stream emitted error: %v", ev.Err)
		}
		if ev.Type == AgentEventTypeTaskProgress && ev.Task != nil {
			progressEvents = append(progressEvents, progressRecord{
				toolCallID: ev.Task.ToolCallID,
				toolName:   ev.Task.ToolName,
				role:       ev.Task.Role,
				model:      ev.Task.Model,
				count:      ev.Task.Count,
				summary:    ev.Task.Summary,
				status:     ev.Task.Status,
			})
		}
		if ev.Type == AgentEventTypeSubagentDone && ev.Task != nil {
			subagentDoneCounts[ev.Task.ToolCallID]++
			if ev.Task.Status != "completed" {
				t.Fatalf("expected completed subagent status, got %+v", ev.Task)
			}
		}
		if ev.Type == AgentEventTypeToolResult && ev.Result != nil && ev.Result.Name == "spawn_subagent" {
			toolResultCounts[ev.Result.ToolCallID]++
		}
	}

	if got := spawn.calls.Load(); got != 3 {
		t.Fatalf("expected 3 spawn_subagent calls, got %d", got)
	}
	if got := spawn.max.Load(); got < 2 {
		t.Fatalf("expected overlapping spawn_subagent calls, max concurrency was %d", got)
	}

	wantProgress := map[string][]progressRecord{
		"tc-subagent-1": {
			{toolCallID: "tc-subagent-1", toolName: "spawn_subagent", role: "explore", model: "mock-progress-model", count: 1, summary: "first progress:tc-subagent-1", status: "running"},
			{toolCallID: "tc-subagent-1", toolName: "spawn_subagent", role: "review", model: "mock-progress-model", count: 2, summary: "second progress:tc-subagent-1", status: "done"},
		},
		"tc-subagent-2": {
			{toolCallID: "tc-subagent-2", toolName: "spawn_subagent", role: "explore", model: "mock-progress-model", count: 1, summary: "first progress:tc-subagent-2", status: "running"},
			{toolCallID: "tc-subagent-2", toolName: "spawn_subagent", role: "review", model: "mock-progress-model", count: 2, summary: "second progress:tc-subagent-2", status: "done"},
		},
		"tc-subagent-3": {
			{toolCallID: "tc-subagent-3", toolName: "spawn_subagent", role: "explore", model: "mock-progress-model", count: 1, summary: "first progress:tc-subagent-3", status: "running"},
			{toolCallID: "tc-subagent-3", toolName: "spawn_subagent", role: "review", model: "mock-progress-model", count: 2, summary: "second progress:tc-subagent-3", status: "done"},
		},
	}
	gotProgressByCall := map[string][]progressRecord{}
	for _, rec := range progressEvents {
		gotProgressByCall[rec.toolCallID] = append(gotProgressByCall[rec.toolCallID], rec)
	}
	if len(progressEvents) != 6 {
		t.Fatalf("expected 6 progress events, got %d: %+v", len(progressEvents), progressEvents)
	}
	for callID, want := range wantProgress {
		got := gotProgressByCall[callID]
		if len(got) != len(want) {
			t.Fatalf("expected %d progress events for %s, got %d: %+v", len(want), callID, len(got), got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("progress event %d for %s = %+v, want %+v", i, callID, got[i], want[i])
			}
		}
	}
	for _, callID := range []string{"tc-subagent-1", "tc-subagent-2", "tc-subagent-3"} {
		if subagentDoneCounts[callID] != 1 {
			t.Fatalf("expected exactly one subagent completed event for %s, got %d", callID, subagentDoneCounts[callID])
		}
		if toolResultCounts[callID] != 1 {
			t.Fatalf("expected exactly one tool result event for %s, got %d", callID, toolResultCounts[callID])
		}
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
