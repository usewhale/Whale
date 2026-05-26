package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type regTestTool struct{ name string }

func (t regTestTool) Name() string { return t.name }
func (t regTestTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "ok"}, nil
}
func (t regTestTool) Description() string { return "desc " + t.name }
func (t regTestTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": true}
}
func (t regTestTool) ReadOnly() bool { return true }

func TestToolRegistrySpecsAndDispatch(t *testing.T) {
	r := NewToolRegistry([]Tool{regTestTool{name: "read_file"}})
	specs := r.Specs()
	if len(specs) != 1 || specs[0].Name != "read_file" || !specs[0].ReadOnly {
		t.Fatalf("unexpected specs: %+v", specs)
	}
	res, err := r.Dispatch(context.Background(), ToolCall{ID: "tc-1", Name: "read_file"})
	if err != nil {
		t.Fatalf("dispatch err: %v", err)
	}
	if res.IsError || !strings.Contains(res.Content, `"ok":true`) || !strings.Contains(res.Content, `"source_tool":"read_file"`) {
		t.Fatalf("unexpected dispatch result: %+v", res)
	}
}

func TestToolRegistryDispatchNotFound(t *testing.T) {
	r := NewToolRegistry(nil)
	res, err := r.Dispatch(context.Background(), ToolCall{ID: "tc-1", Name: "missing"})
	if err != nil {
		t.Fatalf("dispatch err: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "not_found") {
		t.Fatalf("expected not_found error, got %+v", res)
	}
}

type badSpecTool struct{}

func (b badSpecTool) Name() string { return "bad_spec" }
func (b badSpecTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "ok"}, nil
}
func (b badSpecTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"x": map[string]any{"type": "string"}},
		"required":   []string{"missing"},
	}
}

func TestToolRegistryCheckedInvalidSpecReturnsError(t *testing.T) {
	if _, err := NewToolRegistryChecked([]Tool{badSpecTool{}}); err == nil {
		t.Fatal("expected invalid tool spec error")
	}
}

func TestToolRegistryReplaceToolsKeepsPreviousToolsOnInvalidSpec(t *testing.T) {
	r := NewToolRegistry([]Tool{regTestTool{name: "read_file"}})
	if err := r.ReplaceTools([]Tool{badSpecTool{}}); err == nil {
		t.Fatal("expected invalid tool spec error")
	}
	if got := r.Get("read_file"); got == nil {
		t.Fatal("expected previous tool to remain registered")
	}
	if got := r.Get("bad_spec"); got != nil {
		t.Fatal("invalid replacement should not be registered")
	}
}

type longOutputTool struct{}

func (l longOutputTool) Name() string { return "long_out" }
func (l longOutputTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Content:    strings.Repeat("a", 4096),
	}, nil
}

func TestToolRegistryTruncatesLargeResult(t *testing.T) {
	r := NewToolRegistry([]Tool{longOutputTool{}})
	r.SetMaxResultChars(2048)
	res, err := r.Dispatch(context.Background(), ToolCall{ID: "tc-1", Name: "long_out"})
	if err != nil {
		t.Fatalf("dispatch err: %v", err)
	}
	if len(res.Content) > 2048 {
		t.Fatalf("truncated result length = %d, want <= 2048: %s", len(res.Content), res.Content)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content), &out); err != nil {
		t.Fatalf("unexpected json: %v", err)
	}
	if res.IsError {
		t.Fatalf("truncated successful result should not be an error: %+v", res)
	}
	if out["ok"] != true || out["success"] != true || out["code"] != "ok" {
		t.Fatalf("truncated result should preserve success envelope, got: %s", res.Content)
	}
	if out["truncated"] != true {
		t.Fatalf("expected truncated payload, got: %s", res.Content)
	}
	data, ok := out["data"].(map[string]any)
	if !ok || data["head"] == "" || data["tail"] == "" {
		t.Fatalf("expected truncated head/tail data, got: %s", res.Content)
	}
	metadata, ok := out["metadata"].(map[string]any)
	if !ok || metadata["output_truncated"] != true {
		t.Fatalf("expected output_truncated metadata, got: %s", res.Content)
	}
}
