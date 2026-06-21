package tasks

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/skills"
	"github.com/usewhale/whale/internal/store"
	"github.com/usewhale/whale/internal/tools"
)

const (
	MaxParallelPrompts = 8
	DefaultMaxTokens   = 800
	// DefaultMaxToolIters bounds unattended subagents (no human in the loop to
	// cancel a runaway). Set high enough that legitimate implementation subtasks
	// finish well under it, so the cap only ever catches a genuine loop.
	DefaultMaxToolIters   = 200
	DefaultSummaryMaxChar = 8 * 1024
)

type ProviderFactory func(model string, maxTokens int) (llm.Provider, error)
type ProviderFactoryWithOptions func(ProviderRequest) (llm.Provider, error)

type ProviderRequest struct {
	Model     string
	MaxTokens int
	Effort    string
}

type ToolWorkspace struct {
	WorkspaceRoot      string
	WorktreeRoot       string
	OriginalWorkspace  string
	WorktreeName       string
	WorktreeBranch     string
	OriginalBranch     string
	OriginalHeadCommit string
}

type WorkspaceToolRegistryFactory func(ToolWorkspace) (*core.ToolRegistry, error)

type RunnerConfig struct {
	ProviderFactory            ProviderFactory
	ProviderFactoryWithOptions ProviderFactoryWithOptions
	ParentTools                *core.ToolRegistry
	WorkspaceTools             WorkspaceToolRegistryFactory
	AgentDefinitions           *AgentDefinitionLibrary
	ParentPolicy               policy.ToolPolicy
	MessageStore               store.MessageStore
	SessionsDir                string
	ParentSessionID            string
	ParentSessionIDFunc        func() string
	WorkspaceRoot              string
	MemoryEnabled              bool
	MemoryMaxChars             int
	MemoryFileOrder            []string
	SkillsDisabled             []string
	ExtraSkills                []*skills.Skill
	AutoCompact                bool
	AutoCompactThreshold       float64
	DefaultModel               string
	DefaultMaxTokens           int
	DefaultMaxToolIters        int
	SummaryMaxChars            int
	UsageLogPath               string
	ApprovalFunc               policy.ApprovalFunc
}

type Runner struct {
	providerFactory            ProviderFactory
	providerFactoryWithOptions ProviderFactoryWithOptions
	parentTools                *core.ToolRegistry
	workspaceTools             WorkspaceToolRegistryFactory
	agentDefinitions           *AgentDefinitionLibrary
	parentPolicy               policy.ToolPolicy
	messageStore               store.MessageStore
	sessionsDir                string
	parentSessionID            string
	parentSessionIDFunc        func() string
	workspaceRoot              string
	memoryEnabled              bool
	memoryMaxChars             int
	memoryFileOrder            []string
	skillsDisabled             []string
	extraSkills                []*skills.Skill
	autoCompact                bool
	autoCompactThreshold       float64
	defaultModel               string
	defaultMaxTokens           int
	defaultMaxToolIters        int
	summaryMaxChars            int
	usageLogPath               string
	approvalFunc               policy.ApprovalFunc
	subagentBudgetMu           sync.Mutex
	subagentBudget             SubagentBudget
	backgroundMu               sync.Mutex
	backgroundCancels          map[string]context.CancelFunc
}

func NewRunner(cfg RunnerConfig) *Runner {
	model := strings.TrimSpace(cfg.DefaultModel)
	if model == "" {
		model = defaults.DefaultModel
	}
	maxTokens := cfg.DefaultMaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	maxToolIters := cfg.DefaultMaxToolIters
	if maxToolIters <= 0 {
		maxToolIters = DefaultMaxToolIters
	}
	summaryMaxChars := cfg.SummaryMaxChars
	if summaryMaxChars <= 0 {
		summaryMaxChars = DefaultSummaryMaxChar
	}
	return &Runner{
		providerFactory:            cfg.ProviderFactory,
		providerFactoryWithOptions: cfg.ProviderFactoryWithOptions,
		parentTools:                cfg.ParentTools,
		workspaceTools:             cfg.WorkspaceTools,
		agentDefinitions:           cfg.AgentDefinitions,
		parentPolicy:               cfg.ParentPolicy,
		messageStore:               cfg.MessageStore,
		sessionsDir:                strings.TrimSpace(cfg.SessionsDir),
		parentSessionID:            strings.TrimSpace(cfg.ParentSessionID),
		parentSessionIDFunc:        cfg.ParentSessionIDFunc,
		workspaceRoot:              strings.TrimSpace(cfg.WorkspaceRoot),
		memoryEnabled:              cfg.MemoryEnabled,
		memoryMaxChars:             cfg.MemoryMaxChars,
		memoryFileOrder:            append([]string(nil), cfg.MemoryFileOrder...),
		skillsDisabled:             append([]string(nil), cfg.SkillsDisabled...),
		extraSkills:                append([]*skills.Skill(nil), cfg.ExtraSkills...),
		autoCompact:                cfg.AutoCompact,
		autoCompactThreshold:       cfg.AutoCompactThreshold,
		defaultModel:               model,
		defaultMaxTokens:           maxTokens,
		defaultMaxToolIters:        maxToolIters,
		summaryMaxChars:            summaryMaxChars,
		usageLogPath:               strings.TrimSpace(cfg.UsageLogPath),
		approvalFunc:               cfg.ApprovalFunc,
		backgroundCancels:          map[string]context.CancelFunc{},
	}
}

func (r *Runner) newProvider(model string, maxTokens int, effort string) (llm.Provider, error) {
	if r.providerFactoryWithOptions != nil {
		return r.providerFactoryWithOptions(ProviderRequest{
			Model:     strings.TrimSpace(model),
			MaxTokens: maxTokens,
			Effort:    strings.TrimSpace(effort),
		})
	}
	if r.providerFactory == nil {
		return nil, errors.New("provider factory is not configured")
	}
	return r.providerFactory(model, maxTokens)
}

func defaultWorkspaceTools(workspace ToolWorkspace) (*core.ToolRegistry, error) {
	toolset, err := tools.NewToolset(workspace.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	toolset.SetWorktreeContext(workspace.WorktreeRoot, workspace.OriginalWorkspace)
	return core.NewToolRegistryChecked(toolset.Tools())
}
