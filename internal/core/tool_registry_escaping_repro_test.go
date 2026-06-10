package core

// Session 019ead56 follow-up: the first escaping fix covered
// MarshalToolEnvelope, but the agent's live path is
// DispatchWithProgress -> normalizeRegistryResult -> normalizeToolContent,
// which re-parses every tool result and re-marshals it with plain
// json.Marshal — re-introducing the & < > HTML escapes on the
// final model-visible bytes. The earlier repro tests called Toolset methods
// directly and bypassed this layer, which is how the gap survived.

import (
	"context"
	"strings"
	"testing"
)

type fixedContentTool struct {
	name    string
	content string
}

func (t fixedContentTool) Name() string               { return t.name }
func (t fixedContentTool) Parameters() map[string]any { return nil }
func (t fixedContentTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: t.content}, nil
}

func TestDispatchedResultKeepsOperatorsVerbatim(t *testing.T) {
	inner, err := MarshalToolEnvelope(NewToolSuccessEnvelope(map[string]any{
		"payload": map[string]any{
			"content": `if (elem != null && elem.ValueWithoutLink > 0) HasCondition<ConAnorexia>()`,
			"command": `where ilspy 2>&1 & dotnet tool list -g`,
		},
	}))
	if err != nil {
		t.Fatalf("marshal inner envelope: %v", err)
	}
	reg := NewToolRegistry([]Tool{fixedContentTool{name: "fixture", content: inner}})

	res, err := reg.Dispatch(context.Background(), ToolCall{ID: "c1", Name: "fixture", Input: "{}"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	for _, esc := range []string{`\u0026`, `\u003c`, `\u003e`} {
		if strings.Contains(res.Content, esc) {
			t.Errorf("dispatched content contains literal %s; the normalize layer must not re-escape payload text", esc)
		}
	}
	if !strings.Contains(res.Content, "elem != null && elem.ValueWithoutLink > 0") {
		t.Errorf("expected C# operators verbatim after dispatch, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, `2>&1`) {
		t.Errorf("expected shell redirection verbatim after dispatch, got:\n%s", res.Content)
	}
}

func TestDispatchedOversizedResultKeepsOperatorsVerbatim(t *testing.T) {
	// The truncation fallback path (normalizeToolContent's "short" map)
	// serializes separately and must not re-escape either.
	big := strings.Repeat("x && y < z > w\n", 8000)
	inner, err := MarshalToolEnvelope(NewToolSuccessEnvelope(map[string]any{
		"payload": map[string]any{"content": big},
	}))
	if err != nil {
		t.Fatalf("marshal inner envelope: %v", err)
	}
	reg := NewToolRegistry([]Tool{fixedContentTool{name: "fixture", content: inner}})

	res, err := reg.Dispatch(context.Background(), ToolCall{ID: "c1", Name: "fixture", Input: "{}"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	for _, esc := range []string{`\u0026`, `\u003c`, `\u003e`} {
		if strings.Contains(res.Content, esc) {
			t.Errorf("truncated dispatched content contains literal %s", esc)
		}
	}
}
