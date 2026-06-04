package workflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/tasks"
)

func TestScriptRunnerStartWorkflowLaunchesAsyncAndRecordsEvents(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent-session", WorkflowInput{
		Args: map[string]any{"topic": "barriers"},
		Script: `export const meta = {
  name: 'hello',
  description: 'run hello',
  phases: [{ title: 'Explore' }],
}

phase('Explore')
log('starting ' + args.topic)
const result = await agent('explain ' + args.topic, {
  label: 'explain',
  phase: 'Explore',
  model: 'deepseek-test',
})
log(result)
`,
	})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	if out.Status != WorkflowStatusAsyncLaunched || out.RunID == "" || out.TaskID == "" {
		t.Fatalf("unexpected output: %+v", out)
	}
	if out.ScriptPath == "" {
		t.Fatalf("expected inline script to be written to disk")
	}
	if _, err := os.Stat(out.ScriptPath); err != nil {
		t.Fatalf("stat script path: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	if run.Summary != "workflow completed" {
		t.Fatalf("summary = %q, want workflow completed", run.Summary)
	}
	events, err := store.List(context.Background(), out.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if countEvents(events, EventScriptReady) != 1 || countEvents(events, EventPhaseStarted) != 1 || countEvents(events, EventLog) != 2 {
		t.Fatalf("missing workflow events: %+v", events)
	}
	var ready RunEvent
	for _, ev := range events {
		if ev.Type == EventScriptReady {
			ready = ev
			break
		}
	}
	if ready.Data["name"] != "hello" {
		t.Fatalf("script ready name = %v, want hello", ready.Data["name"])
	}
	phases, _ := ready.Data["phases"].([]map[string]any)
	if len(phases) != 1 || phases[0]["title"] != "Explore" {
		t.Fatalf("script ready phases = %+v", ready.Data["phases"])
	}
	if countEvents(events, EventTaskStarted) != 1 || countEvents(events, EventTaskCompleted) != 1 {
		t.Fatalf("missing agent task events: %+v", events)
	}
	var task RunEvent
	for _, ev := range events {
		if ev.Type == EventTaskStarted {
			task = ev
			break
		}
	}
	if task.Label != "explain" || task.Phase != "Explore" || task.Message != "explain barriers" {
		t.Fatalf("task event = %+v", task)
	}
}

func TestScriptRunnerScriptPathTakesPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workflow.js")
	if err := os.WriteFile(path, []byte(`export const meta = { name: 'path', description: 'from path' }
log('from path')
`), 0o600); err != nil {
		t.Fatalf("write script: %v", err)
	}
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(dir, manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{
		ScriptPath: path,
		Script:     `export const meta = { name: 'inline', description: 'from inline' }`,
	})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	if out.ScriptPath != path || out.Summary != "from path" {
		t.Fatalf("output = %+v", out)
	}
	waitRunStatus(t, store, out.RunID, RunStatusCompleted)
}

func TestScriptRunnerRejectsInvalidScriptsBeforeRunStart(t *testing.T) {
	tests := []struct {
		name    string
		input   WorkflowInput
		wantErr string
	}{
		{
			name:    "missing meta",
			input:   WorkflowInput{Script: `log('no meta')`},
			wantErr: "export const meta",
		},
		{
			name:    "computed meta",
			input:   WorkflowInput{Script: `export const meta = { name: NAME, description: 'x' }`},
			wantErr: "pure literal",
		},
		{
			name:    "reserved key",
			input:   WorkflowInput{Script: `export const meta = { name: 'x', description: 'x', constructor: 'x' }`},
			wantErr: "constructor",
		},
		{
			name:    "date now",
			input:   WorkflowInput{Script: `export const meta = { name: 'x', description: 'x' }; Date.now()`},
			wantErr: "deterministic",
		},
		{
			name:    "math random",
			input:   WorkflowInput{Script: `export const meta = { name: 'x', description: 'x' }; Math.random()`},
			wantErr: "deterministic",
		},
		{
			name:    "new date",
			input:   WorkflowInput{Script: `export const meta = { name: 'x', description: 'x' }; new Date()`},
			wantErr: "deterministic",
		},
		{
			name:    "host api",
			input:   WorkflowInput{Script: `export const meta = { name: 'x', description: 'x' }; fetch('https://example.com')`},
			wantErr: "fetch is unavailable",
		},
		{
			name:    "syntax",
			input:   WorkflowInput{Script: `export const meta = { name: 'x', description: 'x' }; const =`},
			wantErr: "syntax",
		},
		{
			name:    "named workflow without library",
			input:   WorkflowInput{Name: "demo"},
			wantErr: "workflow library is not configured",
		},
		{
			name:    "resume",
			input:   WorkflowInput{Script: `export const meta = { name: 'x', description: 'x' }`, ResumeFromRunID: "run-old"},
			wantErr: "resume source run not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &memoryRunEventStore{}
			manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
			runner := NewScriptRunner(t.TempDir(), manager)
			out, err := runner.StartWorkflow(context.Background(), "parent", tt.input)
			if err != nil {
				t.Fatalf("StartWorkflow returned transport error: %v", err)
			}
			if !strings.Contains(out.Error, tt.wantErr) {
				t.Fatalf("error = %q, want contains %q", out.Error, tt.wantErr)
			}
			if out.RunID != "" {
				t.Fatalf("invalid script should not start run: %+v", out)
			}
			if len(store.events) != 0 {
				t.Fatalf("invalid script wrote events: %+v", store.events)
			}
		})
	}
}

func TestScriptRunnerResumeReusesCompletedAgentCalls(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	script := `export const meta = { name: 'resume-basic', description: 'resume basic' }
const one = await agent('one')
const two = await agent('two')
log(one + '|' + two)
`
	first, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: script})
	if err != nil {
		t.Fatalf("first StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, first.RunID, RunStatusCompleted)
	if got := spawnerRequests(spawner); len(got) != 2 {
		t.Fatalf("first requests = %+v", got)
	}
	second, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{
		Script:          script,
		ResumeFromRunID: string(first.RunID),
	})
	if err != nil {
		t.Fatalf("second StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, second.RunID, RunStatusCompleted)
	if got := spawnerRequests(spawner); len(got) != 2 {
		t.Fatalf("resume spawned new agents: %+v", got)
	}
	events, err := store.List(context.Background(), second.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := countCachedTaskCompletions(events); got != 2 {
		t.Fatalf("cached task completions = %d, want 2; events=%+v", got, events)
	}
	if !hasLog(events, "summary one|summary two") {
		t.Fatalf("missing cached result log, events=%+v", events)
	}
}

func TestScriptRunnerResumeRerunsFromFirstChangedAgent(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	firstScript := `export const meta = { name: 'resume-change', description: 'resume change' }
await agent('one')
await agent('two')
await agent('three')
`
	first, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: firstScript})
	if err != nil {
		t.Fatalf("first StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, first.RunID, RunStatusCompleted)
	secondScript := `export const meta = { name: 'resume-change', description: 'resume change' }
await agent('one')
await agent('two changed')
await agent('three')
`
	second, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{
		Script:          secondScript,
		ResumeFromRunID: string(first.RunID),
	})
	if err != nil {
		t.Fatalf("second StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, second.RunID, RunStatusCompleted)
	requests := spawnerRequests(spawner)
	if len(requests) != 5 {
		t.Fatalf("requests = %+v, want first three plus two reruns", requests)
	}
	if requests[3].Task != "two changed" || requests[4].Task != "three" {
		t.Fatalf("rerun requests = %+v, want two changed then three", requests[3:])
	}
	events, err := store.List(context.Background(), second.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := countCachedTaskCompletions(events); got != 1 {
		t.Fatalf("cached task completions = %d, want 1; events=%+v", got, events)
	}
}

func TestScriptRunnerResumeIgnoresLabelAndPhaseChanges(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	first, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'resume-label', description: 'resume label' }
await agent('one', { label: 'old-label', phase: 'Old' })
`})
	if err != nil {
		t.Fatalf("first StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, first.RunID, RunStatusCompleted)
	second, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{
		ResumeFromRunID: string(first.RunID),
		Script: `export const meta = { name: 'resume-label', description: 'resume label' }
await agent('one', { label: 'new-label', phase: 'New' })
`,
	})
	if err != nil {
		t.Fatalf("second StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, second.RunID, RunStatusCompleted)
	if got := spawnerRequests(spawner); len(got) != 1 {
		t.Fatalf("label/phase-only change spawned new agent: %+v", got)
	}
	events, err := store.List(context.Background(), second.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := countCachedTaskCompletions(events); got != 1 {
		t.Fatalf("cached task completions = %d, want 1; events=%+v", got, events)
	}
}

func TestScriptRunnerResumeCacheHitsDoNotSpendBudget(t *testing.T) {
	budgetTokens := 1
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{usages: map[string]llm.Usage{
		"one": {CompletionTokens: 7, TotalTokens: 7},
	}}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	script := `export const meta = { name: 'resume-budget', description: 'resume budget' }
await agent('one')
log('after ' + budget.spent() + '/' + budget.remaining())
`
	first, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: script})
	if err != nil {
		t.Fatalf("first StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, first.RunID, RunStatusCompleted)
	second, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{
		Script:          script,
		BudgetTokens:    &budgetTokens,
		ResumeFromRunID: string(first.RunID),
	})
	if err != nil {
		t.Fatalf("second StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, second.RunID, RunStatusCompleted)
	events, err := store.List(context.Background(), second.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if countEvents(events, EventBudgetUpdated) != 0 {
		t.Fatalf("cached run should not emit budget updates, events=%+v", events)
	}
	if !hasLog(events, "after 0/1") {
		t.Fatalf("missing zero-spend budget log, events=%+v", events)
	}
}

func TestScriptRunnerResumeUsesStablePipelineCallKeys(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	script := `export const meta = { name: 'resume-pipeline', description: 'resume pipeline' }
const results = await pipeline(
  ['a', 'b'],
  item => agent('find:' + item)
)
log(results.join('|'))
`
	first, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: script})
	if err != nil {
		t.Fatalf("first StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, first.RunID, RunStatusCompleted)
	second, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{
		Script:          script,
		ResumeFromRunID: string(first.RunID),
	})
	if err != nil {
		t.Fatalf("second StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, second.RunID, RunStatusCompleted)
	if got := spawnerRequests(spawner); len(got) != 2 {
		t.Fatalf("pipeline resume spawned new agents: %+v", got)
	}
	events, err := store.List(context.Background(), second.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := countCachedTaskCompletions(events); got != 2 {
		t.Fatalf("cached task completions = %d, want 2; events=%+v", got, events)
	}
}

func TestScriptRunnerBudgetDefaultsToNullAndInfinity(t *testing.T) {
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{
		Script: `export const meta = { name: 'budget-default', description: 'budget default' }
log(String(budget.total) + '|' + String(budget.spent()) + '|' + String(budget.remaining()))
return { answer: String(budget.remaining()), totalIsNull: budget.total === null, spent: budget.spent() }
`,
	})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	if got := workflowCompletionMessage(run.Events[len(run.Events)-1].Data["result"]); got != "Infinity" {
		t.Fatalf("completion result = %q, want Infinity", got)
	}
	events, err := store.List(context.Background(), out.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !hasLog(events, "null|0|Infinity") {
		t.Fatalf("missing budget default log, events=%+v", events)
	}
}

func TestScriptRunnerBudgetTracksAgentUsage(t *testing.T) {
	budgetTokens := 20
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{usages: map[string]llm.Usage{
		"one": {PromptTokens: 5, CompletionTokens: 7, TotalTokens: 12},
	}}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{
		BudgetTokens: &budgetTokens,
		Script: `export const meta = { name: 'budget-track', description: 'budget track' }
log('before ' + budget.total + '/' + budget.spent() + '/' + budget.remaining())
await agent('one')
log('after ' + budget.spent() + '/' + budget.remaining())
return { answer: String(budget.spent()), remaining: budget.remaining() }
`,
	})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	events, err := store.List(context.Background(), out.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !hasLog(events, "before 20/0/20") || !hasLog(events, "after 7/13") {
		t.Fatalf("missing budget logs, events=%+v", events)
	}
	var budgetEvent RunEvent
	for _, ev := range events {
		if ev.Type == EventBudgetUpdated {
			budgetEvent = ev
			break
		}
	}
	if budgetEvent.Type == "" {
		t.Fatalf("missing budget update event: %+v", events)
	}
	if got := numberValue(t, budgetEvent.Data["spent_tokens"]); got != 7 {
		t.Fatalf("spent_tokens = %d, want 7; event=%+v", got, budgetEvent)
	}
	if got := numberValue(t, budgetEvent.Data["remaining_tokens"]); got != 13 {
		t.Fatalf("remaining_tokens = %d, want 13; event=%+v", got, budgetEvent)
	}
}

func TestScriptRunnerDefaultBudgetAppliesWithoutOverride(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{usages: map[string]llm.Usage{
		"one": {CompletionTokens: 7, TotalTokens: 7},
	}}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{
		Script: `export const meta = { name: 'default-budget', description: 'default budget', defaultBudgetTokens: 20 }
log('before ' + budget.total + '/' + budget.remaining())
await agent('one')
`,
	})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	events, err := store.List(context.Background(), out.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !hasLog(events, "before 20/20") {
		t.Fatalf("missing default budget log, events=%+v", events)
	}
	var ready RunEvent
	var budgetEvent RunEvent
	for _, ev := range events {
		switch ev.Type {
		case EventScriptReady:
			ready = ev
		case EventBudgetUpdated:
			budgetEvent = ev
		}
	}
	if ready.Type == "" {
		t.Fatalf("missing script ready event: %+v", events)
	}
	readyBudget, _ := ready.Data["budget"].(map[string]any)
	if got := numberValue(t, readyBudget["total_budget_tokens"]); got != 20 {
		t.Fatalf("default total budget = %d, want 20; event=%+v", got, ready)
	}
	if budgetEvent.Type == "" {
		t.Fatalf("missing budget update event: %+v", events)
	}
	if got := numberValue(t, budgetEvent.Data["remaining_tokens"]); got != 13 {
		t.Fatalf("remaining_tokens = %d, want 13; event=%+v", got, budgetEvent)
	}
}

func TestScriptRunnerInputBudgetOverridesDefaultBudget(t *testing.T) {
	budgetTokens := 9
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{usages: map[string]llm.Usage{
		"one": {CompletionTokens: 7, TotalTokens: 7},
	}}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{
		BudgetTokens: &budgetTokens,
		Script: `export const meta = { name: 'default-budget', description: 'default budget', defaultBudgetTokens: 20 }
log('before ' + budget.total + '/' + budget.remaining())
await agent('one')
`,
	})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	events, err := store.List(context.Background(), out.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !hasLog(events, "before 9/9") {
		t.Fatalf("missing override budget log, events=%+v", events)
	}
	var budgetEvent RunEvent
	for _, ev := range events {
		if ev.Type == EventBudgetUpdated {
			budgetEvent = ev
			break
		}
	}
	if budgetEvent.Type == "" {
		t.Fatalf("missing budget update event: %+v", events)
	}
	if got := numberValue(t, budgetEvent.Data["remaining_tokens"]); got != 2 {
		t.Fatalf("remaining_tokens = %d, want 2; event=%+v", got, budgetEvent)
	}
}

func TestScriptRunnerBudgetExceededBlocksNextAgent(t *testing.T) {
	budgetTokens := 5
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{usages: map[string]llm.Usage{
		"one": {CompletionTokens: 5, TotalTokens: 5},
		"two": {CompletionTokens: 1, TotalTokens: 1},
	}}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{
		BudgetTokens: &budgetTokens,
		Script: `export const meta = { name: 'budget-stop', description: 'budget stop' }
await agent('one')
await agent('two')
`,
	})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusFailed)
	if !strings.Contains(run.Error, "workflow budget exceeded") {
		t.Fatalf("run error = %q", run.Error)
	}
	spawner.mu.Lock()
	defer spawner.mu.Unlock()
	if len(spawner.requests) != 1 || spawner.requests[0].Task != "one" {
		t.Fatalf("requests = %+v, want only first agent", spawner.requests)
	}
}

func TestScriptRunnerBudgetExceededFailsParallel(t *testing.T) {
	budgetTokens := 5
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{usages: map[string]llm.Usage{
		"one": {CompletionTokens: 5, TotalTokens: 5},
		"two": {CompletionTokens: 1, TotalTokens: 1},
	}}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{
		BudgetTokens: &budgetTokens,
		Script: `export const meta = { name: 'budget-parallel-stop', description: 'budget parallel stop' }
await agent('one')
const results = await parallel([
  () => agent('two'),
])
return { answer: String(results[0] === null) }
`,
	})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusFailed)
	if !strings.Contains(run.Error, "workflow budget exceeded") {
		t.Fatalf("run error = %q", run.Error)
	}
	spawner.mu.Lock()
	defer spawner.mu.Unlock()
	if len(spawner.requests) != 1 || spawner.requests[0].Task != "one" {
		t.Fatalf("requests = %+v, want only first agent", spawner.requests)
	}
}

func TestScriptRunnerBudgetIsSharedWithChildWorkflow(t *testing.T) {
	budgetTokens := 20
	root := t.TempDir()
	childPath := filepath.Join(root, "child.js")
	writeWorkflowFile(t, childPath, `export const meta = { name: 'child', description: 'child workflow' }
await agent('child-agent')
return { answer: String(budget.spent()) }
`)
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{usages: map[string]llm.Usage{
		"child-agent": {CompletionTokens: 9, TotalTokens: 9},
	}}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{
		BudgetTokens: &budgetTokens,
		Script: `export const meta = { name: 'parent', description: 'parent workflow' }
const result = await workflow({ scriptPath: args.childPath })
log('parent spent ' + budget.spent() + ' child ' + result.answer)
`,
		Args: map[string]any{"childPath": childPath},
	})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	events, err := store.List(context.Background(), out.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !hasLog(events, "parent spent 9 child 9") {
		t.Fatalf("missing shared budget log, events=%+v", events)
	}
}

func TestScriptRunnerStartsNamedWorkflowFromLibrary(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "named-workflow.js")
	writeWorkflowFile(t, path, `export const meta = { name: 'named-workflow', description: 'from library' }
log('topic ' + args.topic)
`)
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	runner.Library = NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})

	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{
		Name: "named-workflow",
		Args: map[string]any{"topic": "ok"},
	})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	if out.Status != WorkflowStatusAsyncLaunched || out.ScriptPath != path || out.Summary != "from library" {
		t.Fatalf("output = %+v", out)
	}
	waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	events, err := store.List(context.Background(), out.RunID)
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

func TestScriptRunnerScriptTakesPrecedenceOverName(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, filepath.Join(root, "named-workflow.js"), `export const meta = { name: 'named-workflow', description: 'from library' }
log('library')
`)
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	runner.Library = NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})

	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{
		Name:   "named-workflow",
		Script: `export const meta = { name: 'inline-workflow', description: 'from inline' }`,
	})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	if out.Summary != "from inline" || out.ScriptPath == filepath.Join(root, "named-workflow.js") {
		t.Fatalf("output = %+v", out)
	}
	waitRunStatus(t, store, out.RunID, RunStatusCompleted)
}

func TestScriptRunnerWorkflowCallsNamedChildWithArgs(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, filepath.Join(root, "double.js"), `export const meta = { name: 'double', description: 'double a number' }
return { answer: 'doubled', doubled: args.n * 2 }
`)
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	runner.Library = NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'parent', description: 'parent workflow' }
const result = await workflow('double', { n: 21 })
return { answer: result.answer, doubled: result.doubled }
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	if run.Summary != "doubled" {
		t.Fatalf("summary = %q", run.Summary)
	}
	events, err := store.List(context.Background(), out.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if countEvents(events, EventWorkflowStarted) != 1 || countEvents(events, EventWorkflowCompleted) != 1 {
		t.Fatalf("missing child workflow events: %+v", events)
	}
	for _, ev := range events {
		if ev.Type != EventWorkflowCompleted {
			continue
		}
		if ev.WorkflowName != "double" || ev.TaskID == "" {
			t.Fatalf("workflow completed event = %+v", ev)
		}
		result, _ := ev.Data["result"].(map[string]any)
		if fmt.Sprint(result["doubled"]) != "42" {
			t.Fatalf("child result = %+v", result)
		}
		return
	}
	t.Fatalf("missing workflow_completed event: %+v", events)
}

func TestScriptRunnerWorkflowCallsScriptPathChild(t *testing.T) {
	dir := t.TempDir()
	childPath := filepath.Join(dir, "child.js")
	writeWorkflowFile(t, childPath, `export const meta = { name: 'child', description: 'child from path' }
return { answer: args.value }
`)
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'parent', description: 'parent workflow' }
const result = await workflow({ scriptPath: args.childPath }, { value: 'ok' })
return result
`, Args: map[string]any{"childPath": childPath}})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	if run.Summary != "ok" {
		t.Fatalf("summary = %q", run.Summary)
	}
}

func TestScriptRunnerWorkflowRejectsUnknownNameWithAvailable(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, filepath.Join(root, "known.js"), `export const meta = { name: 'known', description: 'known' }
return { answer: 'known' }
`)
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	runner.Library = NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'parent', description: 'parent workflow' }
await workflow('missing')
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusFailed)
	if !strings.Contains(run.Error, "workflow not found: missing") || !strings.Contains(run.Error, "available workflows:") || !strings.Contains(run.Error, "known") {
		t.Fatalf("run error = %q", run.Error)
	}
}

func TestScriptRunnerWorkflowLimitsNesting(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, filepath.Join(root, "grandchild.js"), `export const meta = { name: 'grandchild', description: 'grandchild' }
return { answer: 'grandchild' }
`)
	writeWorkflowFile(t, filepath.Join(root, "child.js"), `export const meta = { name: 'child', description: 'child' }
return workflow('grandchild')
`)
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	runner.Library = NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'parent', description: 'parent workflow' }
await workflow('child')
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusFailed)
	if !strings.Contains(run.Error, "nesting is limited to one level") {
		t.Fatalf("run error = %q", run.Error)
	}
	events, err := store.List(context.Background(), out.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if countEvents(events, EventWorkflowFailed) != 1 {
		t.Fatalf("expected child workflow failed event, events=%+v", events)
	}
}

func TestScriptRunnerWorkflowSharesAgentCallLimit(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, filepath.Join(root, "child.js"), `export const meta = { name: 'child', description: 'child' }
await agent('child-one')
await agent('child-two')
`)
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	runner.MaxAgentCalls = 1
	runner.Library = NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'parent', description: 'parent workflow' }
await workflow('child')
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusFailed)
	if !strings.Contains(run.Error, "workflow agent call limit exceeded: 1") {
		t.Fatalf("run error = %q", run.Error)
	}
}

func TestScriptRunnerParallelCanRunWorkflowCalls(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, filepath.Join(root, "a.js"), `export const meta = { name: 'a', description: 'a' }
return { value: 'a' }
`)
	writeWorkflowFile(t, filepath.Join(root, "b.js"), `export const meta = { name: 'b', description: 'b' }
return { value: 'b' }
`)
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	runner.Library = NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'parent', description: 'parent workflow' }
const results = await parallel([
  () => workflow('a'),
  () => workflow('b'),
])
log(results.map(x => x.value).join('|'))
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	events, err := store.List(context.Background(), out.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if countEvents(events, EventWorkflowCompleted) != 2 {
		t.Fatalf("expected two child workflow completions, events=%+v", events)
	}
	for _, ev := range events {
		if ev.Type == EventLog && ev.Message == "a|b" {
			return
		}
	}
	t.Fatalf("missing ordered parallel workflow log, events=%+v", events)
}

func TestScriptRunnerPipelineCanRunWorkflowStage(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, filepath.Join(root, "wrap.js"), `export const meta = { name: 'wrap', description: 'wrap' }
return { value: args.item + '-wrapped' }
`)
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	runner.Library = NewLibraryWithRoots([]LibraryRoot{{Path: root, Source: "project", Rank: 0}})
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'parent', description: 'parent workflow' }
const results = await pipeline(
  ['x'],
  item => workflow('wrap', { item }).then(value => ({ answer: value.value + '!' }))
)
return results[0]
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	if run.Summary != "x-wrapped!" {
		t.Fatalf("run summary = %q", run.Summary)
	}
}

func TestScriptRunnerAgentSchemaReturnsStructuredObject(t *testing.T) {
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{
		summaries: map[string]string{
			"claim": `{"claim":"ok","sources":["primary"]}`,
		},
	}))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'schema', description: 'schema supported' }
const result = await agent('claim', {
  schema: {
    type: 'object',
    properties: {
      claim: { type: 'string' },
      sources: { type: 'array', items: { type: 'string' } },
    },
    required: ['claim', 'sources'],
    additionalProperties: false,
  }
})
log(result.claim + ':' + result.sources[0])
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	events, err := store.List(context.Background(), out.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var sawLog, sawStructured bool
	for _, ev := range events {
		if ev.Type == EventLog && ev.Message == "ok:primary" {
			sawLog = true
		}
		if ev.Type == EventTaskCompleted && ev.Data != nil {
			structured, _ := ev.Data["structured_result"].(map[string]any)
			if structured["claim"] == "ok" {
				sawStructured = true
			}
		}
	}
	if !sawLog || !sawStructured {
		t.Fatalf("missing structured schema evidence: sawLog=%v sawStructured=%v events=%+v", sawLog, sawStructured, events)
	}
}

func TestScriptRunnerAgentSchemaPrefersCapturedStructuredResult(t *testing.T) {
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{
		summaries: map[string]string{
			"claim": "Here is a Markdown report, not JSON.",
		},
		structured: map[string]any{
			"claim": map[string]any{"claim": "ok", "sources": []any{"primary"}},
		},
	}))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'schema-tool', description: 'schema tool' }
const result = await agent('claim', {
  schema: {
    type: 'object',
    properties: {
      claim: { type: 'string' },
      sources: { type: 'array', items: { type: 'string' } },
    },
    required: ['claim', 'sources'],
    additionalProperties: false,
  }
})
log(result.claim + ':' + result.sources[0])
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	if !hasLog(run.Events, "ok:primary") {
		t.Fatalf("missing captured structured log: %+v", run.Events)
	}
}

func TestScriptRunnerArgsAreReadOnly(t *testing.T) {
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{
		Args: map[string]any{"topic": "barriers"},
		Script: `export const meta = { name: 'args', description: 'args readonly' }
args.topic = 'changed'
`,
	})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusFailed)
	if !strings.Contains(run.Error, "read-only") {
		t.Fatalf("run error = %q", run.Error)
	}
}

func TestScriptRunnerJSSandboxDoesNotExposeWorkspaceCWD(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "workspace-sentinel.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	t.Chdir(workspace)

	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'cwd-sandbox', description: 'cwd sandbox' }
let files = [];
let osUnavailable = false;
let readError = "";
try {
  if (typeof os === "undefined" || typeof os.readdir !== "function") {
    osUnavailable = true;
  } else {
    files = os.readdir(".");
  }
} catch (e) {
  readError = String(e);
}
return { files, osUnavailable, readError }`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	result, ok := completedRunResult(t, run).(map[string]any)
	if !ok {
		t.Fatalf("result type = %T", completedRunResult(t, run))
	}
	files, ok := result["files"].([]any)
	if !ok {
		t.Fatalf("files type = %T (%v)", result["files"], result["files"])
	}
	for _, item := range files {
		if item == "workspace-sentinel.txt" {
			t.Fatalf("workflow JS runtime observed workspace cwd sentinel: %#v", result)
		}
	}
}

func TestScriptRunnerWatchdogFailsContinuousJSLoop(t *testing.T) {
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	runner.JSTimeout = 50 * time.Millisecond
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'js-loop', description: 'js loop' }
while (true) {}
return { ok: true }`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusFailed)
	if !strings.Contains(run.Error, "workflow JS execution exceeded") {
		t.Fatalf("expected JS watchdog failure, got %q", run.Error)
	}
}

func TestScriptRunnerWatchdogFailsPipelineStageJSLoop(t *testing.T) {
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	runner.JSTimeout = 50 * time.Millisecond
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'pipeline-js-loop', description: 'pipeline js loop' }
await pipeline([1], () => {
  while (true) {}
  return 1
})
return { ok: true }`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusFailed)
	if !strings.Contains(run.Error, "workflow JS execution exceeded") {
		t.Fatalf("expected JS watchdog failure, got %q", run.Error)
	}
}

func TestScriptRunnerWatchdogAllowsLongAgentWait(t *testing.T) {
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{delay: 150 * time.Millisecond}))
	runner := NewScriptRunner(t.TempDir(), manager)
	runner.JSTimeout = 50 * time.Millisecond
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'agent-wait', description: 'agent wait' }
const res = await agent("slow agent");
return { summary: res }`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	result, ok := completedRunResult(t, run).(map[string]any)
	if !ok {
		t.Fatalf("result type = %T", completedRunResult(t, run))
	}
	if got := result["summary"]; got != "summary slow agent" {
		t.Fatalf("summary = %v", got)
	}
}

func TestScriptRunnerAgentSchemaExtractsJSONFromMarkdownResponse(t *testing.T) {
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{
		summaries: map[string]string{
			"claim": "Here is the structured result:\n```json\n{\"claim\":\"ok\",\"sources\":[\"primary\"]}\n```",
		},
	}))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'schema-markdown', description: 'schema markdown' }
const result = await agent('claim', {
  schema: {
    type: 'object',
    properties: {
      claim: { type: 'string' },
      sources: { type: 'array', items: { type: 'string' } },
    },
    required: ['claim', 'sources'],
    additionalProperties: false,
  }
})
log(result.claim + ':' + result.sources[0])
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	if !hasLog(run.Events, "ok:primary") {
		t.Fatalf("missing extracted structured log: %+v", run.Events)
	}
}

func TestScriptRunnerAgentSchemaMismatchFailsRun(t *testing.T) {
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{
		summaries: map[string]string{"claim": `{"claim":42}`},
		usages:    map[string]llm.Usage{"claim": {PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18}},
		delay:     2 * time.Millisecond,
	}))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'schema-fail', description: 'schema fail' }
await agent('claim', {
  schema: {
    type: 'object',
    properties: { claim: { type: 'string' } },
    required: ['claim'],
    additionalProperties: false,
  }
})
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusFailed)
	if !strings.Contains(run.Error, "does not match schema") {
		t.Fatalf("run error = %q", run.Error)
	}
	var failed RunEvent
	for _, ev := range run.Events {
		if ev.Type == EventTaskFailed {
			failed = ev
			break
		}
	}
	if failed.Data == nil {
		t.Fatalf("schema failure event missing metrics data: %+v", run.Events)
	}
	if got, _ := failed.Data["duration_ms"].(int64); got <= 0 {
		t.Fatalf("schema failure duration_ms = %v; event=%+v", failed.Data["duration_ms"], failed)
	}
	usage, _ := failed.Data["usage"].(map[string]any)
	if got, _ := usage["total_usage_tokens"].(int); got != 18 {
		t.Fatalf("schema failure total_usage_tokens = %v; event=%+v", usage["total_usage_tokens"], failed)
	}
}

func TestScriptRunnerUnsupportedSchemaKeywordFailsRun(t *testing.T) {
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'schema-keyword', description: 'schema keyword' }
await agent('claim', { schema: { type: 'object', oneOf: [] } })
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusFailed)
	if !strings.Contains(run.Error, "unsupported schema keyword") {
		t.Fatalf("run error = %q", run.Error)
	}
}

func TestScriptRunnerParallelRunsThunksConcurrently(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{delay: 120 * time.Millisecond}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	start := time.Now()
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'parallel', description: 'parallel supported' }
phase('Research')
const results = await parallel(['a', 'b', 'c'].map(x => () => agent(x, { label: x, phase: 'Research' })))
log(results.join('|'))
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	if elapsed := time.Since(start); elapsed >= 280*time.Millisecond {
		t.Fatalf("expected concurrent workflow agents, elapsed %s", elapsed)
	}
	if spawner.maxActive.Load() < 2 {
		t.Fatalf("expected overlapping agent calls, maxActive=%d", spawner.maxActive.Load())
	}
	events, err := store.List(context.Background(), out.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var sawLog bool
	for _, ev := range events {
		if ev.Type == EventLog && ev.Message == "summary a|summary b|summary c" {
			sawLog = true
		}
	}
	if !sawLog {
		t.Fatalf("expected ordered parallel results log, events=%+v", events)
	}
}

func TestScriptRunnerPipelineRunsItemsThroughStages(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{
		summaries: map[string]string{
			"research:a:0":       "found-a",
			"research:b:1":       "found-b",
			"verify:a:0:found-a": "ok-a",
			"verify:b:1:found-b": "ok-b",
		},
	}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'pipeline', description: 'pipeline supported' }
const results = await pipeline(
  ['a', 'b'],
  (item, original, index) => agent('research:' + original + ':' + index, { label: 'research-' + original }),
  (found, original, index) => agent('verify:' + original + ':' + index + ':' + found, { label: 'verify-' + original }),
  value => value.toUpperCase()
)
log(results.join('|'))
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	events, err := store.List(context.Background(), out.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var sawLog bool
	for _, ev := range events {
		if ev.Type == EventLog && ev.Message == "OK-A|OK-B" {
			sawLog = true
		}
	}
	if !sawLog {
		t.Fatalf("expected ordered pipeline result log, events=%+v", events)
	}
}

func TestScriptRunnerPipelineDoesNotBarrierBetweenStages(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{
		delays: map[string]time.Duration{
			"find:fast":                20 * time.Millisecond,
			"find:slow":                180 * time.Millisecond,
			"verify:summary find:fast": 20 * time.Millisecond,
			"verify:summary find:slow": 20 * time.Millisecond,
		},
	}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'pipeline-overlap', description: 'pipeline overlap' }
await pipeline(
  ['fast', 'slow'],
  item => agent('find:' + item, { label: 'find:' + item }),
  found => agent('verify:' + found, { label: 'verify:' + found })
)
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	events, err := store.List(context.Background(), out.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	verifyFastStarted := -1
	slowCompleted := -1
	for i, ev := range events {
		if ev.Type == EventTaskStarted && ev.Label == "verify:summary find:fast" {
			verifyFastStarted = i
		}
		if ev.Type == EventTaskCompleted && ev.Label == "find:slow" {
			slowCompleted = i
		}
	}
	if verifyFastStarted == -1 || slowCompleted == -1 {
		t.Fatalf("missing expected pipeline events, events=%+v", events)
	}
	if verifyFastStarted > slowCompleted {
		t.Fatalf("pipeline inserted a stage barrier: verifyFastStarted=%d slowCompleted=%d events=%+v", verifyFastStarted, slowCompleted, events)
	}
}

func TestScriptRunnerPipelineThenPostprocessesAgentResult(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{
		summaries: map[string]string{
			"claim:a": `{"claim":"a","sources":["s1"]}`,
		},
	}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'pipeline-then', description: 'pipeline then' }
const results = await pipeline(
  ['a'],
  item => agent('claim:' + item, {
    schema: {
      type: 'object',
      properties: {
        claim: { type: 'string' },
        sources: { type: 'array', items: { type: 'string' } },
      },
      required: ['claim', 'sources'],
      additionalProperties: false,
    },
  }).then(value => ({ answer: value.claim, sourceCount: value.sources.length }))
)
return results[0]
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	if run.Summary != "a" {
		t.Fatalf("run summary = %q", run.Summary)
	}
	events, err := store.List(context.Background(), out.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, ev := range events {
		if ev.Type != EventRunCompleted {
			continue
		}
		result, _ := ev.Data["result"].(map[string]any)
		if result["answer"] != "a" || fmt.Sprint(result["sourceCount"]) != "1" {
			t.Fatalf("result = %+v", result)
		}
		return
	}
	t.Fatalf("missing run_completed event: %+v", events)
}

func TestScriptRunnerPipelineIsolatesItemFailures(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{failPrompt: "bad"}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'pipeline-failures', description: 'pipeline failures' }
const results = await pipeline(
  ['ok', 'bad', 'multi'],
  item => {
    if (item === 'multi') {
      agent('one')
      agent('two')
      return
    }
    return agent(item)
  },
  value => value + ':done'
)
log(JSON.stringify(results))
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	events, err := store.List(context.Background(), out.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, ev := range events {
		if ev.Type == EventLog && ev.Message == `["summary ok:done",null,null]` {
			return
		}
	}
	t.Fatalf("expected failed items to become null without failing run, events=%+v", events)
}

func TestScriptRunnerAgentCatchRecoversFailedParallelCall(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{failPrompt: "bad"}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'catch-parallel', description: 'catch failed agents' }
const results = await parallel([
  () => agent('ok').then(value => ({ answer: value })),
  () => agent('bad').then(value => ({ answer: value })).catch(e => ({ answer: 'fallback', message: e.message })),
])
log(results[0].answer + '|' + results[1].answer + '|' + results[1].message)
return { answer: results[1].answer }
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	if run.Summary != "fallback" {
		t.Fatalf("run summary = %q", run.Summary)
	}
	if !hasLog(run.Events, "summary ok|fallback|boom") {
		t.Fatalf("missing catch recovery log: %+v", run.Events)
	}
}

func TestScriptRunnerParallelNonFatalAgentFailureBecomesNull(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{
		summaries: map[string]string{
			"good": `{"refuted":false}`,
			"bad":  `{"refuted":"not-a-boolean"}`,
		},
	}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'parallel-null', description: 'parallel null' }
const schema = {
  type: 'object',
  properties: { refuted: { type: 'boolean' } },
  required: ['refuted'],
  additionalProperties: false,
}
const results = await parallel([
  () => agent('good', { schema }),
  () => agent('bad', { schema }),
])
log(JSON.stringify(results))
return { answer: String(results[1] === null) }
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	if run.Summary != "true" {
		t.Fatalf("run summary = %q", run.Summary)
	}
	if !hasLog(run.Events, `[{"refuted":false},null]`) {
		t.Fatalf("missing null parallel result log: %+v", run.Events)
	}
}

func TestScriptRunnerParallelResultSupportsThenPostprocess(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'nested-parallel-then', description: 'nested parallel then' }
const results = await parallel([
  () => parallel([
    () => agent('one'),
    () => agent('two'),
  ]).then(rows => ({ count: rows.length, first: rows[0], second: rows[1] })),
])
return { answer: results[0].first + '|' + results[0].second + '|' + results[0].count }
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	if run.Summary != "summary one|summary two|2" {
		t.Fatalf("run summary = %q", run.Summary)
	}
}

func TestScriptRunnerPipelineStageCanReturnParallelResult(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'pipeline-parallel', description: 'pipeline parallel' }
const results = await pipeline(
  ['topic'],
  item => ({ item, searches: ['one', 'two'] }),
  searchResult => parallel(
    searchResult.searches.map(source => () =>
      agent(source).then(value => ({ source, value }))
    )
  )
)
return { answer: results[0].map(row => row.value).join('|') }
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	if run.Summary != "summary one|summary two" {
		t.Fatalf("run summary = %q", run.Summary)
	}
}

func TestScriptRunnerCatchDoesNotSwallowFatalProviderError(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{failPrompt: "bad", failMessage: `deepseek 402: {"error":{"message":"Insufficient Balance"}}`}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'fatal-catch', description: 'fatal catch' }
const results = await parallel([
  () => agent('bad').catch(e => ({ answer: 'fallback', message: e.message })),
])
return { answer: results[0].answer }
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusFailed)
	if !strings.Contains(run.Error, "Insufficient Balance") {
		t.Fatalf("run error = %q", run.Error)
	}
}

func TestBuiltinDeepResearchFatalScopeFailureFailsRun(t *testing.T) {
	script, ok := BuiltinWorkflowScript(BuiltinDeepResearchName)
	if !ok {
		t.Fatalf("missing builtin %q", BuiltinDeepResearchName)
	}
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{
		failPrompt:  "Decompose this research question",
		failMessage: `deepseek 402: {"error":{"message":"Insufficient Balance"}}`,
	}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{
		Args:   "What changed in the Node.js permission model between v20 and v22?",
		Script: script,
	})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusFailed)
	if !strings.Contains(run.Error, "Insufficient Balance") {
		t.Fatalf("run error = %q", run.Error)
	}
	if countEvents(run.Events, EventRunCompleted) != 0 {
		t.Fatalf("fatal provider error must not complete run: %+v", run.Events)
	}
}

func TestBuiltinDeepResearchCompletesThroughSynthesis(t *testing.T) {
	script, ok := BuiltinWorkflowScript(BuiltinDeepResearchName)
	if !ok {
		t.Fatalf("missing builtin %q", BuiltinDeepResearchName)
	}
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{respond: fakeDeepResearchResponse}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{
		Args:   "What changed in the Node.js permission model between v20 and v22?",
		Script: script,
	})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	if !strings.Contains(run.Summary, "permission model changed") {
		t.Fatalf("run summary = %q", run.Summary)
	}
	var result map[string]any
	for _, ev := range run.Events {
		if ev.Type == EventRunCompleted && ev.Data != nil {
			result, _ = ev.Data["result"].(map[string]any)
			break
		}
	}
	if result == nil {
		t.Fatalf("completed event missing result: %+v", run.Events)
	}
	stats, _ := result["stats"].(map[string]any)
	if got := fmt.Sprint(stats["afterSynthesis"]); got != "1" {
		t.Fatalf("afterSynthesis = %#v, want 1; result=%+v", got, result)
	}
	if countEvents(run.Events, EventPhaseStarted) != 3 {
		t.Fatalf("phase count = %d, want Scope/Verify/Synthesize from raw script", countEvents(run.Events, EventPhaseStarted))
	}
	if countEvents(run.Events, EventTaskFailed) != 0 {
		t.Fatalf("successful builtin run had task failures: %+v", run.Events)
	}
}

func TestBuiltinDeepResearchBoundsFetchAndCanSpawnSeventyFiveVerifyAgents(t *testing.T) {
	script, ok := BuiltinWorkflowScript(BuiltinDeepResearchName)
	if !ok {
		t.Fatalf("missing builtin %q", BuiltinDeepResearchName)
	}
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{respond: fakeDeepResearchFanoutResponse}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{
		Args:   "What changed in the Node.js permission model between v20 and v22?",
		Script: script,
	})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	requests := spawnerRequests(spawner)
	var fetches, verifies int
	for _, req := range requests {
		switch {
		case strings.HasPrefix(req.WorkflowTaskLabel, "fetch:"):
			fetches++
		case strings.HasPrefix(req.WorkflowTaskLabel, "v"):
			verifies++
		}
	}
	if fetches != 30 {
		t.Fatalf("fetch tasks = %d, want all high-relevance unique raw-script results", fetches)
	}
	if verifies != 75 {
		t.Fatalf("verify tasks = %d, want twenty-five ranked claims * three voters", verifies)
	}
}

func TestClaudeCodeDeepResearchRawScriptCompatibility(t *testing.T) {
	scriptBytes, err := os.ReadFile(filepath.Join("testdata", "claude_code_deep_research.js"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{respond: fakeDeepResearchFanoutResponse}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{
		Args:   "What changed in the Node.js permission model between v20 and v22?",
		Script: string(scriptBytes),
	})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	if countEvents(run.Events, EventPhaseStarted) != 3 {
		t.Fatalf("phase count = %d, want Scope/Verify/Synthesize from raw script", countEvents(run.Events, EventPhaseStarted))
	}
	requests := spawnerRequests(spawner)
	var searches, fetches, verifies, synthesize int
	for _, req := range requests {
		switch {
		case strings.HasPrefix(req.WorkflowTaskLabel, "search:"):
			searches++
		case strings.HasPrefix(req.WorkflowTaskLabel, "fetch:"):
			fetches++
		case strings.HasPrefix(req.WorkflowTaskLabel, "v"):
			verifies++
		case req.WorkflowTaskLabel == "synthesize":
			synthesize++
		}
	}
	if searches != 5 {
		t.Fatalf("search tasks = %d, want 5", searches)
	}
	if fetches != 30 {
		t.Fatalf("fetch tasks = %d, want all high-relevance unique raw-script results", fetches)
	}
	if verifies != 75 {
		t.Fatalf("verify tasks = %d, want 25 ranked claims * 3 voters", verifies)
	}
	if synthesize != 1 {
		t.Fatalf("synthesize tasks = %d, want 1", synthesize)
	}
}

func fakeDeepResearchResponse(req tasks.SpawnSubagentRequest) (tasks.SpawnSubagentResponse, bool) {
	label := req.WorkflowTaskLabel
	response := tasks.SpawnSubagentResponse{
		SessionID:   "child-" + strings.ReplaceAll(label, ":", "-"),
		Role:        req.Role,
		Model:       req.Model,
		Status:      TaskStatusCompleted,
		Summary:     "ok " + label,
		ToolCalls:   []string{"web_search"},
		DurationMS:  1,
		CompletedAt: time.Now().UTC().Format(time.RFC3339),
		Usage:       llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
	switch {
	case label == "scope":
		response.StructuredResult = map[string]any{
			"question": "What changed in the Node.js permission model between v20 and v22?",
			"summary":  "Compare official docs, release notes, and security hardening.",
			"angles": []any{
				map[string]any{"label": "Official docs", "query": "Node.js v20 v22 permission model docs", "rationale": "API surface"},
				map[string]any{"label": "Release notes", "query": "Node.js permission model v22.13 stable", "rationale": "release deltas"},
				map[string]any{"label": "Security", "query": "Node.js permission model CVE v20 v22", "rationale": "hardening"},
			},
		}
	case strings.HasPrefix(label, "search:"):
		slug := strings.ToLower(strings.NewReplacer("search:", "", " ", "-", "/", "-", ":", "-").Replace(label))
		response.StructuredResult = map[string]any{
			"results": []any{
				map[string]any{
					"url":       "https://nodejs.org/example/" + slug,
					"title":     "Source for " + label,
					"snippet":   "Relevant Node.js permission model source.",
					"relevance": "high",
				},
			},
		}
	case strings.HasPrefix(label, "fetch:"):
		response.ToolCalls = []string{"web_fetch"}
		response.StructuredResult = map[string]any{
			"sourceQuality": "primary",
			"publishDate":   "2026-01-01",
			"claims": []any{
				map[string]any{
					"claim":      "The Node.js permission model changed between v20 and v22 for " + label,
					"quote":      "Permission model change evidence.",
					"importance": "central",
				},
			},
		}
	case strings.HasPrefix(label, "v0:") || strings.HasPrefix(label, "v1:") || strings.HasPrefix(label, "v2:"):
		response.ToolCalls = []string{"web_search"}
		response.StructuredResult = map[string]any{
			"refuted":    false,
			"evidence":   "Supported by primary-source test fixture.",
			"confidence": "high",
		}
	case label == "synthesize":
		response.ToolCalls = nil
		response.StructuredResult = map[string]any{
			"summary": "The Node.js permission model changed between v20 and v22 in flags, scope, and stability.",
			"findings": []any{
				map[string]any{
					"claim":      "The permission model changed across the tested dimensions.",
					"confidence": "high",
					"sources":    []any{"https://nodejs.org/example/official-docs"},
					"evidence":   "Fixture evidence from verified claims.",
					"vote":       "3-0",
				},
			},
			"caveats":       "Fixture-backed workflow test; not live research.",
			"openQuestions": []any{"Which exact patch releases carried each fix?"},
		}
	default:
		return tasks.SpawnSubagentResponse{}, false
	}
	return response, true
}

func fakeDeepResearchFanoutResponse(req tasks.SpawnSubagentRequest) (tasks.SpawnSubagentResponse, bool) {
	label := req.WorkflowTaskLabel
	response := tasks.SpawnSubagentResponse{
		SessionID:   "child-" + strings.ReplaceAll(label, ":", "-"),
		Role:        req.Role,
		Model:       req.Model,
		Status:      TaskStatusCompleted,
		Summary:     "ok " + label,
		ToolCalls:   []string{"web_search"},
		DurationMS:  1,
		CompletedAt: time.Now().UTC().Format(time.RFC3339),
		Usage:       llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
	switch {
	case label == "scope":
		response.StructuredResult = map[string]any{
			"question": "What changed in the Node.js permission model between v20 and v22?",
			"summary":  "Fan-out fixture.",
			"angles": []any{
				map[string]any{"label": "A", "query": "q a"},
				map[string]any{"label": "B", "query": "q b"},
				map[string]any{"label": "C", "query": "q c"},
				map[string]any{"label": "D", "query": "q d"},
				map[string]any{"label": "E", "query": "q e"},
			},
		}
	case strings.HasPrefix(label, "search:"):
		angle := strings.TrimPrefix(label, "search:")
		results := make([]any, 0, 6)
		for i := 0; i < 6; i++ {
			results = append(results, map[string]any{
				"url":       fmt.Sprintf("https://example.com/%s/%d", strings.ToLower(angle), i),
				"title":     fmt.Sprintf("Source %s %d", angle, i),
				"snippet":   "Relevant source.",
				"relevance": "high",
			})
		}
		response.StructuredResult = map[string]any{"results": results}
	case strings.HasPrefix(label, "fetch:"):
		response.ToolCalls = []string{"web_fetch"}
		claims := make([]any, 0, 5)
		for i := 0; i < 5; i++ {
			claims = append(claims, map[string]any{
				"claim":      fmt.Sprintf("Claim %d from %s", i, label),
				"quote":      "Evidence.",
				"importance": "central",
			})
		}
		response.StructuredResult = map[string]any{
			"sourceQuality": "primary",
			"publishDate":   "2026-01-01",
			"claims":        claims,
		}
	case strings.HasPrefix(label, "v0:") || strings.HasPrefix(label, "v1:") || strings.HasPrefix(label, "v2:"):
		response.StructuredResult = map[string]any{
			"refuted":    false,
			"evidence":   "Supported.",
			"confidence": "high",
		}
	case label == "synthesize":
		response.ToolCalls = nil
		response.StructuredResult = map[string]any{
			"summary": "permission model changed",
			"findings": []any{
				map[string]any{"claim": "changed", "confidence": "high", "sources": []any{"https://example.com/a/0"}, "evidence": "verified"},
			},
			"caveats": "none",
		}
	default:
		return tasks.SpawnSubagentResponse{}, false
	}
	return response, true
}

func TestScriptRunnerPassesAgentCapabilities(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'caps', description: 'caps' }
await agent('research', {
  label: 'research',
  max_tool_iters: 7,
  max_tool_calls: 9,
  capabilities: ['web.search', 'web.fetch'],
})
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	spawner.mu.Lock()
	requests := append([]tasks.SpawnSubagentRequest(nil), spawner.requests...)
	spawner.mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("requests = %+v", requests)
	}
	if got := strings.Join(requests[0].Tools, ","); got != "web.search,web.fetch" {
		t.Fatalf("tools = %q", got)
	}
	if requests[0].MaxToolIters != 7 {
		t.Fatalf("max tool iters = %d, want 7", requests[0].MaxToolIters)
	}
	if requests[0].MaxToolCalls != 9 {
		t.Fatalf("max tool calls = %d, want 9", requests[0].MaxToolCalls)
	}
	events, err := store.List(context.Background(), out.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var sawData bool
	for _, ev := range events {
		if ev.Type != EventTaskStarted {
			continue
		}
		caps, _ := ev.Data["capabilities"].([]string)
		allowed, _ := ev.Data["allowed_tools"].([]string)
		maxCalls, _ := ev.Data["max_tool_calls"].(int)
		if strings.Join(caps, ",") == "web.search,web.fetch" && strings.Join(allowed, ",") == "allowed:web.search,allowed:web.fetch" && maxCalls == 9 {
			sawData = true
		}
	}
	if !sawData {
		t.Fatalf("missing task_started capability data: %+v", events)
	}
}

func TestScriptRunnerPassesAgentDefinitionOptions(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'agent-def', description: 'agent def' }
await agent('review local changes', {
  label: 'review:bugs',
  agent: {
    name: 'reviewer',
    description: 'Review local changes',
    whenToUse: 'Use when reviewing local diffs',
    tools: ['workspace.read', 'web.fetch'],
    disallowedTools: ['web.fetch'],
    model: 'deepseek-v4-flash',
    effort: 'high',
    permissionMode: 'read_only',
    maxTurns: 12,
    skills: ['agent-skill'],
    mcpServers: ['docs'],
    hooks: { PreToolUse: [{ matcher: 'read_file', hooks: [{ type: 'command', command: 'echo nested' }] }] },
    initialPrompt: 'Inspect context before acting.',
    memory: 'project',
    background: true,
    isolation: 'none',
  },
  model: 'deepseek-v4-pro',
  effort: 'medium',
  permissionMode: 'ask',
  maxTurns: 4,
  skills: ['override-skill'],
  mcpServers: ['github'],
  hooks: { SubagentStart: [{ command: 'echo top-level' }] },
  initialPrompt: 'Override context first.',
  memory: 'local',
  background: false,
  isolation: 'worktree',
})
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	spawner.mu.Lock()
	requests := append([]tasks.SpawnSubagentRequest(nil), spawner.requests...)
	spawner.mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("requests = %+v", requests)
	}
	req := requests[0]
	if req.Agent.Name != "reviewer" || req.Agent.Description != "Review local changes" || req.Agent.WhenToUse == "" {
		t.Fatalf("agent definition = %+v", req.Agent)
	}
	if req.Model != "deepseek-v4-pro" || req.Agent.Model != "deepseek-v4-pro" {
		t.Fatalf("model not propagated: req=%q agent=%q", req.Model, req.Agent.Model)
	}
	if req.Agent.Effort != "medium" || req.Agent.PermissionMode != "ask" || req.Agent.MaxTurns != 4 || !req.Agent.Background || req.Agent.Isolation != "worktree" {
		t.Fatalf("runtime fields = %+v", req.Agent)
	}
	if strings.Join(req.Agent.Skills, ",") != "override-skill" || req.Agent.InitialPrompt != "Override context first." || req.Agent.Memory != "local" {
		t.Fatalf("agent context fields = %+v", req.Agent)
	}
	if strings.Join(req.Agent.MCPServers, ",") != "github" {
		t.Fatalf("mcp servers = %+v", req.Agent.MCPServers)
	}
	if req.Agent.Hooks == nil {
		t.Fatalf("hooks not propagated: %+v", req.Agent)
	}
	hooks, err := tasks.ResolveAgentHooks(req.Agent)
	if err != nil {
		t.Fatalf("resolve hooks: %v", err)
	}
	if len(hooks) != 1 || hooks[0].Event != "SubagentStart" || hooks[0].Command != "echo top-level" {
		t.Fatalf("hooks = %+v", hooks)
	}
	if strings.Join(req.Tools, ",") != "workspace.read,web.fetch" {
		t.Fatalf("tools = %#v", req.Tools)
	}
	events, err := store.List(context.Background(), out.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var sawStartedData bool
	for _, ev := range events {
		if ev.Type != EventTaskStarted {
			continue
		}
		if got, _ := ev.Data["max_turns"].(int); got != 4 {
			t.Fatalf("max_turns event data = %v", ev.Data["max_turns"])
		}
		if got, _ := ev.Data["background"].(bool); got != true {
			t.Fatalf("background event data = %v", ev.Data["background"])
		}
		sawStartedData = true
	}
	if !sawStartedData {
		t.Fatalf("missing task_started event: %+v", events)
	}
}

func TestWorkflowSpecHashIncludesMaxTurnsAndBackground(t *testing.T) {
	base := AgentTaskSpec{Prompt: "inspect", Role: "review"}
	same, err := workflowSpecHash(base)
	if err != nil {
		t.Fatalf("workflowSpecHash: %v", err)
	}
	withMaxTurns := base
	withMaxTurns.MaxTurns = 2
	maxTurnsHash, err := workflowSpecHash(withMaxTurns)
	if err != nil {
		t.Fatalf("workflowSpecHash maxTurns: %v", err)
	}
	if same == maxTurnsHash {
		t.Fatal("maxTurns did not affect workflow spec hash")
	}
	withBackground := base
	withBackground.Background = true
	backgroundHash, err := workflowSpecHash(withBackground)
	if err != nil {
		t.Fatalf("workflowSpecHash background: %v", err)
	}
	if same == backgroundHash {
		t.Fatal("background did not affect workflow spec hash")
	}
}

func TestScriptRunnerRecordsReturnedResult(t *testing.T) {
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'returns-result', description: 'returns result' }
return { answer: 'final answer', sources: ['https://example.com'] }
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	if run.Summary != "final answer" {
		t.Fatalf("run summary = %q", run.Summary)
	}
	events, err := store.List(context.Background(), out.RunID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, ev := range events {
		if ev.Type != EventRunCompleted {
			continue
		}
		result, _ := ev.Data["result"].(map[string]any)
		if result["answer"] != "final answer" {
			t.Fatalf("result = %+v", result)
		}
		return
	}
	t.Fatalf("missing run_completed event: %+v", events)
}

func TestScriptRunnerParallelPreservesPerAgentCapabilities(t *testing.T) {
	store := &memoryRunEventStore{}
	spawner := &fakeAgentSpawner{}
	manager := NewRunManager(store, NewTaskScheduler(store, spawner))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'parallel-caps', description: 'parallel caps' }
await parallel([
  () => agent('search', { capabilities: ['web.search'] }),
  () => agent('synth', { capabilities: [] }),
])
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	waitRunStatus(t, store, out.RunID, RunStatusCompleted)
	spawner.mu.Lock()
	requests := append([]tasks.SpawnSubagentRequest(nil), spawner.requests...)
	spawner.mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("requests = %+v", requests)
	}
	seen := map[string]string{}
	for _, req := range requests {
		seen[req.Task] = strings.Join(req.Tools, ",")
	}
	if seen["search"] != "web.search" {
		t.Fatalf("search capabilities = %q", seen["search"])
	}
	if _, ok := seen["synth"]; !ok {
		t.Fatalf("missing synth request: %+v", requests)
	}
	if seen["synth"] != "" || requestsWithNilTools(requests, "synth") {
		t.Fatalf("synth should carry explicit empty tools, requests=%+v", requests)
	}
}

func TestScriptRunnerParallelRejectsBarePromises(t *testing.T) {
	store := &memoryRunEventStore{}
	manager := NewRunManager(store, NewTaskScheduler(store, &fakeAgentSpawner{}))
	runner := NewScriptRunner(t.TempDir(), manager)
	out, err := runner.StartWorkflow(context.Background(), "parent", WorkflowInput{Script: `export const meta = { name: 'parallel-bare', description: 'parallel bare' }
await parallel([agent('x')])
`})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}
	run := waitRunStatus(t, store, out.RunID, RunStatusFailed)
	if !strings.Contains(run.Error, "wrap agent calls as () => agent") {
		t.Fatalf("run error = %q", run.Error)
	}
}

func requestsWithNilTools(requests []tasks.SpawnSubagentRequest, task string) bool {
	for _, req := range requests {
		if req.Task == task {
			return req.Tools == nil
		}
	}
	return false
}

func spawnerRequests(spawner *fakeAgentSpawner) []tasks.SpawnSubagentRequest {
	spawner.mu.Lock()
	defer spawner.mu.Unlock()
	return append([]tasks.SpawnSubagentRequest(nil), spawner.requests...)
}

func countCachedTaskCompletions(events []RunEvent) int {
	var count int
	for _, ev := range events {
		if ev.Type != EventTaskCompleted || ev.Data == nil {
			continue
		}
		if cached, _ := ev.Data["cached"].(bool); cached {
			count++
			continue
		}
		resumeData, _ := ev.Data["resume"].(map[string]any)
		if cached, _ := resumeData["cached"].(bool); cached {
			count++
		}
	}
	return count
}

func hasLog(events []RunEvent, message string) bool {
	for _, ev := range events {
		if ev.Type == EventLog && ev.Message == message {
			return true
		}
	}
	return false
}

func numberValue(t *testing.T, value any) int64 {
	t.Helper()
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case int32:
		return int64(v)
	case float64:
		return int64(v)
	default:
		t.Fatalf("value %T(%v) is not numeric", value, value)
		return 0
	}
}

func waitRunStatus(t *testing.T, store RunEventStore, runID RunID, status string) Run {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var run Run
	var err error
	for time.Now().Before(deadline) {
		run, err = store.LoadRun(context.Background(), runID)
		if err != nil {
			t.Fatalf("LoadRun: %v", err)
		}
		if run.Status == status {
			return run
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach %s; last=%+v err=%v", runID, status, run, err)
	return Run{}
}

func completedRunResult(t *testing.T, run Run) any {
	t.Helper()
	for _, ev := range run.Events {
		if ev.Type == EventRunCompleted {
			return ev.Data["result"]
		}
	}
	t.Fatalf("completed result not found in run events: %+v", run.Events)
	return nil
}
