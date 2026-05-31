package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/store"
)

type SpawnSubagentRequest struct {
	Task              string         `json:"task"`
	Role              string         `json:"role,omitempty"`
	Model             string         `json:"model,omitempty"`
	MaxToolIters      int            `json:"max_tool_iters,omitempty"`
	MaxToolCalls      int            `json:"max_tool_calls,omitempty"`
	Capabilities      []string       `json:"capabilities,omitempty"`
	OutputSchema      map[string]any `json:"output_schema,omitempty"`
	ParentToolCallID  string         `json:"-"`
	WorkflowRunID     string         `json:"-"`
	WorkflowName      string         `json:"-"`
	WorkflowPhase     string         `json:"-"`
	WorkflowTaskID    string         `json:"-"`
	WorkflowTaskLabel string         `json:"-"`
}

type SpawnSubagentResponse struct {
	SessionID         string    `json:"session_id"`
	Role              string    `json:"role"`
	Model             string    `json:"model"`
	PermissionProfile string    `json:"permission_profile"`
	Status            string    `json:"status"`
	Summary           string    `json:"summary"`
	StructuredResult  any       `json:"structured_result,omitempty"`
	Error             string    `json:"error,omitempty"`
	Truncated         bool      `json:"truncated"`
	ToolCalls         []string  `json:"tool_calls,omitempty"`
	Usage             llm.Usage `json:"usage,omitempty"`
	DurationMS        int64     `json:"duration_ms"`
	CompletedAt       string    `json:"completed_at"`
}

type SpawnSubagentError struct {
	SessionID string
	Code      string
	Message   string
	Err       error
}

func (e *SpawnSubagentError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "subagent failed"
}

func (e *SpawnSubagentError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (r *Runner) SpawnSubagent(ctx context.Context, req SpawnSubagentRequest) (SpawnSubagentResponse, error) {
	return r.SpawnSubagentWithProgress(ctx, req, nil)
}

func (r *Runner) AllowedSubagentTools(req SpawnSubagentRequest) ([]string, error) {
	return AllowedCapabilityToolNames(r.parentTools, req.Capabilities)
}

func (r *Runner) SpawnSubagentWithProgress(ctx context.Context, req SpawnSubagentRequest, progress func(core.ToolProgress)) (SpawnSubagentResponse, error) {
	task := strings.TrimSpace(req.Task)
	if task == "" {
		return SpawnSubagentResponse{}, errors.New("task is required")
	}
	if r.providerFactory == nil {
		return SpawnSubagentResponse{}, errors.New("provider factory is not configured")
	}
	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = "explore"
	}
	if !validRole(role) {
		return SpawnSubagentResponse{}, fmt.Errorf("unsupported subagent role %q", role)
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = r.defaultModel
	}
	maxToolIters := req.MaxToolIters
	if maxToolIters <= 0 {
		maxToolIters = r.defaultMaxToolIters
	}
	maxToolCalls := req.MaxToolCalls
	childTools, err := BuildCapabilityRegistry(r.parentTools, req.Capabilities)
	if err != nil {
		return SpawnSubagentResponse{}, err
	}
	var structuredCapture *structuredOutputCapture
	if len(req.OutputSchema) > 0 {
		structuredCapture = &structuredOutputCapture{}
		tools := childTools.Tools()
		tools = append(tools, structuredOutputTool{schema: req.OutputSchema, capture: structuredCapture})
		childTools, err = core.NewToolRegistryChecked(tools)
		if err != nil {
			return SpawnSubagentResponse{}, err
		}
	}
	provider, err := r.providerFactory(model, 0)
	if err != nil {
		return SpawnSubagentResponse{}, err
	}
	sessionID := r.childSessionID(req.ParentToolCallID)
	childStore := r.messageStore
	if childStore == nil {
		childStore = store.NewInMemoryStore()
	}
	start := time.Now()
	parentSessionID := r.currentParentSessionID()
	r.saveSubagentMeta(sessionID, session.SessionMeta{
		Kind:            "subagent",
		ParentSessionID: parentSessionID,
		Role:            role,
		Model:           model,
		Task:            task,
		Status:          "running",
		Workspace:       r.workspaceRoot,
		StartedAt:       start.UTC(),
	})
	extraBlocks := []string{subagentSystemBlock(role)}
	if workflowBlock := workflowContextSystemBlock(req); workflowBlock != "" {
		extraBlocks = append(extraBlocks, workflowBlock)
	}
	if schemaBlock := outputSchemaSystemBlock(req.OutputSchema); schemaBlock != "" {
		extraBlocks = append(extraBlocks, schemaBlock)
	}
	newChild := func(registry *core.ToolRegistry, maxIters int) *agent.Agent {
		return agent.NewAgentWithRegistry(provider, childStore, registry,
			agent.WithSessionMode(session.ModeAsk),
			agent.WithToolPolicy(policy.RulePolicy{Default: policy.PermissionAllow, Rules: policy.DefaultRules(), WorkspaceRoot: r.workspaceRoot}),
			// The child registry is already restricted to read-only tools
			// (BuildReadOnlyRegistry) and a subagent has no interactive approval
			// path, so auto-approve "ask" decisions instead of defaulting them to
			// denied. This keeps read-only MCP/memory tools usable, matching the
			// pre-RulePolicy behavior; "deny" rules still produce a non-Allow
			// decision and are enforced before the approval callback runs.
			agent.WithApprovalFunc(func(approvalReq policy.ApprovalRequest) policy.ApprovalDecision {
				if r.approvalFunc == nil {
					return policy.ApprovalAllow
				}
				approvalReq.Metadata = workflowApprovalMetadata(approvalReq.Metadata, req)
				return r.approvalFunc(approvalReq)
			}),
			agent.WithSessionsDir(r.sessionsDir),
			agent.WithAutoCompact(r.autoCompact, r.autoCompactThreshold, r.contextWindowForModel(model)),
			agent.WithProjectMemory(r.memoryEnabled, r.memoryMaxChars, r.memoryFileOrder, r.workspaceRoot),
			agent.WithUsageLogPath(r.usageLogPath),
			agent.WithMaxToolIters(maxIters),
			agent.WithMaxToolCalls(maxToolCalls),
			agent.WithExtraSystemBlocks(extraBlocks...),
		)
	}
	child := newChild(childTools, maxToolIters)
	events, err := child.RunStream(ctx, sessionID, task)
	if err != nil {
		r.patchSubagentMeta(sessionID, session.SessionMeta{Status: "failed", Error: err.Error(), CompletedAt: time.Now().UTC()})
		return SpawnSubagentResponse{}, &SpawnSubagentError{SessionID: sessionID, Code: "spawn_subagent_failed", Message: err.Error(), Err: err}
	}
	var summary string
	var usage llm.Usage
	var truncated bool
	var toolCalls []string
	childActions := map[string]childToolAction{}
	progressCount := 0
	var progressMessages []core.SubagentStep
	fail := func(code string, err error) (SpawnSubagentResponse, error) {
		msg := "subagent failed"
		if code == "cancelled" {
			msg = "turn cancelled"
		}
		if err != nil {
			msg = err.Error()
		}
		r.patchSubagentMeta(sessionID, session.SessionMeta{Status: code, Error: msg, CompletedAt: time.Now().UTC()})
		return SpawnSubagentResponse{}, &SpawnSubagentError{SessionID: sessionID, Code: code, Message: msg, Err: err}
	}
	drainEvents := func(events <-chan agent.AgentEvent) (string, error) {
		for ev := range events {
			switch ev.Type {
			case agent.AgentEventTypeToolCall:
				if ev.ToolCall != nil {
					internalStructuredOutput := ev.ToolCall.Name == structuredOutputToolName
					if !internalStructuredOutput {
						toolCalls = append(toolCalls, ev.ToolCall.Name)
					}
					action := summarizeChildToolCall(*ev.ToolCall)
					childActions[ev.ToolCall.ID] = action
					if internalStructuredOutput {
						continue
					}
					progressCount++
					step := core.SubagentStep{
						ToolName: ev.ToolCall.Name,
						Status:   "running",
						Summary:  action.Running,
					}
					progressMessages = append(progressMessages, step)
					emitSubagentProgressWithSteps(progress, role, model, progressCount, "running", action.Running, progressMessages, map[string]any{
						"child_session_id": sessionID,
						"child_tool":       ev.ToolCall.Name,
					})
				}
			case agent.AgentEventTypeToolResult:
				if ev.Result != nil {
					if ev.Result.Name == structuredOutputToolName {
						continue
					}
					progressCount++
					status := "running"
					if ev.Result.IsError {
						status = "tool_failed"
					}
					action := childActions[ev.Result.ToolCallID]
					summary := summarizeChildToolResult(*ev.Result, action)
					step := core.SubagentStep{
						ToolName: ev.Result.Name,
						Status:   status,
						Summary:  summary,
					}
					progressMessages = append(progressMessages, step)
					emitSubagentProgressWithSteps(progress, role, model, progressCount, status, summary, progressMessages, map[string]any{
						"child_session_id": sessionID,
						"child_tool":       ev.Result.Name,
					})
				}
			case agent.AgentEventTypeDone:
				if ev.Message != nil {
					summary, truncated = truncateString(strings.TrimSpace(ev.Message.Text), r.summaryMaxChars)
					progressSummary := summary
					if progressSummary == "" {
						progressSummary = "child completed"
					}
					progressMessages = append(progressMessages, core.SubagentStep{
						ToolName: "subagent",
						Status:   "completed",
						Summary:  progressSummary,
					})
					emitSubagentProgressWithSteps(progress, role, model, progressCount, "completed", progressSummary, progressMessages, map[string]any{
						"child_session_id": sessionID,
						"truncated":        truncated,
					})
				}
			case agent.AgentEventTypeUsage:
				if ev.Usage != nil {
					usage = addUsage(usage, ev.Usage.Usage)
				}
			case agent.AgentEventTypeError:
				if ev.Err != nil {
					return "failed", ev.Err
				}
				return "failed", errors.New("subagent failed")
			case agent.AgentEventTypeTurnCancelled:
				return "cancelled", ctx.Err()
			default:
				if status, eventSummary, metadata, ok := summarizeChildAgentEvent(ev); ok {
					progressCount++
					if metadata == nil {
						metadata = map[string]any{}
					}
					metadata["child_session_id"] = sessionID
					step := core.SubagentStep{
						ToolName: "agent_event",
						Status:   status,
						Summary:  eventSummary,
					}
					progressMessages = append(progressMessages, step)
					emitSubagentProgressWithSteps(progress, role, model, progressCount, status, eventSummary, progressMessages, metadata)
				}
				// Other child events are intentionally drained here. The parent only
				// exposes stable subagent lifecycle/progress updates, not every
				// internal child-agent stream event.
			}
			if err := ctx.Err(); err != nil {
				return "cancelled", err
			}
		}
		return "", nil
	}
	if code, err := drainEvents(events); err != nil {
		return fail(code, err)
	}
	if len(req.OutputSchema) > 0 {
		if _, ok := structuredCapture.get(); !ok {
			repairPrompt := structuredOutputRepairPrompt(structuredCapture.error(), summary)
			repairTools, err := core.NewToolRegistryChecked([]core.Tool{structuredOutputTool{schema: req.OutputSchema, capture: structuredCapture}})
			if err != nil {
				return fail("structured_output_missing", err)
			}
			repairChild := newChild(repairTools, 1)
			repairEvents, err := repairChild.RunStreamWithOptions(ctx, sessionID, repairPrompt, true)
			if err != nil {
				return fail("structured_output_missing", err)
			}
			if code, err := drainEvents(repairEvents); err != nil {
				return fail(code, err)
			}
		}
	}
	completedAt := time.Now().UTC()
	var structuredResult any
	if len(req.OutputSchema) > 0 {
		value, ok := structuredCapture.get()
		if !ok {
			code := "structured_output_missing"
			msg := "subagent finished without calling structured_output"
			if lastErr := structuredCapture.error(); lastErr != "" {
				code = "structured_output_invalid"
				msg = "subagent did not submit valid structured output: " + lastErr
			}
			err := errors.New(msg)
			r.patchSubagentMeta(sessionID, session.SessionMeta{Status: "failed", Error: err.Error(), CompletedAt: completedAt})
			return SpawnSubagentResponse{}, &SpawnSubagentError{SessionID: sessionID, Code: code, Message: err.Error(), Err: err}
		}
		structuredResult = value
		if strings.TrimSpace(summary) == "" {
			if b, err := json.Marshal(value); err == nil {
				summary, truncated = truncateString(string(b), r.summaryMaxChars)
			}
		}
	}
	r.patchSubagentMeta(sessionID, session.SessionMeta{Status: "completed", Summary: summary, CompletedAt: completedAt})
	return SpawnSubagentResponse{
		SessionID:         sessionID,
		Role:              role,
		Model:             model,
		PermissionProfile: "read_only",
		Status:            "completed",
		Summary:           summary,
		StructuredResult:  structuredResult,
		Truncated:         truncated,
		ToolCalls:         toolCalls,
		Usage:             usage,
		DurationMS:        time.Since(start).Milliseconds(),
		CompletedAt:       completedAt.Format(time.RFC3339),
	}, nil
}

func workflowContextSystemBlock(req SpawnSubagentRequest) string {
	name := strings.TrimSpace(req.WorkflowName)
	runID := strings.TrimSpace(req.WorkflowRunID)
	phase := strings.TrimSpace(req.WorkflowPhase)
	label := strings.TrimSpace(req.WorkflowTaskLabel)
	if name == "" && runID == "" && phase == "" && label == "" {
		return ""
	}
	lines := []string{"Workflow context:"}
	if name != "" {
		lines = append(lines, "- workflow: "+name)
	}
	if runID != "" {
		lines = append(lines, "- run: "+runID)
	}
	if phase != "" {
		lines = append(lines, "- phase: "+phase)
	}
	if label != "" {
		lines = append(lines, "- task: "+label)
	}
	lines = append(lines, "Mention this workflow context in user-facing tool approval rationale or progress summaries when relevant.")
	return strings.Join(lines, "\n")
}

func workflowApprovalMetadata(existing map[string]any, req SpawnSubagentRequest) map[string]any {
	if strings.TrimSpace(req.WorkflowName) == "" && strings.TrimSpace(req.WorkflowRunID) == "" && strings.TrimSpace(req.WorkflowPhase) == "" && strings.TrimSpace(req.WorkflowTaskLabel) == "" {
		return existing
	}
	out := map[string]any{}
	for k, v := range existing {
		out[k] = v
	}
	if req.WorkflowRunID != "" {
		out["workflow_run_id"] = req.WorkflowRunID
	}
	if req.WorkflowName != "" {
		out["workflow_name"] = req.WorkflowName
	}
	if req.WorkflowPhase != "" {
		out["workflow_phase"] = req.WorkflowPhase
	}
	if req.WorkflowTaskID != "" {
		out["workflow_task_id"] = req.WorkflowTaskID
	}
	if req.WorkflowTaskLabel != "" {
		out["workflow_task_label"] = req.WorkflowTaskLabel
	}
	return out
}

func (r *Runner) contextWindowForModel(model string) int {
	return defaults.ContextWindowForModel(model)
}

func (r *Runner) childSessionID(parentToolCallID string) string {
	childID := safeSessionPart(parentToolCallID)
	if childID == "" {
		childID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	parentID := safeSessionPart(r.currentParentSessionID())
	if parentID == "" {
		return "subagent-" + childID
	}
	return parentID + "--subagent-" + childID
}

func (r *Runner) currentParentSessionID() string {
	if r != nil && r.parentSessionIDFunc != nil {
		if id := strings.TrimSpace(r.parentSessionIDFunc()); id != "" {
			return id
		}
	}
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.parentSessionID)
}

func safeSessionPart(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, v)
	out = strings.Trim(out, "-_.")
	if len(out) > 96 {
		out = out[:96]
	}
	return out
}

func (r *Runner) saveSubagentMeta(sessionID string, meta session.SessionMeta) {
	if strings.TrimSpace(r.sessionsDir) == "" || strings.TrimSpace(sessionID) == "" {
		return
	}
	_ = session.SaveSessionMeta(r.sessionsDir, sessionID, meta)
}

func (r *Runner) patchSubagentMeta(sessionID string, meta session.SessionMeta) {
	if strings.TrimSpace(r.sessionsDir) == "" || strings.TrimSpace(sessionID) == "" {
		return
	}
	_, _ = session.PatchSessionMeta(r.sessionsDir, sessionID, session.SessionMetaPatchFromMeta(meta))
}
