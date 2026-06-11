package tools

// Phase-1 golden baseline: pins the pre-normalize bytes the toolset funnel
// helpers produce today. After the refactor the same helpers populate
// ToolResult.ModelText; the assertions flip from Content to ModelText with
// the golden files untouched — that diff is the byte-parity proof.
// Regenerate with UPDATE_GOLDEN=1.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func assertToolsGolden(t *testing.T, name, got string) {
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

func goldenCall() core.ToolCall {
	return core.ToolCall{ID: "call-7", Name: "fixture_tool", Input: "{}"}
}

func TestMarshalToolResultGolden(t *testing.T) {
	res, err := marshalToolResult(goldenCall(), map[string]any{
		"status": "ok",
		"payload": map[string]any{
			"content": "operators && < > survive, 中文 too",
		},
	})
	if err != nil {
		t.Fatalf("marshalToolResult: %v", err)
	}
	assertToolsGolden(t, "funnel_success_map", res.ModelText)

	res, err = marshalToolResult(goldenCall(), []string{"non", "map", "payload"})
	if err != nil {
		t.Fatalf("marshalToolResult non-map: %v", err)
	}
	assertToolsGolden(t, "funnel_success_nonmap", res.ModelText)
}

func TestMarshalToolErrorGolden(t *testing.T) {
	res := marshalToolError(goldenCall(), "exec_failed", "command failed: foo && bar")
	if !res.IsError() {
		t.Fatal("expected error result")
	}
	assertToolsGolden(t, "funnel_error_plain", res.ModelText)
}

func TestMarshalToolErrorWithRecoveryGolden(t *testing.T) {
	res := marshalToolErrorWithRecovery(goldenCall(), "search_not_found", "edit search text must exactly match", toolRecoveryHint{
		RecommendedNextTool: "read_file",
		RecommendedInput:    map[string]any{"file_path": "a&b.cs"},
		Retryable:           false,
		Reason:              "read the file again before retrying",
	})
	if !res.IsError() {
		t.Fatal("expected error result")
	}
	assertToolsGolden(t, "funnel_error_recovery", res.ModelText)
}
