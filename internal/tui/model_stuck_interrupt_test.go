package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/usewhale/whale/internal/runtime/protocol"
)

// Reproduces session 019ec77f: after a long turn (131 messages) the user
// pressed Esc/Ctrl+C to interrupt, IntentShutdown was dispatched, but the
// hung LLM call never produced EventTurnDone — so m.busy stays true. From
// that state the user reported being unable to do anything ("关不掉，然后
// 旧没法输入任何指令了"). This test hammers Ctrl+C from the stuck state and
// asserts the user can eventually request exit. On the buggy code every
// Ctrl+C re-enters interruptBusyTurn(), which resets quitArmedUntil, so the
// double-Ctrl+C quit flow is never reachable and no IntentRequestExit ever
// fires.
func TestStuckStoppingTurnCanStillQuit(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	// Simulate the hung-after-interrupt state: the shutdown intent was
	// already dispatched, busy never cleared because EventTurnDone is stuck.
	m.busy = true
	m.stopping = true

	// User keeps mashing Ctrl+C trying to get out.
	for i := 0; i < 5; i++ {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		m = next.(model)
	}

	sawExit := false
	for _, in := range *intents {
		if in.Kind == protocol.IntentRequestExit {
			sawExit = true
		}
	}
	if !sawExit {
		t.Fatalf("user is trapped: repeated Ctrl+C in a stuck stopping turn never produced IntentRequestExit; intents=%+v", *intents)
	}
}

// Second half of the same report: "然后没法输入任何指令了" — in the stuck
// busy state, typing a slash command and pressing Enter never runs it (it is
// blocked/queued while busy), so the user can neither quit nor act. This
// documents that the only escape hatch must be Ctrl+C, which Bug A traps.
func TestStuckStoppingTurnBlocksCommandInput(t *testing.T) {
	m, intents := newModelWithDispatchSpy()
	m.busy = true
	m.stopping = true

	m = typeRunesForTest(t, m, "/clear")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)

	for _, in := range *intents {
		if in.Kind == protocol.IntentSubmit || in.Kind == protocol.IntentSubmitLocal {
			t.Fatalf("expected command input to be blocked while stuck-busy, but it dispatched %+v", in)
		}
	}
}
