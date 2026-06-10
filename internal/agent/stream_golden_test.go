package agent

// Phase-1 golden baseline for agent-level model-visible text producers:
// the tool-call-cap blocked result and the hook-context injection (the one
// sanctioned post-production Content mutation). Regenerate with
// UPDATE_GOLDEN=1.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func assertAgentGolden(t *testing.T, name, got string) {
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

func TestToolCallCapBlockedResultGolden(t *testing.T) {
	res := toolCallCapBlockedResult(core.ToolCall{ID: "cap-1", Name: "shell_run"})
	if !res.IsError {
		t.Fatal("expected error result")
	}
	assertAgentGolden(t, "cap_blocked", res.Content)
}

func TestAddHookContextToToolContentGolden(t *testing.T) {
	envelope, err := core.MarshalToolEnvelope(core.NewToolSuccessEnvelope(map[string]any{
		"payload": map[string]any{"stdout": "a && b < c"},
	}))
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	once := addHookContextToToolContent(envelope, "hook says: check & verify")
	assertAgentGolden(t, "hook_injected_once", once)

	twice := addHookContextToToolContent(once, "second hook <note>")
	assertAgentGolden(t, "hook_injected_twice", twice)

	raw := addHookContextToToolContent("plain non-envelope text", "hook says: check & verify")
	assertAgentGolden(t, "hook_injected_raw_text", raw)

	unchanged := addHookContextToToolContent(envelope, "   ")
	if unchanged != envelope {
		t.Fatal("blank hook context must leave content untouched")
	}
}
