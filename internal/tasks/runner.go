package tasks

import (
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/store"
)

const (
	MaxParallelPrompts    = 8
	DefaultMaxTokens      = 800
	DefaultMaxToolIters   = 12
	DefaultSummaryMaxChar = 8 * 1024
)

type ProviderFactory func(model string, maxTokens int) (llm.Provider, error)

type RunnerConfig struct {
	ProviderFactory      ProviderFactory
	ParentTools          *core.ToolRegistry
	MessageStore         store.MessageStore
	SessionsDir          string
	ParentSessionID      string
	ParentSessionIDFunc  func() string
	WorkspaceRoot        string
	MemoryEnabled        bool
	MemoryMaxChars       int
	MemoryFileOrder      []string
	AutoCompact          bool
	AutoCompactThreshold float64
	DefaultModel         string
	DefaultMaxTokens     int
	DefaultMaxToolIters  int
	SummaryMaxChars      int
	UsageLogPath         string
}

type Runner struct {
	providerFactory      ProviderFactory
	parentTools          *core.ToolRegistry
	messageStore         store.MessageStore
	sessionsDir          string
	parentSessionID      string
	parentSessionIDFunc  func() string
	workspaceRoot        string
	memoryEnabled        bool
	memoryMaxChars       int
	memoryFileOrder      []string
	autoCompact          bool
	autoCompactThreshold float64
	defaultModel         string
	defaultMaxTokens     int
	defaultMaxToolIters  int
	summaryMaxChars      int
	usageLogPath         string
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
		providerFactory:      cfg.ProviderFactory,
		parentTools:          cfg.ParentTools,
		messageStore:         cfg.MessageStore,
		sessionsDir:          strings.TrimSpace(cfg.SessionsDir),
		parentSessionID:      strings.TrimSpace(cfg.ParentSessionID),
		parentSessionIDFunc:  cfg.ParentSessionIDFunc,
		workspaceRoot:        strings.TrimSpace(cfg.WorkspaceRoot),
		memoryEnabled:        cfg.MemoryEnabled,
		memoryMaxChars:       cfg.MemoryMaxChars,
		memoryFileOrder:      append([]string(nil), cfg.MemoryFileOrder...),
		autoCompact:          cfg.AutoCompact,
		autoCompactThreshold: cfg.AutoCompactThreshold,
		defaultModel:         model,
		defaultMaxTokens:     maxTokens,
		defaultMaxToolIters:  maxToolIters,
		summaryMaxChars:      summaryMaxChars,
		usageLogPath:         strings.TrimSpace(cfg.UsageLogPath),
	}
}
