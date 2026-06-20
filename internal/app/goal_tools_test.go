package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/telemetry"
)

func TestGoalToolsGetActiveGoal(t *testing.T) {
	app := newGoalTestApp(t)
	if err := session.SaveGoalState(app.sessionsDir, app.sessionID, session.GoalState{
		ID:            "goal-active",
		Objective:     "ship it",
		Status:        session.GoalStatusActive,
		TokenBudget:   100,
		TokenBaseline: 0,
		CreatedAt:     time.Now(),
	}); err != nil {
		t.Fatalf("save goal: %v", err)
	}
	writeUsageRecord(t, filepath.Join(app.cfg.DataDir, "usage"), telemetry.UsageRecord{
		TS:               time.Now().UnixMilli(),
		Session:          app.sessionID,
		Model:            "deepseek-v4-flash",
		PromptTokens:     20,
		CompletionTokens: 10,
	})

	tool := getGoalTool{ctx: goalToolContext{
		dataDir:     app.cfg.DataDir,
		sessionsDir: app.sessionsDir,
		sessionID:   func() string { return app.sessionID },
	}}
	result, err := tool.Run(context.Background(), core.ToolCall{ID: "call-1", Name: tool.Name(), Input: `{}`})
	if err != nil {
		t.Fatalf("get_goal: %v", err)
	}
	if result.IsError() {
		t.Fatalf("get_goal returned error envelope: %s", result.ModelText)
	}
	env := parseGoalToolEnvelope(t, result)
	goal, ok := env.Data["goal"].(map[string]any)
	if !ok {
		t.Fatalf("goal payload = %#v", env.Data["goal"])
	}
	if goal["objective"] != "ship it" || goal["status"] != string(session.GoalStatusActive) {
		t.Fatalf("goal payload = %#v", goal)
	}
	if goal["tokens_used"] != float64(30) || goal["remaining_tokens"] != float64(70) {
		t.Fatalf("token payload = %#v", goal)
	}
}

func TestGetGoalToolDoesNotPersistRefreshedBudgetState(t *testing.T) {
	app := newGoalTestApp(t)
	if err := session.SaveGoalState(app.sessionsDir, app.sessionID, session.GoalState{
		ID:            "goal-active",
		Objective:     "ship it",
		Status:        session.GoalStatusActive,
		TokenBudget:   10,
		TokenBaseline: 0,
		CreatedAt:     time.Now(),
	}); err != nil {
		t.Fatalf("save goal: %v", err)
	}
	writeUsageRecord(t, filepath.Join(app.cfg.DataDir, "usage"), telemetry.UsageRecord{
		TS:               time.Now().UnixMilli(),
		Session:          app.sessionID,
		Model:            "deepseek-v4-flash",
		PromptTokens:     20,
		CompletionTokens: 10,
	})

	tool := getGoalTool{ctx: goalToolContext{
		dataDir:     app.cfg.DataDir,
		sessionsDir: app.sessionsDir,
		sessionID:   func() string { return app.sessionID },
	}}
	result, err := tool.Run(context.Background(), core.ToolCall{ID: "call-1", Name: tool.Name(), Input: `{}`})
	if err != nil {
		t.Fatalf("get_goal: %v", err)
	}
	if !result.IsError() {
		t.Fatalf("over-budget get_goal should report no active goal without persisting: %s", result.ModelText)
	}
	st, ok, err := session.LoadGoalState(app.sessionsDir, app.sessionID)
	if err != nil || !ok {
		t.Fatalf("load goal state ok=%v err=%v", ok, err)
	}
	if st.Status != session.GoalStatusActive || st.TokensUsed != 0 {
		t.Fatalf("get_goal persisted refreshed state: status=%q tokens=%d", st.Status, st.TokensUsed)
	}
}

func TestUpdateGoalToolAdvertisesMutatingState(t *testing.T) {
	spec := core.DescribeTool(updateGoalTool{})
	if spec.ReadOnly {
		t.Fatal("update_goal should not advertise read-only")
	}
}

func TestGoalToolCompleteSettlesUsageAndDoesNotReopen(t *testing.T) {
	app := newGoalTestApp(t)
	if err := session.SaveGoalState(app.sessionsDir, app.sessionID, session.GoalState{
		ID:            "goal-active",
		Objective:     "ship it",
		Status:        session.GoalStatusActive,
		TokenBudget:   25,
		TokenBaseline: 0,
		CreatedAt:     time.Now(),
	}); err != nil {
		t.Fatalf("save goal: %v", err)
	}
	writeUsageRecord(t, filepath.Join(app.cfg.DataDir, "usage"), telemetry.UsageRecord{
		TS:               time.Now().UnixMilli(),
		Session:          app.sessionID,
		Model:            "deepseek-v4-flash",
		PromptTokens:     20,
		CompletionTokens: 10,
	})

	tool := updateGoalTool{ctx: goalToolContext{
		dataDir:     app.cfg.DataDir,
		sessionsDir: app.sessionsDir,
		sessionID:   func() string { return app.sessionID },
	}}
	result, err := tool.Run(context.Background(), core.ToolCall{ID: "call-1", Name: tool.Name(), Input: `{"status":"completed"}`})
	if err != nil {
		t.Fatalf("update_goal: %v", err)
	}
	if result.IsError() {
		t.Fatalf("update_goal returned error envelope: %s", result.ModelText)
	}
	st, ok, err := session.LoadGoalState(app.sessionsDir, app.sessionID)
	if err != nil || !ok {
		t.Fatalf("load completed goal ok=%v err=%v", ok, err)
	}
	if st.Status != session.GoalStatusCompleted {
		t.Fatalf("status = %q, want completed", st.Status)
	}
	if st.TokensUsed != 30 {
		t.Fatalf("tokens used = %d, want 30", st.TokensUsed)
	}

	resumed, err := app.ExecuteLocalCommand("/goal resume")
	if err != nil {
		t.Fatalf("resume completed goal: %v", err)
	}
	if resumed.Turn != nil {
		t.Fatalf("resume completed goal should not start a turn: %#v", resumed.Turn)
	}
}

func TestGoalToolsRejectNonActiveGoal(t *testing.T) {
	app := newGoalTestApp(t)
	for _, status := range []session.GoalStatus{
		session.GoalStatusPaused,
		session.GoalStatusBudgetLimited,
		session.GoalStatusCompleted,
	} {
		if err := session.SaveGoalState(app.sessionsDir, app.sessionID, session.GoalState{
			ID:        "goal-" + string(status),
			Objective: "ship it",
			Status:    status,
			CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("save %s goal: %v", status, err)
		}
		tool := updateGoalTool{ctx: goalToolContext{
			dataDir:     app.cfg.DataDir,
			sessionsDir: app.sessionsDir,
			sessionID:   func() string { return app.sessionID },
		}}
		result, err := tool.Run(context.Background(), core.ToolCall{ID: "call-1", Name: tool.Name(), Input: `{"status":"completed"}`})
		if err != nil {
			t.Fatalf("%s update_goal: %v", status, err)
		}
		if !result.IsError() {
			t.Fatalf("%s update_goal should reject non-active goal: %s", status, result.ModelText)
		}
	}
}

func TestGoalToolRejectsUnsupportedStatus(t *testing.T) {
	app := newGoalTestApp(t)
	if err := session.SaveGoalState(app.sessionsDir, app.sessionID, session.GoalState{
		ID:        "goal-active",
		Objective: "ship it",
		Status:    session.GoalStatusActive,
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("save goal: %v", err)
	}
	tool := updateGoalTool{ctx: goalToolContext{
		dataDir:     app.cfg.DataDir,
		sessionsDir: app.sessionsDir,
		sessionID:   func() string { return app.sessionID },
	}}
	result, err := tool.Run(context.Background(), core.ToolCall{ID: "call-1", Name: tool.Name(), Input: `{"status":"paused"}`})
	if err != nil {
		t.Fatalf("update_goal: %v", err)
	}
	if !result.IsError() {
		t.Fatalf("unsupported status should be rejected: %s", result.ModelText)
	}
}

func parseGoalToolEnvelope(t *testing.T, result core.ToolResult) core.ToolEnvelope {
	t.Helper()
	env, ok := core.ParseToolEnvelope(result.ModelText)
	if !ok {
		t.Fatalf("result content is not a tool envelope: %s", result.ModelText)
	}
	return env
}
