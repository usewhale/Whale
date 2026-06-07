package workflow

import (
	"strings"
	"time"

	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/tasks"
)

type RunID string
type TaskID string

const (
	RunStatusRunning   = "running"
	RunStatusCompleted = "completed"
	RunStatusFailed    = "failed"
	RunStatusCancelled = "cancelled"

	TaskStatusQueued    = "queued"
	TaskStatusRunning   = "running"
	TaskStatusCompleted = "completed"
	TaskStatusFailed    = "failed"
	TaskStatusCancelled = "cancelled"

	ActorKindWorkflow = "workflow"
	ActorKindAgent    = "agent"
	ActorKindSubagent = "subagent"

	EventRunStarted        = "run_started"
	EventRunCompleted      = "run_completed"
	EventRunFailed         = "run_failed"
	EventRunCancelled      = "run_cancelled"
	EventTaskStarted       = "task_started"
	EventTaskProgress      = "task_progress"
	EventTaskCompleted     = "task_completed"
	EventTaskFailed        = "task_failed"
	EventTaskCancelled     = "task_cancelled"
	EventWorkflowStarted   = "workflow_started"
	EventWorkflowCompleted = "workflow_completed"
	EventWorkflowFailed    = "workflow_failed"
	EventBudgetUpdated     = "budget_updated"
	EventPhaseStarted      = "phase_started"
	EventLog               = "log"
	EventScriptReady       = "workflow_script_validated"
)

type ActorContext struct {
	RunID           RunID  `json:"run_id"`
	TaskID          TaskID `json:"task_id,omitempty"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	ActorKind       string `json:"actor_kind,omitempty"`
	ParentTaskID    TaskID `json:"parent_task_id,omitempty"`
	WorkflowName    string `json:"workflow_name,omitempty"`
	Role            string `json:"role,omitempty"`
	Phase           string `json:"phase,omitempty"`
	Label           string `json:"label,omitempty"`
	CallKey         string `json:"call_key,omitempty"`
	SpecHash        string `json:"spec_hash,omitempty"`
	Sequence        int64  `json:"sequence,omitempty"`
}

type AgentTaskSpec struct {
	Prompt         string                `json:"prompt"`
	Role           string                `json:"role,omitempty"`
	Agent          tasks.AgentDefinition `json:"agent,omitempty"`
	Model          string                `json:"model,omitempty"`
	Effort         string                `json:"effort,omitempty"`
	PermissionMode string                `json:"permissionMode,omitempty"`
	MaxTurns       int                   `json:"maxTurns,omitempty"`
	Background     bool                  `json:"background,omitempty"`
	Isolation      string                `json:"isolation,omitempty"`
	Skills         []string              `json:"skills,omitempty"`
	MCPServers     []string              `json:"mcpServers,omitempty"`
	InitialPrompt  string                `json:"initialPrompt,omitempty"`
	Memory         string                `json:"memory,omitempty"`
	MaxToolIters   int                   `json:"max_tool_iters,omitempty"`
	MaxToolCalls   int                   `json:"max_tool_calls,omitempty"`
	Phase          string                `json:"phase,omitempty"`
	Label          string                `json:"label,omitempty"`
	Capabilities   []string              `json:"capabilities,omitempty"`
	OutputSchema   map[string]any        `json:"output_schema,omitempty"`
}

type AgentTaskResult struct {
	TaskID           TaskID    `json:"task_id"`
	ChildSessionID   string    `json:"child_session_id,omitempty"`
	Status           string    `json:"status"`
	Summary          string    `json:"summary,omitempty"`
	StructuredResult any       `json:"structured_result,omitempty"`
	ToolCalls        []string  `json:"tool_calls,omitempty"`
	Usage            llm.Usage `json:"usage,omitempty"`
	DurationMS       int64     `json:"duration_ms,omitempty"`
	Error            string    `json:"error,omitempty"`
}

type RunEvent struct {
	RunID        RunID          `json:"run_id"`
	TaskID       TaskID         `json:"task_id,omitempty"`
	Type         string         `json:"type"`
	Time         time.Time      `json:"time"`
	Message      string         `json:"message,omitempty"`
	Data         map[string]any `json:"data,omitempty"`
	Status       string         `json:"status,omitempty"`
	ParentTaskID TaskID         `json:"parent_task_id,omitempty"`
	WorkflowName string         `json:"workflow_name,omitempty"`
	Phase        string         `json:"phase,omitempty"`
	Label        string         `json:"label,omitempty"`
	Role         string         `json:"role,omitempty"`
	SessionID    string         `json:"session_id,omitempty"`
}

type Run struct {
	ID      RunID      `json:"id"`
	Status  string     `json:"status"`
	Events  []RunEvent `json:"events"`
	Started time.Time  `json:"started_at,omitempty"`
	Ended   time.Time  `json:"ended_at,omitempty"`
	Error   string     `json:"error,omitempty"`
	Summary string     `json:"summary,omitempty"`
}

type WorkflowInput struct {
	Action          string `json:"action,omitempty"`
	Script          string `json:"script,omitempty"`
	Name            string `json:"name,omitempty"`
	Args            any    `json:"args,omitempty"`
	ScriptPath      string `json:"scriptPath,omitempty"`
	SaveAs          string `json:"saveAs,omitempty"`
	ResumeFromRunID string `json:"resumeFromRunId,omitempty"`
	BudgetTokens    *int   `json:"budgetTokens,omitempty"`
}

type WorkflowOutput struct {
	Status        string `json:"status"`
	TaskID        string `json:"taskId"`
	RunID         RunID  `json:"runId,omitempty"`
	Summary       string `json:"summary,omitempty"`
	TranscriptDir string `json:"transcriptDir,omitempty"`
	ScriptPath    string `json:"scriptPath,omitempty"`
	SessionURL    string `json:"sessionUrl,omitempty"`
	Warning       string `json:"warning,omitempty"`
	Error         string `json:"error,omitempty"`
}

type ScriptMeta struct {
	Name                string        `json:"name"`
	Description         string        `json:"description"`
	WhenToUse           string        `json:"whenToUse,omitempty"`
	RiskNote            string        `json:"riskNote,omitempty"`
	EstimatedAgents     int           `json:"estimatedAgents,omitempty"`
	DefaultBudgetTokens int           `json:"defaultBudgetTokens,omitempty"`
	Phases              []ScriptPhase `json:"phases,omitempty"`
}

type ScriptPhase struct {
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Model  string `json:"model,omitempty"`
}

func normalizeStatus(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	return v
}
