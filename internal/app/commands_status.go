package app

import (
	"fmt"
	"path/filepath"
	"strings"
)

func (a *App) buildStatus() string {
	parts := []string{
		"Status",
		"",
		fmt.Sprintf("- session: %s", a.sessionID),
		fmt.Sprintf("- mode: %s", modeDisplay(a.currentMode)),
		fmt.Sprintf("- permissions.default: %s", a.permissionPolicy.Default),
		fmt.Sprintf("- model: %s", a.model),
		fmt.Sprintf("- effort: %s", a.reasoningEffort),
		fmt.Sprintf("- thinking: %s", OnOff(a.thinkingEnabled)),
	}
	parts = append(parts, a.formatCurrentWorktreeStatusLines()...)
	parts = append(parts, formatContextWindowStatus(a))
	parts = append(parts, "- usage: "+a.sessionUsageStatusValue())
	parts = append(parts, a.formatBudgetStatusLine())
	return strings.Join(parts, "\n")
}

func (a *App) buildStatusLocalResult() *LocalResult {
	text := a.buildStatus()
	fields := []LocalResultField{
		{Label: "Session", Value: a.sessionID},
		{Label: "Mode", Value: modeDisplay(a.currentMode), Tone: "info"},
		{Label: "Permissions", Value: string(a.permissionPolicy.Default)},
		{Label: "Model", Value: a.model, Tone: "info"},
		{Label: "Effort", Value: a.reasoningEffort},
		{Label: "Thinking", Value: OnOff(a.thinkingEnabled)},
	}
	if strings.TrimSpace(a.worktree.Name) != "" {
		fields = append(fields,
			LocalResultField{Label: "Worktree", Value: a.worktree.Name, Tone: "info"},
			LocalResultField{Label: "Worktree branch", Value: valueOrDash(a.worktree.Branch)},
			LocalResultField{Label: "Worktree path", Value: valueOrDash(a.worktree.Path)},
			LocalResultField{Label: "Original workspace", Value: valueOrDash(a.worktree.OriginalWorkspace)},
			LocalResultField{Label: "Original branch", Value: valueOrDash(a.worktree.OriginalBranch)},
		)
	}
	fields = append(fields,
		LocalResultField{Label: "Context window", Value: contextWindowStatusValue(a)},
		LocalResultField{Label: "Usage", Value: a.sessionUsageStatusValue()},
		LocalResultField{Label: "Budget limit", Value: budgetStatusValue(a)},
	)
	return &LocalResult{
		Kind:      "status",
		Title:     "Status",
		Fields:    fields,
		PlainText: text,
	}
}

func (a *App) formatCurrentWorktreeStatusLines() []string {
	if strings.TrimSpace(a.worktree.Name) == "" {
		return nil
	}
	return []string{
		"- worktree: " + a.worktree.Name,
		"- worktree.branch: " + valueOrDash(a.worktree.Branch),
		"- worktree.path: " + valueOrDash(a.worktree.Path),
		"- worktree.original_workspace: " + valueOrDash(a.worktree.OriginalWorkspace),
		"- worktree.original_branch: " + valueOrDash(a.worktree.OriginalBranch),
	}
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

func budgetStatusValue(a *App) string {
	if a == nil || a.budgetWarningUSD <= 0 {
		return "disabled"
	}
	return fmt.Sprintf("$%.4f", a.budgetWarningUSD)
}

func (a *App) sessionUsageStatusValue() string {
	if a == nil {
		return "none"
	}
	dataDir := strings.TrimSpace(a.cfg.DataDir)
	if dataDir == "" {
		return "none"
	}
	return formatSessionUsageSummary(readSessionUsageSummary(filepath.Join(dataDir, "usage.jsonl"), a.sessionID))
}
