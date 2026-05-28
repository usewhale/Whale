package core

import (
	"context"
	"strings"
	"testing"
)

type snapshotTestTool struct {
	name    string
	content string
}

func (t snapshotTestTool) Name() string { return t.name }
func (t snapshotTestTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: t.content}, nil
}

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
