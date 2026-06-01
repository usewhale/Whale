package app

import (
	"path/filepath"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/tasks"
	"github.com/usewhale/whale/internal/workflow"
)

func (a *App) rebuildTaskRuntimeLocked() error {
	if a == nil {
		return nil
	}
	effort := a.reasoningEffort
	thinking := a.thinkingEnabled
	apiKey := a.apiKey
	cfg := a.cfg
	providerFactory := func(model string, requestEffort string, maxTokens int) (llm.Provider, error) {
		if strings.TrimSpace(model) == "" {
			model = defaults.DefaultModel
		}
		effectiveEffort := normalizeEffort(core.FirstNonEmpty(strings.TrimSpace(requestEffort), effort))
		return newDeepSeekProvider(providerOptions{
			APIKey:            apiKey,
			BaseURL:           cfg.APIBaseURL,
			Model:             model,
			ReasoningEffort:   effectiveEffort,
			ThinkingEnabled:   thinking,
			MaxTokens:         maxTokens,
			RetryPolicy:       retryPolicyFromConfig(cfg),
			StreamMaxAttempts: cfg.RetryStreamMaxAttempts,
			StreamIdleTimeout: cfg.RetryStreamIdleTimeout,
		})
	}
	taskRunner := tasks.NewRunner(tasks.RunnerConfig{
		ProviderFactory:      providerFactory,
		ParentTools:          a.baseToolRegistry,
		MessageStore:         a.msgStore,
		SessionsDir:          a.sessionsDir,
		ParentSessionIDFunc:  func() string { return a.sessionID },
		WorkspaceRoot:        a.workspaceRoot,
		MemoryEnabled:        cfg.MemoryEnabled,
		MemoryMaxChars:       cfg.MemoryMaxChars,
		MemoryFileOrder:      parseCSVList(cfg.MemoryFileOrder),
		AutoCompact:          cfg.AutoCompact,
		AutoCompactThreshold: cfg.AutoCompactThreshold,
		DefaultModel:         defaults.DefaultModel,
		DefaultEffort:        effort,
		DefaultMaxTokens:     tasks.DefaultMaxTokens,
		DefaultMaxToolIters:  tasks.DefaultMaxToolIters,
		SummaryMaxChars:      tasks.DefaultSummaryMaxChar,
		UsageLogPath:         filepath.Join(cfg.DataDir, "usage.jsonl"),
		ParentPolicy:         a.permissionPolicy,
		ApprovalFunc: func(req policy.ApprovalRequest) policy.ApprovalDecision {
			a.approvalMu.Lock()
			defer a.approvalMu.Unlock()
			if a.autoAcceptPermissions {
				return policy.ApprovalAllow
			}
			return a.approvalFn(req)
		},
		AgentDefinitions: taskAgentDefinitions(a.pluginAgents),
	})
	a.taskTools = tasks.NewTools(taskRunner)
	workflowStore, err := workflow.NewFileRunEventStore(cfg.DataDir)
	if err != nil {
		return err
	}
	workflowScheduler := workflow.NewTaskScheduler(workflowStore, taskRunner)
	a.workflowManager = workflow.NewRunManager(workflowStore, workflowScheduler)
	a.workflowRunner = workflow.NewScriptRunner(cfg.DataDir, a.workflowManager)
	a.workflowRunner.Library = workflow.NewLibrary(a.workspaceRoot)
	a.workflowTools = []core.Tool{workflow.NewTool(a.workflowRunner, func() string { return a.sessionID })}
	return nil
}
