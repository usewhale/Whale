package app

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
)

func (a *App) RunUserPromptSubmitHook(input string) (blocked bool, output string, updatedInput string) {
	if a.hookRunner.Empty() {
		return false, "", input
	}
	report := a.hookRunner.Run(a.ctx, agent.NewUserPromptSubmitPayload(a.sessionID, a.workspaceRoot, input))
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
	if a.hookRunner.Empty() {
		return ""
	}
	report := a.hookRunner.Run(a.ctx, agent.NewStopPayload(a.sessionID, a.workspaceRoot, lastAssistantText, turn))
	return strings.Join(renderHookReport(report), "\n")
}

func (a *App) ensureAgent() (*agent.Agent, error) {
	if a.a == nil {
		var pluginBlocks []string
		if a.pluginManager != nil {
			pluginBlocks = a.pluginManager.StartupBlocks(a.ctx)
		}
		provider, err := newDeepSeekProvider(providerOptions{
			APIKey:            a.apiKey,
			BaseURL:           a.cfg.APIBaseURL,
			Model:             a.model,
			ReasoningEffort:   a.reasoningEffort,
			ThinkingEnabled:   a.thinkingEnabled,
			RetryPolicy:       retryPolicyFromConfig(a.cfg),
			StreamMaxAttempts: a.cfg.RetryStreamMaxAttempts,
		})
		if err != nil {
			return nil, err
		}
		a.a = agent.NewAgentWithRegistry(provider, a.msgStore, a.toolRegistry,
			agent.WithSessionMode(a.currentMode),
			agent.WithSessionsDir(a.sessionsDir),
			agent.WithBudgetWarningUSD(a.budgetWarningUSD),
			agent.WithUsageLogPath(filepath.Join(a.cfg.DataDir, "usage.jsonl")),
			agent.WithAutoCompact(a.cfg.AutoCompact, a.cfg.AutoCompactThreshold, a.contextWindow),
			agent.WithToolPolicy(policy.DefaultToolPolicy{Mode: a.approvalMode, AllowPrefixes: a.allowPrefixes, DenyPrefixes: a.denyPrefixes}),
			agent.WithHookRunner(a.hookRunner),
			agent.WithExtraSystemBlocks(pluginBlocks...),
			agent.WithProjectMemory(a.cfg.MemoryEnabled, a.cfg.MemoryMaxChars, parseCSVList(a.cfg.MemoryFileOrder), a.workspaceRoot),
			agent.WithDisabledSkills(a.cfg.SkillsDisabled),
			agent.WithExtraSkills(a.pluginManager.Skills()),
			agent.WithApprovalFunc(func(req policy.ApprovalRequest) policy.ApprovalDecision {
				a.approvalMu.Lock()
				defer a.approvalMu.Unlock()
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
		_, _ = session.PatchSessionMeta(a.sessionsDir, a.sessionID, session.SessionMeta{Title: input})
	}
	ag, err := a.ensureAgent()
	if err != nil {
		return nil, err
	}
	return ag.RunStreamWithTurnOptions(ctx, a.sessionID, input, opts)
}

func (a *App) RunTurnWithInjectedInput(ctx context.Context, visibleInput, hiddenInput string) (<-chan agent.AgentEvent, error) {
	return a.RunTurnWithInjectedInputOptions(ctx, visibleInput, hiddenInput, agent.RunOptions{})
}

func (a *App) RunTurnWithInjectedInputOptions(ctx context.Context, visibleInput, hiddenInput string, opts agent.RunOptions) (<-chan agent.AgentEvent, error) {
	if strings.TrimSpace(visibleInput) != "" {
		_, _ = session.PatchSessionMeta(a.sessionsDir, a.sessionID, session.SessionMeta{Title: visibleInput})
	}
	ag, err := a.ensureAgent()
	if err != nil {
		return nil, err
	}
	return ag.RunStreamWithInjectedInputOptions(ctx, a.sessionID, visibleInput, hiddenInput, opts)
}

func (a *App) FinalizeTurn(lastAssistantText string) error {
	meta, err := session.LoadSessionMeta(a.sessionsDir, a.sessionID)
	if err != nil {
		return nil
	}
	nextTurn := meta.TurnCount + 1
	summary := strings.TrimSpace(lastAssistantText)
	if len(summary) > 240 {
		summary = summary[:240]
	}
	_, err = session.PatchSessionMeta(a.sessionsDir, a.sessionID, session.SessionMeta{Workspace: a.workspaceRoot, Branch: a.branch, TurnCount: nextTurn, Summary: summary})
	return err
}
