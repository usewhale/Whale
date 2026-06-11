package core

import "strings"
import "testing"
import "context"

func TestBoundedPayloadCapturesOriginalSize(t *testing.T) {
	inner, _ := MarshalToolEnvelope(NewToolSuccessEnvelope(map[string]any{
		"payload": map[string]any{"content": strings.Repeat("z", 5000)},
	}))
	reg := NewToolRegistry([]Tool{fixedContentTool{name: "fixture", content: inner}})
	reg.SetMaxResultChars(1500)
	res, err := reg.Dispatch(context.Background(), ToolCall{ID: "c1", Name: "fixture", Input: "{}"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	payload := res.Payload.(map[string]any)
	orig := int(payload["original_chars"].(float64))
	if orig <= 1500 {
		t.Fatalf("original_chars must reflect the FULL rendered size, got %d", orig)
	}
	head := payload["head"].(string)
	if strings.Contains(head, "...[output truncated") {
		t.Fatalf("head must come from the full text, not the truncated marker text: %q", head[:80])
	}
}
