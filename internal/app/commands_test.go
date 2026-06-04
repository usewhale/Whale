package app

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/usewhale/whale/internal/agent"
	appcommands "github.com/usewhale/whale/internal/commands"
	"github.com/usewhale/whale/internal/core"
	whalemcp "github.com/usewhale/whale/internal/mcp"
	"github.com/usewhale/whale/internal/plugins/memoryplugin"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/store"
	"github.com/usewhale/whale/internal/tasks"
	"github.com/usewhale/whale/internal/telemetry"
	"github.com/usewhale/whale/internal/workflow"
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

func TestResolveInitialSessionIDIgnoresRecentSubagentSession(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	parentPath := filepath.Join(sessionsDir, "parent.jsonl")
	childPath := filepath.Join(sessionsDir, "parent--subagent-call-1.jsonl")
	if err := os.WriteFile(parentPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write parent: %v", err)
	}
	if err := os.WriteFile(childPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write child: %v", err)
	}
	if err := session.SaveSessionMeta(sessionsDir, "parent--subagent-call-1", session.SessionMeta{Kind: "subagent", ParentSessionID: "parent"}); err != nil {
		t.Fatalf("save child meta: %v", err)
	}
	now := time.Now()
	_ = os.Chtimes(parentPath, now.Add(-time.Hour), now.Add(-time.Hour))
	_ = os.Chtimes(childPath, now, now)

	got, err := resolveInitialSessionID(sessionsDir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "parent" {
		t.Fatalf("want parent, got %s", got)
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
		{line: "/stats profile", want: appcommands.SubmitLocalReadOnly},
		{line: "/stats all", want: appcommands.SubmitLocalReadOnly},
		{line: "/mcp", want: appcommands.SubmitLocalReadOnly},
		{line: "/hooks", want: appcommands.SubmitLocalReadOnly},
		{line: "/hooks trust all", want: appcommands.SubmitLocalMutating},
		{line: "/feedback", want: appcommands.SubmitLocalReadOnly},
		{line: "/help", want: appcommands.SubmitLocalReadOnly},
		{line: "/copy", want: appcommands.SubmitLocalReadOnly},
		{line: "/copy 2", want: appcommands.SubmitLocalReadOnly},
		{line: "/model", want: appcommands.SubmitLocalUI},
		{line: "/permissions", want: appcommands.SubmitLocalUI},
		{line: "/focus", want: appcommands.SubmitLocalUI},
		{line: "/open", want: appcommands.SubmitLocalUI},
		{line: "/open .", want: appcommands.SubmitLocalUI},
		{line: "/open My Folder/file.txt", want: appcommands.SubmitLocalUI},
		{line: "/skills", want: appcommands.SubmitLocalUI},
		{line: "/config", want: appcommands.SubmitLocalUI},
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
		{line: "/hooks trust", want: appcommands.SubmitUsageError},
		{line: "/hooks bad", want: appcommands.SubmitUsageError},
		{line: "/feedback now", want: appcommands.SubmitUsageError},
		{line: "/help now", want: appcommands.SubmitUsageError},
		{line: "/copy 0", want: appcommands.SubmitUsageError},
		{line: "/copy latest", want: appcommands.SubmitUsageError},
		{line: "/copy 1 extra", want: appcommands.SubmitUsageError},
		{line: "/worktree", want: appcommands.SubmitUsageError},
		{line: "/worktree list", want: appcommands.SubmitUsageError},
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
	if !strings.Contains(CommandsHelp, "/copy [N]") {
		t.Fatalf("expected /copy to advertise optional index: %s", CommandsHelp)
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

	for _, old := range []string{"/step", "/continue", "/stop", "/revise add retry", "/context", "/memory"} {
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
		TS:                       time.Date(2026, 5, 12, 10, 0, 0, 0, time.Local).UnixMilli(),
		Session:                  "s1",
		Model:                    "deepseek-v4-flash",
		PrefixFingerprint:        "fp-1",
		PromptTokens:             1000,
		CompletionTokens:         200,
		PromptCacheHit:           800,
		PromptCacheMiss:          200,
		PrefixCompletionRequests: 1,
		ReasoningReplayTok:       250,
		ToolResultRawChars:       13000,
		ToolResultReplayChars:    4000,
		ToolResultRawTokens:      3250,
		ToolResultReplayTokens:   1000,
		ToolResultTokensSaved:    2250,
		ToolResultsCompacted:     1,
		CacheShape: &telemetry.CacheShape{
			PrefixHash:  "prefix-a",
			SystemHash:  "system-a",
			RuntimeHash: "runtime-a",
			ToolsHash:   "tools-a",
			RequestHash: "request-a",
			RuntimeSegments: []telemetry.CacheShapeSegment{{
				Name:      "project_memory",
				Stability: "dynamic",
				Hash:      "runtime-project-a",
				Bytes:     10,
			}},
		},
		CostUSD: 0.0123,
	})
	writeUsageRecord(t, filepath.Join(dir, "usage.jsonl"), telemetry.UsageRecord{
		TS:                 time.Date(2026, 5, 12, 10, 3, 0, 0, time.Local).UnixMilli(),
		Session:            "s2",
		Model:              "deepseek-v4-flash",
		PrefixFingerprint:  "fp-2",
		PromptTokens:       2000,
		CompletionTokens:   300,
		PromptCacheHit:     1000,
		PromptCacheMiss:    1000,
		ReasoningReplayTok: 100,
		CacheShape: &telemetry.CacheShape{
			PrefixHash:  "prefix-b",
			SystemHash:  "system-a",
			RuntimeHash: "runtime-b",
			ToolsHash:   "tools-a",
			RequestHash: "request-b",
			RuntimeSegments: []telemetry.CacheShapeSegment{{
				Name:      "project_memory",
				Stability: "dynamic",
				Hash:      "runtime-project-b",
				Bytes:     10,
			}},
		},
		CostUSD: 0.0456,
	})
	writeUsageRecord(t, filepath.Join(dir, "usage.jsonl"), telemetry.UsageRecord{
		TS:               time.Date(2026, 5, 12, 10, 4, 0, 0, time.Local).UnixMilli(),
		Session:          "legacy-chat",
		Model:            "deepseek-chat",
		PromptTokens:     100_000,
		CompletionTokens: 10_000,
		CostUSD:          99,
	})
	writeSessionMessages(t, sessionsDir, "s1", []core.Message{
		{ID: "m-1", SessionID: "s1", Role: core.RoleUser, Text: "please inspect the workspace"},
		{ID: "m-2", SessionID: "s1", Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "read_file", Input: `{"path":"big.go"}`}}},
		{ID: "m-3", SessionID: "s1", Role: core.RoleTool, ToolResults: []core.ToolResult{{ToolCallID: "c1", Name: "read_file", Content: strings.Repeat("x", 13000)}}},
		{ID: "m-4", SessionID: "s1", Role: core.RoleAssistant, Text: "done", Reasoning: "thinking"},
	})
	writeSessionMessages(t, sessionsDir, "s2", []core.Message{
		{ID: "m-1", SessionID: "s2", Role: core.RoleUser, Text: "hi"},
		{ID: "m-2", SessionID: "s2", Role: core.RoleAssistant, Text: "hello world", FinishReason: core.FinishReasonEndTurn},
	})
	writeSessionMessages(t, sessionsDir, "s-local", []core.Message{
		{ID: "m-1", SessionID: "s-local", Role: core.RoleUser, Text: "/stats profile"},
	})
	writeSessionMessages(t, sessionsDir, "s3--subagent-worker", []core.Message{
		{ID: "m-1", SessionID: "s3--subagent-worker", Role: core.RoleUser, Text: "subagent"},
	})
	writeSessionMessages(t, sessionsDir, "s4", []core.Message{
		{ID: "m-1", SessionID: "s4", Role: core.RoleUser, Text: "metadata subagent"},
	})
	if err := session.SaveSessionMeta(sessionsDir, "s4", session.SessionMeta{Kind: "subagent", ParentSessionID: "s1"}); err != nil {
		t.Fatalf("save subagent meta: %v", err)
	}
	writeSessionMessages(t, sessionsDir, "e2e-fixture", []core.Message{
		{ID: "m-1", SessionID: "e2e-fixture", Role: core.RoleUser, Text: "fixture"},
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
		"- turns: 2",
		"- tokens: 3.5K total",
		"- reasoning replay: 350 tokens",
		"- Prefix completion: 1 requests",
		"- estimated cost: $0.0003 total",
		"- top model: deepseek-v4-flash · 2 turns",
		"Tool input",
		"- repaired: 1",
		"- invalid: 1",
		"- repair rate: 50.0%",
		"- top repair: markdown_autolink_path · 1",
		"- top invalid tool: write · 1",
		"More: /stats usage, /stats cache, /stats tools, /stats repair, /stats recent, /stats profile, /stats all",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected stats to contain %q, got:\n%s", want, out)
		}
	}
	for _, dontWant := range []string{
		"Recent tool-input events",
		"Invalid codes",
		"Top tools",
		"deepseek-chat",
		"legacy-chat",
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
		"- sessions: 2",
		"- reasoning replay: 350 tokens · 11.7% of input",
		"- Prefix completion: 1 requests",
		"By window",
		"all-time: 2 turns",
		"Prefix completion 1",
		"cache saved",
		"deepseek-v4-flash: 2 turns",
		"350 reasoning replay",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected usage stats to contain %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Tool input") {
		t.Fatalf("expected usage stats to omit tool input section:\n%s", out)
	}

	handled, out, _, err = a.HandleLocalCommand("/stats profile")
	if err != nil || !handled {
		t.Fatalf("stats profile command handled=%v err=%v", handled, err)
	}
	for _, want := range []string{
		"Profile",
		"- scanned sessions: 3 latest main sessions (limit 50)",
		"- main work sessions: 1",
		"- trivial/local sessions: 2",
		"- tool-heavy sessions: 1",
		"- usage matched sessions: 2",
		"- max prompt: 2K",
		"- reasoning replay: 350 main · 0 subagent · 350 all-in",
		"- tool replay: 1K sent · 3.2K raw · 2.2K saved · 1 compacted",
		"- prefix fingerprints: 2",
		"- provider prefixes: 2 distinct across 2 usage sessions",
		"- tools: 1 calls · 13K result chars",
		"- reasoning/text: 8 reasoning chars",
		"Top tools",
		"read_file: 1 calls · 13K result chars",
		"Top tool replay sessions",
		"s1: 1K sent · 3.2K raw · 2.2K saved · 1 compacted",
		"Top reasoning replay sessions",
		"s1: 250 tokens",
		"Insights",
		"reasoning replay · s1",
		"Top work sessions",
		"s1: $0.0001",
		"please inspect the workspace",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected profile stats to contain %q, got:\n%s", want, out)
		}
	}
	for _, dontWant := range []string{
		`{"path":"big.go"}`,
		strings.Repeat("x", 200),
		"s2: $0.0456",
		"s-local",
		"subagent-worker",
		"metadata subagent",
		"e2e-fixture",
	} {
		if strings.Contains(out, dontWant) {
			t.Fatalf("expected profile stats to omit %q, got:\n%s", dontWant, out)
		}
	}
	profileLocal := a.buildStatsLocalResultAt("profile", time.Date(2026, 5, 12, 10, 5, 0, 0, time.Local))
	for _, want := range []struct {
		section string
		field   string
	}{
		{"Insights", "reasoning replay · s1"},
		{"Profile", "Provider prefixes"},
		{"Profile", "Reasoning replay"},
		{"Profile", "Tool replay"},
		{"Top tool replay sessions", "s1"},
		{"Top reasoning replay sessions", "s1"},
	} {
		if !localResultHasSectionField(profileLocal, want.section, want.field) {
			t.Fatalf("expected stats profile local result section %q field %q, got %+v", want.section, want.field, profileLocal.Sections)
		}
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

	handled, out, _, err = a.HandleLocalCommand("/stats cache")
	if err != nil || !handled {
		t.Fatalf("stats cache command handled=%v err=%v", handled, err)
	}
	if !strings.Contains(out, "Cache diagnostics") {
		t.Fatalf("expected cache stats to contain diagnostics, got:\n%s", out)
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
	if strings.Contains(out, "Profile") {
		t.Fatalf("expected all stats to omit profile section:\n%s", out)
	}

	handled, _, _, err = a.HandleLocalCommand("/stats extra")
	if !handled || err == nil || !strings.Contains(err.Error(), "usage: /stats [usage|cache|tools|repair|recent|profile|all]") {
		t.Fatalf("expected /stats usage error, handled=%v err=%v", handled, err)
	}
}

func TestProfilePrefixChurnDetailNamesSourcesAndSegments(t *testing.T) {
	sp := profileSessionStats{
		ProviderPrefixHashes: map[string]bool{"p1": true, "p2": true},
		SystemHashes:         map[string]bool{"s": true},
		RuntimeHashes:        map[string]bool{"r1": true, "r2": true},
		ToolsHashes:          map[string]bool{"t1": true, "t2": true},
		ShapeSegments: map[string]map[string]bool{
			"runtime:project_memory": {"a": true, "b": true},
			"tool:shell_run":         {"a": true, "b": true},
		},
	}

	got := profilePrefixChurnDetail(sp)
	for _, want := range []string{
		"2 provider prefixes",
		"runtime drift",
		"tool drift",
		"runtime:project_memory",
		"tool:shell_run",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected churn detail to contain %q, got %q", want, got)
		}
	}
	if strings.Contains(got, "system drift") {
		t.Fatalf("unexpected system drift in %q", got)
	}
}

func TestProfileSessionHiddenTaskDoesNotPreviewGreeting(t *testing.T) {
	dir := t.TempDir()
	sessionID := "s-hidden-task"
	writeSessionMessages(t, dir, sessionID, []core.Message{
		{ID: "m-1", SessionID: sessionID, Role: core.RoleUser, Text: "hi"},
		{ID: "m-2", SessionID: sessionID, Role: core.RoleAssistant, Text: "hello", FinishReason: core.FinishReasonEndTurn},
		{ID: "m-3", SessionID: sessionID, Role: core.RoleUser, Text: "review the local changes", Hidden: true},
		{ID: "m-4", SessionID: sessionID, Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "shell_run"}}},
	})

	got := readProfileSessionFile(filepath.Join(dir, sessionID+".jsonl"), sessionID, time.Time{})
	if got.Trivial {
		t.Fatalf("hidden work task should not be classified as trivial/local: %+v", got)
	}
	if got.FirstUserText != "(hidden user task)" {
		t.Fatalf("hidden work task should not preview greeting, got %q", got.FirstUserText)
	}
}

func TestProfileStatsIncludesSubagentUsage(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	usagePath := filepath.Join(dir, "usage.jsonl")
	parentID := "parent"
	namedChildID := parentID + "--subagent-call_00"
	metaChildID := "metadata-child"

	writeSessionMessages(t, sessionsDir, parentID, []core.Message{
		{ID: "m-1", SessionID: parentID, Role: core.RoleUser, Text: "inspect the repository"},
		{ID: "m-2", SessionID: parentID, Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "spawn_subagent"}}},
	})
	writeSessionMessages(t, sessionsDir, namedChildID, []core.Message{
		{ID: "m-1", SessionID: namedChildID, Role: core.RoleUser, Text: "child by name"},
	})
	writeSessionMessages(t, sessionsDir, metaChildID, []core.Message{
		{ID: "m-1", SessionID: metaChildID, Role: core.RoleUser, Text: "child by metadata"},
	})
	if err := session.SaveSessionMeta(sessionsDir, metaChildID, session.SessionMeta{Kind: "subagent", ParentSessionID: parentID}); err != nil {
		t.Fatalf("save child meta: %v", err)
	}
	writeUsageRecord(t, usagePath, telemetry.UsageRecord{
		Session:                parentID,
		Model:                  "deepseek-v4-flash",
		PromptTokens:           1000,
		CompletionTokens:       100,
		PromptCacheHit:         800,
		PromptCacheMiss:        200,
		ReasoningReplayTok:     120,
		ToolResultRawChars:     1000,
		ToolResultReplayChars:  600,
		ToolResultRawTokens:    250,
		ToolResultReplayTokens: 150,
		ToolResultTokensSaved:  100,
		CostUSD:                0.0100,
	})
	writeUsageRecord(t, usagePath, telemetry.UsageRecord{
		Session:                namedChildID,
		Model:                  "deepseek-v4-flash",
		PromptTokens:           4000,
		CompletionTokens:       500,
		PromptCacheHit:         3000,
		PromptCacheMiss:        1000,
		ReasoningReplayTok:     300,
		ToolResultRawChars:     8000,
		ToolResultReplayChars:  2000,
		ToolResultRawTokens:    2000,
		ToolResultReplayTokens: 500,
		ToolResultTokensSaved:  1500,
		ToolResultsCompacted:   1,
		CostUSD:                0.0200,
	})
	writeUsageRecord(t, usagePath, telemetry.UsageRecord{
		Session:                metaChildID,
		Model:                  "deepseek-v4-flash",
		Kind:                   "subagent",
		ParentSessionID:        parentID,
		SubagentRole:           "reviewer",
		PromptTokens:           1000,
		CompletionTokens:       100,
		PromptCacheHit:         900,
		PromptCacheMiss:        100,
		ReasoningReplayTok:     200,
		ToolResultRawChars:     4000,
		ToolResultReplayChars:  1000,
		ToolResultRawTokens:    1000,
		ToolResultReplayTokens: 250,
		ToolResultTokensSaved:  750,
		ToolResultsCompacted:   1,
		CostUSD:                0.0300,
	})

	stats := readProfileStats(sessionsDir, usagePath, 50)
	if len(stats.Sessions) != 1 {
		t.Fatalf("expected only parent main session, got %d sessions: %+v", len(stats.Sessions), stats.Sessions)
	}
	if stats.SubagentSessions != 2 || stats.SubagentPromptTokens != 5000 || stats.SubagentCompletionTokens != 600 || math.Abs(stats.SubagentCostUSD-0.00033292) > 0.000001 || stats.SubagentMaxPromptTokens != 4000 {
		t.Fatalf("unexpected subagent totals: %+v", stats)
	}
	if stats.Sessions[0].SubagentSessions != 2 || stats.Sessions[0].SubagentPromptTokens != 5000 || stats.Sessions[0].SubagentCompletionTokens != 600 || math.Abs(stats.Sessions[0].SubagentCostUSD-0.00033292) > 0.000001 {
		t.Fatalf("unexpected parent subagent totals: %+v", stats.Sessions[0])
	}
	if stats.ReasoningReplayTokens != 120 || stats.SubagentReasoningReplay != 500 || stats.Sessions[0].ReasoningReplayTokens != 120 || stats.Sessions[0].SubagentReasoningReplay != 500 {
		t.Fatalf("unexpected reasoning replay totals: %+v", stats)
	}
	if stats.ToolResultReplayTokens != 150 || stats.SubagentToolResultReplayTokens != 750 || stats.ToolResultTokensSaved != 100 || stats.SubagentToolResultTokensSaved != 2250 || stats.Sessions[0].SubagentToolResultsCompacted != 2 {
		t.Fatalf("unexpected tool replay totals: %+v", stats)
	}

	out := strings.Join(formatProfileStats(stats), "\n")
	for _, want := range []string{
		"- tokens: 1.1K total · 1K input · 100 output",
		"- subagents: 2 child sessions · 5.6K total · 5K input · 600 output · $0.0003 · max prompt 4K · 78.0% cache",
		"- all-in tokens: 6.7K total · $0.0004",
		"- reasoning replay: 120 main · 500 subagent · 620 all-in",
		"- tool replay: 900 sent · 3.2K raw · 2.4K saved · 2 compacted",
		"Top tool replay sessions",
		"parent: 900 sent · 3.2K raw · 2.4K saved · 2 compacted",
		"Top reasoning replay sessions",
		"parent: 620 tokens · +500 subagents · 10.3% of input",
		"parent: $0.0001 · subagents 2 · +$0.0003 · +5.6K tokens",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected profile output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestUsageStatsCountsLegacySubagentSessionIDs(t *testing.T) {
	dir := t.TempDir()
	usagePath := filepath.Join(dir, "usage.jsonl")
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.Local)
	writeUsageRecord(t, usagePath, telemetry.UsageRecord{
		TS:               now.UnixMilli(),
		Session:          "parent",
		Model:            "deepseek-v4-flash",
		PromptTokens:     1000,
		CompletionTokens: 100,
		PromptCacheHit:   800,
		PromptCacheMiss:  200,
	})
	writeUsageRecord(t, usagePath, telemetry.UsageRecord{
		TS:               now.UnixMilli(),
		Session:          "parent--subagent-worker",
		Model:            "deepseek-v4-flash",
		PromptTokens:     2000,
		CompletionTokens: 200,
		PromptCacheHit:   1500,
		PromptCacheMiss:  500,
	})
	writeUsageRecord(t, usagePath, telemetry.UsageRecord{
		TS:               now.UnixMilli(),
		Session:          "subagent-researcher",
		Model:            "deepseek-v4-flash",
		PromptTokens:     3000,
		CompletionTokens: 300,
		PromptCacheHit:   2500,
		PromptCacheMiss:  500,
	})
	writeUsageRecord(t, usagePath, telemetry.UsageRecord{
		TS:               now.UnixMilli(),
		Session:          "metadata-child",
		Model:            "deepseek-v4-flash",
		Kind:             "subagent",
		PromptTokens:     4000,
		CompletionTokens: 400,
		PromptCacheHit:   3500,
		PromptCacheMiss:  500,
	})

	stats := readUsageStats(usagePath, now)
	if stats.SubagentTurns != 3 || stats.SubagentPromptTokens != 9000 || stats.SubagentOutputTokens != 900 {
		t.Fatalf("unexpected subagent usage totals: %+v", stats)
	}
	for _, bucket := range stats.Buckets {
		if bucket.Label == "24h" || bucket.Label == "all-time" {
			if bucket.SubagentTurns != 3 || bucket.SubagentTokens != 9900 {
				t.Fatalf("unexpected subagent bucket %s: %+v", bucket.Label, bucket)
			}
		}
	}
}

func TestTopReasoningReplaySessionsIncludesSubagentOnlyReplay(t *testing.T) {
	sessions := []profileSessionStats{
		{ID: "main-500", ReasoningReplayTokens: 500, CostUSD: 0.50},
		{ID: "main-400", ReasoningReplayTokens: 400, CostUSD: 0.40},
		{ID: "main-300", ReasoningReplayTokens: 300, CostUSD: 0.30},
		{ID: "main-200", ReasoningReplayTokens: 200, CostUSD: 0.20},
		{ID: "main-100", ReasoningReplayTokens: 100, CostUSD: 0.10},
		{ID: "subagent-only", SubagentReasoningReplay: 600, SubagentCostUSD: 0.01},
		{ID: "trivial-subagent", Trivial: true, SubagentReasoningReplay: 700},
	}

	top := topReasoningReplaySessions(sessions, 5)
	if len(top) != 5 {
		t.Fatalf("expected 5 top sessions, got %d: %+v", len(top), top)
	}
	if top[0].ID != "subagent-only" {
		t.Fatalf("expected subagent-only replay session to rank first, got %+v", top)
	}
	for _, sp := range top {
		if sp.ID == "main-100" || sp.ID == "trivial-subagent" {
			t.Fatalf("unexpected session in top replay list: %+v", top)
		}
	}
}

func TestFormatProfileStatsUsesAllInTopReasoningReplayRows(t *testing.T) {
	stats := profileStats{
		Limit:                   5,
		Sessions:                []profileSessionStats{{ID: "subagent-only", SubagentPromptTokens: 2000, SubagentReasoningReplay: 600, FirstUserText: "delegate analysis"}},
		SubagentPromptTokens:    2000,
		SubagentReasoningReplay: 600,
		PrefixFingerprints:      map[string]bool{},
		ByTool:                  map[string]*profileToolStats{},
	}

	out := strings.Join(formatProfileStats(stats), "\n")
	want := "subagent-only: 600 tokens · +600 subagents · 30.0% of input"
	if !strings.Contains(out, want) {
		t.Fatalf("expected top replay row to use all-in replay values %q, got:\n%s", want, out)
	}
	if strings.Contains(out, "subagent-only: 0 tokens") {
		t.Fatalf("top replay row should not show parent-only replay tokens:\n%s", out)
	}
}

func TestProfileStatsIncludesSubagentUsageFromDefaultLogForCustomDataDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dir, "home"))
	sessionsDir := filepath.Join(dir, "custom", "sessions")
	usagePath := filepath.Join(dir, "custom", "usage.jsonl")
	parentID := "parent-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	childID := parentID + "--subagent-call_00"

	writeSessionMessages(t, sessionsDir, parentID, []core.Message{
		{ID: "m-1", SessionID: parentID, Role: core.RoleUser, Text: "inspect the repository"},
		{ID: "m-2", SessionID: parentID, Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "spawn_subagent"}}},
	})
	writeSessionMessages(t, sessionsDir, childID, []core.Message{
		{ID: "m-1", SessionID: childID, Role: core.RoleUser, Text: "child by name"},
	})
	writeUsageRecord(t, usagePath, telemetry.UsageRecord{
		Session:          parentID,
		Model:            "deepseek-v4-flash",
		PromptTokens:     100,
		CompletionTokens: 10,
		CostUSD:          0.0010,
	})
	writeUsageRecord(t, telemetry.DefaultUsageLogPath(), telemetry.UsageRecord{
		Session:          childID,
		Model:            "deepseek-v4-flash",
		PromptTokens:     200,
		CompletionTokens: 20,
		CostUSD:          0.0020,
	})

	stats := readProfileStats(sessionsDir, usagePath, 50)
	if len(stats.Sessions) != 1 {
		t.Fatalf("expected only parent main session, got %d sessions: %+v", len(stats.Sessions), stats.Sessions)
	}
	if stats.PromptTokens != 100 || stats.CompletionTokens != 10 || math.Abs(stats.CostUSD-0.0000168) > 0.000001 {
		t.Fatalf("unexpected parent usage totals: %+v", stats)
	}
	if stats.SubagentSessions != 1 || stats.SubagentPromptTokens != 200 || stats.SubagentCompletionTokens != 20 || math.Abs(stats.SubagentCostUSD-0.0000336) > 0.000001 {
		t.Fatalf("unexpected subagent totals from default log: %+v", stats)
	}
	if stats.Sessions[0].SubagentSessions != 1 || stats.Sessions[0].SubagentPromptTokens != 200 || stats.Sessions[0].SubagentCompletionTokens != 20 || math.Abs(stats.Sessions[0].SubagentCostUSD-0.0000336) > 0.000001 {
		t.Fatalf("unexpected parent subagent totals from default log: %+v", stats.Sessions[0])
	}
}

func TestProfileStatsSeparatesApprovalPromptsFromAuditEvents(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	usagePath := filepath.Join(dir, "usage.jsonl")
	parentID := "approval-parent"
	childID := parentID + "--subagent-worker"

	writeSessionMessages(t, sessionsDir, parentID, []core.Message{
		{ID: "m-1", SessionID: parentID, Role: core.RoleUser, Text: "inspect approvals"},
		{ID: "m-2", SessionID: parentID, Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "tc-1", Name: "shell_run"}}},
	})
	writeSessionMessages(t, sessionsDir, childID, []core.Message{
		{ID: "m-1", SessionID: childID, Role: core.RoleUser, Text: "child"},
	})
	for i := 0; i < 5; i++ {
		writeApprovalEvent(t, sessionsDir, parentID, "approval_required")
	}
	writeApprovalEventForToolCall(t, sessionsDir, parentID, "tc-dedupe", "approval_required", "agent")
	writeApprovalEventForToolCall(t, sessionsDir, parentID, "tc-dedupe", "approval_prompt_shown", "service")
	writeApprovalEvent(t, sessionsDir, parentID, "approval_allowed_once")
	writeApprovalEventForToolCall(t, sessionsDir, parentID, "tc-dedupe", "approval_prompt_allowed_once", "service")
	writeApprovalEventForToolCall(t, sessionsDir, parentID, "tc-dedupe", "approval_allowed_once", "agent")
	writeApprovalEvent(t, sessionsDir, parentID, "approval_allowed_for_session")
	writeApprovalEvent(t, sessionsDir, parentID, "approval_grant_persisted")
	writeApprovalEvent(t, sessionsDir, parentID, "approval_denied")
	writeApprovalEvent(t, sessionsDir, parentID, "approval_canceled")
	for i := 0; i < 31; i++ {
		writeApprovalEvent(t, sessionsDir, parentID, "approval_cached_allowed")
	}
	writeApprovalEvent(t, sessionsDir, childID, "approval_policy_denied")
	writeApprovalEvent(t, sessionsDir, childID, "approval_mode_blocked")

	stats := readProfileStats(sessionsDir, usagePath, 50)
	if len(stats.Sessions) != 1 {
		t.Fatalf("expected one main session, got %+v", stats.Sessions)
	}
	if stats.ApprovalPrompts != 6 || stats.ApprovalReused != 31 || stats.ApprovalAuditEvents != 47 {
		t.Fatalf("approval prompt/audit totals were not separated: %+v", stats)
	}
	if stats.ApprovalAllowedOnce != 2 || stats.ApprovalAllowedForSession != 1 || stats.ApprovalDenied != 1 || stats.ApprovalCanceled != 1 || stats.ApprovalPolicyBlocks != 1 || stats.ApprovalModeBlocks != 1 {
		t.Fatalf("unexpected approval decision/block totals: %+v", stats)
	}

	out := strings.Join(formatProfileStats(stats), "\n")
	for _, want := range []string{
		"- approvals: 6 prompts · 2 allow-once · 1 allow-session · 1 denied · 1 canceled · 31 reused/cached · 2 policy/mode blocks · 47 audit events",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected profile output to contain %q, got:\n%s", want, out)
		}
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
		"/stats [usage|cache|tools|repair|recent|profile|all]",
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
		cfg:           Config{DataDir: dir},
	}
	writeUsageRecord(t, filepath.Join(dir, "usage.jsonl"), telemetry.UsageRecord{
		Session:          "sess-1",
		Model:            "deepseek-v4-flash",
		PromptTokens:     1000,
		CompletionTokens: 100,
		PromptCacheHit:   800,
		PromptCacheMiss:  200,
	})

	out := app.buildStatus()
	for _, want := range []string{
		"- context window:",
		"- usage: 1 turns",
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

func TestBuildStatusLocalResultIncludesStructuredFields(t *testing.T) {
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
		model:            "deepseek-v4-pro",
		reasoningEffort:  "max",
		thinkingEnabled:  false,
		budgetWarningUSD: 0,
		cfg:              Config{DataDir: dir},
	}
	writeUsageRecord(t, filepath.Join(dir, "usage.jsonl"), telemetry.UsageRecord{
		Session:          "sess-1",
		Model:            "deepseek-v4-flash",
		PromptTokens:     1000,
		CompletionTokens: 100,
		PromptCacheHit:   800,
		PromptCacheMiss:  200,
	})

	result := app.buildStatusLocalResult()
	if result == nil || result.Kind != "status" || result.Title != "Status" {
		t.Fatalf("unexpected status local result: %+v", result)
	}
	for _, want := range []string{"Session", "Mode", "Auto-accept", "Model", "Effort", "Thinking", "Context window", "Usage", "Budget limit"} {
		if !localResultHasField(result, want) {
			t.Fatalf("expected local result field %q, got %+v", want, result.Fields)
		}
	}
	if !strings.Contains(result.PlainText, "- session: sess-1") {
		t.Fatalf("expected plain text fallback, got:\n%s", result.PlainText)
	}
}

func TestExecuteSlashStatusReturnsStructuredLocalResult(t *testing.T) {
	dir := t.TempDir()
	msgStore, err := store.NewJSONLStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatalf("store init: %v", err)
	}
	app := &App{
		ctx:             context.Background(),
		workspaceRoot:   dir,
		sessionID:       "sess-1",
		msgStore:        msgStore,
		contextWindow:   1000,
		currentMode:     "agent",
		model:           "deepseek-v4-pro",
		reasoningEffort: "max",
		cfg:             DefaultConfig(),
	}

	out, err := app.ExecuteSlash("/status")
	if err != nil {
		t.Fatalf("execute /status: %v", err)
	}
	if !out.Handled || out.LocalResult == nil || out.LocalResult.Kind != "status" {
		t.Fatalf("expected structured status result, got %+v", out)
	}
	if out.Text == "" || out.Text != out.LocalResult.PlainText {
		t.Fatalf("expected text fallback to match plain result, text=%q local=%q", out.Text, out.LocalResult.PlainText)
	}
}

func TestBuildMCPLocalResultIncludesStructuredFields(t *testing.T) {
	mgr := whalemcp.NewManager(whalemcp.Config{
		Path: "/tmp/mcp.json",
		Servers: map[string]whalemcp.ServerConfig{
			"disabled-fs": {Disabled: true},
		},
	})
	mgr.Initialize(t.Context())
	app := &App{mcpManager: mgr}

	result := app.buildMCPLocalResult()
	if result == nil || result.Kind != "mcp" || result.Title != "MCP Tools" {
		t.Fatalf("unexpected mcp local result: %+v", result)
	}
	if result.PlainText == "" || !strings.Contains(result.PlainText, "disabled-fs") {
		t.Fatalf("expected plain text fallback with server, got:\n%s", result.PlainText)
	}
	for _, want := range []string{"Config", "Servers"} {
		if !localResultHasField(result, want) {
			t.Fatalf("expected local result field %q, got %+v", want, result.Fields)
		}
	}
	if len(result.Sections) != 1 || result.Sections[0].Title != "disabled-fs" {
		t.Fatalf("expected disabled-fs section, got %+v", result.Sections)
	}
	for _, want := range []string{"Status", "Auth", "Tools"} {
		if !localResultSectionHasField(result.Sections[0], want) {
			t.Fatalf("expected mcp section field %q, got %+v", want, result.Sections[0].Fields)
		}
	}
}

func TestExecuteSlashDoctorReturnsStructuredLocalResult(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	msgStore, err := store.NewJSONLStore(sessionsDir)
	if err != nil {
		t.Fatalf("store init: %v", err)
	}
	// Write a session file so countSessions returns >0
	sanitized := core.SanitizeSessionID("sess-1")
	if err := os.WriteFile(filepath.Join(sessionsDir, sanitized+".jsonl"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	app := &App{
		ctx:           context.Background(),
		sessionsDir:   sessionsDir,
		workspaceRoot: dir,
		sessionID:     "sess-1",
		msgStore:      msgStore,
		cfg:           DefaultConfig(),
	}

	out, err := app.ExecuteLocalCommand("/doctor")
	if err != nil {
		t.Fatalf("execute /doctor: %v", err)
	}
	if !out.Handled || out.LocalResult == nil || out.LocalResult.Kind != "doctor" {
		t.Fatalf("expected structured doctor result, got %+v", out)
	}
	if out.Text == "" || out.Text != out.LocalResult.PlainText {
		t.Fatalf("expected text fallback to match plain result, text=%q local=%q", out.Text, out.LocalResult.PlainText)
	}
	// Verify session storage fields
	for _, want := range []string{"Directory", "Session ID", "Total sessions"} {
		if !localResultHasField(out.LocalResult, want) {
			t.Fatalf("expected local result field %q, got %+v", want, out.LocalResult.Fields)
		}
	}
	// Verify sections exist
	if len(out.LocalResult.Sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(out.LocalResult.Sections))
	}
	if out.LocalResult.Sections[0].Title != "Session Files" {
		t.Fatalf("expected section[0] title %q, got %q", "Session Files", out.LocalResult.Sections[0].Title)
	}
	if out.LocalResult.Sections[1].Title != "Diagnostics" {
		t.Fatalf("expected section[1] title %q, got %q", "Diagnostics", out.LocalResult.Sections[1].Title)
	}
	// Verify plain text contains key info
	for _, want := range []string{"Session Storage", "Session ID:", sessionsDir, "Diagnostics"} {
		if !strings.Contains(out.Text, want) {
			t.Fatalf("expected plain text to contain %q, got:\n%s", want, out.Text)
		}
	}
}

func TestBuildMCPLocalResultIncludesCommandHeadersAndToolNames(t *testing.T) {
	mgr := whalemcp.NewManager(whalemcp.Config{
		Path: "/tmp/mcp.json",
		Servers: map[string]whalemcp.ServerConfig{
			"fs": {
				Command: "npx",
				Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
			},
			"stitch": {
				URL: "https://stitch.googleapis.com/mcp",
				Headers: map[string]string{
					"Authorization":  "Bearer ${STITCH_TOKEN}",
					"X-Goog-Api-Key": "${STITCH_KEY}",
				},
			},
		},
	})
	app := &App{mcpManager: mgr}

	result := app.buildMCPLocalResult()
	if got := localResultSectionFieldValue(result, "fs", "Command"); got != "npx -y @modelcontextprotocol/server-filesystem /tmp" {
		t.Fatalf("unexpected fs command: %q", got)
	}
	if got := localResultSectionFieldValue(result, "fs", "Tools"); got != "(none)" {
		t.Fatalf("unexpected fs tools before connection: %q", got)
	}
	if got := localResultSectionFieldValue(result, "stitch", "Auth"); got != "Bearer token" {
		t.Fatalf("unexpected stitch auth: %q", got)
	}
	if got := localResultSectionFieldValue(result, "stitch", "HTTP headers"); got != "Authorization=*****, X-Goog-Api-Key=*****" {
		t.Fatalf("unexpected stitch headers: %q", got)
	}
}

func TestExecuteLocalMCPReturnsStructuredLocalResult(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	out, err := app.ExecuteLocalCommand("/mcp")
	if err != nil {
		t.Fatalf("ExecuteLocalCommand: %v", err)
	}
	if !out.Handled || out.LocalResult == nil || out.LocalResult.Kind != "mcp" {
		t.Fatalf("expected structured mcp result, got %+v", out)
	}
	if out.Text == "" || out.Text != out.LocalResult.PlainText {
		t.Fatalf("expected text fallback to match plain result, text=%q local=%q", out.Text, out.LocalResult.PlainText)
	}
}

func TestBuildWorkflowsLocalResultIncludesRecentRuns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.WorkflowsEnabled = true
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-other", Type: workflow.EventRunStarted, Status: workflow.RunStatusRunning, Message: "other session", SessionID: "other-session"})
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-one", Type: workflow.EventRunStarted, Status: workflow.RunStatusRunning, Message: "audit repo", SessionID: app.SessionID()})
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-one", Type: workflow.EventPhaseStarted, Status: workflow.RunStatusRunning, Phase: "Explore", Message: "Explore"})
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-one", TaskID: "task-a", Type: workflow.EventTaskStarted, Status: workflow.TaskStatusRunning, Label: "scan"})

	result := app.buildWorkflowsLocalResult("")
	if result == nil || result.Kind != "workflows" || result.Title != "Workflows" {
		t.Fatalf("unexpected workflows local result: %+v", result)
	}
	if !strings.Contains(result.PlainText, "run-one") || !strings.Contains(result.PlainText, "phase: Explore") {
		t.Fatalf("expected workflow run in plain text, got:\n%s", result.PlainText)
	}
	if strings.Contains(result.PlainText, "run-other") || localResultSectionFieldValue(result, "run-other", "Status") != "" {
		t.Fatalf("workflow list should be scoped to current session, got:\n%s", result.PlainText)
	}
	if got := localResultSectionFieldValue(result, "run-one", "Status"); got != workflow.RunStatusRunning {
		t.Fatalf("unexpected run status field: %q", got)
	}
	if got := localResultSectionFieldValue(result, "run-one", "Phase"); got != "Explore" {
		t.Fatalf("unexpected phase field: %q", got)
	}
}

func TestBuildWorkflowsLocalResultIncludesAvailableWorkflows(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	workflowDir := filepath.Join(dir, ".whale", "workflows")
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "project-review.js"), []byte(`export const meta = {
  name: 'project-review',
  description: 'Review project code',
  whenToUse: 'when code changed',
}
log('ok')
`), 0o600); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(cwd)
	})
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.WorkflowsEnabled = true
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	result := app.buildWorkflowsLocalResult("")
	if result == nil || result.Kind != "workflows" {
		t.Fatalf("unexpected workflows local result: %+v", result)
	}
	if got := localResultFieldValue(result, "Available"); got != "2 ready" {
		t.Fatalf("available field = %q", got)
	}
	if got := localResultSectionFieldValue(result, "Available workflows", "project-review"); !strings.Contains(got, "Review project code") || !strings.Contains(got, "when: when code changed") {
		t.Fatalf("workflow definition field = %q", got)
	}
	if !strings.Contains(result.PlainText, "available workflows: 2 ready") {
		t.Fatalf("expected available workflows in plain text, got:\n%s", result.PlainText)
	}
}

func TestExecuteLocalWorkflowRunReturnsStructuredLocalResult(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.WorkflowsEnabled = true
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()
	startedAt := time.Date(2026, 5, 29, 10, 0, 0, 0, time.UTC)
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-detail", Type: workflow.EventRunStarted, Status: workflow.RunStatusRunning, Time: startedAt, Message: "detail run"})
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-detail", Type: workflow.EventScriptReady, Status: workflow.RunStatusRunning, Message: "detail", Data: map[string]any{
		"name": "custom-review",
		"phases": []any{
			map[string]any{"title": "Scope"},
			map[string]any{"title": "Research"},
			map[string]any{"title": "Verify"},
			map[string]any{"title": "Synthesize"},
		},
	}})
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-detail", Type: workflow.EventLog, Message: "checked contracts"})
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-detail", TaskID: "workflow-child", Type: workflow.EventWorkflowCompleted, Status: workflow.RunStatusCompleted, WorkflowName: "child-research", Message: "child done"})
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-detail", TaskID: "task-a", Type: workflow.EventTaskStarted, Status: workflow.TaskStatusRunning, Time: startedAt, Phase: "Research", Label: "search:docs", Message: "search docs prompt", Data: map[string]any{
		"actor_kind": "subagent",
		"model":      "deepseek-chat",
	}})
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-detail", TaskID: "task-a", Type: workflow.EventTaskProgress, Status: workflow.TaskStatusRunning, Time: startedAt.Add(time.Second), Phase: "Research", Label: "search:docs", Message: "Searching web \"node permissions\"", Data: map[string]any{
		"tool_name": "web_search",
	}})
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-detail", TaskID: "task-a", Type: workflow.EventTaskProgress, Status: workflow.TaskStatusRunning, Time: startedAt.Add(2 * time.Second), Phase: "Research", Label: "search:docs", Message: "Fetched nodejs.org docs", Data: map[string]any{
		"tool_name": "web_fetch",
	}})
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-detail", TaskID: "task-a", Type: workflow.EventTaskCompleted, Status: workflow.TaskStatusCompleted, Time: startedAt.Add(3 * time.Second), Phase: "Research", Label: "search:docs", Message: "done", Data: map[string]any{
		"duration_ms": int64(3200),
		"tool_calls":  []any{"web_search", "web_fetch"},
		"usage": map[string]any{
			"prompt_tokens":             int64(1000),
			"completion_tokens":         int64(531),
			"total_usage_tokens":        int64(1531),
			"prompt_cache_hit_tokens":   int64(700),
			"prompt_cache_miss_tokens":  int64(300),
			"reasoning_replay_tokens":   int64(120),
			"tool_result_replay_tokens": int64(80),
			"tool_result_raw_tokens":    int64(200),
			"tool_result_tokens_saved":  int64(120),
			"tool_results_compacted":    int64(1),
		},
	}})
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-detail", Type: workflow.EventBudgetUpdated, Status: workflow.RunStatusRunning, Message: "budget spent 7 / 20 completion tokens (13 remaining)", Data: map[string]any{
		"spent_tokens":        int64(7),
		"total_budget_tokens": int64(20),
		"remaining_tokens":    int64(13),
	}})
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-detail", Type: workflow.EventRunCompleted, Status: workflow.RunStatusCompleted, Time: startedAt.Add(10 * time.Second), Message: "final answer", Data: map[string]any{
		"result": map[string]any{
			"answer":  "final answer",
			"sources": []any{"https://example.com/source"},
			"caveats": []any{"limited evidence"},
		},
	}})

	result := app.WorkflowPanelLocalResult("run-detail")
	if result == nil || result.Kind != "workflow" {
		t.Fatalf("expected structured workflow result, got %+v", result)
	}
	if localResultHasSectionField(result, "Events", "log") {
		t.Fatalf("default workflow detail should not include full event section, got %+v", result.Sections)
	}
	if !strings.Contains(result.PlainText, "checked contracts") || strings.Contains(result.PlainText, "task_completed") {
		t.Fatalf("expected compact workflow snapshot without full event stream, got:\n%s", result.PlainText)
	}
	if !strings.Contains(result.PlainText, "child-research") || !strings.Contains(result.PlainText, "child done") {
		t.Fatalf("expected child workflow boundary in plain text, got:\n%s", result.PlainText)
	}
	if !strings.Contains(result.PlainText, "search:docs") || !strings.Contains(result.PlainText, "531 out · 2 tools · 3s") {
		t.Fatalf("expected task metrics in plain text, got:\n%s", result.PlainText)
	}
	if got := localResultSectionFieldValue(result, "Research", "#2 done search:docs"); !strings.Contains(got, "531 out · 2 tools · 3s") {
		t.Fatalf("expected task metrics in structured field, got %q", got)
	}
	if got := localResultSectionFieldValue(result, "Run", "Budget"); got != "7/20 completion tokens · 13 remaining" {
		t.Fatalf("budget field = %q", got)
	}
	if !strings.Contains(result.PlainText, "budget: 7/20 completion tokens · 13 remaining") {
		t.Fatalf("expected budget in plain text, got:\n%s", result.PlainText)
	}
	if got := localResultSectionFieldValue(result, "Result", "Answer"); got != "final answer" {
		t.Fatalf("result answer = %q", got)
	}
	if got := localResultSectionFieldValue(result, "Result", "Sources"); got != "https://example.com/source" {
		t.Fatalf("result sources = %q", got)
	}
	snapshot := result.WorkflowPanelSnapshot
	if snapshot == nil {
		t.Fatalf("expected workflow panel snapshot")
	}
	if snapshot.RunID != "run-detail" || snapshot.Status != workflow.RunStatusCompleted || snapshot.Summary != "final answer" {
		t.Fatalf("unexpected snapshot header: %+v", snapshot)
	}
	if len(snapshot.Phases) < 4 || snapshot.Phases[0].Name != "Scope" || snapshot.Phases[1].Name != "Research" || snapshot.Phases[2].Name != "Verify" || snapshot.Phases[3].Name != "Synthesize" {
		t.Fatalf("expected declared phases in snapshot, got %+v", snapshot.Phases)
	}
	if snapshot.Phases[0].Total != 0 || snapshot.Phases[2].Total != 0 || snapshot.Phases[3].Total != 0 {
		t.Fatalf("expected not-started declared phases to remain empty, got %+v", snapshot.Phases)
	}
	var task *WorkflowPanelTask
	for i := range snapshot.Phases[1].Tasks {
		if snapshot.Phases[1].Tasks[i].ID == "task-a" {
			task = &snapshot.Phases[1].Tasks[i]
			break
		}
	}
	if task == nil {
		t.Fatalf("expected task-a in workflow panel snapshot: %+v", snapshot.Phases[1].Tasks)
	}
	if task.Prompt != "search docs prompt" {
		t.Fatalf("task prompt was not preserved: %q", task.Prompt)
	}
	if task.Outcome != "done" || task.Message != "done" {
		t.Fatalf("task outcome/message = %q/%q", task.Outcome, task.Message)
	}
	if task.Model != "deepseek-chat" || task.ActorKind != "subagent" {
		t.Fatalf("task model/actor = %q/%q", task.Model, task.ActorKind)
	}
	if len(task.Activity) != 2 || task.Activity[0].ToolName != "web_search" || task.Activity[1].Message != "Fetched nodejs.org docs" {
		t.Fatalf("unexpected task activity: %+v", task.Activity)
	}
	if task.ToolCalls != 2 || len(task.ToolCallNames) != 2 || task.ToolCallNames[0] != "web_search" {
		t.Fatalf("unexpected task tools: calls=%d names=%+v", task.ToolCalls, task.ToolCallNames)
	}
	if task.TotalTokens != 1531 || task.CompletionTokens != 531 || task.PromptCacheHit != 700 || task.ReasoningReplay != 120 || task.ToolReplayTokens != 80 || task.DurationMS != 3200 {
		t.Fatalf("unexpected task metrics: %+v", task)
	}

	terminal := app.BuildWorkflowTerminalLocalResult("run-detail")
	if terminal == nil || terminal.Kind != "workflow-terminal" {
		t.Fatalf("expected terminal workflow result, got %+v", terminal)
	}
	for _, want := range []string{
		"Dynamic workflow \"custom-review\" completed",
		"final answer",
		"Full result:",
		"result.json",
		"Runtime:\n2/2 completed · 10s · 531 out · 2 tool calls",
	} {
		if !strings.Contains(terminal.PlainText, want) {
			t.Fatalf("expected terminal plain text to contain %q, got:\n%s", want, terminal.PlainText)
		}
	}
	resultPath := lineAfter(terminal.PlainText, "Full result:")
	if resultPath == "" {
		t.Fatalf("terminal result should include full result path, got:\n%s", terminal.PlainText)
	}
	data, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read result.json: %v", err)
	}
	if !strings.Contains(string(data), `"answer": "final answer"`) {
		t.Fatalf("result.json did not contain structured result, got:\n%s", string(data))
	}
	if strings.Contains(terminal.PlainText, "total") {
		t.Fatalf("terminal plain text should not show total token accounting, got:\n%s", terminal.PlainText)
	}
	if got := localResultFieldValue(terminal, "Total tokens"); got != "" {
		t.Fatalf("terminal fields should not show total tokens, got %q", got)
	}
	if got := localResultSectionFieldValue(terminal, "Runtime", "Details"); got != "" {
		t.Fatalf("runtime section should not repeat details link, got %q", got)
	}
}

func TestWorkflowTerminalResultPreviewDoesNotInferBusinessMeaning(t *testing.T) {
	lines := workflowTerminalResultLinesFor(map[string]any{
		"candidateCount": float64(0),
		"candidates":     []any{},
		"rounds":         float64(2),
	}, workflowResultDisplayFields(map[string]any{
		"candidateCount": float64(0),
		"candidates":     []any{},
		"rounds":         float64(2),
	}))
	got := strings.Join(lines, "\n")
	for _, want := range []string{"Result:", "candidate Count: 0", "candidates: []", "rounds: 2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("preview missing %q, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "No candidates found") {
		t.Fatalf("preview should not infer empty result semantics, got %q", got)
	}

	lines = workflowTerminalResultLinesFor(map[string]any{
		"confirmedCount": float64(2),
		"confirmed": []any{
			map[string]any{"severity": "high", "dimension": "bugs", "title": "index.html not found in workspace"},
			map[string]any{"severity": "medium", "dimension": "a11y", "title": "Missing label"},
		},
	}, workflowResultDisplayFields(map[string]any{
		"confirmedCount": float64(2),
		"confirmed": []any{
			map[string]any{"severity": "high", "dimension": "bugs", "title": "index.html not found in workspace"},
			map[string]any{"severity": "medium", "dimension": "a11y", "title": "Missing label"},
		},
	}))
	got = strings.Join(lines, "\n")
	for _, want := range []string{"confirmed Count: 2", "confirmed: 2 items", "1. [high · bugs] index.html not found in workspace", "2. [medium · a11y] Missing label"} {
		if !strings.Contains(got, want) {
			t.Fatalf("confirmed preview missing %q, got:\n%s", want, got)
		}
	}
}

func TestWorkflowTerminalResultKeepsFullResultPathWhenTruncated(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	app := newWorkflowTestApp(t, cfg.DataDir, &deepResearchTestSpawner{})
	startedAt := time.Now().Add(-time.Minute)
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-long-result", Type: workflow.EventRunStarted, Status: workflow.RunStatusRunning, Time: startedAt, Message: "long result", SessionID: app.sessionID})
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-long-result", Type: workflow.EventScriptReady, Status: workflow.RunStatusRunning, Time: startedAt, Message: "long-result", Data: map[string]any{"name": "long-result"}})
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-long-result", Type: workflow.EventRunCompleted, Status: workflow.RunStatusCompleted, Time: startedAt.Add(time.Second), Message: "done", Data: map[string]any{
		"result": map[string]any{
			"answer": strings.Repeat("A", workflowTerminalPlainTextLimit+2000),
		},
	}})

	terminal := app.BuildWorkflowTerminalLocalResult("run-long-result")
	if terminal == nil || terminal.Kind != "workflow-terminal" {
		t.Fatalf("expected terminal workflow result, got %+v", terminal)
	}
	if !strings.Contains(terminal.PlainText, "... output truncated in chat") {
		t.Fatalf("expected truncation marker, got:\n%s", terminal.PlainText)
	}
	resultPath := lineAfter(terminal.PlainText, "Full result:")
	if resultPath == "" || !strings.HasSuffix(resultPath, "result.json") {
		t.Fatalf("truncated terminal result should keep full result path, got:\n%s", terminal.PlainText)
	}
	if _, err := os.Stat(resultPath); err != nil {
		t.Fatalf("expected result.json to exist at %q: %v", resultPath, err)
	}
}

func TestWorkflowTaskStatusFromEventTreatsCompletedRunningEventAsCompleted(t *testing.T) {
	got := workflowTaskStatusFromEvent(workflow.RunEvent{
		Type:   workflow.EventTaskCompleted,
		Status: workflow.TaskStatusRunning,
	})
	if got != workflow.TaskStatusCompleted {
		t.Fatalf("status = %q, want completed", got)
	}
}

func TestWorkflowResultDisplayFieldsFormatsGenericJSON(t *testing.T) {
	fields := workflowResultDisplayFields(map[string]any{
		"confirmed":      []any{},
		"confirmedCount": float64(0),
		"decision":       "## Release Decision\n\nDO NOT SHIP",
		"reviews": []any{
			map[string]any{"category": "ux", "shipBlocker": true},
		},
	})
	result := &LocalResult{Fields: fields}
	if got := localResultFieldValue(result, "reviews"); strings.Contains(got, "map[") || !strings.Contains(got, `"category": "ux"`) || !strings.Contains(got, `"shipBlocker": true`) {
		t.Fatalf("generic workflow result should render nested values as pretty JSON, got %q", got)
	}
	if got := localResultFieldValue(result, "Decision"); !strings.Contains(got, "DO NOT SHIP") {
		t.Fatalf("decision summary missing, got %q", got)
	}
	if got := localResultFieldValue(result, "confirmed"); got != "[]" {
		t.Fatalf("empty array field = %q, want []", got)
	}
	if got := localResultFieldValue(result, "confirmedCount"); got != "0" {
		t.Fatalf("numeric field = %q, want 0", got)
	}
}

func TestWorkflowTerminalLocalResultDoesNotRepeatLongSummaryAsIdentity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.WorkflowsEnabled = true
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	longSummary := strings.TrimSpace(strings.Repeat("Between Node.js v20 and v22, the Permission Model changed surprisingly little. ", 8))
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-summary", Type: workflow.EventRunStarted, Status: workflow.RunStatusRunning, Message: "starting"})
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-summary", Type: workflow.EventScriptReady, Status: workflow.RunStatusRunning, Data: map[string]any{
		"description": "Deep research harness",
	}})
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-summary", Type: workflow.EventRunCompleted, Status: workflow.RunStatusCompleted, Message: longSummary, Data: map[string]any{
		"result": map[string]any{
			"summary": longSummary,
			"caveats": "verify exact minor versions",
		},
	}})

	terminal := app.BuildWorkflowTerminalLocalResult("run-summary")
	if terminal == nil || terminal.Kind != "workflow-terminal" {
		t.Fatalf("expected terminal workflow result, got %+v", terminal)
	}
	if !strings.Contains(terminal.Title, `Dynamic workflow "run-summary" completed`) {
		t.Fatalf("terminal title should use workflow identity, got %q", terminal.Title)
	}
	if got := localResultFieldValue(terminal, "Workflow"); got != "run-summary" {
		t.Fatalf("workflow identity = %q, want run-summary", got)
	}
	if got := localResultFieldValue(terminal, "Summary"); got == "" || got == longSummary || len([]rune(got)) > 243 {
		t.Fatalf("top-level summary should be compact, got %q", got)
	}
	if got := localResultSectionFieldValue(terminal, "Result", "Summary"); got != longSummary {
		t.Fatalf("result summary should remain complete, got %q", got)
	}
	if strings.Count(terminal.PlainText, longSummary) != 1 {
		t.Fatalf("plain text should include the full summary once, got:\n%s", terminal.PlainText)
	}
}

func TestWorkflowRunLocalResultSurfacesFailureReason(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.WorkflowsEnabled = true
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-failed", Type: workflow.EventRunStarted, Status: workflow.RunStatusRunning, Message: "deep research"})
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-failed", TaskID: "task-scope", Type: workflow.EventTaskStarted, Status: workflow.TaskStatusRunning, Phase: "Scope", Label: "scope"})
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-failed", TaskID: "task-scope", Type: workflow.EventTaskFailed, Status: workflow.TaskStatusFailed, Phase: "Scope", Label: "scope", Message: "deepseek 402: Insufficient Balance"})
	appendWorkflowTestEvent(t, app, workflow.RunEvent{RunID: "run-failed", Type: workflow.EventRunFailed, Status: workflow.RunStatusFailed, Message: "workflow script failed: Error: deepseek 402: Insufficient Balance"})

	result := app.buildWorkflowRunLocalResult("run-failed")
	if result == nil || result.Kind != "workflow" {
		t.Fatalf("expected workflow detail result, got %+v", result)
	}
	if got := localResultFieldValue(result, "Error"); !strings.Contains(got, "Insufficient Balance") {
		t.Fatalf("top-level error = %q", got)
	}
	if got := localResultSectionFieldValue(result, "Run", "Error"); !strings.Contains(got, "Insufficient Balance") {
		t.Fatalf("run section error = %q", got)
	}
	if !strings.Contains(result.PlainText, "summary: workflow script failed: Error: deepseek 402: Insufficient Balance") {
		t.Fatalf("plain text should keep failure summary, got:\n%s", result.PlainText)
	}
	terminal := app.BuildWorkflowTerminalLocalResult("run-failed")
	if terminal == nil || terminal.Kind != "workflow-terminal" {
		t.Fatalf("expected terminal failure result, got %+v", terminal)
	}
	if !strings.Contains(terminal.PlainText, "Dynamic workflow \"run-failed\" failed") || !strings.Contains(terminal.PlainText, "Failed subagents:") || !strings.Contains(terminal.PlainText, "scope: deepseek 402: Insufficient Balance") {
		t.Fatalf("terminal failure should surface failed subagent, got:\n%s", terminal.PlainText)
	}
}

func TestNewRegistersWorkflowToolAsWriteCapable(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.WorkflowsEnabled = true
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	spec, ok := app.toolRegistry.Spec("workflow")
	if !ok {
		t.Fatal("workflow tool was not registered")
	}
	if spec.ReadOnly {
		t.Fatal("workflow tool should not be read-only")
	}
	if app.toolRegistry.Get("workflow") == nil {
		t.Fatal("workflow tool implementation missing")
	}
}

func TestRefreshMCPToolsKeepsWorkflowTool(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.WorkflowsEnabled = true
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	if err := app.refreshMCPTools(); err != nil {
		t.Fatalf("refreshMCPTools: %v", err)
	}
	if app.toolRegistry.Get("workflow") == nil {
		t.Fatal("workflow tool should survive MCP refresh")
	}
	if _, ok := app.toolRegistry.Spec("workflow"); !ok {
		t.Fatal("workflow tool spec should survive MCP refresh")
	}
}

func TestExecuteLocalWorkflowsReturnsUsageErrorForExtraArgs(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.WorkflowsEnabled = true
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	for _, input := range []string{"/workflows run-detail", "/workflows run extra", "/workflows events run-detail", "/workflows cancel run-detail"} {
		_, err = app.ExecuteLocalCommand(input)
		if err == nil || !strings.Contains(err.Error(), workflowsUsage) {
			t.Fatalf("expected /workflows usage error for %q, got %v", input, err)
		}
	}
}

func TestCancelWorkflowRunCancelsActiveRun(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	spawner := &blockingWorkflowTestSpawner{started: make(chan struct{})}
	app := newWorkflowTestApp(t, cfg.DataDir, spawner)

	out, err := app.workflowRunner.StartWorkflow(context.Background(), app.sessionID, workflow.WorkflowInput{Script: `export const meta = { name: 'cancel-test', description: 'cancel test' }
await agent('wait forever', { label: 'wait' })
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	<-spawner.started
	cancelOut, err := app.CancelWorkflowRun(string(out.RunID))
	if err != nil {
		t.Fatalf("CancelWorkflowRun: %v", err)
	}
	if cancelOut == nil || !strings.Contains(cancelOut.PlainText, "cancelling workflow") {
		t.Fatalf("unexpected cancel result: %+v", cancelOut)
	}
	run := waitWorkflowRunStatus(t, app, out.RunID, workflow.RunStatusCancelled)
	if countWorkflowEvents(run.Events, workflow.EventTaskCancelled) != 1 {
		t.Fatalf("expected task_cancelled event, events=%+v", run.Events)
	}
}

func TestExecuteLocalDeepResearchStartsBuiltinWorkflow(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	spawner := &deepResearchTestSpawner{}
	app := newWorkflowTestApp(t, cfg.DataDir, spawner)

	out, err := app.StartWorkflowFromConfirmation(workflow.BuiltinDeepResearchName, "marked v12 sanitize behavior", "", false)
	if err != nil {
		t.Fatalf("StartWorkflowFromConfirmation: %v", err)
	}
	if out == nil || out.Kind != "workflow-run" {
		t.Fatalf("expected workflow-run local result, got %+v", out)
	}
	runID := localResultFieldValue(out, "Run")
	if runID == "" {
		t.Fatalf("missing run id: %+v", out)
	}
	for _, want := range []string{
		"Started the deep-research workflow in the background.",
		"Open /workflows to watch progress and inspect details.",
	} {
		if !strings.Contains(out.PlainText, want) {
			t.Fatalf("expected launch text to contain %q, got:\n%s", want, out.PlainText)
		}
	}
	for _, unwanted := range []string{"Status     async_launched", "Script     "} {
		if strings.Contains(out.PlainText, unwanted) {
			t.Fatalf("launch text should not render structured status/debug fields %q:\n%s", unwanted, out.PlainText)
		}
	}
	run := waitWorkflowRunStatus(t, app, workflow.RunID(runID), workflow.RunStatusCompleted)
	summary := workflowRunSummary(run)
	if !strings.Contains(summary.Summary, "Supported answer") {
		t.Fatalf("summary = %q events=%+v", summary.Summary, run.Events)
	}
	detail := app.buildWorkflowRunLocalResult(runID)
	if got := localResultSectionFieldValue(detail, "Result", "Summary"); !strings.Contains(got, "Supported answer") {
		t.Fatalf("summary field = %q", got)
	}
	spawner.mu.Lock()
	requests := append([]tasks.SpawnSubagentRequest(nil), spawner.requests...)
	spawner.mu.Unlock()
	if len(requests) != 9 {
		t.Fatalf("requests = %+v", requests)
	}
	if got := strings.Join(requests[0].Tools, ","); got != "" {
		t.Fatalf("scope tools = %#v", requests[0].Tools)
	}
	if got := strings.Join(requests[1].Tools, ","); got != "web.search" {
		t.Fatalf("search tools = %#v", requests[1].Tools)
	}
	if got := strings.Join(requests[4].Tools, ","); got != "web.fetch" {
		t.Fatalf("fetch tools = %#v", requests[4].Tools)
	}
	if got := strings.Join(requests[5].Tools, ","); got != "web.search" {
		t.Fatalf("verify tools = %#v", requests[5].Tools)
	}
	if got := strings.Join(requests[len(requests)-1].Tools, ","); got != "" {
		t.Fatalf("synthesize tools = %#v", requests[len(requests)-1].Tools)
	}
	if requests[1].WorkflowName != workflow.BuiltinDeepResearchName || requests[1].WorkflowRunID == "" || requests[1].WorkflowPhase != "Search" {
		t.Fatalf("missing workflow context on request: %+v", requests[1])
	}
}

func TestExecuteLocalDeepResearchRunsProjectOverride(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	spawner := &deepResearchTestSpawner{}
	app := newWorkflowTestApp(t, cfg.DataDir, spawner)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "deep-research.js"), []byte(`export const meta = {
  name: 'deep-research',
  description: 'project deep research',
  phases: [{ title: 'Project' }],
}
log('project override ' + args)
return { answer: 'project answer: ' + args }
`), 0o600); err != nil {
		t.Fatalf("write project workflow: %v", err)
	}
	app.workflowRunner.Library = workflow.NewLibraryWithRoots([]workflow.LibraryRoot{{Path: root, Source: "project", Rank: 0}})

	out, err := app.StartWorkflowFromConfirmation(workflow.BuiltinDeepResearchName, "custom question", "", false)
	if err != nil {
		t.Fatalf("StartWorkflowFromConfirmation: %v", err)
	}
	if out == nil || out.Kind != "workflow-run" {
		t.Fatalf("expected workflow-run local result, got %+v", out)
	}
	if !strings.Contains(out.PlainText, "Started the deep-research workflow in the background.") {
		t.Fatalf("unexpected launch text:\n%s", out.PlainText)
	}
	runID := localResultFieldValue(out, "Run")
	run := waitWorkflowRunStatus(t, app, workflow.RunID(runID), workflow.RunStatusCompleted)
	summary := workflowRunSummary(run)
	if summary.Summary != "project answer: custom question" {
		t.Fatalf("summary = %q; events=%+v", summary.Summary, run.Events)
	}
	if !hasWorkflowLog(run.Events, "project override custom question") {
		t.Fatalf("missing project override log; events=%+v", run.Events)
	}
	spawner.mu.Lock()
	defer spawner.mu.Unlock()
	if len(spawner.requests) != 0 {
		t.Fatalf("project override should not run builtin agents, got %+v", spawner.requests)
	}
}

func TestExecuteLocalDeepResearchShowsLaunchConfirmation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	spawner := &deepResearchTestSpawner{}
	app := newWorkflowTestApp(t, cfg.DataDir, spawner)

	out, err := app.ExecuteLocalCommand("/deep-research marked v12 sanitize behavior")
	if err != nil {
		t.Fatalf("ExecuteLocalCommand: %v", err)
	}
	if !out.Handled || out.Mutated || out.LocalResult == nil || out.LocalResult.Kind != "workflow-launch" {
		t.Fatalf("expected workflow launch confirmation, got %+v", out)
	}
	if !strings.Contains(out.Text, "Run a dynamic workflow?") || strings.Contains(out.Text, "--yes") {
		t.Fatalf("unexpected confirmation text:\n%s", out.Text)
	}
	if !localResultHasAction(out.LocalResult, "View raw script") {
		t.Fatalf("confirmation missing raw script action: %+v", out.LocalResult.Actions)
	}
	if localResultSectionFieldValue(out.LocalResult, "Raw script", "Script") == "" {
		t.Fatalf("confirmation missing raw script section: %+v", out.LocalResult.Sections)
	}
	spawner.mu.Lock()
	defer spawner.mu.Unlock()
	if len(spawner.requests) != 0 {
		t.Fatalf("confirmation should not spawn agents, got %+v", spawner.requests)
	}
}

func TestStartWorkflowFromConfirmationRunsNamedWorkflow(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	app := newWorkflowTestApp(t, cfg.DataDir, &deepResearchTestSpawner{})
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "review-spa.js"), []byte(`export const meta = {
  name: 'review-spa',
  description: 'Review the book SPA',
}
log('topic ' + args.topic)
return { summary: 'reviewed ' + args.topic }
`), 0o600); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	app.workflowRunner.Library = workflow.NewLibraryWithRoots([]workflow.LibraryRoot{{Path: root, Source: "project", Rank: 0}})

	confirmation, err := app.BuildWorkflowLaunchConfirmation("review-spa", `{"topic":"ok"}`, "")
	if err != nil {
		t.Fatalf("BuildWorkflowLaunchConfirmation: %v", err)
	}
	if confirmation.Kind != "workflow-launch" || !strings.Contains(confirmation.PlainText, "Run a dynamic workflow?") {
		t.Fatalf("unexpected confirmation: %+v", confirmation)
	}

	out, err := app.StartWorkflowFromConfirmation("review-spa", `{"topic":"ok"}`, "", false)
	if err != nil {
		t.Fatalf("StartWorkflowFromConfirmation: %v", err)
	}
	if out == nil || out.Kind != "workflow-run" || !strings.Contains(out.PlainText, "Started the review-spa workflow in the background.") {
		t.Fatalf("expected workflow-run local result, got %+v", out)
	}
	runID := localResultFieldValue(out, "Run")
	run := waitWorkflowRunStatus(t, app, workflow.RunID(runID), workflow.RunStatusCompleted)
	summary := workflowRunSummary(run)
	if summary.Summary != "reviewed ok" {
		t.Fatalf("summary = %q; events=%+v", summary.Summary, run.Events)
	}
	if !hasWorkflowLog(run.Events, "topic ok") {
		t.Fatalf("missing workflow log; events=%+v", run.Events)
	}
}

func TestExecuteLocalDeepResearchRememberTrustsWorkflowInProjectConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	spawner := &deepResearchTestSpawner{}
	app := newWorkflowTestApp(t, cfg.DataDir, spawner)

	first, err := app.StartWorkflowFromConfirmation(workflow.BuiltinDeepResearchName, "marked v12 sanitize behavior", "", true)
	if err != nil {
		t.Fatalf("first StartWorkflowFromConfirmation: %v", err)
	}
	if first == nil || first.Kind != "workflow-run" {
		t.Fatalf("expected first command to start workflow, got %+v", first)
	}
	firstRunID := localResultFieldValue(first, "Run")
	if firstRunID == "" {
		t.Fatalf("missing first run id: %+v", first)
	}
	waitWorkflowRunStatus(t, app, workflow.RunID(firstRunID), workflow.RunStatusCompleted)
	cfgFile, _, err := LoadConfigFile(ProjectLocalConfigPath(app.workspaceRoot))
	if err != nil {
		t.Fatalf("LoadConfigFile: %v", err)
	}
	trustKey, err := app.workflowTrustKey(workflow.BuiltinDeepResearchName)
	if err != nil {
		t.Fatalf("workflowTrustKey: %v", err)
	}
	if !containsString(cfgFile.Workflows.Trusted, trustKey) {
		t.Fatalf("expected trusted workflow in project local config, got %+v", cfgFile.Workflows.Trusted)
	}

	second, err := app.ExecuteLocalCommand("/deep-research marked v12 sanitize behavior")
	if err != nil {
		t.Fatalf("second ExecuteLocalCommand: %v", err)
	}
	if !second.Mutated || second.LocalResult == nil || second.LocalResult.Kind != "workflow-run" {
		t.Fatalf("trusted workflow should start without confirmation, got %+v", second)
	}
	secondRunID := localResultFieldValue(second.LocalResult, "Run")
	if secondRunID == "" {
		t.Fatalf("missing second run id: %+v", second.LocalResult)
	}
	waitWorkflowRunStatus(t, app, workflow.RunID(secondRunID), workflow.RunStatusCompleted)
}

func TestExecuteLocalDeepResearchTrustDoesNotCoverProjectOverride(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	spawner := &deepResearchTestSpawner{}
	app := newWorkflowTestApp(t, cfg.DataDir, spawner)

	first, err := app.StartWorkflowFromConfirmation(workflow.BuiltinDeepResearchName, "marked v12 sanitize behavior", "", true)
	if err != nil {
		t.Fatalf("first StartWorkflowFromConfirmation: %v", err)
	}
	firstRunID := localResultFieldValue(first, "Run")
	waitWorkflowRunStatus(t, app, workflow.RunID(firstRunID), workflow.RunStatusCompleted)

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "deep-research.js"), []byte(`export const meta = {
  name: 'deep-research',
  description: 'project deep research',
  phases: [{ title: 'Project' }],
}
return { answer: 'project answer: ' + args }
`), 0o600); err != nil {
		t.Fatalf("write project workflow: %v", err)
	}
	app.workflowRunner.Library = workflow.NewLibraryWithRoots([]workflow.LibraryRoot{{Path: root, Source: "project", Rank: 0}})

	second, err := app.ExecuteLocalCommand("/deep-research custom question")
	if err != nil {
		t.Fatalf("second ExecuteLocalCommand: %v", err)
	}
	if !second.Handled || second.Mutated || second.LocalResult == nil || second.LocalResult.Kind != "workflow-launch" {
		t.Fatalf("project override should require confirmation despite trusted builtin, got %+v", second)
	}
}

func TestExecuteLocalDeepResearchTrustInvalidatesWhenProjectWorkflowChanges(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	spawner := &deepResearchTestSpawner{}
	app := newWorkflowTestApp(t, cfg.DataDir, spawner)
	root := t.TempDir()
	workflowPath := filepath.Join(root, "deep-research.js")
	if err := os.WriteFile(workflowPath, []byte(`export const meta = {
  name: 'deep-research',
  description: 'project deep research v1',
  phases: [{ title: 'Project' }],
}
return { answer: 'project v1: ' + args }
`), 0o600); err != nil {
		t.Fatalf("write project workflow v1: %v", err)
	}
	app.workflowRunner.Library = workflow.NewLibraryWithRoots([]workflow.LibraryRoot{{Path: root, Source: "project", Rank: 0}})

	first, err := app.StartWorkflowFromConfirmation(workflow.BuiltinDeepResearchName, "custom question", "", true)
	if err != nil {
		t.Fatalf("first StartWorkflowFromConfirmation: %v", err)
	}
	firstRunID := localResultFieldValue(first, "Run")
	waitWorkflowRunStatus(t, app, workflow.RunID(firstRunID), workflow.RunStatusCompleted)

	if err := os.WriteFile(workflowPath, []byte(`export const meta = {
  name: 'deep-research',
  description: 'project deep research v2',
  phases: [{ title: 'Project' }],
}
return { answer: 'project v2: ' + args }
`), 0o600); err != nil {
		t.Fatalf("write project workflow v2: %v", err)
	}

	second, err := app.ExecuteLocalCommand("/deep-research custom question")
	if err != nil {
		t.Fatalf("second ExecuteLocalCommand: %v", err)
	}
	if !second.Handled || second.Mutated || second.LocalResult == nil || second.LocalResult.Kind != "workflow-launch" {
		t.Fatalf("changed project workflow should require confirmation, got %+v", second)
	}
}

func TestExecuteLocalDeepResearchRequiresConfirmationBeforeResume(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	spawner := &deepResearchTestSpawner{}
	app := newWorkflowTestApp(t, cfg.DataDir, spawner)

	first, err := app.StartWorkflowFromConfirmation(workflow.BuiltinDeepResearchName, "marked v12 sanitize behavior", "", false)
	if err != nil {
		t.Fatalf("first StartWorkflowFromConfirmation: %v", err)
	}
	sourceRunID := localResultFieldValue(first, "Run")
	if sourceRunID == "" {
		t.Fatalf("missing source run id: %+v", first)
	}
	waitWorkflowRunStatus(t, app, workflow.RunID(sourceRunID), workflow.RunStatusCompleted)
	spawner.mu.Lock()
	firstRequests := len(spawner.requests)
	spawner.mu.Unlock()
	if firstRequests != 9 {
		t.Fatalf("first requests = %d, want 9", firstRequests)
	}

	second, err := app.ExecuteLocalCommand("/deep-research --resume " + sourceRunID + " marked v12 sanitize behavior")
	if err != nil {
		t.Fatalf("second ExecuteLocalCommand: %v", err)
	}
	if !second.Handled || second.Mutated || second.LocalResult == nil || second.LocalResult.Kind != "workflow-launch" {
		t.Fatalf("resume should require launch confirmation, got %+v", second)
	}
	if !strings.Contains(second.Text, "Run a dynamic workflow?") {
		t.Fatalf("unexpected resume confirmation text:\n%s", second.Text)
	}
	if got := localResultFieldValue(second.LocalResult, "Resume"); got != sourceRunID {
		t.Fatalf("resume confirmation field = %q, want %q", got, sourceRunID)
	}
	spawner.mu.Lock()
	afterConfirmationRequests := len(spawner.requests)
	spawner.mu.Unlock()
	if afterConfirmationRequests != firstRequests {
		t.Fatalf("resume confirmation spawned agents: before=%d after=%d", firstRequests, afterConfirmationRequests)
	}

	confirmed, err := app.startDeepResearchWorkflow(deepResearchOptions{
		Question:        "marked v12 sanitize behavior",
		ResumeFromRunID: sourceRunID,
		Confirmed:       true,
	})
	if err != nil {
		t.Fatalf("confirmed resume start: %v", err)
	}
	second = CommandExecution{Handled: true, LocalResult: confirmed, Mutated: true}
	if got := localResultFieldValue(second.LocalResult, "Resume"); got != sourceRunID {
		t.Fatalf("resume field = %q, want %q", got, sourceRunID)
	}
	resumedRunID := localResultFieldValue(second.LocalResult, "Run")
	waitWorkflowRunStatus(t, app, workflow.RunID(resumedRunID), workflow.RunStatusCompleted)
	spawner.mu.Lock()
	allRequests := len(spawner.requests)
	spawner.mu.Unlock()
	if allRequests != firstRequests {
		t.Fatalf("resume spawned new agents: before=%d after=%d", firstRequests, allRequests)
	}
	detail := app.buildWorkflowRunLocalResult(resumedRunID)
	if got := localResultSectionFieldValue(detail, "Run", "Tasks"); got != "0 running · 9 completed · 0 failed · 9 cached" {
		t.Fatalf("tasks field = %q", got)
	}
}

func TestExecuteLocalDeepResearchRequiresQuestion(t *testing.T) {
	app := &App{}
	_, err := app.ExecuteLocalCommand("/deep-research")
	if err == nil || !strings.Contains(err.Error(), deepResearchUsage) {
		t.Fatalf("expected usage error, got %v", err)
	}
}

func TestExecuteLocalDeepResearchValidatesOptions(t *testing.T) {
	app := &App{}
	tests := []string{
		"/deep-research --resume",
		"/deep-research --budget nope question",
		"/deep-research --budget 0 question",
		"/deep-research --unknown question",
	}
	for _, line := range tests {
		t.Run(line, func(t *testing.T) {
			_, err := app.ExecuteLocalCommand(line)
			if err == nil || !strings.Contains(err.Error(), deepResearchUsage) {
				t.Fatalf("expected usage error, got %v", err)
			}
		})
	}
}

func appendWorkflowTestEvent(t *testing.T, app *App, ev workflow.RunEvent) {
	t.Helper()
	if app == nil || app.workflowManager == nil || app.workflowManager.Store == nil {
		t.Fatal("workflow store unavailable")
	}
	if err := app.workflowManager.Store.Append(t.Context(), ev); err != nil {
		t.Fatalf("append workflow event: %v", err)
	}
}

type deepResearchTestSpawner struct {
	mu       sync.Mutex
	requests []tasks.SpawnSubagentRequest
}

func (s *deepResearchTestSpawner) AllowedSubagentTools(req tasks.SpawnSubagentRequest) ([]string, error) {
	out := make([]string, 0, len(req.Tools))
	for _, tool := range req.Tools {
		out = append(out, "allowed:"+tool)
	}
	return out, nil
}

func (s *deepResearchTestSpawner) SpawnSubagentWithProgress(_ context.Context, req tasks.SpawnSubagentRequest, _ func(core.ToolProgress)) (tasks.SpawnSubagentResponse, error) {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()
	summary := `{"answer":"Supported answer","sources":["https://example.com/source"],"caveats":[]}`
	switch {
	case strings.Contains(req.Task, "Decompose this research question"):
		summary = `{"question":"marked v12 sanitize behavior","summary":"check official sources","angles":[{"label":"official","query":"official docs"},{"label":"release","query":"release notes"},{"label":"source","query":"source code"}]}`
	case strings.Contains(req.Task, "## Web Searcher"):
		summary = `{"results":[{"url":"https://example.com/source","title":"Source","snippet":"relevant","relevance":"high"}]}`
	case strings.Contains(req.Task, "## Source Extractor"):
		summary = `{"sourceQuality":"primary","publishDate":"2026-01-01","claims":[{"claim":"Supported claim","quote":"source confirms it","importance":"central"}]}`
	case strings.Contains(req.Task, "## Adversarial Claim Verifier"):
		summary = `{"refuted":false,"evidence":"source confirms it","confidence":"high"}`
	case strings.Contains(req.Task, "## Synthesis: research report"):
		summary = `{"summary":"Supported answer","findings":[{"claim":"Supported claim","confidence":"high","sources":["https://example.com/source"],"evidence":"source confirms it","vote":"3-0"}],"caveats":"","openQuestions":[]}`
	}
	return tasks.SpawnSubagentResponse{
		SessionID:  "child-test",
		Status:     workflow.TaskStatusCompleted,
		Summary:    summary,
		ToolCalls:  []string{"web_search"},
		DurationMS: 1,
	}, nil
}

type blockingWorkflowTestSpawner struct {
	started chan struct{}
	once    sync.Once
}

func (s *blockingWorkflowTestSpawner) SpawnSubagentWithProgress(ctx context.Context, _ tasks.SpawnSubagentRequest, _ func(core.ToolProgress)) (tasks.SpawnSubagentResponse, error) {
	s.once.Do(func() {
		close(s.started)
	})
	<-ctx.Done()
	return tasks.SpawnSubagentResponse{}, ctx.Err()
}

func newWorkflowTestApp(t *testing.T, dataDir string, spawner workflow.AgentSpawner) *App {
	t.Helper()
	store, err := workflow.NewFileRunEventStore(dataDir)
	if err != nil {
		t.Fatalf("NewFileRunEventStore: %v", err)
	}
	scheduler := workflow.NewTaskScheduler(store, spawner)
	manager := workflow.NewRunManager(store, scheduler)
	runner := workflow.NewScriptRunner(dataDir, manager)
	runner.Library = workflow.NewLibraryWithRoots(nil)
	return &App{
		sessionID:       "session-test",
		workspaceRoot:   t.TempDir(),
		cfg:             DefaultConfig(),
		workflowManager: manager,
		workflowRunner:  runner,
	}
}

func waitWorkflowRunStatus(t *testing.T, app *App, runID workflow.RunID, status string) workflow.Run {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		run, err := app.workflowManager.Store.LoadRun(context.Background(), runID)
		if err != nil {
			t.Fatalf("LoadRun: %v", err)
		}
		if run.Status == status {
			return run
		}
		if run.Status == workflow.RunStatusFailed {
			t.Fatalf("workflow failed: %+v", run)
		}
		time.Sleep(10 * time.Millisecond)
	}
	run, _ := app.workflowManager.Store.LoadRun(context.Background(), runID)
	t.Fatalf("timed out waiting for %s, run=%+v", status, run)
	return workflow.Run{}
}

func countWorkflowEvents(events []workflow.RunEvent, typ string) int {
	n := 0
	for _, ev := range events {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

func TestExecuteLocalStatsReturnsStructuredLocalResult(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()

	out, err := app.ExecuteLocalCommand("/stats usage")
	if err != nil {
		t.Fatalf("ExecuteLocalCommand: %v", err)
	}
	if !out.Handled || out.LocalResult == nil || out.LocalResult.Kind != "stats" || out.LocalResult.Title != "Stats: usage" {
		t.Fatalf("expected structured stats result, got %+v", out)
	}
	if out.Text == "" || out.Text != out.LocalResult.PlainText {
		t.Fatalf("expected text fallback to match local result, text=%q local=%q", out.Text, out.LocalResult.PlainText)
	}
	if !localResultHasField(out.LocalResult, "View") || len(out.LocalResult.Sections) == 0 {
		t.Fatalf("expected stats fields and sections, got %+v", out.LocalResult)
	}
}

func TestExecuteLocalHelpReturnsStructuredLocalResult(t *testing.T) {
	app := &App{}
	out, err := app.ExecuteLocalCommand("/help")
	if err != nil {
		t.Fatalf("ExecuteLocalCommand: %v", err)
	}
	if !out.Handled || out.LocalResult == nil || out.LocalResult.Kind != "help" {
		t.Fatalf("expected structured help result, got %+v", out)
	}
	if out.Text == "" || out.Text != out.LocalResult.PlainText {
		t.Fatalf("expected text fallback to match local result, text=%q local=%q", out.Text, out.LocalResult.PlainText)
	}
	if len(out.LocalResult.Sections) < 3 {
		t.Fatalf("expected grouped help sections, got %+v", out.LocalResult.Sections)
	}
}

func TestExecuteLocalMemoryReturnsStructuredLocalResult(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	workspace := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	app, err := New(t.Context(), cfg, StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer app.Close()
	memStore := memoryplugin.NewStore(filepath.Join(app.cfg.DataDir, "plugins", "memory"), app.workspaceRoot)
	if _, err := memStore.Write(memoryplugin.WriteInput{Scope: "global", Type: "user", Name: "style", Description: "concise Chinese", Content: "Answer concisely in Chinese."}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	out, err := app.ExecuteLocalCommand("/memory show global/style")
	if err != nil {
		t.Fatalf("ExecuteLocalCommand: %v", err)
	}
	if !out.Handled || out.LocalResult == nil || out.LocalResult.Kind != "memory" || out.LocalResult.Title != "Memory entry" {
		t.Fatalf("expected structured memory result, got %+v", out)
	}
	for _, want := range []string{"Name", "Scope", "Type", "Path", "Description", "Content"} {
		if !localResultHasField(out.LocalResult, want) {
			t.Fatalf("expected memory field %q, got %+v", want, out.LocalResult.Fields)
		}
	}
}

func TestBuildMemoryShowLocalResultDoesNotParseBodyAsMetadata(t *testing.T) {
	text := strings.Join([]string{
		"# style (global/user)",
		"",
		"> concise Chinese",
		"",
		"path: /tmp/memory/style.md",
		"",
		"Keep this body.",
		"> This is a real body quote.",
		"path: this is body content",
	}, "\n")
	result := buildMemoryShowLocalResult(text)
	if result == nil || result.Kind != "memory" {
		t.Fatalf("expected memory local result, got %+v", result)
	}
	if got := localResultFieldValue(result, "Description"); got != "concise Chinese" {
		t.Fatalf("unexpected description %q", got)
	}
	if got := localResultFieldValue(result, "Path"); got != "/tmp/memory/style.md" {
		t.Fatalf("unexpected path %q", got)
	}
	content := localResultFieldValue(result, "Content")
	for _, want := range []string{"Keep this body.", "> This is a real body quote.", "path: this is body content"} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected content to keep %q, got:\n%s", want, content)
		}
	}
}

func TestBuildCompactLocalResultIncludesStructuredFields(t *testing.T) {
	text := "compacted conversation: 10 -> 2 messages; ~1000 -> ~200 tokens"
	result := buildCompactLocalResult(agent.CompactInfo{
		Compacted:      true,
		MessagesBefore: 10,
		MessagesAfter:  2,
		BeforeEstimate: 1000,
		AfterEstimate:  200,
	}, text)
	if result == nil || result.Kind != "compact" || result.PlainText != text {
		t.Fatalf("expected structured compact result, got %+v", result)
	}
	for _, want := range []string{"Result", "Messages", "Tokens"} {
		if !localResultHasField(result, want) {
			t.Fatalf("expected compact field %q, got %+v", want, result.Fields)
		}
	}
}

func localResultHasField(result *LocalResult, label string) bool {
	if result == nil {
		return false
	}
	for _, field := range result.Fields {
		if field.Label == label {
			return true
		}
	}
	return false
}

func localResultHasAction(result *LocalResult, label string) bool {
	if result == nil {
		return false
	}
	for _, action := range result.Actions {
		if action.Label == label {
			return true
		}
	}
	return false
}

func localResultFieldValue(result *LocalResult, label string) string {
	if result == nil {
		return ""
	}
	for _, field := range result.Fields {
		if field.Label == label {
			return field.Value
		}
	}
	return ""
}

func localResultSectionHasField(section LocalResultSection, label string) bool {
	for _, field := range section.Fields {
		if field.Label == label {
			return true
		}
	}
	return false
}

func localResultHasSectionField(result *LocalResult, sectionTitle, fieldLabel string) bool {
	return localResultSectionFieldValue(result, sectionTitle, fieldLabel) != ""
}

func localResultSectionFieldValue(result *LocalResult, sectionTitle, fieldLabel string) string {
	if result == nil {
		return ""
	}
	for _, section := range result.Sections {
		if section.Title != sectionTitle {
			continue
		}
		for _, field := range section.Fields {
			if field.Label == fieldLabel {
				return field.Value
			}
		}
	}
	return ""
}

func lineContaining(text, needle string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func lineAfter(text, marker string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if !strings.Contains(line, marker) {
			continue
		}
		for _, next := range lines[i+1:] {
			if value := strings.TrimSpace(next); value != "" {
				return value
			}
		}
	}
	return ""
}

func hasWorkflowLog(events []workflow.RunEvent, message string) bool {
	for _, ev := range events {
		if ev.Type == workflow.EventLog && ev.Message == message {
			return true
		}
	}
	return false
}

func TestStartupLinesIncludeEffectiveThinkingAndEffort(t *testing.T) {
	app := &App{
		sessionID:        "sess-1",
		currentMode:      "agent",
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
		sessionID:   "sess-1",
		currentMode: "agent",
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
	if strings.Contains(out, "- view:") {
		t.Fatalf("expected status not to expose view mode, got:\n%s", out)
	}
}

func TestBuildStatusIncludesWorktreeDetails(t *testing.T) {
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
		model:            "deepseek-v4-pro",
		reasoningEffort:  "max",
		thinkingEnabled:  true,
		budgetWarningUSD: 0,
		cfg:              DefaultConfig(),
		worktree: WorktreeSession{
			Name:               "feature",
			Workspace:          "/tmp/repo/.whale/worktrees/feature/packages/api",
			Path:               "/tmp/repo/.whale/worktrees/feature",
			Branch:             "worktree-feature",
			OriginalWorkspace:  "/tmp/repo/packages/api",
			OriginalBranch:     "main",
			OriginalHeadCommit: "abc123",
		},
	}

	out := app.buildStatus()
	for _, want := range []string{
		"- worktree: feature",
		"- worktree.branch: worktree-feature",
		"- worktree.path: /tmp/repo/.whale/worktrees/feature",
		"- worktree.original_workspace: /tmp/repo/packages/api",
		"- worktree.original_branch: main",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected status to contain %q, got:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{
		"- worktree.workspace:",
		"- worktree.original_head:",
	} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("expected status not to contain %q, got:\n%s", unwanted, out)
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
	for _, want := range []string{"You are an expert code reviewer", "Target: local changes", "git diff --cached", "git diff", "inspect the contents of each relevant untracked file", "git symbolic-ref --short refs/remotes/origin/HEAD", "Avoid shell pipelines, redirects", "Do not fix findings, edit files, create commits, push branches, or open/update pull requests", "do not infer that review findings should be fixed first", "Do not prefix commands with cd", "If a local git diff output is truncated", "git diff --stat", "git diff --name-only", "Start with findings"} {
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
		Base:               policy.DefaultToolPolicy{},
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

	app.sessionID = "sess-1"
	res, err := app.ExecuteSlash("/new\tfresh-structured")
	if err != nil {
		t.Fatalf("ExecuteSlash /new: %v", err)
	}
	if !res.Handled || res.LocalResult == nil || res.LocalResult.Kind != "new_session" {
		t.Fatalf("expected structured new-session result, got %+v", res)
	}
	for _, want := range []string{"Session", "Previous", "Resume previous", "Mode"} {
		if !localResultHasField(res.LocalResult, want) {
			t.Fatalf("expected new-session field %q, got %+v", want, res.LocalResult.Fields)
		}
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
	structured, err := app.ExecuteSlash("/fork\tCustomAgain")
	if err != nil {
		t.Fatalf("ExecuteSlash /fork: %v", err)
	}
	if structured.LocalResult == nil || structured.LocalResult.Kind != "fork" {
		t.Fatalf("expected structured fork result, got %+v", structured)
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

func writeSessionMessages(t *testing.T, sessionsDir, sessionID string, msgs []core.Message) {
	t.Helper()
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions dir: %v", err)
	}
	path := filepath.Join(sessionsDir, sessionID+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open session log: %v", err)
	}
	defer f.Close()
	for _, msg := range msgs {
		b, err := json.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal session message: %v", err)
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			t.Fatalf("write session message: %v", err)
		}
	}
}

func writeToolInputEvent(t *testing.T, sessionsDir, sessionID string, rec telemetry.ToolInputEvent) {
	t.Helper()
	if err := telemetry.AppendToolInputEvent(sessionsDir, rec, time.UnixMilli(rec.TS)); err != nil {
		t.Fatalf("append tool input event for %s: %v", sessionID, err)
	}
}

func writeApprovalEvent(t *testing.T, sessionsDir, sessionID, event string) {
	t.Helper()
	writeApprovalEventForToolCall(t, sessionsDir, sessionID, "", event, "")
}

func writeApprovalEventForToolCall(t *testing.T, sessionsDir, sessionID, toolCallID, event, source string) {
	t.Helper()
	if err := telemetry.AppendApprovalEvent(sessionsDir, telemetry.ApprovalEvent{
		Session:    sessionID,
		Event:      event,
		Source:     source,
		ToolCallID: toolCallID,
		Tool:       "shell_run",
	}, time.UnixMilli(1)); err != nil {
		t.Fatalf("append approval event for %s: %v", sessionID, err)
	}
}
