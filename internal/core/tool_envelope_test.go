package core

import (
	"strings"
	"testing"
)

func TestToolEnvelopeRoundTrip(t *testing.T) {
	content, err := MarshalToolEnvelope(NewToolSuccessEnvelope(map[string]any{
		"payload": map[string]any{"stdout": "ok"},
	}))
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	got, ok := ParseToolEnvelope(content)
	if !ok {
		t.Fatal("expected envelope to parse")
	}
	if !got.Success || got.Code != "ok" {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	payload, _ := got.Data["payload"].(map[string]any)
	if payload["stdout"] != "ok" {
		t.Fatalf("unexpected payload: %+v", got.Data)
	}
}

// HTML escaping in the envelope used to be asserted here to keep
// <proposed_plan> out of result text, but it corrupted every payload
// containing & < > on its way to the model (session 019ead56). The TUI
// strips proposed_plan tags from visible content itself (ebc13d6), so the
// envelope now stays verbatim; see tool_envelope_escaping_repro_test.go.
func TestToolEnvelopeKeepsPromptTagsVerbatim(t *testing.T) {
	content, err := MarshalToolEnvelope(ToolEnvelope{
		OK:      false,
		Success: false,
		Code:    "plan_mode_blocked",
		Summary: "output the final plan in a <proposed_plan> block",
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if !strings.Contains(content, "<proposed_plan>") {
		t.Fatalf("expected prompt tag verbatim in envelope, got %s", content)
	}
}

func TestToolEnvelopeParsesLegacyMessageAsError(t *testing.T) {
	got, ok := ParseToolEnvelope(`{"success":false,"code":"failed","message":"boom"}`)
	if !ok {
		t.Fatal("expected legacy envelope to parse")
	}
	if got.Error != "boom" || got.Message != "boom" {
		t.Fatalf("expected message copied to error, got %+v", got)
	}
}
