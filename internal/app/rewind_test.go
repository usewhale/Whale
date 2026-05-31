package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/checkpoint"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/store"
)

func TestExecuteRewindListsRecentUserMessages(t *testing.T) {
	app := newRewindTestApp(t)
	_, _ = app.msgStore.Create(context.Background(), core.Message{SessionID: app.sessionID, Role: core.RoleUser, Text: "first prompt"})
	_, _ = app.msgStore.Create(context.Background(), core.Message{SessionID: app.sessionID, Role: core.RoleAssistant, Text: "answer"})

	res, err := app.ExecuteLocalCommand("/rewind")
	if err != nil {
		t.Fatalf("ExecuteLocalCommand: %v", err)
	}
	if !res.Handled || res.LocalResult == nil {
		t.Fatalf("rewind was not handled with a local result: %+v", res)
	}
	if !strings.Contains(res.Text, "m-1") || !strings.Contains(res.Text, "first prompt") {
		t.Fatalf("rewind list text = %q", res.Text)
	}
}

func TestExecuteRewindWithArgumentShowsUsage(t *testing.T) {
	app := newRewindTestApp(t)
	target, _ := app.msgStore.Create(context.Background(), core.Message{SessionID: app.sessionID, Role: core.RoleUser, Text: "redo this"})

	res, err := app.ExecuteLocalCommand("/rewind " + target.ID)
	if err == nil || !strings.Contains(err.Error(), "usage: /rewind") {
		t.Fatalf("expected /rewind argument usage error, got res=%+v err=%v", res, err)
	}
	if !res.Handled {
		t.Fatalf("expected /rewind with args to be handled as usage error: %+v", res)
	}
}

func TestRewindToMessageRewritesSession(t *testing.T) {
	app := newRewindTestApp(t)
	_, _ = app.msgStore.Create(context.Background(), core.Message{SessionID: app.sessionID, Role: core.RoleUser, Text: "keep"})
	_, _ = app.msgStore.Create(context.Background(), core.Message{SessionID: app.sessionID, Role: core.RoleAssistant, Text: "old"})
	target, _ := app.msgStore.Create(context.Background(), core.Message{SessionID: app.sessionID, Role: core.RoleUser, Text: "redo this"})
	_, _ = app.msgStore.Create(context.Background(), core.Message{SessionID: app.sessionID, Role: core.RoleAssistant, Text: "remove"})

	restoreInput, err := app.RewindToMessage(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("RewindToMessage: %v", err)
	}
	if restoreInput != "redo this" {
		t.Fatalf("restore input = %q, want target prompt", restoreInput)
	}
	msgs, err := app.msgStore.List(context.Background(), app.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Text != "keep" || msgs[1].Text != "old" {
		t.Fatalf("messages after rewind = %+v", msgs)
	}
}

func TestExecuteRewindBothRestoresTrackedFiles(t *testing.T) {
	app := newRewindTestApp(t)
	target, _ := app.msgStore.Create(context.Background(), core.Message{SessionID: app.sessionID, Role: core.RoleUser, Text: "edit file"})
	_, _ = app.msgStore.Create(context.Background(), core.Message{SessionID: app.sessionID, Role: core.RoleAssistant, Text: "done"})

	path := filepath.Join(app.workspaceRoot, "file.txt")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := app.checkpoints.CreateSnapshot(app.sessionID, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := app.checkpoints.TrackFile(app.sessionID, target.ID, path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}

	restoreInput, err := app.RewindToMessage(context.Background(), target.ID)
	if err != nil {
		t.Fatalf("RewindToMessage: %v", err)
	}
	if restoreInput != "edit file" {
		t.Fatalf("restore input = %q, want target prompt", restoreInput)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "before" {
		t.Fatalf("file content = %q, want before", got)
	}
	msgs, err := app.msgStore.List(context.Background(), app.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("messages after rewinding first turn = %+v", msgs)
	}
}

func newRewindTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	st, err := store.NewJSONLStore(sessionsDir)
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	return &App{
		ctx:           context.Background(),
		sessionsDir:   sessionsDir,
		workspaceRoot: workspace,
		msgStore:      st,
		sessionID:     "sess-1",
		checkpoints:   checkpoint.NewManager(sessionsDir, workspace),
	}
}
