package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func TestWorkflowToolRejectsUnsavedScriptLaunch(t *testing.T) {
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
	if !res.IsError || !strings.Contains(res.Content, "saved as a named workflow") {
		t.Fatalf("expected unsaved script confirmation error, got: %+v", res)
	}
	if len(store.events) != 0 {
		t.Fatalf("unsaved script should not start run: %+v", store.events)
	}
}

func TestWorkflowToolDescriptionPrefersNamedCatalogWorkflows(t *testing.T) {
	desc := NewTool(nil).Description()
	for _, want := range []string{
		"names/describes an available workflow",
		"create, generate, or write a new workflow",
		"do not inspect existing workflow directories or load skills first",
		"set saveAs",
		"Claude Code compatibility",
		"tool-scoped workers",
		"Use phase('Name') only as a statement",
		"Await async workflow primitives before reading their results",
		"Use agent(prompt, { label, phase, schema",
		"Do not set opts.model",
		"returning a final JSON-serializable result",
		"Do not call request_user_input",
		"single TUI launch confirmation",
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

func TestWorkflowToolDefersGeneratedWorkflowSaveUntilConfirmation(t *testing.T) {
	root := t.TempDir()
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	runner.Library = NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})
	tool := NewTool(runner, func() string { return "parent-session" })
	script := `export const meta = { name: 'generated-review', description: 'generated review' }
log('saved ' + args.topic)
`
	b, err := json.Marshal(map[string]any{
		"script": script,
		"saveAs": "generated-review",
		"args":   map[string]any{"topic": "ok"},
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	res, err := tool.Run(context.Background(), core.ToolCall{ID: "tool-1", Name: "workflow", Input: string(b)})
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
	if env.Code != "workflow_confirmation_required" || env.Data["workflowName"] != "generated-review" {
		t.Fatalf("expected confirmation envelope, got: %+v", env)
	}
	wantPath := filepath.Join(root, "generated-review.js")
	if env.Data["scriptPath"] != wantPath {
		t.Fatalf("scriptPath = %v, want %s", env.Data["scriptPath"], wantPath)
	}
	if env.Data["workflowArgs"] != `{"topic":"ok"}` {
		t.Fatalf("workflowArgs = %v", env.Data["workflowArgs"])
	}
	if env.Data["workflowScript"] == "" || env.Data["workflowSaveAs"] != "generated-review" {
		t.Fatalf("missing pending save data: %+v", env.Data)
	}
	if _, err := os.Stat(wantPath); !os.IsNotExist(err) {
		t.Fatalf("generated workflow should not be written before confirmation, stat err=%v", err)
	}
	if len(store.events) != 0 {
		t.Fatalf("confirmation should not start run: %+v", store.events)
	}
}

func TestWorkflowToolConfirmsScriptPathWorkflow(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "custom-workflow.js")
	writeWorkflowFile(t, path, `export const meta = { name: 'custom-workflow', description: 'custom file workflow' }
log('topic ' + args.topic)
`)
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	tool := NewTool(runner, func() string { return "parent-session" })

	input, err := json.Marshal(map[string]any{
		"scriptPath": path,
		"args":       map[string]any{"topic": "ok"},
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	res, err := tool.Run(context.Background(), core.ToolCall{ID: "tool-1", Name: "workflow", Input: string(input)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok || !env.Success || env.Code != "workflow_confirmation_required" {
		t.Fatalf("unexpected envelope: %s", res.Content)
	}
	if env.Data["workflowName"] != "custom-workflow" || env.Data["workflowScriptPath"] != path || env.Data["scriptPath"] != path {
		t.Fatalf("unexpected workflow data: %+v", env.Data)
	}
	if len(store.events) != 0 {
		t.Fatalf("confirmation should not start run: %+v", store.events)
	}
}

func TestWorkflowToolConfirmsNamedWorkflow(t *testing.T) {
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
	if env.Code != "workflow_confirmation_required" || env.Data["workflowName"] != "named-tool" || env.Data["workflowArgs"] != `{"topic":"ok"}` {
		t.Fatalf("expected confirmation data, got: %+v", env)
	}
	if res.Metadata["abort_turn_after_tool_result"] != true {
		t.Fatalf("expected workflow confirmation to request turn abort, got metadata: %+v", res.Metadata)
	}
	if len(store.events) != 0 {
		t.Fatalf("confirmation should not start run: %+v", store.events)
	}
}

func TestWorkflowToolConfirmsNamedWorkflowWithStringArgs(t *testing.T) {
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
	if env.Code != "workflow_confirmation_required" || env.Data["workflowName"] != "string-args" || env.Data["workflowArgs"] != "What changed in Node.js permissions?" {
		t.Fatalf("expected confirmation data, got: %+v", env)
	}
	if len(store.events) != 0 {
		t.Fatalf("confirmation should not start run: %+v", store.events)
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
