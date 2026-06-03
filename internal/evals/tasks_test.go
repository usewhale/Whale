package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func TestTaskSearchReadEditFlow(t *testing.T) {
	run, err := RunTask(context.Background(), TaskSpec{
		ID:          "search-read-edit",
		Description: "find a target file, inspect it, and patch one line",
		Scenario: ScenarioSpec{
			Setup: func(root string) error {
				if err := os.MkdirAll(filepath.Join(root, "cmd", "app"), 0o755); err != nil {
					return err
				}
				content := "package main\n\nfunc main() {\n\tprintln(\"old-value\")\n}\n"
				return os.WriteFile(filepath.Join(root, "cmd", "app", "main.go"), []byte(content), 0o644)
			},
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "search", ToolName: "search_files", Input: `{"path":".","pattern":"main.go","limit":20}`},
						{ID: "read", ToolName: "read_file", Input: `{"file_path":"cmd/app/main.go"}`},
					},
				},
				{
					Steps: []StepSpec{
						{
							ID:       "edit",
							ToolName: "edit",
							Input:    `{"file_path":"cmd/app/main.go","search":"old-value","replace":"new-value"}`,
						},
					},
				},
			},
			Verify: func(run *Run) error {
				search := run.FindStep("search")
				if search == nil {
					return fmt.Errorf("missing search step")
				}
				items, ok := envelopeStringSlice(search.Envelope, "payload", "items")
				if !ok || len(items) == 0 || items[0] != "cmd/app/main.go" {
					return fmt.Errorf("unexpected search items: %+v", items)
				}
				read := run.FindStep("read")
				if read == nil || !strings.Contains(read.Result.Content, "old-value") {
					return fmt.Errorf("read step missing original content")
				}
				b, err := os.ReadFile(filepath.Join(run.Root, "cmd", "app", "main.go"))
				if err != nil {
					return err
				}
				if !strings.Contains(string(b), "new-value") {
					return fmt.Errorf("file was not edited")
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if run.FindStep("edit") == nil {
		t.Fatal("expected edit step")
	}
}

func TestTaskBackgroundShellWaitFlow(t *testing.T) {
	run, err := RunTask(context.Background(), TaskSpec{
		ID:          "background-shell-wait",
		Description: "start a background shell task and wait for completion",
		Scenario: ScenarioSpec{
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "start", ToolName: "shell_run", Input: `{"command":"printf bg-eval","background":true}`},
					},
				},
				{
					Steps: []StepSpec{
						{
							ID:       "wait",
							ToolName: "shell_wait",
							InputFunc: func(history []core.Message) (string, error) {
								taskID, err := shellTaskIDFromHistory(history)
								if err != nil {
									return "", err
								}
								return fmt.Sprintf(`{"task_id":%q,"timeout_ms":5000}`, taskID), nil
							},
						},
					},
				},
			},
			Verify: func(run *Run) error {
				wait := run.FindStep("wait")
				if wait == nil {
					return fmt.Errorf("missing wait step")
				}
				if !strings.Contains(wait.Result.Content, "bg-eval") {
					return fmt.Errorf("wait result missing stdout")
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	start := run.FindStep("start")
	if start == nil || !strings.Contains(start.Result.Content, "task_id") {
		t.Fatal("expected task_id from background shell start")
	}
}

func TestTaskRejectsWorkspaceEscape(t *testing.T) {
	_, err := RunTask(context.Background(), TaskSpec{
		ID:          "reject-escape",
		Description: "reject a path that escapes the workspace",
		Scenario: ScenarioSpec{
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "escape", ToolName: "read_file", Input: `{"file_path":"../outside.txt","offset":0,"limit":20}`, ExpectError: true},
					},
				},
			},
			Verify: func(run *Run) error {
				step := run.FindStep("escape")
				if step == nil {
					return fmt.Errorf("missing escape step")
				}
				if step.Envelope.Code != "permission_denied" {
					return fmt.Errorf("unexpected error code: %s", step.Envelope.Code)
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
}

func shellTaskIDFromHistory(history []core.Message) (string, error) {
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		if msg.Role != core.RoleTool {
			continue
		}
		for _, tr := range msg.ToolResults {
			if tr.Name != "shell_run" {
				continue
			}
			env, ok := core.ParseToolEnvelope(tr.Content)
			if !ok {
				continue
			}
			if taskID, ok := envelopeString(env, "payload", "task_id"); ok && taskID != "" {
				return taskID, nil
			}
		}
	}
	return "", fmt.Errorf("background shell task_id not found in history")
}

func envelopeString(env core.ToolEnvelope, keys ...string) (string, bool) {
	if env.Data == nil {
		return "", false
	}
	var cur any = env.Data
	for _, key := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur, ok = obj[key]
		if !ok {
			return "", false
		}
	}
	s, ok := cur.(string)
	return s, ok
}

func envelopeStringSlice(env core.ToolEnvelope, keys ...string) ([]string, bool) {
	if env.Data == nil {
		return nil, false
	}
	var cur any = env.Data
	for _, key := range keys {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = obj[key]
		if !ok {
			return nil, false
		}
	}
	raw, err := json.Marshal(cur)
	if err != nil {
		return nil, false
	}
	var items []string
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, false
	}
	return items, true
}
