package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/llm"
	llmretry "github.com/usewhale/whale/internal/llm/retry"
	whalemcp "github.com/usewhale/whale/internal/mcp"
	"github.com/usewhale/whale/internal/plugins"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/store"
	"github.com/usewhale/whale/internal/tasks"
	"github.com/usewhale/whale/internal/tools"
)

const CommandsHelp = "/model, /permissions, /agent, /ask [prompt], /plan [prompt], /focus, /skills, /plugins, /memory, /skills-improver, /local-indexer, /new [id], /resume, /clear, /status, /stats, /mcp, /compact, /init, /exit"

const (
	ViewModeDefault = "default"
	ViewModeFocus   = "focus"
)

type Config struct {
	DataDir              string
	ConfigLoaded         bool
	ApprovalMode         string
	AllowPrefixes        string
	DenyPrefixes         string
	AutoCompact          bool
	AutoCompactThreshold float64
	MemoryEnabled        bool
	MemoryMaxChars       int
	MemoryFileOrder      string
	BudgetWarningUSD     float64
	Model                string
	ModelExplicit        bool
	ReasoningEffort      string
	ThinkingEnabled      bool
	ViewMode             string
	RetryMaxAttempts     int
	RetryMaxDelay        time.Duration
	MCPConfigPath        string
	APIBaseURL           string
	SkillsDisabled       []string
	PluginsDisabled      []string
}

type StartOptions struct {
	SessionID     string
	ModeOverride  string
	ResumeMenu    bool
	NewSession    bool
	ApprovalFunc  policy.ApprovalFunc
	UserInputFunc agent.UserInputFunc
}

type ResumeChoice struct {
	Index int
	ID    string
}

type App struct {
	ctx              context.Context
	sessionsDir      string
	workspaceRoot    string
	branch           string
	msgStore         *store.JSONLStore
	toolRegistry     *core.ToolRegistry
	baseToolRegistry *core.ToolRegistry
	toolset          *tools.Toolset
	baseTools        []core.Tool
	taskTools        []core.Tool
	hooks            []agent.ResolvedHook
	hookRunner       *agent.HookRunner
	hookSources      []string
	currentMode      session.Mode
	sessionID        string
	approvalMode     policy.ApprovalMode
	allowPrefixes    []string
	denyPrefixes     []string
	budgetWarningUSD float64
	cfg              Config
	model            string
	reasoningEffort  string
	thinkingEnabled  bool
	contextWindow    int
	mcpManager       *whalemcp.Manager
	pluginManager    *plugins.Manager
	pluginTools      []core.Tool
	mcpInitMu        sync.Mutex
	mcpInitStarted   bool

	a          *agent.Agent
	apiKey     string
	approvalMu sync.Mutex
	approvalFn policy.ApprovalFunc
	userInput  agent.UserInputFunc
}

func DefaultConfig() Config {
	return Config{
		DataDir:              store.DefaultDataDir(),
		ApprovalMode:         string(policy.ApprovalModeOnRequest),
		AutoCompact:          true,
		AutoCompactThreshold: defaults.DefaultAutoCompactThreshold,
		MemoryEnabled:        true,
		MemoryMaxChars:       defaults.DefaultMemoryMaxChars,
		MemoryFileOrder:      defaults.DefaultMemoryFileOrderCSV,
		Model:                defaults.DefaultModel,
		ReasoningEffort:      defaults.DefaultReasoningEffort,
		ThinkingEnabled:      defaults.DefaultThinkingEnabled,
		ViewMode:             ViewModeDefault,
		RetryMaxAttempts:     llmretry.DefaultPolicy().MaxAttempts,
		RetryMaxDelay:        llmretry.DefaultPolicy().MaxDelay,
	}
}

func New(ctx context.Context, cfg Config, start StartOptions) (*App, error) {
	workspaceRoot, _ := os.Getwd()
	if !cfg.ConfigLoaded {
		resolved, err := LoadAndApplyConfig(cfg, workspaceRoot)
		if err != nil {
			return nil, err
		}
		cfg = resolved
	}
	sessionsDir := store.DefaultSessionsDir(cfg.DataDir)
	msgStore, err := store.NewJSONLStore(sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("init session store failed: %w", err)
	}
	sessionID := ""
	if sid := strings.TrimSpace(start.SessionID); sid != "" {
		sessionID = sid
	} else if start.NewSession || start.ResumeMenu {
		sessionID = newSessionID(time.Now())
	} else {
		var err error
		sessionID, err = resolveInitialSessionID(sessionsDir)
		if err != nil {
			return nil, fmt.Errorf("resolve session failed: %w", err)
		}
	}
	if !start.NewSession && !start.ResumeMenu {
		if msg, blocked, err := CheckResumeWorkspace(sessionsDir, sessionID, workspaceRoot); err != nil {
			return nil, err
		} else if blocked {
			return nil, &CrossWorkspaceResumeError{Message: msg}
		}
	}
	approvalMode, err := policy.ParseApprovalMode(cfg.ApprovalMode)
	if err != nil {
		return nil, fmt.Errorf("invalid permissions.mode: %w", err)
	}
	toolset, err := tools.NewToolset(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("init tools failed: %w", err)
	}
	toolset.SetSkillDisabled(cfg.SkillsDisabled)
	mcpConfigPath := strings.TrimSpace(cfg.MCPConfigPath)
	if mcpConfigPath == "" {
		mcpConfigPath = whalemcp.DefaultConfigPath(cfg.DataDir)
	}
	mcpConfig, err := whalemcp.LoadConfig(mcpConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load mcp config: %w", err)
	}
	mcpManager := whalemcp.NewManager(mcpConfig)
	mcpManager.SetWorkspaceRoot(workspaceRoot)
	pluginManager := plugins.NewManager(plugins.Context{DataDir: cfg.DataDir, WorkspaceRoot: workspaceRoot}, cfg.PluginsDisabled)
	pluginTools := pluginManager.Tools()
	toolset.SetExtraSkills(pluginManager.Skills())
	baseTools := append([]core.Tool{}, toolset.Tools()...)
	baseToolRegistry, err := core.NewToolRegistryChecked(baseTools)
	if err != nil {
		return nil, fmt.Errorf("init base tool registry failed: %w", err)
	}
	hooks, hookSources, hookLoadErr := agent.LoadHooks(workspaceRoot, cfg.DataDir)
	if hookLoadErr != nil {
		return nil, fmt.Errorf("load hooks failed: %w", hookLoadErr)
	}
	hookRunner := agent.NewHookRunner(hooks, workspaceRoot)
	hookRunner.AddHandlers(pluginManager.Hooks()...)
	modeState, err := session.LoadModeState(sessionsDir, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load session mode failed: %w", err)
	}
	if raw := strings.TrimSpace(start.ModeOverride); raw != "" {
		mode, err := session.ParseMode(raw)
		if err != nil {
			return nil, err
		}
		modeState.Mode = mode
		if err := session.SaveModeState(sessionsDir, sessionID, mode); err != nil {
			return nil, fmt.Errorf("save mode state failed: %w", err)
		}
	}
	branch := session.DetectGitBranch(workspaceRoot)
	if start.NewSession || start.ResumeMenu {
		if _, err := session.PatchSessionMeta(sessionsDir, sessionID, session.SessionMeta{Workspace: workspaceRoot, Branch: branch}); err != nil {
			return nil, fmt.Errorf("patch session meta failed: %w", err)
		}
	}

	model := firstNonEmpty(strings.TrimSpace(cfg.Model), defaults.DefaultModel)
	effort := normalizeEffort(firstNonEmpty(strings.TrimSpace(cfg.ReasoningEffort), defaults.DefaultReasoningEffort))
	viewMode, err := NormalizeViewMode(cfg.ViewMode)
	if err != nil {
		return nil, err
	}
	cfg.ViewMode = viewMode
	thinking := cfg.ThinkingEnabled
	contextWindow := contextWindowForModel(model)
	apiKey, err := LoadDeepSeekAPIKey(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("load api key failed: %w", err)
	}
	providerFactory := func(model string, maxTokens int) (llm.Provider, error) {
		if strings.TrimSpace(model) == "" {
			model = defaults.DefaultModel
		}
		return newDeepSeekProvider(providerOptions{
			APIKey:          apiKey,
			BaseURL:         cfg.APIBaseURL,
			Model:           model,
			ReasoningEffort: effort,
			ThinkingEnabled: thinking,
			MaxTokens:       maxTokens,
			RetryPolicy:     retryPolicyFromConfig(cfg),
		})
	}
	var appRef *App
	taskRunner := tasks.NewRunner(tasks.RunnerConfig{
		ProviderFactory: providerFactory,
		ParentTools:     baseToolRegistry,
		MessageStore:    msgStore,
		SessionsDir:     sessionsDir,
		ParentSessionID: sessionID,
		ParentSessionIDFunc: func() string {
			if appRef != nil {
				return appRef.sessionID
			}
			return sessionID
		},
		WorkspaceRoot:       workspaceRoot,
		MemoryEnabled:       cfg.MemoryEnabled,
		MemoryMaxChars:      cfg.MemoryMaxChars,
		MemoryFileOrder:     parseCSVList(cfg.MemoryFileOrder),
		DefaultModel:        defaults.DefaultModel,
		DefaultMaxTokens:    tasks.DefaultMaxTokens,
		DefaultMaxToolIters: tasks.DefaultMaxToolIters,
		SummaryMaxChars:     tasks.DefaultSummaryMaxChar,
	})
	taskTools := tasks.NewTools(taskRunner)
	registeredTools := append([]core.Tool{}, baseTools...)
	registeredTools = append(registeredTools, pluginTools...)
	registeredTools = append(registeredTools, taskTools...)
	toolRegistry, err := core.NewToolRegistryChecked(registeredTools)
	if err != nil {
		return nil, fmt.Errorf("init tool registry failed: %w", err)
	}

	app := &App{
		ctx:              ctx,
		sessionsDir:      sessionsDir,
		workspaceRoot:    workspaceRoot,
		branch:           branch,
		msgStore:         msgStore,
		toolRegistry:     toolRegistry,
		baseToolRegistry: baseToolRegistry,
		toolset:          toolset,
		baseTools:        append([]core.Tool{}, baseTools...),
		taskTools:        append([]core.Tool{}, taskTools...),
		hooks:            hooks,
		hookRunner:       hookRunner,
		hookSources:      hookSources,
		currentMode:      modeState.Mode,
		sessionID:        sessionID,
		approvalMode:     approvalMode,
		allowPrefixes:    parseCSVList(cfg.AllowPrefixes),
		denyPrefixes:     parseCSVList(cfg.DenyPrefixes),
		budgetWarningUSD: cfg.BudgetWarningUSD,
		cfg:              cfg,
		model:            model,
		reasoningEffort:  effort,
		thinkingEnabled:  thinking,
		contextWindow:    contextWindow,
		mcpManager:       mcpManager,
		pluginManager:    pluginManager,
		pluginTools:      append([]core.Tool{}, pluginTools...),
		apiKey:           apiKey,
		approvalFn:       defaultApprovalFunc(start.ApprovalFunc),
		userInput:        defaultUserInputFunc(start.UserInputFunc),
	}
	appRef = app
	return app, nil
}

func defaultApprovalFunc(fn policy.ApprovalFunc) policy.ApprovalFunc {
	if fn != nil {
		return fn
	}
	return func(policy.ApprovalRequest) policy.ApprovalDecision { return policy.ApprovalDeny }
}

func defaultUserInputFunc(fn agent.UserInputFunc) agent.UserInputFunc {
	if fn != nil {
		return fn
	}
	return func(agent.UserInputRequest) (core.UserInputResponse, bool) {
		return core.UserInputResponse{}, false
	}
}

func (a *App) SetApprovalFunc(fn policy.ApprovalFunc) {
	if fn == nil {
		return
	}
	a.approvalFn = fn
	a.a = nil
}

func (a *App) SetUserInputFunc(fn agent.UserInputFunc) {
	if fn == nil {
		return
	}
	a.userInput = fn
	a.a = nil
}

func (a *App) StartupLines() []string {
	lines := []string{"whale repl", fmt.Sprintf("session: %s", a.sessionID), fmt.Sprintf("mode: %s", a.currentMode), fmt.Sprintf("permissions.mode: %s", a.approvalMode)}
	lines = append(lines, fmt.Sprintf("model: %s", a.model), fmt.Sprintf("effort: %s", a.reasoningEffort), fmt.Sprintf("thinking: %s", onOff(a.thinkingEnabled)), fmt.Sprintf("view: %s", a.ViewMode()))
	if a.budgetWarningUSD > 0 {
		lines = append(lines, fmt.Sprintf("budget.session_limit_usd: %.4f", a.budgetWarningUSD))
	} else {
		lines = append(lines, "budget.session_limit_usd: disabled")
	}
	if len(a.hookSources) > 0 {
		lines = append(lines, fmt.Sprintf("hooks: %s", strings.Join(a.hookSources, ", ")))
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

func (a *App) SessionID() string                 { return a.sessionID }
func (a *App) CurrentMode() session.Mode         { return a.currentMode }
func (a *App) ApprovalMode() policy.ApprovalMode { return a.approvalMode }
func (a *App) SetMode(mode session.Mode) (string, error) {
	if _, err := session.ParseMode(string(mode)); err != nil {
		return "", err
	}
	if err := session.SaveModeState(a.sessionsDir, a.sessionID, mode); err != nil {
		return "", err
	}
	a.currentMode = mode
	a.a = nil
	return fmt.Sprintf("%s mode enabled", modeTitle(mode)), nil
}
func (a *App) ToggleMode() (string, error) {
	switch a.currentMode {
	case session.ModeAgent:
		return a.SetMode(session.ModeAsk)
	case session.ModeAsk:
		return a.SetMode(session.ModePlan)
	default:
		return a.SetMode(session.ModeAgent)
	}
}
func (a *App) SetApprovalMode(mode policy.ApprovalMode) {
	a.approvalMode = mode
	a.a = nil
}
func (a *App) WorkspaceRoot() string   { return a.workspaceRoot }
func (a *App) Model() string           { return a.model }
func (a *App) ReasoningEffort() string { return a.reasoningEffort }
func (a *App) ThinkingEnabled() bool   { return a.thinkingEnabled }
func (a *App) ViewMode() string {
	if a == nil {
		return ViewModeDefault
	}
	mode, err := NormalizeViewMode(a.cfg.ViewMode)
	if err != nil {
		return ViewModeDefault
	}
	return mode
}
func (a *App) ListMessages() ([]core.Message, error) {
	return a.msgStore.List(a.ctx, a.sessionID)
}
func (a *App) SupportedModels() []string { return defaults.SupportedModels() }
func (a *App) SupportedEfforts() []string {
	return SupportedReasoningEfforts()
}

func (a *App) SetModelAndEffort(modelName, effort string) error {
	m := strings.TrimSpace(strings.ToLower(modelName))
	e := normalizeEffort(effort)
	if m == "" || e == "" {
		return errors.New("model and effort are required")
	}
	if !containsString(a.SupportedModels(), m) {
		return fmt.Errorf("unsupported model: %s", modelName)
	}
	if !containsString(a.SupportedEfforts(), e) {
		return fmt.Errorf("unsupported effort: %s", effort)
	}
	a.model = m
	a.reasoningEffort = e
	a.a = nil
	a.savePreferences()
	return nil
}

func (a *App) SetThinkingEnabled(enabled bool) {
	a.thinkingEnabled = enabled
	a.a = nil
	a.savePreferences()
}

func (a *App) SetViewMode(mode string) error {
	mode, err := NormalizeViewMode(mode)
	if err != nil {
		return err
	}
	a.cfg.ViewMode = mode
	return SaveGlobalViewMode(a.cfg.DataDir, mode)
}

func (a *App) ToggleViewMode() (string, error) {
	next := ViewModeFocus
	if a.ViewMode() == ViewModeFocus {
		next = ViewModeDefault
	}
	if err := a.SetViewMode(next); err != nil {
		return "", err
	}
	return next, nil
}

func ViewModeToggleMessage(mode string) string {
	if strings.TrimSpace(mode) == ViewModeFocus {
		return "Focus view enabled"
	}
	return "Focus view disabled"
}

func (a *App) Close() error {
	if a == nil || a.mcpManager == nil {
		return nil
	}
	return a.mcpManager.Close()
}

func (a *App) savePreferences() {
	enabled := a.thinkingEnabled
	_ = SaveGlobalPreferences(a.cfg.DataDir, a.model, a.reasoningEffort, enabled)
}
