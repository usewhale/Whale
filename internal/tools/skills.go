package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/skills"
)

func (b *Toolset) loadSkill(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	var in struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}
	if err := decodeInput(call.Input, &in); err != nil {
		return marshalToolError(call, "invalid_args", err.Error()), nil
	}
	name := strings.TrimSpace(in.Name)
	if !skills.ValidName(name) {
		return marshalToolError(call, "invalid_args", "skill name must be alphanumeric with hyphens"), nil
	}
	roots := skills.DefaultRoots(b.root)
	report := skills.BuildReport(roots, skills.ReportOptions{DisabledNames: b.skillDisabled, WorkspaceRoot: b.root})
	for _, view := range report.Disabled {
		if view.Name == name {
			return marshalToolError(call, "disabled", "skill disabled: "+name), nil
		}
	}
	if skillNameDisabled(name, b.skillDisabled) {
		return marshalToolError(call, "disabled", "skill disabled: "+name), nil
	}
	for _, view := range report.Problems {
		if view.Name == name {
			return marshalToolError(call, "unavailable", fmt.Sprintf("skill unavailable: %s: %s", name, view.Reason)), nil
		}
	}
	skill, _, ok := skills.Find(roots, name)
	if !ok {
		for _, candidate := range skills.Filter(b.extraSkills, b.skillDisabled) {
			if candidate != nil && candidate.Name == name {
				cp := *candidate
				skill = &cp
				ok = true
				break
			}
		}
	}
	if !ok {
		available := report.Selectable()
		names := make([]string, 0, len(available))
		for _, s := range available {
			names = append(names, s.Name)
		}
		for _, s := range skills.Filter(b.extraSkills, b.skillDisabled) {
			if s != nil {
				names = append(names, s.Name)
			}
		}
		msg := fmt.Sprintf("skill not found: %s", name)
		if len(names) > 0 {
			msg += ". available skills: " + strings.Join(names, ", ")
		}
		return marshalToolError(call, "not_found", msg), nil
	}
	missing := skills.MissingRequirements(skill, skills.ReportOptions{DisabledNames: b.skillDisabled, WorkspaceRoot: b.root})
	content, trunc := truncateTextSmart(skill.Instructions, maxToolTextChars)
	payload := map[string]any{
		"name":         skill.Name,
		"description":  skill.Description,
		"when":         skill.When,
		"path":         skill.Path,
		"skill_file":   skill.SkillFilePath,
		"instructions": content,
		"arguments":    strings.TrimSpace(in.Arguments),
		"truncation":   trunc,
		"setup_status": skills.FormatMissingRequirements(missing),
		"read_only":    true,
		"execution":    "not_executed",
		"usage_hint":   "Follow these instructions for the current task. This tool only loads skill instructions; it does not execute scripts or modify files.",
	}
	return marshalToolResult(call, map[string]any{
		"status":  "ok",
		"payload": payload,
		"summary": "loaded skill: " + skill.Name,
	})
}

func skillNameDisabled(name string, disabled []string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, candidate := range disabled {
		if strings.ToLower(strings.TrimSpace(candidate)) == name {
			return true
		}
	}
	return false
}
