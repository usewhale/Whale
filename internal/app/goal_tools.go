package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/session"
)

type goalToolContext struct {
	dataDir     string
	sessionsDir string
	sessionID   func() string
}

func newGoalTools(dataDir, sessionsDir string, sessionID func() string) []core.Tool {
	ctx := goalToolContext{
		dataDir:     dataDir,
		sessionsDir: sessionsDir,
		sessionID:   sessionID,
	}
	return []core.Tool{
		getGoalTool{ctx: ctx},
		updateGoalTool{ctx: ctx},
	}
}

func (g goalToolContext) currentSessionID() string {
	if g.sessionID == nil {
		return ""
	}
	return strings.TrimSpace(g.sessionID())
}

func (g goalToolContext) loadActiveGoal(applyBudgetLimit bool) (session.GoalState, bool, error) {
	sessionID := g.currentSessionID()
	if sessionID == "" {
		return session.GoalState{}, false, nil
	}
	st, ok, err := session.LoadGoalState(g.sessionsDir, sessionID)
	if err != nil || !ok {
		return st, ok, err
	}
	st = refreshGoalUsageWithTotal(st, currentSessionGoalTokens(g.dataDir, sessionID), applyBudgetLimit)
	return st, true, nil
}

func (g goalToolContext) save(st session.GoalState) error {
	return session.SaveGoalState(g.sessionsDir, g.currentSessionID(), st)
}

type getGoalTool struct {
	ctx goalToolContext
}

func (t getGoalTool) Name() string { return "get_goal" }

func (t getGoalTool) Description() string {
	return "Read the current active session goal, including objective, status, token budget, tokens used, and remaining budget."
}

func (t getGoalTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
	}
}

func (t getGoalTool) ReadOnly() bool { return true }

func (t getGoalTool) Run(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	st, ok, err := t.ctx.loadActiveGoal(true)
	if err != nil {
		return core.ToolResult{}, err
	}
	if !ok || st.Status != session.GoalStatusActive {
		return goalToolError(call, "goal_not_active", "No active goal is set.")
	}
	return goalToolSuccess(call, goalToolData(st))
}

type updateGoalTool struct {
	ctx goalToolContext
}

func (t updateGoalTool) Name() string { return "update_goal" }

func (t updateGoalTool) Description() string {
	return "Mark the active session goal as completed after verifying the objective is fully satisfied. Only status=completed is accepted."
}

func (t updateGoalTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{
				"type":        "string",
				"description": "The new goal status. The only accepted value is completed.",
				"enum":        []string{"completed"},
			},
		},
		"required":             []string{"status"},
		"additionalProperties": false,
	}
}

func (t updateGoalTool) ReadOnly() bool { return false }

func (t updateGoalTool) Run(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	var input struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(call.Input)), &input); err != nil {
		return goalToolError(call, "invalid_input", "update_goal expects JSON input with status=completed")
	}
	if input.Status != string(session.GoalStatusCompleted) {
		return goalToolError(call, "unsupported_status", "update_goal only supports status=completed")
	}
	st, ok, err := t.ctx.loadActiveGoal(false)
	if err != nil {
		return core.ToolResult{}, err
	}
	if !ok || st.Status != session.GoalStatusActive {
		return goalToolError(call, "goal_not_active", "No active goal is set.")
	}
	st.Status = session.GoalStatusCompleted
	st.CompletedAt = time.Now()
	if err := t.ctx.save(st); err != nil {
		return core.ToolResult{}, err
	}
	return goalToolSuccess(call, goalToolData(st))
}

func goalToolData(st session.GoalState) map[string]any {
	remaining := 0
	if st.TokenBudget > 0 {
		remaining = max(0, st.TokenBudget-st.TokensUsed)
	}
	return map[string]any{
		"goal": map[string]any{
			"id":                st.ID,
			"objective":         st.Objective,
			"status":            st.Status,
			"token_budget":      st.TokenBudget,
			"tokens_used":       st.TokensUsed,
			"remaining_tokens":  remaining,
			"created_at":        st.CreatedAt,
			"updated_at":        st.UpdatedAt,
			"completed_at":      st.CompletedAt,
			"time_used_seconds": st.TimeUsedSeconds,
		},
		"remaining_tokens": remaining,
	}
}

func goalToolSuccess(call core.ToolCall, data map[string]any) (core.ToolResult, error) {
	content, err := core.MarshalToolEnvelope(core.NewToolSuccessEnvelope(data))
	if err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		ModelText:  content,
	}, nil
}

func goalToolError(call core.ToolCall, code, message string) (core.ToolResult, error) {
	content, err := core.MarshalToolEnvelope(core.NewToolErrorEnvelope(code, message))
	if err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		ModelText:  content,
	}, nil
}
