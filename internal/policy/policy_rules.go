package policy

import (
	"fmt"
	"sort"
	"strings"
)

func (p RulePolicy) evaluate(permission, pattern string) PermissionRule {
	rule, _ := p.evaluateDetailed(permission, pattern)
	return rule
}
func (p RulePolicy) evaluateDetailed(permission, pattern string) (PermissionRule, bool) {
	fallback := PermissionRule{Permission: permission, Pattern: "*", Action: p.Default}
	if fallback.Action == "" {
		fallback.Action = PermissionAllow
	}
	if permission == "shell" {
		return p.evaluateShell(pattern, fallback), true
	}
	for i := len(p.Rules) - 1; i >= 0; i-- {
		rule := p.Rules[i]
		if rule.Action == "" {
			continue
		}
		if !wildcardMatch(rule.Permission, permission) {
			continue
		}
		if wildcardMatch(rule.Pattern, pattern) {
			return rule, true
		}
	}
	return fallback, false
}
func (p RulePolicy) evaluateShell(command string, fallback PermissionRule) PermissionRule {
	// Each shell segment keeps normal last-match-wins rule evaluation. The
	// command-level result then preserves deny precedence across segments so an
	// approval prompt cannot mask a separate denied command.
	segments := normalizeShellSegments(command)
	var ask *PermissionRule
	var allow *PermissionRule
	for _, segment := range segments {
		rule, matched := p.evaluateShellSegment(segment)
		if !matched {
			rule = fallback
		}
		switch rule.Action {
		case PermissionDeny:
			return rule
		case PermissionAsk:
			copy := rule
			ask = &copy
		case PermissionAllow:
			copy := rule
			allow = &copy
		}
	}
	if ask != nil {
		return *ask
	}
	if allow != nil {
		return *allow
	}
	return fallback
}
func (p RulePolicy) evaluateShellSegment(segment string) (PermissionRule, bool) {
	for i := len(p.Rules) - 1; i >= 0; i-- {
		rule := p.Rules[i]
		if rule.Action == "" {
			continue
		}
		if !wildcardMatch(rule.Permission, "shell") {
			continue
		}
		if shellSegmentRuleMatches(rule, segment) {
			return rule, true
		}
	}
	return PermissionRule{}, false
}
func RulesFromMap(permission string, rules map[string]string) ([]PermissionRule, error) {
	if len(rules) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(rules))
	for key := range rules {
		keys = append(keys, key)
	}
	// Rules are evaluated last-match-wins, so order keys least-specific first.
	// Specificity is approximated by literal (non-wildcard) character count, so
	// a narrow pattern like "git push --force*" outranks a broad "git push*"
	// regardless of how the unordered TOML map was written. Ties fall back to
	// lexical order for determinism.
	sort.SliceStable(keys, func(i, j int) bool {
		li, lj := literalLen(keys[i]), literalLen(keys[j])
		if li != lj {
			return li < lj
		}
		return keys[i] < keys[j]
	})
	out := make([]PermissionRule, 0, len(keys))
	for _, pattern := range keys {
		action, err := ParsePermissionAction(rules[pattern])
		if err != nil {
			return nil, err
		}
		out = append(out, PermissionRule{Permission: permission, Pattern: expandHome(pattern), Action: action})
	}
	return out, nil
}
func RulesFromConfig(config PermissionConfig) ([]PermissionRule, error) {
	tables := []struct {
		name  string
		rules map[string]string
	}{
		{name: "read", rules: config.Read},
		{name: "edit", rules: config.Edit},
		{name: "shell", rules: config.Shell},
		{name: "external_directory", rules: config.ExternalDirectory},
		{name: "mcp", rules: config.MCP},
		{name: "memory", rules: config.Memory},
		{name: "task", rules: config.Task},
		{name: "mutating_tool", rules: config.MutatingTool},
	}
	var out []PermissionRule
	for _, table := range tables {
		rules, err := RulesFromMap(table.name, table.rules)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", table.name, err)
		}
		out = append(out, rules...)
	}
	return out, nil
}
func ruleLabel(rule PermissionRule) string {
	return strings.TrimSpace(rule.Permission + ":" + rule.Pattern + "=" + string(rule.Action))
}
