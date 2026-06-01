package tasks

import (
	"slices"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/policy"
)

type childToolApprovalPolicy struct {
	Base         policy.ToolPolicy
	Capabilities []string
}

func (p childToolApprovalPolicy) Decide(spec core.ToolSpec, call core.ToolCall) policy.PolicyDecision {
	base := p.Base
	if base == nil {
		base = policy.RulePolicy{Default: policy.PermissionAllow, Rules: policy.DefaultRules()}
	}
	decision := base.Decide(spec, call)
	if !decision.Allow || decision.RequiresApproval || !p.requiresApproval(spec, call) {
		return decision
	}
	decision.RequiresApproval = true
	decision.Reason = "child agent write-capable tools require approval"
	decision.Code = "child_tool_approval_required"
	decision.Phase = "approval_required"
	return decision
}

func (p childToolApprovalPolicy) requiresApproval(spec core.ToolSpec, call core.ToolCall) bool {
	if core.IsReadOnlyToolCall(spec, call) {
		return false
	}
	caps := childCapabilitySet(p.Capabilities)
	if caps[CapabilityWorkspaceWrite] && slices.Contains(spec.Capabilities, CapabilityWorkspaceWrite) {
		return true
	}
	if caps[CapabilityShellRun] && slices.Contains(spec.Capabilities, CapabilityShellRun) {
		return true
	}
	for _, cap := range spec.Capabilities {
		if strings.TrimSpace(cap) == "mutates_state" {
			return true
		}
	}
	return false
}

func childCapabilitySet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out[trimmed] = true
		}
	}
	return out
}
