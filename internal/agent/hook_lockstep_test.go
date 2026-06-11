package agent

import (
	"context"
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
		ModelText: `{"success":false,"error":"missing file","code":"not_found"}`,
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

func TestBypassResultFinalizedBeforePostHook(t *testing.T) {
	// The PostToolUse payload must see the same shape the system persists:
	// finalized structured fields and rendered text, not raw legacy JSON.
	res := core.ToolResult{
		ToolCallID: "c1", Name: "read_file",
		ModelText: `{"success":false,"error":"missing file","code":"not_found"}`,
	}
	finalized := core.FinalizeToolResultChannels(res)
	payload := NewPostToolUsePayload("s1", core.ToolCall{ID: "c1", Name: "read_file"}, nil, finalized)
	if payload.ToolOutcome != string(core.OutcomeFailure) || payload.ToolErrorCode != "not_found" {
		t.Fatalf("hook payload missing structured fields: %+v", payload)
	}
	if !strings.HasPrefix(payload.ToolResult, "error (not_found)") {
		t.Fatalf("hook payload should carry rendered text, got %q", payload.ToolResult)
	}
}

func TestCanceledRecoveryBackoffResultIsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, ok := waitRecoveryBackoff(ctx, core.ToolCall{ID: "c1", Name: "shell_run"}, RecoveryRule{
		Action: RecoveryActionRetryWithBackoff, BackoffMS: 50,
	})
	if !ok {
		t.Fatal("expected backoff wait to report cancellation")
	}
	if !res.IsError() || res.Outcome != core.OutcomeCancelled {
		t.Fatalf("canceled backoff result must be error-class, got %+v", res)
	}
	if toolResultUsableAfterCancel(res) {
		t.Fatal("canceled backoff result must not be treated as usable")
	}
}
