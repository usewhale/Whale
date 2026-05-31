package commands

import "testing"

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
