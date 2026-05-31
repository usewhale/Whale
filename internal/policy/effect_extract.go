package policy

import (
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/policy/effects"
)

func (p RulePolicy) effectPlanFor(spec core.ToolSpec, call core.ToolCall) effects.Plan {
	var plan effects.Plan
	add := func(effect effects.Effect) {
		if strings.TrimSpace(effect.Scope) != "" {
			plan.Effects = append(plan.Effects, effect)
		}
	}
	switch spec.Name {
	case "shell_run":
		cmd := shellCommandFromInput(call.Input)
		add(effects.ShellExecEffect(cmd))
		for _, dir := range p.externalDirs(cmd) {
			add(effects.ExternalDirectoryEffect(dir))
		}
	case "read_file", "list_dir", "grep", "search_files":
		target := readScopeTarget(call)
		if target != "*" {
			add(effects.ReadPathEffect(target))
		}
		for _, dir := range p.externalDirsFromReadInput(call) {
			add(effects.ExternalDirectoryEffect(dir))
		}
	default:
		if strings.HasPrefix(spec.Name, "mcp__") && mcpFilesystemTool(spec) {
			for _, dir := range p.externalDirsFromMCPInput(call.Input) {
				add(effects.ExternalDirectoryEffect(dir))
			}
		}
	}
	plan.Risk = effects.RiskUnknown
	for _, effect := range plan.Effects {
		if effect.Risk == effects.RiskBoundedWrite {
			plan.Risk = effects.RiskBoundedWrite
			return plan
		}
		if effect.Risk == effects.RiskSafeRead && plan.Risk == effects.RiskUnknown {
			plan.Risk = effects.RiskSafeRead
		}
	}
	return plan
}

func permissionRequestsFromEffects(plan effects.Plan) []permissionRequest {
	var requests []permissionRequest
	for _, effect := range plan.Effects {
		switch effect.Kind {
		case effects.ExternalDirectory:
			requests = append(requests, permissionRequest{Kind: string(effects.ExternalDirectory), Pattern: effect.Scope})
		}
	}
	return requests
}
