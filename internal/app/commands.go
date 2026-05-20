package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/agent"
	appcommands "github.com/usewhale/whale/internal/app/commands"
	"github.com/usewhale/whale/internal/compact"
	"github.com/usewhale/whale/internal/plugins"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/skills"
	"github.com/usewhale/whale/internal/store"
	whaleworktree "github.com/usewhale/whale/internal/worktree"
)

func resolveInitialSessionID(sessionsDir string) (string, error) {
	recent, err := store.MostRecentSessionID(sessionsDir)
	if err == nil && recent != "" {
		return recent, nil
	}
	return "default", nil
}

func newSessionID(now time.Time) string {
	return appcommands.NewSessionID(now)
}

func resolveCLIResumeID(args []string) (string, bool, error) {
	if len(args) == 0 {
		return "", false, nil
	}
	if args[0] != "resume" {
		return "", false, nil
	}
	if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
		return "", true, fmt.Errorf("usage: whale resume <id>")
	}
	return strings.TrimSpace(args[1]), true, nil
}

func handleCommand(line, currentSessionID string, now time.Time) (appcommands.Result, error) {
	return appcommands.Parse(line, currentSessionID, now)
}

func planPromptFromSlash(line string) (string, bool) {
	return appcommands.PlanPromptFromSlash(line)
}

func (a *App) buildStatus() string {
	parts := []string{
		"Status",
		"",
		fmt.Sprintf("- session: %s", a.sessionID),
		fmt.Sprintf("- mode: %s", modeDisplay(a.currentMode)),
		fmt.Sprintf("- approval: %s", approvalModeDisplay(a.approvalMode)),
		fmt.Sprintf("- model: %s", a.model),
		fmt.Sprintf("- effort: %s", a.reasoningEffort),
		fmt.Sprintf("- thinking: %s", onOff(a.thinkingEnabled)),
		fmt.Sprintf("- view: %s", a.ViewMode()),
	}
	if strings.TrimSpace(a.worktree.Name) != "" {
		parts = append(parts, fmt.Sprintf("- worktree: %s (%s)", a.worktree.Name, a.worktree.Path))
	}
	parts = append(parts, formatContextWindowStatus(a))
	parts = append(parts, a.formatBudgetStatusLine())
	return strings.Join(parts, "\n")
}

func (a *App) buildWorktreeStatus(args []string) (string, error) {
	if len(args) == 0 {
		if strings.TrimSpace(a.worktree.Name) == "" {
			return "Worktree\n\ncurrent: none", nil
		}
		return strings.Join([]string{
			"Worktree",
			"",
			"current: " + a.worktree.Name,
			"branch: " + valueOrDash(a.worktree.Branch),
			"path: " + a.worktree.Path,
			"original workspace: " + valueOrDash(a.worktree.OriginalWorkspace),
		}, "\n"), nil
	}
	switch args[0] {
	case "list":
		items, err := whaleworktree.List(a.workspaceRoot)
		if err != nil {
			return "", err
		}
		return formatWorktreeList(items), nil
	case "status":
		if len(args) == 1 {
			return a.buildWorktreeStatus(nil)
		}
		item, err := whaleworktree.Status(a.workspaceRoot, args[1])
		if err != nil {
			return "", err
		}
		return formatWorktreeDetail(item), nil
	default:
		return "", fmt.Errorf("usage: /worktree [list|status [name]|remove <name> [--force]]")
	}
}

func (a *App) removeWorktree(args []string) (string, error) {
	if len(args) != 2 && len(args) != 3 {
		return "", fmt.Errorf("usage: /worktree remove <name> [--force]")
	}
	if args[0] != "remove" {
		return "", fmt.Errorf("usage: /worktree remove <name> [--force]")
	}
	force := false
	if len(args) == 3 {
		if args[2] != "--force" {
			return "", fmt.Errorf("usage: /worktree remove <name> [--force]")
		}
		force = true
	}
	res, err := whaleworktree.Remove(a.workspaceRoot, args[1], force)
	if err != nil {
		return "", err
	}
	lines := []string{
		"Removed worktree",
		"",
		"name: " + res.Entry.Name,
		"path: " + res.Entry.Path,
	}
	if res.BranchDeleted {
		lines = append(lines, "deleted branch: "+whaleworktree.BranchName(args[1]))
	} else if strings.TrimSpace(res.BranchWarning) != "" {
		lines = append(lines, "branch warning: "+res.BranchWarning)
	}
	return strings.Join(lines, "\n"), nil
}

func formatWorktreeList(items []whaleworktree.Entry) string {
	if len(items) == 0 {
		return "Worktrees\n\nnone"
	}
	lines := []string{"Worktrees", ""}
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("- %s: %s %s %s", item.Name, worktreeStatusLabel(item), valueOrDash(item.Head), item.Path))
	}
	return strings.Join(lines, "\n")
}

func formatWorktreeDetail(item whaleworktree.Entry) string {
	return strings.Join([]string{
		"Worktree",
		"",
		"name: " + item.Name,
		"branch: " + item.Branch,
		"head: " + valueOrDash(item.Head),
		"status: " + worktreeStatusLabel(item),
		"path: " + item.Path,
	}, "\n")
}

func worktreeStatusLabel(item whaleworktree.Entry) string {
	if item.Missing {
		return "missing"
	}
	if item.Dirty {
		return "dirty"
	}
	return "clean"
}

func valueOrDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return strings.TrimSpace(v)
}

func (a *App) formatBudgetStatusLine() string {
	if a == nil || a.budgetWarningUSD <= 0 {
		return "- budget limit: disabled"
	}
	return fmt.Sprintf("- budget limit: $%.4f", a.budgetWarningUSD)
}

func (a *App) buildMCPStatus() string {
	if a == nil || a.mcpManager == nil {
		return "MCP\n\nconfig: unavailable\nservers: none"
	}
	lines := []string{"MCP", "", fmt.Sprintf("config: %s", a.mcpManager.ConfigPath())}
	states := a.mcpManager.States()
	if len(states) == 0 {
		lines = append(lines, "servers: none")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, fmt.Sprintf("servers: %d", len(states)))
	for _, st := range states {
		status := st.Status
		if status == "" {
			status = "disabled"
			if st.Connected {
				status = "connected"
			} else if st.Error != "" {
				status = "failed"
			}
		}
		line := fmt.Sprintf("- %s: %s", st.Name, status)
		if st.Tools > 0 {
			line += fmt.Sprintf(" (%d tool(s))", st.Tools)
		}
		lines = append(lines, line)
		if st.Error != "" {
			lines = append(lines, "  error: "+st.Error)
		}
	}
	return strings.Join(lines, "\n")
}

func (a *App) PluginStatuses() []plugins.PluginStatus {
	if a == nil || a.pluginManager == nil {
		return nil
	}
	return a.pluginManager.Statuses()
}

func modeDisplay(mode session.Mode) string {
	if mode == session.ModeAsk {
		return "ask"
	}
	if mode == session.ModePlan {
		return "plan"
	}
	return "agent"
}

func modeTitle(mode session.Mode) string {
	if mode == session.ModeAsk {
		return "Ask"
	}
	if mode == session.ModePlan {
		return "Plan"
	}
	return "Agent"
}

func approvalModeDisplay(mode policy.ApprovalMode) string {
	switch mode {
	case policy.ApprovalModeNever:
		return "auto approve"
	default:
		return "ask first"
	}
}

func formatContextWindowStatus(a *App) string {
	msgs, err := a.msgStore.List(a.ctx, a.sessionID)
	if err != nil {
		return "- context window: unavailable"
	}
	used := compact.EstimateMessagesTokens(msgs)
	window := a.contextWindow
	if window < 1 {
		window = 1
	}
	leftPct := 100 - (used*100)/window
	if leftPct < 0 {
		leftPct = 0
	}
	return fmt.Sprintf("- context window: %d%% left (%s used / %s)", leftPct, formatTokenCount(used), formatTokenCount(window))
}

func formatTokenCount(v int) string {
	if v >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(v)/1_000_000.0)
	}
	if v >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(v)/1_000.0)
	}
	return fmt.Sprintf("%d", v)
}

func (a *App) initMemory() (string, error) {
	path := filepath.Join(a.workspaceRoot, "AGENTS.md")
	if _, err := os.Stat(path); err == nil {
		return fmt.Sprintf("AGENTS.md already exists at %s. Skipping /init to avoid overwriting it.", path), nil
	}
	return "", nil
}

func buildInitSyntheticPrompt() string {
	return strings.TrimSpace(`Generate a file named AGENTS.md that serves as a contributor guide for this repository.
Your goal is to produce a clear, concise, and well-structured document with descriptive headings and actionable explanations for each section.
Follow the outline below, but adapt as needed — add sections if relevant, and omit those that do not apply to this project.

Document Requirements

- Title the document "Repository Guidelines".
- Use Markdown headings (#, ##, etc.) for structure.
- Keep the document concise. 200-400 words is optimal.
- Keep explanations short, direct, and specific to this repository.
- Provide examples where helpful (commands, directory paths, naming patterns).
- Maintain a professional, instructional tone.

Recommended Sections

Project Structure & Module Organization

- Outline the project structure, including where the source code, tests, and assets are located.

Build, Test, and Development Commands

- List key commands for building, testing, and running locally (e.g., npm test, make build).
- Briefly explain what each command does.

Coding Style & Naming Conventions

- Specify indentation rules, language-specific style preferences, and naming patterns.
- Include any formatting or linting tools used.

Testing Guidelines

- Identify testing frameworks and coverage requirements.
- State test naming conventions and how to run tests.

Commit & Pull Request Guidelines

- Summarize commit message conventions found in the project’s Git history.
- Outline pull request requirements (descriptions, linked issues, screenshots, etc.).

(Optional) Add other sections if relevant, such as Security & Configuration Tips, Architecture Overview, or Agent-Specific Instructions.`)
}

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
			if skillNameDisabled(skill.Name, a.cfg.SkillsDisabled) {
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
			if candidate != nil && candidate.Name == name && !skillNameDisabled(name, a.cfg.SkillsDisabled) {
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
	if strings.HasPrefix(bindingPath, "plugin://") && a.pluginManager != nil {
		if skillNameDisabled(name, a.cfg.SkillsDisabled) {
			return "", "", fmt.Errorf("skill disabled: %s", name)
		}
		for _, candidate := range a.pluginManager.Skills() {
			if candidate != nil && candidate.Name == name && candidate.SkillFilePath == bindingPath {
				cp := *candidate
				return a.buildSkillSyntheticPromptForSkill(&cp, args)
			}
		}
		return "", "", fmt.Errorf("skill unavailable: %s", name)
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

func (a *App) SetPluginEnabled(id string, enabled bool) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("plugin id must not be empty")
	}
	if a == nil || a.pluginManager == nil {
		return "", fmt.Errorf("plugins unavailable")
	}
	st, ok := a.pluginManager.Status(id)
	if !ok {
		return "", fmt.Errorf("plugin not found: %s", id)
	}
	id = st.Manifest.ID

	path := ProjectLocalConfigPath(a.workspaceRoot)
	cfg, _, err := LoadConfigFile(path)
	if err != nil {
		return "", err
	}
	disabled := disabledNameSet(cfg.Plugins.Disabled)
	enabledSet := disabledNameSet(cfg.Plugins.Enabled)
	if enabled {
		delete(disabled, strings.ToLower(id))
		enabledSet[strings.ToLower(id)] = id
	} else {
		disabled[strings.ToLower(id)] = id
		delete(enabledSet, strings.ToLower(id))
	}
	cfg.Plugins.Disabled = sortedSkillNames(disabled)
	cfg.Plugins.Enabled = sortedSkillNames(enabledSet)
	if err := SaveConfigFile(path, cfg); err != nil {
		return "", err
	}
	if err := a.reloadPluginDisabledConfig(); err != nil {
		return "", err
	}
	a.pluginManager = plugins.NewManager(plugins.Context{DataDir: a.cfg.DataDir, WorkspaceRoot: a.workspaceRoot}, a.cfg.PluginsDisabled)
	a.pluginTools = a.pluginManager.Tools()
	if a.toolset != nil {
		a.toolset.SetExtraSkills(a.pluginManager.Skills())
	}
	a.hookRunner = agent.NewHookRunner(a.hooks, a.workspaceRoot)
	a.hookRunner.AddHandlers(a.pluginManager.Hooks()...)
	if err := a.refreshMCPTools(); err != nil {
		return "", err
	}
	a.a = nil
	if enabled {
		return fmt.Sprintf("enabled plugin: %s\nconfig: %s", id, path), nil
	}
	return fmt.Sprintf("disabled plugin: %s\nconfig: %s", id, path), nil
}

func (a *App) reloadSkillDisabledConfig() error {
	if a == nil {
		return nil
	}
	loaded, err := LoadConfigFiles(a.cfg.DataDir, a.workspaceRoot)
	if err != nil {
		return err
	}
	cfg := Config{}
	if err := ApplyLoadedConfig(&cfg, loaded); err != nil {
		return err
	}
	a.cfg.SkillsDisabled = trimList(cfg.SkillsDisabled)
	if a.toolset != nil {
		a.toolset.SetSkillDisabled(a.cfg.SkillsDisabled)
	}
	a.a = nil
	return nil
}

func (a *App) reloadPluginDisabledConfig() error {
	if a == nil {
		return nil
	}
	loaded, err := LoadConfigFiles(a.cfg.DataDir, a.workspaceRoot)
	if err != nil {
		return err
	}
	cfg := Config{}
	ApplyLoadedConfig(&cfg, loaded)
	a.cfg.PluginsDisabled = trimList(cfg.PluginsDisabled)
	return nil
}

func reportHasSkill(report skills.Report, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, view := range allReportSkills(report) {
		if strings.ToLower(view.Name) == name {
			return true
		}
	}
	return false
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

func reportSkillNames(report skills.Report) []string {
	seen := map[string]bool{}
	var out []string
	for _, view := range allReportSkills(report) {
		name := strings.TrimSpace(view.Name)
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func allReportSkills(report skills.Report) []skills.SkillView {
	var out []skills.SkillView
	out = append(out, report.Ready...)
	out = append(out, report.NeedsSetup...)
	out = append(out, report.Disabled...)
	out = append(out, report.Problems...)
	return out
}

func disabledNameSet(names []string) map[string]string {
	out := map[string]string{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out[strings.ToLower(name)] = name
	}
	return out
}

func sortedSkillNames(names map[string]string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func mergeNames(existing, add []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(existing)+len(add))
	for _, name := range existing {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, trimmed)
	}
	for _, name := range add {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, trimmed)
	}
	return out
}

func removeNames(names, remove []string) []string {
	removeSet := disabledNameSet(remove)
	if len(removeSet) == 0 {
		return trimList(names)
	}
	var out []string
	seen := map[string]bool{}
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := removeSet[key]; ok || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, trimmed)
	}
	return out
}

func parseSkillMention(line string) (name, args string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "$") {
		return "", "", false
	}
	head := trimmed
	if idx := strings.IndexAny(trimmed, " \t\n"); idx >= 0 {
		head = trimmed[:idx]
		args = strings.TrimSpace(trimmed[idx:])
	}
	name = strings.TrimPrefix(head, "$")
	if !skills.ValidName(name) {
		return "", "", false
	}
	return name, args, true
}

func parseCSVList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
