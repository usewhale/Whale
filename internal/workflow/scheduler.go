package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/tasks"
)

type AgentSpawner interface {
	SpawnSubagentWithProgress(ctx context.Context, req tasks.SpawnSubagentRequest, progress func(core.ToolProgress)) (tasks.SpawnSubagentResponse, error)
}

type AgentToolPlanner interface {
	AllowedSubagentTools(req tasks.SpawnSubagentRequest) ([]string, error)
}

type TaskScheduler struct {
	Store   RunEventStore
	Spawner AgentSpawner
	Now     func() time.Time
}

func NewTaskScheduler(store RunEventStore, spawner AgentSpawner) *TaskScheduler {
	return &TaskScheduler{Store: store, Spawner: spawner, Now: time.Now}
}

func workflowAgentDefinition(spec AgentTaskSpec) tasks.AgentDefinition {
	def := spec.Agent
	if strings.TrimSpace(def.Name) == "" {
		def.Name = strings.TrimSpace(spec.Role)
	}
	if strings.TrimSpace(spec.Model) != "" {
		def.Model = strings.TrimSpace(spec.Model)
	}
	if strings.TrimSpace(spec.Effort) != "" {
		def.Effort = strings.TrimSpace(spec.Effort)
	}
	if strings.TrimSpace(spec.PermissionMode) != "" {
		def.PermissionMode = strings.TrimSpace(spec.PermissionMode)
	}
	if spec.MaxTurns > 0 {
		def.MaxTurns = spec.MaxTurns
	}
	if spec.Background {
		def.Background = true
	}
	if strings.TrimSpace(spec.Isolation) != "" {
		def.Isolation = strings.TrimSpace(spec.Isolation)
	}
	if spec.Skills != nil {
		def.Skills = cloneStringSlice(spec.Skills)
	}
	if spec.MCPServers != nil {
		def.MCPServers = cloneStringSlice(spec.MCPServers)
	}
	if strings.TrimSpace(spec.InitialPrompt) != "" {
		def.InitialPrompt = strings.TrimSpace(spec.InitialPrompt)
	}
	if strings.TrimSpace(spec.Memory) != "" {
		def.Memory = strings.TrimSpace(spec.Memory)
	}
	if spec.Capabilities != nil {
		def.Tools = cloneStringSlice(spec.Capabilities)
	}
	return def
}

func (s *TaskScheduler) SpawnAgent(ctx context.Context, actor ActorContext, spec AgentTaskSpec) (AgentTaskResult, error) {
	if strings.TrimSpace(spec.Prompt) == "" {
		return AgentTaskResult{}, errors.New("prompt is required")
	}
	if s == nil || s.Store == nil {
		return AgentTaskResult{}, errors.New("run event store is required")
	}
	if s.Spawner == nil {
		return AgentTaskResult{}, errors.New("agent spawner is required")
	}
	if actor.RunID == "" {
		return AgentTaskResult{}, errors.New("run_id is required")
	}
	if actor.TaskID == "" {
		actor.TaskID = TaskID("task-" + uuid.NewString())
	}
	actor.ActorKind = firstNonEmpty(actor.ActorKind, ActorKindSubagent)
	actor.Role = firstNonEmpty(actor.Role, spec.Role, spec.Agent.Name)
	actor.Phase = firstNonEmpty(actor.Phase, spec.Phase)
	actor.Label = firstNonEmpty(actor.Label, spec.Label)
	def := workflowAgentDefinition(spec)
	req := tasks.SpawnSubagentRequest{
		Task:              spec.Prompt,
		Role:              actor.Role,
		Agent:             def,
		Model:             spec.Model,
		MaxToolIters:      spec.MaxToolIters,
		MaxToolCalls:      spec.MaxToolCalls,
		Capabilities:      cloneStringSlice(spec.Capabilities),
		OutputSchema:      cloneMap(spec.OutputSchema),
		ParentToolCallID:  string(actor.TaskID),
		WorkflowRunID:     string(actor.RunID),
		WorkflowName:      actor.WorkflowName,
		WorkflowPhase:     actor.Phase,
		WorkflowTaskID:    string(actor.TaskID),
		WorkflowTaskLabel: actor.Label,
	}
	var allowedTools []string
	if planner, ok := s.Spawner.(AgentToolPlanner); ok {
		var err error
		allowedTools, err = planner.AllowedSubagentTools(req)
		if err != nil {
			return AgentTaskResult{TaskID: actor.TaskID, Status: TaskStatusFailed, Error: err.Error()}, err
		}
	}
	start := s.now().UTC()
	if err := s.Store.Append(ctx, RunEvent{
		RunID:        actor.RunID,
		TaskID:       actor.TaskID,
		Type:         EventTaskStarted,
		Time:         start,
		Status:       TaskStatusRunning,
		ParentTaskID: actor.ParentTaskID,
		WorkflowName: actor.WorkflowName,
		Phase:        actor.Phase,
		Label:        actor.Label,
		Role:         actor.Role,
		Message:      strings.TrimSpace(spec.Prompt),
		Data: map[string]any{
			"actor_kind":        actor.ActorKind,
			"parent_session_id": actor.ParentSessionID,
			"model":             strings.TrimSpace(def.Model),
			"agent":             def,
			"effort":            strings.TrimSpace(def.Effort),
			"permission_mode":   strings.TrimSpace(def.PermissionMode),
			"max_turns":         def.MaxTurns,
			"background":        def.Background,
			"isolation":         strings.TrimSpace(def.Isolation),
			"skills":            def.Skills,
			"mcp_servers":       def.MCPServers,
			"initial_prompt":    strings.TrimSpace(def.InitialPrompt),
			"memory":            strings.TrimSpace(def.Memory),
			"max_tool_iters":    spec.MaxToolIters,
			"max_tool_calls":    spec.MaxToolCalls,
			"capabilities":      spec.Capabilities,
			"allowed_tools":     allowedTools,
			"resume":            workflowResumeData(actor.CallKey, actor.SpecHash, actor.Sequence),
		},
	}); err != nil {
		return AgentTaskResult{}, err
	}
	res, err := s.Spawner.SpawnSubagentWithProgress(ctx, req, func(p core.ToolProgress) {
		_ = s.Store.Append(context.Background(), RunEvent{
			RunID:        actor.RunID,
			TaskID:       actor.TaskID,
			Type:         EventTaskProgress,
			Time:         s.now().UTC(),
			Status:       normalizeStatus(p.Status, TaskStatusRunning),
			ParentTaskID: actor.ParentTaskID,
			WorkflowName: actor.WorkflowName,
			Phase:        actor.Phase,
			Label:        actor.Label,
			Role:         actor.Role,
			Message:      strings.TrimSpace(p.Summary),
			Data: map[string]any{
				"count":        p.Count,
				"tool_name":    p.ToolName,
				"tool_call_id": p.ToolCallID,
				"metadata":     p.Metadata,
			},
		})
	})
	if err != nil {
		status := TaskStatusFailed
		eventType := EventTaskFailed
		if ctx.Err() != nil {
			status = TaskStatusCancelled
			eventType = EventTaskCancelled
		}
		result := AgentTaskResult{TaskID: actor.TaskID, Status: status, Error: err.Error()}
		var subErr *tasks.SpawnSubagentError
		if errors.As(err, &subErr) {
			result.ChildSessionID = subErr.SessionID
		}
		appendErr := s.Store.Append(context.Background(), RunEvent{
			RunID:        actor.RunID,
			TaskID:       actor.TaskID,
			Type:         eventType,
			Time:         s.now().UTC(),
			Status:       status,
			ParentTaskID: actor.ParentTaskID,
			WorkflowName: actor.WorkflowName,
			Phase:        actor.Phase,
			Label:        actor.Label,
			Role:         actor.Role,
			Message:      err.Error(),
			SessionID:    result.ChildSessionID,
		})
		return result, firstErr(err, appendErr)
	}
	result := AgentTaskResult{
		TaskID:           actor.TaskID,
		ChildSessionID:   res.SessionID,
		Status:           normalizeStatus(res.Status, TaskStatusCompleted),
		Summary:          res.Summary,
		StructuredResult: res.StructuredResult,
		ToolCalls:        append([]string(nil), res.ToolCalls...),
		Usage:            res.Usage,
		DurationMS:       res.DurationMS,
	}
	if len(spec.OutputSchema) > 0 && result.StructuredResult == nil {
		structured, err := parseAndValidateStructuredOutput(res.Summary, spec.OutputSchema)
		if err != nil {
			result.Status = TaskStatusFailed
			result.Error = err.Error()
			data := map[string]any{
				"tool_calls":  result.ToolCalls,
				"duration_ms": result.DurationMS,
				"usage":       usageEventData(result.Usage),
			}
			if resumeData := workflowResumeData(actor.CallKey, actor.SpecHash, actor.Sequence); resumeData != nil {
				data["resume"] = resumeData
			}
			appendErr := s.Store.Append(context.Background(), RunEvent{
				RunID:        actor.RunID,
				TaskID:       actor.TaskID,
				Type:         EventTaskFailed,
				Time:         s.now().UTC(),
				Status:       TaskStatusFailed,
				ParentTaskID: actor.ParentTaskID,
				WorkflowName: actor.WorkflowName,
				Phase:        actor.Phase,
				Label:        actor.Label,
				Role:         actor.Role,
				Message:      err.Error(),
				SessionID:    result.ChildSessionID,
				Data:         data,
			})
			return result, firstErr(err, appendErr)
		}
		result.StructuredResult = structured
	}
	if result.Status == "" {
		result.Status = TaskStatusCompleted
	}
	eventStatus := result.Status
	if eventStatus == TaskStatusRunning {
		eventStatus = TaskStatusCompleted
	}
	data := map[string]any{
		"tool_calls":   result.ToolCalls,
		"duration_ms":  result.DurationMS,
		"usage":        usageEventData(result.Usage),
		"child_status": result.Status,
	}
	if resumeData := workflowResumeData(actor.CallKey, actor.SpecHash, actor.Sequence); resumeData != nil {
		data["resume"] = resumeData
	}
	if result.StructuredResult != nil {
		data["structured_result"] = result.StructuredResult
	}
	if err := s.Store.Append(ctx, RunEvent{
		RunID:        actor.RunID,
		TaskID:       actor.TaskID,
		Type:         EventTaskCompleted,
		Time:         s.now().UTC(),
		Status:       eventStatus,
		ParentTaskID: actor.ParentTaskID,
		WorkflowName: actor.WorkflowName,
		Phase:        actor.Phase,
		Label:        actor.Label,
		Role:         actor.Role,
		Message:      result.Summary,
		SessionID:    result.ChildSessionID,
		Data:         data,
	}); err != nil {
		return result, err
	}
	return result, nil
}

func (s *TaskScheduler) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func firstErr(primary, secondary error) error {
	if primary != nil {
		if secondary != nil {
			return fmt.Errorf("%w; also failed to record event: %v", primary, secondary)
		}
		return primary
	}
	return secondary
}
