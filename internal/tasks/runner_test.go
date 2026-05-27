package tasks

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/store"
)

func TestParallelReasonPreservesOrderAndAggregatesUsage(t *testing.T) {
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(ctx context.Context, history []core.Message, tools []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				if len(tools) != 0 {
					t.Errorf("parallel_reason tools: want none, got %d", len(tools))
				}
				prompt := history[len(history)-1].Text
				out <- llm.ProviderEvent{
					Type: llm.EventComplete,
					Response: &llm.ProviderResponse{
						Content: "answer:" + prompt,
						Usage:   llm.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
					},
				}
			}()
			return out
		}), nil
	}
	r := NewRunner(RunnerConfig{ProviderFactory: factory})
	res, err := r.ParallelReason(context.Background(), ParallelReasonRequest{Prompts: []string{"b", "a", "c"}})
	if err != nil {
		t.Fatalf("ParallelReason: %v", err)
	}
	for i, prompt := range []string{"b", "a", "c"} {
		if res.Results[i].Index != i || res.Results[i].Prompt != prompt || res.Results[i].Output != "answer:"+prompt {
			t.Fatalf("result[%d] = %+v", i, res.Results[i])
		}
	}
	if res.Usage.TotalTokens != 9 || res.Usage.PromptTokens != 3 || res.Usage.CompletionTokens != 6 {
		t.Fatalf("usage = %+v", res.Usage)
	}
}

func TestParallelReasonCancellation(t *testing.T) {
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(ctx context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent)
			go func() {
				defer close(out)
				<-ctx.Done()
				out <- llm.ProviderEvent{Type: llm.EventError, Err: ctx.Err()}
			}()
			return out
		}), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := NewRunner(RunnerConfig{ProviderFactory: factory})
	res, err := r.ParallelReason(ctx, ParallelReasonRequest{Prompts: []string{"one"}})
	if err != nil {
		t.Fatalf("ParallelReason should return per-result cancellation, got error: %v", err)
	}
	if !strings.Contains(res.Results[0].Error, "context canceled") {
		t.Fatalf("cancel error = %q", res.Results[0].Error)
	}
}

func TestReadOnlyRegistryFiltersMutatingAndTaskTools(t *testing.T) {
	parent := core.NewToolRegistry([]core.Tool{
		testTool{name: "read_file", readOnly: true},
		testTool{name: "write", readOnly: false},
		testTool{name: "apply_patch", readOnly: false},
		testTool{name: "shell_run", readOnly: false, readOnlyCheck: func(args map[string]any) bool { return true }},
		testTool{name: "shell_wait", readOnly: true},
		testTool{name: "todo_add", readOnly: true},
		testTool{name: "parallel_reason", readOnly: true},
	})
	child, err := BuildReadOnlyRegistry(parent)
	if err != nil {
		t.Fatalf("BuildReadOnlyRegistry: %v", err)
	}
	if child.Get("read_file") == nil {
		t.Fatalf("expected read_file")
	}
	for _, name := range []string{"write", "apply_patch", "shell_run", "shell_wait", "todo_add", "parallel_reason"} {
		if child.Get(name) != nil {
			t.Fatalf("expected %s to be filtered", name)
		}
	}
}

func TestSpawnSubagentSummaryTruncation(t *testing.T) {
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{Content: strings.Repeat("x", 20)}}
			}()
			return out
		}), nil
	}
	parent := core.NewToolRegistry([]core.Tool{testTool{name: "read_file", readOnly: true}})
	r := NewRunner(RunnerConfig{ProviderFactory: factory, ParentTools: parent, SummaryMaxChars: 8})
	res, err := r.SpawnSubagent(context.Background(), SpawnSubagentRequest{Task: "inspect"})
	if err != nil {
		t.Fatalf("SpawnSubagent: %v", err)
	}
	if res.Summary != strings.Repeat("x", 8) || !res.Truncated {
		t.Fatalf("summary/truncated = %q %v", res.Summary, res.Truncated)
	}
	if res.Role != "explore" || res.SessionID == "" {
		t.Fatalf("metadata = %+v", res)
	}
}

func TestSpawnSubagentReportsChildToolProgress(t *testing.T) {
	calls := 0
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				calls++
				if calls == 1 {
					out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
						FinishReason: core.FinishReasonToolUse,
						ToolCalls: []core.ToolCall{{
							ID:    "child-read",
							Name:  "read_file",
							Input: `{"file_path":"internal/tasks/runner.go"}`,
						}},
					}}
					return
				}
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
					FinishReason: core.FinishReasonEndTurn,
					Content:      "review complete",
				}}
			}()
			return out
		}), nil
	}
	parent := core.NewToolRegistry([]core.Tool{testTool{name: "read_file", readOnly: true}})
	r := NewRunner(RunnerConfig{ProviderFactory: factory, ParentTools: parent})
	var progress []core.ToolProgress
	res, err := r.SpawnSubagentWithProgress(context.Background(), SpawnSubagentRequest{Task: "inspect", Role: "review"}, func(p core.ToolProgress) {
		progress = append(progress, p)
	})
	if err != nil {
		t.Fatalf("SpawnSubagentWithProgress: %v", err)
	}
	if res.Summary != "review complete" {
		t.Fatalf("summary = %q", res.Summary)
	}
	if len(progress) < 2 {
		t.Fatalf("expected child tool progress, got %+v", progress)
	}
	if progress[0].Status != "running" || progress[0].Role != "review" || !strings.Contains(progress[0].Summary, "internal/tasks/runner.go") {
		t.Fatalf("unexpected first progress: %+v", progress[0])
	}
	if progress[1].Summary != "Read internal/tasks/runner.go" {
		t.Fatalf("unexpected second progress: %+v", progress[1])
	}
}

func TestSpawnSubagentAllowsReadOnlyMCPToolsWithoutApprovalPath(t *testing.T) {
	calls := 0
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				calls++
				if calls == 1 {
					out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
						FinishReason: core.FinishReasonToolUse,
						ToolCalls: []core.ToolCall{{
							ID:    "child-mcp",
							Name:  "mcp__docs_search",
							Input: `{"query":"permissions"}`,
						}},
					}}
					return
				}
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
					FinishReason: core.FinishReasonEndTurn,
					Content:      "done",
				}}
			}()
			return out
		}), nil
	}
	ran := 0
	parent := core.NewToolRegistry([]core.Tool{recordingTool{
		testTool: testTool{name: "mcp__docs_search", readOnly: true},
		ran:      &ran,
	}})
	r := NewRunner(RunnerConfig{ProviderFactory: factory, ParentTools: parent})
	res, err := r.SpawnSubagent(context.Background(), SpawnSubagentRequest{Task: "look it up", Role: "explore"})
	if err != nil {
		t.Fatalf("SpawnSubagent: %v", err)
	}
	// DefaultRules routes mcp "*" through ask; a subagent has no interactive
	// approval path, so without an auto-approving callback the call would be
	// denied and the tool would never run.
	if ran != 1 {
		t.Fatalf("read-only MCP tool ran %d times, want 1", ran)
	}
	if res.Summary != "done" {
		t.Fatalf("summary = %q", res.Summary)
	}
}

func TestSpawnSubagentInheritsAutoCompact(t *testing.T) {
	var histories [][]core.Message
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, history []core.Message, tools []core.Tool) <-chan llm.ProviderEvent {
			histories = append(histories, append([]core.Message(nil), history...))
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				content := "child done"
				if len(tools) == 0 && len(history) > 0 && strings.Contains(history[len(history)-1].Text, "Summarize the conversation") {
					content = "compact summary"
				}
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
					FinishReason: core.FinishReasonEndTurn,
					Content:      content,
				}}
			}()
			return out
		}), nil
	}
	msgStore := store.NewInMemoryStore()
	childSessionID := "parent-session--subagent-tool-1"
	for range 8 {
		_, _ = msgStore.Create(context.Background(), core.Message{
			SessionID: childSessionID,
			Role:      core.RoleUser,
			Text:      strings.Repeat("large prior context ", 80),
		})
	}
	parent := core.NewToolRegistry([]core.Tool{testTool{name: "read_file", readOnly: true}})
	r := NewRunner(RunnerConfig{
		ProviderFactory:      factory,
		ParentTools:          parent,
		MessageStore:         msgStore,
		ParentSessionID:      "parent-session",
		AutoCompact:          true,
		AutoCompactThreshold: 0.01,
	})
	res, err := r.SpawnSubagentWithProgress(context.Background(), SpawnSubagentRequest{
		Task:             "continue inspection",
		Role:             "explore",
		Model:            "legacy-model",
		ParentToolCallID: "tool-1",
	}, func(core.ToolProgress) {})
	if err != nil {
		t.Fatalf("SpawnSubagentWithProgress: %v", err)
	}
	if res.Summary != "child done" {
		t.Fatalf("summary = %q", res.Summary)
	}
	if len(histories) < 2 {
		t.Fatalf("expected compact summary call and child response call, got %d", len(histories))
	}
	first := histories[0]
	if len(first) == 0 || !strings.Contains(first[len(first)-1].Text, "Summarize the conversation") {
		t.Fatalf("expected first provider call to compact child history, got %+v", first)
	}
	msgs, err := msgStore.List(context.Background(), childSessionID)
	if err != nil {
		t.Fatalf("list child messages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected compact summary plus child response, got %+v", msgs)
	}
	if msgs[0].Role != core.RoleUser || msgs[0].Text != "compact summary" || msgs[0].FinishReason != core.FinishReasonEndTurn {
		t.Fatalf("unexpected compact summary message: %+v", msgs[0])
	}
	if msgs[1].Role != core.RoleAssistant || msgs[1].Text != "child done" {
		t.Fatalf("unexpected child response message: %+v", msgs[1])
	}
}

func TestSpawnSubagentDerivesAutoCompactWindowFromChildModel(t *testing.T) {
	var histories [][]core.Message
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, history []core.Message, tools []core.Tool) <-chan llm.ProviderEvent {
			histories = append(histories, append([]core.Message(nil), history...))
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				content := "child done"
				if len(tools) == 0 && len(history) > 0 && strings.Contains(history[len(history)-1].Text, "Summarize the conversation") {
					content = "compact summary"
				}
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
					FinishReason: core.FinishReasonEndTurn,
					Content:      content,
				}}
			}()
			return out
		}), nil
	}
	msgStore := store.NewInMemoryStore()
	childSessionID := "parent-session--subagent-tool-1"
	for range 8 {
		_, _ = msgStore.Create(context.Background(), core.Message{
			SessionID: childSessionID,
			Role:      core.RoleUser,
			Text:      strings.Repeat("large prior context ", 80),
		})
	}
	parent := core.NewToolRegistry([]core.Tool{testTool{name: "read_file", readOnly: true}})
	r := NewRunner(RunnerConfig{
		ProviderFactory:      factory,
		ParentTools:          parent,
		MessageStore:         msgStore,
		ParentSessionID:      "parent-session",
		AutoCompact:          true,
		AutoCompactThreshold: 0.01,
	})
	res, err := r.SpawnSubagentWithProgress(context.Background(), SpawnSubagentRequest{
		Task:             "continue inspection",
		Role:             "explore",
		Model:            defaults.DefaultModel,
		ParentToolCallID: "tool-1",
	}, func(core.ToolProgress) {})
	if err != nil {
		t.Fatalf("SpawnSubagentWithProgress: %v", err)
	}
	if res.Summary != "child done" {
		t.Fatalf("summary = %q", res.Summary)
	}
	if len(histories) != 1 {
		t.Fatalf("expected child response without premature compact, got %d provider calls", len(histories))
	}
	first := histories[0]
	if len(first) == 0 || strings.Contains(first[len(first)-1].Text, "Summarize the conversation") {
		t.Fatalf("expected first provider call to use child model window, got %+v", first)
	}
	msgs, err := msgStore.List(context.Background(), childSessionID)
	if err != nil {
		t.Fatalf("list child messages: %v", err)
	}
	if len(msgs) <= 2 {
		t.Fatalf("expected un-compacted child history to remain, got %+v", msgs)
	}
	if msgs[len(msgs)-1].Role != core.RoleAssistant || msgs[len(msgs)-1].Text != "child done" {
		t.Fatalf("unexpected child response message: %+v", msgs[len(msgs)-1])
	}
}

func TestSpawnSubagentPersistsDurableChildSessionAndMeta(t *testing.T) {
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
					FinishReason: core.FinishReasonEndTurn,
					Content:      "durable review complete",
				}}
			}()
			return out
		}), nil
	}
	dir := t.TempDir()
	msgStore, err := store.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	parent := core.NewToolRegistry([]core.Tool{testTool{name: "read_file", readOnly: true}})
	r := NewRunner(RunnerConfig{
		ProviderFactory: factory,
		ParentTools:     parent,
		MessageStore:    msgStore,
		SessionsDir:     dir,
		ParentSessionID: "parent-session",
	})
	res, err := r.SpawnSubagent(context.Background(), SpawnSubagentRequest{
		Task:             "review internal/tasks",
		Role:             "review",
		ParentToolCallID: "tc-subagent",
	})
	if err != nil {
		t.Fatalf("SpawnSubagent: %v", err)
	}
	if res.SessionID != "parent-session--subagent-tc-subagent" {
		t.Fatalf("session id = %q", res.SessionID)
	}
	if res.PermissionProfile != "read_only" || res.Status != "completed" {
		t.Fatalf("metadata = %+v", res)
	}
	msgs, err := msgStore.List(context.Background(), res.SessionID)
	if err != nil {
		t.Fatalf("list child messages: %v", err)
	}
	if len(msgs) < 2 || msgs[0].Role != core.RoleUser || msgs[len(msgs)-1].Role != core.RoleAssistant {
		t.Fatalf("unexpected child messages: %+v", msgs)
	}
	meta, err := session.LoadSessionMeta(dir, res.SessionID)
	if err != nil {
		t.Fatalf("load child meta: %v", err)
	}
	if meta.Kind != "subagent" || meta.ParentSessionID != "parent-session" || meta.Role != "review" || meta.Status != "completed" {
		t.Fatalf("unexpected meta: %+v", meta)
	}
	if meta.Task != "review internal/tasks" || meta.Summary != "durable review complete" || meta.StartedAt.IsZero() || meta.CompletedAt.IsZero() {
		t.Fatalf("incomplete meta: %+v", meta)
	}
}

func TestSpawnSubagentToolFailureIncludesChildSessionID(t *testing.T) {
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			go func() {
				defer close(out)
				out <- llm.ProviderEvent{Type: llm.EventError, Err: context.Canceled}
			}()
			return out
		}), nil
	}
	dir := t.TempDir()
	msgStore, err := store.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	parent := core.NewToolRegistry([]core.Tool{testTool{name: "read_file", readOnly: true}})
	r := NewRunner(RunnerConfig{
		ProviderFactory: factory,
		ParentTools:     parent,
		MessageStore:    msgStore,
		SessionsDir:     dir,
		ParentSessionID: "parent-session",
	})
	tool := spawnSubagentTool{runner: r}
	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    "tc-fail",
		Name:  "spawn_subagent",
		Input: `{"task":"inspect","role":"review"}`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected tool error: %+v", res)
	}
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok {
		t.Fatalf("parse envelope failed: %s", res.Content)
	}
	if env.Data["child_session_id"] != "parent-session--subagent-tc-fail" {
		t.Fatalf("child session id missing: %+v", env.Data)
	}
	meta, err := session.LoadSessionMeta(dir, "parent-session--subagent-tc-fail")
	if err != nil {
		t.Fatalf("load child meta: %v", err)
	}
	if meta.Status != "failed" && meta.Status != "cancelled" {
		t.Fatalf("unexpected failure meta: %+v", meta)
	}
}

func TestChildToolProgressSummariesIncludeTargetsAndResultMetrics(t *testing.T) {
	call := core.ToolCall{ID: "grep-1", Name: "grep", Input: `{"pattern":"TaskProgress","path":"internal/tui","include":"*.go"}`}
	action := summarizeChildToolCall(call)
	if action.Running != `Searching "TaskProgress" in internal/tui (*.go)` {
		t.Fatalf("running summary = %q", action.Running)
	}
	result := core.ToolResult{
		ToolCallID: "grep-1",
		Name:       "grep",
		Content:    `{"ok":true,"success":true,"data":{"metrics":{"total_matches":7,"files_matched":3},"payload":{"matches":[]}}}`,
	}
	if got := summarizeChildToolResult(result, action); got != `Searched "TaskProgress" in internal/tui (*.go) · 7 matches in 3 files` {
		t.Fatalf("result summary = %q", got)
	}

	web := summarizeChildToolCall(core.ToolCall{ID: "web-1", Name: "web_search", Input: `{"query":"codex subagent ps"}`})
	if web.Running != `Searching web "codex subagent ps"` {
		t.Fatalf("web running summary = %q", web.Running)
	}
	webResult := core.ToolResult{
		ToolCallID: "web-1",
		Name:       "web_search",
		Content:    `{"ok":true,"success":true,"data":{"count":4,"results":[]}}`,
	}
	if got := summarizeChildToolResult(webResult, web); got != `Searched web "codex subagent ps" · 4 results` {
		t.Fatalf("web result summary = %q", got)
	}

	fetch := summarizeChildToolCall(core.ToolCall{ID: "fetch-1", Name: "fetch", Input: `{"url":"https://docs.example.com/path/to/page?x=1"}`})
	if fetch.Running != "Fetching docs.example.com/path/to/page" {
		t.Fatalf("fetch running summary = %q", fetch.Running)
	}
	fetchResult := core.ToolResult{
		ToolCallID: "fetch-1",
		Name:       "fetch",
		Content:    `{"ok":true,"success":true,"data":{"status_code":200}}`,
	}
	if got := summarizeChildToolResult(fetchResult, fetch); got != "Fetched docs.example.com/path/to/page · HTTP 200" {
		t.Fatalf("fetch result summary = %q", got)
	}
}

type providerFunc func(context.Context, []core.Message, []core.Tool) <-chan llm.ProviderEvent

func (f providerFunc) StreamResponse(ctx context.Context, history []core.Message, tools []core.Tool) <-chan llm.ProviderEvent {
	return f(ctx, history, tools)
}

type testTool struct {
	name          string
	readOnly      bool
	readOnlyCheck func(map[string]any) bool
}

func (t testTool) Name() string        { return t.name }
func (t testTool) Description() string { return "test tool" }
func (t testTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": true}
}
func (t testTool) ReadOnly() bool { return t.readOnly }
func (t testTool) ReadOnlyCheck(args map[string]any) bool {
	if t.readOnlyCheck == nil {
		return t.readOnly
	}
	return t.readOnlyCheck(args)
}
func (t testTool) Run(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: `{"ok":true}`}, nil
}

// recordingTool wraps testTool to count how many times it actually executes,
// so a test can tell a real run apart from a policy denial (which never calls
// Run). The counter is a pointer so it survives the tool being copied into a
// registry by value.
type recordingTool struct {
	testTool
	ran *int
}

func (t recordingTool) Run(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	*t.ran++
	return t.testTool.Run(ctx, call)
}

func tc(name string, args map[string]any) core.ToolCall {
	b, _ := json.Marshal(args)
	return core.ToolCall{ID: name + "-" + time.Now().Format("150405.000000"), Name: name, Input: string(b)}
}
