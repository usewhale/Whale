package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/clipboard"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/store"
)

func TestCopyMessageAge(t *testing.T) {
	tests := []struct {
		line    string
		want    int
		wantErr string
	}{
		{line: "/copy", want: 0},
		{line: "/copy 1", want: 0},
		{line: "/copy 3", want: 2},
		{line: "/copy 0", wantErr: "Usage: /copy [N]"},
		{line: "/copy nope", wantErr: "Got: nope"},
		{line: "/copy 1 extra", wantErr: "Usage: /copy [N]"},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			got, err := copyMessageAge(tt.line)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("copyMessageAge(%q) = %d, want %d", tt.line, got, tt.want)
			}
		})
	}
}

func TestCollectRecentAssistantTexts(t *testing.T) {
	msgs := []core.Message{
		{Role: core.RoleAssistant, Text: "old"},
		{Role: core.RoleAssistant, Text: "hidden", Hidden: true},
		{Role: core.RoleAssistant, Text: "error", FinishReason: core.FinishReasonError},
		{Role: core.RoleUser, Text: "user"},
		{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "shell_run"}}},
		{Role: core.RoleAssistant, Text: "latest"},
	}
	got := collectRecentAssistantTexts(msgs, 20)
	want := []string{"latest", "old"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("collectRecentAssistantTexts = %#v, want %#v", got, want)
	}
}

func TestExecuteCopyCommandCopiesLatestAssistantMessage(t *testing.T) {
	app := newCopyTestApp(t)
	createCopyTestMessage(t, app, core.RoleAssistant, "first")
	createCopyTestMessage(t, app, core.RoleUser, "question")
	createCopyTestMessage(t, app, core.RoleAssistant, "second\nline")

	var copied string
	restore := stubCopyClipboard(t, &copied)
	defer restore()

	res, err := app.ExecuteLocalCommand("/copy")
	if err != nil {
		t.Fatalf("ExecuteLocalCommand: %v", err)
	}
	if !res.Handled {
		t.Fatal("expected /copy to be handled")
	}
	if copied != "second\nline" {
		t.Fatalf("copied = %q", copied)
	}
	if res.LocalResult == nil || res.LocalResult.Kind != "copy" {
		t.Fatalf("expected copy local result, got %#v", res.LocalResult)
	}
	for _, want := range []string{"Copied to clipboard", "Also written to /tmp/whale/response.md"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("copy output = %q, missing %q", res.Text, want)
		}
	}
}

func TestExecuteCopyCommandCopiesNthAssistantMessage(t *testing.T) {
	app := newCopyTestApp(t)
	createCopyTestMessage(t, app, core.RoleAssistant, "first")
	createCopyTestMessage(t, app, core.RoleAssistant, "second")

	var copied string
	restore := stubCopyClipboard(t, &copied)
	defer restore()

	res, err := app.ExecuteLocalCommand("/copy 2")
	if err != nil {
		t.Fatalf("ExecuteLocalCommand: %v", err)
	}
	if !res.Handled || copied != "first" {
		t.Fatalf("handled=%v copied=%q", res.Handled, copied)
	}
}

func TestExecuteCopyCommandReportsMissingAssistantMessage(t *testing.T) {
	app := newCopyTestApp(t)
	createCopyTestMessage(t, app, core.RoleUser, "question")

	res, err := app.ExecuteLocalCommand("/copy")
	if err != nil {
		t.Fatalf("ExecuteLocalCommand: %v", err)
	}
	if res.Text != "No assistant message to copy" {
		t.Fatalf("unexpected output: %q", res.Text)
	}
}

func TestExecuteCopyCommandReportsOutOfRangeIndex(t *testing.T) {
	app := newCopyTestApp(t)
	createCopyTestMessage(t, app, core.RoleAssistant, "only")

	_, err := app.ExecuteLocalCommand("/copy 2")
	if err == nil || !strings.Contains(err.Error(), "Only 1 assistant message available to copy") {
		t.Fatalf("expected out-of-range error, got %v", err)
	}
}

func newCopyTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	st, err := store.NewJSONLStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	return &App{ctx: context.Background(), sessionID: "copy-test", msgStore: st}
}

func createCopyTestMessage(t *testing.T, app *App, role core.Role, text string) {
	t.Helper()
	if _, err := app.msgStore.Create(context.Background(), core.Message{SessionID: app.sessionID, Role: role, Text: text}); err != nil {
		t.Fatalf("create message: %v", err)
	}
}

func stubCopyClipboard(t *testing.T, copied *string) func() {
	t.Helper()
	prev := copyClipboardText
	copyClipboardText = func(_ context.Context, text string, _ clipboard.Options) (clipboard.Result, error) {
		*copied = text
		return clipboard.Result{
			Chars:    len(text),
			Lines:    strings.Count(text, "\n") + 1,
			FilePath: "/tmp/whale/response.md",
			Native:   true,
			OSC52:    true,
		}, nil
	}
	return func() { copyClipboardText = prev }
}
