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

func TestTaskShellRunFailureReturnsExecFailed(t *testing.T) {
	run, err := RunTask(context.Background(), TaskSpec{
		ID:    "exec-shell-failure",
		Suite: SuiteRegression,
		Scenario: ScenarioSpec{
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "fail", ToolName: "shell_run", Input: `{"command":"exit 9"}`, ExpectError: true},
					},
				},
			},
			Verify: func(run *Run) error {
				step := run.FindStep("fail")
				if step == nil || step.Envelope.Code != "exec_failed" {
					return fmt.Errorf("unexpected exec failure code: %s", step.Envelope.Code)
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if run.Summary().Suite != SuiteRegression {
		t.Fatalf("unexpected suite: %s", run.Summary().Suite)
	}
}

func TestTaskApprovalRequiredWriteDenied(t *testing.T) {
	_, err := RunTask(context.Background(), TaskSpec{
		ID:    "approval-required-write-denied",
		Suite: SuiteRegression,
		Scenario: ScenarioSpec{
			AgentOptions: []agent.AgentOption{
				agent.WithToolPolicy(editApprovalPolicy()),
				agent.WithApprovalFunc(func(req policy.ApprovalRequest) policy.ApprovalDecision { return policy.ApprovalDeny }),
			},
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "write", ToolName: "write", Input: `{"file_path":"docs/denied.txt","content":"blocked\n"}`, ExpectError: true},
					},
				},
			},
			Verify: func(run *Run) error {
				step := run.FindStep("write")
				if step == nil || step.Envelope.Code != "approval_denied" {
					return fmt.Errorf("unexpected approval denial code: %s", step.Envelope.Code)
				}
				if _, err := os.Stat(filepath.Join(run.Root, "docs", "denied.txt")); !os.IsNotExist(err) {
					return fmt.Errorf("denied write should not create file")
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
}

func TestTaskPlanModeBlocksWriteFlow(t *testing.T) {
	_, err := RunTask(context.Background(), TaskSpec{
		ID:    "plan-mode-block-write",
		Suite: SuiteRegression,
		Scenario: ScenarioSpec{
			AgentOptions: []agent.AgentOption{
				agent.WithSessionMode(session.ModePlan),
				agent.WithToolPolicy(policy.RulePolicy{Default: policy.PermissionAllow}),
			},
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "write", ToolName: "write", Input: `{"file_path":"blocked.txt","content":"nope\n"}`, ExpectError: true},
					},
				},
			},
			Verify: func(run *Run) error {
				step := run.FindStep("write")
				if step == nil || step.Envelope.Code != "plan_mode_blocked" {
					return fmt.Errorf("unexpected plan mode block code: %s", step.Envelope.Code)
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
}

func TestTaskEditMissingSearchReturnsSearchNotFound(t *testing.T) {
	_, err := RunTask(context.Background(), TaskSpec{
		ID:    "edit-missing-search",
		Suite: SuiteRegression,
		Scenario: ScenarioSpec{
			Setup: func(root string) error {
				return os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello\n"), 0o644)
			},
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "read", ToolName: "read_file", Input: `{"file_path":"notes.txt"}`},
					},
				},
				{
					Steps: []StepSpec{
						{
							ID:          "edit",
							ToolName:    "edit",
							Input:       `{"file_path":"notes.txt","search":"missing","replace":"new"}`,
							ExpectError: true,
						},
					},
				},
			},
			Verify: func(run *Run) error {
				step := run.FindStep("edit")
				if step == nil || step.Envelope.Code != "search_not_found" {
					return fmt.Errorf("unexpected edit error code: %s", step.Envelope.Code)
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
}

func TestTaskEditWithoutReadReturnsReadRequired(t *testing.T) {
	_, err := RunTask(context.Background(), TaskSpec{
		ID:    "edit-read-required",
		Suite: SuiteRegression,
		Scenario: ScenarioSpec{
			Setup: func(root string) error {
				return os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello\n"), 0o644)
			},
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "edit", ToolName: "edit", Input: `{"file_path":"notes.txt","search":"hello","replace":"new"}`, ExpectError: true},
					},
				},
			},
			Verify: func(run *Run) error {
				step := run.FindStep("edit")
				if step == nil || step.Envelope.Code != "read_required" {
					return fmt.Errorf("unexpected edit error code: %s", step.Envelope.Code)
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
}

func TestTaskExpectedToolFailureStopsCleanly(t *testing.T) {
	run, err := RunTask(context.Background(), TaskSpec{
		ID:    "expected-failure-clean-stop",
		Suite: SuiteRegression,
		Scenario: ScenarioSpec{
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "missing", ToolName: "read_file", Input: `{"file_path":"missing.txt","offset":0,"limit":20}`, ExpectError: true},
					},
				},
			},
			Verify: func(run *Run) error {
				if run.Final.FinishReason != core.FinishReasonEndTurn {
					return fmt.Errorf("expected clean end turn, got %s", run.Final.FinishReason)
				}
				step := run.FindStep("missing")
				if step == nil || step.Envelope.Code != "not_found" {
					return fmt.Errorf("unexpected missing-file code: %s", step.Envelope.Code)
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if got := run.Summary().StepCount; got != 1 {
		t.Fatalf("unexpected step count: %d", got)
	}
}

func TestTaskBackgroundShellRunningThenFailed(t *testing.T) {
	_, err := RunTask(context.Background(), TaskSpec{
		ID:    "background-shell-running-then-failed",
		Suite: SuiteRegression,
		Scenario: ScenarioSpec{
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "start", ToolName: "shell_run", Input: `{"command":"sleep 0.1; echo nope >&2; exit 7","background":true}`},
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
							ID:       "wait-failed",
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
				failed := run.FindStep("wait-failed")
				if running == nil || !strings.Contains(running.Result.ModelText, `running in background`) {
					return fmt.Errorf("expected running wait result")
				}
				if failed == nil || !strings.Contains(failed.Result.ModelText, `exit `) || !strings.Contains(failed.Result.ModelText, "nope") {
					return fmt.Errorf("expected failed terminal wait result, got %+v", failed)
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
}

func TestTaskPreToolHookBlockSkipsWrite(t *testing.T) {
	_, err := RunTask(context.Background(), TaskSpec{
		ID:    "pre-tool-hook-block",
		Suite: SuiteRegression,
		Scenario: ScenarioSpec{
			AgentOptions: []agent.AgentOption{
				agent.WithHooks([]agent.ResolvedHook{{
					HookConfig: agent.HookConfig{Command: "echo blocked >&2; exit 2"},
					Event:      agent.HookEventPreToolUse,
				}}, "."),
			},
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "write", ToolName: "write", Input: `{"file_path":"blocked.txt","content":"nope\n"}`, ExpectError: true},
					},
				},
			},
			Verify: func(run *Run) error {
				step := run.FindStep("write")
				if step == nil || step.Envelope.Code != "hook_blocked" {
					return fmt.Errorf("unexpected hook block code: %s", step.Envelope.Code)
				}
				if _, err := os.Stat(filepath.Join(run.Root, "blocked.txt")); !os.IsNotExist(err) {
					return fmt.Errorf("hook-blocked write should not create file")
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
}

func TestTaskPostToolHookWarnStillCompletes(t *testing.T) {
	run, err := RunTask(context.Background(), TaskSpec{
		ID:    "post-tool-hook-warn",
		Suite: SuiteRegression,
		Scenario: ScenarioSpec{
			AgentOptions: []agent.AgentOption{
				agent.WithHooks([]agent.ResolvedHook{{
					HookConfig: agent.HookConfig{Command: "echo post-warn >&2; exit 5"},
					Event:      agent.HookEventPostToolUse,
				}}, "."),
			},
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "write", ToolName: "write", Input: `{"file_path":"warn.txt","content":"ok\n"}`},
						{ID: "read", ToolName: "read_file", Input: `{"file_path":"warn.txt","offset":0,"limit":20}`},
					},
				},
			},
			Verify: func(run *Run) error {
				b, err := os.ReadFile(filepath.Join(run.Root, "warn.txt"))
				if err != nil {
					return err
				}
				if string(b) != "ok\n" {
					return fmt.Errorf("hook warn flow should still complete write, got %q", string(b))
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if read := run.FindStep("read"); read == nil || !strings.Contains(read.Result.ModelText, "ok") {
		t.Fatal("expected readback after hook warning")
	}
}

func TestTaskTodoUpdateUnknownIDReturnsNotFound(t *testing.T) {
	_, err := RunTask(context.Background(), TaskSpec{
		ID:    "todo-update-unknown-id",
		Suite: SuiteRegression,
		Scenario: ScenarioSpec{
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "update", ToolName: "todo_update", Input: `{"id":"td-missing","text":"nope"}`, ExpectError: true},
					},
				},
			},
			Verify: func(run *Run) error {
				step := run.FindStep("update")
				if step == nil || !strings.Contains(step.Result.ModelText, "todo_not_found") {
					return fmt.Errorf("unexpected todo update error: %+v", step)
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
}

func TestTaskTodoRemoveInvalidPayloadReturnsInvalidTodoRemove(t *testing.T) {
	_, err := RunTask(context.Background(), TaskSpec{
		ID:    "todo-remove-invalid-payload",
		Suite: SuiteRegression,
		Scenario: ScenarioSpec{
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "remove", ToolName: "todo_remove", Input: `{}`, ExpectError: true},
					},
				},
			},
			Verify: func(run *Run) error {
				step := run.FindStep("remove")
				if step == nil || !strings.Contains(step.Result.ModelText, "invalid_todo_remove") {
					return fmt.Errorf("unexpected todo remove error: %+v", step)
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
}
