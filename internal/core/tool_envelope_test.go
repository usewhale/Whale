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

func TestToolEnvelopeKeepsDefaultEscapingForPromptTags(t *testing.T) {
	content, err := MarshalToolEnvelope(ToolEnvelope{
		OK:      false,
		Success: false,
		Code:    "plan_mode_blocked",
		Summary: "output the final plan in a <proposed_plan> block",
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if strings.Contains(content, "<proposed_plan>") {
		t.Fatalf("generic envelope should keep default JSON escaping, got %s", content)
	}
	if !strings.Contains(content, `\u003cproposed_plan\u003e`) {
		t.Fatalf("expected prompt tag to be escaped in generic envelope, got %s", content)
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
