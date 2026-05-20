package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	appcommands "github.com/usewhale/whale/internal/app/commands"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/store"
	"github.com/usewhale/whale/internal/telemetry"
	whaleworktree "github.com/usewhale/whale/internal/worktree"
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
	if _, err := uuid.Parse(res.SessionID); err != nil {
		t.Fatalf("expected valid UUID, got %s", res.SessionID)
	}
	if res.SessionID == "cur" {
		t.Fatalf("expected different session ID, got %s", res.SessionID)
	}

	u7, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("failed to generate uuid v7: %v", err)
	}
	currentUUID := u7.String()
	res, err = handleCommand("/new", currentUUID, now)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if _, err := uuid.Parse(res.SessionID); err != nil {
		t.Fatalf("expected valid UUID, got %s", res.SessionID)
	}
	if res.SessionID == currentUUID {
		t.Fatalf("expected different session ID, got %s", res.SessionID)
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

	res, err = handleCommand("/fork", "cur", now)
	if err != nil {
		t.Fatalf("unexpected /fork err: %v", err)
	}
	if res.SessionID != "cur" || res.ForkName != "" {
		t.Fatalf("unexpected /fork result: %+v", res)
	}

	res, err = handleCommand("/fork\tbranch-name", "cur", now)
	if err != nil {
		t.Fatalf("unexpected named /fork err: %v", err)
	}
	if res.SessionID != "cur" || res.ForkName != "branch-name" {
		t.Fatalf("unexpected named /fork result: %+v", res)
	}

	_, err = handleCommand("/fork a b", "cur", now)
	if err == nil {
		t.Fatal("expected /fork usage error")
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
		{line: "/feedback", want: appcommands.SubmitLocalReadOnly},
		{line: "/help", want: appcommands.SubmitLocalReadOnly},
		{line: "/worktree", want: appcommands.SubmitLocalReadOnly},
		{line: "/worktree list", want: appcommands.SubmitLocalReadOnly},
		{line: "/worktree status", want: appcommands.SubmitLocalReadOnly},
		{line: "/worktree status feature", want: appcommands.SubmitLocalReadOnly},
		{line: "/worktree remove feature", want: appcommands.SubmitLocalMutating},
		{line: "/worktree remove feature --force", want: appcommands.SubmitLocalMutating},
		{line: "/model", want: appcommands.SubmitLocalUI},
		{line: "/permissions", want: appcommands.SubmitLocalUI},
		{line: "/focus", want: appcommands.SubmitLocalUI},
		{line: "/skills", want: appcommands.SubmitLocalUI},
		{line: "/plugins", want: appcommands.SubmitLocalUI},
		{line: "/review", want: appcommands.SubmitLocalUI},
		{line: "/review local", want: appcommands.SubmitTurnStarting},
		{line: "/review pr 123", want: appcommands.SubmitTurnStarting},
		{line: "/review commit abc123", want: appcommands.SubmitTurnStarting},
		{line: "/review inspect auth changes", want: appcommands.SubmitTurnStarting},
		{line: "/btw what is happening?", want: appcommands.SubmitLocalReadOnly},
		{line: "/memory", want: appcommands.SubmitLocalReadOnly},
		{line: "/memory list", want: appcommands.SubmitLocalReadOnly},
		{line: "/memory path", want: appcommands.SubmitLocalReadOnly},
		{line: "/memory show global/style", want: appcommands.SubmitLocalReadOnly},
		{line: "/memory forget global/style", want: appcommands.SubmitLocalMutating},
		{line: "/resume", want: appcommands.SubmitLocalUI},
		{line: "/agent", want: appcommands.SubmitLocalMode},
		{line: "/ask", want: appcommands.SubmitLocalMode},
		{line: "/plan", want: appcommands.SubmitLocalMode},
		{line: "/new", want: appcommands.SubmitLocalMutating},
		{line: "/new scratch", want: appcommands.SubmitLocalMutating},
		{line: "/new\tscratch", want: appcommands.SubmitLocalMutating},
		{line: "/fork", want: appcommands.SubmitLocalMutating},
		{line: "/fork scratch", want: appcommands.SubmitLocalMutating},
		{line: "/fork\tscratch", want: appcommands.SubmitLocalMutating},
		{line: "/clear", want: appcommands.SubmitLocalMutating},
		{line: "/exit", want: appcommands.SubmitExit},
		{line: "/ask inspect", want: appcommands.SubmitTurnStarting},
		{line: "/plan propose a fix", want: appcommands.SubmitTurnStarting},
		{line: "/init", want: appcommands.SubmitTurnStarting},
		{line: "/compact", want: appcommands.SubmitTurnStarting},
		{line: "/model xxx", want: appcommands.SubmitUsageError},
		{line: "/focus now", want: appcommands.SubmitUsageError},
		{line: "/skills xxx", want: appcommands.SubmitUsageError},
		{line: "/plugins status memory", want: appcommands.SubmitUsageError},
		{line: "/btw", want: appcommands.SubmitUsageError},
		{line: "/btw   ", want: appcommands.SubmitUsageError},
		{line: "/review pr", want: appcommands.SubmitTurnStarting},
		{line: "/memory bad", want: appcommands.SubmitUsageError},
		{line: "/memory show", want: appcommands.SubmitUsageError},
		{line: "/skills-improver status", want: appcommands.SubmitUsageError},
		{line: "/resume xxx", want: appcommands.SubmitUsageError},
		{line: "/new a b", want: appcommands.SubmitUsageError},
		{line: "/fork a b", want: appcommands.SubmitUsageError},
		{line: "/stats bad", want: appcommands.SubmitUsageError},
		{line: "/feedback now", want: appcommands.SubmitUsageError},
		{line: "/help now", want: appcommands.SubmitUsageError},
		{line: "/worktree remove feature --bad", want: appcommands.SubmitUsageError},
		{line: "/worktree remove", want: appcommands.SubmitUsageError},
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

func TestHandleLocalCommandWorktreeStatusAndList(t *testing.T) {
	repo := newAppGitRepo(t)
	sess, err := whaleworktree.Start(repo, "feature")
	if err != nil {
		t.Fatalf("Start worktree: %v", err)
	}
	app := &App{
		workspaceRoot: repo,
		worktree: WorktreeSession{
			Name:   "feature",
			Path:   sess.Path,
			Branch: sess.Branch,
		},
	}

	handled, out, _, err := app.HandleLocalCommand("/worktree")
	if err != nil {
		t.Fatalf("/worktree: %v", err)
	}
	if !handled || !strings.Contains(out, "current: feature") || !strings.Contains(out, sess.Path) {
		t.Fatalf("unexpected /worktree output:\n%s", out)
	}

	handled, out, _, err = app.HandleLocalCommand("/worktree list")
	if err != nil {
		t.Fatalf("/worktree list: %v", err)
	}
	if !handled || !strings.Contains(out, "feature") || !strings.Contains(out, "clean") {
		t.Fatalf("unexpected /worktree list output:\n%s", out)
	}
}

func TestHandleLocalCommandWorktreeRemoveDirtyGuard(t *testing.T) {
	repo := newAppGitRepo(t)
	sess, err := whaleworktree.Start(repo, "dirty")
	if err != nil {
		t.Fatalf("Start worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sess.Path, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatalf("write dirty: %v", err)
	}
	app := &App{workspaceRoot: repo}

	handled, _, _, err := app.HandleLocalCommand("/worktree remove dirty")
	if !handled || err == nil || !strings.Contains(err.Error(), "has changes") {
		t.Fatalf("expected dirty guard, handled=%v err=%v", handled, err)
	}

	handled, out, _, err := app.HandleLocalCommand("/worktree remove dirty --force")
	if err != nil {
		t.Fatalf("/worktree remove --force: %v", err)
	}
	if !handled || !strings.Contains(out, "Removed worktree") {
		t.Fatalf("unexpected remove output:\n%s", out)
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
	if got := expandUniqueSlashPrefix("/bt"); got != "/btw" {
		t.Fatalf("expected /btw, got %q", got)
	}
	if got := expandUniqueSlashPrefix("/Users/goranka/Engineer/ai/dsk"); got != "/Users/goranka/Engineer/ai/dsk" {
		t.Fatalf("absolute path should stay unchanged, got %q", got)
	}
}

func TestClassifyBtwBusyImmediate(t *testing.T) {
	got := appcommands.ClassifySubmit("/btw summarize this", CommandsHelp, "/mcp")
	if !got.LocalNoTurn() {
		t.Fatal("expected /btw to be local no-turn")
	}
	if !got.BusyImmediate() {
		t.Fatal("expected /btw to be available while busy")
	}
}

func TestCommandsHelpMarksBtwQuestionRequired(t *testing.T) {
	if !strings.Contains(CommandsHelp, "/btw <question>") {
		t.Fatalf("expected /btw to advertise a required question: %s", CommandsHelp)
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

func TestHandleLocalCommandFeedbackOpensIssues(t *testing.T) {
	opened := ""
	oldOpen := openFeedbackURL
	openFeedbackURL = func(url string) error {
		opened = url
		return nil
	}
	t.Cleanup(func() { openFeedbackURL = oldOpen })

	a := &App{}
	handled, out, synthetic, err := a.HandleLocalCommand("/feedback")
	if err != nil {
		t.Fatalf("HandleLocalCommand: %v", err)
	}
	if !handled {
		t.Fatal("expected /feedback to be handled")
	}
	if synthetic != "" {
		t.Fatalf("expected no synthetic prompt, got %q", synthetic)
	}
	if opened != FeedbackIssuesURL {
		t.Fatalf("opened %q, want %q", opened, FeedbackIssuesURL)
	}
	if !strings.Contains(out, FeedbackIssuesURL) || !strings.Contains(out, "Opening feedback issues") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestHandleLocalCommandFeedbackReportsOpenError(t *testing.T) {
	oldOpen := openFeedbackURL
	openFeedbackURL = func(url string) error {
		return errors.New("opener missing")
	}
	t.Cleanup(func() { openFeedbackURL = oldOpen })

	a := &App{}
	handled, out, synthetic, err := a.HandleLocalCommand("/feedback")
	if err != nil {
		t.Fatalf("HandleLocalCommand: %v", err)
	}
	if !handled {
		t.Fatal("expected /feedback to be handled")
	}
	if synthetic != "" {
		t.Fatalf("expected no synthetic prompt, got %q", synthetic)
	}
	if !strings.Contains(out, FeedbackIssuesURL) ||
		!strings.Contains(out, "Could not open browser: opener missing") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestHandleLocalCommandHelp(t *testing.T) {
	a := &App{}
	handled, out, synthetic, err := a.HandleLocalCommand("/help")
	if err != nil {
		t.Fatalf("HandleLocalCommand: %v", err)
	}
	if !handled {
		t.Fatal("expected /help to be handled")
	}
	if synthetic != "" {
		t.Fatalf("expected no synthetic prompt, got %q", synthetic)
	}
	for _, want := range []string{
		"Whale help",
		"Browse default commands:",
		"/agent",
		"/ask [prompt]",
		"/compact",
		"/review [local|branch|pr|commit|<instructions>]",
		"/status",
		"/stats [usage|tools|repair|recent|all]",
		"/plugins",
		"/feedback",
		"For more help:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected /help output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestHandleLocalCommandHelpUsageError(t *testing.T) {
	a := &App{}
	handled, _, _, err := a.HandleLocalCommand("/help now")
	if !handled || err == nil || !strings.Contains(err.Error(), "usage: /help") {
		t.Fatalf("expected /help usage error, handled=%v err=%v", handled, err)
	}
}

func TestCommandsHelpKeepsSkillCommandOutOfPrimaryList(t *testing.T) {
	if !strings.Contains(CommandsHelp, "/agent") {
		t.Fatalf("expected /agent in help: %s", CommandsHelp)
	}
	if !strings.Contains(CommandsHelp, "/help") {
		t.Fatalf("expected /help in help: %s", CommandsHelp)
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
	if out != "" {
		t.Fatalf("expected /clear to avoid appending a local result, got: %q", out)
	}
}

func TestHandleSlashBtwReturnsTUIOnlyError(t *testing.T) {
	app := &App{sessionID: "sess-1", workspaceRoot: t.TempDir()}
	handled, out, synthetic, shouldExit, clearScreen, err := app.HandleSlash("/btw quick question")
	if err == nil {
		t.Fatal("expected /btw to return an error outside the TUI service path")
	}
	if !strings.Contains(err.Error(), "interactive TUI") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled || out != "" || synthetic != "" || shouldExit || clearScreen {
		t.Fatalf("unexpected result: handled=%v out=%q synthetic=%q shouldExit=%v clear=%v", handled, out, synthetic, shouldExit, clearScreen)
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

func TestStartupLinesIncludeWorktree(t *testing.T) {
	app := &App{
		sessionID:    "sess-1",
		currentMode:  "agent",
		approvalMode: "never",
		worktree: WorktreeSession{
			Name: "feature",
			Path: "/tmp/repo/.whale/worktrees/feature",
		},
	}

	lines := app.StartupLines()
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "worktree: feature (/tmp/repo/.whale/worktrees/feature)") {
		t.Fatalf("expected worktree startup line, got:\n%s", joined)
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

func TestHandleSlashReviewBuildsHiddenPrompt(t *testing.T) {
	app := &App{sessionID: "sess-1", cfg: DefaultConfig()}

	handled, out, synthetic, shouldExit, clearScreen, err := app.HandleSlash("/review local")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !handled || out != "" || shouldExit || clearScreen {
		t.Fatalf("unexpected /review flags handled=%v out=%q shouldExit=%v clearScreen=%v", handled, out, shouldExit, clearScreen)
	}
	for _, want := range []string{"You are an expert code reviewer", "Target: local changes", "git diff --cached", "git diff", "inspect the contents of each relevant untracked file", "git symbolic-ref --short refs/remotes/origin/HEAD", "Avoid shell pipelines, redirects", "Do not prefix commands with cd", "Start with findings"} {
		if !strings.Contains(synthetic, want) {
			t.Fatalf("review prompt missing %q:\n%s", want, synthetic)
		}
	}

	_, _, _, _, _, err = app.HandleSlash("/review pr")
	if err == nil || !strings.Contains(err.Error(), "usage: /review pr <number-or-url>") {
		t.Fatalf("expected /review pr usage error, got %v", err)
	}
}

func TestReviewPRCarriesScopedShellAllowPrefixes(t *testing.T) {
	app := &App{sessionID: "sess-1", cfg: DefaultConfig()}

	cmd, err := app.ExecuteSlash("/review pr 123")
	if err != nil {
		t.Fatalf("ExecuteSlash: %v", err)
	}
	if !cmd.Handled || cmd.Turn == nil {
		t.Fatalf("expected review turn, got %+v", cmd)
	}
	if !cmd.Turn.ReadOnly {
		t.Fatalf("expected review turn to be read-only")
	}
	for _, want := range []string{"gh pr list", "gh pr view", "gh pr diff"} {
		if !slices.Contains(cmd.Turn.ShellAllowPrefixes, want) {
			t.Fatalf("expected allow prefix %q in %+v", want, cmd.Turn.ShellAllowPrefixes)
		}
	}
	if strings.Contains(strings.Join(cmd.Turn.ShellAllowPrefixes, ","), "curl") {
		t.Fatalf("review pr should not allow curl: %+v", cmd.Turn.ShellAllowPrefixes)
	}

	local, err := app.ExecuteSlash("/review local")
	if err != nil {
		t.Fatalf("ExecuteSlash local: %v", err)
	}
	if local.Turn == nil {
		t.Fatalf("expected local review turn")
	}
	if !local.Turn.ReadOnly {
		t.Fatalf("expected local review turn to be read-only")
	}
	if len(local.Turn.ShellAllowPrefixes) != 0 {
		t.Fatalf("local review should not carry PR shell prefixes: %+v", local.Turn.ShellAllowPrefixes)
	}
}

func TestReviewPromptQuotesShellTargets(t *testing.T) {
	tests := []struct {
		name    string
		args    string
		want    string
		notWant string
	}{
		{
			name:    "branch",
			args:    "branch feature;$(touch-pwn)",
			want:    "git diff 'feature;$(touch-pwn)...HEAD'",
			notWant: "git diff feature;$(touch-pwn)...HEAD",
		},
		{
			name:    "pr",
			args:    "pr https://github.com/usewhale/whale/pull/1?x=$(touch-pwn)",
			want:    "gh pr diff 'https://github.com/usewhale/whale/pull/1?x=$(touch-pwn)'",
			notWant: "gh pr diff https://github.com/usewhale/whale/pull/1?x=$(touch-pwn)",
		},
		{
			name:    "commit",
			args:    "commit abc123;$(touch-pwn)",
			want:    "git show --stat --patch 'abc123;$(touch-pwn)'",
			notWant: "git show --stat --patch abc123;$(touch-pwn)",
		},
		{
			name: "single quote",
			args: "commit abc'123",
			want: `git show --stat --patch 'abc'"'"'123'`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prompt, err := appcommands.ReviewPromptFromArgs(tc.args)
			if err != nil {
				t.Fatalf("ReviewPromptFromArgs: %v", err)
			}
			if !strings.Contains(prompt, tc.want) {
				t.Fatalf("expected quoted command %q in prompt:\n%s", tc.want, prompt)
			}
			if tc.notWant != "" && strings.Contains(prompt, tc.notWant) {
				t.Fatalf("prompt contains unsafe unquoted command %q:\n%s", tc.notWant, prompt)
			}
		})
	}
}

func TestReviewPromptGuidesPRDiffRecovery(t *testing.T) {
	prompt, err := appcommands.ReviewPromptFromArgs("pr 123")
	if err != nil {
		t.Fatalf("ReviewPromptFromArgs: %v", err)
	}
	for _, want := range []string{
		"gh pr view '123' --json",
		"If a PR diff is truncated",
		"run one plain read-only command at a time",
		"Do not add redirects, pipes, command substitutions, semicolon chains, && chains, || fallbacks, or temp/workspace diff capture files",
		"Do not assume gh pr diff supports path filtering",
		"git diff <base>...<head> -- <path>",
		"Do not use read_file for PR-added files unless you have confirmed the PR head is checked out locally",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("review prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestReviewPRPromptCommandsAreScopedAutoAllowed(t *testing.T) {
	const args = "pr 123"
	prompt, err := appcommands.ReviewPromptFromArgs(args)
	if err != nil {
		t.Fatalf("ReviewPromptFromArgs: %v", err)
	}
	allowPrefixes, err := appcommands.ReviewShellAllowPrefixesFromArgs(args)
	if err != nil {
		t.Fatalf("ReviewShellAllowPrefixesFromArgs: %v", err)
	}
	if len(allowPrefixes) == 0 {
		t.Fatal("expected /review pr to return scoped allow prefixes")
	}
	p := policy.ScopedAllowPolicy{
		Base:               policy.DefaultToolPolicy{Mode: policy.ApprovalModeOnRequest},
		ShellAllowPrefixes: allowPrefixes,
	}
	spec := core.ToolSpec{Name: "shell_run"}
	re := regexp.MustCompile(`(?m)^-\s+(gh pr [^\n]+)$`)
	matches := re.FindAllStringSubmatch(prompt, -1)
	if len(matches) == 0 {
		t.Fatalf("expected at least one '- gh pr ...' command line in prompt:\n%s", prompt)
	}
	for _, m := range matches {
		cmd := strings.TrimSpace(m[1])
		decision := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(cmd) + `}`})
		if !decision.Allow || decision.RequiresApproval || decision.Code != "scoped_allow_prefix" {
			t.Fatalf("prompt command %q must auto-allow under scoped policy (drifted from whitelist): %+v", cmd, decision)
		}
	}
}

func TestSetSkillEnabledUpdatesProjectLocalConfig(t *testing.T) {
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
	projectCfg, loaded, err := LoadConfigFile(ProjectLocalConfigPath(dir))
	if err != nil || !loaded {
		t.Fatalf("load project local config loaded=%v err=%v", loaded, err)
	}
	if !containsString(projectCfg.Skills.Disabled, "test-skill") {
		t.Fatalf("expected project local config disabled list to include test-skill, got %+v", projectCfg.Skills.Disabled)
	}
	if _, loaded, err := LoadConfigFile(ProjectConfigPath(dir)); err != nil || loaded {
		t.Fatalf("shared project config should not be written by skill toggle, loaded=%v err=%v", loaded, err)
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
	projectCfg, loaded, err = LoadConfigFile(ProjectLocalConfigPath(dir))
	if err != nil || !loaded {
		t.Fatalf("reload project local config loaded=%v err=%v", loaded, err)
	}
	if containsString(projectCfg.Skills.Disabled, "test-skill") {
		t.Fatalf("expected project local config disabled list to drop test-skill, got %+v", projectCfg.Skills.Disabled)
	}
	ok, out, synthetic, err := app.BuildSkillMentionSyntheticPrompt("$test-skill")
	if err != nil || !ok || !strings.Contains(out, "loaded skill: test-skill") || !strings.Contains(synthetic, "Follow workspace instructions") {
		t.Fatalf("expected enabled skill mention, ok=%v out=%q synthetic=%q err=%v", ok, out, synthetic, err)
	}
}

func TestSetSkillEnabledLocalEnableOverridesSharedDisabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	writeAppSkill(t, filepath.Join(dir, ".whale", "skills", "test-skill"), "test-skill", "Workspace skill.", "# Test Skill\n\nFollow workspace instructions.")
	if err := SaveConfigFile(ProjectConfigPath(dir), FileConfig{
		Skills: FileSkillsConfig{Disabled: []string{"test-skill"}},
	}); err != nil {
		t.Fatalf("save shared project config: %v", err)
	}
	app := &App{sessionID: "sess-1", workspaceRoot: dir, cfg: Config{SkillsDisabled: []string{"test-skill"}}}

	out, err := app.SetSkillEnabled("test-skill", true)
	if err != nil {
		t.Fatalf("enable unexpected err: %v", err)
	}
	if !strings.Contains(out, "enabled skill: test-skill") {
		t.Fatalf("unexpected enable result out=%q", out)
	}
	if containsString(app.cfg.SkillsDisabled, "test-skill") {
		t.Fatalf("expected in-memory disabled list to drop test-skill, got %+v", app.cfg.SkillsDisabled)
	}
	projectLocal, loaded, err := LoadConfigFile(ProjectLocalConfigPath(dir))
	if err != nil || !loaded {
		t.Fatalf("load project local config loaded=%v err=%v", loaded, err)
	}
	if !containsString(projectLocal.Skills.Enabled, "test-skill") {
		t.Fatalf("expected project local config enabled list to include test-skill, got %+v", projectLocal.Skills.Enabled)
	}
	reloaded, err := LoadAndApplyConfig(Config{}, dir)
	if err != nil {
		t.Fatalf("LoadAndApplyConfig: %v", err)
	}
	if containsString(reloaded.SkillsDisabled, "test-skill") {
		t.Fatalf("expected local enable to override shared disabled on reload, got %+v", reloaded.SkillsDisabled)
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
	if !strings.Contains(out, "New session") || !strings.Contains(out, "session:  fresh") {
		t.Fatalf("expected output to contain new session, got: %q", out)
	}
	if !strings.Contains(out, "dropped:  1 message") {
		t.Fatalf("expected output to mention dropped messages, got: %q", out)
	}
	if !strings.Contains(out, "whale resume sess-1") {
		t.Fatalf("expected output to include resume hint, got: %q", out)
	}
}

func TestHandleSlashForkCopiesConversationAndSwitchesSession(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	st, err := store.NewJSONLStore(sessionsDir)
	if err != nil {
		t.Fatalf("store init: %v", err)
	}
	user, err := st.Create(context.Background(), core.Message{SessionID: "sess-1", Role: core.RoleUser, Text: "hello\nfork"})
	if err != nil {
		t.Fatalf("write user: %v", err)
	}
	assistant, err := st.Create(context.Background(), core.Message{
		SessionID: "sess-1",
		Role:      core.RoleAssistant,
		Text:      "done",
		ToolCalls: []core.ToolCall{{ID: "tc-1", Name: "shell_run", Input: `{"cmd":"pwd"}`}},
	})
	if err != nil {
		t.Fatalf("write assistant: %v", err)
	}
	if err := session.SaveModeState(sessionsDir, "sess-1", session.ModePlan); err != nil {
		t.Fatalf("save mode: %v", err)
	}
	if err := session.SaveTodoState(sessionsDir, "sess-1", session.TodoState{Items: []session.TodoItem{{ID: "t1", Text: "todo"}}}); err != nil {
		t.Fatalf("save todo: %v", err)
	}

	app := &App{
		sessionsDir:   sessionsDir,
		workspaceRoot: dir,
		branch:        "feat/fork",
		sessionID:     "sess-1",
		msgStore:      st,
		currentMode:   session.ModePlan,
		ctx:           context.Background(),
	}
	handled, out, synthetic, shouldExit, clearScreen, err := app.HandleSlash("/fork\tCustom")
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	if !handled || synthetic != "" || shouldExit || clearScreen {
		t.Fatalf("unexpected command result handled=%v synthetic=%q shouldExit=%v clearScreen=%v", handled, synthetic, shouldExit, clearScreen)
	}
	forkID := app.SessionID()
	if forkID == "" || forkID == "sess-1" {
		t.Fatalf("expected new fork session id, got %q", forkID)
	}
	if !strings.Contains(out, `Forked conversation "Custom (Branch)"`) || !strings.Contains(out, "To resume the original:") || !strings.Contains(out, "sess-1") {
		t.Fatalf("unexpected fork output: %q", out)
	}

	got, err := st.List(context.Background(), forkID)
	if err != nil {
		t.Fatalf("list fork: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 copied messages, got %+v", got)
	}
	if got[0].SessionID != forkID || got[0].ID != user.ID || got[0].Text != user.Text {
		t.Fatalf("unexpected copied user: %+v source=%+v", got[0], user)
	}
	if got[1].SessionID != forkID || got[1].ID != assistant.ID || len(got[1].ToolCalls) != 1 || got[1].ToolCalls[0].ID != "tc-1" {
		t.Fatalf("unexpected copied assistant: %+v source=%+v", got[1], assistant)
	}
	meta, err := session.LoadSessionMeta(sessionsDir, forkID)
	if err != nil {
		t.Fatalf("load meta: %v", err)
	}
	if meta.Kind != "fork" || meta.ParentSessionID != "sess-1" || meta.Title != "Custom (Branch)" || meta.Branch != "feat/fork" || meta.Workspace != dir {
		t.Fatalf("unexpected fork meta: %+v", meta)
	}
	mode, err := session.LoadModeState(sessionsDir, forkID)
	if err != nil {
		t.Fatalf("load mode: %v", err)
	}
	if mode.Mode != session.ModePlan {
		t.Fatalf("expected fork mode plan, got %+v", mode)
	}
	todo, err := session.LoadTodoState(sessionsDir, forkID)
	if err != nil {
		t.Fatalf("load todo: %v", err)
	}
	if len(todo.Items) != 1 || todo.Items[0].ID != "t1" {
		t.Fatalf("expected copied todo, got %+v", todo)
	}
}

func TestHandleSlashForkDerivesTitleAndAvoidsCollisions(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	st, err := store.NewJSONLStore(sessionsDir)
	if err != nil {
		t.Fatalf("store init: %v", err)
	}
	if _, err := st.Create(context.Background(), core.Message{SessionID: "sess-1", Role: core.RoleUser, Text: "first\nprompt"}); err != nil {
		t.Fatalf("write user: %v", err)
	}
	app := &App{
		sessionsDir:   sessionsDir,
		workspaceRoot: dir,
		sessionID:     "sess-1",
		msgStore:      st,
		ctx:           context.Background(),
	}

	if _, _, _, _, _, err := app.HandleSlash("/fork"); err != nil {
		t.Fatalf("first fork: %v", err)
	}
	firstFork := app.SessionID()
	meta, err := session.LoadSessionMeta(sessionsDir, firstFork)
	if err != nil {
		t.Fatalf("first meta: %v", err)
	}
	if meta.Title != "first prompt (Branch)" {
		t.Fatalf("unexpected first title: %q", meta.Title)
	}

	app.sessionID = "sess-1"
	if _, _, _, _, _, err := app.HandleSlash("/fork"); err != nil {
		t.Fatalf("second fork: %v", err)
	}
	secondFork := app.SessionID()
	meta, err = session.LoadSessionMeta(sessionsDir, secondFork)
	if err != nil {
		t.Fatalf("second meta: %v", err)
	}
	if meta.Title != "first prompt (Branch 2)" {
		t.Fatalf("unexpected second title: %q", meta.Title)
	}
}

func TestHandleSlashForkRequiresConversation(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	st, err := store.NewJSONLStore(sessionsDir)
	if err != nil {
		t.Fatalf("store init: %v", err)
	}
	app := &App{
		sessionsDir:   sessionsDir,
		workspaceRoot: dir,
		sessionID:     "empty",
		msgStore:      st,
		ctx:           context.Background(),
	}
	handled, out, _, _, _, err := app.HandleSlash("/fork")
	if !handled {
		t.Fatal("expected /fork handled")
	}
	if err == nil || !strings.Contains(err.Error(), "no conversation to fork") {
		t.Fatalf("expected empty fork error, got out=%q err=%v", out, err)
	}
	if app.SessionID() != "empty" {
		t.Fatalf("session changed on failed fork: %s", app.SessionID())
	}
}

func newAppGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	runAppGit(t, dir, "init", "-b", "main")
	runAppGit(t, dir, "config", "user.email", "test@example.com")
	runAppGit(t, dir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("test\n"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runAppGit(t, dir, "add", "README.md")
	runAppGit(t, dir, "commit", "-m", "initial")
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve repo: %v", err)
	}
	return resolved
}

func runAppGit(t *testing.T, cwd string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, string(out))
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
