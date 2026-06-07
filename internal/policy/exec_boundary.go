package policy

import (
	"path"
	"strings"
)

// ExecBoundaryRequest is the structured command boundary used by an
// intercepting shell runtime when the shell is about to exec a concrete program. This is
// intentionally separate from write_stdin: terminal input is transport, while
// command policy belongs at the eventual program/argv boundary.
type ExecBoundaryRequest struct {
	Program string
	Argv    []string
	CWD     string
}

func (p RulePolicy) DecideExecBoundary(req ExecBoundaryRequest) PolicyDecision {
	patterns := execBoundaryShellPatterns(req)
	if len(patterns) == 0 {
		return PolicyDecision{
			Allow:      false,
			Reason:     "exec boundary request missing program",
			Code:       "invalid_exec_boundary",
			Phase:      "denied",
			Permission: "shell",
		}
	}
	externalDecision, externalHandled := p.decideExecBoundaryExternalDirectory(req, patterns[0])
	if externalHandled && !externalDecision.Allow {
		return externalDecision
	}
	shellDecision := p.decideExecBoundaryShell(patterns)
	if !shellDecision.Allow {
		return shellDecision
	}
	if shellDecision.RequiresApproval {
		if externalHandled && externalDecision.RequiresApproval {
			externalDecision.ApprovalRequirements = append(
				[]ApprovalRequirement{{Permission: "shell", Pattern: shellDecision.Pattern}},
				externalDecision.ApprovalRequirements...,
			)
			return externalDecision
		}
		if externalHandled {
			shellDecision.AllowedRequirements = append(shellDecision.AllowedRequirements, externalDecision.AllowedRequirements...)
		}
		return shellDecision
	}
	if externalHandled {
		return externalDecision
	}
	return shellDecision
}

func (p RulePolicy) decideExecBoundaryShell(patterns []string) PolicyDecision {
	fallback := PermissionRule{Permission: "shell", Pattern: "*", Action: p.Default}
	if fallback.Action == "" {
		fallback.Action = PermissionAllow
	}
	var ask *PermissionRule
	var askPattern string
	var allow *PermissionRule
	var allowPattern string
	for _, pattern := range patterns {
		rule, matched := p.evaluateShellSegment(pattern)
		if !matched {
			continue
		}
		switch rule.Action {
		case PermissionDeny:
			return execBoundaryDecision(rule, pattern, false, false)
		case PermissionAsk:
			copy := rule
			ask = &copy
			askPattern = pattern
		case PermissionAllow:
			if rule.Pattern != "*" {
				return execBoundaryDecision(rule, pattern, true, false)
			}
			copy := rule
			allow = &copy
			allowPattern = pattern
		}
	}
	if ask != nil {
		return execBoundaryDecision(*ask, askPattern, true, true)
	}
	if allow != nil {
		return execBoundaryDecision(*allow, allowPattern, true, false)
	}
	switch fallback.Action {
	case PermissionDeny:
		return execBoundaryDecision(fallback, patterns[0], false, false)
	case PermissionAsk:
		return execBoundaryDecision(fallback, patterns[0], true, true)
	default:
		return PolicyDecision{Allow: true, Code: "permission_allow", Phase: "allowed", Permission: "shell", Pattern: patterns[0]}
	}
}

func (p RulePolicy) decideExecBoundaryExternalDirectory(req ExecBoundaryRequest, command string) (PolicyDecision, bool) {
	pathRoot := strings.TrimSpace(req.CWD)
	if pathRoot == "" {
		pathRoot = p.WorkspaceRoot
	}
	plan := p.execBoundaryEffectPlan(req, command, pathRoot)
	return p.decidePermissionRequests(permissionRequestsFromEffects(plan))
}

func execBoundaryDecision(rule PermissionRule, pattern string, allow, ask bool) PolicyDecision {
	phase := "allowed"
	code := "permission_allow"
	reason := ""
	if !allow {
		phase = "denied"
		code = "permission_denied"
		reason = "shell denied by permission rule"
	} else if ask {
		phase = "needs_approval"
		code = "permission_required"
		reason = "permission rule requires approval"
	}
	return PolicyDecision{
		Allow:            allow,
		RequiresApproval: ask,
		Reason:           reason,
		Code:             code,
		Phase:            phase,
		MatchedRule:      ruleLabel(rule),
		Permission:       "shell",
		Pattern:          pattern,
	}
}

func execBoundaryShellPatterns(req ExecBoundaryRequest) []string {
	program := strings.TrimSpace(req.Program)
	if program == "" {
		return nil
	}
	argv := append([]string(nil), req.Argv...)
	if len(argv) == 0 {
		argv = []string{program}
	}
	for i := range argv {
		argv[i] = strings.TrimSpace(argv[i])
	}
	args := argv[1:]
	exact := strings.Join(append([]string{program}, args...), " ")
	base := path.Base(strings.TrimRight(program, "/"))
	if base == "." || base == "/" || base == "" || base == program {
		return []string{strings.TrimSpace(exact)}
	}
	return []string{strings.TrimSpace(exact), strings.TrimSpace(strings.Join(append([]string{base}, args...), " "))}
}
