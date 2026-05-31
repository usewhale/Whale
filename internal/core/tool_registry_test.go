package core

import (
	"context"
	"strings"
	"testing"
)

type snapshotTestTool struct {
	name    string
	content string
	params  map[string]any
}

func (t snapshotTestTool) Name() string { return t.name }
func (t snapshotTestTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: t.content}, nil
}
func (t snapshotTestTool) Parameters() map[string]any { return t.params }

func TestToolRegistrySnapshotIsStableAfterReplace(t *testing.T) {
	reg := NewToolRegistry([]Tool{snapshotTestTool{name: "old", content: "old-ok"}})
	snap := reg.Snapshot()

	if err := reg.ReplaceTools([]Tool{snapshotTestTool{name: "new", content: "new-ok"}}); err != nil {
		t.Fatalf("replace tools: %v", err)
	}

	if got := snap.Get("old"); got == nil {
		t.Fatal("snapshot lost old tool")
	}
	if got := snap.Get("new"); got != nil {
		t.Fatal("snapshot should not include replacement tool")
	}
	res, err := snap.Dispatch(context.Background(), ToolCall{ID: "tc-old", Name: "old"})
	if err != nil {
		t.Fatalf("dispatch snapshot tool: %v", err)
	}
	if res.IsError || !strings.Contains(res.Content, "old-ok") {
		t.Fatalf("unexpected snapshot dispatch result: %+v", res)
	}
}

func TestSearchFilesUnknownIncludeReturnsRecoveryHint(t *testing.T) {
	reg := NewToolRegistry([]Tool{snapshotTestTool{
		name: "search_files",
		params: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string"},
				"limit":   map[string]any{"type": "integer"},
			},
			"required": []string{"pattern"},
		},
		content: `{"ok":true}`,
	}})

	res, err := reg.Dispatch(context.Background(), ToolCall{
		ID:    "tc-search",
		Name:  "search_files",
		Input: `{"pattern":"version","include":"*.go","limit":20}`,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected invalid input error, got %+v", res)
	}
	for _, want := range []string{
		`"code":"invalid_input"`,
		`unknown field \"include\"`,
		"search_files does not support include; retry with grep for content search or remove include.",
		`"recovery"`,
	} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("result missing %q:\n%s", want, res.Content)
		}
	}
}

func TestSearchFilesMissingPatternReturnsRecoveryHint(t *testing.T) {
	reg := NewToolRegistry([]Tool{snapshotTestTool{
		name: "search_files",
		params: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string"},
				"limit":   map[string]any{"type": "integer"},
			},
			"required": []string{"pattern"},
		},
		content: `{"ok":true}`,
	}})

	res, err := reg.Dispatch(context.Background(), ToolCall{
		ID:    "tc-search",
		Name:  "search_files",
		Input: `{"limit":20}`,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected invalid input error, got %+v", res)
	}
	for _, want := range []string{
		`"code":"invalid_input"`,
		`missing required field \"pattern\"`,
		"search_files requires pattern; provide pattern and path, or use grep for content search.",
		`"recovery"`,
	} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("result missing %q:\n%s", want, res.Content)
		}
	}
}
