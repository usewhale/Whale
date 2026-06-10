package core

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestNewToolResultFromEnvelopeModelTextMatchesEnvelope(t *testing.T) {
	env := ToolEnvelope{
		OK: false, Success: false,
		Error: "command failed", Code: "exec_failed", Summary: "command failed",
		Data: map[string]any{"payload": map[string]any{"stderr": "a && b < c"}},
	}
	want, err := MarshalToolEnvelope(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	res := NewToolResultFromEnvelope(ToolCall{ID: "c1", Name: "shell_run"}, env, nil)
	if res.ModelText != want {
		t.Fatalf("ModelText must equal the envelope serialization:\nwant: %s\ngot:  %s", want, res.ModelText)
	}
	if res.Content != want {
		t.Fatalf("phase-1 invariant: Content mirrors ModelText, got %s", res.Content)
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
