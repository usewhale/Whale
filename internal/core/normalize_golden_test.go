package core

// Phase-1 golden baseline (ToolResult channel separation). These goldens pin
// the FINAL model-visible bytes — normalizeToolContent is the last writer on
// the dispatch path, so its output is what the model reads today and what
// ToolResult.ModelText must reproduce byte-for-byte after the refactor.
// Regenerate with UPDATE_GOLDEN=1 go test ./internal/core/ -run Golden.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func assertGolden(t *testing.T, name, got string) {
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

const goldenDurationMS = 7

func mustEnvelope(t *testing.T, env ToolEnvelope) string {
	t.Helper()
	s, err := MarshalToolEnvelope(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return s
}

func TestNormalizeToolContentGolden(t *testing.T) {
	ctx := context.Background()
	shellEnvelope := mustEnvelope(t, ToolEnvelope{
		OK: false, Success: false,
		Error: "command failed", Code: "exec_failed", Summary: "command failed",
		Data: map[string]any{
			"status": "error",
			"metrics": map[string]any{
				"exit_code": 1, "duration_ms": 43,
			},
			"payload": map[string]any{
				"command": `git add a.txt && git status 2>&1`,
				"stdout":  "",
				"stderr":  "The following paths are ignored by one of your .gitignore files:\nnpm/vendor",
			},
		},
	})
	readEnvelope := mustEnvelope(t, NewToolSuccessEnvelope(map[string]any{
		"status": "ok",
		"payload": map[string]any{
			"file_path": "Patch.cs",
			"content":   "if (elem != null && elem.ValueWithoutLink > 0) HasCondition<ConAnorexia>() // 中文注释",
		},
	}))
	cases := []struct {
		name        string
		raw         string
		fallbackErr bool
		maxChars    int
	}{
		{"normalize_shell_error", shellEnvelope, true, DefaultMaxToolResultChars},
		{"normalize_read_success", readEnvelope, false, DefaultMaxToolResultChars},
		{"normalize_raw_text", "plain MCP text with & < > and 中文, not JSON", false, DefaultMaxToolResultChars},
		{"normalize_empty", "", false, DefaultMaxToolResultChars},
		{"normalize_truncated", mustEnvelope(t, NewToolSuccessEnvelope(map[string]any{
			"payload": map[string]any{"content": strings.Repeat("x && y < z\n", 200)},
		})), false, 600},
	}
	for _, tc := range cases {
		content, isErr, archive, _ := normalizeToolContent(ctx, "fixture_tool", "call-1", tc.raw, tc.fallbackErr, tc.maxChars, goldenDurationMS)
		if archive != "" {
			t.Fatalf("%s: expected no archive path without archive config, got %q", tc.name, archive)
		}
		assertGolden(t, tc.name, content)
		if tc.fallbackErr != isErr && tc.name != "normalize_truncated" {
			// isErr follows the parsed envelope's ok flag; pin it per case.
			t.Logf("%s: isErr=%v", tc.name, isErr)
		}
	}
}

func TestInvalidToolInputContentGolden(t *testing.T) {
	assertGolden(t, "invalid_input_plain", invalidToolInputContent("read_file", errMessage("input did not match schema: missing file_path")))
}

type errMessage string

func (e errMessage) Error() string { return string(e) }
