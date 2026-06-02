package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/telemetry"
)

func newGoalTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	return &App{
		ctx:           context.Background(),
		sessionID:     "goal-session",
		sessionsDir:   filepath.Join(dir, "sessions"),
		workspaceRoot: dir,
		cfg:           Config{DataDir: dir},
	}
}

func TestGoalCommandSetsStateAndStartsHiddenTurn(t *testing.T) {
	app := newGoalTestApp(t)

	res, err := app.ExecuteLocalCommand("/goal --tokens 50k ship the goal command")
	if err != nil {
		t.Fatalf("goal command: %v", err)
	}
	if !res.Handled || !res.Mutated {
		t.Fatalf("handled/mutated = %v/%v, want true/true", res.Handled, res.Mutated)
	}
	if res.Turn == nil {
		t.Fatal("expected goal command to start a turn")
	}
	if !res.Turn.Hidden || !res.Turn.SkipUserPromptHooks || !res.Turn.SkipSkillInjection {
		t.Fatalf("turn flags = hidden:%v hooks:%v skills:%v", res.Turn.Hidden, res.Turn.SkipUserPromptHooks, res.Turn.SkipSkillInjection)
	}
	if !strings.Contains(res.Turn.Input, "ship the goal command") {
		t.Fatalf("turn prompt missing objective:\n%s", res.Turn.Input)
	}
	if !strings.Contains(res.Turn.Input, `MUST call update_goal with {"status":"completed"}`) {
		t.Fatalf("turn prompt missing completion tool protocol:\n%s", res.Turn.Input)
	}

	st, ok, err := session.LoadGoalState(app.sessionsDir, app.sessionID)
	if err != nil {
		t.Fatalf("load goal state: %v", err)
	}
	if !ok {
		t.Fatal("expected goal state to be saved")
	}
	if st.Objective != "ship the goal command" {
		t.Fatalf("objective = %q", st.Objective)
	}
	if st.TokenBudget != 50_000 {
		t.Fatalf("token budget = %d, want 50000", st.TokenBudget)
	}
	if st.Status != session.GoalStatusActive {
		t.Fatalf("status = %q, want active", st.Status)
	}
}

func TestGoalCommandRunsHooksOnObjectiveAndSkipsHiddenTurnHooks(t *testing.T) {
	app := newGoalTestApp(t)
	app.hookRunner = agent.NewHookRunner([]agent.ResolvedHook{{
		HookConfig: agent.HookConfig{Command: `printf '{"updated_input":"rewritten objective"}'`},
		Event:      agent.HookEventUserPromptSubmit,
	}}, app.workspaceRoot)

	res, err := app.ExecuteLocalCommand("/goal original objective")
	if err != nil {
		t.Fatalf("goal command: %v", err)
	}
	if res.Turn == nil || !res.Turn.SkipUserPromptHooks {
		t.Fatalf("hidden continuation should skip hooks: %#v", res.Turn)
	}
	st, ok, err := session.LoadGoalState(app.sessionsDir, app.sessionID)
	if err != nil || !ok {
		t.Fatalf("load goal state ok=%v err=%v", ok, err)
	}
	if st.Objective != "rewritten objective" {
		t.Fatalf("objective = %q, want hook rewrite", st.Objective)
	}
	if !strings.Contains(res.Turn.Input, "rewritten objective") {
		t.Fatalf("continuation prompt missing rewritten objective:\n%s", res.Turn.Input)
	}
}

func TestGoalCommandStartsContinuationInAgentMode(t *testing.T) {
	app := newGoalTestApp(t)
	app.currentMode = session.ModePlan

	res, err := app.ExecuteLocalCommand("/goal implement the plan")
	if err != nil {
		t.Fatalf("goal command: %v", err)
	}
	if res.Turn == nil {
		t.Fatal("expected goal command to start a turn")
	}
	if app.currentMode != session.ModeAgent {
		t.Fatalf("current mode = %q, want agent", app.currentMode)
	}
	modeState, err := session.LoadModeState(app.sessionsDir, app.sessionID)
	if err != nil {
		t.Fatalf("load mode state: %v", err)
	}
	if modeState.Mode != session.ModeAgent {
		t.Fatalf("persisted mode = %q, want agent", modeState.Mode)
	}
	if !strings.Contains(res.Text, "Agent mode enabled") {
		t.Fatalf("goal text should mention mode switch:\n%s", res.Text)
	}
}

func TestGoalResumeStartsContinuationInAgentMode(t *testing.T) {
	app := newGoalTestApp(t)
	app.currentMode = session.ModeAsk
	if _, err := app.ExecuteLocalCommand("/goal ship the goal command"); err != nil {
		t.Fatalf("set goal: %v", err)
	}
	if _, err := app.ExecuteLocalCommand("/goal pause"); err != nil {
		t.Fatalf("pause goal: %v", err)
	}
	app.currentMode = session.ModeAsk

	resumed, err := app.ExecuteLocalCommand("/goal resume")
	if err != nil {
		t.Fatalf("resume goal: %v", err)
	}
	if resumed.Turn == nil {
		t.Fatal("resume should start a continuation turn")
	}
	if app.currentMode != session.ModeAgent {
		t.Fatalf("current mode = %q, want agent", app.currentMode)
	}
	if !strings.Contains(resumed.Text, "Agent mode enabled") {
		t.Fatalf("resume text should mention mode switch:\n%s", resumed.Text)
	}
}

func TestGoalCommandBlockedByObjectiveHookDoesNotStartGoal(t *testing.T) {
	app := newGoalTestApp(t)
	app.hookRunner = agent.NewHookRunner([]agent.ResolvedHook{{
		HookConfig: agent.HookConfig{Command: `echo blocked objective >&2; exit 2`},
		Event:      agent.HookEventUserPromptSubmit,
	}}, app.workspaceRoot)

	res, err := app.ExecuteLocalCommand("/goal blocked objective")
	if err != nil {
		t.Fatalf("goal command: %v", err)
	}
	if res.Turn != nil || res.Mutated {
		t.Fatalf("blocked goal should not mutate or start turn: %+v", res)
	}
	if !strings.Contains(res.Text, "blocked by UserPromptSubmit hook") || !strings.Contains(res.Text, "Goal was not started.") {
		t.Fatalf("unexpected blocked text:\n%s", res.Text)
	}
	if _, ok, err := session.LoadGoalState(app.sessionsDir, app.sessionID); err != nil || ok {
		t.Fatalf("blocked goal should not save state ok=%v err=%v", ok, err)
	}
}

func TestGoalCommandStatusPauseResumeClear(t *testing.T) {
	app := newGoalTestApp(t)

	if _, err := app.ExecuteLocalCommand("/goal ship the goal command"); err != nil {
		t.Fatalf("set goal: %v", err)
	}

	status, err := app.ExecuteLocalCommand("/goal")
	if err != nil {
		t.Fatalf("goal status: %v", err)
	}
	if !strings.Contains(status.Text, "Current goal:") || !strings.Contains(status.Text, "ship the goal command") {
		t.Fatalf("unexpected status:\n%s", status.Text)
	}

	paused, err := app.ExecuteLocalCommand("/goal pause")
	if err != nil {
		t.Fatalf("pause goal: %v", err)
	}
	if !paused.Mutated || paused.Turn != nil {
		t.Fatalf("pause mutated/turn = %v/%v, want true/nil", paused.Mutated, paused.Turn)
	}
	st, ok, err := session.LoadGoalState(app.sessionsDir, app.sessionID)
	if err != nil || !ok {
		t.Fatalf("load paused goal ok=%v err=%v", ok, err)
	}
	if st.Status != session.GoalStatusPaused {
		t.Fatalf("status = %q, want paused", st.Status)
	}

	resumed, err := app.ExecuteLocalCommand("/goal resume")
	if err != nil {
		t.Fatalf("resume goal: %v", err)
	}
	if !resumed.Mutated || resumed.Turn == nil || !resumed.Turn.Hidden {
		t.Fatalf("resume mutated/turn = %v/%#v, want mutated hidden turn", resumed.Mutated, resumed.Turn)
	}
	if !resumed.Turn.SkipUserPromptHooks {
		t.Fatalf("resume continuation should skip prompt hooks: %#v", resumed.Turn)
	}

	cleared, err := app.ExecuteLocalCommand("/goal clear")
	if err != nil {
		t.Fatalf("clear goal: %v", err)
	}
	if !cleared.Mutated {
		t.Fatal("clear should mutate")
	}
	_, ok, err = session.LoadGoalState(app.sessionsDir, app.sessionID)
	if err != nil {
		t.Fatalf("load after clear: %v", err)
	}
	if ok {
		t.Fatal("expected goal to be cleared")
	}
}

func TestGoalCommandRejectsBadUsage(t *testing.T) {
	app := newGoalTestApp(t)
	for _, line := range []string{
		"/goal --tokens",
		"/goal --tokens= ship",
		"/goal --tokens nope ship",
		"/goal --tokens 0.1 ship",
		"/goal --unknown ship",
		"/goal status extra",
	} {
		if _, err := app.ExecuteLocalCommand(line); err == nil {
			t.Fatalf("%s: expected error", line)
		}
	}
}

func TestGoalCommandPreservesFlagLookingObjectiveWords(t *testing.T) {
	app := newGoalTestApp(t)

	res, err := app.ExecuteLocalCommand("/goal fix --help output")
	if err != nil {
		t.Fatalf("goal command: %v", err)
	}
	if res.Turn == nil {
		t.Fatal("expected hidden turn")
	}
	st, ok, err := session.LoadGoalState(app.sessionsDir, app.sessionID)
	if err != nil || !ok {
		t.Fatalf("load goal state ok=%v err=%v", ok, err)
	}
	if st.Objective != "fix --help output" {
		t.Fatalf("objective = %q, want flag-looking words preserved", st.Objective)
	}
}

func TestGoalCommandHandlesTabSeparatedArguments(t *testing.T) {
	app := newGoalTestApp(t)

	if _, err := app.ExecuteLocalCommand("/goal\tship tab objective"); err != nil {
		t.Fatalf("tab-separated goal command: %v", err)
	}
	st, ok, err := session.LoadGoalState(app.sessionsDir, app.sessionID)
	if err != nil || !ok {
		t.Fatalf("load goal state ok=%v err=%v", ok, err)
	}
	if st.Objective != "ship tab objective" {
		t.Fatalf("objective = %q, want tab-separated objective", st.Objective)
	}

	status, err := app.ExecuteLocalCommand("/goal\tstatus")
	if err != nil {
		t.Fatalf("tab-separated goal status: %v", err)
	}
	if !strings.Contains(status.Text, "ship tab objective") {
		t.Fatalf("unexpected status:\n%s", status.Text)
	}
}

func TestGoalStatusRefreshesTokenUsageAndBudgetLimit(t *testing.T) {
	app := newGoalTestApp(t)

	if _, err := app.ExecuteLocalCommand("/goal --tokens 100 ship the goal command"); err != nil {
		t.Fatalf("set goal: %v", err)
	}
	writeUsageRecord(t, filepath.Join(app.cfg.DataDir, "usage.jsonl"), telemetry.UsageRecord{
		TS:               time.Now().UnixMilli(),
		Session:          app.sessionID,
		Model:            "deepseek-v4-flash",
		PromptTokens:     70,
		CompletionTokens: 50,
	})

	status, err := app.ExecuteLocalCommand("/goal status")
	if err != nil {
		t.Fatalf("goal status: %v", err)
	}
	if !strings.Contains(status.Text, "Status: budget_limited") {
		t.Fatalf("expected budget_limited status:\n%s", status.Text)
	}
	if !strings.Contains(status.Text, "Budget: 120 / 100 tokens") {
		t.Fatalf("expected refreshed budget usage:\n%s", status.Text)
	}

	st, ok, err := session.LoadGoalState(app.sessionsDir, app.sessionID)
	if err != nil || !ok {
		t.Fatalf("load goal state ok=%v err=%v", ok, err)
	}
	if st.TokensUsed != 0 || st.Status != session.GoalStatusActive {
		t.Fatalf("status inspection should not persist usage refresh: tokens/status = %d/%q, want 0/active", st.TokensUsed, st.Status)
	}
	resumed, err := app.ExecuteLocalCommand("/goal resume")
	if err != nil {
		t.Fatalf("resume goal: %v", err)
	}
	if resumed.Turn != nil {
		t.Fatalf("resume over budget should not start a turn: %#v", resumed.Turn)
	}
	st, ok, err = session.LoadGoalState(app.sessionsDir, app.sessionID)
	if err != nil || !ok {
		t.Fatalf("load resumed budget-limited goal ok=%v err=%v", ok, err)
	}
	if st.TokensUsed != 120 || st.Status != session.GoalStatusBudgetLimited {
		t.Fatalf("resume should persist budget refresh: tokens/status = %d/%q, want 120/budget_limited", st.TokensUsed, st.Status)
	}
	paused, err := app.ExecuteLocalCommand("/goal pause")
	if err != nil {
		t.Fatalf("pause budget-limited goal: %v", err)
	}
	if paused.Turn != nil {
		t.Fatalf("pause budget-limited goal should not start a turn: %#v", paused.Turn)
	}
	st, ok, err = session.LoadGoalState(app.sessionsDir, app.sessionID)
	if err != nil || !ok {
		t.Fatalf("load budget-limited goal ok=%v err=%v", ok, err)
	}
	if st.Status != session.GoalStatusBudgetLimited {
		t.Fatalf("pause should preserve budget_limited status, got %q", st.Status)
	}
}

func TestGoalBudgetExcludesPausedSessionUsage(t *testing.T) {
	app := newGoalTestApp(t)

	if _, err := app.ExecuteLocalCommand("/goal --tokens 100 ship the goal command"); err != nil {
		t.Fatalf("set goal: %v", err)
	}
	writeUsageRecord(t, filepath.Join(app.cfg.DataDir, "usage.jsonl"), telemetry.UsageRecord{
		TS:               time.Now().UnixMilli(),
		Session:          app.sessionID,
		Model:            "deepseek-v4-flash",
		PromptTokens:     20,
		CompletionTokens: 10,
	})
	if _, err := app.ExecuteLocalCommand("/goal pause"); err != nil {
		t.Fatalf("pause goal: %v", err)
	}

	writeUsageRecord(t, filepath.Join(app.cfg.DataDir, "usage.jsonl"), telemetry.UsageRecord{
		TS:               time.Now().UnixMilli(),
		Session:          app.sessionID,
		Model:            "deepseek-v4-flash",
		PromptTokens:     80,
		CompletionTokens: 40,
	})
	status, err := app.ExecuteLocalCommand("/goal status")
	if err != nil {
		t.Fatalf("goal status while paused: %v", err)
	}
	if !strings.Contains(status.Text, "Status: paused") {
		t.Fatalf("expected paused status:\n%s", status.Text)
	}
	if !strings.Contains(status.Text, "Budget: 30 / 100 tokens") {
		t.Fatalf("paused work should not count toward budget:\n%s", status.Text)
	}

	resumed, err := app.ExecuteLocalCommand("/goal resume")
	if err != nil {
		t.Fatalf("resume goal: %v", err)
	}
	if resumed.Turn == nil {
		t.Fatal("resume should start a continuation turn when active budget remains")
	}
	st, ok, err := session.LoadGoalState(app.sessionsDir, app.sessionID)
	if err != nil || !ok {
		t.Fatalf("load resumed goal ok=%v err=%v", ok, err)
	}
	if st.TokensUsed != 30 || st.Status != session.GoalStatusActive {
		t.Fatalf("tokens/status = %d/%q, want 30/active", st.TokensUsed, st.Status)
	}
}

func TestGoalResumeContinuesIdleActiveGoal(t *testing.T) {
	app := newGoalTestApp(t)

	if _, err := app.ExecuteLocalCommand("/goal ship the goal command"); err != nil {
		t.Fatalf("set goal: %v", err)
	}
	resumed, err := app.ExecuteLocalCommand("/goal resume")
	if err != nil {
		t.Fatalf("resume active goal: %v", err)
	}
	if resumed.Turn == nil {
		t.Fatal("resume active idle goal should start a continuation turn")
	}
	if !resumed.Turn.GoalContinuation || !resumed.Turn.Hidden {
		t.Fatalf("resume active turn = %#v, want hidden goal continuation", resumed.Turn)
	}
	if !strings.Contains(resumed.Text, "Goal resumed") {
		t.Fatalf("unexpected resume text:\n%s", resumed.Text)
	}
	st, ok, err := session.LoadGoalState(app.sessionsDir, app.sessionID)
	if err != nil || !ok {
		t.Fatalf("load active goal ok=%v err=%v", ok, err)
	}
	if st.Status != session.GoalStatusActive {
		t.Fatalf("status = %q, want active", st.Status)
	}
}

func TestGoalPauseResumeDoNotReopenCompleteGoal(t *testing.T) {
	app := newGoalTestApp(t)
	if err := session.SaveGoalState(app.sessionsDir, app.sessionID, session.GoalState{
		ID:        "goal-complete",
		Objective: "ship the goal command",
		Status:    session.GoalStatusCompleted,
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("save completed goal: %v", err)
	}

	resumed, err := app.ExecuteLocalCommand("/goal resume")
	if err != nil {
		t.Fatalf("resume completed goal: %v", err)
	}
	if resumed.Turn != nil {
		t.Fatalf("resume completed goal should not start a turn: %#v", resumed.Turn)
	}
	paused, err := app.ExecuteLocalCommand("/goal pause")
	if err != nil {
		t.Fatalf("pause completed goal: %v", err)
	}
	if paused.Turn != nil {
		t.Fatalf("pause completed goal should not start a turn: %#v", paused.Turn)
	}
	st, ok, err := session.LoadGoalState(app.sessionsDir, app.sessionID)
	if err != nil || !ok {
		t.Fatalf("load completed goal ok=%v err=%v", ok, err)
	}
	if st.Status != session.GoalStatusCompleted {
		t.Fatalf("status = %q, want completed", st.Status)
	}
}

func TestGoalStatusDoesNotAccrueTokensAfterComplete(t *testing.T) {
	app := newGoalTestApp(t)
	now := time.Now()
	if err := session.SaveGoalState(app.sessionsDir, app.sessionID, session.GoalState{
		ID:            "goal-complete",
		Objective:     "ship once",
		Status:        session.GoalStatusCompleted,
		TokenBaseline: 100,
		TokensUsed:    100,
		CreatedAt:     now,
		CompletedAt:   now,
	}); err != nil {
		t.Fatalf("save completed goal: %v", err)
	}
	writeUsageRecord(t, filepath.Join(app.cfg.DataDir, "usage.jsonl"), telemetry.UsageRecord{
		TS:               now.Add(time.Minute).UnixMilli(),
		Session:          app.sessionID,
		Model:            "deepseek-v4-flash",
		PromptTokens:     150,
		CompletionTokens: 50,
	})

	status, err := app.ExecuteLocalCommand("/goal status")
	if err != nil {
		t.Fatalf("goal status: %v", err)
	}
	if !strings.Contains(status.Text, "Status: completed") {
		t.Fatalf("expected complete status:\n%s", status.Text)
	}
	st, ok, err := session.LoadGoalState(app.sessionsDir, app.sessionID)
	if err != nil || !ok {
		t.Fatalf("load goal state ok=%v err=%v", ok, err)
	}
	if st.TokensUsed != 100 || st.TokenBaseline != 100 {
		t.Fatalf("completed goal usage drifted: tokens=%d baseline=%d, want 100/100", st.TokensUsed, st.TokenBaseline)
	}
}
