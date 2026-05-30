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
