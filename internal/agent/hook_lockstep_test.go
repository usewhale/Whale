package agent

import (
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func TestHookInjectionKeepsModelChannelInLockstep(t *testing.T) {
	// Funnel result (plain text model channel).
	res := core.NewToolResultSuccess(core.ToolCall{ID: "c1", Name: "shell_run"}, map[string]any{
		"payload": map[string]any{"stdout": "done"},
	}, nil)
	addHookContextToToolResult(&res, "pre-hook says check twice")
	if got := core.ToolResultModelText(res); !strings.Contains(got, "pre-hook says check twice") {
		t.Fatalf("PostToolUse payload would miss the pre-hook context: %q", got)
	}
	if res.Payload == nil || res.Outcome != core.OutcomeSuccess {
		t.Fatalf("structured channel lost: %+v", res)
	}

	// Bypass producer (envelope literal, no model channel yet): the
	// injection must derive the channel first, then land on it.
	bypass := core.ToolResult{
		ToolCallID: "c2", Name: "read_file",
		Content: `{"success":false,"error":"missing file","code":"not_found"}`,
		IsError: true,
	}
	addHookContextToToolResult(&bypass, "hook note")
	got := core.ToolResultModelText(bypass)
	if !strings.Contains(got, "hook note") || !strings.Contains(got, "error (not_found)") {
		t.Fatalf("bypass result must be rendered then injected: %q", got)
	}
	if bypass.Outcome != core.OutcomeFailure || bypass.Code != "not_found" {
		t.Fatalf("bypass classification lost: %+v", bypass)
	}
}
