package tasks

// Phase-1 golden baseline for the tasks-package result helpers; see
// internal/tools/toolset_golden_test.go for the migration contract.
// Regenerate with UPDATE_GOLDEN=1.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func assertTasksGolden(t *testing.T, name, got string) {
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

func TestTasksMarshalHelpersGolden(t *testing.T) {
	call := core.ToolCall{ID: "task-1", Name: "spawn_subagent", Input: "{}"}

	res, err := marshalSuccess(call, map[string]any{
		"role":    "explore",
		"summary": "found a && b in <file>",
	})
	if err != nil {
		t.Fatalf("marshalSuccess: %v", err)
	}
	assertTasksGolden(t, "tasks_success", res.Content)

	res, err = marshalError(call, "subagent_failed", "child exited: signal & detail")
	if err != nil {
		t.Fatalf("marshalError: %v", err)
	}
	assertTasksGolden(t, "tasks_error", res.Content)

	res, err = marshalErrorWithData(call, "subagent_failed", "child exited", map[string]any{
		"child_session_id": "abc",
		"stderr":           "panic: x < y",
	})
	if err != nil {
		t.Fatalf("marshalErrorWithData: %v", err)
	}
	assertTasksGolden(t, "tasks_error_data", res.Content)
}
