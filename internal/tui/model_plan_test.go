package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/usewhale/whale/internal/runtime/protocol"
	"github.com/usewhale/whale/internal/runtime/timeline"
	tuirender "github.com/usewhale/whale/internal/tui/render"
	"strings"
	"testing"
)

func TestPlanTurnDoneWithAssistantButNoProposedPlanDoesNotShowNotice(t *testing.T) {
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		chatMode:  "plan",
		width:     80,
		height:    24,
		busy:      true,
	}
	next, _ := m.Update(svcMsg(protocol.Event{
		Kind: protocol.EventAssistantDelta,
		Text: "Here is the test execution plan:\n\n- Run TUI tests\n- Run full tests",
	}))
	m = next.(model)
	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventTurnDone}))
	m = next.(model)

	if m.mode == modePlanImplementation {
		t.Fatal("did not expect implementation picker without a proposed_plan block")
	}
	got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(got, "Here is the test execution plan") {
		t.Fatalf("expected assistant text to remain visible:\n%s", got)
	}
	if strings.Contains(got, "No proposed plan was produced") || strings.Contains(got, "<proposed_plan>") {
		t.Fatalf("did not expect missing proposed plan notice in transcript:\n%s", got)
	}
	if m.sawAssistantThisTurn || m.sawPlanThisTurn {
		t.Fatal("expected turn tracking flags to reset")
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
func TestMarkMissingProposedPlanIfNeededLogsOnly(t *testing.T) {
	m := model{
		assembler:            tuirender.NewAssembler(),
		chatMode:             "plan",
		sawAssistantThisTurn: true,
	}
	if !m.markMissingProposedPlanIfNeeded(true) {
		t.Fatal("expected missing proposed plan to be marked")
	}
	snap := m.assembler.Snapshot()
	if len(snap) != 0 {
		t.Fatalf("expected no user-visible notice entry, got %+v", snap)
	}
	if len(m.logs) != 1 || m.logs[0].Kind != "missing_proposed_plan" {
		t.Fatalf("expected diagnostic log entry, got %+v", m.logs)
	}
}
func TestMarkMissingProposedPlanIfNeededSkippedWithPlan(t *testing.T) {
	m := model{
		assembler:            tuirender.NewAssembler(),
		chatMode:             "plan",
		sawAssistantThisTurn: true,
		sawPlanThisTurn:      true,
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
func TestQueuedPromptSuppressesPlanImplementationPicker(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.busy = true
	m.chatMode = "plan"
	m.mode = modeChat
	m.sawPlanThisTurn = true
	m.queuedPrompts = []queuedPrompt{{Text: "queued follow up"}}

	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventTurnDone}))
	m = next.(model)

	if m.mode == modePlanImplementation {
		t.Fatal("queued prompt should suppress plan implementation picker")
	}
	if len(*intents) != 1 || (*intents)[0].Input != "queued follow up" {
		t.Fatalf("expected queued follow-up submitted, got %+v", *intents)
	}
}
func TestPlanImplementationPickerDefersUntilLocalSubmitDone(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.busy = true
	m.chatMode = "plan"
	m.mode = modeChat
	m.sawPlanThisTurn = true
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
func TestQueuedPromptSuppressesDeferredPlanImplementationPicker(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.busy = true
	m.chatMode = "plan"
	m.mode = modeChat
	m.sawPlanThisTurn = true
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
	if m.mode == modePlanImplementation {
		t.Fatal("queued prompt should suppress deferred implementation picker")
	}
	if m.deferredPlanPicker {
		t.Fatal("expected queued prompt to clear deferred implementation picker")
	}
	if len(*intents) != 1 || (*intents)[0].Kind != protocol.IntentSubmit || (*intents)[0].Input != "queued follow up" {
		t.Fatalf("expected queued follow-up submitted after local submit done, got %+v", *intents)
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
