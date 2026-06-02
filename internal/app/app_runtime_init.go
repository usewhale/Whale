package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/tasks"
	"github.com/usewhale/whale/internal/workflow"
)

func initAppRuntime(cfg Config, sessionInit appSessionInit, toolInit appToolInit, workspaceRoot string, parentSessionIDFunc func() string, approvalFunc policy.ApprovalFunc) (appRuntimeInit, error) {
	model := core.FirstNonEmpty(strings.TrimSpace(cfg.Model), defaults.DefaultModel)
	effort := normalizeEffort(core.FirstNonEmpty(strings.TrimSpace(cfg.ReasoningEffort), defaults.DefaultReasoningEffort))
	viewMode, err := NormalizeViewMode(cfg.ViewMode)
	if err != nil {
		return appRuntimeInit{}, err
	}
	cfg.ViewMode = viewMode
	thinking := cfg.ThinkingEnabled
	contextWindow := contextWindowForModel(model)
	apiKey, err := LoadDeepSeekAPIKey(cfg.DataDir)
	if err != nil {
		return appRuntimeInit{}, fmt.Errorf("load api key failed: %w", err)
	}
	toolInit.toolset.SetWebFetchExtractor(newDeepSeekWebFetchExtractor(webFetchExtractorOptions{
		APIKey:  apiKey,
		BaseURL: cfg.APIBaseURL,
	}))
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
		ParentTools:          toolInit.baseToolRegistry,
		MessageStore:         sessionInit.msgStore,
		SessionsDir:          sessionInit.sessionsDir,
		ParentSessionID:      sessionInit.sessionID,
		ParentSessionIDFunc:  parentSessionIDFunc,
		WorkspaceRoot:        workspaceRoot,
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
		ApprovalFunc:         approvalFunc,
		ParentPolicy:         policy.RulePolicy{Default: cfg.PermissionDefault, Rules: append([]policy.PermissionRule{}, cfg.PermissionRules...), WorkspaceRoot: workspaceRoot},
		AgentDefinitions:     taskAgentDefinitions(toolInit.pluginAgents),
	})
	taskTools := tasks.NewTools(taskRunner)
	goalTools := newGoalTools(cfg.DataDir, sessionInit.sessionsDir, parentSessionIDFunc)
	workflowStore, err := workflow.NewFileRunEventStore(cfg.DataDir)
	if err != nil {
		return appRuntimeInit{}, fmt.Errorf("init workflow event store failed: %w", err)
	}
	workflowScheduler := workflow.NewTaskScheduler(workflowStore, taskRunner)
	workflowManager := workflow.NewRunManager(workflowStore, workflowScheduler)
	workflowRunner := workflow.NewScriptRunner(cfg.DataDir, workflowManager)
	workflowRunner.Library = workflow.NewLibrary(workspaceRoot)
	workflowTools := []core.Tool{workflow.NewTool(workflowRunner, parentSessionIDFunc)}
	registeredTools := append([]core.Tool{}, toolInit.baseTools...)
	registeredTools = append(registeredTools, toolInit.pluginTools...)
	registeredTools = append(registeredTools, taskTools...)
	registeredTools = append(registeredTools, goalTools...)
	registeredTools = append(registeredTools, workflowTools...)
	toolRegistry, err := core.NewToolRegistryChecked(registeredTools)
	if err != nil {
		return appRuntimeInit{}, fmt.Errorf("init tool registry failed: %w", err)
	}
	return appRuntimeInit{
		cfg:             cfg,
		model:           model,
		effort:          effort,
		thinking:        thinking,
		contextWindow:   contextWindow,
		apiKey:          apiKey,
		taskTools:       taskTools,
		goalTools:       goalTools,
		workflowTools:   workflowTools,
		workflowManager: workflowManager,
		workflowRunner:  workflowRunner,
		toolRegistry:    toolRegistry,
	}, nil
}
