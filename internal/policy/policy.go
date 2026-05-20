package policy

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/policy/shellrisk"
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

type ScopedAllowPolicy struct {
	Base               ToolPolicy
	ShellAllowPrefixes []string
}

func (p ScopedAllowPolicy) Decide(spec core.ToolSpec, call core.ToolCall) PolicyDecision {
	base := p.Base
	if base == nil {
		base = DefaultToolPolicy{Mode: ApprovalModeOnRequest}
	}
	decision := base.Decide(spec, call)
	if !decision.Allow {
		return decision
	}
	if spec.Name == "shell_run" {
		cmd := shellCommandFromInput(call.Input)
		if shellCommandEligibleForScopedAllow(cmd) {
			for _, allow := range p.ShellAllowPrefixes {
				if hasAllowCommandPrefix(cmd, allow) && scopedAllowCommandAllowed(cmd, allow) {
					return PolicyDecision{
						Allow:            true,
						RequiresApproval: false,
						Code:             "scoped_allow_prefix",
						Phase:            "allowed",
						MatchedRule:      allow,
					}
				}
			}
		}
	}
	return decision
}

type ReadOnlyTurnPolicy struct {
	Base ToolPolicy
}

func (p ReadOnlyTurnPolicy) Decide(spec core.ToolSpec, call core.ToolCall) PolicyDecision {
	base := p.Base
	if base == nil {
		base = DefaultToolPolicy{Mode: ApprovalModeOnRequest}
	}
	decision := base.Decide(spec, call)
	if !decision.Allow {
		return decision
	}
	if core.IsReadOnlyToolCall(spec, call) {
		return decision
	}
	if spec.Name == "shell_run" && decision.Code == "scoped_allow_prefix" && !decision.RequiresApproval {
		return decision
	}
	return PolicyDecision{
		Allow:  false,
		Reason: "review turns are read-only",
		Code:   "read_only_turn_denied",
		Phase:  "denied",
	}
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
	if spec.Name == "shell_run" {
		cmd := shellCommandFromInput(call.Input)
		for _, deny := range p.DenyPrefixes {
			if hasDenyCommandPrefix(cmd, deny) {
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
			if hasAllowCommandPrefix(cmd, allow) {
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
	if spec.Name == "shell_run" {
		cmd := shellCommandFromInput(call.Input)
		decision := shellrisk.Classify(cmd)
		if decision.Allow {
			return PolicyDecision{Allow: true, Code: "shell_auto_allow", Phase: "allowed"}
		}
		if decision.Level == shellrisk.LevelBoundedWrite {
			return PolicyDecision{
				Allow:            true,
				RequiresApproval: true,
				Reason:           decision.Reason,
				Code:             shellrisk.CodeBoundedWrite,
				Phase:            "needs_approval",
			}
		}
	}
	if hasCapability(spec, "mutates_state") {
		return PolicyDecision{
			Allow:            true,
			RequiresApproval: true,
			Reason:           "tool mutates persistent state",
			Code:             "approval_required",
			Phase:            "needs_approval",
		}
	}
	switch spec.Name {
	case "edit", "write", "apply_patch", "shell_run":
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

func hasCapability(spec core.ToolSpec, capability string) bool {
	want := strings.TrimSpace(strings.ToLower(capability))
	if want == "" {
		return false
	}
	for _, got := range spec.Capabilities {
		if strings.TrimSpace(strings.ToLower(got)) == want {
			return true
		}
	}
	return false
}

func shellCommandFromInput(input string) string {
	var body map[string]any
	if err := json.Unmarshal([]byte(input), &body); err != nil {
		return ""
	}
	cmd, _ := body["command"].(string)
	return strings.TrimSpace(cmd)
}

func shellCommandEligibleForScopedAllow(command string) bool {
	if command == "" {
		return false
	}
	return !strings.ContainsAny(command, "\n\r;&|<>$`(){}#")
}

// The gh pr * argv whitelist below is intentionally narrow and assumes the
// exact command shapes emitted by the /review pr prompt in
// internal/app/commands/review.go. If that prompt changes (different flags,
// different field lists, additional args), update the corresponding
// ghPR*ScopedAllowArgs helper so legitimate review calls keep auto-allowing.
func scopedAllowCommandAllowed(command, rule string) bool {
	rule = normalizeCommandPrefix(rule)
	if !strings.HasPrefix(rule, "gh pr ") {
		return true
	}
	return ghPRScopedAllowCommandAllowed(command, rule)
}

func ghPRScopedAllowCommandAllowed(command, rule string) bool {
	argv := strings.Fields(command)
	if len(argv) < 3 || strings.ToLower(argv[0]) != "gh" || strings.ToLower(argv[1]) != "pr" {
		return false
	}
	for _, arg := range argv[3:] {
		if arg == "--web" || arg == "-w" {
			return false
		}
	}
	switch rule {
	case "gh pr view":
		return ghPRViewScopedAllowArgs(argv[3:])
	case "gh pr list":
		return ghPRListScopedAllowArgs(argv[3:])
	case "gh pr diff":
		return ghPRDiffScopedAllowArgs(argv[3:])
	default:
		return false
	}
}

func ghPRViewScopedAllowArgs(args []string) bool {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") || strings.TrimSpace(args[0]) == "" {
		return false
	}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--comments":
			continue
		case "--json":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") || strings.TrimSpace(args[i+1]) == "" {
				return false
			}
			i++
		default:
			return false
		}
	}
	return true
}

func ghPRListScopedAllowArgs(args []string) bool {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--limit", "--json", "--state", "--author", "--base", "--head", "--label", "--search":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") || strings.TrimSpace(args[i+1]) == "" {
				return false
			}
			i++
		default:
			return false
		}
	}
	return true
}

func ghPRDiffScopedAllowArgs(args []string) bool {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") || strings.TrimSpace(args[0]) == "" {
		return false
	}
	for _, arg := range args[1:] {
		switch arg {
		case "--patch", "--name-only":
			continue
		default:
			if strings.HasPrefix(arg, "--color=") {
				continue
			}
			return false
		}
	}
	return true
}

func hasAllowCommandPrefix(command, rule string) bool {
	if strings.ContainsAny(command, "\n\r") || strings.ContainsAny(rule, "\n\r") {
		return false
	}
	return hasSingleLineCommandPrefix(command, rule)
}

func hasDenyCommandPrefix(command, rule string) bool {
	if strings.ContainsAny(rule, "\n\r") {
		return false
	}
	for _, segment := range strings.FieldsFunc(command, func(r rune) bool {
		return r == '\n' || r == '\r'
	}) {
		if hasSingleLineCommandPrefix(segment, rule) {
			return true
		}
	}
	return false
}

func hasSingleLineCommandPrefix(command, rule string) bool {
	command = normalizeCommandPrefix(command)
	rule = normalizeCommandPrefix(rule)
	if command == "" || rule == "" {
		return false
	}
	return command == rule || strings.HasPrefix(command, rule+" ")
}

func normalizeCommandPrefix(v string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(v))), " ")
}
