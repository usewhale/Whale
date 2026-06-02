package tui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/usewhale/whale/internal/runtime/protocol"
	tuirender "github.com/usewhale/whale/internal/tui/render"
	"strings"
	"testing"
	"time"
)

func TestTurnDoneReasoningOnlyCommitsFallback(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 80, height: 24, busy: true}
	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventReasoningDelta, Text: "I should answer."}))
	m = next.(model)
	next, cmd := m.Update(svcMsg(protocol.Event{Kind: protocol.EventTurnDone}))
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected wait-event command")
	}
	if got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n"); !strings.Contains(got, "Reasoning only") || !strings.Contains(got, "did not produce a visible answer") {
		t.Fatalf("expected reasoning-only status in transcript:\n%s", got)
	}
	if m.sawReasoningThisTurn || m.sawAssistantThisTurn {
		t.Fatal("expected turn tracking flags to reset")
	}
}
func TestTurnDoneReconcilesDroppedAssistantDeltaFromLastResponse(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 80, height: 8, busy: true}
	next, _ := m.Update(svcMsg(protocol.Event{
		Kind: protocol.EventAssistantDelta,
		Text: "visible answer head\n",
	}))
	m = next.(model)

	next, _ = m.Update(svcMsg(protocol.Event{
		Kind:         protocol.EventTurnDone,
		LastResponse: "visible answer head\nmissing latest tail",
		Metadata:     agentTurnMetadata(),
	}))
	m = next.(model)

	got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(got, "missing latest tail") {
		t.Fatalf("expected turn completion to reconcile dropped assistant delta from LastResponse:\n%s", got)
	}
}
func TestTurnDoneRecoversAssistantWhenAllDeltasDropped(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.appendTranscript("you", tuirender.KindText, "prompt")
	m.beginTurnTranscript()
	m.busy = true

	next, _ := m.Update(svcMsg(protocol.Event{
		Kind:         protocol.EventTurnDone,
		LastResponse: "final answer only present in LastResponse",
		Metadata:     agentTurnMetadata(),
	}))
	m = next.(model)

	got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(got, "final answer only present in LastResponse") {
		t.Fatalf("expected turn completion to recover assistant text from LastResponse:\n%s", got)
	}
	if strings.Contains(got, "did not produce a visible answer") {
		t.Fatalf("did not expect reasoning-only fallback after LastResponse recovery:\n%s", got)
	}
}
func TestTurnDoneAddsDurationNoticeForLongTurn(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 80, height: 24, busy: true}
	m.busySince = time.Now().Add(-(3*time.Minute + 5*time.Second))

	next, _ := m.Update(svcMsg(protocol.Event{
		Kind:         protocol.EventTurnDone,
		LastResponse: "done",
		Metadata:     agentTurnMetadata(),
	}))
	m = next.(model)

	got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(got, "done") {
		t.Fatalf("expected final assistant response in transcript:\n%s", got)
	}
	if !strings.Contains(got, "✻ Worked for 3m ") {
		t.Fatalf("expected turn duration notice in transcript:\n%s", got)
	}
}
func TestTurnDoneSkipsDurationNoticeForShortTurn(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 80, height: 24, busy: true}
	m.busySince = time.Now().Add(-29 * time.Second)

	next, _ := m.Update(svcMsg(protocol.Event{
		Kind:         protocol.EventTurnDone,
		LastResponse: "done",
		Metadata:     agentTurnMetadata(),
	}))
	m = next.(model)

	got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if strings.Contains(got, "Worked for") {
		t.Fatalf("did not expect turn duration notice for short turn:\n%s", got)
	}
}
func TestFormatTurnDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{name: "negative", duration: -time.Second, want: "0s"},
		{name: "seconds", duration: 45 * time.Second, want: "45s"},
		{name: "minutes", duration: 2*time.Minute + 9*time.Second, want: "2m 9s"},
		{name: "hours", duration: time.Hour + 2*time.Minute + 3*time.Second, want: "1h 2m 3s"},
		{name: "days", duration: 24*24*time.Hour + time.Minute + 2*time.Second, want: "24d 0h 1m 2s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTurnDuration(tt.duration); got != tt.want {
				t.Fatalf("formatTurnDuration(%s) = %q, want %q", tt.duration, got, tt.want)
			}
		})
	}
}
func TestReplacingCurrentTurnAssistantRewindsNativeScrollback(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.appendTranscript("you", tuirender.KindText, "prompt")
	m.beginTurnTranscript()
	m.appendTranscript("assistant", tuirender.KindText, "partial visible answer")
	m.appendTranscript("result_ok", tuirender.KindToolResult, "✓")
	m.visibleAssistantThisTurn = "partial visible answer"
	m.sawAssistantThisTurn = true
	m.nativeScrollbackPrinted = len(m.transcript)

	reconciled := m.reconcileFinalAssistant("corrected final answer")
	m.commitLiveTranscript(reconciled)

	cmd := m.flushNativeScrollbackCmd()
	if cmd == nil {
		t.Fatal("expected rewritten transcript to produce corrected native scrollback output")
	}
	if got := fmt.Sprintf("%#v", cmd()); !strings.Contains(got, "corrected final answer") {
		t.Fatalf("expected corrected final answer in native scrollback output, got %s", got)
	}
}
func TestMarkNoFinalAnswerIfNeeded(t *testing.T) {
	m := model{
		assembler:            tuirender.NewAssembler(),
		sawReasoningThisTurn: true,
	}
	if !m.markNoFinalAnswerIfNeeded() {
		t.Fatal("expected no-final-answer status to be marked")
	}
	if m.status != "" {
		t.Fatalf("unexpected status: %q", m.status)
	}
	snap := m.assembler.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected one notice entry, got %+v", snap)
	}
	if snap[0].Kind != tuirender.KindStatus || snap[0].Role != "status" {
		t.Fatalf("expected status entry, got %+v", snap[0])
	}
	if !strings.Contains(snap[0].Text, "reasoning only") || !strings.Contains(snap[0].Text, "visible answer") {
		t.Fatalf("expected generic reasoning-only status, got %q", snap[0].Text)
	}
	if len(m.logs) != 1 || m.logs[0].Kind != "no_final_answer" {
		t.Fatalf("expected diagnostic log entry, got %+v", m.logs)
	}
}
func TestMarkNoFinalAnswerIfNeededSkippedWithTerminalToolOutcome(t *testing.T) {
	m := model{
		assembler:                      tuirender.NewAssembler(),
		sawReasoningThisTurn:           true,
		sawTerminalToolOutcomeThisTurn: true,
	}
	if m.markNoFinalAnswerIfNeeded() {
		t.Fatal("did not expect no-final-answer status after terminal tool outcome")
	}
	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected no chat entries, got %d", got)
	}
}
func TestSuppressesNoFinalAnswerForSemanticTerminalToolRoles(t *testing.T) {
	for _, role := range []string{
		"result_denied",
		"result_canceled",
		"result_timeout",
		"result_blocked",
		"result_mode_hint",
		"result_http_error",
		"result_usage_hint",
	} {
		if !suppressesNoFinalAnswer(role) {
			t.Fatalf("expected %q to suppress no-final-answer notice", role)
		}
	}
	if suppressesNoFinalAnswer("result_failed") {
		t.Fatal("ordinary failures should not suppress no-final-answer notice")
	}
}
func TestMarkNoFinalAnswerIfNeededSkippedWithAssistant(t *testing.T) {
	m := model{
		assembler:            tuirender.NewAssembler(),
		sawReasoningThisTurn: true,
		sawAssistantThisTurn: true,
	}
	if m.markNoFinalAnswerIfNeeded() {
		t.Fatal("did not expect no-final-answer status with assistant answer")
	}
	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected no chat entries, got %d", got)
	}
}
func TestProviderRetryEventUpdatesStatusWithoutTranscript(t *testing.T) {
	m := newModel(nil, "", "", "")
	beforeTranscript := len(m.transcript)
	m.handleServiceEvent(protocol.Event{Kind: protocol.EventProviderRetry, Text: "API rate limited, retrying in 2s (1/3)", Metadata: map[string]any{"delay_ms": int64(2000)}})

	if m.status != "ready" {
		t.Fatalf("status should not be overwritten by retry, got %s", m.status)
	}
	if m.providerRetryStatus != "API rate limited, retrying in 2s (1/3)" {
		t.Fatalf("providerRetryStatus: %s", m.providerRetryStatus)
	}
	if len(m.transcript) != beforeTranscript {
		t.Fatalf("retry event should not append transcript, before=%d after=%d", beforeTranscript, len(m.transcript))
	}
	if len(m.logs) == 0 || m.logs[len(m.logs)-1].Kind != "api_retry" {
		t.Fatalf("missing api_retry log: %+v", m.logs)
	}
}
func TestProviderRetryStatusClearsOnAssistantDelta(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.handleServiceEvent(protocol.Event{Kind: protocol.EventProviderRetry, Text: "API rate limited, retrying in 2s (1/3)", Metadata: map[string]any{"delay_ms": int64(2000)}})
	m.handleServiceEvent(protocol.Event{Kind: protocol.EventAssistantDelta, Text: "ok"})

	if m.providerRetryStatus != "" || !m.providerRetryUntil.IsZero() {
		t.Fatalf("provider retry status not cleared: %q until=%v", m.providerRetryStatus, m.providerRetryUntil)
	}
}
func TestProviderRetryStreamResetClearsLiveAttempt(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.handleServiceEvent(protocol.Event{Kind: protocol.EventAssistantDelta, Text: "old answer"})
	m.handleServiceEvent(protocol.Event{Kind: protocol.EventReasoningDelta, Text: "old thought"})
	m.handleServiceEvent(protocol.Event{Kind: protocol.EventToolCall, ToolCallID: "tc-old", ToolName: "shell_run", Text: `shell_run: {"command":"date"}`})

	if len(m.liveTranscriptMessages()) == 0 {
		t.Fatal("expected live attempt content before retry reset")
	}
	if m.busyTokenCount == 0 {
		t.Fatal("expected live token count before retry reset")
	}
	m.handleServiceEvent(protocol.Event{
		Kind:     protocol.EventProviderRetry,
		Text:     "API stream disconnected, retrying in 1s (1/1)",
		Metadata: map[string]any{"delay_ms": int64(1000), "stage": "stream", "stream_reset": true},
	})

	if got := len(m.liveTranscriptMessages()); got != 0 {
		t.Fatalf("expected live attempt cleared, got %+v", m.liveTranscriptMessages())
	}
	if m.visibleAssistantThisTurn != "" || m.sawAssistantThisTurn || m.sawReasoningThisTurn {
		t.Fatalf("turn visibility not reset: visible=%q assistant=%v reasoning=%v", m.visibleAssistantThisTurn, m.sawAssistantThisTurn, m.sawReasoningThisTurn)
	}
	if m.busyTokenCount != 0 {
		t.Fatalf("expected live token count reset, got %d", m.busyTokenCount)
	}
	if m.providerRetryStatus == "" {
		t.Fatal("expected provider retry status after reset")
	}
	if len(m.transcript) != 0 {
		t.Fatalf("retry reset should not append transcript: %+v", m.transcript)
	}
}

func TestResponseResetClearsLiveAndCommittedCurrentTurnOutput(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.appendTranscript("you", tuirender.KindText, "start")
	m.beginTurnTranscript()
	m.handleServiceEvent(protocol.Event{Kind: protocol.EventAssistantDelta, Text: "old answer"})
	m.handleServiceEvent(protocol.Event{Kind: protocol.EventReasoningDelta, Text: "old thought"})
	m.handleServiceEvent(protocol.Event{Kind: protocol.EventToolCall, ToolCallID: "tc-old", ToolName: "shell_run", Text: `shell_run: {"command":"date"}`})
	m.commitLiveTranscript(false)

	before := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(before, "old answer") || !strings.Contains(before, "Running") {
		t.Fatalf("expected committed old response and tool call before reset:\n%s", before)
	}

	m.handleServiceEvent(protocol.Event{Kind: protocol.EventResponseReset})
	afterReset := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if strings.Contains(afterReset, "old answer") || strings.Contains(afterReset, "old thought") {
		t.Fatalf("response reset left stale model output:\n%s", afterReset)
	}
	if !strings.Contains(afterReset, "Running") {
		t.Fatalf("response reset should keep tool context:\n%s", afterReset)
	}
	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected live assembler cleared, got %+v", m.assembler.Snapshot())
	}
	if m.visibleAssistantThisTurn != "" || m.sawAssistantThisTurn || m.sawReasoningThisTurn {
		t.Fatalf("turn visibility not reset: visible=%q assistant=%v reasoning=%v", m.visibleAssistantThisTurn, m.sawAssistantThisTurn, m.sawReasoningThisTurn)
	}

	m.handleServiceEvent(protocol.Event{Kind: protocol.EventAssistantDelta, Text: "steered answer"})
	m.handleServiceEvent(protocol.Event{Kind: protocol.EventTurnDone, LastResponse: "steered answer"})
	final := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if strings.Contains(final, "old answer") || !strings.Contains(final, "steered answer") {
		t.Fatalf("unexpected transcript after steered answer:\n%s", final)
	}
}

func TestLiveTokenEstimateAccumulatesDeltas(t *testing.T) {
	m := newModel(nil, "", "", "")
	full := "hello 世界"

	for _, r := range full {
		m.recordAssistantDelta(string(r))
	}

	if m.busyTokenCount != estimateTokens(full) {
		t.Fatalf("chunked estimate = %d, want %d", m.busyTokenCount, estimateTokens(full))
	}
	if m.busyTokenASCIIChars != 6 || m.busyTokenNonASCIIChars != 2 {
		t.Fatalf("unexpected token char counts: ascii=%d nonASCII=%d", m.busyTokenASCIIChars, m.busyTokenNonASCIIChars)
	}
}
func TestResetTurnVisibilityClearsLiveTokenEstimate(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.recordAssistantDelta("hello 世界")

	m.resetTurnVisibility()

	if m.busyTokenCount != 0 || m.busyTokenASCIIChars != 0 || m.busyTokenNonASCIIChars != 0 {
		t.Fatalf("expected token estimate reset, got count=%d ascii=%d nonASCII=%d", m.busyTokenCount, m.busyTokenASCIIChars, m.busyTokenNonASCIIChars)
	}
}
func TestChatViewportTurnDoneUnfreezesScrolledLiveOutput(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	for i := 0; i < 40; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.beginTurnTranscript()
	m.startBusy()
	m.append("assistant", "live-head\n")
	m.refreshViewportContentFollow(true)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	m.append("assistant", "live-tail-after-scroll\n")

	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventTurnDone, LastResponse: "done"}))
	m = next.(model)
	if m.viewportFrozen {
		t.Fatal("expected turn completion to unfreeze chat viewport")
	}
	got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(got, "live-tail-after-scroll") {
		t.Fatalf("expected frozen live output to be committed on turn done:\n%s", got)
	}
}
func TestTurnDoneWhileScrolledFlushesNativeScrollbackImmediately(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	for i := 0; i < 40; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.nativeScrollbackPrinted = len(m.transcript)
	m.beginTurnTranscript()
	m.startBusy()
	m.append("assistant", "live-head\n")
	m.refreshViewportContentFollow(true)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	m.append("assistant", "tail while scrolled\n")
	cmd := m.handleTurnDone(protocol.Event{Kind: protocol.EventTurnDone, LastResponse: "done"})

	// The user's scroll position is preserved (we do not yank them to the tail)...
	if m.followTail {
		t.Fatal("expected turn completion to preserve user-scrolled position")
	}
	// ...but the completed turn is flushed to native scrollback immediately, so
	// the final answer is reachable by scrolling the terminal rather than hidden
	// until the next keystroke.
	if m.nativeScrollbackPrinted != len(m.transcript) {
		t.Fatal("expected turn completion to flush native scrollback even while scrolled")
	}
	if cmd == nil {
		t.Fatal("expected turn completion to return a native-scrollback flush command")
	}
	if got := fmt.Sprintf("%#v", cmd()); !strings.Contains(got, "tail while scrolled") {
		t.Fatalf("expected flushed native scrollback to include turn tail, got %s", got)
	}
}
func TestLongTurnDoneWhileScrolledPreservesViewportButFlushesDurationNotice(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	for i := 0; i < 40; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.nativeScrollbackPrinted = len(m.transcript)
	m.beginTurnTranscript()
	m.startBusy()
	m.busySince = time.Now().Add(-(3*time.Minute + 5*time.Second))
	m.append("assistant", "live-head\n")
	m.refreshViewportContentFollow(true)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	m.append("assistant", "tail while scrolled\n")
	cmd := m.handleTurnDone(protocol.Event{
		Kind:         protocol.EventTurnDone,
		LastResponse: "done",
		Metadata:     agentTurnMetadata(),
	})

	// Scroll position is preserved: the user is not yanked to the tail.
	if m.followTail {
		t.Fatal("expected long turn completion to preserve user-scrolled position")
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(rendered, "✻ Worked for 3m ") {
		t.Fatalf("expected duration notice to be appended to transcript:\n%s", rendered)
	}
	// But the completed long turn — including the duration notice — is flushed
	// to native scrollback immediately, not deferred until the user returns to
	// the tail.
	if m.nativeScrollbackPrinted != len(m.transcript) {
		t.Fatal("expected long turn completion to flush native scrollback even while scrolled")
	}
	if cmd == nil {
		t.Fatal("expected long turn completion to return a native-scrollback flush command")
	}
	if got := fmt.Sprintf("%#v", cmd()); !strings.Contains(got, "✻ Worked for 3m ") {
		t.Fatalf("expected flushed native scrollback to include duration notice, got %s", got)
	}
}
func TestTurnDoneReconciliationPreservesScrolledPosition(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	for i := 0; i < 40; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.nativeScrollbackPrinted = len(m.transcript)
	m.beginTurnTranscript()
	m.startBusy()

	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventAssistantDelta, Text: "visible assistant"}))
	m = next.(model)
	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventToolCall, ToolCallID: "tc-1", ToolName: "read_file", Text: `read_file: {"file_path":"internal/tui/model.go"}`}))
	m = next.(model)
	raw := `{"success":true,"data":{"status":"ok","metrics":{"returned_lines":24,"total_lines":100},"payload":{"file_path":"internal/tui/model.go","content":"package tui"}}}`
	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventToolResult, ToolCallID: "tc-1", ToolName: "read_file", Text: raw}))
	m = next.(model)

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	if !m.viewportFrozen || m.followTail {
		t.Fatalf("expected user scroll to freeze away from tail, frozen=%v follow=%v", m.viewportFrozen, m.followTail)
	}

	next, _ = m.Update(svcMsg(protocol.Event{
		Kind:         protocol.EventTurnDone,
		LastResponse: "visible assistant with final reconciliation",
		Metadata:     map[string]any{protocol.EventMetadataAgentTurn: true},
	}))
	m = next.(model)

	if m.followTail {
		t.Fatal("final reconciliation should not force scrolled chat back to tail")
	}
	if m.nativeScrollbackPrinted != len(m.transcript) {
		t.Fatal("expected reconciled turn output to flush to native scrollback even while scrolled")
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	prefixIx := strings.Index(rendered, "visible assistant")
	toolIx := strings.Index(rendered, "Read internal/tui/model.go")
	tailIx := strings.Index(rendered, "with final reconciliation")
	if prefixIx < 0 || toolIx < 0 || tailIx < 0 || !(prefixIx < toolIx && toolIx < tailIx) {
		t.Fatalf("expected final assistant tail after committed tool output:\n%s", rendered)
	}
}
func TestToolDeniedDoesNotAddNoFinalAnswerNotice(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 80, height: 24, busy: true}
	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventReasoningDelta, Text: "I should edit the file."}))
	m = next.(model)
	next, _ = m.Update(svcMsg(protocol.Event{
		Kind:       protocol.EventToolCall,
		ToolCallID: "tc-1",
		ToolName:   "edit",
		Text:       `edit: internal/tui/model.go`,
	}))
	m = next.(model)
	next, _ = m.Update(svcMsg(protocol.Event{
		Kind:       protocol.EventToolResult,
		ToolCallID: "tc-1",
		ToolName:   "edit",
		Text:       `{"success":false,"code":"approval_denied","message":"tool approval denied"}`,
	}))
	m = next.(model)
	next, _ = m.Update(svcMsg(protocol.Event{Kind: protocol.EventTurnDone}))
	m = next.(model)
	snap := m.assembler.Snapshot()
	for _, entry := range snap {
		if strings.Contains(entry.Text, "did not produce a visible answer") {
			t.Fatalf("unexpected reasoning-only status after tool denial: %+v", snap)
		}
	}
}
