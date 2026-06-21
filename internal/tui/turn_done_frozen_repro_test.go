package tui

import (
	"fmt"
	"github.com/usewhale/whale/internal/runtime/protocol"
	"strings"
	"testing"
	"time"

	tuirender "github.com/usewhale/whale/internal/tui/render"
)

// TestTurnDoneWhileFrozenFlushesFinalToScrollback guards the fix for the
// "看起来突然停下来" symptom. Reproduction scenario: the user scrolls up during
// a long turn, which freezes the chat viewport and clears followTail.
//
// Before the fix, the turn-completion path persisted the final assistant
// answer + the "✻ Worked for ..." notice into the transcript but neither
// scrolled them into the in-app viewport NOR flushed them to native
// scrollback — the native flush is gated on the same followTail/!frozen state.
// The final answer was therefore stranded off-screen until the next keystroke
// ran resumeChatTail.
//
// The chosen fix (codex-aligned) keeps the user's scroll position untouched —
// we do not yank a reading user back to the tail — but always emits the
// completed turn into the terminal's native scrollback, so the final answer is
// immediately reachable by scrolling the terminal and is never hidden.
func TestTurnDoneWhileFrozenFlushesFinalToScrollback(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 10 // small viewport so the transcript overflows it

	// A tall history so "top" and "bottom" are genuinely different positions.
	for i := 0; i < 40; i++ {
		m.appendTranscript("assistant", tuirender.KindText, fmt.Sprintf("history line %d", i))
	}
	m.nativeScrollbackPrinted = len(m.transcript)
	m.refreshViewportContentFollow(true) // lay out and anchor at the bottom
	if !m.chat.AtBottom() {
		t.Fatalf("precondition: expected to start anchored at the bottom")
	}

	// Turn begins; user scrolls up mid-turn. This is the end state produced by
	// pressing PgUp while busy (history_nav.go): viewport frozen, followTail
	// cleared, list scrolled away from the bottom.
	m.beginTurnTranscript()
	m.busy = true
	m.busySince = time.Now().Add(-2 * time.Minute) // long turn -> notice will append
	m.freezeChatViewport()
	m.followTail = false
	m.chat.ScrollToTop()
	if m.chat.AtBottom() {
		t.Fatalf("precondition: expected to be scrolled up before the turn completes")
	}

	// The turn completes normally with a final answer (end_turn).
	cmd := m.handleTurnDone(protocol.Event{
		Kind:         protocol.EventTurnDone,
		LastResponse: "THE FINAL ANSWER — 要继续完成 planState 集成吗？",
		Metadata:     agentTurnMetadata(),
	})

	// The data is intact: the final answer (and the notice) are in the transcript.
	full := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if !strings.Contains(full, "THE FINAL ANSWER") {
		t.Fatalf("final answer must be persisted in the transcript:\n%s", full)
	}
	if !strings.Contains(full, "Worked for") {
		t.Fatalf("expected the turn-duration notice in the transcript:\n%s", full)
	}

	// Scroll position is preserved: the reading user is NOT yanked to the tail.
	if m.followTail {
		t.Fatalf("turn completion must preserve the user-scrolled position (followTail)")
	}

	// But the completed turn is flushed to native scrollback immediately — the
	// fix. nativeScrollbackPrinted advances and the returned command prints the
	// final answer to the terminal scrollback.
	if m.nativeScrollbackPrinted != len(m.transcript) {
		t.Fatalf("turn completion must flush the final answer to native scrollback even while scrolled")
	}
	if cmd == nil {
		t.Fatalf("turn completion must return a native-scrollback flush command")
	}
	found := false
	for _, m := range cmdResults(cmd) {
		if strings.Contains(fmt.Sprintf("%#v", m), "要继续完成 planState") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("flushed native scrollback must contain the final answer, got: %v", cmdResults(cmd))
	}
}
