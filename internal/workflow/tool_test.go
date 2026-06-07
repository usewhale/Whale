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

func TestWorkflowToolDescriptionIsStableResolverGuidance(t *testing.T) {
	desc := NewTool(nil).Description()
	for _, want := range []string{
		"Official Whale workflow resolver and launcher",
		"Use status",
		"Use run, or omit action",
		"Do not inspect .whale/workflows",
		"workflow_disabled",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q:\n%s", want, desc)
		}
	}
	for _, unexpected := range []string{
		"Claude Code compatibility",
		"tool-scoped workers",
		"Use phase('Name')",
		"Call agent(prompt",
	} {
		if strings.Contains(desc, unexpected) {
			t.Fatalf("description should not include long authoring guidance %q:\n%s", unexpected, desc)
		}
	}
}

func TestWorkflowToolParametersExposeActionWithoutAddingTools(t *testing.T) {
	params := NewTool(nil).Parameters()
	props, _ := params["properties"].(map[string]any)
	action, _ := props["action"].(map[string]any)
	values, _ := action["enum"].([]string)
	if action["type"] != "string" || strings.Join(values, ",") != "status,list,resolve,run,create" {
		t.Fatalf("unexpected action schema: %+v", action)
	}
	if desc := strings.TrimSpace(core.AsString(action["description"])); !strings.Contains(desc, "Omit for backward-compatible run") {
		t.Fatalf("unexpected action description: %q", desc)
	}
}

func TestWorkflowToolStatusWorksWhenDisabled(t *testing.T) {
	root := t.TempDir()
	tool := NewToolWithOptions(nil, ToolOptions{
		Enabled: false,
		Library: NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}}),
	})

	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    "tool-1",
		Name:  "workflow",
		Input: `{"action":"status"}`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("status should not be a tool error: %s", res.Content)
	}
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok || !env.Success || env.Code != "workflow_disabled" {
		t.Fatalf("unexpected envelope: %s", res.Content)
	}
	if env.Summary != workflowDisabledUserMessage {
		t.Fatalf("disabled status should expose a short user-facing summary, got %q", env.Summary)
	}
	if env.Data["enabled"] != false || env.Data["canRun"] != false {
		t.Fatalf("unexpected status data: %+v", env.Data)
	}
	if _, hasRoots := env.Data["roots"]; hasRoots {
		t.Fatalf("disabled status should not expose workflow roots: %+v", env.Data)
	}
	if env.Data["workflowDirectoriesHidden"] != true {
		t.Fatalf("disabled status should hide workflow directories: %+v", env.Data)
	}
	if env.Data["brandName"] != "Whale" || env.Data["forbiddenBrand"] != "Whisper" {
		t.Fatalf("disabled status should pin the Whale brand: %+v", env.Data)
	}
	if env.Data["autoEnable"] != false {
		t.Fatalf("disabled status should disallow auto-enable: %+v", env.Data)
	}
	if strings.Contains(res.Content, ".whale/config.local.toml") || strings.Contains(res.Content, "set [workflows]") {
		t.Fatalf("disabled status should not instruct config-file edits: %s", res.Content)
	}
	if !strings.Contains(res.Content, "read or edit Whale configuration") {
		t.Fatalf("disabled status should tell the model not to mutate config: %s", res.Content)
	}
}

func TestWorkflowToolListReturnsDisabledWithoutDirectoryDiscovery(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, filepath.Join(root, "disabled-scan.js"), `export const meta = { name: 'disabled-scan', description: 'disabled scan' }
log('disabled')
`)
	tool := NewToolWithOptions(nil, ToolOptions{
		Enabled: false,
		Library: NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}}),
	})

	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    "tool-1",
		Name:  "workflow",
		Input: `{"action":"list"}`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError {
		t.Fatalf("disabled list should be a tool error: %s", res.Content)
	}
	if res.Metadata["abort_turn_after_tool_result"] != true {
		t.Fatalf("disabled list should request turn abort, got metadata: %+v", res.Metadata)
	}
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok || env.Success || env.Code != "workflow_disabled" {
		t.Fatalf("unexpected envelope: %s", res.Content)
	}
	if _, hasWorkflows := env.Data["workflows"]; hasWorkflows {
		t.Fatalf("disabled list should not expose scanned workflows: %+v", env.Data)
	}
	if strings.Contains(res.Content, "disabled-scan") {
		t.Fatalf("disabled list should not scan workflow definitions: %s", res.Content)
	}
	if strings.Contains(res.Content, ".whale/config.local.toml") || strings.Contains(res.Content, "set [workflows]") {
		t.Fatalf("disabled list should not instruct config-file edits: %s", res.Content)
	}
	if !strings.Contains(res.Content, "edit configuration") {
		t.Fatalf("disabled list should tell the model not to mutate config: %s", res.Content)
	}
}

func TestWorkflowToolRunReturnsDisabledWithoutConfigMutationGuidance(t *testing.T) {
	tool := NewToolWithOptions(nil, ToolOptions{Enabled: false})

	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    "tool-1",
		Name:  "workflow",
		Input: `{"action":"run","name":"dead-code-scan"}`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError {
		t.Fatalf("disabled run should be a tool error: %s", res.Content)
	}
	if res.Metadata["abort_turn_after_tool_result"] != true {
		t.Fatalf("disabled run should request turn abort, got metadata: %+v", res.Metadata)
	}
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok || env.Success || env.Code != "workflow_disabled" {
		t.Fatalf("unexpected envelope: %s", res.Content)
	}
	if env.Error != workflowDisabledUserMessage {
		t.Fatalf("disabled run should expose a short user-facing error, got %q", env.Error)
	}
	if env.Data["autoEnable"] != false {
		t.Fatalf("disabled run should disallow auto-enable: %+v", env.Data)
	}
	if env.Data["fallbackAllowed"] != false {
		t.Fatalf("disabled run should disallow fallback suggestions: %+v", env.Data)
	}
	if env.Data["brandName"] != "Whale" || env.Data["forbiddenBrand"] != "Whisper" {
		t.Fatalf("disabled run should pin the Whale brand: %+v", env.Data)
	}
	if guidance := core.AsString(env.Data["modelGuidance"]); !strings.Contains(guidance, "Do not say Whisper") || !strings.Contains(guidance, "Do not ask what to do next") || !strings.Contains(guidance, "later user request") || !strings.Contains(guidance, "call workflow again") {
		t.Fatalf("disabled run should keep model-only guidance in data: %+v", env.Data)
	}
	for _, unexpected := range []string{".whale/config.local.toml", "set [workflows]", "enabled = true", "unless the user explicitly asks", "tell me", "I can help after"} {
		if strings.Contains(res.Content, unexpected) {
			t.Fatalf("disabled run should not instruct config mutation via %q: %s", unexpected, res.Content)
		}
	}
	for _, want := range []string{"Reply only", "Dynamic workflows are disabled in Whale", "Do not say Whisper", "Do not ask what to do next", "present choices", "inspect workflow directories", "edit configuration", "retry within the same turn", "shell/manual substitutes", "later user request", "call workflow again"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("disabled run missing %q: %s", want, res.Content)
		}
	}
}

func TestWorkflowToolListsWorkflowsWithRoots(t *testing.T) {
	projectRoot := t.TempDir()
	userRoot := t.TempDir()
	missingRoot := filepath.Join(t.TempDir(), "missing-workflows")
	writeWorkflowFile(t, filepath.Join(projectRoot, "project-scan.js"), `export const meta = { name: 'project-scan', description: 'project scan' }
log('project')
`)
	writeWorkflowFile(t, filepath.Join(userRoot, "user-scan.js"), `export const meta = { name: 'user-scan', description: 'user scan' }
log('user')
`)
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	runner.Library = NewLibraryWithRoots([]LibraryRoot{
		{Path: projectRoot, Source: "project", Rank: 0},
		{Path: userRoot, Source: "user", Rank: 1},
		{Path: missingRoot, Source: "global", Rank: 2},
	})
	tool := NewTool(runner, func() string { return "parent-session" })

	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    "tool-1",
		Name:  "workflow",
		Input: `{"action":"list"}`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("list should not be a tool error: %s", res.Content)
	}
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok || !env.Success || env.Code != "workflow_list" {
		t.Fatalf("unexpected envelope: %s", res.Content)
	}
	available, _ := env.Data["available"].([]any)
	if !containsAnyString(available, "project-scan") || !containsAnyString(available, "user-scan") {
		t.Fatalf("available workflows missing expected names: %+v", env.Data["available"])
	}
	roots, _ := env.Data["roots"].([]any)
	if !rootStatusExists(roots, missingRoot, "missing") {
		t.Fatalf("missing root status not returned: %+v", env.Data["roots"])
	}
	if _, hasMeta := res.Metadata["workflow_confirmation_required"]; hasMeta {
		t.Fatalf("list should not request workflow confirmation metadata: %+v", res.Metadata)
	}
	if len(store.events) != 0 {
		t.Fatalf("list should not start run: %+v", store.events)
	}
}

func TestWorkflowToolResolvesNamedWorkflowWithoutConfirmation(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, filepath.Join(root, "named-resolve.js"), `export const meta = { name: 'named-resolve', description: 'resolve only' }
log('ok')
`)
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	runner.Library = NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})
	tool := NewTool(runner, func() string { return "parent-session" })

	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    "tool-1",
		Name:  "workflow",
		Input: `{"action":"resolve","name":"named-resolve"}`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.IsError {
		t.Fatalf("resolve should not be a tool error: %s", res.Content)
	}
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok || !env.Success || env.Code != "workflow_resolved" {
		t.Fatalf("unexpected envelope: %s", res.Content)
	}
	workflowData, _ := env.Data["workflow"].(map[string]any)
	if workflowData["name"] != "named-resolve" || workflowData["source"] != "project" {
		t.Fatalf("unexpected workflow data: %+v", workflowData)
	}
	if _, hasMeta := res.Metadata["workflow_confirmation_required"]; hasMeta {
		t.Fatalf("resolve should not request workflow confirmation metadata: %+v", res.Metadata)
	}
	if len(store.events) != 0 {
		t.Fatalf("resolve should not start run: %+v", store.events)
	}
}

func TestWorkflowToolResolveMissingReturnsAvailableNames(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, filepath.Join(root, "known-workflow.js"), `export const meta = { name: 'known-workflow', description: 'known' }
log('ok')
`)
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	runner.Library = NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})
	tool := NewTool(runner, func() string { return "parent-session" })

	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    "tool-1",
		Name:  "workflow",
		Input: `{"action":"resolve","name":"missing-workflow"}`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError {
		t.Fatalf("missing resolve should be a tool error: %s", res.Content)
	}
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok || env.Success || env.Code != "workflow_not_found" {
		t.Fatalf("unexpected envelope: %s", res.Content)
	}
	if !strings.Contains(env.Message, "known-workflow") {
		t.Fatalf("missing resolve should list available workflows, got %q", env.Message)
	}
	available, _ := env.Data["available"].([]any)
	if !containsAnyString(available, "known-workflow") {
		t.Fatalf("available workflows missing known-workflow: %+v", env.Data["available"])
	}
	if len(store.events) != 0 {
		t.Fatalf("resolve missing should not start run: %+v", store.events)
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

func TestWorkflowToolCreateRequiresScriptAndSaveAs(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, filepath.Join(root, "dead-code-scan.js"), `export const meta = { name: 'dead-code-scan', description: 'scan' }
log('scan')
`)
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	runner.Library = NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})
	tool := NewTool(runner, func() string { return "parent-session" })

	res, err := tool.Run(context.Background(), core.ToolCall{
		ID:    "tool-1",
		Name:  "workflow",
		Input: `{"action":"create","name":"dead-code-scan"}`,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected create without script/saveAs to fail, got: %s", res.Content)
	}
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok || env.Code != "invalid_input" {
		t.Fatalf("expected invalid_input envelope, got: %s", res.Content)
	}
	if _, hasMeta := res.Metadata["workflow_confirmation_required"]; hasMeta {
		t.Fatalf("malformed create must not request workflow confirmation: %+v", res.Metadata)
	}
	if strings.Contains(res.Content, "workflow_confirmation_required") {
		t.Fatalf("malformed create must not fall through to launch confirmation: %s", res.Content)
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

func containsAnyString(values []any, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func rootStatusExists(values []any, path, status string) bool {
	for _, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if item["path"] == path && item["status"] == status {
			return true
		}
	}
	return false
}
