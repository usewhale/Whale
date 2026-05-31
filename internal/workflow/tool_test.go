package workflow

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func TestWorkflowToolLaunchesWorkflow(t *testing.T) {
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	tool := NewTool(runner, func() string { return "parent-session" })

	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    "tool-1",
		Name:  "workflow",
		Input: `{"script":"export const meta = { name: 'wf', description: 'run wf' }\nlog('ok')"}`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok || !env.Success {
		t.Fatalf("unexpected envelope: %s", res.Content)
	}
	if env.Data["status"] != WorkflowStatusAsyncLaunched || env.Data["runId"] == "" || env.Data["taskId"] == "" {
		t.Fatalf("unexpected workflow data: %+v", env.Data)
	}
	runID := RunID(env.Data["runId"].(string))
	waitRunStatus(t, store, runID, RunStatusCompleted)
	events, err := store.List(context.Background(), runID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) == 0 || events[0].SessionID != "parent-session" {
		t.Fatalf("expected parent session on run start, events=%+v", events)
	}
}

func TestWorkflowToolDescriptionPrefersNamedCatalogWorkflows(t *testing.T) {
	desc := NewTool(nil).Description()
	for _, want := range []string{
		"names/describes an available workflow",
		"Do not first inspect files",
		"include args only when the user supplied useful input",
		"Do not ask for a missing args value",
		"Use ordinary tools instead for a single quick read",
		"/workflows opens the workflow panel",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q:\n%s", want, desc)
		}
	}
}

func TestWorkflowToolLaunchesNamedWorkflow(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, filepath.Join(root, "named-tool.js"), `export const meta = { name: 'named-tool', description: 'tool named' }
log('topic ' + args.topic)
`)
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	runner.Library = NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})
	tool := NewTool(runner, func() string { return "parent-session" })

	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    "tool-1",
		Name:  "workflow",
		Input: `{"name":"named-tool","args":{"topic":"ok"}}`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok || !env.Success {
		t.Fatalf("unexpected envelope: %s", res.Content)
	}
	runID := RunID(env.Data["runId"].(string))
	waitRunStatus(t, store, runID, RunStatusCompleted)
	events, err := store.List(context.Background(), runID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var found bool
	for _, ev := range events {
		if ev.Type == EventLog && ev.Message == "topic ok" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected named workflow log event, events=%+v", events)
	}
}

func TestWorkflowToolLaunchesNamedWorkflowWithStringArgs(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, filepath.Join(root, "string-args.js"), `export const meta = { name: 'string-args', description: 'tool string args' }
log('question ' + args)
`)
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	runner.Library = NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})
	tool := NewTool(runner, func() string { return "parent-session" })

	params := tool.Parameters()
	props, _ := params["properties"].(map[string]any)
	argsSchema, _ := props["args"].(map[string]any)
	if _, hasType := argsSchema["type"]; hasType {
		t.Fatalf("workflow args schema should not force object args: %+v", argsSchema)
	}
	if desc := strings.TrimSpace(core.AsString(argsSchema["description"])); !strings.Contains(desc, "Optional JSON-serializable args") || !strings.Contains(desc, "Omit this field") {
		t.Fatalf("workflow args schema should describe args as optional, got: %q", desc)
	}

	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    "tool-1",
		Name:  "workflow",
		Input: `{"name":"string-args","args":"What changed in Node.js permissions?"}`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok || !env.Success {
		t.Fatalf("unexpected envelope: %s", res.Content)
	}
	runID := RunID(env.Data["runId"].(string))
	waitRunStatus(t, store, runID, RunStatusCompleted)
	events, err := store.List(context.Background(), runID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !hasLog(events, "question What changed in Node.js permissions?") {
		t.Fatalf("expected string args workflow log event, events=%+v", events)
	}
}

func TestWorkflowToolReturnsRejectedScriptAsToolError(t *testing.T) {
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	tool := NewTool(NewScriptRunner(t.TempDir(), manager))
	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    "tool-1",
		Name:  "workflow",
		Input: `{"script":"log('no meta')"}`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected tool error")
	}
	if !strings.Contains(res.Content, "export const meta") {
		t.Fatalf("unexpected error content: %s", res.Content)
	}
	if len(store.events) != 0 {
		t.Fatalf("rejected script should not start run: %+v", store.events)
	}
}
