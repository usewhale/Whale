package tui

import (
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/runtime/protocol"
)

func TestHydratedPlanUpdateFromStructuredPayload(t *testing.T) {
	// Phase-2 result: Content is plain rendered text; the plan lives in
	// the structured payload.
	tr := protocol.ToolResult{
		Name:    "update_plan",
		Content: "ok\ndata: {...}",
		Outcome: "success",
		Payload: map[string]any{
			"explanation": "two-step plan",
			"plan": []any{
				map[string]any{"step": "read the file", "status": "completed"},
				map[string]any{"step": "edit the file", "status": "in_progress"},
			},
		},
	}
	text, ok := hydratedPlanUpdateFromResult(tr)
	if !ok {
		t.Fatal("expected plan update to hydrate from structured payload")
	}
	for _, want := range []string{"two-step plan", "[x] read the file", "[~] edit the file"} {
		if !strings.Contains(text, want) {
			t.Fatalf("plan text missing %q:\n%s", want, text)
		}
	}
}

func TestHydratedPlanUpdateLegacyTextFallback(t *testing.T) {
	tr := protocol.ToolResult{
		Name:    "update_plan",
		Content: `{"success":true,"data":{"explanation":"legacy","plan":[{"step":"do thing","status":"pending"}]}}`,
	}
	text, ok := hydratedPlanUpdateFromResult(tr)
	if !ok {
		t.Fatal("expected legacy plan update to hydrate via text fallback")
	}
	if !strings.Contains(text, "[ ] do thing") || !strings.Contains(text, "legacy") {
		t.Fatalf("unexpected legacy plan text: %q", text)
	}
}
