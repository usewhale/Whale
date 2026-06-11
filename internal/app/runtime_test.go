package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/core"
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

func TestAppShellForegroundWaitConfigUpdatesToolDescription(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	dataDir := t.TempDir()
	workspace := t.TempDir()
	if err := SaveConfigFile(GlobalConfigPath(dataDir), FileConfig{
		Shell: FileShellConfig{
			ForegroundWaitDefaultMS: intPtr(45000),
			ForegroundWaitMaxMS:     intPtr(240000),
		},
	}); err != nil {
		t.Fatalf("SaveConfigFile: %v", err)
	}
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir workspace: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	cfg := DefaultConfig()
	cfg.DataDir = dataDir
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()
	spec, ok := app.toolRegistry.Spec("shell_run")
	if !ok {
		t.Fatal("missing shell_run spec")
	}
	if !strings.Contains(spec.Description, "yield_time_ms defaults to 45000ms and clamps at 240000ms") {
		t.Fatalf("shell_run description did not use configured waits:\n%s", spec.Description)
	}
	yield, ok := spec.Parameters["properties"].(map[string]any)["yield_time_ms"].(map[string]any)
	if !ok {
		t.Fatalf("missing yield_time_ms schema: %#v", spec.Parameters)
	}
	if yield["maximum"].(int) != 240000 {
		t.Fatalf("yield_time_ms schema maximum = %#v, want 240000", yield["maximum"])
	}
	timeout, ok := spec.Parameters["properties"].(map[string]any)["timeout_ms"].(map[string]any)
	if !ok {
		t.Fatalf("missing timeout schema: %#v", spec.Parameters)
	}
	if timeout["maximum"].(int) != 1800000 {
		t.Fatalf("timeout schema maximum = %#v, want 1800000", timeout["maximum"])
	}
	if !strings.Contains(timeout["description"].(string), "defaults to 45000 and clamps at 240000") {
		t.Fatalf("timeout description did not use configured waits: %s", timeout["description"])
	}
	subagentSpec, ok := app.subagentToolRegistry.Spec("shell_run")
	if !ok {
		t.Fatal("missing subagent shell_run spec")
	}
	if !strings.Contains(subagentSpec.Description, "yield_time_ms defaults to 45000ms and clamps at 240000ms") {
		t.Fatalf("subagent shell_run description did not use configured waits:\n%s", subagentSpec.Description)
	}
}

func TestRuntimeRespectsWorkflowToggles(t *testing.T) {
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
	disabled := false
	if err := SaveConfigFile(ProjectConfigPath(workspace), FileConfig{Workflows: FileWorkflowsConfig{Enabled: &disabled}}); err != nil {
		t.Fatalf("SaveConfigFile disabled workflows: %v", err)
	}
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New disabled workflows: %v", err)
	}
	defer app.Close()
	workflowTool := app.toolRegistry.Get("workflow")
	if workflowTool == nil {
		t.Fatal("workflow status tool should remain registered when workflows are disabled")
	}
	res, err := workflowTool.Run(t.Context(), core.ToolCall{ID: "wf-disabled", Name: "workflow", Input: `{"action":"list"}`})
	if err != nil {
		t.Fatalf("disabled workflow tool Run: %v", err)
	}
	if !res.IsError() || !strings.Contains(res.ModelText, "workflow_disabled") {
		t.Fatalf("disabled workflow tool should return workflow_disabled, got error=%v content=%s", res.IsError(), res.ModelText)
	}

	cfg = DefaultConfig()
	cfg.DataDir = t.TempDir()
	if err := os.Remove(ProjectConfigPath(workspace)); err != nil {
		t.Fatalf("remove project config: %v", err)
	}
	enabled := true
	if err := SaveConfigFile(ProjectConfigPath(workspace), FileConfig{Workflows: FileWorkflowsConfig{Enabled: &enabled, KeywordTriggerEnabled: &disabled}}); err != nil {
		t.Fatalf("SaveConfigFile disabled keyword trigger: %v", err)
	}
	app, err = New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New disabled keyword trigger: %v", err)
	}
	defer app.Close()
	if app.toolRegistry.Get("workflow") == nil {
		t.Fatal("workflow tool should remain registered when only keyword trigger is disabled")
	}
	spec, ok := app.toolRegistry.Spec("workflow")
	if !ok {
		t.Fatal("workflow tool spec not found")
	}
	desc := spec.Description
	if strings.Contains(desc, "system prompt catalog") {
		t.Fatalf("workflow tool description should not advertise catalog when keyword trigger is disabled: %s", desc)
	}
}

func TestTurnReloadsWorkflowConfigChanges(t *testing.T) {
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
	disabled := false
	if err := SaveConfigFile(ProjectConfigPath(workspace), FileConfig{Workflows: FileWorkflowsConfig{Enabled: &disabled}}); err != nil {
		t.Fatalf("SaveConfigFile disabled workflows: %v", err)
	}
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	workflowTool := app.toolRegistry.Get("workflow")
	if workflowTool == nil {
		t.Fatal("workflow tool should be registered")
	}
	res, err := workflowTool.Run(t.Context(), core.ToolCall{ID: "wf-disabled", Name: "workflow", Input: `{"action":"run","name":"dead-code-scan"}`})
	if err != nil {
		t.Fatalf("disabled workflow Run: %v", err)
	}
	if !res.IsError() || !strings.Contains(res.ModelText, "workflow_disabled") {
		t.Fatalf("expected disabled workflow result, got error=%v content=%s", res.IsError(), res.ModelText)
	}

	enabled := true
	if err := SaveConfigFile(ProjectConfigPath(workspace), FileConfig{Workflows: FileWorkflowsConfig{Enabled: &enabled}}); err != nil {
		t.Fatalf("SaveConfigFile enabled workflows: %v", err)
	}
	if err := app.reloadWorkflowConfigForTurn(); err != nil {
		t.Fatalf("reloadWorkflowConfigForTurn: %v", err)
	}
	if !app.cfg.WorkflowsEnabled {
		t.Fatal("expected workflows enabled after turn reload")
	}
	workflowTool = app.toolRegistry.Get("workflow")
	if workflowTool == nil {
		t.Fatal("workflow tool should remain registered")
	}
	res, err = workflowTool.Run(t.Context(), core.ToolCall{ID: "wf-enabled", Name: "workflow", Input: `{"action":"list"}`})
	if err != nil {
		t.Fatalf("enabled workflow Run: %v", err)
	}
	if res.IsError() || strings.Contains(res.ModelText, "workflow_disabled") {
		t.Fatalf("expected enabled workflow list result, got error=%v content=%s", res.IsError(), res.ModelText)
	}
}

func TestTurnReloadPreservesExplicitWorkflowOverrides(t *testing.T) {
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
	cfg.WorkflowsEnabled = true
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New explicit true: %v", err)
	}
	defer app.Close()
	if !app.cfg.WorkflowsEnabled || !app.cfg.WorkflowsEnabledExplicit {
		t.Fatalf("expected programmatic workflow enable to be explicit after New: enabled=%v explicit=%v", app.cfg.WorkflowsEnabled, app.cfg.WorkflowsEnabledExplicit)
	}
	if err := app.reloadWorkflowConfigForTurn(); err != nil {
		t.Fatalf("reloadWorkflowConfigForTurn explicit true: %v", err)
	}
	if !app.cfg.WorkflowsEnabled {
		t.Fatal("turn reload should preserve explicit programmatic workflows.enabled=true")
	}

	enabled := true
	if err := SaveConfigFile(ProjectConfigPath(workspace), FileConfig{Workflows: FileWorkflowsConfig{Enabled: &enabled}}); err != nil {
		t.Fatalf("SaveConfigFile enabled workflows: %v", err)
	}
	cfg = DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.WorkflowsEnabled = false
	cfg.WorkflowsEnabledExplicit = true
	app, err = New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New explicit false: %v", err)
	}
	defer app.Close()
	if app.cfg.WorkflowsEnabled {
		t.Fatal("explicit programmatic workflows.enabled=false should override file during New")
	}
	if err := app.reloadWorkflowConfigForTurn(); err != nil {
		t.Fatalf("reloadWorkflowConfigForTurn explicit false: %v", err)
	}
	if app.cfg.WorkflowsEnabled {
		t.Fatal("turn reload should preserve explicit programmatic workflows.enabled=false")
	}
}

func TestTurnReloadDoesNotFreezeLoadedWorkflowConfig(t *testing.T) {
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

	disabled := false
	if err := SaveConfigFile(ProjectConfigPath(workspace), FileConfig{Workflows: FileWorkflowsConfig{Enabled: &disabled}}); err != nil {
		t.Fatalf("SaveConfigFile disabled workflows: %v", err)
	}
	loaded, err := LoadAndApplyConfig(Config{DataDir: t.TempDir()}, workspace)
	if err != nil {
		t.Fatalf("LoadAndApplyConfig: %v", err)
	}
	if !loaded.ConfigLoaded || !loaded.WorkflowsEnabledExplicit || loaded.WorkflowsEnabled {
		t.Fatalf("expected loaded file config to be explicit disabled: loaded=%+v", loaded)
	}
	app, err := New(t.Context(), loaded, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	enabled := true
	if err := SaveConfigFile(ProjectConfigPath(workspace), FileConfig{Workflows: FileWorkflowsConfig{Enabled: &enabled}}); err != nil {
		t.Fatalf("SaveConfigFile enabled workflows: %v", err)
	}
	if err := app.reloadWorkflowConfigForTurn(); err != nil {
		t.Fatalf("reloadWorkflowConfigForTurn: %v", err)
	}
	if !app.cfg.WorkflowsEnabled {
		t.Fatal("turn reload should not reapply stale file-loaded workflows.enabled=false")
	}
}

func TestWorkflowAuthoringPromptIsTurnScoped(t *testing.T) {
	a := &App{cfg: DefaultConfig()}
	disabledBlock := a.workflowDynamicSystemBlock(agent.RunOptions{})
	if !strings.Contains(disabledBlock, "Dynamic workflows are disabled in Whale.") {
		t.Fatalf("disabled workflow runtime block missing status: %s", disabledBlock)
	}
	if !strings.Contains(disabledBlock, "call the workflow tool for the current status") {
		t.Fatalf("disabled workflow runtime should require fresh tool status: %s", disabledBlock)
	}

	a.cfg.WorkflowsEnabled = true

	runBlock := a.workflowDynamicSystemBlock(agent.RunOptions{})
	if !strings.Contains(runBlock, "Workflow runtime.") {
		t.Fatalf("enabled workflow runtime block missing: %s", runBlock)
	}
	if strings.Contains(runBlock, "Workflow authoring.") {
		t.Fatalf("run/list workflow runtime should not include authoring guidance: %s", runBlock)
	}

	createBlock := a.workflowDynamicSystemBlock(agent.RunOptions{WorkflowAuthoring: true})
	if !strings.Contains(createBlock, "Workflow authoring.") {
		t.Fatalf("create workflow turn should include authoring guidance: %s", createBlock)
	}
}

func TestWorkflowAuthoringIntentDetection(t *testing.T) {
	for _, input := range []string{
		"create a repo-review workflow",
		"generate workflow for dead code",
		"新增一个 dead-code workflow",
		"创建一个工作流",
		"编写 workflow",
	} {
		if !workflowAuthoringRequested(input) {
			t.Fatalf("expected authoring intent for %q", input)
		}
	}
	for _, input := range []string{
		"run dead-code-scan workflow",
		"有哪些 workflow",
		"list workflows",
		"workflow 状态",
	} {
		if workflowAuthoringRequested(input) {
			t.Fatalf("did not expect authoring intent for %q", input)
		}
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

func TestInjectTurnInputHiddenDoesNotPatchSessionTitle(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	msgStore, err := store.NewJSONLStore(sessionsDir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	a := &App{
		ctx:         context.Background(),
		sessionID:   "hidden-inject",
		sessionsDir: sessionsDir,
		cfg:         Config{DataDir: dir},
		a:           agent.NewAgent(nil, msgStore, nil),
	}

	injected, err := a.InjectTurnInputWithHidden(context.Background(), "secret queued prompt", "", agent.RunOptions{HiddenInput: true})
	if err != nil {
		t.Fatalf("InjectTurnInputWithHidden: %v", err)
	}
	if injected {
		t.Fatal("expected no active turn to inject into")
	}
	meta, err := session.LoadSessionMeta(sessionsDir, a.sessionID)
	if err != nil {
		t.Fatalf("LoadSessionMeta: %v", err)
	}
	if meta.Title != "" {
		t.Fatalf("hidden injected input should not patch title, got %q", meta.Title)
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
