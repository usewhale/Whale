package timeline

import (
	"testing"

	"github.com/usewhale/whale/internal/runtime/protocol"
)

func TestResultLooksFailedUsesStructuredOutcome(t *testing.T) {
	failed := protocol.Event{Kind: protocol.EventToolResult, Text: "error (not_found): missing file", ToolOutcome: "failure"}
	if !resultLooksFailed(failed) {
		t.Fatal("structured failure outcome must classify as failed")
	}
	ok := protocol.Event{Kind: protocol.EventToolResult, Text: "exit 0 (12ms)", ToolOutcome: "success"}
	if resultLooksFailed(ok) {
		t.Fatal("structured success outcome must not classify as failed")
	}
	// no_result is an answer, not a failure.
	if resultLooksFailed(protocol.Event{Kind: protocol.EventToolResult, ToolOutcome: "no_result"}) {
		t.Fatal("no_result must not classify as failed")
	}
	// Legacy events without outcome keep the old detection.
	legacy := protocol.Event{Kind: protocol.EventToolResult, Text: `{"success":false,"code":"x"}`}
	if !resultLooksFailed(legacy) {
		t.Fatal("legacy JSON failure markers must keep working")
	}
}
