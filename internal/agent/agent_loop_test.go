package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
	llmretry "github.com/usewhale/whale/internal/llm/retry"
)

type mockProvider struct {
	calls int
}

func (m *mockProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	m.calls++
	if m.calls == 1 {
		return eventStream(toolUseEvent(toolCall("tc-1", "echo", "hi")))
	}
	return eventStream(endTurnEvent("done"))
}

type tooManyToolsProvider struct{}

func (p *tooManyToolsProvider) StreamResponse(_ context.Context, _ []Message, tools []Tool) <-chan ProviderEvent {
	if len(tools) == 0 {
		return eventStream(endTurnEvent("forced summary"))
	}
	return eventStream(toolUseEvent(
		toolCall("tc-1", "echo", `{"n":1}`),
		toolCall("tc-2", "echo", `{"n":2}`),
		toolCall("tc-3", "echo", `{"n":3}`),
	))
}

type captureToolsProvider struct {
	toolNames []string
}

func (p *captureToolsProvider) StreamResponse(_ context.Context, _ []Message, tools []Tool) <-chan ProviderEvent {
	p.toolNames = p.toolNames[:0]
	for _, tool := range tools {
		p.toolNames = append(p.toolNames, tool.Name())
	}
	return eventStream(endTurnEvent("done"))
}

func TestRunStreamWithContentOptionsPersistsMessageParts(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgent(&mockProvider{}, store, []Tool{echoTool{}})

	events, err := a.RunStreamWithContentOptions(context.Background(), "s-content", []core.MessagePart{
		{Type: core.MessagePartText, Text: "describe"},
		{Type: core.MessagePartAttachment, Attachment: &core.AttachmentRef{
			Kind:        core.AttachmentKindImage,
			DisplayName: "screen.png",
		}},
	}, RunOptions{HiddenInput: true})
	if err != nil {
		t.Fatalf("RunStreamWithContentOptions: %v", err)
	}
	for ev := range events {
		if ev.Type == AgentEventTypeError {
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}

	msgs, err := store.List(context.Background(), "s-content")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("expected persisted messages")
	}
	if !msgs[0].Hidden {
		t.Fatal("expected hidden input to be preserved")
	}
	if len(msgs[0].Parts) != 2 || msgs[0].Parts[1].Attachment == nil {
		t.Fatalf("unexpected parts: %#v", msgs[0].Parts)
	}
}

func TestWorkflowAuthoringTurnOnlyExposesWorkflowTool(t *testing.T) {
	store := NewInMemoryStore()
	provider := &captureToolsProvider{}
	a := NewAgent(provider, store, []Tool{
		namedTestTool{name: "workflow"},
		namedTestTool{name: "list_dir"},
		namedTestTool{name: "shell_run"},
	})

	events, err := a.RunStreamWithTurnOptions(context.Background(), "s-authoring-tools", "create workflow", RunOptions{WorkflowAuthoring: true})
	if err != nil {
		t.Fatalf("RunStreamWithTurnOptions: %v", err)
	}
	for ev := range events {
		if ev.Type == AgentEventTypeError {
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	if got, want := strings.Join(provider.toolNames, ","), "workflow"; got != want {
		t.Fatalf("workflow authoring turn should expose only workflow tool, got %q", got)
	}
}

func TestNonWorkflowAuthoringTurnExposesFullToolSnapshot(t *testing.T) {
	store := NewInMemoryStore()
	provider := &captureToolsProvider{}
	a := NewAgent(provider, store, []Tool{
		namedTestTool{name: "workflow"},
		namedTestTool{name: "list_dir"},
		namedTestTool{name: "shell_run"},
	})

	events, err := a.RunStreamWithTurnOptions(context.Background(), "s-normal-tools", "list files", RunOptions{})
	if err != nil {
		t.Fatalf("RunStreamWithTurnOptions: %v", err)
	}
	for ev := range events {
		if ev.Type == AgentEventTypeError {
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	if got, want := strings.Join(provider.toolNames, ","), "workflow,list_dir,shell_run"; got != want {
		t.Fatalf("normal turn should expose full tool snapshot, got %q", got)
	}
}

func TestAgentMaxToolCallsDropsExcessAndForcesSummary(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgent(&tooManyToolsProvider{}, store, []Tool{echoTool{}})
	WithMaxToolCalls(2)(a)

	events, err := a.RunStream(context.Background(), "s-tool-cap", "go")
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	var blocked int
	var forced bool
	for ev := range events {
		switch ev.Type {
		case AgentEventTypeToolCallBlocked:
			if ev.ToolBlocked != nil && ev.ToolBlocked.ReasonCode == "tool_call_cap_reached" {
				blocked++
			}
		case AgentEventTypeForcedSummaryStarted:
			if ev.Content == "tool call cap reached" {
				forced = true
			}
		case AgentEventTypeError:
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	if blocked != 1 {
		t.Fatalf("blocked tool calls = %d, want 1", blocked)
	}
	if !forced {
		t.Fatal("expected forced summary when tool call cap was reached")
	}
	all, err := store.List(context.Background(), "s-tool-cap")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var executed int
	var capped int
	for _, msg := range all {
		for _, res := range msg.ToolResults {
			if res.Name == "echo" && !res.IsError() {
				executed++
			}
			if res.Name == "echo" && res.IsError() && res.ToolCallID == "tc-3" {
				capped++
			}
		}
	}
	if executed != 2 || capped != 1 {
		t.Fatalf("executed/capped = %d/%d, want 2/1", executed, capped)
	}
}

func TestAgentMaxTurnsForcesSummaryAfterToolRound(t *testing.T) {
	store := NewInMemoryStore()
	prov := &mockProvider{}
	a := NewAgent(prov, store, []Tool{echoTool{}})
	WithMaxTurns(1)(a)

	events, err := a.RunStream(context.Background(), "s-turn-cap", "go")
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	var forced bool
	for ev := range events {
		switch ev.Type {
		case AgentEventTypeForcedSummaryStarted:
			if ev.Content == "turn cap reached" {
				forced = true
			}
		case AgentEventTypeError:
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	if !forced {
		t.Fatal("expected forced summary when max turns reached after tool round")
	}
	if prov.calls != 2 {
		t.Fatalf("provider calls = %d, want 2 including forced summary", prov.calls)
	}
}

type stormingProvider struct {
	calls int
}

func (p *stormingProvider) StreamResponse(_ context.Context, _ []Message, tools []Tool) <-chan ProviderEvent {
	p.calls++
	if len(tools) == 0 {
		// Forced-summary calls suppress tools; answer them so the turn can end.
		return eventStream(endTurnEvent("forced summary"))
	}
	// Always the same call: it trips the storm breaker and never progresses.
	return eventStream(toolUseEvent(toolCall("tc-loop", "echo", `{"n":1}`)))
}

func TestAgentStormLoopForcesSummaryWithoutToolIterCap(t *testing.T) {
	store := NewInMemoryStore()
	prov := &stormingProvider{}
	a := NewAgent(prov, store, []Tool{echoTool{}})
	// The main agent runs without a tool-iter cap; the storm loop-guard is what
	// must stop this otherwise-infinite loop.
	if a.maxToolIters != 0 {
		t.Fatalf("expected uncapped main agent, got maxToolIters=%d", a.maxToolIters)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	events, err := a.RunStream(ctx, "s-storm", "go")
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	var forced bool
	var done *core.Message
	for ev := range events {
		switch ev.Type {
		case AgentEventTypeForcedSummaryStarted:
			if ev.Content == "repetitive tool-call loop detected" {
				forced = true
			}
		case AgentEventTypeDone:
			done = ev.Message
		case AgentEventTypeError:
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	if !forced {
		t.Fatal("expected forced summary from the storm loop-guard")
	}
	if done == nil || !strings.Contains(done.Text, "auto-interrupted") {
		t.Fatalf("expected truncation banner in final message, got %+v", done)
	}
	// Bounded: a few storm rounds plus the summary, nowhere near a runaway.
	if prov.calls > 20 {
		t.Fatalf("provider calls = %d, want a small bounded number", prov.calls)
	}
}

func TestAgentLoopWithToolRoundTrip(t *testing.T) {
	store := NewInMemoryStore()
	prov := &mockProvider{}
	a := NewAgent(prov, store, []Tool{echoTool{}})

	msg, err := a.RunSession(context.Background(), "s1", "hello")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if msg.FinishReason != FinishReasonEndTurn {
		t.Fatalf("unexpected finish: %s", msg.FinishReason)
	}

	all, _ := store.List(context.Background(), "s1")
	if len(all) != 4 {
		t.Fatalf("expected 4 messages (user,assistant,tool,assistant), got %d", len(all))
	}
}

type steeredInputProvider struct {
	firstStarted chan struct{}
	releaseFirst chan struct{}
	mu           sync.Mutex
	calls        int
	inputs       [][]Message
}

func newSteeredInputProvider() *steeredInputProvider {
	return &steeredInputProvider{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
}

func (p *steeredInputProvider) StreamResponse(_ context.Context, input []Message, _ []Tool) <-chan ProviderEvent {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.inputs = append(p.inputs, append([]Message(nil), input...))
	p.mu.Unlock()
	out := make(chan ProviderEvent, 1)
	go func() {
		if call == 1 {
			close(p.firstStarted)
			<-p.releaseFirst
			out <- endTurnEvent("first answer")
		} else {
			out <- endTurnEvent("steered answer")
		}
		close(out)
	}()
	return out
}

func (p *steeredInputProvider) snapshot() (int, [][]Message) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, append([][]Message(nil), p.inputs...)
}

func TestActiveTurnInjectedInputContinuesCurrentTurn(t *testing.T) {
	store := NewInMemoryStore()
	prov := newSteeredInputProvider()
	a := NewAgent(prov, store, nil)

	events, err := a.RunStream(context.Background(), "s-steer", "start")
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	<-prov.firstStarted
	injected, err := a.InjectTurnInput(context.Background(), "s-steer", []Message{{
		Role: RoleUser,
		Text: "change direction",
	}})
	if err != nil {
		t.Fatalf("InjectTurnInput: %v", err)
	}
	if !injected {
		t.Fatal("expected input to inject into active turn")
	}
	close(prov.releaseFirst)
	var sawResponseReset bool
	for ev := range events {
		if ev.Type == AgentEventTypeResponseReset {
			sawResponseReset = true
		}
		if ev.Type == AgentEventTypeError {
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	if !sawResponseReset {
		t.Fatal("expected response reset before continuing with injected input")
	}

	calls, inputs := prov.snapshot()
	if calls != 2 {
		t.Fatalf("provider calls = %d, want 2", calls)
	}
	if len(inputs) != 2 {
		t.Fatalf("captured inputs = %d, want 2", len(inputs))
	}
	var sawSteer bool
	for _, msg := range inputs[1] {
		if msg.Role == RoleUser && msg.Text == "change direction" {
			sawSteer = true
		}
	}
	if !sawSteer {
		t.Fatalf("second request did not include injected input: %+v", inputs[1])
	}
	all, err := store.List(context.Background(), "s-steer")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var sawPersisted bool
	for _, msg := range all {
		if msg.Role == RoleUser && msg.Text == "change direction" {
			sawPersisted = true
		}
	}
	if !sawPersisted {
		t.Fatalf("injected input was not persisted: %+v", all)
	}
}

type abortAfterToolResultTool struct{}

func (t abortAfterToolResultTool) Name() string { return "confirm_later" }
func (t abortAfterToolResultTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		ModelText:  `{"success":true,"code":"confirmation_required"}`,
		Metadata: map[string]any{
			"abort_turn_after_tool_result": true,
		},
	}, nil
}

type abortPlusEchoProvider struct{}

func (p *abortPlusEchoProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	return eventStream(toolUseEvent(
		toolCall("tc-confirm", "confirm_later", "{}"),
		toolCall("tc-echo", "echo", "hi"),
	))
}

type abortWithSteerProvider struct {
	firstStarted chan struct{}
	releaseFirst chan struct{}
	mu           sync.Mutex
	calls        int
	inputs       [][]Message
}

func newAbortWithSteerProvider() *abortWithSteerProvider {
	return &abortWithSteerProvider{
		firstStarted: make(chan struct{}),
		releaseFirst: make(chan struct{}),
	}
}

func (p *abortWithSteerProvider) StreamResponse(_ context.Context, input []Message, _ []Tool) <-chan ProviderEvent {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.inputs = append(p.inputs, append([]Message(nil), input...))
	p.mu.Unlock()
	out := make(chan ProviderEvent, 1)
	go func() {
		if call == 1 {
			close(p.firstStarted)
			<-p.releaseFirst
			out <- toolUseEvent(toolCall("tc-confirm", "confirm_later", "{}"))
		} else {
			out <- endTurnEvent("handled steering")
		}
		close(out)
	}()
	return out
}

func (p *abortWithSteerProvider) snapshot() (int, [][]Message) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, append([][]Message(nil), p.inputs...)
}

func TestAgentLoopAbortsAfterToolResultWhenToolRequestsRuntimeHandoff(t *testing.T) {
	store := NewInMemoryStore()
	prov := &oneToolProvider{tool: "confirm_later", input: "{}"}
	a := NewAgent(prov, store, []Tool{abortAfterToolResultTool{}})

	msg, err := a.RunSession(context.Background(), "s-abort-after-tool", "run workflow")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if msg.FinishReason != FinishReasonEndTurn {
		t.Fatalf("unexpected finish: %s", msg.FinishReason)
	}
	if prov.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", prov.calls)
	}
	all, _ := store.List(context.Background(), "s-abort-after-tool")
	if len(all) != 3 {
		t.Fatalf("expected 3 messages (user,assistant,tool), got %d", len(all))
	}
	if all[2].Role != RoleTool || len(all[2].ToolResults) != 1 {
		t.Fatalf("expected terminal tool message, got %+v", all[2])
	}
}

func TestAbortTurnToolResultContinuesWhenSteeredInputIsPending(t *testing.T) {
	store := NewInMemoryStore()
	prov := newAbortWithSteerProvider()
	a := NewAgent(prov, store, []Tool{abortAfterToolResultTool{}})

	events, err := a.RunStream(context.Background(), "s-abort-steer", "run workflow")
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	<-prov.firstStarted
	injected, err := a.InjectTurnInput(context.Background(), "s-abort-steer", []Message{{
		Role: RoleUser,
		Text: "do not launch the workflow; publish directly",
	}})
	if err != nil {
		t.Fatalf("InjectTurnInput: %v", err)
	}
	if !injected {
		t.Fatal("expected steering input to inject into active turn")
	}
	close(prov.releaseFirst)
	for ev := range events {
		if ev.Type == AgentEventTypeError {
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	calls, inputs := prov.snapshot()
	if calls != 2 {
		t.Fatalf("provider calls = %d, want 2", calls)
	}
	var sawSteer bool
	for _, msg := range inputs[1] {
		if msg.Role == RoleUser && msg.Text == "do not launch the workflow; publish directly" {
			sawSteer = true
		}
	}
	if !sawSteer {
		t.Fatalf("second request did not include steered input after abort-turn tool result: %+v", inputs[1])
	}
}

func TestAgentLoopAbortAddsResultsForUnprocessedToolCalls(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgent(&abortPlusEchoProvider{}, store, []Tool{abortAfterToolResultTool{}, echoTool{}})

	msg, err := a.RunSession(context.Background(), "s-abort-align", "run workflow and echo")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if msg.FinishReason != FinishReasonEndTurn {
		t.Fatalf("unexpected finish: %s", msg.FinishReason)
	}
	all, err := store.List(context.Background(), "s-abort-align")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 messages (user,assistant,tool), got %d", len(all))
	}
	if all[2].Role != RoleTool || len(all[2].ToolResults) != 2 {
		t.Fatalf("expected aligned terminal tool message, got %+v", all[2])
	}
	if got := all[2].ToolResults[0]; got.ToolCallID != "tc-confirm" || got.IsError() {
		t.Fatalf("first result = %+v", got)
	}
	if got := all[2].ToolResults[1]; got.ToolCallID != "tc-echo" || !got.IsError() || !strings.Contains(got.ModelText, "turn_aborted") {
		t.Fatalf("second result = %+v", got)
	}
}

// deltasNoTerminalProvider emits content deltas then closes the channel
// without an EventComplete/EventError. This models the SSE-EOF-before-[DONE]
// case noted in issue #22 review.
type deltasNoTerminalProvider struct{}

func (p *deltasNoTerminalProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 4)
	out <- ProviderEvent{Type: EventContentDelta, Content: "partial-"}
	out <- ProviderEvent{Type: EventContentDelta, Content: "answer"}
	close(out)
	return out
}

type manyReasoningDeltasProvider struct{}

func (p *manyReasoningDeltasProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 32)
	for i := 0; i < 31; i++ {
		out <- ProviderEvent{Type: EventReasoningDelta, ReasoningDelta: "x"}
	}
	out <- endTurnEvent("done")
	close(out)
	return out
}

func TestRunStreamCancelWithoutDrainingEventsReleasesSession(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgent(&manyReasoningDeltasProvider{}, store, nil)

	ctx, cancel := context.WithCancel(context.Background())
	events, err := a.RunStream(ctx, "s-undrained", "go")
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	_ = events

	time.Sleep(100 * time.Millisecond)
	cancel()

	deadline := time.After(2 * time.Second)
	for {
		if _, loaded := a.active.Load("s-undrained"); !loaded {
			return
		}
		select {
		case <-deadline:
			t.Fatal("expected canceled undrained stream to release active session")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestStreamFallthroughPersistsPartialAssistant(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgent(&deltasNoTerminalProvider{}, store, nil)

	events, err := a.RunStream(context.Background(), "s-partial", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	for range events {
	}

	all, err := store.List(context.Background(), "s-partial")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) < 2 {
		t.Fatalf("expected user+assistant, got %d", len(all))
	}
	asst := all[len(all)-1]
	if asst.Role != RoleAssistant {
		t.Fatalf("expected last to be assistant, got %s", asst.Role)
	}
	if asst.Text != "partial-answer" {
		t.Fatalf("expected persisted partial text %q, got %q", "partial-answer", asst.Text)
	}
}

type toolStartThenErrorProvider struct{}

func (p *toolStartThenErrorProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	return eventStream(
		ProviderEvent{Type: EventContentDelta, Content: "about to run"},
		ProviderEvent{Type: EventToolArgsDelta, ToolArgsDelta: &llm.ToolArgsDelta{ToolCallIndex: 0, ToolName: "shell_run", ArgsDelta: `{"command":`, ArgsChars: len(`{"command":`)}},
		ProviderEvent{Type: EventToolUseStart, ToolCall: &ToolCall{ID: "tc-partial", Name: "shell_run"}},
		ProviderEvent{Type: EventError, Err: errors.New("stream timed out after progress")},
	)
}

func TestStreamErrorDropsIncompleteToolCall(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgent(&toolStartThenErrorProvider{}, store, []Tool{echoTool{}})

	events, err := a.RunStream(context.Background(), "s-incomplete-tool", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	for range events {
	}

	all, err := store.List(context.Background(), "s-incomplete-tool")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected user+assistant, got %d: %+v", len(all), all)
	}
	asst := all[1]
	if asst.Text != "about to run" {
		t.Fatalf("expected partial assistant text to survive, got %q", asst.Text)
	}
	if asst.FinishReason != FinishReasonError {
		t.Fatalf("expected error finish reason, got %q", asst.FinishReason)
	}
	if len(asst.ToolCalls) != 0 {
		t.Fatalf("incomplete tool call persisted: %+v", asst.ToolCalls)
	}
}

type toolOnlyThenErrorProvider struct{}

func (p *toolOnlyThenErrorProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	return eventStream(
		ProviderEvent{Type: EventToolArgsDelta, ToolArgsDelta: &llm.ToolArgsDelta{ToolCallIndex: 0, ToolName: "shell_run", ArgsDelta: `{"command":`, ArgsChars: len(`{"command":`)}},
		ProviderEvent{Type: EventToolUseStart, ToolCall: &ToolCall{ID: "tc-empty", Name: "shell_run"}},
		ProviderEvent{Type: EventError, Err: errors.New("stream timed out before final tool input")},
	)
}

func TestStreamErrorWithOnlyIncompleteToolCallDoesNotPersistToolCall(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgent(&toolOnlyThenErrorProvider{}, store, []Tool{echoTool{}})

	events, err := a.RunStream(context.Background(), "s-tool-only-error", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	for range events {
	}

	all, err := store.List(context.Background(), "s-tool-only-error")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected user+assistant, got %d: %+v", len(all), all)
	}
	asst := all[1]
	if asst.FinishReason != FinishReasonError {
		t.Fatalf("expected error finish reason, got %q", asst.FinishReason)
	}
	if len(asst.ToolCalls) != 0 {
		t.Fatalf("incomplete tool call persisted: %+v", asst.ToolCalls)
	}
}

type streamRetryProvider struct{}

func (p *streamRetryProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	return eventStream(
		ProviderEvent{Type: EventReasoningDelta, ReasoningDelta: "old-thought"},
		ProviderEvent{Type: EventContentDelta, Content: "old-answer"},
		ProviderEvent{Type: EventToolUseStart, ToolCall: &ToolCall{ID: "tc-old", Name: "echo"}},
		ProviderEvent{Type: llm.EventRetryScheduled, Retry: &llmretry.Info{Attempt: 1, MaxAttempts: 2, Reason: "API stream disconnected", Stage: "stream", StreamReset: true}},
		ProviderEvent{Type: EventReasoningDelta, ReasoningDelta: "new-thought"},
		ProviderEvent{Type: EventContentDelta, Content: "new-answer"},
		ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonEndTurn, Content: "new-answer", Reasoning: "new-thought"}},
	)
}

func TestStreamRetryResetClearsPartialAssistant(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgent(&streamRetryProvider{}, store, []Tool{echoTool{}})

	events, err := a.RunStream(context.Background(), "s-retry-reset", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawRetry bool
	for ev := range events {
		if ev.Type == AgentEventTypeProviderRetryScheduled {
			sawRetry = true
			if ev.ProviderRetry == nil || !ev.ProviderRetry.StreamReset {
				t.Fatalf("provider retry should request stream reset: %+v", ev.ProviderRetry)
			}
		}
		if ev.Type == AgentEventTypeError {
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	if !sawRetry {
		t.Fatal("missing provider retry event")
	}

	all, err := store.List(context.Background(), "s-retry-reset")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected user+assistant, got %d: %+v", len(all), all)
	}
	asst := all[1]
	if asst.Text != "new-answer" || asst.Reasoning != "new-thought" || len(asst.ToolCalls) != 0 {
		t.Fatalf("assistant retained stale stream state: %+v", asst)
	}
}

func TestRunStreamWithInjectedInputStoresVisibleAndHiddenMessages(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgent(&mockProviderWithDeltas{}, store, nil)

	events, err := a.RunStreamWithInjectedInput(context.Background(), "s-skill", "$demo do it", "<skill>demo</skill>")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	for range events {
	}
	all, err := store.List(context.Background(), "s-skill")
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(all) < 2 {
		t.Fatalf("expected at least two user messages, got %d", len(all))
	}
	if all[0].Role != RoleUser || all[0].Hidden || all[0].Text != "$demo do it" {
		t.Fatalf("unexpected visible message: %+v", all[0])
	}
	if all[1].Role != RoleUser || !all[1].Hidden || all[1].Text != "<skill>demo</skill>" {
		t.Fatalf("unexpected hidden message: %+v", all[1])
	}
}

type mockProviderWithDeltas struct{}

func (m *mockProviderWithDeltas) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 16)
	out <- ProviderEvent{Type: EventReasoningDelta, ReasoningDelta: "think-1"}
	out <- ProviderEvent{Type: EventContentDelta, Content: "hello"}
	out <- ProviderEvent{
		Type: EventToolArgsDelta,
		ToolArgsDelta: &ToolArgsDelta{
			ToolCallIndex: 0,
			ToolName:      "echo",
			ArgsDelta:     "{",
			ArgsChars:     1,
			ReadyCount:    0,
		},
	}
	out <- ProviderEvent{
		Type: EventToolArgsDelta,
		ToolArgsDelta: &ToolArgsDelta{
			ToolCallIndex: 0,
			ToolName:      "echo",
			ArgsDelta:     "\"x\":1}",
			ArgsChars:     7,
			ReadyCount:    1,
		},
	}
	out <- ProviderEvent{Type: EventComplete, Response: &ProviderResponse{FinishReason: FinishReasonEndTurn, Content: "hello", Reasoning: "think-1"}}
	close(out)
	return out
}

func TestRunStreamEmitsReasoningAndToolArgsDeltas(t *testing.T) {
	store := NewInMemoryStore()
	a := NewAgent(&mockProviderWithDeltas{}, store, nil)

	events, err := a.RunStream(context.Background(), "s-delta", "hi")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}

	var seenReasoning bool
	var seenToolArgs bool
	var gotDone *Message
	for ev := range events {
		switch ev.Type {
		case AgentEventTypeReasoningDelta:
			if ev.ReasoningDelta == "think-1" {
				seenReasoning = true
			}
		case AgentEventTypeToolArgsDelta:
			if ev.ToolArgs != nil && ev.ToolArgs.ToolName == "echo" && ev.ToolArgs.ReadyCount == 1 {
				seenToolArgs = true
			}
		case AgentEventTypeDone:
			gotDone = ev.Message
		case AgentEventTypeError:
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}

	if !seenReasoning {
		t.Fatal("expected reasoning delta event")
	}
	if !seenToolArgs {
		t.Fatal("expected tool args delta event")
	}
	if gotDone == nil || gotDone.Text != "hello" || gotDone.Reasoning != "think-1" {
		t.Fatalf("unexpected done message: %+v", gotDone)
	}
}

type historyCaptureProvider struct {
	histories [][]Message
}

func (p *historyCaptureProvider) StreamResponse(_ context.Context, history []Message, _ []Tool) <-chan ProviderEvent {
	copied := append([]Message(nil), history...)
	p.histories = append(p.histories, copied)

	return eventStream(endTurnEvent("ok"))
}

func TestResumeSessionHistoryAfterSwitchingSessions(t *testing.T) {
	store := NewInMemoryStore()
	prov := &historyCaptureProvider{}
	a := NewAgent(prov, store, nil)

	if _, err := a.RunSession(context.Background(), "s1", "first-s1"); err != nil {
		t.Fatalf("run s1 first turn failed: %v", err)
	}
	if _, err := a.RunSession(context.Background(), "s2", "first-s2"); err != nil {
		t.Fatalf("run s2 first turn failed: %v", err)
	}
	if _, err := a.RunSession(context.Background(), "s1", "second-s1"); err != nil {
		t.Fatalf("run s1 second turn failed: %v", err)
	}

	if len(prov.histories) != 3 {
		t.Fatalf("expected 3 provider calls, got %d", len(prov.histories))
	}

	third := prov.histories[2]
	if len(third) < 3 {
		t.Fatalf("expected resumed s1 history len>=3, got %d", len(third))
	}
	userAssistantUser := make([]Message, 0, 3)
	for _, m := range third {
		if m.Role == RoleUser || m.Role == RoleAssistant {
			userAssistantUser = append(userAssistantUser, m)
		}
	}
	if len(userAssistantUser) < 3 {
		t.Fatalf("expected at least 3 user/assistant messages, got %d", len(userAssistantUser))
	}
	if userAssistantUser[0].Role != RoleUser || userAssistantUser[0].Text != "first-s1" {
		t.Fatalf("unexpected third history[0]: %+v", userAssistantUser[0])
	}
	if userAssistantUser[1].Role != RoleAssistant || userAssistantUser[1].Text != "ok" {
		t.Fatalf("unexpected third history[1]: %+v", userAssistantUser[1])
	}
	if userAssistantUser[2].Role != RoleUser || userAssistantUser[2].Text != "second-s1" {
		t.Fatalf("unexpected third history[2]: %+v", userAssistantUser[2])
	}
}
