package app

import (
	"fmt"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/skills"
	"path/filepath"
	"strings"
)

func (a *App) buildSkillsList() string {
	report := a.SkillReport()
	return renderSkillsReport(report)
}

func (a *App) SkillReport() skills.Report {
	roots := skills.DefaultRoots(a.workspaceRoot)
	report := skills.BuildReport(roots, skills.ReportOptions{
		DisabledNames: a.cfg.SkillsDisabled,
		MCPConnected:  a.mcpConnectedSet(),
		WorkspaceRoot: a.workspaceRoot,
	})
	if a != nil && a.pluginManager != nil {
		for _, skill := range a.pluginManager.Skills() {
			if skill == nil || reportHasSkill(report, skill.Name) {
				continue
			}
			view := skills.SkillView{
				Name:          skill.Name,
				Description:   skill.Description,
				When:          skill.When,
				Path:          skill.Path,
				SkillFilePath: skill.SkillFilePath,
				Source:        "plugin",
				Status:        skills.AvailabilityReady,
			}
			if core.SkillNameDisabled(skill.Name, a.cfg.SkillsDisabled) {
				view.Status = skills.AvailabilityDisabled
				view.Reason = "Disabled in config"
				report.Disabled = append(report.Disabled, view)
				continue
			}
			report.Ready = append(report.Ready, view)
		}
	}
	return report
}

func (a *App) SkillSuggestions() []skills.SkillView {
	return a.SkillReport().Selectable()
}

func (a *App) mcpConnectedSet() map[string]bool {
	out := map[string]bool{}
	if a == nil || a.mcpManager == nil {
		return out
	}
	for _, st := range a.mcpManager.States() {
		out[st.Name] = st.Connected
	}
	return out
}

func renderSkillsReport(report skills.Report) string {
	lines := []string{"Skills", ""}
	if len(report.Ready) == 0 && len(report.NeedsSetup) == 0 && len(report.Disabled) == 0 && len(report.Problems) == 0 {
		lines = append(lines, "no skills found", "", "roots:")
		for _, root := range report.Roots {
			lines = append(lines, "- "+root)
		}
		return strings.Join(lines, "\n")
	}
	appendSkillGroup(&lines, "Ready", report.Ready)
	appendSkillGroup(&lines, "Needs setup", report.NeedsSetup)
	appendSkillGroup(&lines, "Disabled", report.Disabled)
	appendSkillGroup(&lines, "Problems", report.Problems)
	lines = append(lines, "Use a skill with `$skill-name`. Manage skills from the TUI with `/skills`.")
	return strings.Join(lines, "\n")
}

func appendSkillGroup(lines *[]string, title string, views []skills.SkillView) {
	if len(views) == 0 {
		return
	}
	*lines = append(*lines, title)
	for _, view := range views {
		desc := strings.TrimSpace(view.Description)
		if view.Status == skills.AvailabilityNeedsSetup || view.Status == skills.AvailabilityDisabled || view.Status == skills.AvailabilityProblem {
			desc = strings.TrimSpace(view.Reason)
		}
		line := fmt.Sprintf("- `%s`", view.Name)
		if desc != "" {
			line += ": " + desc
		}
		if view.Source != "" && (view.Status == skills.AvailabilityReady || view.Status == skills.AvailabilityNeedsSetup) {
			line += " (" + view.Source + ")"
		}
		*lines = append(*lines, line)
	}
	*lines = append(*lines, "")
}

type SkillBinding struct {
	Name          string
	SkillFilePath string
}

func (a *App) buildSkillSyntheticPrompt(name, args string) (string, string, error) {
	name = strings.TrimSpace(name)
	args = strings.TrimSpace(args)
	if !skills.ValidName(name) {
		return "", "", fmt.Errorf("skill name must be alphanumeric with hyphens")
	}
	roots := skills.DefaultRoots(a.workspaceRoot)
	report := a.SkillReport()
	for _, view := range report.Disabled {
		if view.Name == name {
			return "", "", fmt.Errorf("skill disabled: %s", name)
		}
	}
	for _, view := range report.Problems {
		if view.Name == name {
			return "", "", fmt.Errorf("skill unavailable: %s: %s", name, view.Reason)
		}
	}
	skill, _, ok := skills.Find(roots, name)
	if !ok && a.pluginManager != nil {
		for _, candidate := range a.pluginManager.Skills() {
			if candidate != nil && candidate.Name == name && !core.SkillNameDisabled(name, a.cfg.SkillsDisabled) {
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
		msg := fmt.Sprintf("skill not found: %s", name)
		if len(names) > 0 {
			msg += ". available skills: " + strings.Join(names, ", ")
		}
		return "", "", fmt.Errorf("%s", msg)
	}
	return a.buildSkillSyntheticPromptForSkill(skill, args)
}

func (a *App) buildSkillSyntheticPromptFromBinding(name, args string, binding SkillBinding) (string, string, error) {
	name = strings.TrimSpace(name)
	args = strings.TrimSpace(args)
	bindingName := strings.TrimSpace(binding.Name)
	bindingPath := strings.TrimSpace(binding.SkillFilePath)
	if !skills.ValidName(name) {
		return "", "", fmt.Errorf("skill name must be alphanumeric with hyphens")
	}
	if bindingName == "" || bindingPath == "" {
		return "", "", fmt.Errorf("skill binding is incomplete")
	}
	if bindingName != name {
		return "", "", fmt.Errorf("skill binding mismatch: selected %s but prompt mentions %s", bindingName, name)
	}
	roots := skills.DefaultRoots(a.workspaceRoot)
	if a.pluginManager != nil {
		if core.SkillNameDisabled(name, a.cfg.SkillsDisabled) {
			return "", "", fmt.Errorf("skill disabled: %s", name)
		}
		for _, candidate := range a.pluginManager.Skills() {
			if candidate != nil && candidate.Name == name && sameSkillPath(candidate.SkillFilePath, bindingPath) {
				cp := *candidate
				return a.buildSkillSyntheticPromptForSkill(&cp, args)
			}
		}
		if strings.HasPrefix(bindingPath, "plugin://") {
			return "", "", fmt.Errorf("skill unavailable: %s", name)
		}
	}
	skill, _, ok := skills.FindByPath(roots, bindingPath)
	if !ok {
		return "", "", fmt.Errorf("skill unavailable: %s", name)
	}
	if skill.Name != name {
		return "", "", fmt.Errorf("skill binding mismatch: selected %s but path contains %s", name, skill.Name)
	}
	report := a.SkillReport()
	for _, view := range report.Disabled {
		if sameSkillPath(view.SkillFilePath, skill.SkillFilePath) || view.Name == name {
			return "", "", fmt.Errorf("skill disabled: %s", name)
		}
	}
	for _, view := range report.Problems {
		if sameSkillPath(view.SkillFilePath, skill.SkillFilePath) {
			return "", "", fmt.Errorf("skill unavailable: %s: %s", name, view.Reason)
		}
	}
	return a.buildSkillSyntheticPromptForSkill(skill, args)
}

func (a *App) buildSkillSyntheticPromptForSkill(skill *skills.Skill, args string) (string, string, error) {
	if skill == nil {
		return "", "", fmt.Errorf("skill unavailable")
	}
	missing := skills.MissingRequirements(skill, skills.ReportOptions{
		DisabledNames: a.cfg.SkillsDisabled,
		MCPConnected:  a.mcpConnectedSet(),
		WorkspaceRoot: a.workspaceRoot,
	})
	var b strings.Builder
	b.WriteString("Use this skill for the current turn.\n\n")
	b.WriteString("<skill>\n")
	b.WriteString("<name>")
	b.WriteString(skill.Name)
	b.WriteString("</name>\n")
	b.WriteString("<description>")
	b.WriteString(skill.Description)
	b.WriteString("</description>\n")
	if strings.TrimSpace(skill.When) != "" {
		b.WriteString("<when>")
		b.WriteString(skill.When)
		b.WriteString("</when>\n")
	}
	b.WriteString("<path>")
	b.WriteString(skill.SkillFilePath)
	b.WriteString("</path>\n")
	if len(missing) > 0 {
		b.WriteString("<setup_status>")
		b.WriteString(skills.FormatMissingRequirements(missing))
		b.WriteString("</setup_status>\n")
	}
	if args != "" {
		b.WriteString("<arguments>\n")
		b.WriteString(args)
		b.WriteString("\n</arguments>\n")
	}
	b.WriteString("<instructions>\n")
	b.WriteString(skill.Instructions)
	b.WriteString("\n</instructions>\n")
	b.WriteString("</skill>")
	out := "loaded skill: " + skill.Name
	if len(missing) > 0 {
		out += " (" + skills.FormatMissingRequirements(missing) + ")"
	}
	return out, strings.TrimSpace(b.String()), nil
}

func (a *App) BuildSkillMentionSyntheticPrompt(line string) (bool, string, string, error) {
	return a.BuildSkillMentionSyntheticPromptWithBinding(line, nil)
}

func (a *App) BuildSkillMentionSyntheticPromptWithBinding(line string, binding *SkillBinding) (bool, string, string, error) {
	name, args, ok := parseSkillMention(line)
	if !ok {
		return false, "", "", nil
	}
	var out, synthetic string
	var err error
	if binding != nil {
		out, synthetic, err = a.buildSkillSyntheticPromptFromBinding(name, args, *binding)
	} else {
		out, synthetic, err = a.buildSkillSyntheticPrompt(name, args)
	}
	if err != nil {
		return true, "", "", err
	}
	return true, out, synthetic, nil
}

func sameSkillPath(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr == nil {
		left = leftAbs
	}
	if rightErr == nil {
		right = rightAbs
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func (a *App) setSkillDisabled(name string, disabled bool) (string, error) {
	name = strings.TrimSpace(name)
	if !skills.ValidName(name) {
		return "", fmt.Errorf("skill name must be alphanumeric with hyphens")
	}
	report := a.SkillReport()
	if !reportHasSkill(report, name) {
		names := reportSkillNames(report)
		msg := fmt.Sprintf("skill not found: %s", name)
		if len(names) > 0 {
			msg += ". available skills: " + strings.Join(names, ", ")
		}
		return "", fmt.Errorf("%s", msg)
	}

	path := ProjectLocalConfigPath(a.workspaceRoot)
	cfg, _, err := LoadConfigFile(path)
	if err != nil {
		return "", err
	}
	before := disabledNameSet(cfg.Skills.Disabled)
	enabledSet := disabledNameSet(cfg.Skills.Enabled)
	if disabled {
		before[strings.ToLower(name)] = name
		delete(enabledSet, strings.ToLower(name))
	} else {
		delete(before, strings.ToLower(name))
		enabledSet[strings.ToLower(name)] = name
	}
	cfg.Skills.Disabled = sortedSkillNames(before)
	cfg.Skills.Enabled = sortedSkillNames(enabledSet)
	if err := SaveConfigFile(path, cfg); err != nil {
		return "", err
	}
	if err := a.reloadSkillDisabledConfig(); err != nil {
		return "", err
	}
	if disabled {
		return fmt.Sprintf("disabled skill: %s\nconfig: %s", name, path), nil
	}
	return fmt.Sprintf("enabled skill: %s\nconfig: %s", name, path), nil
}

func (a *App) SetSkillEnabled(name string, enabled bool) (string, error) {
	return a.setSkillDisabled(name, !enabled)
}
