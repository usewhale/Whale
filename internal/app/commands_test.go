package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appcommands "github.com/usewhale/whale/internal/app/commands"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/store"
	"github.com/usewhale/whale/internal/telemetry"
)

func TestResolveInitialSessionID(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "recent.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := resolveInitialSessionID(sessionsDir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "recent" {
		t.Fatalf("want recent, got %s", got)
	}
}

func TestHandleCommandResumeAndNew(t *testing.T) {
	now := time.Date(2026, 5, 2, 10, 20, 30, 0, time.UTC)

	_, err := handleCommand("/resume abc", "cur", now)
	if err == nil {
		t.Fatal("expected /resume usage error")
	}
	_, err = handleCommand("/resume\tabc", "cur", now)
	if err == nil {
		t.Fatal("expected tab-separated /resume usage error")
	}

	res, err := handleCommand("/new", "cur", now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.SessionID != "20260502-102030" {
		t.Fatalf("unexpected generated id: %s", res.SessionID)
	}

	res, err = handleCommand("/new s2", "cur", now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.SessionID != "s2" {
		t.Fatalf("unexpected new id: %s", res.SessionID)
	}

	res, err = handleCommand("/new\ts3", "cur", now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.SessionID != "s3" {
		t.Fatalf("unexpected tab-separated new id: %s", res.SessionID)
	}

	_, err = handleCommand("/new a b", "cur", now)
	if err == nil {
		t.Fatal("expected /new usage error")
	}
	_, err = handleCommand("/new\ta\tb", "cur", now)
	if err == nil {
		t.Fatal("expected tab-separated /new usage error")
	}
}

func TestClassifySubmitSlashCommands(t *testing.T) {
	tests := []struct {
		line string
		want appcommands.SubmitClass
	}{
		{line: "hello", want: appcommands.SubmitText},
		{line: "/Users/goranka/Engineer/ai/dsk", want: appcommands.SubmitText},
		{line: "/status", want: appcommands.SubmitLocalReadOnly},
		{line: "/stats", want: appcommands.SubmitLocalReadOnly},
		{line: "/stats usage", want: appcommands.SubmitLocalReadOnly},
		{line: "/stats tools", want: appcommands.SubmitLocalReadOnly},
		{line: "/stats repair", want: appcommands.SubmitLocalReadOnly},
		{line: "/stats recent", want: appcommands.SubmitLocalReadOnly},
		{line: "/stats all", want: appcommands.SubmitLocalReadOnly},
		{line: "/mcp", want: appcommands.SubmitLocalReadOnly},
		{line: "/model", want: appcommands.SubmitLocalUI},
		{line: "/permissions", want: appcommands.SubmitLocalUI},
		{line: "/focus", want: appcommands.SubmitLocalUI},
		{line: "/skills", want: appcommands.SubmitLocalUI},
		{line: "/memory", want: appcommands.SubmitLocalReadOnly},
		{line: "/memory list", want: appcommands.SubmitLocalReadOnly},
		{line: "/memory path", want: appcommands.SubmitLocalReadOnly},
		{line: "/memory show global/style", want: appcommands.SubmitLocalReadOnly},
		{line: "/memory forget global/style", want: appcommands.SubmitLocalMutating},
		{line: "/skills-improver status", want: appcommands.SubmitLocalReadOnly},
		{line: "/skills-improver evidence", want: appcommands.SubmitLocalReadOnly},
		{line: "/skills-improver evidence demo-skill", want: appcommands.SubmitLocalReadOnly},
		{line: "/skills-improver proposals", want: appcommands.SubmitLocalReadOnly},
		{line: "/skills-improver propose demo-skill", want: appcommands.SubmitTurnStarting},
		{line: "/skills-improver apply sp-123", want: appcommands.SubmitLocalMutating},
		{line: "/resume", want: appcommands.SubmitLocalUI},
		{line: "/agent", want: appcommands.SubmitLocalMode},
		{line: "/ask", want: appcommands.SubmitLocalMode},
		{line: "/plan", want: appcommands.SubmitLocalMode},
		{line: "/new", want: appcommands.SubmitLocalMutating},
		{line: "/new scratch", want: appcommands.SubmitLocalMutating},
		{line: "/new\tscratch", want: appcommands.SubmitLocalMutating},
		{line: "/clear", want: appcommands.SubmitLocalMutating},
		{line: "/exit", want: appcommands.SubmitExit},
		{line: "/ask inspect", want: appcommands.SubmitTurnStarting},
		{line: "/plan propose a fix", want: appcommands.SubmitTurnStarting},
		{line: "/init", want: appcommands.SubmitTurnStarting},
		{line: "/compact", want: appcommands.SubmitTurnStarting},
		{line: "/model xxx", want: appcommands.SubmitUsageError},
		{line: "/focus now", want: appcommands.SubmitUsageError},
		{line: "/skills xxx", want: appcommands.SubmitUsageError},
		{line: "/memory bad", want: appcommands.SubmitUsageError},
		{line: "/memory show", want: appcommands.SubmitUsageError},
		{line: "/skills-improver propose", want: appcommands.SubmitUsageError},
		{line: "/skills-improver apply", want: appcommands.SubmitUsageError},
		{line: "/resume xxx", want: appcommands.SubmitUsageError},
		{line: "/new a b", want: appcommands.SubmitUsageError},
		{line: "/stats bad", want: appcommands.SubmitUsageError},
		{line: "/compact bad", want: appcommands.SubmitUsageError},
		{line: "/plan show", want: appcommands.SubmitUsageError},
		{line: "/unknown", want: appcommands.SubmitUsageError},
	}
	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			got := appcommands.ClassifySubmit(tc.line, CommandsHelp, "/mcp")
			if got.Class != tc.want {
				t.Fatalf("ClassifySubmit(%q) = %v, want %v", tc.line, got.Class, tc.want)
			}
		})
	}
}

func TestExpandUniqueSlashPrefix(t *testing.T) {
	if got := expandUniqueSlashPrefix("/com"); got != "/compact" {
		t.Fatalf("expected /compact, got %q", got)
	}
	if got := expandUniqueSlashPrefix("/tool"); got != "/tool" {
		t.Fatalf("removed local command should stay unchanged, got %q", got)
	}
	if got := expandUniqueSlashPrefix("/bud"); got != "/bud" {
		t.Fatalf("removed local command should stay unchanged, got %q", got)
	}
	if got := expandUniqueSlashPrefix("/plan inspect"); got != "/plan inspect" {
		t.Fatalf("commands with args should stay unchanged, got %q", got)
	}
	if got := expandUniqueSlashPrefix("/as"); got != "/ask" {
		t.Fatalf("expected /ask, got %q", got)
	}
	if got := expandUniqueSlashPrefix("/Users/goranka/Engineer/ai/dsk"); got != "/Users/goranka/Engineer/ai/dsk" {
		t.Fatalf("absolute path should stay unchanged, got %q", got)
	}
}

func TestLooksLikeSlashCommand(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{line: "/", want: true},
		{line: "/plan", want: true},
		{line: "/plan inspect parser", want: true},
		{line: "/Users/goranka/Engineer/ai/dsk", want: false},
		{line: "/tmp/project 里有几个 go 项目", want: false},
		{line: " /status", want: true},
		{line: "inspect /tmp/project", want: false},
	}
	for _, tc := range cases {
		if got := appcommands.LooksLikeSlashCommand(tc.line); got != tc.want {
			t.Fatalf("LooksLikeSlashCommand(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestResolveCLIResumeID(t *testing.T) {
	got, matched, err := resolveCLIResumeID([]string{"resume", "s-1"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !matched || got != "s-1" {
		t.Fatalf("unexpected result: got=%q matched=%v", got, matched)
	}

	_, matched, err = resolveCLIResumeID([]string{"resume"})
	if err == nil || !matched {
		t.Fatalf("expected usage error for missing id, matched=%v err=%v", matched, err)
	}

	got, matched, err = resolveCLIResumeID([]string{"other", "x"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if matched || got != "" {
		t.Fatalf("unexpected non-resume parse: got=%q matched=%v", got, matched)
	}
}

func TestHandleCommandModeSwitch(t *testing.T) {
	now := time.Date(2026, 5, 3, 8, 0, 0, 0, time.UTC)
	res, err := handleCommand("/status", "cur", now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res.Handled || !res.ShowStatus {
		t.Fatalf("unexpected /status result: %+v", res)
	}

	res, err = handleCommand("/plan", "cur", now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res.Handled || res.Mode != "plan" {
		t.Fatalf("unexpected /plan result: %+v", res)
	}

	res, err = handleCommand("/agent", "cur", now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res.Handled || res.Mode != "agent" {
		t.Fatalf("unexpected /agent result: %+v", res)
	}

	if _, err = handleCommand("/plan show", "cur", now); err == nil {
		t.Fatal("expected /plan show usage error")
	}

	res, err = handleCommand("/plan implement tests", "cur", now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res.Handled || res.Mode != "plan" || res.PlanPrompt != "implement tests" {
		t.Fatalf("unexpected /plan prompt result: %+v", res)
	}

	if _, err = handleCommand("/plan on", "cur", now); err == nil {
		t.Fatal("expected /plan on usage error")
	}
	if _, err = handleCommand("/plan off", "cur", now); err == nil {
		t.Fatal("expected /plan off usage error")
	}

	res, err = handleCommand("/ask", "cur", now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res.Handled || res.Mode != "ask" {
		t.Fatalf("unexpected /ask result: %+v", res)
	}

	res, err = handleCommand("/ask inspect the parser", "cur", now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res.Handled || res.Mode != "ask" || res.AskPrompt != "inspect the parser" {
		t.Fatalf("unexpected /ask prompt result: %+v", res)
	}

	for _, old := range []string{"/step", "/checkpoint", "/continue", "/stop", "/revise add retry", "/context", "/memory"} {
		res, err = handleCommand(old, "cur", now)
		if err != nil || res.Handled {
			t.Fatalf("expected %s to be unhandled, got %+v err=%v", old, res, err)
		}
	}
	res, err = handleCommand("/init", "cur", now)
	if err != nil || !res.Handled || !res.InitMemory {
		t.Fatalf("unexpected /init result: %+v err=%v", res, err)
	}
	res, err = handleCommand("/skills", "cur", now)
	if err != nil || !res.Handled || !res.ShowSkills {
		t.Fatalf("unexpected /skills result: %+v err=%v", res, err)
	}
	if _, err = handleCommand("/skills disable code-review", "cur", now); err == nil || !strings.Contains(err.Error(), "usage: /skills") {
		t.Fatalf("expected /skills subcommand usage error, got %v", err)
	}
}

func TestHandleLocalCommandStats(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	writeUsageRecord(t, filepath.Join(dir, "usage.jsonl"), telemetry.UsageRecord{
		TS:               time.Date(2026, 5, 12, 10, 0, 0, 0, time.Local).UnixMilli(),
		Session:          "s1",
		Model:            "deepseek-v4-flash",
		PromptTokens:     1000,
		CompletionTokens: 200,
		PromptCacheHit:   800,
		PromptCacheMiss:  200,
		CostUSD:          0.0123,
	})
	writeToolInputEvent(t, sessionsDir, "s1", telemetry.ToolInputEvent{
		TS:         time.Date(2026, 5, 12, 10, 1, 0, 0, time.Local).UnixMilli(),
		Session:    "s1",
		Model:      "deepseek-v4-flash",
		Tool:       "read_file",
		Event:      "tool_input_repaired",
		RepairKind: "markdown_autolink_path",
		Path:       "file_path",
	})
	writeToolInputEvent(t, sessionsDir, "s1", telemetry.ToolInputEvent{
		TS:        time.Date(2026, 5, 12, 10, 2, 0, 0, time.Local).UnixMilli(),
		Session:   "s1",
		Model:     "deepseek-v4-flash",
		Tool:      "write",
		Event:     "tool_input_invalid",
		ErrorCode: "invalid_args",
	})
	a := &App{
		cfg:         Config{DataDir: dir},
		sessionsDir: sessionsDir,
	}

	handled, out, _, err := a.HandleLocalCommand("/stats")
	if err != nil {
		t.Fatalf("stats command: %v", err)
	}
	if !handled {
		t.Fatal("expected /stats to be handled")
	}
	for _, want := range []string{
		"Stats",
		"Usage",
		"- turns: 1",
		"- tokens: 1.2K total",
		"- estimated cost: $0.0123 total",
		"- top model: deepseek-v4-flash · 1 turns",
		"Tool input",
		"- repaired: 1",
		"- invalid: 1",
		"- repair rate: 50.0%",
		"- top repair: markdown_autolink_path · 1",
		"- top invalid tool: write · 1",
		"More: /stats usage, /stats tools, /stats repair, /stats recent, /stats all",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected stats to contain %q, got:\n%s", want, out)
		}
	}
	for _, dontWant := range []string{
		"Recent tool-input events",
		"Invalid codes",
		"Top tools",
	} {
		if strings.Contains(out, dontWant) {
			t.Fatalf("expected overview stats to omit %q, got:\n%s", dontWant, out)
		}
	}
	if strings.Contains(out, `"input"`) {
		t.Fatalf("stats should not expose raw input fields:\n%s", out)
	}

	handled, out, _, err = a.HandleLocalCommand("/stats usage")
	if err != nil || !handled {
		t.Fatalf("stats usage command handled=%v err=%v", handled, err)
	}
	for _, want := range []string{
		"- sessions: 1",
		"deepseek-v4-flash: 1 turns",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected usage stats to contain %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Tool input") {
		t.Fatalf("expected usage stats to omit tool input section:\n%s", out)
	}

	handled, out, _, err = a.HandleLocalCommand("/stats tools")
	if err != nil || !handled {
		t.Fatalf("stats tools command handled=%v err=%v", handled, err)
	}
	for _, want := range []string{
		"Tool input",
		"markdown_autolink_path: 1",
		"invalid_args: 1",
		"read_file: 1 repaired",
		"write: 0 repaired · 1 invalid",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected tool stats to contain %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Recent tool-input events") {
		t.Fatalf("expected tool stats to omit recent events:\n%s", out)
	}

	handled, out, _, err = a.HandleLocalCommand("/stats recent")
	if err != nil || !handled {
		t.Fatalf("stats recent command handled=%v err=%v", handled, err)
	}
	for _, want := range []string{
		"Recent turns",
		"Recent tool-input events",
		"markdown_autolink_path · file_path",
		"invalid_args",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected recent stats to contain %q, got:\n%s", want, out)
		}
	}

	handled, out, _, err = a.HandleLocalCommand("/stats all")
	if err != nil || !handled {
		t.Fatalf("stats all command handled=%v err=%v", handled, err)
	}
	for _, want := range []string{
		"Usage",
		"Tool input",
		"Recent turns",
		"Recent tool-input events",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected all stats to contain %q, got:\n%s", want, out)
		}
	}

	handled, _, _, err = a.HandleLocalCommand("/stats extra")
	if !handled || err == nil || !strings.Contains(err.Error(), "usage: /stats [usage|tools|repair|recent|all]") {
		t.Fatalf("expected /stats usage error, handled=%v err=%v", handled, err)
	}
}

func TestHandleLocalCommandFocusTogglesAndPersists(t *testing.T) {
	dir := t.TempDir()
	a := &App{cfg: Config{DataDir: dir, ViewMode: ViewModeDefault}}
	handled, out, _, err := a.HandleLocalCommand("/focus")
	if err != nil {
		t.Fatalf("HandleLocalCommand: %v", err)
	}
	if !handled || out != "Focus view enabled" {
		t.Fatalf("focus output: handled=%v out=%q", handled, out)
	}
	if a.ViewMode() != ViewModeFocus {
		t.Fatalf("view mode: want focus, got %q", a.ViewMode())
	}
	loaded, ok, err := LoadConfigFile(GlobalConfigPath(dir))
	if err != nil {
		t.Fatalf("LoadConfigFile: %v", err)
	}
	if !ok || loaded.UI.ViewMode != ViewModeFocus {
		t.Fatalf("persisted view mode: ok=%v cfg=%+v", ok, loaded.UI)
	}
}

func TestCommandsHelpKeepsSkillCommandOutOfPrimaryList(t *testing.T) {
	if !strings.Contains(CommandsHelp, "/agent") {
		t.Fatalf("expected /agent in help: %s", CommandsHelp)
	}
	if !strings.Contains(CommandsHelp, "/skills") {
		t.Fatalf("expected /skills in help: %s", CommandsHelp)
	}
	if strings.Contains(CommandsHelp, "/skill ") {
		t.Fatalf("expected /skill debug command to stay out of primary help: %s", CommandsHelp)
	}
}

func TestHandleSlashInitReturnsSyntheticPrompt(t *testing.T) {
	dir := t.TempDir()
	app := &App{workspaceRoot: dir, sessionID: "cur"}
	handled, output, synthetic, shouldExit, _, err := app.HandleSlash("/init")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !handled || shouldExit {
		t.Fatalf("unexpected handled=%v shouldExit=%v", handled, shouldExit)
	}
	if !strings.Contains(output, "Initializing AGENTS.md") {
		t.Fatalf("unexpected output: %q", output)
	}
	if !strings.Contains(synthetic, "Generate a file named AGENTS.md") {
		t.Fatalf("missing synthetic init prompt: %q", synthetic)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("expected /init not to write AGENTS.md directly, err=%v", err)
	}
}

func TestHandleCommandClear(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	res, err := handleCommand("/clear", "cur", now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res.Handled {
		t.Fatal("expected /clear to be handled")
	}
	if !res.ClearScreen {
		t.Fatal("expected clearScreen=true for /clear")
	}
	if res.SessionID != "cur" {
		t.Fatalf("expected session unchanged, got %s", res.SessionID)
	}
}

func TestHandleSlashClearReturnsClearScreenFlag(t *testing.T) {
	app := &App{sessionID: "sess-1", workspaceRoot: t.TempDir()}
	handled, out, synthetic, shouldExit, clearScreen, err := app.HandleSlash("/clear")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !handled {
		t.Fatal("expected /clear to be handled")
	}
	if !clearScreen {
		t.Fatal("expected clearScreen=true")
	}
	if shouldExit {
		t.Fatal("expected shouldExit=false")
	}
	if synthetic != "" {
		t.Fatal("expected no synthetic prompt")
	}
	if !strings.Contains(out, "terminal cleared") {
		t.Fatalf("expected output to mention terminal cleared, got: %q", out)
	}
}

func TestBuildStatusIncludesContextAndBudget(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	msgStore, err := store.NewJSONLStore(sessionsDir)
	if err != nil {
		t.Fatalf("store init: %v", err)
	}
	app := &App{
		ctx:           context.Background(),
		workspaceRoot: dir,
		sessionID:     "sess-1",
		msgStore:      msgStore,
		contextWindow: 1000,
		cfg:           DefaultConfig(),
	}

	out := app.buildStatus()
	for _, want := range []string{
		"- context window:",
		"- budget limit: disabled",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected status to contain %q, got:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"- memory:", "- mcp:"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("expected status not to contain %q, got:\n%s", unwanted, out)
		}
	}
}

func TestStartupLinesIncludeEffectiveThinkingAndEffort(t *testing.T) {
	app := &App{
		sessionID:        "sess-1",
		currentMode:      "agent",
		approvalMode:     "never",
		model:            "deepseek-v4-pro",
		reasoningEffort:  "max",
		thinkingEnabled:  false,
		budgetWarningUSD: 0,
	}

	lines := app.StartupLines()
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"model: deepseek-v4-pro",
		"effort: max",
		"thinking: off",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected startup lines to contain %q, got:\n%s", want, joined)
		}
	}
}

func TestBuildStatusIncludesEffectiveThinkingAndEffort(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	msgStore, err := store.NewJSONLStore(sessionsDir)
	if err != nil {
		t.Fatalf("store init: %v", err)
	}
	app := &App{
		ctx:              context.Background(),
		workspaceRoot:    dir,
		sessionID:        "sess-1",
		msgStore:         msgStore,
		contextWindow:    1000,
		currentMode:      "agent",
		approvalMode:     "never",
		model:            "deepseek-v4-pro",
		reasoningEffort:  "max",
		thinkingEnabled:  false,
		budgetWarningUSD: 0,
		cfg:              DefaultConfig(),
	}

	out := app.buildStatus()
	for _, want := range []string{
		"- model: deepseek-v4-pro",
		"- effort: max",
		"- thinking: off",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected status to contain %q, got:\n%s", want, out)
		}
	}
}

func TestHandleSlashSkillsCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	writeAppSkill(t, filepath.Join(dir, ".whale", "skills", "test-skill"), "test-skill", "Workspace skill.", "# Test Skill\n\nFollow workspace instructions.")
	writeAppSkillWithFrontmatter(t, filepath.Join(dir, ".whale", "skills", "needs-setup"), "---\nname: needs-setup\ndescription: Needs setup skill.\nrequires:\n  env: [WHALE_TEST_MISSING_ENV]\n---\n\n# Needs setup")
	writeAppSkill(t, filepath.Join(dir, ".whale", "skills", "disabled-skill"), "disabled-skill", "Disabled skill.", "# Disabled")
	app := &App{sessionID: "sess-1", workspaceRoot: dir, cfg: Config{SkillsDisabled: []string{"disabled-skill"}}}

	handled, out, synthetic, shouldExit, clearScreen, err := app.HandleSlash("/skills")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !handled || shouldExit || clearScreen || synthetic != "" {
		t.Fatalf("unexpected /skills flags handled=%v shouldExit=%v clearScreen=%v synthetic=%q", handled, shouldExit, clearScreen, synthetic)
	}
	for _, want := range []string{"Ready", "test-skill", "Needs setup", "needs-setup", "Disabled", "disabled-skill", "Use a skill with `$skill-name`.", "Manage skills from the TUI with `/skills`."} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected /skills output to contain %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Follow workspace instructions") {
		t.Fatalf("unexpected /skills output: %q", out)
	}

}

func TestSetSkillEnabledUpdatesProjectConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	writeAppSkill(t, filepath.Join(dir, ".whale", "skills", "test-skill"), "test-skill", "Workspace skill.", "# Test Skill\n\nFollow workspace instructions.")
	app := &App{sessionID: "sess-1", workspaceRoot: dir, cfg: DefaultConfig()}

	out, err := app.SetSkillEnabled("test-skill", false)
	if err != nil {
		t.Fatalf("disable unexpected err: %v", err)
	}
	if !strings.Contains(out, "disabled skill: test-skill") {
		t.Fatalf("unexpected disable output: %q", out)
	}
	if !containsString(app.cfg.SkillsDisabled, "test-skill") {
		t.Fatalf("expected in-memory disabled list to include test-skill, got %+v", app.cfg.SkillsDisabled)
	}
	projectCfg, loaded, err := LoadConfigFile(ProjectConfigPath(dir))
	if err != nil || !loaded {
		t.Fatalf("load project config loaded=%v err=%v", loaded, err)
	}
	if !containsString(projectCfg.Skills.Disabled, "test-skill") {
		t.Fatalf("expected project config disabled list to include test-skill, got %+v", projectCfg.Skills.Disabled)
	}
	if _, _, _, err := app.BuildSkillMentionSyntheticPrompt("$test-skill"); err == nil || !strings.Contains(err.Error(), "skill disabled") {
		t.Fatalf("expected disabled skill mention error, got %v", err)
	}
	if out := app.buildSkillsList(); !strings.Contains(out, "Disabled") || !strings.Contains(out, "test-skill") {
		t.Fatalf("expected /skills to show disabled skill, got:\n%s", out)
	}

	out, err = app.SetSkillEnabled("test-skill", true)
	if err != nil {
		t.Fatalf("enable unexpected err: %v", err)
	}
	if !strings.Contains(out, "enabled skill: test-skill") {
		t.Fatalf("unexpected enable result out=%q", out)
	}
	if containsString(app.cfg.SkillsDisabled, "test-skill") {
		t.Fatalf("expected in-memory disabled list to drop test-skill, got %+v", app.cfg.SkillsDisabled)
	}
	projectCfg, loaded, err = LoadConfigFile(ProjectConfigPath(dir))
	if err != nil || !loaded {
		t.Fatalf("reload project config loaded=%v err=%v", loaded, err)
	}
	if containsString(projectCfg.Skills.Disabled, "test-skill") {
		t.Fatalf("expected project config disabled list to drop test-skill, got %+v", projectCfg.Skills.Disabled)
	}
	ok, out, synthetic, err := app.BuildSkillMentionSyntheticPrompt("$test-skill")
	if err != nil || !ok || !strings.Contains(out, "loaded skill: test-skill") || !strings.Contains(synthetic, "Follow workspace instructions") {
		t.Fatalf("expected enabled skill mention, ok=%v out=%q synthetic=%q err=%v", ok, out, synthetic, err)
	}
}

func TestBuildSkillMentionSyntheticPrompt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	writeAppSkill(t, filepath.Join(dir, ".whale", "skills", "test-skill"), "test-skill", "Workspace skill.", "# Test Skill\n\nFollow workspace instructions.")
	writeAppSkillWithFrontmatter(t, filepath.Join(dir, ".whale", "skills", "needs-setup"), "---\nname: needs-setup\ndescription: Needs setup skill.\nwhen: Use when setup is missing.\nrequires:\n  env: [WHALE_TEST_MISSING_ENV]\n---\n\n# Needs setup")
	writeAppSkill(t, filepath.Join(dir, ".whale", "skills", "disabled-skill"), "disabled-skill", "Disabled skill.", "# Disabled")
	app := &App{sessionID: "sess-1", workspaceRoot: dir, cfg: Config{SkillsDisabled: []string{"disabled-skill"}}}

	ok, out, synthetic, err := app.BuildSkillMentionSyntheticPrompt("$test-skill arg1 arg2")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !ok {
		t.Fatal("expected skill mention match")
	}
	if !strings.Contains(out, "loaded skill: test-skill") {
		t.Fatalf("unexpected output: %q", out)
	}
	if !strings.Contains(synthetic, "Follow workspace instructions") || !strings.Contains(synthetic, "arg1 arg2") {
		t.Fatalf("unexpected synthetic prompt: %q", synthetic)
	}
	ok, out, synthetic, err = app.BuildSkillMentionSyntheticPrompt("$needs-setup do it")
	if err != nil || !ok {
		t.Fatalf("expected needs setup skill mention, ok=%v err=%v", ok, err)
	}
	if !strings.Contains(out, "Needs: WHALE_TEST_MISSING_ENV") || !strings.Contains(synthetic, "<setup_status>Needs: WHALE_TEST_MISSING_ENV</setup_status>") || !strings.Contains(synthetic, "<when>Use when setup is missing.</when>") {
		t.Fatalf("unexpected needs setup output=%q synthetic=%q", out, synthetic)
	}
	ok, _, _, err = app.BuildSkillMentionSyntheticPrompt("$disabled-skill")
	if err == nil || !strings.Contains(err.Error(), "skill disabled") || !ok {
		t.Fatalf("expected disabled skill error, ok=%v err=%v", ok, err)
	}

	ok, _, _, err = app.BuildSkillMentionSyntheticPrompt("please use $test-skill")
	if err != nil || ok {
		t.Fatalf("expected non-leading mention to be ignored, ok=%v err=%v", ok, err)
	}
}

func TestBuildSkillMentionSyntheticPromptWithBinding(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".whale", "skills", "test-skill")
	writeAppSkill(t, skillDir, "test-skill", "Workspace skill.", "# Test Skill\n\nBound instructions.")
	app := &App{sessionID: "sess-1", workspaceRoot: dir, cfg: DefaultConfig()}
	binding := &SkillBinding{Name: "test-skill", SkillFilePath: filepath.Join(skillDir, "SKILL.md")}

	ok, out, synthetic, err := app.BuildSkillMentionSyntheticPromptWithBinding("$test-skill run bound", binding)
	if err != nil || !ok {
		t.Fatalf("expected bound skill mention, ok=%v err=%v", ok, err)
	}
	if !strings.Contains(out, "loaded skill: test-skill") || !strings.Contains(synthetic, "Bound instructions.") || !strings.Contains(synthetic, "run bound") {
		t.Fatalf("unexpected bound skill output=%q synthetic=%q", out, synthetic)
	}

	_, _, _, err = app.BuildSkillMentionSyntheticPromptWithBinding("$other-skill", binding)
	if err == nil || !strings.Contains(err.Error(), "skill binding mismatch") {
		t.Fatalf("expected binding mismatch error, got %v", err)
	}

	outside := filepath.Join(t.TempDir(), "test-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatalf("mkdir outside skill: %v", err)
	}
	if err := os.WriteFile(outside, []byte("---\nname: test-skill\ndescription: Outside skill.\n---\n\n# Outside\n"), 0o644); err != nil {
		t.Fatalf("write outside skill: %v", err)
	}
	_, _, _, err = app.BuildSkillMentionSyntheticPromptWithBinding("$test-skill", &SkillBinding{Name: "test-skill", SkillFilePath: outside})
	if err == nil || !strings.Contains(err.Error(), "skill unavailable") {
		t.Fatalf("expected unavailable outside binding error, got %v", err)
	}
}

func TestHandleSlashNewIncludesResumeHint(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Write a message so old session has content.
	store, err := store.NewJSONLStore(sessionsDir)
	if err != nil {
		t.Fatalf("store init: %v", err)
	}
	if _, err := store.Create(context.Background(), core.Message{SessionID: "sess-1", Role: core.RoleUser, Text: "hello"}); err != nil {
		t.Fatalf("append: %v", err)
	}

	app := &App{
		sessionsDir:   sessionsDir,
		workspaceRoot: dir,
		sessionID:     "sess-1",
		msgStore:      store,
		ctx:           context.Background(),
	}
	handled, out, synthetic, shouldExit, clearScreen, err := app.HandleSlash("/new\tfresh")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !handled {
		t.Fatal("expected /new to be handled")
	}
	if clearScreen {
		t.Fatal("expected clearScreen=false for /new")
	}
	if shouldExit {
		t.Fatal("expected shouldExit=false")
	}
	if synthetic != "" {
		t.Fatal("expected no synthetic prompt")
	}
	if app.SessionID() != "fresh" {
		t.Fatalf("expected tab-separated session id fresh, got %s", app.SessionID())
	}
	if !strings.Contains(out, "new session: fresh") {
		t.Fatalf("expected output to contain new session, got: %q", out)
	}
	if !strings.Contains(out, "dropped 1 message") {
		t.Fatalf("expected output to mention dropped messages, got: %q", out)
	}
	if !strings.Contains(out, "whale resume sess-1") {
		t.Fatalf("expected output to include resume hint, got: %q", out)
	}
}

func writeAppSkill(t *testing.T, dir, name, desc, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "---\nname: " + name + "\ndescription: " + desc + "\n---\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
}

func writeAppSkillWithFrontmatter(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
}

func writeUsageRecord(t *testing.T, path string, rec telemetry.UsageRecord) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir usage dir: %v", err)
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal usage record: %v", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open usage log: %v", err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		t.Fatalf("write usage log: %v", err)
	}
}

func writeToolInputEvent(t *testing.T, sessionsDir, sessionID string, rec telemetry.ToolInputEvent) {
	t.Helper()
	if err := telemetry.AppendToolInputEvent(sessionsDir, rec, time.UnixMilli(rec.TS)); err != nil {
		t.Fatalf("append tool input event for %s: %v", sessionID, err)
	}
}
