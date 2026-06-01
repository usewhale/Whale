package protocol

import "time"

type EventKind string

const (
	EventMetadataAgentTurn   = "agent_turn"
	EventMetadataLocalSubmit = "local_submit"
)

const (
	EventInfo                 EventKind = "info"
	EventError                EventKind = "error"
	EventAssistantDelta       EventKind = "assistant_delta"
	EventReasoningDelta       EventKind = "reasoning_delta"
	EventPlanDelta            EventKind = "plan_delta"
	EventPlanCompleted        EventKind = "plan_completed"
	EventPlanUpdate           EventKind = "plan_update"
	EventProviderRetry        EventKind = "provider_retry"
	EventToolCall             EventKind = "tool_call"
	EventToolResult           EventKind = "tool_result"
	EventHookStarted          EventKind = "hook_started"
	EventHookCompleted        EventKind = "hook_completed"
	EventTaskStarted          EventKind = "task_started"
	EventTaskProgress         EventKind = "task_progress"
	EventTaskCompleted        EventKind = "task_completed"
	EventMCPStatus            EventKind = "mcp_status"
	EventMCPComplete          EventKind = "mcp_complete"
	EventApprovalRequired     EventKind = "approval_required"
	EventUserInputRequired    EventKind = "user_input_required"
	EventUserInputDone        EventKind = "user_input_done"
	EventSessionsListed       EventKind = "sessions_listed"
	EventLocalSubmitResult    EventKind = "local_submit_result"
	EventLocalSubmitDone      EventKind = "local_submit_done"
	EventDiffResult           EventKind = "diff_result"
	EventBtwStarted           EventKind = "btw_started"
	EventBtwDelta             EventKind = "btw_delta"
	EventBtwDone              EventKind = "btw_done"
	EventBtwError             EventKind = "btw_error"
	EventTurnDone             EventKind = "turn_done"
	EventViewModeChanged      EventKind = "view_mode_changed"
	EventSkillLoaded          EventKind = "skill_loaded"
	EventWorktreeExitPrompt   EventKind = "worktree_exit_prompt"
	EventExitRequested        EventKind = "exit_requested"
	EventSessionHydrated      EventKind = "session_hydrated"
	EventRewindMessagesListed EventKind = "rewind_messages_listed"
	EventWorkflowPanel        EventKind = "workflow_panel"
	EventWorkflowTerminal     EventKind = "workflow_terminal"
)

const (
	EventModelSelectionRequested       EventKind = "model_selection_requested"
	EventPermissionsSelectionRequested EventKind = "permissions_selection_requested"
	EventSkillsSelectionRequested      EventKind = "skills_selection_requested"
	EventSkillsManagerUpdated          EventKind = "skills_manager_updated"
	EventPluginsManagerUpdated         EventKind = "plugins_manager_updated"
	EventHooksManagerUpdated           EventKind = "hooks_manager_updated"
	EventHooksStartupReviewRequested   EventKind = "hooks_startup_review_requested"
	EventReviewRequested               EventKind = "review_requested"
	EventScreenClearRequested          EventKind = "screen_clear_requested"
)

type Event struct {
	Kind             EventKind            `json:"kind"`
	Text             string               `json:"text,omitempty"`
	ToolCallID       string               `json:"tool_call_id,omitempty"`
	ToolName         string               `json:"tool_name,omitempty"`
	Metadata         map[string]any       `json:"metadata,omitempty"`
	Status           string               `json:"status,omitempty"`
	Count            int                  `json:"count,omitempty"`
	DurationMS       int64                `json:"duration_ms,omitempty"`
	ProgressMessages []ProgressStep       `json:"progress_messages,omitempty"`
	Questions        []UserInputQuestion  `json:"questions,omitempty"`
	Choices          []string             `json:"choices,omitempty"`
	Approval         *ApprovalRequest     `json:"approval,omitempty"`
	LastResponse     string               `json:"last_response,omitempty"`
	ModelChoices     []string             `json:"model_choices,omitempty"`
	EffortChoices    []string             `json:"effort_choices,omitempty"`
	CurrentModel     string               `json:"current_model,omitempty"`
	CurrentEffort    string               `json:"current_effort,omitempty"`
	ThinkingChoices  []string             `json:"thinking_choices,omitempty"`
	CurrentThinking  string               `json:"current_thinking,omitempty"`
	AutoAccept       bool                 `json:"auto_accept,omitempty"`
	AutoAcceptKnown  bool                 `json:"auto_accept_known,omitempty"`
	ViewMode         string               `json:"view_mode,omitempty"`
	LocalResult      *LocalResult         `json:"local_result,omitempty"`
	Hook             *HookRun             `json:"hook,omitempty"`
	Skills           []SkillView          `json:"skills,omitempty"`
	Plugins          []PluginStatus       `json:"plugins,omitempty"`
	Open             bool                 `json:"open,omitempty"`
	Hooks            *HooksManagerState   `json:"hooks,omitempty"`
	WorktreeExit     *WorktreeExitSummary `json:"worktree_exit,omitempty"`
	SessionID        string               `json:"session_id,omitempty"`
	Messages         []Message            `json:"messages,omitempty"`
}

type HooksManagerState struct {
	Entries           []HookEntry        `json:"entries,omitempty"`
	Events            []HookEventSummary `json:"events,omitempty"`
	ReviewNeededCount int                `json:"review_needed_count,omitempty"`
}

type HookEventSummary struct {
	Event       string `json:"event"`
	Description string `json:"description,omitempty"`
	Installed   int    `json:"installed"`
	Active      int    `json:"active"`
	Review      int    `json:"review"`
}

type HookEntry struct {
	Key         string `json:"key"`
	Event       string `json:"event"`
	Type        string `json:"type,omitempty"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source,omitempty"`
	Match       string `json:"match,omitempty"`
	Command     string `json:"command,omitempty"`
	Description string `json:"description,omitempty"`
	TimeoutSec  int    `json:"timeout_sec,omitempty"`
	CWD         string `json:"cwd,omitempty"`
	Hash        string `json:"hash,omitempty"`
	Enabled     bool   `json:"enabled"`
	Managed     bool   `json:"managed"`
	Active      bool   `json:"active"`
	Trust       string `json:"trust,omitempty"`
}

type HookRun struct {
	ID         string `json:"id,omitempty"`
	Event      string `json:"event,omitempty"`
	Name       string `json:"name,omitempty"`
	Source     string `json:"source,omitempty"`
	Command    string `json:"command,omitempty"`
	Status     string `json:"status,omitempty"`
	Decision   string `json:"decision,omitempty"`
	ExitCode   int    `json:"exit_code,omitempty"`
	Message    string `json:"message,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
}

type ProgressStep struct {
	ToolName string `json:"tool_name,omitempty"`
	Status   string `json:"status,omitempty"`
	Summary  string `json:"summary,omitempty"`
}

type UserInputOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type UserInputQuestion struct {
	Header   string            `json:"header"`
	ID       string            `json:"id"`
	Question string            `json:"question"`
	Options  []UserInputOption `json:"options"`
}

type Message struct {
	ID           string       `json:"id,omitempty"`
	SessionID    string       `json:"session_id,omitempty"`
	Role         string       `json:"role,omitempty"`
	Text         string       `json:"text,omitempty"`
	Hidden       bool         `json:"hidden,omitempty"`
	Reasoning    string       `json:"reasoning,omitempty"`
	ToolCalls    []ToolCall   `json:"tool_calls,omitempty"`
	ToolResults  []ToolResult `json:"tool_results,omitempty"`
	FinishReason string       `json:"finish_reason,omitempty"`
	CreatedAt    time.Time    `json:"created_at,omitempty"`
	UpdatedAt    time.Time    `json:"updated_at,omitempty"`
}

type ToolCall struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input string `json:"input,omitempty"`
}

type ToolResult struct {
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
	Content    string         `json:"content,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	IsError    bool           `json:"is_error,omitempty"`
}

type LocalResult struct {
	Kind                  string                 `json:"kind,omitempty"`
	Title                 string                 `json:"title,omitempty"`
	Fields                []LocalResultField     `json:"fields,omitempty"`
	Sections              []LocalResultSection   `json:"sections,omitempty"`
	Actions               []LocalResultAction    `json:"actions,omitempty"`
	PlainText             string                 `json:"plain_text,omitempty"`
	WorkflowPanelSnapshot *WorkflowPanelSnapshot `json:"workflow_panel_snapshot,omitempty"`
}

type LocalResultSection struct {
	Title  string             `json:"title,omitempty"`
	Fields []LocalResultField `json:"fields,omitempty"`
}

type LocalResultField struct {
	Label string `json:"label,omitempty"`
	Value string `json:"value,omitempty"`
	Tone  string `json:"tone,omitempty"`
}

type LocalResultAction struct {
	Label          string `json:"label,omitempty"`
	Description    string `json:"description,omitempty"`
	Command        string `json:"command,omitempty"`
	Tone           string `json:"tone,omitempty"`
	WorkflowName   string `json:"workflow_name,omitempty"`
	WorkflowArgs   string `json:"workflow_args,omitempty"`
	WorkflowResume string `json:"workflow_resume,omitempty"`
	WorkflowTrust  bool   `json:"workflow_trust,omitempty"`
}

type WorkflowPanelSnapshot struct {
	RunID        string               `json:"run_id,omitempty"`
	Status       string               `json:"status,omitempty"`
	Summary      string               `json:"summary,omitempty"`
	Error        string               `json:"error,omitempty"`
	Budget       string               `json:"budget,omitempty"`
	CurrentPhase string               `json:"current_phase,omitempty"`
	StartedAt    time.Time            `json:"started_at,omitempty"`
	EndedAt      time.Time            `json:"ended_at,omitempty"`
	ElapsedMS    int64                `json:"elapsed_ms,omitempty"`
	Phases       []WorkflowPanelPhase `json:"phases,omitempty"`
	Logs         []string             `json:"logs,omitempty"`
	Result       any                  `json:"result,omitempty"`
}

type WorkflowPanelPhase struct {
	Name      string              `json:"name,omitempty"`
	Status    string              `json:"status,omitempty"`
	Done      int                 `json:"done,omitempty"`
	Running   int                 `json:"running,omitempty"`
	Failed    int                 `json:"failed,omitempty"`
	Cancelled int                 `json:"cancelled,omitempty"`
	Cached    int                 `json:"cached,omitempty"`
	Total     int                 `json:"total,omitempty"`
	Tasks     []WorkflowPanelTask `json:"tasks,omitempty"`
}

type WorkflowPanelTask struct {
	ID               string                  `json:"id,omitempty"`
	Sequence         int                     `json:"sequence,omitempty"`
	Phase            string                  `json:"phase,omitempty"`
	Label            string                  `json:"label,omitempty"`
	Status           string                  `json:"status,omitempty"`
	Model            string                  `json:"model,omitempty"`
	ActorKind        string                  `json:"actor_kind,omitempty"`
	Prompt           string                  `json:"prompt,omitempty"`
	Outcome          string                  `json:"outcome,omitempty"`
	Error            string                  `json:"error,omitempty"`
	Message          string                  `json:"message,omitempty"`
	Cached           bool                    `json:"cached,omitempty"`
	IsChild          bool                    `json:"is_child,omitempty"`
	StartedAt        time.Time               `json:"started_at,omitempty"`
	CompletedAt      time.Time               `json:"completed_at,omitempty"`
	DurationMS       int64                   `json:"duration_ms,omitempty"`
	PromptTokens     int64                   `json:"prompt_tokens,omitempty"`
	CompletionTokens int64                   `json:"completion_tokens,omitempty"`
	TotalTokens      int64                   `json:"total_tokens,omitempty"`
	PromptCacheHit   int64                   `json:"prompt_cache_hit,omitempty"`
	PromptCacheMiss  int64                   `json:"prompt_cache_miss,omitempty"`
	ReasoningReplay  int64                   `json:"reasoning_replay,omitempty"`
	ToolReplayTokens int64                   `json:"tool_replay_tokens,omitempty"`
	ToolRawTokens    int64                   `json:"tool_raw_tokens,omitempty"`
	ToolTokensSaved  int64                   `json:"tool_tokens_saved,omitempty"`
	ToolCompacted    int64                   `json:"tool_compacted,omitempty"`
	ToolCalls        int                     `json:"tool_calls,omitempty"`
	ToolCallNames    []string                `json:"tool_call_names,omitempty"`
	Activity         []WorkflowPanelActivity `json:"activity,omitempty"`
}

type WorkflowPanelActivity struct {
	Time     time.Time `json:"time,omitempty"`
	Message  string    `json:"message,omitempty"`
	ToolName string    `json:"tool_name,omitempty"`
}

type SkillView struct {
	Name          string               `json:"name,omitempty"`
	Description   string               `json:"description,omitempty"`
	When          string               `json:"when,omitempty"`
	Path          string               `json:"path,omitempty"`
	SkillFilePath string               `json:"skill_file_path,omitempty"`
	Source        string               `json:"source,omitempty"`
	Status        string               `json:"status,omitempty"`
	Reason        string               `json:"reason,omitempty"`
	Missing       []MissingRequirement `json:"missing,omitempty"`
}

type MissingRequirement struct {
	Kind   string `json:"kind,omitempty"`
	Name   string `json:"name,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type PluginStatus struct {
	Manifest    PluginManifest     `json:"manifest"`
	Enabled     bool               `json:"enabled,omitempty"`
	Commands    []PluginCommand    `json:"commands,omitempty"`
	Tools       []string           `json:"tools,omitempty"`
	Skills      []string           `json:"skills,omitempty"`
	Agents      []string           `json:"agents,omitempty"`
	Rules       []string           `json:"rules,omitempty"`
	Hooks       []string           `json:"hooks,omitempty"`
	Services    []PluginService    `json:"services,omitempty"`
	Diagnostics []PluginDiagnostic `json:"diagnostics,omitempty"`
	Paths       map[string]string  `json:"paths,omitempty"`
}

type PluginManifest struct {
	ID           string   `json:"id,omitempty"`
	Name         string   `json:"name,omitempty"`
	Version      string   `json:"version,omitempty"`
	Description  string   `json:"description,omitempty"`
	Official     bool     `json:"official,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	Permissions  []string `json:"permissions,omitempty"`
	Status       string   `json:"status,omitempty"`
}

type PluginCommand struct {
	Name        string `json:"name,omitempty"`
	Usage       string `json:"usage,omitempty"`
	Description string `json:"description,omitempty"`
	Class       string `json:"class,omitempty"`
	StartsTurn  bool   `json:"starts_turn,omitempty"`
}

type PluginService struct {
	Name   string `json:"name,omitempty"`
	Status string `json:"status,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type PluginDiagnostic struct {
	PluginID string `json:"plugin_id,omitempty"`
	Level    string `json:"level,omitempty"`
	Label    string `json:"label,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type WorktreeSession struct {
	Name               string `json:"name,omitempty"`
	Workspace          string `json:"workspace,omitempty"`
	Path               string `json:"path,omitempty"`
	Branch             string `json:"branch,omitempty"`
	OriginalWorkspace  string `json:"original_workspace,omitempty"`
	OriginalBranch     string `json:"original_branch,omitempty"`
	OriginalHeadCommit string `json:"original_head_commit,omitempty"`
}

type WorktreeExitSummary struct {
	Session      WorktreeSession `json:"session"`
	ChangedFiles int             `json:"changed_files,omitempty"`
	IgnoredFiles int             `json:"ignored_files,omitempty"`
	Commits      int             `json:"commits,omitempty"`
}

type ToolSpec struct {
	Name             string         `json:"name,omitempty"`
	Description      string         `json:"description,omitempty"`
	Parameters       map[string]any `json:"parameters,omitempty"`
	ReadOnly         bool           `json:"read_only,omitempty"`
	Capabilities     []string       `json:"capabilities,omitempty"`
	ApprovalHint     string         `json:"approval_hint,omitempty"`
	SupportsParallel bool           `json:"supports_parallel,omitempty"`
}

type ApprovalRequest struct {
	SessionID string         `json:"session_id,omitempty"`
	ToolCall  ToolCall       `json:"tool_call"`
	Spec      ToolSpec       `json:"spec,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	Code      string         `json:"code,omitempty"`
	Key       string         `json:"key,omitempty"`
	Keys      []string       `json:"keys,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}
