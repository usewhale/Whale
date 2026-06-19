package policy

import (
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/policy/effects"
)

func (p RulePolicy) effectPlanFor(spec core.ToolSpec, call core.ToolCall) effects.Plan {
	switch spec.Name {
	case "shell_run":
		if req, ok := shellExecutionRequestFromToolCall(call); ok {
			return p.shellExecutionEffectPlan(req, p.WorkspaceRoot)
		}
	case "read_file", "list_dir", "grep", "search_files":
		return p.readEffectPlan(call)
	default:
		if strings.HasPrefix(spec.Name, "mcp__") && mcpFilesystemTool(spec) {
			return p.mcpEffectPlan(call.Input)
		}
	}
	return effects.Plan{Risk: effects.RiskUnknown}
}

func (p RulePolicy) shellExecutionEffectPlan(req ShellExecutionRequest, pathRoot string) effects.Plan {
	var plan effects.Plan
	add := func(effect effects.Effect) {
		if strings.TrimSpace(effect.Scope) != "" {
			plan.Effects = append(plan.Effects, effect)
		}
	}
	add(effects.ShellExecEffect(req.Command))
	for _, dir := range p.externalDirsFromRoot(req.Command, pathRoot, req.Source == "exec_boundary") {
		add(effects.ExternalDirectoryEffect(dir))
	}
	return finalizeEffectPlan(plan)
}

func (p RulePolicy) execBoundaryEffectPlan(req ExecBoundaryRequest, command, pathRoot string) effects.Plan {
	plan := p.shellExecutionEffectPlan(ShellExecutionRequest{
		Source:  "exec_boundary",
		Command: command,
		CWD:     req.CWD,
	}, pathRoot)
	if dir := p.execBoundaryImplicitCWDExternalDir(req); dir != "" {
		plan.Effects = append(plan.Effects, effects.ExternalDirectoryEffect(dir))
	}
	return finalizeEffectPlan(plan)
}

func (p RulePolicy) readEffectPlan(call core.ToolCall) effects.Plan {
	var plan effects.Plan
	add := func(effect effects.Effect) {
		if strings.TrimSpace(effect.Scope) != "" {
			plan.Effects = append(plan.Effects, effect)
		}
	}
	target := readScopeTarget(call)
	if target != "*" {
		add(effects.ReadPathEffect(target))
	}
	for _, dir := range p.externalDirsFromReadInput(call) {
		add(effects.ExternalDirectoryEffect(dir))
	}
	return finalizeEffectPlan(plan)
}

func (p RulePolicy) mcpEffectPlan(input string) effects.Plan {
	var plan effects.Plan
	for _, dir := range p.externalDirsFromMCPInput(input) {
		if strings.TrimSpace(dir) != "" {
			plan.Effects = append(plan.Effects, effects.ExternalDirectoryEffect(dir))
		}
	}
	return finalizeEffectPlan(plan)
}

func finalizeEffectPlan(plan effects.Plan) effects.Plan {
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
