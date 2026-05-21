package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/store"
)

type SpawnSubagentRequest struct {
	Task             string `json:"task"`
	Role             string `json:"role,omitempty"`
	Model            string `json:"model,omitempty"`
	MaxToolIters     int    `json:"max_tool_iters,omitempty"`
	ParentToolCallID string `json:"-"`
}

type SpawnSubagentResponse struct {
	SessionID         string   `json:"session_id"`
	Role              string   `json:"role"`
	Model             string   `json:"model"`
	PermissionProfile string   `json:"permission_profile"`
	Status            string   `json:"status"`
	Summary           string   `json:"summary"`
	Error             string   `json:"error,omitempty"`
	Truncated         bool     `json:"truncated"`
	ToolCalls         []string `json:"tool_calls,omitempty"`
	DurationMS        int64    `json:"duration_ms"`
	CompletedAt       string   `json:"completed_at"`
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
	childTools, err := BuildReadOnlyRegistry(r.parentTools)
	if err != nil {
		return SpawnSubagentResponse{}, err
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
	child := agent.NewAgentWithRegistry(provider, childStore, childTools,
		agent.WithSessionMode(session.ModeAsk),
		agent.WithToolPolicy(policy.RulePolicy{Default: policy.PermissionAllow, Rules: policy.DefaultRules(), WorkspaceRoot: r.workspaceRoot}),
		// The child registry is already restricted to read-only tools
		// (BuildReadOnlyRegistry) and a subagent has no interactive approval
		// path, so auto-approve "ask" decisions instead of defaulting them to
		// denied. This keeps read-only MCP/memory tools usable, matching the
		// pre-RulePolicy behavior; "deny" rules still produce a non-Allow
		// decision and are enforced before the approval callback runs.
		agent.WithApprovalFunc(func(policy.ApprovalRequest) policy.ApprovalDecision {
			return policy.ApprovalAllow
		}),
		agent.WithSessionsDir(r.sessionsDir),
		agent.WithProjectMemory(r.memoryEnabled, r.memoryMaxChars, r.memoryFileOrder, r.workspaceRoot),
		agent.WithUsageLogPath(""),
		agent.WithMaxToolIters(maxToolIters),
		agent.WithExtraSystemBlocks(subagentSystemBlock(role)),
	)
	events, err := child.RunStream(ctx, sessionID, task)
	if err != nil {
		r.patchSubagentMeta(sessionID, session.SessionMeta{Status: "failed", Error: err.Error(), CompletedAt: time.Now().UTC()})
		return SpawnSubagentResponse{}, &SpawnSubagentError{SessionID: sessionID, Code: "spawn_subagent_failed", Message: err.Error(), Err: err}
	}
	var summary string
	var toolCalls []string
	childActions := map[string]childToolAction{}
	progressCount := 0
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
	for ev := range events {
		switch ev.Type {
		case agent.AgentEventTypeToolCall:
			if ev.ToolCall != nil {
				toolCalls = append(toolCalls, ev.ToolCall.Name)
				action := summarizeChildToolCall(*ev.ToolCall)
				childActions[ev.ToolCall.ID] = action
				progressCount++
				emitSubagentProgress(progress, role, model, progressCount, "running", action.Running, map[string]any{
					"child_session_id": sessionID,
					"child_tool":       ev.ToolCall.Name,
				})
			}
		case agent.AgentEventTypeToolResult:
			if ev.Result != nil {
				progressCount++
				status := "running"
				if ev.Result.IsError {
					status = "tool_failed"
				}
				action := childActions[ev.Result.ToolCallID]
				emitSubagentProgress(progress, role, model, progressCount, status, summarizeChildToolResult(*ev.Result, action), map[string]any{
					"child_session_id": sessionID,
					"child_tool":       ev.Result.Name,
				})
			}
		case agent.AgentEventTypeDone:
			if ev.Message != nil {
				summary = ev.Message.Text
				emitSubagentProgress(progress, role, model, progressCount, "summarizing", "child produced final summary", map[string]any{
					"child_session_id": sessionID,
				})
			}
		case agent.AgentEventTypeError:
			if ev.Err != nil {
				return fail("failed", ev.Err)
			}
			return fail("failed", errors.New("subagent failed"))
		case agent.AgentEventTypeTurnCancelled:
			return fail("cancelled", ctx.Err())
		}
		if err := ctx.Err(); err != nil {
			return fail("cancelled", err)
		}
	}
	summary, truncated := truncateString(strings.TrimSpace(summary), r.summaryMaxChars)
	completedAt := time.Now().UTC()
	r.patchSubagentMeta(sessionID, session.SessionMeta{Status: "completed", Summary: summary, CompletedAt: completedAt})
	return SpawnSubagentResponse{
		SessionID:         sessionID,
		Role:              role,
		Model:             model,
		PermissionProfile: "read_only",
		Status:            "completed",
		Summary:           summary,
		Truncated:         truncated,
		ToolCalls:         toolCalls,
		DurationMS:        time.Since(start).Milliseconds(),
		CompletedAt:       completedAt.Format(time.RFC3339),
	}, nil
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
	_, _ = session.PatchSessionMeta(r.sessionsDir, sessionID, meta)
}
