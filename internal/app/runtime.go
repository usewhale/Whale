package app

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/workflow"
)

func (a *App) RunUserPromptSubmitHook(input string) (blocked bool, output string, updatedInput string) {
	return a.RunUserPromptSubmitHookWithObserver(input, nil)
}

func (a *App) RunUserPromptSubmitHookWithObserver(input string, observer agent.HookRunObserver) (blocked bool, output string, updatedInput string) {
	if a == nil || a.hookRunner == nil || a.hookRunner.Empty() {
		return false, "", input
	}
	report := a.hookRunner.RunHookWithObserver(a.ctx, agent.NewUserPromptSubmitPayload(a.sessionID, a.workspaceRoot, input), observer)
	lines := renderHookReport(report)
	if report.Blocked {
		lines = append(lines, "assistant> blocked by UserPromptSubmit hook")
	}
	updatedInput = input
	if strings.TrimSpace(report.UpdatedInput) != "" {
		updatedInput = strings.TrimSpace(report.UpdatedInput)
	}
	return report.Blocked, strings.Join(lines, "\n"), updatedInput
}

func (a *App) RunStopHook(lastAssistantText string, turn int) string {
	return a.RunStopHookWithObserver(lastAssistantText, turn, nil)
}

func (a *App) RunStopHookWithObserver(lastAssistantText string, turn int, observer agent.HookRunObserver) string {
	if a.hookRunner.Empty() {
		return ""
	}
	report := a.hookRunner.RunHookWithObserver(a.ctx, agent.NewStopPayload(a.sessionID, a.workspaceRoot, lastAssistantText, turn), observer)
	return strings.Join(renderHookReport(report), "\n")
}

func (a *App) RunSessionStartHook(observer agent.HookRunObserver) string {
	if a == nil || a.hookRunner == nil || a.hookRunner.Empty() {
		return ""
	}
	report := a.hookRunner.RunHookWithObserver(a.ctx, agent.NewSessionStartPayload(a.sessionID, a.workspaceRoot), observer)
	return strings.Join(renderHookReport(report), "\n")
}

func (a *App) RunPermissionRequestHook(req policy.ApprovalRequest, observer agent.HookRunObserver) (blocked bool, output string) {
	if a == nil || a.hookRunner == nil || a.hookRunner.Empty() {
		return false, ""
	}
	payload := agent.NewPermissionRequestPayload(req.SessionID, a.workspaceRoot, req.ToolCall, req.Reason, req.Code)
	report := a.hookRunner.RunHookWithObserver(a.ctx, payload, observer)
	lines := renderHookReport(report)
	if report.Blocked {
		lines = append(lines, "assistant> blocked by PermissionRequest hook")
	}
	return report.Blocked, strings.Join(lines, "\n")
}

func (a *App) RunSubagentHook(event agent.HookEvent, info *agent.TaskActivityInfo, observer agent.HookRunObserver) string {
	if a == nil || a.hookRunner == nil || a.hookRunner.Empty() || info == nil {
		return ""
	}
	payload := agent.NewSubagentHookPayload(event, a.sessionID, a.workspaceRoot, info.Role, info.Model, info.Summary)
	report := a.hookRunner.RunHookWithObserver(a.ctx, payload, observer)
	return strings.Join(renderHookReport(report), "\n")
}

func (a *App) ensureAgent() (*agent.Agent, error) {
	if a.a == nil {
		var pluginBlocks []string
		if a.pluginManager != nil {
			pluginBlocks = a.pluginManager.StartupBlocks(a.ctx)
		}
		provider, err := newDeepSeekProvider(providerOptions{
			APIKey:                   a.apiKey,
			BaseURL:                  a.cfg.APIBaseURL,
			Model:                    a.model,
			ReasoningEffort:          a.reasoningEffort,
			ThinkingEnabled:          a.thinkingEnabled,
			RetryPolicy:              retryPolicyFromConfig(a.cfg),
			StreamMaxAttempts:        a.cfg.RetryStreamMaxAttempts,
			StreamIdleTimeout:        a.cfg.RetryStreamIdleTimeout,
			DeepSeekPrefixCompletion: a.cfg.DeepSeekPrefixCompletion,
		})
		if err != nil {
			return nil, err
		}
		a.a = agent.NewAgentWithRegistry(provider, a.msgStore, a.toolRegistry,
			agent.WithSessionMode(a.currentMode),
			agent.WithSessionsDir(a.sessionsDir),
			agent.WithBudgetWarningUSD(a.budgetWarningUSD),
			agent.WithUsageLogPath(filepath.Join(a.cfg.DataDir, "usage.jsonl")),
			agent.WithCheckpoints(a.checkpoints),
			agent.WithAutoCompact(a.cfg.AutoCompact, a.cfg.AutoCompactThreshold, a.contextWindow),
			agent.WithToolPolicy(a.permissionPolicy),
			agent.WithToolRefresh(func(context.Context) error {
				return a.refreshMCPTools()
			}),
			agent.WithHookRunner(a.hookRunner),
			agent.WithExtraSystemBlocks(pluginBlocks...),
			agent.WithDynamicSystemBlocks(func() string {
				if a.workflowRunner == nil || a.workflowRunner.Library == nil {
					return ""
				}
				return workflow.RenderPromptCatalog(context.Background(), a.workflowRunner.Library, workflow.DefaultPromptCatalogLimit)
			}),
			agent.WithProjectMemory(a.cfg.MemoryEnabled, a.cfg.MemoryMaxChars, parseCSVList(a.cfg.MemoryFileOrder), a.workspaceRoot),
			agent.WithWorktreeContext(a.worktree.Path, a.worktree.OriginalWorkspace),
			agent.WithMaxParallelSubagents(a.cfg.MaxParallelSubagents),
			agent.WithDisabledSkills(a.cfg.SkillsDisabled),
			agent.WithExtraSkills(a.pluginManager.Skills()),
			agent.WithApprovalFunc(func(req policy.ApprovalRequest) policy.ApprovalDecision {
				a.approvalMu.Lock()
				defer a.approvalMu.Unlock()
				if a.autoAcceptPermissions {
					return policy.ApprovalAllow
				}
				return a.approvalFn(req)
			}),
			agent.WithUserInputFunc(a.userInput),
		)
	}
	return a.a, nil
}

func (a *App) RunTurn(ctx context.Context, input string, hiddenInput bool) (<-chan agent.AgentEvent, error) {
	return a.RunTurnWithOptions(ctx, input, agent.RunOptions{HiddenInput: hiddenInput})
}

func (a *App) RunTurnWithOptions(ctx context.Context, input string, opts agent.RunOptions) (<-chan agent.AgentEvent, error) {
	if !opts.HiddenInput && strings.TrimSpace(input) != "" {
		_, _ = session.PatchSessionMeta(a.sessionsDir, a.sessionID, session.SessionMetaPatch{Title: input})
	}
	a.pendingGoalTurn = opts.GoalContinuation
	opts = a.applyRunOptionsDefaults(opts)
	ag, err := a.ensureAgent()
	if err != nil {
		a.pendingGoalTurn = false
		return nil, err
	}
	return ag.RunStreamWithTurnOptions(ctx, a.sessionID, input, opts)
}

func (a *App) RunTurnWithInjectedInput(ctx context.Context, visibleInput, hiddenInput string) (<-chan agent.AgentEvent, error) {
	return a.RunTurnWithInjectedInputOptions(ctx, visibleInput, hiddenInput, agent.RunOptions{})
}

func (a *App) RunTurnWithInjectedInputOptions(ctx context.Context, visibleInput, hiddenInput string, opts agent.RunOptions) (<-chan agent.AgentEvent, error) {
	if strings.TrimSpace(visibleInput) != "" {
		_, _ = session.PatchSessionMeta(a.sessionsDir, a.sessionID, session.SessionMetaPatch{Title: visibleInput})
	}
	opts = a.applyRunOptionsDefaults(opts)
	ag, err := a.ensureAgent()
	if err != nil {
		return nil, err
	}
	return ag.RunStreamWithInjectedInputOptions(ctx, a.sessionID, visibleInput, hiddenInput, opts)
}

func (a *App) applyRunOptionsDefaults(opts agent.RunOptions) agent.RunOptions {
	return opts
}

func (a *App) FinalizeTurn(lastAssistantText string, completed bool) error {
	if err := a.finalizeGoalTurn(lastAssistantText, completed); err != nil {
		return err
	}
	meta, err := session.LoadSessionMeta(a.sessionsDir, a.sessionID)
	if err != nil {
		return nil
	}
	nextTurn := meta.TurnCount + 1
	summary := strings.TrimSpace(lastAssistantText)
	if len(summary) > 240 {
		summary = summary[:240]
	}
	_, err = session.PatchSessionMeta(a.sessionsDir, a.sessionID, session.SessionMetaPatch{Workspace: a.workspaceRoot, Branch: a.branch, TurnCount: &nextTurn, Summary: summary})
	return err
}

func (a *App) finalizeGoalTurn(lastAssistantText string, completed bool) error {
	if a == nil || !a.pendingGoalTurn {
		return nil
	}
	a.pendingGoalTurn = false
	if !completed {
		return nil
	}
	st, ok, err := session.LoadGoalState(a.sessionsDir, a.sessionID)
	if err != nil || !ok {
		return err
	}
	if st.Status != session.GoalStatusCompleted {
		return nil
	}
	st = refreshCompletedGoalUsageWithTotal(st, a.currentSessionGoalTokens())
	return session.SaveGoalState(a.sessionsDir, a.sessionID, st)
}
