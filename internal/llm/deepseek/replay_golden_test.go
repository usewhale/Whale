package deepseek

// Phase-1 golden baseline for the provider-bound message build: the exact
// content strings the model receives, including the ToolResultReplayContent
// truncation of an oversized result. After the refactor toDeepSeekMessages
// reads ToolResult.ModelText; this golden must not change.
// Regenerate with UPDATE_GOLDEN=1.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func assertReplayGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (regenerate with UPDATE_GOLDEN=1)", path, err)
	}
	if string(want) != got {
		t.Fatalf("golden mismatch for %s:\nwant: %s\ngot:  %s", name, want, got)
	}
}

func replayFixtureHistory(t *testing.T) []core.Message {
	t.Helper()
	smallEnvelope, err := core.MarshalToolEnvelope(core.NewToolSuccessEnvelope(map[string]any{
		"payload": map[string]any{
			"content": "if (elem != null && elem.ValueWithoutLink > 0) HasCondition<ConAnorexia>()",
			"command": "where ilspy 2>&1 & dotnet tool list -g",
		},
	}))
	if err != nil {
		t.Fatalf("marshal small envelope: %v", err)
	}
	bigEnvelope, err := core.MarshalToolEnvelope(core.NewToolSuccessEnvelope(map[string]any{
		"payload": map[string]any{"content": strings.Repeat("long output && more <data>\n", 2000)},
	}))
	if err != nil {
		t.Fatalf("marshal big envelope: %v", err)
	}
	return []core.Message{
		{Role: core.RoleSystem, Text: "You are a test fixture & nothing more."},
		{Role: core.RoleUser, Text: "read the file with <operators>"},
		{Role: core.RoleAssistant, Text: "reading", ToolCalls: []core.ToolCall{
			{ID: "tc-1", Name: "read_file", Input: `{"file_path":"a&b.cs"}`},
		}},
		{Role: core.RoleTool, ToolResults: []core.ToolResult{
			{ToolCallID: "tc-1", Name: "read_file", Content: smallEnvelope},
		}},
		{Role: core.RoleAssistant, Text: "now the big one", ToolCalls: []core.ToolCall{
			{ID: "tc-2", Name: "shell_run", Input: `{"command":"cat big.log 2>&1"}`},
		}},
		{Role: core.RoleTool, ToolResults: []core.ToolResult{
			{ToolCallID: "tc-2", Name: "shell_run", Content: bigEnvelope},
		}},
		{Role: core.RoleTool, ToolResults: []core.ToolResult{
			{ToolCallID: "tc-raw", Name: "mcp_tool", Content: "raw non-envelope text & <tags>"},
		}},
	}
}

func TestToDeepSeekMessagesGolden(t *testing.T) {
	msgs := toDeepSeekMessages(replayFixtureHistory(t))
	blob, err := core.MarshalToolJSON(msgs)
	if err != nil {
		t.Fatalf("marshal provider messages: %v", err)
	}
	assertReplayGolden(t, "deepseek_messages", string(blob))

	// The oversized result must have gone through replay truncation —
	// guard that the fixture actually exercises that path.
	joined := string(blob)
	if !strings.Contains(joined, "tool result compacted for model replay") {
		t.Fatal("fixture did not trigger ToolResultReplayContent truncation")
	}
}
