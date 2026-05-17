package agent

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/memory"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/store"
	"github.com/usewhale/whale/internal/telemetry"
)

var ErrSessionBusy = errors.New("session is currently processing another request")
var ErrBudgetExceeded = errors.New("session budget exhausted")

type AgentEventType string

const (
	AgentEventTypeAssistantDelta        AgentEventType = "assistant_delta"
	AgentEventTypeReasoningDelta        AgentEventType = "reasoning_delta"
	AgentEventTypeToolArgsDelta         AgentEventType = "tool_args_delta"
	AgentEventTypeToolArgsRepaired      AgentEventType = "tool_args_repaired"
	AgentEventTypeToolCallBlocked       AgentEventType = "tool_call_blocked"
	AgentEventTypeToolModeBlocked       AgentEventType = "tool_mode_blocked"
	AgentEventTypeToolApprovalRequired  AgentEventType = "tool_approval_required"
	AgentEventTypeToolApprovalGranted   AgentEventType = "tool_approval_granted"
	AgentEventTypeToolCallScavenged     AgentEventType = "tool_call_scavenged"
	AgentEventTypeToolPolicyDecision    AgentEventType = "tool_policy_decision"
	AgentEventTypeToolCall              AgentEventType = "tool_call"
	AgentEventTypeToolResult            AgentEventType = "tool_result"
	AgentEventTypeUserInputRequired     AgentEventType = "user_input_required"
	AgentEventTypeUserInputSubmitted    AgentEventType = "user_input_submitted"
	AgentEventTypeUserInputCancelled    AgentEventType = "user_input_cancelled"
	AgentEventTypePlanDelta             AgentEventType = "plan_delta"
	AgentEventTypePlanCompleted         AgentEventType = "plan_completed"
	AgentEventTypePlanStepBlocked       AgentEventType = "plan_step_blocked"
	AgentEventTypeToolRecoveryScheduled AgentEventType = "tool_recovery_scheduled"
	AgentEventTypeToolRecoveryAttempt   AgentEventType = "tool_recovery_attempt"
	AgentEventTypeToolRecoveryExhausted AgentEventType = "tool_recovery_exhausted"
	AgentEventTypeReplanRequiredSet     AgentEventType = "replan_required_set"
	AgentEventTypeContextCompacted      AgentEventType = "context_compacted"
	AgentEventTypePrefixDrift           AgentEventType = "prefix_drift"
	AgentEventTypePrefixCacheMetrics    AgentEventType = "prefix_cache_metrics"
	AgentEventTypeBudgetWarning         AgentEventType = "budget_warning"
	AgentEventTypeTurnCancelled         AgentEventType = "turn_cancelled"
	AgentEventTypeForcedSummaryStarted  AgentEventType = "forced_summary_started"
	AgentEventTypeForcedSummaryDone     AgentEventType = "forced_summary_done"
	AgentEventTypeForcedSummaryFailed   AgentEventType = "forced_summary_failed"
	AgentEventTypeHookStarted           AgentEventType = "hook_started"
	AgentEventTypeHookBlocked           AgentEventType = "hook_blocked"
	AgentEventTypeHookWarned            AgentEventType = "hook_warned"
	AgentEventTypeHookFailed            AgentEventType = "hook_failed"
	AgentEventTypeHookCompleted         AgentEventType = "hook_completed"
	AgentEventTypeParallelReasonStarted AgentEventType = "parallel_reason_started"
	AgentEventTypeParallelReasonDone    AgentEventType = "parallel_reason_completed"
	AgentEventTypeSubagentStarted       AgentEventType = "subagent_started"
	AgentEventTypeTaskProgress          AgentEventType = "task_progress"
	AgentEventTypeSubagentDone          AgentEventType = "subagent_completed"
	AgentEventTypeDone                  AgentEventType = "done"
	AgentEventTypeError                 AgentEventType = "error"
)

type ToolArgsProgress struct {
	ToolCallIndex int
	ToolName      string
	ArgsChars     int
	ReadyCount    int
}

type ToolArgsRepair struct {
	ToolCallIndex int
	ToolName      string
}

type ToolCallBlocked struct {
	ToolCallID string
	ToolName   string
	ReasonCode string
}

type ToolApprovalRequired struct {
	ToolCallID string
	ToolName   string
	Reason     string
	Code       string
	Key        string
	Keys       []string
	Summary    string
	Scope      string
	Metadata   map[string]any
}

type ToolApprovalGranted struct {
	SessionID  string
	ToolCallID string
	ToolName   string
	Key        string
	Keys       []string
}

type ToolCallScavenged struct {
	Count int
}

type ToolPolicyDecision struct {
	ToolCallID    string
	ToolName      string
	Allow         bool
	NeedsApproval bool
	Reason        string
	Code          string
	Phase         string
	MatchedRule   string
}

type AgentEvent struct {
	Type           AgentEventType
	Content        string
	ReasoningDelta string
	ToolArgs       *ToolArgsProgress
	ToolArgsRepair *ToolArgsRepair
	ToolBlocked    *ToolCallBlocked
	Approval       *ToolApprovalRequired
	ApprovalGrant  *ToolApprovalGranted
	Scavenged      *ToolCallScavenged
	Policy         *ToolPolicyDecision
	Recovery       *ToolRecoveryInfo
	Compact        *CompactInfo
	PrefixDrift    *PrefixDriftInfo
	CacheMetrics   *PrefixCacheMetricsInfo
	Budget         *BudgetWarningInfo
	Hook           *HookEventInfo
	Task           *TaskActivityInfo
	ToolCall       *core.ToolCall
	UserInputReq   *core.UserInputRequest
	UserInputResp  *core.UserInputResponse
	Result         *core.ToolResult
	Message        *core.Message
	Err            error
}

type TaskActivityInfo struct {
	ToolCallID string
	ToolName   string
	Role       string
	Model      string
	Count      int
	Summary    string
	Status     string
	DurationMS int64
	Metadata   map[string]any
}

type BudgetWarningInfo struct {
	CapUSD      float64
	SpentUSD    float64
	Percent     int
	TurnCostUSD float64
}

type UserInputRequest struct {
	SessionID string
	ToolCall  core.ToolCall
	Questions []core.UserInputQuestion
}

type UserInputFunc func(req UserInputRequest) (core.UserInputResponse, bool)

type HookEventInfo struct {
	Name       string
	Event      HookEvent
	Decision   HookDecision
	ExitCode   int
	Message    string
	DurationMS int64
	Truncated  bool
}

type CompactInfo struct {
	Compacted      bool
	Auto           bool
	MessagesBefore int
	MessagesAfter  int
	BeforeEstimate int
	AfterEstimate  int
}

type PrefixDriftInfo struct {
	Expected string
	Actual   string
}

type PrefixCacheMetricsInfo struct {
	Model             string
	PrefixFingerprint string
	PromptTokens      int
	CachedTokens      int
	CacheHitRatio     float64
}

type ToolRecoveryInfo struct {
	ToolCallID     string
	ToolName       string
	FailureClass   string
	Action         string
	Attempt        int
	MaxAttempts    int
	Reason         string
	Executed       bool
	ReplanInjected bool
}

type Agent struct {
	provider               llm.Provider
	store                  store.MessageStore
	tools                  *core.ToolRegistry
	storm                  stormConfig
	repairer               *toolCallRepair
	policy                 policy.ToolPolicy
	approve                policy.ApprovalFunc
	userInput              UserInputFunc
	approvalCache          *policy.SessionApprovalCache
	mode                   session.Mode
	autoCompact            bool
	compactThresh          float64
	contextWindow          int
	recovery               RecoveryPolicy
	hooks                  *HookRunner
	projectMemoryEnabled   bool
	projectMemoryMaxChars  int
	projectMemoryFileOrder []string
	workspaceRoot          string
	disabledSkills         []string
	extraSystemBlocks      []string
	sessionRuntime         *memory.SessionRuntime
	sessionsDir            string
	budgetWarningUSD       float64
	usageLogPath           string
	budgetWarned80         sync.Map
	maxToolIters           int
	active                 sync.Map
}

func NewAgent(provider llm.Provider, store store.MessageStore, tools []core.Tool) *Agent {
	return &Agent{
		provider:               provider,
		store:                  store,
		tools:                  core.NewToolRegistry(tools),
		storm:                  defaultStormConfig(),
		repairer:               newToolCallRepair(defaultStormConfig()),
		policy:                 policy.DefaultToolPolicy{Mode: policy.ApprovalModeOnRequest},
		approvalCache:          policy.NewSessionApprovalCache(),
		mode:                   session.ModeAgent,
		compactThresh:          defaults.DefaultAgentCompactThreshold,
		contextWindow:          defaults.DefaultContextWindow,
		recovery:               DefaultRecoveryPolicy(),
		hooks:                  NewHookRunner(nil, ""),
		projectMemoryEnabled:   true,
		projectMemoryMaxChars:  defaults.DefaultMemoryMaxChars,
		projectMemoryFileOrder: defaults.DefaultMemoryFileOrder(),
		sessionRuntime:         memory.NewSessionRuntime(""),
		usageLogPath:           telemetry.DefaultUsageLogPath(),
		maxToolIters:           64,
	}
}

func NewAgentWithRegistry(provider llm.Provider, store store.MessageStore, tools *core.ToolRegistry, opts ...AgentOption) *Agent {
	if tools == nil {
		tools = core.NewToolRegistry(nil)
	}
	a := &Agent{
		provider:               provider,
		store:                  store,
		tools:                  tools,
		storm:                  defaultStormConfig(),
		repairer:               newToolCallRepair(defaultStormConfig()),
		policy:                 policy.DefaultToolPolicy{Mode: policy.ApprovalModeOnRequest},
		approvalCache:          policy.NewSessionApprovalCache(),
		mode:                   session.ModeAgent,
		compactThresh:          defaults.DefaultAgentCompactThreshold,
		contextWindow:          defaults.DefaultContextWindow,
		recovery:               DefaultRecoveryPolicy(),
		hooks:                  NewHookRunner(nil, ""),
		projectMemoryEnabled:   true,
		projectMemoryMaxChars:  defaults.DefaultMemoryMaxChars,
		projectMemoryFileOrder: defaults.DefaultMemoryFileOrder(),
		sessionRuntime:         memory.NewSessionRuntime(""),
		usageLogPath:           telemetry.DefaultUsageLogPath(),
		maxToolIters:           64,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
	return a
}

type AgentOption func(*Agent)

func WithToolPolicy(policy policy.ToolPolicy) AgentOption {
	return func(a *Agent) {
		if policy != nil {
			a.policy = policy
		}
	}
}

func WithApprovalFunc(fn policy.ApprovalFunc) AgentOption {
	return func(a *Agent) {
		a.approve = fn
	}
}

func WithUserInputFunc(fn UserInputFunc) AgentOption {
	return func(a *Agent) {
		a.userInput = fn
	}
}

func WithSessionMode(mode session.Mode) AgentOption {
	return func(a *Agent) {
		a.mode = mode
	}
}

func WithSessionsDir(sessionsDir string) AgentOption {
	return func(a *Agent) {
		a.sessionsDir = strings.TrimSpace(sessionsDir)
		a.sessionRuntime = memory.NewSessionRuntime(sessionsDir)
	}
}

func WithAutoCompact(enabled bool, threshold float64, contextWindow int) AgentOption {
	return func(a *Agent) {
		a.autoCompact = enabled
		if threshold > 0 && threshold < 1 {
			a.compactThresh = threshold
		}
		if contextWindow > 0 {
			a.contextWindow = contextWindow
		}
	}
}

func WithBudgetWarningUSD(capUSD float64) AgentOption {
	return func(a *Agent) {
		if capUSD > 0 {
			a.budgetWarningUSD = capUSD
		} else {
			a.budgetWarningUSD = 0
		}
	}
}

func WithUsageLogPath(path string) AgentOption {
	return func(a *Agent) {
		a.usageLogPath = strings.TrimSpace(path)
	}
}

func WithRecoveryPolicy(r RecoveryPolicy) AgentOption {
	return func(a *Agent) {
		a.recovery = r
	}
}

func WithHooks(hooks []ResolvedHook, workspaceRoot string) AgentOption {
	return func(a *Agent) {
		a.hooks = NewHookRunner(hooks, workspaceRoot)
	}
}

func WithProjectMemory(enabled bool, maxChars int, fileOrder []string, workspaceRoot string) AgentOption {
	return func(a *Agent) {
		a.projectMemoryEnabled = enabled
		if maxChars > 0 {
			a.projectMemoryMaxChars = maxChars
		}
		if len(fileOrder) > 0 {
			a.projectMemoryFileOrder = fileOrder
		}
		a.workspaceRoot = strings.TrimSpace(workspaceRoot)
	}
}

func WithDisabledSkills(names []string) AgentOption {
	return func(a *Agent) {
		a.disabledSkills = append([]string(nil), names...)
	}
}

func WithExtraSystemBlocks(blocks ...string) AgentOption {
	return func(a *Agent) {
		a.extraSystemBlocks = append([]string(nil), blocks...)
	}
}

func WithMaxToolIters(maxIters int) AgentOption {
	return func(a *Agent) {
		if maxIters > 0 {
			a.maxToolIters = maxIters
		}
	}
}

func (a *Agent) Run(ctx context.Context, sessionID, input string) (core.Message, error) {
	events, err := a.RunStream(ctx, sessionID, input)
	if err != nil {
		return core.Message{}, err
	}
	var final core.Message
	cancelled := false
	for ev := range events {
		if ev.Type == AgentEventTypeError && ev.Err != nil {
			return core.Message{}, ev.Err
		}
		if ev.Type == AgentEventTypeTurnCancelled {
			cancelled = true
		}
		if ev.Type == AgentEventTypeDone && ev.Message != nil {
			final = *ev.Message
		}
	}
	if final.ID == "" {
		if cancelled {
			if err := ctx.Err(); err != nil {
				return core.Message{}, err
			}
			return core.Message{}, context.Canceled
		}
		return core.Message{}, errors.New("agent finished without final message")
	}
	return final, nil
}

func (a *Agent) RunStream(ctx context.Context, sessionID, input string) (<-chan AgentEvent, error) {
	return a.RunStreamWithOptions(ctx, sessionID, input, false)
}
