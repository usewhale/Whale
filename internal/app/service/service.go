package service

import (
	"context"
	"os/exec"
	"sync"
	"sync/atomic"

	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/plugins"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/runtime/protocol"
	"github.com/usewhale/whale/internal/skills"
)

type IntentKind string

const (
	IntentSubmit              IntentKind = "submit"
	IntentSubmitLocal         IntentKind = "submit_local"
	IntentAllowTool           IntentKind = "allow_tool"
	IntentAllowToolForSession IntentKind = "allow_tool_for_session"
	IntentDenyTool            IntentKind = "deny_tool"
	IntentCancelToolApproval  IntentKind = "cancel_tool_approval"
	IntentSubmitUserInput     IntentKind = "submit_user_input"
	IntentCancelUserInput     IntentKind = "cancel_user_input"
	IntentSelectSession       IntentKind = "select_session"
	IntentSelectRewindMessage IntentKind = "select_rewind_message"
	IntentRequestSessions     IntentKind = "request_sessions"
	IntentRequestExit         IntentKind = "request_exit"
	IntentShutdown            IntentKind = "shutdown"
	IntentSetModelAndEffort   IntentKind = "set_model_and_effort"
	IntentSetApprovalMode     IntentKind = "set_approval_mode"
	IntentSetViewMode         IntentKind = "set_view_mode"
	IntentToggleMode          IntentKind = "toggle_mode"
	IntentImplementPlan       IntentKind = "implement_plan"
	IntentDeclinePlan         IntentKind = "decline_plan"
	IntentRequestSkillsManage IntentKind = "request_skills_manage"
	IntentSetSkillEnabled     IntentKind = "set_skill_enabled"
	IntentSetPluginEnabled    IntentKind = "set_plugin_enabled"
	IntentWorktreeExitChoice  IntentKind = "worktree_exit_choice"
)

type Intent struct {
	Kind           IntentKind
	Input          string
	HiddenInput    bool
	ToolCallID     string
	UserInput      *core.UserInputResponse
	SessionInput   string
	MessageID      string
	Model          string
	Effort         string
	Thinking       string
	ApprovalMode   string
	ViewMode       string
	SkillName      string
	SkillEnabled   bool
	PluginID       string
	PluginEnabled  bool
	SkillBinding   *app.SkillBinding
	WorktreeAction string
}

type EventKind = protocol.EventKind
type Event = protocol.Event

const (
	EventMetadataAgentTurn   = protocol.EventMetadataAgentTurn
	EventMetadataLocalSubmit = protocol.EventMetadataLocalSubmit
)

const (
	EventInfo                          = protocol.EventInfo
	EventError                         = protocol.EventError
	EventAssistantDelta                = protocol.EventAssistantDelta
	EventReasoningDelta                = protocol.EventReasoningDelta
	EventPlanDelta                     = protocol.EventPlanDelta
	EventPlanCompleted                 = protocol.EventPlanCompleted
	EventPlanUpdate                    = protocol.EventPlanUpdate
	EventProviderRetry                 = protocol.EventProviderRetry
	EventToolCall                      = protocol.EventToolCall
	EventToolResult                    = protocol.EventToolResult
	EventTaskStarted                   = protocol.EventTaskStarted
	EventTaskProgress                  = protocol.EventTaskProgress
	EventTaskCompleted                 = protocol.EventTaskCompleted
	EventMCPStatus                     = protocol.EventMCPStatus
	EventMCPComplete                   = protocol.EventMCPComplete
	EventApprovalRequired              = protocol.EventApprovalRequired
	EventUserInputRequired             = protocol.EventUserInputRequired
	EventUserInputDone                 = protocol.EventUserInputDone
	EventSessionsListed                = protocol.EventSessionsListed
	EventLocalSubmitResult             = protocol.EventLocalSubmitResult
	EventLocalSubmitDone               = protocol.EventLocalSubmitDone
	EventDiffResult                    = protocol.EventDiffResult
	EventBtwStarted                    = protocol.EventBtwStarted
	EventBtwDelta                      = protocol.EventBtwDelta
	EventBtwDone                       = protocol.EventBtwDone
	EventBtwError                      = protocol.EventBtwError
	EventTurnDone                      = protocol.EventTurnDone
	EventModelSelectionRequested       = protocol.EventModelSelectionRequested
	EventPermissionsSelectionRequested = protocol.EventPermissionsSelectionRequested
	EventSkillsSelectionRequested      = protocol.EventSkillsSelectionRequested
	EventSkillsManagerUpdated          = protocol.EventSkillsManagerUpdated
	EventPluginsManagerUpdated         = protocol.EventPluginsManagerUpdated
	EventReviewRequested               = protocol.EventReviewRequested
	EventViewModeChanged               = protocol.EventViewModeChanged
	EventSkillLoaded                   = protocol.EventSkillLoaded
	EventWorktreeExitPrompt            = protocol.EventWorktreeExitPrompt
	EventExitRequested                 = protocol.EventExitRequested
	EventScreenClearRequested          = protocol.EventScreenClearRequested
	EventSessionHydrated               = protocol.EventSessionHydrated
	EventRewindMessagesListed          = protocol.EventRewindMessagesListed
)

type Service struct {
	ctx              context.Context
	serviceCtxCancel context.CancelFunc
	app              *app.App
	events           chan Event
	localSubmits     chan string
	cancelMu         sync.Mutex
	cancel           context.CancelFunc
	active           bool
	bgWG             sync.WaitGroup

	interactionMu     sync.Mutex
	shutdownRequested bool

	approveMu     sync.Mutex
	approvals     map[string]chan policy.ApprovalDecision
	sessionGrants map[string]map[string]bool

	inputMu sync.Mutex
	inputs  map[string]chan userInputDecision

	btwNextID atomic.Int64
}

type userInputDecision struct {
	response core.UserInputResponse
	ok       bool
}

func New(ctx context.Context, cfg app.Config, start app.StartOptions) (*Service, error) {
	ctx, cancel := context.WithCancel(ctx)
	a, err := app.New(ctx, cfg, start)
	if err != nil {
		cancel()
		return nil, err
	}
	s := &Service{
		ctx:              ctx,
		serviceCtxCancel: cancel,
		app:              a,
		events:           make(chan Event, 512),
		localSubmits:     make(chan string, 64),
		approvals:        map[string]chan policy.ApprovalDecision{},
		sessionGrants:    map[string]map[string]bool{},
		inputs:           map[string]chan userInputDecision{},
	}
	a.SetApprovalFunc(s.awaitApproval)
	a.SetUserInputFunc(s.awaitUserInput)
	s.goTracked(s.runLocalSubmitWorker)
	if start.ResumeMenu {
		if !s.emitSessionChoices() {
			s.emitSessionHydrated()
		}
	} else {
		for _, line := range a.StartupLines() {
			s.emit(Event{Kind: EventInfo, Text: line})
		}
		s.emitSessionHydrated()
	}
	s.startMCPStartup()
	return s, nil
}

// goTracked runs fn in a goroutine and tracks it on bgWG so Close can wait
// for in-flight work (turns, side questions, local submits) to finish before
// the service tears down. Without this, background goroutines can outlive
// Close and race with cleanup of caller-owned state (e.g. test temp dirs).
func (s *Service) goTracked(fn func()) {
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		fn()
	}()
}

func (s *Service) Events() <-chan Event { return s.events }
func (s *Service) SessionID() string    { return s.app.SessionID() }
func (s *Service) WorkspaceRoot() string {
	return s.app.WorkspaceRoot()
}
func (s *Service) PrepareOpenCommand(line string) (string, *exec.Cmd, error) {
	openCmd, err := s.app.PrepareOpenCommand(line)
	if err != nil {
		return "", nil, err
	}
	return openCmd.Path, openCmd.Cmd, nil
}
func (s *Service) Model() string           { return s.app.Model() }
func (s *Service) ReasoningEffort() string { return s.app.ReasoningEffort() }
func (s *Service) ThinkingEnabled() bool   { return s.app.ThinkingEnabled() }
func (s *Service) ViewMode() string        { return s.app.ViewMode() }
func (s *Service) ShowReasoning() bool {
	if s == nil || s.app == nil {
		return false
	}
	return s.app.ShowReasoning()
}
func (s *Service) SetViewMode(mode string) error {
	if s == nil || s.app == nil {
		return nil
	}
	return s.app.SetViewMode(mode)
}
func (s *Service) SkillSuggestions() []skills.SkillView {
	if s == nil || s.app == nil {
		return nil
	}
	return s.app.SkillSuggestions()
}

func (s *Service) SkillsForManager() []skills.SkillView {
	if s == nil || s.app == nil {
		return nil
	}
	return s.app.SkillReport().All()
}

func (s *Service) PluginsForManager() []plugins.PluginStatus {
	if s == nil || s.app == nil {
		return nil
	}
	return s.app.PluginStatuses()
}

func (s *Service) Close() error {
	if s == nil || s.app == nil {
		return nil
	}
	if s.serviceCtxCancel != nil {
		s.serviceCtxCancel()
	}
	s.bgWG.Wait()
	return s.app.Close()
}
