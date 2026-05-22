package evals

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
)

func TestTaskSearchApplyPatchReadbackFlow(t *testing.T) {
	run, err := RunTask(context.Background(), TaskSpec{
		ID:    "search-apply-patch-readback",
		Suite: SuiteCapability,
		Scenario: ScenarioSpec{
			Setup: func(root string) error {
				if err := os.MkdirAll(filepath.Join(root, "internal", "cfg"), 0o755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(root, "internal", "cfg", "app.txt"), []byte("name=whale\nmode=old\n"), 0o644)
			},
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "search", ToolName: "search_files", Input: `{"path":".","pattern":"app.txt","limit":20}`},
						{ID: "patch", ToolName: "apply_patch", Input: "{\"patch\":\"*** Begin Patch\\n*** Update File: internal/cfg/app.txt\\n@@\\n-mode=old\\n+mode=new\\n*** End Patch\\n\"}"},
						{ID: "read", ToolName: "read_file", Input: `{"file_path":"internal/cfg/app.txt","offset":0,"limit":20}`},
					},
				},
			},
			Verify: func(run *Run) error {
				read := run.FindStep("read")
				if read == nil || !strings.Contains(read.Result.Content, "mode=new") {
					return fmt.Errorf("patched content missing from readback")
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if run.Summary().Suite != SuiteCapability {
		t.Fatalf("unexpected suite: %s", run.Summary().Suite)
	}
}

func TestTaskShellRunCreatesFileInSubdir(t *testing.T) {
	run, err := RunTask(context.Background(), TaskSpec{
		ID:    "exec-shell-cwd-write",
		Suite: SuiteCapability,
		Scenario: ScenarioSpec{
			Setup: func(root string) error {
				return os.MkdirAll(filepath.Join(root, "out"), 0o755)
			},
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "shell", ToolName: "shell_run", Input: `{"command":"printf from-shell > result.txt","cwd":"out"}`},
						{ID: "read", ToolName: "read_file", Input: `{"file_path":"out/result.txt","offset":0,"limit":20}`},
					},
				},
			},
			Verify: func(run *Run) error {
				b, err := os.ReadFile(filepath.Join(run.Root, "out", "result.txt"))
				if err != nil {
					return err
				}
				if string(b) != "from-shell" {
					return fmt.Errorf("unexpected shell output file content: %q", string(b))
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if read := run.FindStep("read"); read == nil || !strings.Contains(read.Result.Content, "from-shell") {
		t.Fatal("expected readback of shell-written file")
	}
}

func TestTaskPlanModeReadOnlyFlow(t *testing.T) {
	run, err := RunTask(context.Background(), TaskSpec{
		ID:    "plan-mode-read-only",
		Suite: SuiteCapability,
		Scenario: ScenarioSpec{
			Setup: func(root string) error {
				return os.WriteFile(filepath.Join(root, "notes.txt"), []byte("plan safe\n"), 0o644)
			},
			AgentOptions: []agent.AgentOption{
				agent.WithSessionMode(session.ModePlan),
				agent.WithToolPolicy(policy.RulePolicy{Default: policy.PermissionAllow}),
			},
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "list", ToolName: "list_dir", Input: `{"path":"."}`},
						{ID: "read", ToolName: "read_file", Input: `{"file_path":"notes.txt","offset":0,"limit":20}`},
					},
				},
			},
			Verify: func(run *Run) error {
				read := run.FindStep("read")
				if read == nil || !strings.Contains(read.Result.Content, "plan safe") {
					return fmt.Errorf("plan mode read-only flow did not read file")
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if run.Final.Text != "done" {
		t.Fatalf("unexpected final text: %q", run.Final.Text)
	}
}

func TestTaskApprovalRequiredWriteApproved(t *testing.T) {
	run, err := RunTask(context.Background(), TaskSpec{
		ID:    "approval-required-write-approved",
		Suite: SuiteCapability,
		Scenario: ScenarioSpec{
			AgentOptions: []agent.AgentOption{
				agent.WithToolPolicy(policy.DefaultToolPolicy{}),
				agent.WithApprovalFunc(func(req policy.ApprovalRequest) policy.ApprovalDecision { return policy.ApprovalAllow }),
			},
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "write", ToolName: "write", Input: `{"file_path":"docs/approved.txt","content":"approved\n"}`},
						{ID: "read", ToolName: "read_file", Input: `{"file_path":"docs/approved.txt","offset":0,"limit":20}`},
					},
				},
			},
			Verify: func(run *Run) error {
				read := run.FindStep("read")
				if read == nil || !strings.Contains(read.Result.Content, "approved") {
					return fmt.Errorf("approval-approved write missing readback")
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if step := run.FindStep("write"); step == nil || step.Result.IsError {
		t.Fatal("expected approved write step to succeed")
	}
}

func TestTaskGrepReadEditFlow(t *testing.T) {
	run, err := RunTask(context.Background(), TaskSpec{
		ID:    "grep-read-edit",
		Suite: SuiteCapability,
		Scenario: ScenarioSpec{
			Setup: func(root string) error {
				if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
					return err
				}
				content := "package pkg\n\nconst banner = \"hello whale\"\n"
				return os.WriteFile(filepath.Join(root, "pkg", "banner.go"), []byte(content), 0o644)
			},
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "grep", ToolName: "grep", Input: `{"pattern":"hello whale","path":".","literal_text":true}`},
						{ID: "read", ToolName: "read_file", Input: `{"file_path":"pkg/banner.go","offset":0,"limit":20}`},
						{ID: "edit", ToolName: "edit", Input: `{"file_path":"pkg/banner.go","search":"hello whale","replace":"hello eval"}`},
					},
				},
			},
			Verify: func(run *Run) error {
				grep := run.FindStep("grep")
				if grep == nil || !strings.Contains(grep.Result.Content, "pkg/banner.go") {
					return fmt.Errorf("grep did not surface expected file")
				}
				b, err := os.ReadFile(filepath.Join(run.Root, "pkg", "banner.go"))
				if err != nil {
					return err
				}
				if !strings.Contains(string(b), "hello eval") {
					return fmt.Errorf("edit did not update banner")
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

func TestTaskBackgroundShellWaitWithHistoryLookup(t *testing.T) {
	run, err := RunTask(context.Background(), TaskSpec{
		ID:    "background-shell-history-wait",
		Suite: SuiteCapability,
		Scenario: ScenarioSpec{
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "start", ToolName: "shell_run", Input: `{"command":"printf hello-from-bg","background":true}`},
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
						{ID: "write", ToolName: "write", Input: `{"file_path":"bg.txt","content":"done\n"}`},
					},
				},
			},
			Verify: func(run *Run) error {
				wait := run.FindStep("wait")
				if wait == nil || !strings.Contains(wait.Result.Content, "hello-from-bg") {
					return fmt.Errorf("background wait missing stdout")
				}
				b, err := os.ReadFile(filepath.Join(run.Root, "bg.txt"))
				if err != nil {
					return err
				}
				if string(b) != "done\n" {
					return fmt.Errorf("post-wait write missing")
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if run.Summary().StepCount != 3 {
		t.Fatalf("unexpected step count: %d", run.Summary().StepCount)
	}
}

func TestTaskTodoWorkflowAddUpdateListRemove(t *testing.T) {
	run, err := RunTask(context.Background(), TaskSpec{
		ID:    "todo-workflow-add-update-remove",
		Suite: SuiteCapability,
		Scenario: ScenarioSpec{
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "add", ToolName: "todo_add", Input: `{"text":"ship evals","priority":3}`},
						{ID: "list", ToolName: "todo_list", Input: `{}`},
					},
				},
				{
					Steps: []StepSpec{
						{
							ID:       "update",
							ToolName: "todo_update",
							InputFunc: func(history []core.Message) (string, error) {
								id, err := todoIDFromHistory(history)
								if err != nil {
									return "", err
								}
								return fmt.Sprintf(`{"id":%q,"text":"ship evals now","priority":4}`, id), nil
							},
						},
						{ID: "list-updated", ToolName: "todo_list", Input: `{}`},
					},
				},
				{
					Steps: []StepSpec{
						{
							ID:       "remove",
							ToolName: "todo_remove",
							InputFunc: func(history []core.Message) (string, error) {
								id, err := todoIDFromHistory(history)
								if err != nil {
									return "", err
								}
								return fmt.Sprintf(`{"id":%q}`, id), nil
							},
						},
						{ID: "final-list", ToolName: "todo_list", Input: `{}`},
					},
				},
			},
			Verify: func(run *Run) error {
				state, err := todoStateForRun(run)
				if err != nil {
					return err
				}
				if len(state.Items) != 0 {
					return fmt.Errorf("expected empty todo state after remove, got %+v", state.Items)
				}
				step := run.FindStep("list-updated")
				if step == nil || !strings.Contains(step.Result.Content, "ship evals now") {
					return fmt.Errorf("updated todo not visible in list result")
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if run.Summary().StepCount != 6 {
		t.Fatalf("unexpected step count: %d", run.Summary().StepCount)
	}
}

func TestTaskTodoWorkflowClearDone(t *testing.T) {
	run, err := RunTask(context.Background(), TaskSpec{
		ID:    "todo-workflow-clear-done",
		Suite: SuiteCapability,
		Scenario: ScenarioSpec{
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "add-done", ToolName: "todo_add", Input: `{"text":"done item","priority":5}`},
						{ID: "add-open", ToolName: "todo_add", Input: `{"text":"keep item","priority":2}`},
					},
				},
				{
					Steps: []StepSpec{
						{
							ID:       "mark-done",
							ToolName: "todo_update",
							InputFunc: func(history []core.Message) (string, error) {
								id, err := todoIDFromHistory(history)
								if err != nil {
									return "", err
								}
								return fmt.Sprintf(`{"id":%q,"done":true}`, id), nil
							},
						},
						{ID: "clear-done", ToolName: "todo_clear_done", Input: `{}`},
						{ID: "list", ToolName: "todo_list", Input: `{"include_done":true}`},
					},
				},
			},
			Verify: func(run *Run) error {
				state, err := todoStateForRun(run)
				if err != nil {
					return err
				}
				if len(state.Items) != 1 {
					return fmt.Errorf("expected one todo after clear_done, got %+v", state.Items)
				}
				if state.Items[0].Text != "keep item" || state.Items[0].Done {
					return fmt.Errorf("unexpected remaining todo: %+v", state.Items[0])
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if list := run.FindStep("list"); list == nil || !strings.Contains(list.Result.Content, "keep item") {
		t.Fatal("expected remaining todo to appear in list")
	}
}

func TestTaskBackgroundShellRunningThenExited(t *testing.T) {
	run, err := RunTask(context.Background(), TaskSpec{
		ID:    "background-shell-running-then-exited",
		Suite: SuiteCapability,
		Scenario: ScenarioSpec{
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "start", ToolName: "shell_run", Input: `{"command":"sleep 0.2; printf delayed-ok","background":true}`},
					},
				},
				{
					Steps: []StepSpec{
						{
							ID:       "wait-running",
							ToolName: "shell_wait",
							InputFunc: func(history []core.Message) (string, error) {
								taskID, err := shellTaskIDFromHistory(history)
								if err != nil {
									return "", err
								}
								return fmt.Sprintf(`{"task_id":%q,"timeout_ms":1}`, taskID), nil
							},
						},
					},
				},
				{
					Steps: []StepSpec{
						{
							ID:       "wait-exited",
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
				running := run.FindStep("wait-running")
				exited := run.FindStep("wait-exited")
				if running == nil || !strings.Contains(running.Result.Content, `"status":"running"`) {
					return fmt.Errorf("expected running wait result, got %+v", running)
				}
				if exited == nil || !strings.Contains(exited.Result.Content, `"status":"exited"`) || !strings.Contains(exited.Result.Content, "delayed-ok") {
					return fmt.Errorf("expected exited wait result with stdout, got %+v", exited)
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if run.Summary().StepCount != 3 {
		t.Fatalf("unexpected step count: %d", run.Summary().StepCount)
	}
}
