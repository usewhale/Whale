package commands

import (
	"strings"
	"testing"
)

func TestDiffCommandIsLocalReadOnly(t *testing.T) {
	got := ClassifySubmit("/diff", CommandsHelp())
	if got.Class != SubmitLocalReadOnly {
		t.Fatalf("/diff class = %v, want SubmitLocalReadOnly", got.Class)
	}

	got = ClassifySubmit("/diff extra", CommandsHelp())
	if got.Class != SubmitUsageError {
		t.Fatalf("/diff extra class = %v, want SubmitUsageError", got.Class)
	}
}

func TestWorkflowsCommandIsLocalReadOnly(t *testing.T) {
	got := ClassifySubmit("/workflows", CommandsHelp())
	if got.Class != SubmitLocalReadOnly {
		t.Fatalf("/workflows class = %v, want SubmitLocalReadOnly", got.Class)
	}

	for _, line := range []string{"/workflows run-123", "/workflows run-123 extra", "/workflows events", "/workflows cancel", "/workflows events run-123", "/workflows cancel run-123"} {
		got := ClassifySubmit(line, CommandsHelp())
		if got.Class != SubmitUsageError {
			t.Fatalf("%s class = %v, want SubmitUsageError", line, got.Class)
		}
	}
}

func TestDeepResearchCommandIsLocalMutating(t *testing.T) {
	for _, line := range []string{
		"/deep-research marked v12 sanitize behavior",
		"/deep-research --resume run-123 marked v12 sanitize behavior",
		"/deep-research --resume=run-123 marked v12 sanitize behavior",
	} {
		got := ClassifySubmit(line, CommandsHelp())
		if got.Class != SubmitLocalMutating {
			t.Fatalf("%s class = %v, want SubmitLocalMutating", line, got.Class)
		}
	}

	for _, line := range []string{
		"/deep-research",
		"/deep-research --resume run-123",
		"/deep-research --budget",
		"/deep-research --budget 1000 marked v12 sanitize behavior",
		"/deep-research --unknown question",
	} {
		got := ClassifySubmit(line, CommandsHelp())
		if got.Class != SubmitUsageError {
			t.Fatalf("%s class = %v, want SubmitUsageError", line, got.Class)
		}
	}
}

func TestGoalCommandClassification(t *testing.T) {
	cases := []struct {
		line string
		want SubmitClass
	}{
		{"/goal", SubmitLocalReadOnly},
		{"/goal status", SubmitLocalReadOnly},
		{"/goal pause", SubmitLocalMutating},
		{"/goal resume", SubmitTurnStarting},
		{"/goal clear", SubmitLocalMutating},
		{"/goal ship the goal command", SubmitTurnStarting},
		{"/goal fix --help output", SubmitTurnStarting},
		{"/goal --tokens 50k ship the goal command", SubmitTurnStarting},
		{"/goal --tokens 50k fix --help output", SubmitTurnStarting},
		{"/goal --tokens=50k ship the goal command", SubmitTurnStarting},
		{"/goal --tokens", SubmitUsageError},
		{"/goal --tokens 50k", SubmitUsageError},
		{"/goal --tokens nope ship", SubmitUsageError},
		{"/goal --tokens 0 ship", SubmitUsageError},
		{"/goal --tokens 0.1 ship", SubmitUsageError},
		{"/goal --tokens=-1 ship", SubmitUsageError},
		{"/goal --tokens= ship", SubmitUsageError},
		{"/goal --unknown ship", SubmitUsageError},
		{"/goal status extra", SubmitUsageError},
	}
	for _, tc := range cases {
		got := ClassifySubmit(tc.line, CommandsHelp())
		if got.Class != tc.want {
			t.Fatalf("%s class = %v, want %v", tc.line, got.Class, tc.want)
		}
	}
}

func TestCommandsHelpIncludesGoal(t *testing.T) {
	if !strings.Contains(CommandsHelp(), "/goal [--tokens 50k] <objective>|status|pause|resume|clear") {
		t.Fatalf("CommandsHelp missing /goal: %s", CommandsHelp())
	}
}
