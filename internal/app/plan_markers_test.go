package app

import (
	"context"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/store"
)

func TestEnsureCurrentModeMarkerRecordsInitialModeAndDeduplicates(t *testing.T) {
	msgStore, err := store.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	app := &App{
		msgStore:    msgStore,
		sessionID:   "s-mode-marker",
		currentMode: session.ModeAgent,
	}

	if err := app.ensureCurrentModeMarker(); err != nil {
		t.Fatalf("ensureCurrentModeMarker initial: %v", err)
	}
	msgs, err := msgStore.List(context.Background(), app.sessionID)
	if err != nil {
		t.Fatalf("List initial: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages after initial ensure = %d, want 1: %+v", len(msgs), msgs)
	}
	if got := msgs[0]; !got.Hidden || got.Role != "user" || !strings.Contains(got.Text, "active session mode is now agent") || !strings.Contains(got.Text, "Agent mode instruction") {
		t.Fatalf("unexpected initial mode marker: %+v", got)
	}
	if got := msgs[0].Text; !strings.Contains(got, "execute the user's current request") || strings.Contains(got, "current goal") {
		t.Fatalf("agent marker should describe the current request without goal wording: %q", got)
	}

	if err := app.ensureCurrentModeMarker(); err != nil {
		t.Fatalf("ensureCurrentModeMarker duplicate: %v", err)
	}
	msgs, err = msgStore.List(context.Background(), app.sessionID)
	if err != nil {
		t.Fatalf("List duplicate: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("duplicate ensure should not append marker, got %d messages: %+v", len(msgs), msgs)
	}

	app.currentMode = session.ModePlan
	if err := app.ensureCurrentModeMarker(); err != nil {
		t.Fatalf("ensureCurrentModeMarker mode change: %v", err)
	}
	msgs, err = msgStore.List(context.Background(), app.sessionID)
	if err != nil {
		t.Fatalf("List mode change: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages after mode change = %d, want 2: %+v", len(msgs), msgs)
	}
	if got := msgs[1]; !got.Hidden || !strings.Contains(got.Text, "active session mode is now plan") || !strings.Contains(got.Text, "changed from agent") || !strings.Contains(got.Text, "How to present the plan") || !strings.Contains(got.Text, "taken as your proposed plan") {
		t.Fatalf("unexpected plan mode marker: %+v", got)
	}
}

func TestEnsureCurrentModeMarkerAppendsPlanReminderPerTurn(t *testing.T) {
	msgStore, err := store.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	app := &App{
		msgStore:    msgStore,
		sessionID:   "s-plan-reminder",
		currentMode: session.ModePlan,
	}
	ctx := context.Background()

	// First ensure records the mode marker; no reminder needed because the
	// full instruction is already the newest message.
	if err := app.ensureCurrentModeMarker(); err != nil {
		t.Fatalf("ensureCurrentModeMarker initial: %v", err)
	}
	msgs, err := msgStore.List(ctx, app.sessionID)
	if err != nil {
		t.Fatalf("List initial: %v", err)
	}
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text, "<mode_changed>") {
		t.Fatalf("messages after initial ensure = %+v, want single mode marker", msgs)
	}

	// A retried ensure with the marker still newest must not stack a reminder.
	if err := app.ensureCurrentModeMarker(); err != nil {
		t.Fatalf("ensureCurrentModeMarker retry: %v", err)
	}
	if msgs, _ = msgStore.List(ctx, app.sessionID); len(msgs) != 1 {
		t.Fatalf("retry should not append, got %d messages", len(msgs))
	}

	appendUserTurn := func(text string) {
		t.Helper()
		if _, err := msgStore.Create(ctx, core.Message{SessionID: app.sessionID, Role: core.RoleUser, Text: text}); err != nil {
			t.Fatalf("Create user message: %v", err)
		}
	}

	// Later turns each get one reminder; the first four are sparse, the fifth full.
	for i := 1; i <= planModeReminderFullEvery; i++ {
		appendUserTurn("turn input")
		if err := app.ensureCurrentModeMarker(); err != nil {
			t.Fatalf("ensureCurrentModeMarker turn %d: %v", i, err)
		}
		msgs, err = msgStore.List(ctx, app.sessionID)
		if err != nil {
			t.Fatalf("List turn %d: %v", i, err)
		}
		got := msgs[len(msgs)-1]
		if !got.Hidden || got.Role != "user" || !strings.Contains(got.Text, planModeReminderOpenTag) {
			t.Fatalf("turn %d: newest message is not a plan reminder: %+v", i, got)
		}
		wantFull := i%planModeReminderFullEvery == 0
		isFull := strings.Contains(got.Text, "Plan mode instruction:")
		if isFull != wantFull {
			t.Fatalf("turn %d: reminder full = %v, want %v: %q", i, isFull, wantFull, got.Text)
		}
		// A retried ensure with the reminder still newest must not stack another.
		if err := app.ensureCurrentModeMarker(); err != nil {
			t.Fatalf("ensureCurrentModeMarker retry turn %d: %v", i, err)
		}
		after, _ := msgStore.List(ctx, app.sessionID)
		if len(after) != len(msgs) {
			t.Fatalf("turn %d: retry appended a duplicate reminder", i)
		}
	}

	// Reminders stop once the session leaves plan mode.
	app.currentMode = session.ModeAgent
	if err := app.ensureCurrentModeMarker(); err != nil {
		t.Fatalf("ensureCurrentModeMarker back to agent: %v", err)
	}
	appendUserTurn("agent turn")
	if err := app.ensureCurrentModeMarker(); err != nil {
		t.Fatalf("ensureCurrentModeMarker agent turn: %v", err)
	}
	msgs, _ = msgStore.List(ctx, app.sessionID)
	got := msgs[len(msgs)-1]
	if strings.Contains(got.Text, planModeReminderOpenTag) {
		t.Fatalf("agent mode turn appended a plan reminder: %+v", got)
	}
}

func TestEnsureCurrentModeMarkerRefreshesStalePlanInstructionAtTail(t *testing.T) {
	msgStore, err := store.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	app := &App{
		msgStore:    msgStore,
		sessionID:   "s-plan-stale-instruction",
		currentMode: session.ModePlan,
	}
	ctx := context.Background()

	staleMarker := "<mode_changed>\nThe active session mode is now plan, changed from agent.\n\nPlan mode instruction: prepare a plan and wait.\n</mode_changed>"
	if _, err := msgStore.Create(ctx, core.Message{
		SessionID:    app.sessionID,
		Role:         core.RoleUser,
		Text:         staleMarker,
		Hidden:       true,
		FinishReason: core.FinishReasonEndTurn,
	}); err != nil {
		t.Fatalf("Create stale marker: %v", err)
	}

	if err := app.ensureCurrentModeMarker(); err != nil {
		t.Fatalf("ensureCurrentModeMarker stale marker: %v", err)
	}
	msgs, err := msgStore.List(ctx, app.sessionID)
	if err != nil {
		t.Fatalf("List stale marker: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages after stale marker refresh = %d, want 2: %+v", len(msgs), msgs)
	}
	got := msgs[1]
	if !got.Hidden || !strings.Contains(got.Text, planModeReminderOpenTag) || !strings.Contains(got.Text, "How to present the plan") || !strings.Contains(got.Text, "taken as your proposed plan") {
		t.Fatalf("stale marker should be followed by current full reminder: %+v", got)
	}

	if err := app.ensureCurrentModeMarker(); err != nil {
		t.Fatalf("ensureCurrentModeMarker retry: %v", err)
	}
	after, err := msgStore.List(ctx, app.sessionID)
	if err != nil {
		t.Fatalf("List retry: %v", err)
	}
	if len(after) != len(msgs) {
		t.Fatalf("retry after current reminder appended duplicate, got %d messages", len(after))
	}
}

func TestEnsureCurrentModeMarkerRefreshesStalePlanInstructionBehindVisibleMessages(t *testing.T) {
	msgStore, err := store.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	app := &App{
		msgStore:    msgStore,
		sessionID:   "s-plan-stale-instruction-behind-visible",
		currentMode: session.ModePlan,
	}
	ctx := context.Background()

	staleMarker := "<mode_changed>\nThe active session mode is now plan, changed from agent.\n\nPlan mode instruction: prepare a plan and wait.\n</mode_changed>"
	if _, err := msgStore.Create(ctx, core.Message{
		SessionID:    app.sessionID,
		Role:         core.RoleUser,
		Text:         staleMarker,
		Hidden:       true,
		FinishReason: core.FinishReasonEndTurn,
	}); err != nil {
		t.Fatalf("Create stale marker: %v", err)
	}
	if _, err := msgStore.Create(ctx, core.Message{
		SessionID: app.sessionID,
		Role:      core.RoleUser,
		Text:      "continue planning",
	}); err != nil {
		t.Fatalf("Create visible user message: %v", err)
	}

	if err := app.ensureCurrentModeMarker(); err != nil {
		t.Fatalf("ensureCurrentModeMarker stale marker behind visible: %v", err)
	}
	msgs, err := msgStore.List(ctx, app.sessionID)
	if err != nil {
		t.Fatalf("List stale marker behind visible: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("messages after stale marker refresh = %d, want 3: %+v", len(msgs), msgs)
	}
	got := msgs[2]
	if !got.Hidden || !strings.Contains(got.Text, planModeReminderOpenTag) || !strings.Contains(got.Text, "How to present the plan") || !strings.Contains(got.Text, "taken as your proposed plan") {
		t.Fatalf("stale non-tail marker should force current full reminder: %+v", got)
	}
}
