package policy

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

func (p ScopedAllowPolicy) Decide(spec core.ToolSpec, call core.ToolCall) PolicyDecision {
	base := p.Base
	if base == nil {
		base = DefaultToolPolicy{}
	}
	decision := base.Decide(spec, call)
	if !decision.Allow || spec.Name != "shell_run" {
		return decision
	}
	cmd := shellCommandFromInput(call.Input)
	if !shellCommandEligibleForScopedAllow(cmd) {
		return decision
	}
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
	return decision
}
func (p ReadOnlyTurnPolicy) Decide(spec core.ToolSpec, call core.ToolCall) PolicyDecision {
	base := p.Base
	if base == nil {
		base = DefaultToolPolicy{}
	}
	decision := base.Decide(spec, call)
	if !decision.Allow {
		return decision
	}
	if core.IsReadOnlyToolCall(spec, call) {
		return decision
	}
	return PolicyDecision{
		Allow:  false,
		Reason: "review turns are read-only",
		Code:   "read_only_turn_denied",
		Phase:  "denied",
	}
}
func (p RulePolicy) Decide(spec core.ToolSpec, call core.ToolCall) PolicyDecision {
	if denied := p.mcpAllowedDirsDenied(spec, call); denied != "" {
		return PolicyDecision{
			Allow:       false,
			Reason:      denied,
			Code:        "mcp_allowed_dirs_denied",
			Phase:       "denied",
			MatchedRule: "mcp_allowed_dirs",
			Permission:  "mcp",
		}
	}
	requests := p.requestsFor(spec, call)
	if len(requests) == 0 {
		requests = []permissionRequest{{Kind: permissionKind(spec.Name), Pattern: permissionTarget(call)}}
	}
	if decision, ok := p.decidePermissionRequests(requests); ok {
		return decision
	}
	return PolicyDecision{Allow: true, Code: "permission_allow", Phase: "allowed"}
}

func (p RulePolicy) decidePermissionRequests(requests []permissionRequest) (PolicyDecision, bool) {
	var ask *PermissionRule
	var askReq permissionRequest
	var askReqs []permissionRequest
	var allowedExternal *permissionRequest
	var allowedExternalRule PermissionRule
	var allowedReqs []permissionRequest
	for _, req := range requests {
		rule, matched := p.evaluateDetailed(req.Kind, req.Pattern)
		switch rule.Action {
		case PermissionDeny:
			return PolicyDecision{
				Allow:       false,
				Reason:      fmt.Sprintf("%s denied by permission rule", req.Kind),
				Code:        "permission_denied",
				Phase:       "denied",
				MatchedRule: ruleLabel(rule),
				Permission:  req.Kind,
				Pattern:     req.Pattern,
			}, true
		case PermissionAsk:
			copy := rule
			ask = &copy
			askReq = req
			askReqs = append(askReqs, req)
		case PermissionAllow:
			if req.Kind == "external_directory" && matched {
				copy := req
				allowedExternal = &copy
				allowedExternalRule = rule
				allowedReqs = append(allowedReqs, req)
			}
		}
	}
	if ask != nil {
		return PolicyDecision{
			Allow:                true,
			RequiresApproval:     true,
			Reason:               "permission rule requires approval",
			Code:                 "permission_required",
			Phase:                "needs_approval",
			MatchedRule:          ruleLabel(*ask),
			Permission:           askReq.Kind,
			Pattern:              askReq.Pattern,
			ApprovalRequirements: approvalRequirementsFromRequests(askReqs),
			AllowedRequirements:  approvalRequirementsFromRequests(allowedReqs),
		}, true
	}
	if allowedExternal != nil {
		return PolicyDecision{
			Allow:       true,
			Code:        "permission_allow",
			Phase:       "allowed",
			MatchedRule: ruleLabel(allowedExternalRule),
			Permission:  allowedExternal.Kind,
			Pattern:     allowedExternal.Pattern,
			AllowedRequirements: []ApprovalRequirement{{
				Permission: allowedExternal.Kind,
				Pattern:    allowedExternal.Pattern,
			}},
		}, true
	}
	return PolicyDecision{}, false
}
func (p DefaultToolPolicy) Decide(spec core.ToolSpec, call core.ToolCall) PolicyDecision {
	rules := append([]PermissionRule{}, DefaultRules()...)
	rules = append(rules, p.Rules...)
	def := p.Default
	if def == "" {
		def = PermissionAllow
	}
	return RulePolicy{Default: def, Rules: rules, WorkspaceRoot: p.WorkspaceRoot, WorktreeRoot: p.WorktreeRoot}.Decide(spec, call)
}
func approvalRequirementsFromRequests(requests []permissionRequest) []ApprovalRequirement {
	if len(requests) == 0 {
		return nil
	}
	out := make([]ApprovalRequirement, 0, len(requests))
	seen := map[string]bool{}
	for _, req := range requests {
		if strings.TrimSpace(req.Kind) == "" || strings.TrimSpace(req.Pattern) == "" {
			continue
		}
		key := req.Kind + "\x00" + req.Pattern
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ApprovalRequirement{Permission: req.Kind, Pattern: req.Pattern})
	}
	return out
}

type permissionRequest struct {
	Kind    string
	Pattern string
}

const dynamicShellRedirectionTarget = "dynamic redirection target"

func (p RulePolicy) requestsFor(spec core.ToolSpec, call core.ToolCall) []permissionRequest {
	kind := permissionKind(spec.Name)
	target := permissionTarget(call)
	// A custom or plugin tool that advertises a state-mutating capability but
	// whose name resolves to no built-in permission category is governed by the
	// mutating_tool permission kind. Do not also evaluate the raw tool name, or
	// restrictive global defaults would make [permissions.mutating_tool] allow
	// rules ineffective.
	if !isMappedPermissionKind(kind) && hasCapability(spec, "mutates_state") {
		return []permissionRequest{{Kind: "mutating_tool", Pattern: spec.Name}}
	}
	requests := []permissionRequest{{Kind: kind, Pattern: target}}
	if spec.Name == "shell_run" {
		if req, ok := shellExecutionRequestFromToolCall(call); ok {
			requests[0].Pattern = req.Command
		} else {
			requests[0].Pattern = ""
		}
	}
	requests = append(requests, permissionRequestsFromEffects(p.effectPlanFor(spec, call))...)
	switch spec.Name {
	case "spawn_subagent":
		if core.IsReadOnlyToolCall(spec, call) {
			requests[0].Pattern = "readonly"
		} else {
			requests[0].Pattern = "mutating"
		}
	case "write_stdin":
		if writeStdinPollCall(call) {
			requests[0] = permissionRequest{Kind: "read", Pattern: "*"}
		}
	case "grep", "search_files":
		// These read-scoped tools carry a search regex/glob in their non-path
		// fields. Evaluate read rules against the directory being searched,
		// not the query text, so that searching for ".env" is not mistaken
		// for reading a .env file.
		requests[0].Pattern = readScopeTarget(call)
	case "apply_patch":
		// Evaluate edit rules against each file the patch touches so that
		// path-based [permissions.edit] rules apply, rather than matching a
		// hash of the whole patch.
		if paths := patchEditPaths(call.Input); len(paths) > 0 {
			requests = requests[:0]
			for _, path := range paths {
				requests = append(requests, permissionRequest{Kind: kind, Pattern: path})
			}
		}
	}
	return requests
}

func writeStdinPollCall(call core.ToolCall) bool {
	if call.Name != "write_stdin" {
		return false
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(call.Input), &body); err != nil {
		return false
	}
	chars, hasChars := body["chars"]
	keys := body["keys"]
	if s, ok := chars.(string); ok && s != "" {
		return false
	}
	if hasChars && chars != nil {
		if _, ok := chars.(string); !ok {
			return false
		}
	}
	if keysLen(keys) > 0 {
		return false
	}
	return true
}

func keysLen(v any) int {
	switch keys := v.(type) {
	case []any:
		return len(keys)
	case []string:
		return len(keys)
	default:
		return 0
	}
}

// isMappedPermissionKind reports whether kind is one of the built-in permission
// categories permissionKind resolves known tools to, as opposed to a raw,
// unmapped custom or plugin tool name.
func isMappedPermissionKind(kind string) bool {
	switch kind {
	case "read", "edit", "shell", "terminal", "memory", "task", "mcp", "web_search", "web_fetch":
		return true
	default:
		return false
	}
}

// hasCapability reports whether spec advertises the given capability,
// case-insensitively.
func hasCapability(spec core.ToolSpec, capability string) bool {
	want := strings.TrimSpace(strings.ToLower(capability))
	for _, got := range spec.Capabilities {
		if strings.TrimSpace(strings.ToLower(got)) == want {
			return true
		}
	}
	return false
}

// patchEditPaths extracts the file paths an apply_patch call modifies from its
// "*** Update/Add/Delete File:" headers. Rename targets ("*** Move to:") are
// included as well, so a rule cannot be bypassed by moving an allowed file
// onto a denied path.
func patchEditPaths(input string) []string {
	var body map[string]any
	if err := json.Unmarshal([]byte(input), &body); err != nil {
		return nil
	}
	patch, _ := body["patch"].(string)
	if strings.TrimSpace(patch) == "" {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(patch, "\n") {
		line = strings.TrimRight(line, "\r")
		for _, prefix := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: ", "*** Move to: "} {
			if strings.HasPrefix(line, prefix) {
				if path := strings.TrimSpace(strings.TrimPrefix(line, prefix)); path != "" {
					paths = append(paths, path)
				}
			}
		}
	}
	return uniqueStrings(paths)
}
