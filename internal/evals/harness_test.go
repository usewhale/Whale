package evals

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunScenarioOfflineToolLoop(t *testing.T) {
	recordPath := filepath.Join(t.TempDir(), "offline-tool-loop.jsonl")
	run, err := RunScenario(context.Background(), ScenarioSpec{
		Name:       "offline-tool-loop",
		Prompt:     "run the standard tool loop",
		RecordPath: recordPath,
		Setup: func(root string) error {
			return os.WriteFile(filepath.Join(root, "notes.txt"), []byte("alpha\nbeta\n"), 0o644)
		},
		Turns: []TurnSpec{
			{
				Steps: []StepSpec{
					{ID: "list", ToolName: "list_dir", Input: `{"path":"."}`},
					{ID: "view", ToolName: "read_file", Input: `{"file_path":"notes.txt","offset":0,"limit":20}`},
					{ID: "multi", ToolName: "multi_edit", Input: `{"file_path":"notes.txt","edits":[{"search":"alpha","replace":"alpha patched"}]}`},
					{ID: "shell", ToolName: "shell_run", Input: `{"command":"printf whale-eval"}`},
				},
			},
		},
		Verify: func(run *Run) error {
			b, err := os.ReadFile(filepath.Join(run.Root, "notes.txt"))
			if err != nil {
				return err
			}
			if !strings.Contains(string(b), "alpha patched") {
				return fmt.Errorf("patched file missing expected content")
			}
			if len(run.Steps) != 4 {
				return fmt.Errorf("expected 4 steps, got %d", len(run.Steps))
			}
			if !strings.Contains(run.Steps[3].Result.ModelText, "whale-eval") {
				return fmt.Errorf("shell output missing expected token")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("run scenario: %v", err)
	}
	if run.Final.Text != "done" {
		t.Fatalf("unexpected final text: %q", run.Final.Text)
	}
	record, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(record)), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 record lines, got %d", len(lines))
	}
}

func TestRunScenarioCapturesExpectedToolErrors(t *testing.T) {
	_, err := RunScenario(context.Background(), ScenarioSpec{
		Name:   "tool-error",
		Prompt: "try a failing edit",
		Turns: []TurnSpec{
			{
				Steps: []StepSpec{
					{
						ID:          "missing-file",
						ToolName:    "read_file",
						Input:       `{"file_path":"missing.txt","offset":0,"limit":10}`,
						ExpectError: true,
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("run scenario with expected error: %v", err)
	}
}

func TestRunTaskVerifiesWorkspaceState(t *testing.T) {
	run, err := RunTask(context.Background(), TaskSpec{
		ID:          "write-and-read",
		Description: "writes a file and confirms it can be read back",
		Scenario: ScenarioSpec{
			Setup: func(root string) error {
				return os.MkdirAll(filepath.Join(root, "docs"), 0o755)
			},
			Turns: []TurnSpec{
				{
					Steps: []StepSpec{
						{ID: "write", ToolName: "write", Input: `{"file_path":"docs/guide.txt","content":"hello whale\n"}`},
						{ID: "read", ToolName: "read_file", Input: `{"file_path":"docs/guide.txt","offset":0,"limit":20}`},
					},
				},
			},
			Verify: func(run *Run) error {
				b, err := os.ReadFile(filepath.Join(run.Root, "docs", "guide.txt"))
				if err != nil {
					return err
				}
				if string(b) != "hello whale\n" {
					return fmt.Errorf("unexpected guide content: %q", string(b))
				}
				if !strings.Contains(run.Steps[1].Result.ModelText, "hello whale") {
					return fmt.Errorf("read step missing file content")
				}
				return nil
			},
		},
	})
	if err != nil {
		t.Fatalf("run task: %v", err)
	}
	if run.Name != "write-and-read" {
		t.Fatalf("unexpected task name: %q", run.Name)
	}
}
