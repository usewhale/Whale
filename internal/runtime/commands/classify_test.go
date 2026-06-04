package commands

import "testing"

func TestClassifySubmitAcceptsStatsCache(t *testing.T) {
	got := ClassifySubmit("/stats cache", "")
	if got.Class != SubmitLocalReadOnly {
		t.Fatalf("/stats cache class = %v, want %v", got.Class, SubmitLocalReadOnly)
	}
}
