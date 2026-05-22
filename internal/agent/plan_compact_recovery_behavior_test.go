package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/session"
)

type planModeHistoryProvider struct {
	firstHistory []Message
}

func (p *planModeHistoryProvider) StreamResponse(_ context.Context, history []Message, _ []Tool) <-chan ProviderEvent {
	if p.firstHistory == nil {
		p.firstHistory = append([]Message(nil), history...)
	}
	out := make(chan ProviderEvent, 1)
	out <- ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonEndTurn, Content: "ok"}}
	close(out)
	return out
}

type autoCompactProvider struct {
	histories [][]Message
}

func (p *autoCompactProvider) StreamResponse(_ context.Context, history []Message, tools []Tool) <-chan ProviderEvent {
	p.histories = append(p.histories, append([]Message(nil), history...))
	out := make(chan ProviderEvent, 1)
	content := "ok"
	if len(tools) == 0 && len(history) > 0 && strings.Contains(history[len(history)-1].Text, "Summarize the conversation") {
		content = "compact summary"
	}
	out <- ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonEndTurn, Content: content}}
	close(out)
	return out
}

type stepAdvanceProvider struct {
	calls int
}

func (p *stepAdvanceProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 1)
	p.calls++
	if p.calls == 1 {
		out <- ProviderEvent{
			Type: EventComplete,
			Response: &ProviderResponse{
				FinishReason: FinishReasonToolUse,
				ToolCalls: []ToolCall{
					{ID: "tc-read-1", Name: "read_file", Input: `{"file_path":"x.txt"}`},
				},
			},
		}
	} else {
		out <- ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonEndTurn, Content: "done"}}
	}
	close(out)
	return out
}

type stepBlockProvider struct {
	calls int
}

func (p *stepBlockProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 1)
	p.calls++
	if p.calls == 1 {
		out <- ProviderEvent{
			Type: EventComplete,
			Response: &ProviderResponse{
				FinishReason: FinishReasonToolUse,
				ToolCalls: []ToolCall{
					{ID: "tc-bad-1", Name: "bad", Input: `{}`},
				},
			},
		}
	} else {
		out <- ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonEndTurn, Content: "done"}}
	}
	close(out)
	return out
}

type readOnlyViewTool struct{}

func (r readOnlyViewTool) Name() string   { return "read_file" }
func (r readOnlyViewTool) ReadOnly() bool { return true }
func (r readOnlyViewTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "ok"}, nil
}

func TestPlanModeInjectsSystemPrompt(t *testing.T) {
	store := NewInMemoryStore()
	prov := &planModeHistoryProvider{}
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry(nil),
		WithSessionMode(session.ModePlan),
	)
	if _, err := a.Run(context.Background(), "s-plan-sys", "hello"); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(prov.firstHistory) == 0 {
		t.Fatal("expected provider history")
	}
	if prov.firstHistory[0].Role != RoleSystem {
		t.Fatalf("expected first role system, got %s", prov.firstHistory[0].Role)
	}
	if !strings.Contains(prov.firstHistory[0].Text, "PLAN mode") {
		t.Fatalf("unexpected system prompt: %s", prov.firstHistory[0].Text)
	}
}

type finalPlanOnlyProvider struct{}

func (p *finalPlanOnlyProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 2)
	out <- ProviderEvent{Type: EventContentDelta, Content: "drafting..."}
	out <- ProviderEvent{
		Type: EventComplete,
		Response: &ProviderResponse{
			FinishReason: FinishReasonEndTurn,
			Content:      "drafting...\n<proposed_plan>\n# Plan\n- Implement final-content fallback\n</proposed_plan>",
		},
	}
	close(out)
	return out
}

func TestPlanModeEmitsPlanCompletedFromFinalContentFallback(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgentWithRegistry(
		&finalPlanOnlyProvider{},
		store,
		NewToolRegistry(nil),
		WithSessionMode(session.ModePlan),
	)
	events, err := a.RunStream(context.Background(), "s-final-plan", "plan")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var completed string
	for ev := range events {
		if ev.Type == AgentEventTypePlanCompleted {
			completed = ev.Content
		}
	}
	if !strings.Contains(completed, "Implement final-content fallback") {
		t.Fatalf("expected final proposed plan, got %q", completed)
	}
}

func TestAutoCompactEmitsEvent(t *testing.T) {
	store := NewInMemoryStore()
	for i := 0; i < 8; i++ {
		_, _ = store.Create(context.Background(), Message{SessionID: "s-auto", Role: RoleUser, Text: strings.Repeat("line ", 300)})
	}
	prov := &autoCompactProvider{}
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry(nil),
		WithAutoCompact(true, 0.01, 1000),
	)
	events, err := a.RunStream(context.Background(), "s-auto", "next")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var saw bool
	for ev := range events {
		if ev.Type == AgentEventTypeContextCompacted && ev.Compact != nil && ev.Compact.Auto {
			saw = true
		}
	}
	if !saw {
		t.Fatal("expected auto compact event")
	}
	msgs, err := store.List(context.Background(), "s-auto")
	if err != nil {
		t.Fatalf("list messages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected compact summary plus current turn, got %+v", msgs)
	}
	if msgs[0].Role != RoleUser || msgs[0].Text != "compact summary" || msgs[0].FinishReason != FinishReasonEndTurn {
		t.Fatalf("expected first message to be compact summary, got %+v", msgs[0])
	}
	if msgs[1].Role != RoleAssistant || msgs[1].Text != "ok" {
		t.Fatalf("expected current assistant response after compact summary, got %+v", msgs[1])
	}
	if len(prov.histories) < 2 {
		t.Fatalf("expected summary call and normal call, got %d calls", len(prov.histories))
	}
}

func TestCompactSessionRewritesToSummaryOnly(t *testing.T) {
	store := NewInMemoryStore()
	_, _ = store.Create(context.Background(), Message{SessionID: "s-compact", Role: RoleUser, Text: "keep this"})
	_, _ = store.Create(context.Background(), Message{SessionID: "s-compact", Role: RoleAssistant, Text: "old answer"})
	prov := &autoCompactProvider{}
	a := NewAgentWithRegistry(prov, store, NewToolRegistry(nil))
	info, err := a.CompactSession(context.Background(), "s-compact")
	if err != nil {
		t.Fatalf("compact failed: %v", err)
	}
	if !info.Compacted {
		t.Fatal("expected compact to rewrite session")
	}
	if info.MessagesBefore != 2 || info.MessagesAfter != 1 {
		t.Fatalf("unexpected compact counts: %+v", info)
	}
	msgs, err := store.List(context.Background(), "s-compact")
	if err != nil {
		t.Fatalf("list messages failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected summary-only session, got %+v", msgs)
	}
	if msgs[0].Role != RoleUser || msgs[0].Text != "compact summary" || msgs[0].FinishReason != FinishReasonEndTurn {
		t.Fatalf("unexpected compact summary message: %+v", msgs[0])
	}
}

func TestPlanModeAllowsReadOnlyToolsWithoutChecklistPlan(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgentWithRegistry(
		&stepAdvanceProvider{},
		store,
		NewToolRegistry([]Tool{readOnlyViewTool{}}),
		WithSessionMode(session.ModePlan),
		WithSessionsDir(t.TempDir()),
	)
	events, err := a.RunStream(context.Background(), "s-plan-required", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawReadResult bool
	var sawRequired bool
	for ev := range events {
		if ev.Type == AgentEventTypeToolResult && ev.Result != nil && ev.Result.Name == "read_file" && strings.Contains(ev.Result.Content, "ok") {
			sawReadResult = true
		}
		if ev.Type == AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.Content, "plan_required") {
			sawRequired = true
		}
	}
	if !sawReadResult {
		t.Fatal("expected read-only tool result")
	}
	if sawRequired {
		t.Fatal("did not expect plan_required tool result")
	}
}

func TestRecoveryRetriesTimeoutLikeAndSucceeds(t *testing.T) {
	store := NewInMemoryStore()
	tool := &flakyTool{}
	prov := &oneToolProvider{tool: "flaky", input: `{}`}
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry([]Tool{tool}),
		WithToolPolicy(RulePolicy{Default: PermissionAllow}),
		WithRecoveryPolicy(RecoveryPolicy{
			Enabled: true,
			Rules: map[FailureClass]RecoveryRule{
				FailureClassExecFailed: {Action: RecoveryActionRetrySame, MaxAttempts: 1},
			},
		}),
	)
	events, err := a.RunStream(context.Background(), "s-retry-ok", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawScheduled bool
	var sawAttempt bool
	var finalErr bool
	for ev := range events {
		if ev.Type == AgentEventTypeToolRecoveryScheduled {
			sawScheduled = true
		}
		if ev.Type == AgentEventTypeToolRecoveryAttempt {
			sawAttempt = true
		}
		if ev.Type == AgentEventTypeToolResult && ev.Result != nil {
			finalErr = ev.Result.IsError
		}
	}
	if !sawScheduled || !sawAttempt {
		t.Fatalf("expected recovery schedule+attempt events, got scheduled=%v attempt=%v", sawScheduled, sawAttempt)
	}
	if finalErr {
		t.Fatal("expected final tool result success after retry")
	}
}

type alwaysFailTool struct{}

func (a alwaysFailTool) Name() string { return "always_fail" }
func (a alwaysFailTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Content:    `{"success":false,"error":"policy denied tool call","code":"policy_denied"}`,
		IsError:    true,
	}, nil
}

func TestRecoveryHardBlockExhaustedNoRetry(t *testing.T) {
	store := NewInMemoryStore()
	prov := &oneToolProvider{tool: "always_fail", input: `{}`}
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry([]Tool{alwaysFailTool{}}),
		WithToolPolicy(RulePolicy{Default: PermissionAllow}),
		WithRecoveryPolicy(DefaultRecoveryPolicy()),
	)
	events, err := a.RunStream(context.Background(), "s-retry-no", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var exhausted bool
	var attempts int
	for ev := range events {
		if ev.Type == AgentEventTypeToolRecoveryAttempt {
			attempts++
		}
		if ev.Type == AgentEventTypeToolRecoveryExhausted && ev.Recovery != nil && ev.Recovery.FailureClass == string(FailureClassPolicyDenied) {
			exhausted = true
		}
	}
	if !exhausted {
		t.Fatal("expected recovery exhausted for policy_denied")
	}
	if attempts != 0 {
		t.Fatalf("expected no retry attempts for hard block, got %d", attempts)
	}
}

type failWriteTool struct{}

func (f failWriteTool) Name() string { return "write" }
func (f failWriteTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Content:    `{"success":false,"error":"command failed","code":"exec_failed"}`,
		IsError:    true,
	}, nil
}

func TestRecoveryFallbackReadonlyExecutesTool(t *testing.T) {
	store := NewInMemoryStore()
	prov := &oneToolProvider{tool: "write", input: `{"file_path":"a.txt","content":"x"}`}
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry([]Tool{failWriteTool{}, readOnlyViewTool{}}),
		WithToolPolicy(RulePolicy{Default: PermissionAllow}),
		WithRecoveryPolicy(RecoveryPolicy{
			Enabled: true,
			Rules: map[FailureClass]RecoveryRule{
				FailureClassExecFailed: {Action: RecoveryActionFallbackReadOnly, MaxAttempts: 0},
			},
		}),
	)
	events, err := a.RunStream(context.Background(), "s-fallback", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawExecuted bool
	var sawRecoveredResult bool
	for ev := range events {
		if ev.Type == AgentEventTypeToolRecoveryExhausted && ev.Recovery != nil && ev.Recovery.Executed {
			sawExecuted = true
		}
		if ev.Type == AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.Content, "recovered_with_fallback") {
			sawRecoveredResult = true
		}
	}
	if !sawExecuted || !sawRecoveredResult {
		t.Fatalf("expected fallback executed and recovered result, executed=%v recovered=%v", sawExecuted, sawRecoveredResult)
	}
}

type failExecDefaultTool struct{}

func (f failExecDefaultTool) Name() string { return "exec_default_fail" }
func (f failExecDefaultTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Content:    `{"success":false,"error":"command failed","code":"exec_failed"}`,
		IsError:    true,
	}, nil
}

type unknownDefaultTool struct{}

func (u unknownDefaultTool) Name() string { return "unknown_default_fail" }
func (u unknownDefaultTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Content:    `{"success":false,"error":"opaque failure","code":"opaque_failure"}`,
		IsError:    true,
	}, nil
}

type mcpDeniedDefaultTool struct{}

func (m mcpDeniedDefaultTool) Name() string { return "mcp__fs__search_files" }
func (m mcpDeniedDefaultTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Content:    `{"success":false,"error":"Error: Access denied - path outside allowed directories: /workspace not in /tmp","code":"mcp_tool_error"}`,
		IsError:    true,
	}, nil
}

func TestDefaultRecoveryPassesThroughCommonToolFailures(t *testing.T) {
	tests := []struct {
		name string
		tool string
		reg  Tool
		code string
	}{
		{name: "exec failed", tool: "exec_default_fail", reg: failExecDefaultTool{}, code: `"code":"exec_failed"`},
		{name: "unknown", tool: "unknown_default_fail", reg: unknownDefaultTool{}, code: `"code":"opaque_failure"`},
		{name: "mcp access denied", tool: "mcp__fs__search_files", reg: mcpDeniedDefaultTool{}, code: `"code":"mcp_tool_error"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewInMemoryStore()
			prov := &oneToolProvider{tool: tt.tool, input: `{}`}
			a := NewAgentWithRegistry(
				prov,
				store,
				NewToolRegistry([]Tool{tt.reg}),
				WithToolPolicy(RulePolicy{Default: PermissionAllow}),
				WithRecoveryPolicy(DefaultRecoveryPolicy()),
			)
			sessionID := "s-pass-through-" + strings.ReplaceAll(tt.name, " ", "-")
			events, err := a.RunStream(context.Background(), sessionID, "go")
			if err != nil {
				t.Fatalf("run stream failed: %v", err)
			}
			var sawOriginal bool
			for ev := range events {
				switch ev.Type {
				case AgentEventTypeToolRecoveryExhausted, AgentEventTypeReplanRequiredSet:
					t.Fatalf("unexpected recovery event for %s: %s", tt.name, ev.Type)
				case AgentEventTypeToolResult:
					if ev.Result != nil {
						if strings.Contains(ev.Result.Content, `"code":"request_replan"`) {
							t.Fatalf("unexpected request_replan: %s", ev.Result.Content)
						}
						if strings.Contains(ev.Result.Content, tt.code) {
							sawOriginal = true
						}
					}
				}
			}
			if !sawOriginal {
				t.Fatalf("expected original code %s", tt.code)
			}
		})
	}
}

func TestRecoveryRequestReplanBuildsStructuredResult(t *testing.T) {
	store := NewInMemoryStore()
	prov := &oneToolProvider{tool: "always_fail", input: `{}`}
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry([]Tool{alwaysFailTool{}}),
		WithToolPolicy(RulePolicy{Default: PermissionAllow}),
		WithRecoveryPolicy(RecoveryPolicy{
			Enabled: true,
			Rules: map[FailureClass]RecoveryRule{
				FailureClassPolicyDenied: {Action: RecoveryActionRequestReplan, MaxAttempts: 0},
			},
		}),
	)
	events, err := a.RunStream(context.Background(), "s-replan", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawReplanEvent bool
	var sawReplanResult bool
	for ev := range events {
		if ev.Type == AgentEventTypeToolRecoveryExhausted && ev.Recovery != nil && ev.Recovery.ReplanInjected {
			sawReplanEvent = true
		}
		if ev.Type == AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.Content, `"code":"request_replan"`) {
			sawReplanResult = true
		}
	}
	if !sawReplanEvent || !sawReplanResult {
		t.Fatalf("expected replan event/result, event=%v result=%v", sawReplanEvent, sawReplanResult)
	}
}

type notFoundTool struct{}

func (n notFoundTool) Name() string { return "read_file" }
func (n notFoundTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Content:    `{"success":false,"error":"missing file","code":"not_found"}`,
		IsError:    true,
	}, nil
}

func TestRecoveryDoesNotWrapExploratoryPathFailures(t *testing.T) {
	store := NewInMemoryStore()
	prov := &oneToolProvider{tool: "read_file", input: `{"file_path":"missing.txt"}`}
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry([]Tool{notFoundTool{}}),
		WithToolPolicy(RulePolicy{Default: PermissionAllow}),
		WithRecoveryPolicy(DefaultRecoveryPolicy()),
	)
	events, err := a.RunStream(context.Background(), "s-not-found", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawNotFound bool
	for ev := range events {
		switch ev.Type {
		case AgentEventTypeToolRecoveryExhausted, AgentEventTypeReplanRequiredSet:
			t.Fatalf("unexpected recovery event for path failure: %s", ev.Type)
		case AgentEventTypeToolResult:
			if ev.Result != nil {
				if strings.Contains(ev.Result.Content, "request_replan") {
					t.Fatalf("unexpected replan result: %s", ev.Result.Content)
				}
				if strings.Contains(ev.Result.Content, `"code":"not_found"`) {
					sawNotFound = true
				}
			}
		}
	}
	if !sawNotFound {
		t.Fatal("expected original not_found tool result")
	}
}
