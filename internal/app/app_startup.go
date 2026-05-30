package app

import (
	"fmt"
	"github.com/usewhale/whale/internal/session"
	"strings"
)

func (a *App) StartupLines() []string {
	lines := []string{"whale repl", fmt.Sprintf("session: %s", a.sessionID), fmt.Sprintf("mode: %s", a.currentMode), fmt.Sprintf("permissions.default: %s", a.permissionPolicy.Default)}
	lines = append(lines, fmt.Sprintf("model: %s", a.model), fmt.Sprintf("effort: %s", a.reasoningEffort), fmt.Sprintf("thinking: %s", OnOff(a.thinkingEnabled)), fmt.Sprintf("view: %s", a.ViewMode()))
	if a.budgetWarningUSD > 0 {
		lines = append(lines, fmt.Sprintf("budget.session_limit_usd: %.4f", a.budgetWarningUSD))
	} else {
		lines = append(lines, "budget.session_limit_usd: disabled")
	}
	if len(a.hookSources) > 0 {
		lines = append(lines, fmt.Sprintf("hooks: %s", strings.Join(a.hookSources, ", ")))
	}
	if strings.TrimSpace(a.worktree.Name) != "" {
		lines = append(lines, fmt.Sprintf("worktree: %s (%s)", a.worktree.Name, a.worktree.Path))
	}
	if a.mcpManager != nil {
		states := a.mcpManager.States()
		if len(states) > 0 {
			connected := 0
			failed := 0
			for _, st := range states {
				if st.Connected {
					connected++
				} else if st.Error != "" {
					failed++
				}
			}
			lines = append(lines, fmt.Sprintf("mcp: %d server(s), %d connected, %d failed", len(states), connected, failed))
		}
	}
	lines = append(lines, "commands: "+CommandsHelp, "env: DEEPSEEK_API_KEY=...")
	if ust, err := session.LoadUserInputState(a.sessionsDir, a.sessionID); err == nil && ust.Pending {
		lines = append(lines, fmt.Sprintf("pending user input: tool_call=%s questions=%d", ust.ToolCallID, len(ust.Questions)))
	}
	return lines
}

func (a *App) baseSessionMeta() session.SessionMeta {
	meta := session.SessionMeta{Workspace: a.workspaceRoot, Branch: a.branch}
	if strings.TrimSpace(a.worktree.Name) != "" {
		meta.WorktreeName = a.worktree.Name
		meta.WorktreePath = a.worktree.Path
		meta.WorktreeBranch = a.worktree.Branch
		meta.OriginalWorkspace = a.worktree.OriginalWorkspace
		meta.OriginalBranch = a.worktree.OriginalBranch
		meta.OriginalHeadCommit = a.worktree.OriginalHeadCommit
	}
	return meta
}
