package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/usewhale/whale/internal/runtime/protocol"
	"github.com/usewhale/whale/internal/runtime/timeline"
	tuirender "github.com/usewhale/whale/internal/tui/render"
	"strings"
	"testing"
)

func TestPlanTurnDoneWithAssistantButNoProposedPlanShowsRecoveryNotice(t *testing.T) {
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		chatMode:  "plan",
		width:     80,
		height:    24,
		busy:      true,
	}
	next, _ := m.Update(svcMsg(protocol.Event{
		Kind: protocol.EventPlanDelta,
		Text: "Here is the test execution plan:\n\n- Run TUI tests\n- Run full tests",
	}))
	m = next.(model)
	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventTurnDone}))
	m = next.(model)

	if m.mode == modePlanImplementation {
		t.Fatal("did not expect implementation picker without a completed plan")
	}
	got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(got, "Here is the test execution plan") {
		t.Fatalf("expected investigation text to remain visible:\n%s", got)
	}
	if strings.Contains(got, "Proposed Plan") {
		t.Fatalf("uncompleted investigation text must not render as a Proposed Plan card:\n%s", got)
	}
	if !strings.Contains(got, "No plan was produced") || !strings.Contains(got, "final plan as its reply") {
		t.Fatalf("expected missing plan recovery notice in transcript:\n%s", got)
	}
	if m.sawPlanThisTurn || m.sawPlanCompletedThisTurn {
		t.Fatal("expected turn tracking flags to reset")
	}
}

// A Plan-mode turn that streamed only investigation preamble (plan_delta) but
// never finalized a plan (no plan_completed — e.g. it ended via a cap/forced
// summary) must NOT open the implementation picker: there is nothing to approve.
func TestPlanTurnDoneWithPlanDeltaButNoCompletionDoesNotShowPicker(t *testing.T) {
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		chatMode:  "plan",
		width:     80,
		height:    24,
		busy:      true,
	}
	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventPlanDelta, Text: "Let me inspect the file first."}))
	m = next.(model)
	if !m.sawPlanThisTurn {
		t.Fatal("plan_delta should mark sawPlanThisTurn")
	}
	if m.sawPlanCompletedThisTurn {
		t.Fatal("plan_delta alone must not mark a completed plan")
	}
	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventTurnDone}))
	m = next.(model)

	if m.mode == modePlanImplementation {
		t.Fatal("implementation picker must not open without a completed plan")
	}
	if m.deferredPlanPicker {
		t.Fatal("picker must not be deferred without a completed plan")
	}
}

// A Plan-mode preamble streamed as a plan delta must not freeze into the
// transcript as a Proposed Plan card when the turn is committed mid-flight (e.g.
// a shell_run approval decision) before the real plan is finalized. It should be
// demoted to ordinary assistant text; the later finalized plan renders the card.
func TestPlanModePreambleDemotedOnMidTurnCommit(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, chatMode: "plan", width: 80, height: 24, busy: true}

	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventPlanDelta, Text: "Let me first explore the file."}))
	m = next.(model)
	// Mid-turn commit before the plan is finalized (no plan_completed yet).
	m.commitLiveTranscript(false)

	for _, msg := range m.transcript {
		if msg.Kind == tuirender.KindPlan {
			t.Fatalf("preamble must not be committed as a Proposed Plan card: %+v", msg)
		}
	}
	joined := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(joined, "Let me first explore") {
		t.Fatalf("preamble text should remain visible as assistant text:\n%s", joined)
	}
	if strings.Contains(joined, "Proposed Plan") {
		t.Fatalf("preamble must not render under a Proposed Plan card:\n%s", joined)
	}

	// The real plan then finalizes and renders as a Proposed Plan card.
	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventPlanCompleted, Text: "# Plan\n- do the thing"}))
	m = next.(model)
	foundPlan := false
	for _, msg := range m.transcript {
		if msg.Kind == tuirender.KindPlan && strings.Contains(msg.Text, "do the thing") {
			foundPlan = true
		}
	}
	if !foundPlan {
		t.Fatalf("finalized plan should render as a Proposed Plan card, transcript=%+v", m.transcript)
	}
}

// In-progress Plan-mode content (plan deltas before plan_completed) must render
// in the live view as ordinary assistant text, never as a Proposed Plan card —
// otherwise interim preamble flashes the card chrome and then reverts.
func TestPlanModeLiveDeltasRenderAsTextUntilCompleted(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, chatMode: "plan", width: 80, height: 24, busy: true}
	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventPlanDelta, Text: "Let me first explore the file."}))
	m = next.(model)

	live := m.liveTranscriptMessages()
	for _, msg := range live {
		if msg.Kind == tuirender.KindPlan {
			t.Fatalf("in-progress plan delta must not render as a plan card: %+v", msg)
		}
	}
	joined := strings.Join(tuirender.ChatLines(live, 80), "\n")
	if !strings.Contains(joined, "Let me first explore") {
		t.Fatalf("plan delta text should be visible as assistant text:\n%s", joined)
	}
	if strings.Contains(joined, "Proposed Plan") {
		t.Fatalf("must not show Proposed Plan card while streaming:\n%s", joined)
	}

	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventPlanCompleted, Text: "# Plan\n- do the thing"}))
	m = next.(model)
	full := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(full, "Proposed Plan") || !strings.Contains(full, "do the thing") {
		t.Fatalf("finalized plan should render as a Proposed Plan card:\n%s", full)
	}
}

// A Plan-mode turn that replies directly (no tool rounds) finalizes the plan via
// plan_completed; the turn-done reconcile must NOT also append the plan text as a
// separate assistant bubble — otherwise the same content renders twice (once as a
// Proposed Plan card, once as plain text).
func TestPlanTurnDoneDoesNotDuplicateCompletedPlanAsAssistantText(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, chatMode: "plan", width: 80, height: 24, busy: true}
	const plan = "I'm in Plan mode, so I can't commit. Switch to Agent mode to proceed."

	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventPlanDelta, Text: plan}))
	m = next.(model)
	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventPlanCompleted, Text: plan}))
	m = next.(model)
	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventTurnDone, LastResponse: plan, Metadata: agentTurnMetadata()}))
	m = next.(model)

	plans, texts := 0, 0
	for _, msg := range m.transcript {
		switch msg.Kind {
		case tuirender.KindPlan:
			plans++
		case tuirender.KindText:
			if msg.Role == "assistant" && strings.Contains(msg.Text, "Plan mode") {
				texts++
			}
		}
	}
	if plans != 1 {
		t.Fatalf("expected exactly one Proposed Plan card, got %d (transcript=%+v)", plans, m.transcript)
	}
	if texts != 0 {
		t.Fatalf("plan must not be duplicated as an assistant text bubble, got %d (transcript=%+v)", texts, m.transcript)
	}
}

func TestPlanTurnDoneWithQuotedProposedPlanTagDoesNotShowPicker(t *testing.T) {
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		chatMode:  "plan",
		width:     80,
		height:    24,
		busy:      true,
	}
	text := "Tool result says to output the final plan in a `<proposed_plan>` block."
	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventAssistantDelta, Text: text}))
	m = next.(model)
	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventTurnDone}))
	m = next.(model)

	if m.mode == modePlanImplementation {
		t.Fatal("did not expect implementation picker for quoted proposed_plan tag")
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(rendered, "<proposed_plan>") {
		t.Fatalf("expected quoted tag text to remain visible:\n%s", rendered)
	}
}

func TestMarkNoFinalAnswerIfNeededAddsPlanNotice(t *testing.T) {
	m := model{
		assembler:            tuirender.NewAssembler(),
		chatMode:             "plan",
		sawReasoningThisTurn: true,
	}
	if !m.markNoFinalAnswerIfNeeded() {
		t.Fatal("expected no-final-answer status to be marked")
	}
	snap := m.assembler.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected one notice entry, got %+v", snap)
	}
	if snap[0].Kind != tuirender.KindStatus || snap[0].Role != "status" {
		t.Fatalf("expected status entry, got %+v", snap[0])
	}
	if !strings.Contains(snap[0].Text, "reasoning only") || !strings.Contains(snap[0].Text, "visible plan") {
		t.Fatalf("expected reasoning-only plan status, got %q", snap[0].Text)
	}
}
func TestMarkMissingProposedPlanIfNeededAddsRecoveryNotice(t *testing.T) {
	m := model{
		assembler:       tuirender.NewAssembler(),
		chatMode:        "plan",
		sawPlanThisTurn: true,
	}
	// A streamed-but-uncompleted plan card should be demoted, then flagged.
	m.assembler.AddPlanDelta("Let me inspect the file first.")
	if !m.markMissingProposedPlanIfNeeded(true) {
		t.Fatal("expected missing proposed plan to be marked")
	}
	snap := m.assembler.Snapshot()
	for _, msg := range snap {
		if msg.Kind == tuirender.KindPlan {
			t.Fatalf("uncompleted plan card should be demoted, got %+v", msg)
		}
	}
	var status tuirender.UIMessage
	for _, msg := range snap {
		if msg.Kind == tuirender.KindStatus {
			status = msg
		}
	}
	if !strings.Contains(status.Text, "No plan was produced") || !strings.Contains(status.Text, "final plan as its reply") {
		t.Fatalf("expected missing plan recovery notice, got %+v", status)
	}
	if len(m.logs) != 1 || m.logs[0].Kind != "missing_proposed_plan" {
		t.Fatalf("expected diagnostic log entry, got %+v", m.logs)
	}
}
func TestMarkMissingProposedPlanIfNeededSkippedWithPlan(t *testing.T) {
	m := model{
		assembler:                tuirender.NewAssembler(),
		chatMode:                 "plan",
		sawPlanThisTurn:          true,
		sawPlanCompletedThisTurn: true,
	}
	if m.markMissingProposedPlanIfNeeded(true) {
		t.Fatal("did not expect missing proposed plan notice after plan completion")
	}
	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected no chat entries, got %d", got)
	}
}
func TestVisibleSubmittedTextForPlanPrompt(t *testing.T) {
	if got := visibleSubmittedText("/ask inspect the parser"); got != "inspect the parser" {
		t.Fatalf("unexpected visible text for ask prompt: %q", got)
	}
	if got := visibleSubmittedText("/plan inspect the parser"); got != "inspect the parser" {
		t.Fatalf("unexpected visible text: %q", got)
	}
	if got := visibleSubmittedText("/plan"); got != "/plan" {
		t.Fatalf("unexpected visible text for bare plan: %q", got)
	}
	if got := visibleSubmittedText("/plan off"); got != "/plan off" {
		t.Fatalf("unexpected visible text for unsupported plan off: %q", got)
	}
}
func TestLiveTokenEstimateCountsReasoningAndPlanDeltas(t *testing.T) {
	m := newModel(nil, "", "", "")

	m.handleServiceEvent(protocol.Event{Kind: protocol.EventReasoningDelta, Text: "think "})
	m.handleServiceEvent(protocol.Event{Kind: protocol.EventPlanDelta, Text: "plan "})
	m.handleServiceEvent(protocol.Event{Kind: protocol.EventAssistantDelta, Text: "answer"})

	want := estimateTokens("think plan answer")
	if m.busyTokenCount != want {
		t.Fatalf("live token estimate = %d, want %d", m.busyTokenCount, want)
	}
	if m.visibleAssistantThisTurn != "answer" {
		t.Fatalf("assistant visibility should only track assistant deltas, got %q", m.visibleAssistantThisTurn)
	}
}
func TestSlashSuggestionPlanAutoRunsWhenSelected(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.input.SetValue("/pl")
	m.updateSlashMatches()
	selectSlashCommand(t, &m, "/plan")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 {
		t.Fatalf("expected one dispatch for selected /plan, got %+v", *intents)
	}
	if (*intents)[0].Kind != protocol.IntentSubmitLocal || (*intents)[0].Input != "/plan" {
		t.Fatalf("unexpected dispatched intent: %+v", (*intents)[0])
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected input cleared after /plan autorun, got %q", got)
	}
	if m.busy || !m.busySince.IsZero() {
		t.Fatalf("expected /plan autorun not to start working state, busy=%v busySince=%v", m.busy, m.busySince)
	}
}
// A pending plan must be gated: a queued/typed prompt must NOT bypass the picker
// by running as another model turn (that would let the model misread arbitrary
// input as approval). The picker opens; the queued text is restored to the
// composer for after the decision, not submitted.
func TestPlanImplementationPickerTakesPriorityOverQueuedPrompt(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.busy = true
	m.chatMode = "plan"
	m.mode = modeChat
	m.sawPlanThisTurn = true
	m.sawPlanCompletedThisTurn = true
	m.queuedPrompts = []queuedPrompt{{Text: "queued follow up"}}

	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventTurnDone}))
	m = next.(model)

	if m.mode != modePlanImplementation {
		t.Fatalf("expected plan implementation picker to take priority, got mode %v", m.mode)
	}
	for _, in := range *intents {
		if in.Input == "queued follow up" {
			t.Fatalf("queued prompt must not be submitted as a model turn while a plan is pending, got %+v", *intents)
		}
	}
	if !strings.Contains(m.input.Value(), "queued follow up") {
		t.Fatalf("queued text should be restored to the composer, got %q", m.input.Value())
	}
}
func TestPlanImplementationPickerDefersUntilLocalSubmitDone(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.busy = true
	m.chatMode = "plan"
	m.mode = modeChat
	m.sawPlanThisTurn = true
	m.sawPlanCompletedThisTurn = true
	m.localSubmitPending = 1

	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventTurnDone}))
	m = next.(model)
	if m.mode == modePlanImplementation {
		t.Fatal("plan implementation picker should wait for pending local submit")
	}
	if !m.deferredPlanPicker {
		t.Fatal("expected plan implementation picker to be deferred")
	}
	if m.status != "wait for command to finish" {
		t.Fatalf("expected wait status while plan picker is deferred, got %q", m.status)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 0 {
		t.Fatalf("pending local submit should block implementation intent, got %+v", *intents)
	}

	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventLocalSubmitDone}))
	m = next.(model)
	if m.mode != modePlanImplementation {
		t.Fatalf("expected deferred implementation picker after local submit done, got mode %v", m.mode)
	}
	if m.deferredPlanPicker {
		t.Fatal("expected deferred picker flag to clear after opening")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if len(*intents) != 1 || (*intents)[0].Kind != protocol.IntentImplementPlan {
		t.Fatalf("expected implementation intent after pending local submit clears, got %+v", *intents)
	}
}
// Even with a queued prompt, a deferred plan picker (held back by an in-flight
// local submit) must take priority once the local submit finishes: the picker
// opens and the queued text is restored to the composer, not run as a model turn.
func TestDeferredPlanImplementationPickerTakesPriorityOverQueuedPrompt(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.busy = true
	m.chatMode = "plan"
	m.mode = modeChat
	m.sawPlanThisTurn = true
	m.sawPlanCompletedThisTurn = true
	m.localSubmitPending = 1
	m.queuedPrompts = []queuedPrompt{{Text: "queued follow up"}}

	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventTurnDone}))
	m = next.(model)
	if m.mode == modePlanImplementation {
		t.Fatal("plan implementation picker should not open before local submit done")
	}
	if !m.deferredPlanPicker {
		t.Fatal("expected plan picker to be deferred while local submit is pending")
	}

	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventLocalSubmitDone}))
	m = next.(model)
	if m.mode != modePlanImplementation {
		t.Fatalf("expected deferred picker to open after local submit done, got mode %v", m.mode)
	}
	for _, in := range *intents {
		if in.Input == "queued follow up" {
			t.Fatalf("queued prompt must not be submitted while a plan is pending, got %+v", *intents)
		}
	}
	if !strings.Contains(m.input.Value(), "queued follow up") {
		t.Fatalf("queued text should be restored to the composer, got %q", m.input.Value())
	}
}
func TestPlanCompletedReplacesPartialPlanAndTurnDoneShowsPicker(t *testing.T) {
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		chatMode:  "plan",
		busy:      true,
		status:    "working",
	}
	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventPlanDelta, Text: "partial"}))
	m = next.(model)
	liveRendered := strings.Join(tuirender.ChatLines(m.assembler.Snapshot(), 80), "\n")
	if !strings.Contains(liveRendered, "Proposed Plan") || !strings.Contains(liveRendered, "partial") {
		t.Fatalf("expected live proposed plan render, got %q", liveRendered)
	}
	next, cmd := m.Update(svcMsg(protocol.Event{Kind: protocol.EventPlanCompleted, Text: "complete final plan"}))
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected wait-event command")
	}
	if snap := m.assembler.Snapshot(); len(snap) != 0 {
		t.Fatalf("expected completed plan to leave live assembler empty, got %+v", snap)
	}
	if len(m.transcript) != 1 || m.transcript[0].Kind != tuirender.KindPlan {
		t.Fatalf("expected completed plan in transcript, got %+v", m.transcript)
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(rendered, "Proposed Plan") || !strings.Contains(rendered, "complete final plan") {
		t.Fatalf("expected rendered proposed plan, got %q", rendered)
	}
	if m.lastProposedPlan != "complete final plan" {
		t.Fatalf("expected last proposed plan to be captured, got %q", m.lastProposedPlan)
	}
	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventTurnDone, LastResponse: "done"}))
	m = next.(model)

	if m.mode != modePlanImplementation {
		t.Fatalf("expected implementation picker, got mode %v", m.mode)
	}
	if m.busy {
		t.Fatal("expected busy=false after turn done")
	}
	snap := m.assembler.Snapshot()
	if len(snap) != 0 {
		t.Fatalf("expected completed turn to move plan out of live assembler, got %+v", snap)
	}
}
func TestPlanImplementationIntentDoesNotEmbedLastProposedPlan(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.mode = modePlanImplementation
	m.planImplementation.index = 0
	m.lastProposedPlan = "# Plan\n- Patch it"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if len(*intents) != 1 || (*intents)[0].Kind != protocol.IntentImplementPlan {
		t.Fatalf("expected implement intent, got %+v", *intents)
	}
	if (*intents)[0].Input != "" {
		t.Fatalf("expected implement intent to avoid embedding plan text, got %q", (*intents)[0].Input)
	}
	if m.chatMode != "agent" {
		t.Fatalf("expected chat mode switched to agent, got %q", m.chatMode)
	}
}
func TestPlanImplementationNoDeclinesAndClearsPendingPlan(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.mode = modePlanImplementation
	m.chatMode = "plan"
	m.planImplementation.index = 1
	m.lastProposedPlan = "# Plan\n- Patch it"
	m.sawPlanThisTurn = true
	m.sawPlanCompletedThisTurn = true
	m.deferredPlanPicker = true

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	if len(*intents) != 1 || (*intents)[0].Kind != protocol.IntentDeclinePlan {
		t.Fatalf("expected decline intent, got %+v", *intents)
	}
	if m.mode != modeChat {
		t.Fatalf("expected chat mode after decline popup, got %v", m.mode)
	}
	if m.chatMode != "plan" {
		t.Fatalf("decline should stay in plan chat mode, got %q", m.chatMode)
	}
	if m.lastProposedPlan != "" || m.sawPlanThisTurn || m.deferredPlanPicker || m.planImplementation.index != 0 {
		t.Fatalf("expected stale plan state cleared, last=%q saw=%v deferred=%v index=%d", m.lastProposedPlan, m.sawPlanThisTurn, m.deferredPlanPicker, m.planImplementation.index)
	}
}
func TestPlanImplementationEscDeclinesAndStaysInPlanMode(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.mode = modePlanImplementation
	m.chatMode = "plan"
	m.lastProposedPlan = "# Plan\n- Patch it"
	m.sawPlanThisTurn = true
	m.sawPlanCompletedThisTurn = true
	m.deferredPlanPicker = true

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)

	if len(*intents) != 1 || (*intents)[0].Kind != protocol.IntentDeclinePlan {
		t.Fatalf("expected decline intent, got %+v", *intents)
	}
	if m.mode != modeChat || m.chatMode != "plan" {
		t.Fatalf("expected esc decline to close popup and stay in plan mode, mode=%v chatMode=%q", m.mode, m.chatMode)
	}
	if m.lastProposedPlan != "" || m.sawPlanThisTurn || m.deferredPlanPicker {
		t.Fatalf("expected stale plan state cleared, last=%q saw=%v deferred=%v", m.lastProposedPlan, m.sawPlanThisTurn, m.deferredPlanPicker)
	}
}
func TestPlanUpdateEventRendersUpdatedPlan(t *testing.T) {
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
	}
	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventPlanUpdate, Text: "[x] Inspect\n[~] Patch\n[ ] Test"}))
	m = next.(model)
	if len(m.transcript) != 0 {
		t.Fatalf("plan update should wait for tool result before committing transcript, got %+v", m.transcript)
	}
	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventToolResult, ToolCallID: "plan-1", ToolName: "update_plan", Text: `{"success":true}`}))
	m = next.(model)
	if len(m.transcript) != 1 || m.transcript[0].Kind != tuirender.KindPlanUpdate {
		t.Fatalf("expected plan update in transcript, got %+v", m.transcript)
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(rendered, "Updated Plan") || !strings.Contains(rendered, "Patch") {
		t.Fatalf("expected rendered updated plan, got %q", rendered)
	}
}

func TestPlanUpdateInPlanModeDoesNotOpenImplementationPicker(t *testing.T) {
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		chatMode:  "plan",
		busy:      true,
		status:    "working",
	}
	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventPlanUpdate, Text: "[~] Draft checklist"}))
	m = next.(model)
	if m.sawPlanThisTurn {
		t.Fatal("plan_update must not count as a proposed plan")
	}
	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventToolResult, ToolCallID: "plan-1", ToolName: "update_plan", Text: `{"success":true}`}))
	m = next.(model)
	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventTurnDone, LastResponse: "done"}))
	m = next.(model)
	if m.mode == modePlanImplementation {
		t.Fatal("plan_update should not open the implementation picker")
	}
	if m.chatMode != "plan" {
		t.Fatalf("expected to stay in plan mode, got %q", m.chatMode)
	}
}

func TestPlanUpdateDoesNotClearPendingToolCallsBeforeResult(t *testing.T) {
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		timeline:  timeline.NewTurnTimelineBuilder(),
	}
	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventToolCall, ToolCallID: "read-1", ToolName: "read_file", Text: `read_file: docs/plugins.md`}))
	m = next.(model)
	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventToolCall, ToolCallID: "plan-1", ToolName: "update_plan", Text: `update_plan: 2 step(s)`}))
	m = next.(model)
	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventPlanUpdate, Text: "[x] Inspect\n[ ] Report"}))
	m = next.(model)

	if !m.hasPendingLifecycleItems() {
		t.Fatal("plan update event cleared unrelated pending lifecycle item")
	}
	if got := m.timeline.Snapshot().Items; len(got) != 1 || got[0].ToolCallID != "read-1" {
		t.Fatalf("update_plan should not create a lifecycle row, got %+v", got)
	}
	if len(m.transcript) != 0 {
		t.Fatalf("plan update should remain live while another tool is pending, got %+v", m.transcript)
	}

	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventToolResult, ToolCallID: "plan-1", ToolName: "update_plan", Text: `{"success":true}`}))
	m = next.(model)
	if len(m.transcript) != 0 {
		t.Fatalf("update_plan result should not commit while read tool is pending, got %+v", m.transcript)
	}

	readResult := `{"success":true,"data":{"content":"ok"},"metadata":{"duration_ms":1}}`
	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventToolResult, ToolCallID: "read-1", ToolName: "read_file", Text: readResult}))
	m = next.(model)
	if len(m.transcript) != 2 {
		t.Fatalf("expected read result and plan update committed together, got %+v", m.transcript)
	}
	if m.transcript[0].Text == "Updating plan" || strings.Contains(m.transcript[0].Text, "Updating plan") {
		t.Fatalf("stale update_plan tool row was committed: %+v", m.transcript)
	}
	if m.transcript[1].Kind != tuirender.KindPlanUpdate {
		t.Fatalf("expected plan update after pending tool resolves, got %+v", m.transcript)
	}
}
func TestStalePlanCompletionDoesNotOpenPickerOnLaterTurnDone(t *testing.T) {
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		chatMode:  "plan",
		busy:      false,
		status:    "ready",
	}
	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventPlanCompleted, Text: "complete final plan"}))
	m = next.(model)
	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventTurnDone, LastResponse: "status"}))
	m = next.(model)

	if m.mode == modePlanImplementation {
		t.Fatal("stale plan completion opened implementation picker")
	}
	if m.sawPlanThisTurn {
		t.Fatal("expected stale plan flag to reset after turn done")
	}
}
func TestPlanCompletedWithoutDeltasStillRendersPlan(t *testing.T) {
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		chatMode:  "plan",
		busy:      true,
	}
	plan := strings.Repeat("final plan\n", 100)
	next, cmd := m.Update(svcMsg(protocol.Event{Kind: protocol.EventPlanCompleted, Text: plan}))
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected wait-event command")
	}
	if snap := m.assembler.Snapshot(); len(snap) != 0 {
		t.Fatalf("expected final plan to leave live assembler empty, got %+v", snap)
	}
	if len(m.transcript) != 1 || m.transcript[0].Kind != tuirender.KindPlan {
		t.Fatalf("expected final plan in transcript, got %+v", m.transcript)
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(rendered, "Proposed Plan") || !strings.Contains(rendered, "final plan") {
		t.Fatalf("expected rendered proposed plan, got %q", rendered)
	}
	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventTurnDone, LastResponse: "done"}))
	m = next.(model)
	snap := m.assembler.Snapshot()
	if len(snap) != 0 {
		t.Fatalf("expected final plan to be committed and live assembler cleared, got %+v", snap)
	}
	if m.mode != modePlanImplementation {
		t.Fatalf("expected implementation picker, got mode %v", m.mode)
	}
}
func TestBusyLocalCommandResultDoesNotDuplicateCompletedPlan(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, chatMode: "plan", width: 100, height: 30, busy: true}
	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventPlanDelta, Text: "partial plan"}))
	m = next.(model)
	next, _ = m.Update(svcMsg(protocol.Event{
		Kind:   protocol.EventLocalSubmitResult,
		Status: "info",
		Text:   "Status\n\nsession: test-session",
	}))
	m = next.(model)
	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventPlanCompleted, Text: "complete final plan"}))
	m = next.(model)

	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected completed plan and local result to commit together, got %+v", m.assembler.Snapshot())
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	planIx := strings.Index(rendered, "complete final plan")
	localIx := strings.Index(rendered, "session: test-session")
	if planIx < 0 || localIx < 0 || planIx > localIx {
		t.Fatalf("expected completed plan before local result:\n%s", rendered)
	}
	if strings.Count(rendered, "complete final plan") != 1 || strings.Contains(rendered, "partial plan") {
		t.Fatalf("expected final plan to replace partial plan once:\n%s", rendered)
	}
}
func TestSummarizeToolResultForChat_RequestReplanHidesInternalRecoveryText(t *testing.T) {
	raw := `{"success":false,"code":"request_replan","error":"recovery exhausted, replan required","data":{"tool_name":"mcp__fs__search_files","last_error":"{\"success\":false,\"code\":\"mcp_tool_error\",\"error\":\"Error: Access denied - path outside allowed directories: /workspace not in /tmp\"}"}}`
	role, got := summarizeToolResultForChat("mcp__fs__search_files", raw)
	if role != "result_failed" {
		t.Fatalf("expected result_failed role, got %q", role)
	}
	if strings.Contains(got, "recovery exhausted") || strings.Contains(got, "replan required") {
		t.Fatalf("summary leaked internal recovery text: %q", got)
	}
	if !strings.Contains(got, "Access blocked") || !strings.Contains(got, "/workspace") {
		t.Fatalf("expected user-facing access block, got %q", got)
	}
}
