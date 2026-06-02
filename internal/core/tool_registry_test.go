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

func TestWebFetchMaxResultsReturnsRecoveryHint(t *testing.T) {
	reg := NewToolRegistry([]Tool{snapshotTestTool{
		name: "web_fetch",
		params: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"url":        map[string]any{"type": "string"},
				"prompt":     map[string]any{"type": "string"},
				"timeout_ms": map[string]any{"type": "integer"},
			},
			"required": []string{"url", "prompt"},
		},
		content: `{"ok":true}`,
	}})

	res, err := reg.Dispatch(context.Background(), ToolCall{
		ID:    "tc-web-fetch",
		Name:  "web_fetch",
		Input: `{"url":"https://example.com","prompt":"summarize","max_results":5}`,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected invalid input error, got %+v", res)
	}
	for _, want := range []string{
		`"code":"invalid_input"`,
		`unknown field \"max_results\"`,
		"web_fetch does not support max_results; remove it or use web_search when you need multiple search results.",
		`"recovery"`,
	} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("result missing %q:\n%s", want, res.Content)
		}
	}
}

func TestFetchFormatReturnsRecoveryHint(t *testing.T) {
	reg := NewToolRegistry([]Tool{snapshotTestTool{
		name: "fetch",
		params: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"url":        map[string]any{"type": "string"},
				"prompt":     map[string]any{"type": "string"},
				"timeout_ms": map[string]any{"type": "integer"},
			},
			"required": []string{"url", "prompt"},
		},
		content: `{"ok":true}`,
	}})

	res, err := reg.Dispatch(context.Background(), ToolCall{
		ID:    "tc-fetch",
		Name:  "fetch",
		Input: `{"url":"https://example.com","prompt":"summarize","format":"text"}`,
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected invalid input error, got %+v", res)
	}
	for _, want := range []string{
		`"code":"invalid_input"`,
		`unknown field \"format\"`,
		"fetch does not support format; omit it and use prompt to request the output shape.",
		`"recovery"`,
	} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("result missing %q:\n%s", want, res.Content)
		}
	}
}

func TestFetchFileURLReturnsRecoveryHint(t *testing.T) {
	for _, msg := range []string{
		`url scheme must be http or https`,
		`valid url is required`,
	} {
		content := invalidToolInputContent("fetch", errString(msg))
		for _, want := range []string{
			`"code":"invalid_input"`,
			msg,
			"fetch only supports http/https URLs; use read_file for local file paths or tool result files.",
			`"recovery"`,
		} {
			if !strings.Contains(content, want) {
				t.Fatalf("result for %q missing %q:\n%s", msg, want, content)
			}
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
