package app

import (
	"context"
	"strings"
	"testing"

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
	if got := msgs[1]; !got.Hidden || !strings.Contains(got.Text, "active session mode is now plan") || !strings.Contains(got.Text, "changed from agent") || !strings.Contains(got.Text, "<proposed_plan>") {
		t.Fatalf("unexpected plan mode marker: %+v", got)
	}
}
