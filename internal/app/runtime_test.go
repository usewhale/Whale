package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/store"
	"github.com/usewhale/whale/internal/telemetry"
)

func TestHandleLocalCommandCompactUsageError(t *testing.T) {
	a := &App{sessionID: "s1", workspaceRoot: t.TempDir()}
	handled, out, _, err := a.HandleLocalCommand("/compact extra")
	if !handled {
		t.Fatal("expected /compact extra to be handled")
	}
	if out != "" {
		t.Fatalf("expected empty output on usage error, got %q", out)
	}
	if err == nil || !strings.Contains(err.Error(), "usage: /compact") {
		t.Fatalf("expected /compact usage error, got %v", err)
	}
}

func TestRunUserPromptSubmitHookBlockedOutput(t *testing.T) {
	a := &App{
		ctx:           context.Background(),
		sessionID:     "s1",
		workspaceRoot: ".",
		hookRunner: agent.NewHookRunner([]agent.ResolvedHook{{
			HookConfig: agent.HookConfig{Command: "echo blocked prompt >&2; exit 2"},
			Event:      agent.HookEventUserPromptSubmit,
		}}, "."),
	}

	blocked, out, updated := a.RunUserPromptSubmitHook("deploy")
	if !blocked {
		t.Fatal("expected prompt hook to block")
	}
	if updated != "deploy" {
		t.Fatalf("blocked hook should preserve input, got %q", updated)
	}
	if !strings.Contains(out, "decision:block") || !strings.Contains(out, "assistant> blocked by UserPromptSubmit hook") {
		t.Fatalf("unexpected blocked hook output: %q", out)
	}
}

func TestRunUserPromptSubmitHookWarnOutput(t *testing.T) {
	a := &App{
		ctx:           context.Background(),
		sessionID:     "s1",
		workspaceRoot: ".",
		hookRunner: agent.NewHookRunner([]agent.ResolvedHook{{
			HookConfig: agent.HookConfig{Command: "echo warn prompt >&2; exit 1"},
			Event:      agent.HookEventUserPromptSubmit,
		}}, "."),
	}

	blocked, out, _ := a.RunUserPromptSubmitHook("deploy")
	if blocked {
		t.Fatal("expected prompt hook warning not to block")
	}
	if !strings.Contains(out, "decision:warn") || !strings.Contains(out, "warn prompt") {
		t.Fatalf("unexpected warn hook output: %q", out)
	}
}

func TestRunUserPromptSubmitHookReturnsUpdatedInput(t *testing.T) {
	a := &App{
		ctx:           context.Background(),
		sessionID:     "s1",
		workspaceRoot: ".",
		hookRunner: agent.NewHookRunner([]agent.ResolvedHook{{
			HookConfig: agent.HookConfig{Command: `printf '{"updated_input":"rewritten prompt"}'`},
			Event:      agent.HookEventUserPromptSubmit,
		}}, "."),
	}

	blocked, _, updated := a.RunUserPromptSubmitHook("original prompt")
	if blocked {
		t.Fatal("expected prompt rewrite hook not to block")
	}
	if updated != "rewritten prompt" {
		t.Fatalf("updated input = %q", updated)
	}
}

func TestRunStopHookIncludesAssistantTextAndTurn(t *testing.T) {
	dir := t.TempDir()
	payloadPath := filepath.Join(dir, "payload.jsonl")
	cmd := "cat > " + payloadPath
	a := &App{
		ctx:           context.Background(),
		sessionID:     "s-stop",
		workspaceRoot: dir,
		hookRunner:    agent.NewHookRunner([]agent.ResolvedHook{{HookConfig: agent.HookConfig{Command: cmd}, Event: agent.HookEventStop}}, dir),
	}

	out := a.RunStopHook("final answer", 7)
	if out != "" {
		t.Fatalf("expected successful stop hook to produce no rendered output, got %q", out)
	}
	b, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Fatalf("read stop payload: %v", err)
	}
	if !strings.Contains(string(b), `"last_assistant_text":"final answer"`) || !strings.Contains(string(b), `"turn":7`) {
		t.Fatalf("unexpected stop payload: %s", string(b))
	}
}

func TestOfficialPluginNoopStopHooksDoNotRenderOutput(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	out := app.RunStopHook("final answer", 1)
	if out != "" {
		t.Fatalf("expected plugin pass hooks to render no output, got %q", out)
	}
}

func TestNewSessionStoresWorktreeMeta(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	workspace := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	app, err := New(t.Context(), cfg, StartOptions{
		NewSession: true,
		Worktree: WorktreeSession{
			Name:               "feature",
			Path:               workspace,
			Branch:             "worktree-feature",
			OriginalWorkspace:  "/tmp/original",
			OriginalBranch:     "main",
			OriginalHeadCommit: "abc123",
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	meta, err := session.LoadSessionMeta(store.DefaultSessionsDir(cfg.DataDir), app.SessionID())
	if err != nil {
		t.Fatalf("LoadSessionMeta: %v", err)
	}
	if meta.WorktreeName != "feature" || meta.WorktreePath != workspace || meta.WorktreeBranch != "worktree-feature" || meta.OriginalWorkspace != "/tmp/original" || meta.OriginalBranch != "main" || meta.OriginalHeadCommit != "abc123" {
		t.Fatalf("unexpected worktree meta: %+v", meta)
	}
}

func TestRunOptionsDoNotDefaultToCurrentViewMode(t *testing.T) {
	a := &App{cfg: Config{ViewMode: ViewModeFocus}}

	got := a.applyRunOptionsDefaults(agent.RunOptions{})
	if got.ViewMode != "" {
		t.Fatalf("view mode should stay unset for headless app turns, got %q", got.ViewMode)
	}
}

func TestRunOptionsKeepExplicitViewMode(t *testing.T) {
	a := &App{cfg: Config{ViewMode: ViewModeFocus}}

	got := a.applyRunOptionsDefaults(agent.RunOptions{ViewMode: ViewModeDefault})
	if got.ViewMode != ViewModeDefault {
		t.Fatalf("view mode: want explicit %q, got %q", ViewModeDefault, got.ViewMode)
	}
}

func TestFinalizeTurnDoesNotCompletePendingGoalWithoutUpdateTool(t *testing.T) {
	dir := t.TempDir()
	a := &App{
		sessionID:       "goal-session",
		sessionsDir:     filepath.Join(dir, "sessions"),
		cfg:             Config{DataDir: dir},
		workspaceRoot:   dir,
		pendingGoalTurn: true,
	}
	if err := session.SaveGoalState(a.sessionsDir, a.sessionID, session.GoalState{
		ID:        "goal-active",
		Objective: "ship it",
		Status:    session.GoalStatusActive,
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("save goal: %v", err)
	}

	if err := a.FinalizeTurn("done", true); err != nil {
		t.Fatalf("FinalizeTurn: %v", err)
	}
	st, ok, err := session.LoadGoalState(a.sessionsDir, a.sessionID)
	if err != nil || !ok {
		t.Fatalf("load goal state ok=%v err=%v", ok, err)
	}
	if st.Status != session.GoalStatusActive {
		t.Fatalf("goal status = %q, want active", st.Status)
	}
	if a.pendingGoalTurn {
		t.Fatal("pendingGoalTurn should be cleared")
	}
}

func TestFinalizeTurnRefreshesCompletedGoalUsage(t *testing.T) {
	dir := t.TempDir()
	a := &App{
		sessionID:       "goal-session",
		sessionsDir:     filepath.Join(dir, "sessions"),
		cfg:             Config{DataDir: dir},
		workspaceRoot:   dir,
		pendingGoalTurn: true,
	}
	if err := session.SaveGoalState(a.sessionsDir, a.sessionID, session.GoalState{
		ID:            "goal-complete",
		Objective:     "ship it",
		Status:        session.GoalStatusCompleted,
		TokenBaseline: 10,
		TokensUsed:    20,
		CreatedAt:     time.Now(),
		CompletedAt:   time.Now(),
	}); err != nil {
		t.Fatalf("save goal: %v", err)
	}
	writeUsageRecord(t, filepath.Join(dir, "usage.jsonl"), telemetry.UsageRecord{
		Session:          "goal-session",
		Model:            "deepseek-v4-flash",
		PromptTokens:     50,
		CompletionTokens: 15,
	})

	if err := a.FinalizeTurn("done", true); err != nil {
		t.Fatalf("FinalizeTurn: %v", err)
	}
	st, ok, err := session.LoadGoalState(a.sessionsDir, a.sessionID)
	if err != nil || !ok {
		t.Fatalf("load goal state ok=%v err=%v", ok, err)
	}
	if st.Status != session.GoalStatusCompleted {
		t.Fatalf("goal status = %q, want completed", st.Status)
	}
	if st.TokensUsed != 75 {
		t.Fatalf("tokens used = %d, want 75", st.TokensUsed)
	}
	if a.pendingGoalTurn {
		t.Fatal("pendingGoalTurn should be cleared")
	}
}

func TestFinalizeTurnDoesNotCompleteOrdinaryActiveGoal(t *testing.T) {
	dir := t.TempDir()
	a := &App{
		sessionID:     "goal-session",
		sessionsDir:   filepath.Join(dir, "sessions"),
		cfg:           Config{DataDir: dir},
		workspaceRoot: dir,
	}
	if err := session.SaveGoalState(a.sessionsDir, a.sessionID, session.GoalState{
		ID:        "goal-active",
		Objective: "ship it",
		Status:    session.GoalStatusActive,
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("save goal: %v", err)
	}

	if err := a.FinalizeTurn("ordinary response", true); err != nil {
		t.Fatalf("FinalizeTurn: %v", err)
	}
	st, ok, err := session.LoadGoalState(a.sessionsDir, a.sessionID)
	if err != nil || !ok {
		t.Fatalf("load goal state ok=%v err=%v", ok, err)
	}
	if st.Status != session.GoalStatusActive {
		t.Fatalf("goal status = %q, want active", st.Status)
	}
}

func TestFinalizeTurnDoesNotCompleteInterruptedGoalTurn(t *testing.T) {
	dir := t.TempDir()
	a := &App{
		sessionID:       "goal-session",
		sessionsDir:     filepath.Join(dir, "sessions"),
		cfg:             Config{DataDir: dir},
		workspaceRoot:   dir,
		pendingGoalTurn: true,
	}
	if err := session.SaveGoalState(a.sessionsDir, a.sessionID, session.GoalState{
		ID:        "goal-active",
		Objective: "ship it",
		Status:    session.GoalStatusActive,
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("save goal: %v", err)
	}

	if err := a.FinalizeTurn("partial progress", false); err != nil {
		t.Fatalf("FinalizeTurn: %v", err)
	}
	st, ok, err := session.LoadGoalState(a.sessionsDir, a.sessionID)
	if err != nil || !ok {
		t.Fatalf("load goal state ok=%v err=%v", ok, err)
	}
	if st.Status != session.GoalStatusActive {
		t.Fatalf("goal status = %q, want active", st.Status)
	}
	if a.pendingGoalTurn {
		t.Fatal("pendingGoalTurn should be cleared")
	}
}

func TestFinalizeTurnDoesNotCompletePendingWorkflowGoalTurn(t *testing.T) {
	dir := t.TempDir()
	a := &App{
		sessionID:       "goal-session",
		sessionsDir:     filepath.Join(dir, "sessions"),
		cfg:             Config{DataDir: dir},
		workspaceRoot:   dir,
		pendingGoalTurn: true,
	}
	if err := session.SaveGoalState(a.sessionsDir, a.sessionID, session.GoalState{
		ID:        "goal-active",
		Objective: "research it",
		Status:    session.GoalStatusActive,
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("save goal: %v", err)
	}

	text := "The workflow is running asynchronously. When it completes, I'll share the results."
	if err := a.FinalizeTurn(text, true); err != nil {
		t.Fatalf("FinalizeTurn: %v", err)
	}
	st, ok, err := session.LoadGoalState(a.sessionsDir, a.sessionID)
	if err != nil || !ok {
		t.Fatalf("load goal state ok=%v err=%v", ok, err)
	}
	if st.Status != session.GoalStatusActive {
		t.Fatalf("goal status = %q, want active", st.Status)
	}
}
