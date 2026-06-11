package skillsimprover

import (
	"testing"

	"github.com/usewhale/whale/internal/agent"
)

func TestToolFailureEvidencePrefersRenderedCause(t *testing.T) {
	payload := agent.HookPayload{
		Event:         agent.HookEventPostToolUse,
		SessionID:     "s1",
		ToolName:      "shell_run",
		ToolResult:    "error (exec_failed): tests failed: 3 of 41 cases\nstderr:\n--- FAIL: TestX",
		ToolOutcome:   "failure",
		ToolErrorCode: "exec_failed",
	}
	ev, ok := toolFailureEvidence(payload)
	if !ok {
		t.Fatal("expected failure evidence")
	}
	// The human-readable rendered line is the cause; the bare code stays
	// in metadata only.
	if ev.ToolResultSummary != "error (exec_failed): tests failed: 3 of 41 cases" {
		t.Fatalf("summary should be the first rendered line, got %q", ev.ToolResultSummary)
	}
	if ev.Metadata["code"] != "exec_failed" {
		t.Fatalf("metadata code missing: %#v", ev.Metadata)
	}

	// Success outcomes never produce evidence.
	payload.ToolOutcome = "success"
	if _, ok := toolFailureEvidence(payload); ok {
		t.Fatal("success outcome must not produce failure evidence")
	}
}
