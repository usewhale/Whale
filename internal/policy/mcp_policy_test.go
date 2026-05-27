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

func TestRulePolicyMatchesMCPByToolName(t *testing.T) {
	p := RulePolicy{
		Default: PermissionAllow,
		Rules: []PermissionRule{
			{Permission: "mcp", Pattern: "*", Action: PermissionAllow},
			{Permission: "mcp", Pattern: "mcp__github__create_issue", Action: PermissionAsk},
		},
	}

	decision := p.Decide(
		core.ToolSpec{Name: "mcp__github__create_issue"},
		core.ToolCall{Name: "mcp__github__create_issue", Input: `{"name":"unrelated","path":"/tmp/x"}`},
	)
	if !decision.Allow || !decision.RequiresApproval || decision.MatchedRule != "mcp:mcp__github__create_issue=ask" {
		t.Fatalf("decision: %+v", decision)
	}
}

func TestRulePolicyMCPArgumentsDoNotChangePermissionTarget(t *testing.T) {
	p := RulePolicy{
		Default: PermissionAllow,
		Rules: []PermissionRule{
			{Permission: "mcp", Pattern: "*", Action: PermissionAllow},
			{Permission: "mcp", Pattern: "/etc/passwd", Action: PermissionDeny},
		},
	}

	decision := p.Decide(
		core.ToolSpec{Name: "mcp__fs__read"},
		core.ToolCall{Name: "mcp__fs__read", Input: `{"path":"/etc/passwd"}`},
	)
	if !decision.Allow || decision.RequiresApproval || decision.Code == "permission_denied" {
		t.Fatalf("decision: %+v", decision)
	}
}
