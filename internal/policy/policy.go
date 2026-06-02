package policy

import (
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

type PermissionAction string

const (
	PermissionAllow PermissionAction = "allow"
	PermissionAsk   PermissionAction = "ask"
	PermissionDeny  PermissionAction = "deny"
)

func ParsePermissionAction(v string) (PermissionAction, error) {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "", "allow":
		return PermissionAllow, nil
	case "ask":
		return PermissionAsk, nil
	case "deny":
		return PermissionDeny, nil
	default:
		return "", fmt.Errorf("invalid permission action: %s", v)
	}
}

type PermissionRule struct {
	Permission string
	Pattern    string
	Action     PermissionAction
}
type PermissionConfig struct {
	Read              map[string]string
	Edit              map[string]string
	Shell             map[string]string
	ExternalDirectory map[string]string
	MCP               map[string]string
	Memory            map[string]string
	Task              map[string]string
	WebSearch         map[string]string
	WebFetch          map[string]string
	MutatingTool      map[string]string
}
type RulePolicy struct {
	Default       PermissionAction
	Rules         []PermissionRule
	WorkspaceRoot string
	WorktreeRoot  string
}
type DefaultToolPolicy struct {
	Default       PermissionAction
	Rules         []PermissionRule
	WorkspaceRoot string
	WorktreeRoot  string
}
type PolicyDecision struct {
	Allow                bool
	RequiresApproval     bool
	Reason               string
	Code                 string
	Phase                string
	MatchedRule          string
	Permission           string
	Pattern              string
	ApprovalRequirements []ApprovalRequirement
	AllowedRequirements  []ApprovalRequirement
}
type ApprovalRequirement struct {
	Permission string
	Pattern    string
}
type ToolPolicy interface {
	Decide(spec core.ToolSpec, call core.ToolCall) PolicyDecision
}
type ScopedAllowPolicy struct {
	Base               ToolPolicy
	ShellAllowPrefixes []string
}
type ReadOnlyTurnPolicy struct {
	Base ToolPolicy
}
