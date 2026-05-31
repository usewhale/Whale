package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/runtime/protocol"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

// collectPrintsFromCmd executes a tea.Cmd and gathers the text of any
// tea.Println (printLineMessage) it emits, recursing through Batch/Sequence
// message slices. It deliberately does NOT recurse into runtime wait commands
// (the driven test never produces them).
func collectPrintsFromCmd(cmd tea.Cmd, out *[]string) {
	if cmd == nil {
		return
	}
	collectPrintsFromMsg(cmd(), out)
}

func collectPrintsFromMsg(msg tea.Msg, out *[]string) {
	if msg == nil {
		return
	}
	rv := reflect.ValueOf(msg)
	if rv.Kind() == reflect.Slice {
		for i := 0; i < rv.Len(); i++ {
			if c, ok := rv.Index(i).Interface().(tea.Cmd); ok {
				collectPrintsFromCmd(c, out)
			}
		}
		return
	}
	s := fmt.Sprintf("%#v", msg)
	if strings.Contains(s, "printLineMessage") || strings.Contains(s, "messageBody") {
		*out = append(*out, s)
	}
}

// driveServiceBatch mimics handleServiceUpdate for one batch of events without
// the blocking waitEventCmd: it runs the events, then flushes native scrollback
// the same way the real update loop does, collecting all printed text.
func driveServiceBatch(m *model, prints *[]string, events ...protocol.Event) {
	eventCmd, _, _ := m.handleServiceEvents(events)
	headerCmd := m.startupHeaderPrintCmd()
	scrollbackCmd := m.flushNativeScrollbackCmd()
	collectPrintsFromCmd(eventCmd, prints)
	collectPrintsFromCmd(headerCmd, prints)
	collectPrintsFromCmd(scrollbackCmd, prints)
}

// TestFinalAnswerStaysVisibleInViewportAtTail reproduces the user's screenshot
// (Path B): the user is at the tail (followTail, NOT scrolled/frozen — the
// "✻ Worked for" notice is visible, which only happens at the bottom), yet the
// final answer is missing right above it.
//
// Root cause: at followTail the in-app viewport renders only
// transcript[nativeScrollbackPrinted:]. The old turn-done path flushed the
// finished answer to native scrollback AND advanced nativeScrollbackPrinted to
// the end, so View() shows nothing for the finished turn — the answer lives
// ONLY in the terminal's native scrollback (tea.Println). On terminals where
// inline Println is unreliable (Windows conpty / some tmux) the answer then
// vanishes from the screen entirely.
//
// The fix keeps the just-completed turn in the in-app viewport until the next
// user turn, so the answer is always rendered by View() regardless of whether
// Println landed.
func TestFinalAnswerStaysVisibleInViewportAtTail(t *testing.T) {
	const finalAnswer = "## ✅ 工作完成总结\nFINAL_ANSWER_MARKER table rows here"

	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 12
	m.viewMode = protocol.ViewModeFocus

	for i := 0; i < 20; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("history-%02d", i))
	}
	m.nativeScrollbackPrinted = len(m.transcript)

	m.beginTurnTranscript()
	m.startBusy()
	m.busySince = time.Now().Add(-(3*time.Minute + 16*time.Second))

	var prints []string
	for i := 0; i < 3; i++ {
		driveServiceBatch(&m, &prints, protocol.Event{Kind: protocol.EventAssistantDelta, Text: fmt.Sprintf("step %d analysis\n", i)})
		driveServiceBatch(&m, &prints, protocol.Event{Kind: protocol.EventToolCall, ToolCallID: fmt.Sprintf("c%d", i), ToolName: "read_file", Text: "Read internal/fscanner/scanner.go"})
		driveServiceBatch(&m, &prints, protocol.Event{Kind: protocol.EventToolResult, ToolCallID: fmt.Sprintf("c%d", i), ToolName: "read_file", Text: "...file contents..."})
	}
	driveServiceBatch(&m, &prints, protocol.Event{Kind: protocol.EventAssistantDelta, Text: finalAnswer})
	driveServiceBatch(&m, &prints, protocol.Event{
		Kind:         protocol.EventTurnDone,
		LastResponse: finalAnswer,
		Metadata:     agentTurnMetadata(),
	})

	// Precondition: we never scrolled, so we must still be following the tail.
	if !m.followTail || m.viewportFrozen {
		t.Fatalf("precondition: expected to remain at the tail (followTail=%v frozen=%v)", m.followTail, m.viewportFrozen)
	}

	// The in-app viewport (what View() renders) must contain the final answer —
	// not only the terminal's native scrollback.
	viewport := strings.Join(tuirender.ChatLines(m.chatViewportMessages(), 80), "\n")
	if !strings.Contains(viewport, "FINAL_ANSWER_MARKER") {
		t.Fatalf("final answer not visible in in-app viewport at tail (the bug).\nViewport:\n%s", viewport)
	}
}

// TestHeldFinalAnswerSurvivesResize guards against the resize regression: when
// a completed turn is held in the in-app viewport at the tail and the user
// resizes the terminal before submitting again, the resize-driven scrollback
// replay must NOT sink the held answer out of View() (which would re-expose the
// vanishing-final-answer behavior on terminals where tea.Println is flaky).
func TestHeldFinalAnswerSurvivesResize(t *testing.T) {
	const finalAnswer = "## ✅ 工作完成总结\nFINAL_ANSWER_MARKER table rows here"

	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 12
	m.viewMode = protocol.ViewModeFocus
	m.sizeMsgReceived = true // so the next size change counts as a real resize

	for i := 0; i < 20; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("history-%02d", i))
	}
	m.nativeScrollbackPrinted = len(m.transcript)

	m.beginTurnTranscript()
	m.startBusy()
	m.busySince = time.Now().Add(-(3*time.Minute + 16*time.Second))

	var prints []string
	for i := 0; i < 3; i++ {
		driveServiceBatch(&m, &prints, protocol.Event{Kind: protocol.EventAssistantDelta, Text: fmt.Sprintf("step %d analysis\n", i)})
		driveServiceBatch(&m, &prints, protocol.Event{Kind: protocol.EventToolCall, ToolCallID: fmt.Sprintf("c%d", i), ToolName: "read_file", Text: "Read internal/fscanner/scanner.go"})
		driveServiceBatch(&m, &prints, protocol.Event{Kind: protocol.EventToolResult, ToolCallID: fmt.Sprintf("c%d", i), ToolName: "read_file", Text: "...file contents..."})
	}
	driveServiceBatch(&m, &prints, protocol.Event{Kind: protocol.EventAssistantDelta, Text: finalAnswer})
	driveServiceBatch(&m, &prints, protocol.Event{
		Kind:         protocol.EventTurnDone,
		LastResponse: finalAnswer,
		Metadata:     agentTurnMetadata(),
	})

	if !m.holdCompletedTurnInViewport {
		t.Fatalf("precondition: expected the completed turn to be held in the in-app viewport")
	}

	// The user resizes the terminal before submitting another prompt.
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = next.(model)

	// The held answer must still be rendered by View() (the in-app viewport),
	// not trimmed out by the resize replay.
	if !m.holdCompletedTurnInViewport {
		t.Fatalf("resize must not release the in-app hold on the completed turn")
	}
	viewport := strings.Join(tuirender.ChatLines(m.chatViewportMessages(), m.width), "\n")
	if !strings.Contains(viewport, "FINAL_ANSWER_MARKER") {
		t.Fatalf("held final answer vanished from the in-app viewport after resize (the bug).\nViewport:\n%s", viewport)
	}
}

// TestHeldFinalAnswerSurvivesFocusToggle guards against the focus-toggle
// regression: while a completed turn is held in the in-app viewport at the
// tail, toggling focus mode (ctrl+o) must not push the held answer back onto
// the unreliable native-print path and trim it out of View().
func TestHeldFinalAnswerSurvivesFocusToggle(t *testing.T) {
	const finalAnswer = "## ✅ 工作完成总结\nFINAL_ANSWER_MARKER table rows here"

	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 12
	m.viewMode = protocol.ViewModeFocus

	for i := 0; i < 20; i++ {
		m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("history-%02d", i))
	}
	m.nativeScrollbackPrinted = len(m.transcript)

	m.beginTurnTranscript()
	m.startBusy()
	m.busySince = time.Now().Add(-(3*time.Minute + 16*time.Second))

	var prints []string
	for i := 0; i < 3; i++ {
		driveServiceBatch(&m, &prints, protocol.Event{Kind: protocol.EventAssistantDelta, Text: fmt.Sprintf("step %d analysis\n", i)})
		driveServiceBatch(&m, &prints, protocol.Event{Kind: protocol.EventToolCall, ToolCallID: fmt.Sprintf("c%d", i), ToolName: "read_file", Text: "Read internal/fscanner/scanner.go"})
		driveServiceBatch(&m, &prints, protocol.Event{Kind: protocol.EventToolResult, ToolCallID: fmt.Sprintf("c%d", i), ToolName: "read_file", Text: "...file contents..."})
	}
	driveServiceBatch(&m, &prints, protocol.Event{Kind: protocol.EventAssistantDelta, Text: finalAnswer})
	driveServiceBatch(&m, &prints, protocol.Event{
		Kind:         protocol.EventTurnDone,
		LastResponse: finalAnswer,
		Metadata:     agentTurnMetadata(),
	})

	if !m.holdCompletedTurnInViewport {
		t.Fatalf("precondition: expected the completed turn to be held in the in-app viewport")
	}

	// User toggles focus mode (ctrl+o -> default) before submitting again.
	m.viewMode = protocol.ViewModeDefault
	_ = m.redrawTranscriptForFocusToggleCmd()

	viewport := strings.Join(tuirender.ChatLines(m.chatViewportMessages(), m.width), "\n")
	if !strings.Contains(viewport, "FINAL_ANSWER_MARKER") {
		t.Fatalf("held final answer vanished from the in-app viewport after focus toggle (the bug).\nViewport:\n%s", viewport)
	}
}

// TestFinalAnswerReachesNativeScrollbackInFocusMode guards the command-level
// data path behind the user's "任务执行着就忽然结束了 / 终答没渲染出来" report: a long,
// tool-heavy turn in focus view mode (the boxed "Explored" layout in the
// screenshot) completes. The completed turn is held in the in-app viewport at
// the tail, and is sunk to native scrollback (tea.Println) once the next turn
// starts — never lost.
//
// The three sub-cases cover how DeepSeek can deliver the terminating content:
//   - streamed: full answer arrives as assistant deltas (happy path)
//   - lastResponseOnly: no deltas survive; answer only in TurnDone.LastResponse
//     (synthesized EventComplete delta dropped under UI backpressure)
//   - partialThenLastResponse: a prefix streams, the tail only lands in
//     LastResponse
func TestFinalAnswerReachesNativeScrollbackInFocusMode(t *testing.T) {
	const finalAnswer = "## ✅ 工作完成总结\nFINAL_ANSWER_MARKER table rows here"

	cases := []struct {
		name        string
		streamDelta string // assistant delta emitted for the final segment ("" = none)
	}{
		{"streamed", finalAnswer},
		{"lastResponseOnly", ""},
		{"partialThenLastResponse", "## ✅ 工作完成总结\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(nil, "", "", "")
			m.width = 80
			m.height = 10
			m.viewMode = protocol.ViewModeFocus

			// Some prior history already flushed to native scrollback.
			for i := 0; i < 20; i++ {
				m.appendTranscript("info", tuirender.KindText, fmt.Sprintf("history-%02d", i))
			}
			m.nativeScrollbackPrinted = len(m.transcript)

			m.beginTurnTranscript()
			m.startBusy()
			m.busySince = time.Now().Add(-(3*time.Minute + 16*time.Second))

			var prints []string

			// A few rounds of: short assistant text -> tool call -> tool result.
			for i := 0; i < 3; i++ {
				driveServiceBatch(&m, &prints, protocol.Event{Kind: protocol.EventAssistantDelta, Text: fmt.Sprintf("step %d analysis\n", i)})
				driveServiceBatch(&m, &prints,
					protocol.Event{Kind: protocol.EventToolCall, ToolCallID: fmt.Sprintf("c%d", i), ToolName: "read_file", Text: "Read internal/fscanner/scanner.go"},
				)
				driveServiceBatch(&m, &prints,
					protocol.Event{Kind: protocol.EventToolResult, ToolCallID: fmt.Sprintf("c%d", i), ToolName: "read_file", Text: "...file contents..."},
				)
			}

			// The final segment, then end_turn carrying the authoritative answer.
			if tc.streamDelta != "" {
				driveServiceBatch(&m, &prints, protocol.Event{Kind: protocol.EventAssistantDelta, Text: tc.streamDelta})
			}
			driveServiceBatch(&m, &prints, protocol.Event{
				Kind:         protocol.EventTurnDone,
				LastResponse: finalAnswer,
				Metadata:     agentTurnMetadata(),
			})

			// At the tail the completed turn is held in-app, not yet flushed to
			// native scrollback.
			if !m.holdCompletedTurnInViewport {
				t.Fatalf("expected completed turn to be held in the in-app viewport at the tail")
			}
			viewport := strings.Join(tuirender.ChatLines(m.chatViewportMessages(), 80), "\n")
			if !strings.Contains(viewport, "FINAL_ANSWER_MARKER") {
				t.Fatalf("final answer must be visible in the in-app viewport at the tail.\nViewport:\n%s", viewport)
			}

			// When the next turn starts, the held turn sinks to native scrollback
			// (tea.Println) — the answer is preserved, never lost.
			m.beginTurnTranscript()
			driveServiceBatch(&m, &prints, protocol.Event{Kind: protocol.EventAssistantDelta, Text: "next turn begins\n"})

			all := strings.Join(prints, "\n")
			if !strings.Contains(all, "Worked for") {
				t.Fatalf("duration notice should sink to native scrollback on the next turn; prints:\n%s", all)
			}
			if !strings.Contains(all, "FINAL_ANSWER_MARKER") {
				t.Fatalf("final answer must sink to native scrollback on the next turn.\nNative scrollback prints:\n%s", all)
			}
		})
	}
}
