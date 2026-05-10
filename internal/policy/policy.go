package policy

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

type ApprovalMode string

const (
	ApprovalModeOnRequest ApprovalMode = "on-request"
	ApprovalModeNever     ApprovalMode = "never"
)

func ParseApprovalMode(v string) (ApprovalMode, error) {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "", "on-request", "on_request":
		return ApprovalModeOnRequest, nil
	case "never", "never-ask", "never_ask":
		return ApprovalModeNever, nil
	default:
		return "", fmt.Errorf("invalid approval mode: %s", v)
	}
}

type PolicyDecision struct {
	Allow            bool
	RequiresApproval bool
	Reason           string
	Code             string
	Phase            string
	MatchedRule      string
}

type ToolPolicy interface {
	Decide(spec core.ToolSpec, call core.ToolCall) PolicyDecision
}

type DefaultToolPolicy struct {
	Mode          ApprovalMode
	AllowPrefixes []string
	DenyPrefixes  []string
}

func (p DefaultToolPolicy) Decide(spec core.ToolSpec, call core.ToolCall) PolicyDecision {
	mode := p.Mode
	if mode == "" {
		mode = ApprovalModeOnRequest
	}
	if spec.Name == "exec_shell" {
		cmd := shellCommandFromInput(call.Input)
		for _, deny := range p.DenyPrefixes {
			if hasCommandPrefix(cmd, deny) {
				return PolicyDecision{
					Allow:       false,
					Reason:      "command blocked by deny prefix",
					Code:        "policy_denied",
					Phase:       "denied",
					MatchedRule: deny,
				}
			}
		}
		for _, allow := range p.AllowPrefixes {
			if hasCommandPrefix(cmd, allow) {
				return PolicyDecision{
					Allow:            true,
					RequiresApproval: false,
					Code:             "allow_prefix",
					Phase:            "allowed",
					MatchedRule:      allow,
				}
			}
		}
	}
	if mode == ApprovalModeNever {
		return PolicyDecision{Allow: true, Code: "auto_allow", Phase: "allowed"}
	}
	if core.IsReadOnlyToolCall(spec, call) {
		return PolicyDecision{Allow: true, Code: "read_only", Phase: "allowed"}
	}
	switch spec.Name {
	case "edit", "write", "apply_patch", "exec_shell":
	default:
		if strings.HasPrefix(spec.Name, "mcp__") {
			return PolicyDecision{
				Allow:            true,
				RequiresApproval: true,
				Reason:           "MCP tool requires approval",
				Code:             "approval_required",
				Phase:            "needs_approval",
			}
		}
		return PolicyDecision{Allow: true, Code: "non_mutating_default", Phase: "allowed"}
	}
	return PolicyDecision{
		Allow:            true,
		RequiresApproval: true,
		Reason:           "tool requires approval",
		Code:             "approval_required",
		Phase:            "needs_approval",
	}
}

func shellCommandFromInput(input string) string {
	var body map[string]any
	if err := json.Unmarshal([]byte(input), &body); err != nil {
		return ""
	}
	cmd, _ := body["command"].(string)
	return strings.TrimSpace(cmd)
}

func hasCommandPrefix(command, rule string) bool {
	command = strings.TrimSpace(strings.ToLower(command))
	rule = strings.TrimSpace(strings.ToLower(rule))
	if command == "" || rule == "" {
		return false
	}
	return strings.HasPrefix(command, rule)
}
