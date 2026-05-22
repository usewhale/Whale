package policy

import (
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func TestDefaultToolPolicyRequiresApprovalForMCPTools(t *testing.T) {
	decision := DefaultToolPolicy{}.Decide(
		core.ToolSpec{Name: "mcp__github__create_issue"},
		core.ToolCall{Name: "mcp__github__create_issue", Input: `{}`},
	)
	if !decision.Allow || !decision.RequiresApproval {
		t.Fatalf("decision: %+v", decision)
	}
}

func TestDefaultToolPolicyRequiresApprovalForReadOnlyMCPTools(t *testing.T) {
	decision := DefaultToolPolicy{}.Decide(
		core.ToolSpec{Name: "mcp__fs__read", ReadOnly: true},
		core.ToolCall{Name: "mcp__fs__read", Input: `{}`},
	)
	if !decision.Allow || !decision.RequiresApproval || decision.Code != "permission_required" {
		t.Fatalf("decision: %+v", decision)
	}
}
