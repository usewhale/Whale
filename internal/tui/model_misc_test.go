package tui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/app/service"
	tuirender "github.com/usewhale/whale/internal/tui/render"
	"strings"
	"testing"
)

func agentTurnMetadata() map[string]any {
	return map[string]any{service.EventMetadataAgentTurn: true}
}
func newLongHistoryComposerModel(historyCount int, input string) model {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 12
	m.transcript = make([]tuirender.UIMessage, 0, historyCount)
	m.input.SetValue(input)
	for i := 0; i < historyCount; i++ {
		m.transcript = append(m.transcript, tuirender.UIMessage{
			Role: "info",
			Kind: tuirender.KindText,
			Text: fmt.Sprintf("entry-%04d", i),
		})
	}
	m.refreshViewportContentFollow(true)
	return m
}
func TestFilePanelNavigationSuppressesHistoryWhenNoMatches(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.input.SetValue("@missing")
	m.promptHistory = []string{"previous prompt"}
	m.inHistoryNav = true
	m.lastHistoryText = "@missing"
	m.files.active = true
	m.files.query = "missing"
	m.files.searching = false

	m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyUp})
	if got := m.input.Value(); got != "@missing" {
		t.Fatalf("expected visible file panel to keep history navigation suppressed, got %q", got)
	}
}
func TestSkillLoadedEventUpdatesStatusAndLogOnly(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.handleServiceEvent(service.Event{Kind: service.EventSkillLoaded, Text: "loaded skill: code-review"})

	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("skill loaded event should not add chat entries, got %d", got)
	}
	if m.status != "loaded skill: code-review" {
		t.Fatalf("expected skill loaded status, got %q", m.status)
	}
	if len(m.logs) != 1 {
		t.Fatalf("expected one log entry, got %+v", m.logs)
	}
	if got := m.logs[0]; got.Kind != "skill_loaded" || got.Source != "skills" || got.Summary != "loaded skill: code-review" {
		t.Fatalf("unexpected skill loaded log: %+v", got)
	}
}
func TestAutoAcceptInfoAppendsStructuredNotice(t *testing.T) {
	m := newModel(nil, "", "", "")
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventInfo, Text: "Session auto-accept enabled", AutoAccept: true, AutoAcceptKnown: true}))
	m = next.(model)
	if !m.autoAccept {
		t.Fatal("expected auto-accept state to update")
	}
	if m.assembler == nil {
		t.Fatal("expected assembler with permission notice")
	}
	snap := m.assembler.Snapshot()
	if len(snap) == 0 {
		t.Fatal("expected permission notice message")
	}
	got := snap[len(snap)-1]
	if got.Kind != tuirender.KindNotice || got.Notice == nil {
		t.Fatalf("expected structured permission notice, got: %+v", got)
	}
	if got.Text != "Session auto-accept enabled" || got.Notice.Kind != "permission_auto_accept_enabled" {
		t.Fatalf("unexpected permission notice: text=%q notice=%+v", got.Text, got.Notice)
	}
}
func TestHelpInfoRendersAsListInsteadOfCollapsedParagraph(t *testing.T) {
	m, _ := newModelWithDispatchSpy()
	m.width = 100
	m.height = 30

	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventLocalSubmitResult, Status: "info", Text: app.BuildHelpText()}))
	m = next.(model)

	got := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	if strings.Contains(got, "/agent Switch to agent mode /ask") {
		t.Fatalf("expected help commands to render as a list, got:\n%s", got)
	}
	for _, want := range []string{"Whale help", "Browse default commands:", "/agent", "Switch to agent mode", "/feedback", "For more help:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected help output to contain %q:\n%s", want, got)
		}
	}
}
func TestFinalAssistantDroppedTailPreservesToolOrder(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 100, height: 30, busy: true}
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventAssistantDelta, Text: "visible assistant"}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventToolCall,
		ToolCallID: "tc-read",
		ToolName:   "read_file",
		Text:       `read_file: {"file_path":"internal/tui/model.go"}`,
	}))
	m = next.(model)
	raw := `{"success":true,"data":{"status":"ok","metrics":{"returned_lines":24,"total_lines":100},"payload":{"file_path":"internal/tui/model.go","content":"package tui"}}}`
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventToolResult,
		ToolCallID: "tc-read",
		ToolName:   "read_file",
		Text:       raw,
	}))
	m = next.(model)

	next, _ = m.Update(svcMsg(service.Event{
		Kind:         service.EventTurnDone,
		LastResponse: "visible assistant recovered tail",
		Metadata:     map[string]any{service.EventMetadataAgentTurn: true},
	}))
	m = next.(model)

	rendered := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	prefixIx := strings.Index(rendered, "visible assistant")
	toolIx := strings.Index(rendered, "Read internal/tui/model.go")
	tailIx := strings.Index(rendered, "recovered tail")
	if prefixIx < 0 || toolIx < 0 || tailIx < 0 || !(prefixIx < toolIx && toolIx < tailIx) {
		t.Fatalf("expected dropped assistant tail after already committed tool output:\n%s", rendered)
	}
	if earlyFullIx := strings.Index(rendered, "visible assistant recovered tail"); earlyFullIx >= 0 && earlyFullIx < toolIx {
		t.Fatalf("final assistant text should not be moved before tool output:\n%s", rendered)
	}
}
func TestFinalAssistantNonPrefixReconciliationStaysAfterToolOutput(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 100, height: 30, busy: true}
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventAssistantDelta, Text: "visible pre "}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventToolCall,
		ToolCallID: "tc-read",
		ToolName:   "read_file",
		Text:       `read_file: {"file_path":"internal/tui/model.go"}`,
	}))
	m = next.(model)
	raw := `{"success":true,"data":{"status":"ok","metrics":{"returned_lines":24,"total_lines":100},"payload":{"file_path":"internal/tui/model.go","content":"package tui"}}}`
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventToolResult,
		ToolCallID: "tc-read",
		ToolName:   "read_file",
		Text:       raw,
	}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{Kind: service.EventAssistantDelta, Text: "visible later"}))
	m = next.(model)

	next, _ = m.Update(svcMsg(service.Event{
		Kind:         service.EventTurnDone,
		LastResponse: "visible pre missing middle visible later final tail",
		Metadata:     map[string]any{service.EventMetadataAgentTurn: true},
	}))
	m = next.(model)

	rendered := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	toolIx := strings.Index(rendered, "Read internal/tui/model.go")
	finalIx := strings.Index(rendered, "visible pre missing middle visible later final tail")
	if toolIx < 0 || finalIx < 0 || toolIx > finalIx {
		t.Fatalf("expected non-prefix reconciled assistant after tool output:\n%s", rendered)
	}
	if earlyFullIx := strings.Index(rendered, "visible pre missing middle"); earlyFullIx >= 0 && earlyFullIx < toolIx {
		t.Fatalf("final assistant text should not replace the committed pre-tool assistant:\n%s", rendered)
	}
}
func TestFinalAssistantFailedCommittedReplacementDoesNotMutateTranscript(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 100, height: 30, busy: true}
	next, _ := m.Update(svcMsg(service.Event{Kind: service.EventAssistantDelta, Text: "visible pre"}))
	m = next.(model)
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventToolCall,
		ToolCallID: "tc-read",
		ToolName:   "read_file",
		Text:       `read_file: {"file_path":"internal/tui/model.go"}`,
	}))
	m = next.(model)
	raw := `{"success":true,"data":{"status":"ok","metrics":{"returned_lines":24,"total_lines":100},"payload":{"file_path":"internal/tui/model.go","content":"package tui"}}}`
	next, _ = m.Update(svcMsg(service.Event{
		Kind:       service.EventToolResult,
		ToolCallID: "tc-read",
		ToolName:   "read_file",
		Text:       raw,
	}))
	m = next.(model)

	next, _ = m.Update(svcMsg(service.Event{
		Kind:         service.EventTurnDone,
		LastResponse: "different final response",
		Metadata:     map[string]any{service.EventMetadataAgentTurn: true},
	}))
	m = next.(model)

	rendered := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	prefixIx := strings.Index(rendered, "visible pre")
	toolIx := strings.Index(rendered, "Read internal/tui/model.go")
	finalIx := strings.Index(rendered, "different final response")
	if prefixIx < 0 || toolIx < 0 || finalIx < 0 || !(prefixIx < toolIx && toolIx < finalIx) {
		t.Fatalf("expected original assistant, tool, then appended final response:\n%s", rendered)
	}
	if strings.Count(rendered, "different final response") != 1 {
		t.Fatalf("expected final response to be appended exactly once:\n%s", rendered)
	}
}
func TestCtrlUKillsSingleLineComposerDoesNotScrollTranscript(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.transcript = nil
	for i := 0; i < 30; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.refreshViewportContentFollow(true)
	m.handleViewportScrollKey("home")
	m.input.SetValue("clear me")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = next.(model)
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected Ctrl+U to clear composer, got %q", got)
	}
	if m.viewport.YOffset != 0 {
		t.Fatalf("expected Ctrl+U not to scroll transcript, offset=%d", m.viewport.YOffset)
	}
}
func TestCtrlDDeletesComposerCharDoesNotScrollTranscript(t *testing.T) {
	// PR 2 removed the chat-mode interception of Ctrl+D for transcript
	// half-page-down. Ctrl+D should now reach the textarea's
	// DeleteCharacterForward via the standard composer dispatch path.
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 8
	m.transcript = nil
	for i := 0; i < 30; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.refreshViewportContentFollow(true)
	m.handleViewportScrollKey("home")
	initialOffset := m.viewport.YOffset
	m.input.SetValue("abc")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA}) // cursor → line start
	m = next.(model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = next.(model)
	if got := m.input.Value(); got != "bc" {
		t.Fatalf("expected Ctrl+D to delete first char, got %q", got)
	}
	if m.viewport.YOffset != initialOffset {
		t.Fatalf("expected Ctrl+D not to scroll transcript, offset=%d want %d", m.viewport.YOffset, initialOffset)
	}
}
func TestLongHistoryComposerEditsStayIncrementalAtTail(t *testing.T) {
	m := newLongHistoryComposerModel(600, "seed")
	initialGeneration := m.chat.generation
	initialItems := len(m.chat.items)

	for _, msg := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("a")},
		{Type: tea.KeyRunes, Runes: []rune("b")},
		{Type: tea.KeyBackspace},
		{Type: tea.KeyCtrlU},
	} {
		next, _ := m.Update(msg)
		m = next.(model)
		if m.chat.generation != initialGeneration {
			t.Fatalf("expected long-history edit %v not to rerender chat, gen=%d want=%d", msg.Type, m.chat.generation, initialGeneration)
		}
		if len(m.chat.items) != initialItems {
			t.Fatalf("expected long-history edit %v to keep chat item count stable, got %d want %d", msg.Type, len(m.chat.items), initialItems)
		}
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected Ctrl+U to clear composer after long-history edits, got %q", got)
	}
	if !m.followTail || !m.chat.AtBottom() || !m.viewport.AtBottom() {
		t.Fatalf("expected long-history tail edits to keep latest content visible, follow=%v chatBottom=%v viewportBottom=%v", m.followTail, m.chat.AtBottom(), m.viewport.AtBottom())
	}
	if view := m.View(); !strings.Contains(view, "entry-0599") {
		t.Fatalf("expected long-history tail view to keep latest entry visible:\n%s", view)
	}
}
func TestLongHistoryComposerEditsStayIncrementalOffTail(t *testing.T) {
	m := newLongHistoryComposerModel(600, "seed")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	if m.followTail {
		t.Fatal("expected PageUp to leave tail-follow mode for long history")
	}
	initialGeneration := m.chat.generation
	initialItems := len(m.chat.items)
	initialOffset := m.viewport.YOffset

	for _, msg := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("a")},
		{Type: tea.KeyBackspace},
		{Type: tea.KeyCtrlU},
	} {
		next, _ = m.Update(msg)
		m = next.(model)
		if m.chat.generation != initialGeneration {
			t.Fatalf("expected off-tail long-history edit %v not to rerender chat, gen=%d want=%d", msg.Type, m.chat.generation, initialGeneration)
		}
		if len(m.chat.items) != initialItems {
			t.Fatalf("expected off-tail long-history edit %v to keep chat item count stable, got %d want %d", msg.Type, len(m.chat.items), initialItems)
		}
		if m.viewport.YOffset != initialOffset {
			t.Fatalf("expected off-tail long-history edit %v not to move chat offset, got %d want %d", msg.Type, m.viewport.YOffset, initialOffset)
		}
	}
	if got := m.input.Value(); got != "" {
		t.Fatalf("expected Ctrl+U to clear composer after off-tail long-history edits, got %q", got)
	}
	if m.followTail {
		t.Fatal("expected off-tail long-history edits not to resume tail following")
	}
	if view := m.View(); strings.Contains(view, "entry-0599") {
		t.Fatalf("expected off-tail long-history edits not to jump back to the latest tail:\n%s", view)
	}
}
func TestComposerHeightGrowthAtTailUpdatesLayoutWithoutRerender(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	m.input.SetValue("seed")
	for i := 0; i < 60; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%02d", i))
	}
	m.refreshViewportContentFollow(true)
	mainWidth, _ := m.layoutDims()
	initialBodyHeight := m.viewportBodyHeight(mainWidth)
	initialGeneration := m.chat.generation

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	m = next.(model)
	mainWidth, _ = m.layoutDims()
	if got := m.viewportBodyHeight(mainWidth); got >= initialBodyHeight {
		t.Fatalf("expected composer growth to reduce chat body height, got %d want < %d", got, initialBodyHeight)
	}
	if got := m.input.Value(); got != "seed\n" {
		t.Fatalf("expected ctrl+j to add newline, got %q", got)
	}
	if m.chat.generation != initialGeneration {
		t.Fatalf("expected composer growth not to rerender chat, gen=%d want=%d", m.chat.generation, initialGeneration)
	}
	if !m.followTail || !m.chat.AtBottom() || !m.viewport.AtBottom() {
		t.Fatalf("expected tail-follow layout sync to keep latest content visible, follow=%v chatBottom=%v viewportBottom=%v", m.followTail, m.chat.AtBottom(), m.viewport.AtBottom())
	}
	if view := m.View(); !strings.Contains(view, "entry-59") {
		t.Fatalf("expected tail view to keep latest entry visible after composer growth:\n%s", view)
	}
}
func TestShiftEnterKeyInsertsNewline(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.input.SetValue("seed")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("shift+enter")})
	m = next.(model)

	if got := m.input.Value(); got != "seed\n" {
		t.Fatalf("expected shift+enter to add newline, got %q", got)
	}
}
func TestComposerHeightShrinkOffTailClampsLayoutWithoutRerender(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10
	m.transcript = nil
	m.input.SetValue("seed\n")
	for i := 0; i < 120; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("entry-%03d", i))
	}
	m.refreshViewportContentFollow(true)
	tailOffset := m.viewport.YOffset

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = next.(model)
	if m.followTail {
		t.Fatal("expected PageUp to leave tail-follow mode")
	}
	mainWidth, _ := m.layoutDims()
	initialBodyHeight := m.viewportBodyHeight(mainWidth)
	initialGeneration := m.chat.generation
	initialOffset := m.viewport.YOffset

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(model)
	mainWidth, _ = m.layoutDims()
	if got := m.viewportBodyHeight(mainWidth); got <= initialBodyHeight {
		t.Fatalf("expected composer shrink to increase chat body height, got %d want > %d", got, initialBodyHeight)
	}
	if got := m.input.Value(); got != "seed" {
		t.Fatalf("expected backspace to remove trailing newline, got %q", got)
	}
	if m.chat.generation != initialGeneration {
		t.Fatalf("expected composer shrink not to rerender chat, gen=%d want=%d", m.chat.generation, initialGeneration)
	}
	if m.followTail {
		t.Fatal("expected off-tail layout sync not to resume tail following")
	}
	if m.viewport.YOffset > initialOffset {
		t.Fatalf("expected off-tail layout sync only to clamp offset, got %d want <= %d", m.viewport.YOffset, initialOffset)
	}
	if m.viewport.YOffset >= tailOffset {
		t.Fatalf("expected off-tail layout sync not to jump back to tail, got %d tail=%d", m.viewport.YOffset, tailOffset)
	}
}
func containsString(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
}
