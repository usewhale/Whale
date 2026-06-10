package store

// Phase-1 legacy fixture: a session file written by the CURRENT schema
// (ToolResult carries Content + IsError). After the schema change, loading
// this exact file must yield, for every tool result, ModelText equal to the
// original Content bytes and a derived Outcome — the fixture is the
// regression oracle for the legacy decoder. Regenerate (rarely!) with
// UPDATE_GOLDEN=1; the assertions recompute expected envelopes via the same
// production marshal helpers, so they hold across regeneration.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

const legacyFixtureSession = "legacy-fixture-session"

func legacyFixtureMessages(t *testing.T) []core.Message {
	t.Helper()
	successEnvelope, err := core.MarshalToolEnvelope(core.NewToolSuccessEnvelope(map[string]any{
		"payload": map[string]any{"content": "operators && < > and 中文 survive"},
	}))
	if err != nil {
		t.Fatalf("marshal success envelope: %v", err)
	}
	errorEnvelope, err := core.MarshalToolEnvelope(core.NewToolErrorEnvelope("exec_failed", "command failed: a & b"))
	if err != nil {
		t.Fatalf("marshal error envelope: %v", err)
	}
	return []core.Message{
		{SessionID: legacyFixtureSession, Role: core.RoleUser, Text: "run the fixture"},
		{SessionID: legacyFixtureSession, Role: core.RoleAssistant, Text: "running", ToolCalls: []core.ToolCall{
			{ID: "tc-ok", Name: "read_file", Input: `{"file_path":"a.cs"}`},
			{ID: "tc-err", Name: "shell_run", Input: `{"command":"false"}`},
			{ID: "tc-raw", Name: "mcp_tool", Input: `{}`},
			{ID: "tc-empty", Name: "noop", Input: `{}`},
		}},
		{SessionID: legacyFixtureSession, Role: core.RoleTool, ToolResults: []core.ToolResult{
			{ToolCallID: "tc-ok", Name: "read_file", Content: successEnvelope},
			{ToolCallID: "tc-err", Name: "shell_run", Content: errorEnvelope, IsError: true},
			{ToolCallID: "tc-raw", Name: "mcp_tool", Content: "raw non-envelope text & <tags>"},
			{ToolCallID: "tc-empty", Name: "noop", Content: ""},
		}},
	}
}

func TestLegacySessionFixtureRoundTrip(t *testing.T) {
	dir := filepath.Join("testdata", "legacy_session")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatalf("clean fixture dir: %v", err)
		}
		st, err := NewJSONLStore(dir)
		if err != nil {
			t.Fatalf("new store: %v", err)
		}
		for _, m := range legacyFixtureMessages(t) {
			if _, err := st.Create(context.Background(), m); err != nil {
				t.Fatalf("create fixture message: %v", err)
			}
		}
		return
	}

	st, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("open fixture store: %v", err)
	}
	msgs, err := st.List(context.Background(), legacyFixtureSession)
	if err != nil {
		t.Fatalf("list fixture session: %v", err)
	}
	want := legacyFixtureMessages(t)
	if len(msgs) != len(want) {
		t.Fatalf("fixture message count: got %d want %d (legacy decoder must never drop lines)", len(msgs), len(want))
	}
	var results []core.ToolResult
	for _, m := range msgs {
		results = append(results, m.ToolResults...)
	}
	wantResults := want[2].ToolResults
	if len(results) != len(wantResults) {
		t.Fatalf("tool result count: got %d want %d", len(results), len(wantResults))
	}
	for i, got := range results {
		// Phase-1 invariant: the model-visible text loaded from a legacy
		// file is byte-identical to what was written. (After the schema
		// change this assertion reads got.ModelText.)
		if got.Content != wantResults[i].Content {
			t.Errorf("result %d (%s): model-visible text drifted:\nwant: %q\ngot:  %q", i, got.ToolCallID, wantResults[i].Content, got.Content)
		}
		if got.IsError != wantResults[i].IsError {
			t.Errorf("result %d (%s): error flag drifted: got %v want %v", i, got.ToolCallID, got.IsError, wantResults[i].IsError)
		}
	}
}
