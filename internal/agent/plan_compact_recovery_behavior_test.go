package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
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
	tools     [][]Tool
}

func (p *autoCompactProvider) StreamResponse(_ context.Context, history []Message, tools []Tool) <-chan ProviderEvent {
	p.histories = append(p.histories, append([]Message(nil), history...))
	p.tools = append(p.tools, append([]Tool(nil), tools...))
	out := make(chan ProviderEvent, 1)
	content := "ok"
	if len(history) > 0 && strings.Contains(history[len(history)-1].Text, "Summarize the conversation") {
		content = "compact summary"
	}
	out <- ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonEndTurn, Content: content}}
	close(out)
	return out
}

type compactUsageProvider struct{}

func (p *compactUsageProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 1)
	out <- ProviderEvent{Type: EventComplete, Response: &ProviderResponse{
		FinishReason: FinishReasonEndTurn,
		Content:      "compact summary",
		Model:        "deepseek-v4-flash",
		Usage:        Usage{PromptTokens: 100, CompletionTokens: 10, PromptCacheHitTokens: 25, PromptCacheMissTokens: 75},
	}}
	close(out)
	return out
}

type compactToolOnlyWhenToolsProvider struct {
	toolsSeen int
}

func (p *compactToolOnlyWhenToolsProvider) StreamResponse(_ context.Context, _ []Message, tools []Tool) <-chan ProviderEvent {
	p.toolsSeen = len(tools)
	out := make(chan ProviderEvent, 1)
	if len(tools) > 0 {
		out <- toolUseEvent(toolCall("call-1", tools[0].Name(), `{}`))
	} else {
		out <- ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonEndTurn, Content: "compact summary"}}
	}
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
	return ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: "ok"}, nil
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
	if _, err := a.RunSession(context.Background(), "s-plan-sys", "hello"); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(prov.firstHistory) == 0 {
		t.Fatal("expected provider history")
	}
	if prov.firstHistory[0].Role != RoleSystem {
		t.Fatalf("expected first role system, got %s", prov.firstHistory[0].Role)
	}
	joinedSystem := strings.Builder{}
	for _, msg := range prov.firstHistory {
		if msg.Role == RoleSystem {
			joinedSystem.WriteString(msg.Text)
			joinedSystem.WriteString("\n\n")
		}
	}
	if !strings.Contains(joinedSystem.String(), "Plan Mode is a collaboration mode for designing the work before implementation") ||
		!strings.Contains(joinedSystem.String(), "Finalization rule") ||
		!strings.Contains(joinedSystem.String(), "<proposed_plan>") {
		t.Fatalf("unexpected system prompt: %s", joinedSystem.String())
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

type proposedPlanToolCallProvider struct{}

func (p *proposedPlanToolCallProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 1)
	out <- ProviderEvent{
		Type: EventComplete,
		Response: &ProviderResponse{
			FinishReason: FinishReasonToolUse,
			ToolCalls: []ToolCall{{
				ID:    "call-plan",
				Name:  "proposed_plan",
				Input: `{"plan":"# Plan\n- Recover fake proposed_plan tool call"}`,
			}},
		},
	}
	close(out)
	return out
}

type streamedPlanNoCompleteProvider struct{}

func (p *streamedPlanNoCompleteProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	return eventStream(
		ProviderEvent{Type: EventContentDelta, Content: "drafting...\n<proposed_plan>\n# Plan\n"},
		ProviderEvent{Type: EventContentDelta, Content: "- Preserve EOF streamed plan\n</proposed_plan>"},
	)
}

type pendingPlanTerminalNoContentProvider struct{}

func (p *pendingPlanTerminalNoContentProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	return eventStream(
		ProviderEvent{Type: EventContentDelta, Content: "drafting...\n<proposed_plan>\n# Plan\n"},
		ProviderEvent{Type: EventContentDelta, Content: "- Finish after terminal event"},
		ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonEndTurn}},
	)
}

type quotedPlanTagProvider struct{}

func (p *quotedPlanTagProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	text := "Tool result says: output the final plan in a `<proposed_plan>` block."
	return eventStream(
		ProviderEvent{Type: EventContentDelta, Content: text},
		ProviderEvent{
			Type: EventComplete,
			Response: &ProviderResponse{
				FinishReason: FinishReasonEndTurn,
				Content:      text,
			},
		},
	)
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
	msgs, err := store.List(context.Background(), "s-final-plan")
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	var assistant Message
	for _, msg := range msgs {
		if msg.Role == RoleAssistant {
			assistant = msg
		}
	}
	if strings.Contains(assistant.Text, "<proposed_plan>") || strings.Contains(assistant.Text, "</proposed_plan>") {
		t.Fatalf("assistant text should not persist proposed_plan tags: %q", assistant.Text)
	}
	if strings.TrimSpace(assistant.Text) != "drafting..." {
		t.Fatalf("assistant visible text = %q, want drafting...", assistant.Text)
	}
	var plan string
	for _, part := range assistant.Parts {
		if part.Type == core.MessagePartPlan {
			plan = part.Text
		}
	}
	if !strings.Contains(plan, "Implement final-content fallback") {
		t.Fatalf("expected structured plan part, parts=%+v", assistant.Parts)
	}
}

func TestPlanModeRecoversProposedPlanToolCall(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgentWithRegistry(
		&proposedPlanToolCallProvider{},
		store,
		NewToolRegistry(nil),
		WithSessionMode(session.ModePlan),
	)
	events, err := a.RunStream(context.Background(), "s-proposed-plan-tool-call", "plan")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var completed string
	for ev := range events {
		if ev.Type == AgentEventTypeToolCall {
			t.Fatalf("proposed_plan fake tool should not be emitted as a tool call: %+v", ev)
		}
		if ev.Type == AgentEventTypeToolResult {
			t.Fatalf("proposed_plan fake tool should not dispatch a tool result: %+v", ev)
		}
		if ev.Type == AgentEventTypePlanCompleted {
			completed = ev.Content
		}
	}
	if !strings.Contains(completed, "Recover fake proposed_plan tool call") {
		t.Fatalf("expected recovered proposed plan, got %q", completed)
	}
	msgs, err := store.List(context.Background(), "s-proposed-plan-tool-call")
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	var assistant Message
	for _, msg := range msgs {
		if msg.Role == RoleAssistant {
			assistant = msg
		}
	}
	if assistant.FinishReason != FinishReasonEndTurn {
		t.Fatalf("assistant finish reason = %q, want end_turn", assistant.FinishReason)
	}
	if len(assistant.ToolCalls) != 0 {
		t.Fatalf("fake proposed_plan tool call should be cleared, got %+v", assistant.ToolCalls)
	}
	var plan string
	for _, part := range assistant.Parts {
		if part.Type == core.MessagePartPlan {
			plan = part.Text
		}
	}
	if !strings.Contains(plan, "Recover fake proposed_plan tool call") {
		t.Fatalf("expected structured recovered plan part, parts=%+v", assistant.Parts)
	}
}

func TestPlanModePersistsCompletedStreamedPlanWithoutEventComplete(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgentWithRegistry(
		&streamedPlanNoCompleteProvider{},
		store,
		NewToolRegistry(nil),
		WithSessionMode(session.ModePlan),
	)
	events, err := a.RunStream(context.Background(), "s-streamed-plan-eof", "plan")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var completed string
	for ev := range events {
		if ev.Type == AgentEventTypePlanCompleted {
			completed = ev.Content
		}
	}
	if !strings.Contains(completed, "Preserve EOF streamed plan") {
		t.Fatalf("expected streamed proposed plan completion, got %q", completed)
	}
	msgs, err := store.List(context.Background(), "s-streamed-plan-eof")
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	var assistant Message
	for _, msg := range msgs {
		if msg.Role == RoleAssistant {
			assistant = msg
		}
	}
	if strings.Contains(assistant.Text, "<proposed_plan>") || strings.Contains(assistant.Text, "</proposed_plan>") {
		t.Fatalf("assistant text should not persist proposed_plan tags: %q", assistant.Text)
	}
	if strings.TrimSpace(assistant.Text) != "drafting..." {
		t.Fatalf("assistant visible text = %q, want drafting...", assistant.Text)
	}
	var plan string
	for _, part := range assistant.Parts {
		if part.Type == core.MessagePartPlan {
			plan = part.Text
		}
	}
	if !strings.Contains(plan, "Preserve EOF streamed plan") {
		t.Fatalf("expected structured plan part on EOF, parts=%+v", assistant.Parts)
	}
	if got := core.MessagePlainText(assistant); !strings.Contains(got, "Preserve EOF streamed plan") {
		t.Fatalf("provider plain text should include EOF plan, got %q", got)
	}
}

func TestPlanModeFinishesPendingPlanAfterTerminalCompleteWithoutContent(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgentWithRegistry(
		&pendingPlanTerminalNoContentProvider{},
		store,
		NewToolRegistry(nil),
		WithSessionMode(session.ModePlan),
	)
	events, err := a.RunStream(context.Background(), "s-pending-plan-terminal", "plan")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var completed string
	for ev := range events {
		if ev.Type == AgentEventTypePlanCompleted {
			completed = ev.Content
		}
	}
	if !strings.Contains(completed, "Finish after terminal event") {
		t.Fatalf("expected pending plan to complete after terminal event, got %q", completed)
	}
	msgs, err := store.List(context.Background(), "s-pending-plan-terminal")
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	var assistant Message
	for _, msg := range msgs {
		if msg.Role == RoleAssistant {
			assistant = msg
		}
	}
	var plan string
	for _, part := range assistant.Parts {
		if part.Type == core.MessagePartPlan {
			plan = part.Text
		}
	}
	if !strings.Contains(plan, "Finish after terminal event") {
		t.Fatalf("expected structured plan after terminal complete, parts=%+v", assistant.Parts)
	}
	if got := core.MessagePlainText(assistant); !strings.Contains(got, "Finish after terminal event") {
		t.Fatalf("provider plain text should include terminal-completed plan, got %q", got)
	}
}

func TestPlanModeQuotedProposedPlanTagDoesNotEmitPlanCompleted(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgentWithRegistry(
		&quotedPlanTagProvider{},
		store,
		NewToolRegistry(nil),
		WithSessionMode(session.ModePlan),
	)
	events, err := a.RunStream(context.Background(), "s-quoted-plan-tag", "plan")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawAssistant bool
	for ev := range events {
		if ev.Type == AgentEventTypePlanCompleted || ev.Type == AgentEventTypePlanDelta {
			t.Fatalf("quoted proposed_plan tag should not emit plan events: %+v", ev)
		}
		if ev.Type == AgentEventTypeAssistantDelta && strings.Contains(ev.Content, "<proposed_plan>") {
			sawAssistant = true
		}
	}
	if !sawAssistant {
		t.Fatal("expected quoted proposed_plan text to remain assistant text")
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
	if len(msgs) != 3 {
		t.Fatalf("expected compact summary plus retained current turn and assistant, got %+v", msgs)
	}
	if msgs[0].Role != RoleUser || msgs[0].Text != "compact summary" || msgs[0].FinishReason != FinishReasonEndTurn {
		t.Fatalf("expected first message to be compact summary, got %+v", msgs[0])
	}
	if msgs[1].Role != RoleUser || msgs[1].Text != "next" {
		t.Fatalf("expected retained current user turn after compact summary, got %+v", msgs[1])
	}
	if msgs[2].Role != RoleAssistant || msgs[2].Text != "ok" {
		t.Fatalf("expected current assistant response after retained tail, got %+v", msgs[2])
	}
	if len(prov.histories) < 2 {
		t.Fatalf("expected summary call and normal call, got %d calls", len(prov.histories))
	}
	if prov.histories[0][0].Role != RoleSystem {
		t.Fatalf("expected compact summary call to include system prefix, got %+v", prov.histories[0])
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

func TestCompactSessionRecordsCacheShapeRequestKind(t *testing.T) {
	store := NewInMemoryStore()
	_, _ = store.Create(context.Background(), Message{SessionID: "s-compact-usage", Role: RoleUser, Text: "keep this"})
	usagePath := filepath.Join(t.TempDir(), "usage")
	a := NewAgentWithRegistry(&compactUsageProvider{}, store, NewToolRegistry([]Tool{readOnlyViewTool{}}), WithUsageLogPath(usagePath))

	if _, err := a.CompactSession(context.Background(), "s-compact-usage"); err != nil {
		t.Fatalf("compact failed: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(usagePath, "s-compact-usage.jsonl"))
	if err != nil {
		t.Fatalf("read usage log: %v", err)
	}
	if !strings.Contains(string(b), `"request_kind":"compact"`) {
		t.Fatalf("missing compact cache shape: %s", string(b))
	}
	if !strings.Contains(string(b), `"prefix_fingerprint"`) || !strings.Contains(string(b), `"system_segments"`) {
		t.Fatalf("missing compact prefix shape: %s", string(b))
	}
	if !strings.Contains(string(b), `"tools_bytes":4`) || strings.Contains(string(b), `"read_file"`) {
		t.Fatalf("compact summary should record only an empty provider tool shape: %s", string(b))
	}
}

func TestCompactSessionUsesPrefixWithoutTools(t *testing.T) {
	store := NewInMemoryStore()
	_, _ = store.Create(context.Background(), Message{SessionID: "s-compact-tools", Role: RoleUser, Text: "keep this"})
	prov := &autoCompactProvider{}
	a := NewAgentWithRegistry(prov, store, NewToolRegistry([]Tool{readOnlyViewTool{}}))

	if _, err := a.CompactSession(context.Background(), "s-compact-tools"); err != nil {
		t.Fatalf("compact failed: %v", err)
	}
	if len(prov.histories) != 1 {
		t.Fatalf("expected one compact provider call, got %d", len(prov.histories))
	}
	if prov.histories[0][0].Role != RoleSystem {
		t.Fatalf("expected compact summary call to include system prefix, got %+v", prov.histories[0])
	}
	if len(prov.tools) != 1 || len(prov.tools[0]) != 0 {
		t.Fatalf("expected compact summary call to omit provider tools, got %+v", prov.tools)
	}
}

func TestCompactSessionDoesNotAdvertiseToolsThatItCannotDispatch(t *testing.T) {
	store := NewInMemoryStore()
	_, _ = store.Create(context.Background(), Message{SessionID: "s-compact-no-tools", Role: RoleUser, Text: "keep this"})
	prov := &compactToolOnlyWhenToolsProvider{}
	a := NewAgentWithRegistry(prov, store, NewToolRegistry([]Tool{readOnlyViewTool{}}))

	if _, err := a.CompactSession(context.Background(), "s-compact-no-tools"); err != nil {
		t.Fatalf("compact failed: %v", err)
	}
	if prov.toolsSeen != 0 {
		t.Fatalf("compact summary request advertised %d tools", prov.toolsSeen)
	}
}

func TestForceSummaryRecordsCacheShapeRequestKind(t *testing.T) {
	store := NewInMemoryStore()
	usagePath := filepath.Join(t.TempDir(), "usage")
	a := NewAgentWithRegistry(&compactUsageProvider{}, store, NewToolRegistry(nil), WithUsageLogPath(usagePath))

	reqCtx, err := a.buildSummaryRequestContext(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("summary context failed: %v", err)
	}
	_, err = a.forceSummary(context.Background(), "s-force-summary", []Message{{SessionID: "s-force-summary", Role: RoleUser, Text: "work"}}, "test", reqCtx)
	if err != nil {
		t.Fatalf("force summary failed: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(usagePath, "s-force-summary.jsonl"))
	if err != nil {
		t.Fatalf("read usage log: %v", err)
	}
	if !strings.Contains(string(b), `"request_kind":"force_summary"`) {
		t.Fatalf("missing force summary cache shape: %s", string(b))
	}
	if !strings.Contains(string(b), `"prefix_fingerprint"`) || !strings.Contains(string(b), `"system_segments"`) {
		t.Fatalf("missing force summary prefix shape: %s", string(b))
	}
}

func TestForceSummaryUsesPrefixWithoutTools(t *testing.T) {
	store := NewInMemoryStore()
	prov := &autoCompactProvider{}
	a := NewAgentWithRegistry(prov, store, NewToolRegistry([]Tool{readOnlyViewTool{}}))
	reqCtx, err := a.buildSummaryRequestContext(context.Background(), RunOptions{})
	if err != nil {
		t.Fatalf("summary context failed: %v", err)
	}

	_, err = a.forceSummary(context.Background(), "s-force-tools", []Message{{SessionID: "s-force-tools", Role: RoleUser, Text: "work"}}, "test", reqCtx)
	if err != nil {
		t.Fatalf("force summary failed: %v", err)
	}
	if len(prov.histories) != 1 {
		t.Fatalf("expected one force summary provider call, got %d", len(prov.histories))
	}
	if prov.histories[0][0].Role != RoleSystem {
		t.Fatalf("expected force summary call to include system prefix, got %+v", prov.histories[0])
	}
	if len(prov.tools) != 1 || len(prov.tools[0]) != 0 {
		t.Fatalf("expected force summary call to omit provider tools, got %+v", prov.tools)
	}
}

func TestPreCompactHookContextIsIncludedInSummaryPrompt(t *testing.T) {
	store := NewInMemoryStore()
	_, _ = store.Create(context.Background(), Message{SessionID: "s-compact-hook", Role: RoleUser, Text: "keep this"})
	prov := &autoCompactProvider{}
	runner := NewHookRunner(nil, ".")
	runner.AddHandlers(HookHandler{
		Event: HookEventPreCompact,
		Name:  "compact context",
		Run: func(context.Context, HookPayload) HookResult {
			return HookResult{Decision: HookDecisionPass, AdditionalContext: "remember hook context"}
		},
	})
	a := NewAgentWithRegistry(prov, store, NewToolRegistry(nil), WithHookRunner(runner))
	if _, err := a.CompactSession(context.Background(), "s-compact-hook"); err != nil {
		t.Fatalf("compact failed: %v", err)
	}
	if len(prov.histories) == 0 {
		t.Fatal("expected summary provider call")
	}
	lastHistory := prov.histories[0]
	if len(lastHistory) == 0 || !strings.Contains(lastHistory[len(lastHistory)-1].Text, "remember hook context") {
		t.Fatalf("expected PreCompact context in summary prompt, got %+v", lastHistory)
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
		if ev.Type == AgentEventTypeToolResult && ev.Result != nil && ev.Result.Name == "read_file" && strings.Contains(ev.Result.ModelText, "ok") {
			sawReadResult = true
		}
		if ev.Type == AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.ModelText, "plan_required") {
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
			finalErr = ev.Result.IsError()
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
		ModelText:  `{"success":false,"error":"policy denied tool call","code":"policy_denied"}`,
		Outcome:    core.OutcomeFailure,
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
		ModelText:  `{"success":false,"error":"command failed","code":"exec_failed"}`,
		Outcome:    core.OutcomeFailure,
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
		if ev.Type == AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.ModelText, "recovered_with_fallback") {
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
		ModelText:  `{"success":false,"error":"command failed","code":"exec_failed"}`,
		Outcome:    core.OutcomeFailure,
	}, nil
}

type unknownDefaultTool struct{}

func (u unknownDefaultTool) Name() string { return "unknown_default_fail" }
func (u unknownDefaultTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		ModelText:  `{"success":false,"error":"opaque failure","code":"opaque_failure"}`,
		Outcome:    core.OutcomeFailure,
	}, nil
}

type searchNotFoundTool struct{}

func (s searchNotFoundTool) Name() string { return "search_not_found_edit" }
func (s searchNotFoundTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		ModelText:  `{"success":false,"error":"search text not found","code":"search_not_found"}`,
		Outcome:    core.OutcomeFailure,
	}, nil
}

type mcpDeniedDefaultTool struct{}

func (m mcpDeniedDefaultTool) Name() string { return "mcp__fs__search_files" }
func (m mcpDeniedDefaultTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		ModelText:  `{"success":false,"error":"Error: Access denied - path outside allowed directories: /workspace not in /tmp","code":"mcp_tool_error"}`,
		Outcome:    core.OutcomeFailure,
	}, nil
}

func TestDefaultRecoveryPassesThroughCommonToolFailures(t *testing.T) {
	tests := []struct {
		name string
		tool string
		reg  Tool
		code string
	}{
		{name: "exec failed", tool: "exec_default_fail", reg: failExecDefaultTool{}, code: `error (exec_failed)`},
		{name: "unknown", tool: "unknown_default_fail", reg: unknownDefaultTool{}, code: `error (opaque_failure)`},
		{name: "search not found", tool: "search_not_found_edit", reg: searchNotFoundTool{}, code: `error (search_not_found)`},
		{name: "mcp access denied", tool: "mcp__fs__search_files", reg: mcpDeniedDefaultTool{}, code: `error (mcp_tool_error)`},
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
						if strings.Contains(ev.Result.ModelText, `error (request_replan)`) {
							t.Fatalf("unexpected request_replan: %s", ev.Result.ModelText)
						}
						if strings.Contains(ev.Result.ModelText, tt.code) {
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
		if ev.Type == AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.ModelText, `error (request_replan)`) {
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
		ModelText:  `{"success":false,"error":"missing file","code":"not_found"}`,
		Outcome:    core.OutcomeFailure,
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
				if strings.Contains(ev.Result.ModelText, "request_replan") {
					t.Fatalf("unexpected replan result: %s", ev.Result.ModelText)
				}
				if strings.Contains(ev.Result.ModelText, `error (not_found)`) {
					sawNotFound = true
				}
			}
		}
	}
	if !sawNotFound {
		t.Fatal("expected original not_found tool result")
	}
}
