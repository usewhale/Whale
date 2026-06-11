package core

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestNewToolResultFromEnvelopeRendersPlainText(t *testing.T) {
	env := ToolEnvelope{
		OK: false, Success: false,
		Error: "command failed", Code: "exec_failed", Summary: "command failed",
		Data: map[string]any{
			"metrics": map[string]any{"exit_code": 1, "duration_ms": 43},
			"payload": map[string]any{"stderr": "a && b < c"},
		},
	}
	res := NewToolResultFromEnvelope(ToolCall{ID: "c1", Name: "shell_run"}, env, nil)
	if !strings.HasPrefix(res.ModelText, "exit 1 (43ms)") {
		t.Fatalf("expected plain-text shell header, got:\n%s", res.ModelText)
	}
	if !strings.Contains(res.ModelText, "a && b < c") {
		t.Fatalf("stderr must appear verbatim, got:\n%s", res.ModelText)
	}
	if strings.Contains(res.ModelText, `"success"`) || strings.Contains(res.ModelText, `\u0026`) {
		t.Fatalf("model text must not contain envelope scaffolding or escapes:\n%s", res.ModelText)
	}
	if res.Content != res.ModelText {
		t.Fatalf("Content mirrors ModelText, got %s", res.Content)
	}
	if res.Outcome != OutcomeFailure || res.Code != "exec_failed" || !res.IsError {
		t.Fatalf("unexpected classification: %+v", res)
	}
}

func TestOutcomeForErrorCode(t *testing.T) {
	cases := map[string]ToolOutcome{
		"timeout":               OutcomeTimeout,
		"cancelled":             OutcomeCancelled,
		"canceled":              OutcomeCancelled,
		"approval_denied":       OutcomeBlocked,
		"policy_denied":         OutcomeBlocked,
		"plan_mode_blocked":     OutcomeBlocked,
		"tool_call_cap_reached": OutcomeBlocked,
		"exec_failed":           OutcomeFailure,
		"search_not_found":      OutcomeFailure,
		"":                      OutcomeFailure,
	}
	for code, want := range cases {
		if got := OutcomeForErrorCode(code); got != want {
			t.Errorf("OutcomeForErrorCode(%q) = %s, want %s", code, got, want)
		}
	}
}

func TestCanonicalizeToolPayloadIsJSONTyped(t *testing.T) {
	env := NewToolSuccessEnvelope(map[string]any{
		"metrics": map[string]any{"exit_code": 1, "duration_ms": int64(42)},
		"payload": map[string]any{"stdout": "x", "lines": []int{1, 2}},
	})
	env.Summary = "no matches (exit 1)"
	payload := CanonicalizeToolPayload(env)

	// The canonical form must be identical before and after a persistence
	// round trip — that is the whole point of canonicalization.
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var reloaded map[string]any
	if err := json.Unmarshal(b, &reloaded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if !reflect.DeepEqual(payload, reloaded) {
		t.Fatalf("payload is not canonical:\nlive:     %#v\nreloaded: %#v", payload, reloaded)
	}
	metrics, ok := payload["metrics"].(map[string]any)
	if !ok {
		t.Fatalf("metrics missing: %#v", payload)
	}
	if _, ok := metrics["exit_code"].(float64); !ok {
		t.Fatalf("expected JSON-typed float64 exit_code, got %T", metrics["exit_code"])
	}
	if payload["summary"] != "no matches (exit 1)" {
		t.Fatalf("summary reserved key missing: %#v", payload)
	}
}

func TestFinalizeToolResultChannels(t *testing.T) {
	// The hand-written literals agent code emits for blocked/skipped calls
	// must classify the same way live and after a legacy reload.
	denied := FinalizeToolResultChannels(ToolResult{
		ToolCallID: "c1", Name: "shell_run",
		Content: `{"success":false,"error":"tool approval denied","code":"approval_denied"}`,
		IsError: true,
	})
	if denied.Outcome != OutcomeBlocked || denied.Code != "approval_denied" {
		t.Fatalf("approval denied misclassified: %+v", denied)
	}
	if denied.ModelText != denied.Content {
		t.Fatal("ModelText must take Content bytes verbatim")
	}

	skipped := FinalizeToolResultChannels(ToolResult{
		ToolCallID: "c2", Name: "edit",
		Content: `{"success":false,"error":"tool skipped because another tool requested a runtime handoff","code":"turn_aborted"}`,
		IsError: true,
	})
	if skipped.Outcome != OutcomeFailure || skipped.Code != "turn_aborted" {
		t.Fatalf("turn_aborted misclassified: %+v", skipped)
	}

	raw := FinalizeToolResultChannels(ToolResult{ToolCallID: "c3", Name: "mcp", Content: "plain text & <tags>"})
	if raw.Outcome != OutcomeSuccess || raw.Payload != nil || raw.ModelText != "plain text & <tags>" {
		t.Fatalf("raw text result misclassified: %+v", raw)
	}

	// Idempotence: a funnel-produced result passes through untouched.
	already := ToolResult{ToolCallID: "c4", Outcome: OutcomeSuccess, Code: "ok", ModelText: "x", Content: "x"}
	if got := FinalizeToolResultChannels(already); got.Outcome != OutcomeSuccess || got.ModelText != "x" {
		t.Fatalf("finalize must be idempotent: %+v", got)
	}
}

func TestDispatchPopulatesChannelSeparatedFields(t *testing.T) {
	inner, err := MarshalToolEnvelope(NewToolSuccessEnvelope(map[string]any{
		"payload": map[string]any{"stdout": "hello"},
	}))
	if err != nil {
		t.Fatalf("marshal inner: %v", err)
	}
	reg := NewToolRegistry([]Tool{fixedContentTool{name: "fixture", content: inner}})
	res, err := reg.Dispatch(context.Background(), ToolCall{ID: "c1", Name: "fixture", Input: "{}"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if res.ModelText != res.Content {
		t.Fatalf("ModelText must mirror Content in phase 1:\nModelText: %s\nContent:   %s", res.ModelText, res.Content)
	}
	if res.Outcome != OutcomeSuccess || res.Code != "ok" {
		t.Fatalf("unexpected classification: outcome=%s code=%s", res.Outcome, res.Code)
	}
	payload, ok := res.Payload.(map[string]any)
	if !ok {
		t.Fatalf("expected canonical map payload, got %T", res.Payload)
	}
	inner2, ok := payload["payload"].(map[string]any)
	if !ok || inner2["stdout"] != "hello" {
		t.Fatalf("payload content missing: %#v", payload)
	}

	// Error path: tool not found goes through the same funnel.
	res, err = reg.Dispatch(context.Background(), ToolCall{ID: "c2", Name: "missing", Input: "{}"})
	if err != nil {
		t.Fatalf("dispatch missing: %v", err)
	}
	if res.Outcome != OutcomeFailure || res.Code != "not_found" || !res.IsError {
		t.Fatalf("unexpected not-found classification: outcome=%s code=%s isError=%v", res.Outcome, res.Code, res.IsError)
	}
	if res.ModelText == "" || res.ModelText != res.Content {
		t.Fatalf("not-found result must carry mirrored ModelText, got %q", res.ModelText)
	}
}

func TestShellRenderWithoutMetricsAvoidsExitHeader(t *testing.T) {
	// Approval/validation failures never ran a process: no exit header.
	res := NewToolResultError(ToolCall{ID: "c1", Name: "shell_run"}, "approval_denied", "tool approval denied", nil)
	if strings.HasPrefix(res.ModelText, "exit") || strings.Contains(res.ModelText, "exit none") {
		t.Fatalf("non-executed shell failure must not carry an exit header: %q", res.ModelText)
	}
	if !strings.HasPrefix(res.ModelText, "error (approval_denied)") {
		t.Fatalf("expected generic error rendering, got %q", res.ModelText)
	}

	// Real exits keep the header.
	env := NewToolSuccessEnvelope(map[string]any{
		"metrics": map[string]any{"exit_code": 0, "duration_ms": 12},
		"payload": map[string]any{"stdout": "done"},
	})
	ok := NewToolResultFromEnvelope(ToolCall{ID: "c2", Name: "shell_run"}, env, nil)
	if !strings.HasPrefix(ok.ModelText, "exit 0 (12ms)") {
		t.Fatalf("executed command must keep the exit header: %q", ok.ModelText)
	}
}
