package agent

import (
	"fmt"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func TestToolCallTargetIgnoresIncidentalArgs(t *testing.T) {
	a := toolCallTarget(core.ToolCall{Name: "read_file", Input: `{"file_path":"/x/y.go","offset":10,"limit":1}`})
	b := toolCallTarget(core.ToolCall{Name: "read_file", Input: `{"file_path":"/x/y.go","offset":99,"limit":50}`})
	if a != b {
		t.Fatalf("same file with different offset/limit should share a target: %q vs %q", a, b)
	}
	c := toolCallTarget(core.ToolCall{Name: "read_file", Input: `{"file_path":"/x/z.go"}`})
	if a == c {
		t.Fatalf("different files should not share a target: %q == %q", a, c)
	}
	// Name-scoped: same target string under a different tool is distinct.
	g := toolCallTarget(core.ToolCall{Name: "grep", Input: `{"path":"/x/y.go"}`})
	if g == a {
		t.Fatalf("different tools should not share a target: %q == %q", g, a)
	}
}

func TestToolCallTargetFallsBackToInputWhenNoKnownKey(t *testing.T) {
	a := toolCallTarget(core.ToolCall{Name: "weird", Input: `{"a":1}`})
	b := toolCallTarget(core.ToolCall{Name: "weird", Input: `{"a":2}`})
	if a == b {
		t.Fatalf("without a known target key, distinct inputs should be distinct targets: %q == %q", a, b)
	}
}

func readOnlyByName(names ...string) func(core.ToolCall) bool {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(c core.ToolCall) bool { return set[c.Name] }
}

func TestProgressTrackerFlagsSteppingOffsetLoop(t *testing.T) {
	p := &progressTracker{}
	ro := readOnlyByName("read_file")
	// Mimic the real regression: re-read one file, stepping the offset each
	// round so every input string differs (defeating the storm breaker).
	redundantStreak := 0
	for i := 0; i < 30; i++ {
		call := core.ToolCall{Name: "read_file", Input: fmt.Sprintf(`{"file_path":"/x/y.go","limit":1,"offset":%d}`, 600-i)}
		if p.observe([]core.ToolCall{call}, nil, ro) {
			redundantStreak++
		} else {
			redundantStreak = 0
		}
	}
	if redundantStreak < maxConsecutiveRedundantRounds {
		t.Fatalf("stepping-offset loop should accumulate redundant rounds, got streak=%d", redundantStreak)
	}
}

func TestProgressTrackerMutatingRoundResetsStreak(t *testing.T) {
	p := &progressTracker{}
	ro := readOnlyByName("read_file") // "write" is not read-only
	read := core.ToolCall{Name: "read_file", Input: `{"file_path":"/x/y.go"}`}
	for i := 0; i < targetRevisitThreshold+3; i++ {
		p.observe([]core.ToolCall{read}, nil, ro)
	}
	// A mutating call to the same path is progress: the round is not redundant.
	if p.observe([]core.ToolCall{{Name: "write", Input: `{"file_path":"/x/y.go"}`}}, nil, ro) {
		t.Fatal("a mutating round must not count as redundant")
	}
}

func TestProgressTrackerDistinctFilesNeverRedundant(t *testing.T) {
	p := &progressTracker{}
	ro := readOnlyByName("read_file")
	for i := 0; i < 30; i++ {
		call := core.ToolCall{Name: "read_file", Input: fmt.Sprintf(`{"file_path":"/x/file_%d.go"}`, i)}
		if p.observe([]core.ToolCall{call}, nil, ro) {
			t.Fatalf("reading distinct files is genuine progress; round %d wrongly flagged redundant", i)
		}
	}
}

func TestProgressTrackerDistinctSearchesUnderSamePathAreProgress(t *testing.T) {
	p := &progressTracker{}
	ro := readOnlyByName("grep")
	// Different patterns under the same path are distinct searches, each making
	// progress — they must not collapse to one [0,2000) file range and trip the
	// guard (the P2 grep/search regression).
	for i := 0; i < 12; i++ {
		call := core.ToolCall{Name: "grep", Input: fmt.Sprintf(`{"path":"/x","pattern":"needle_%d"}`, i)}
		if p.observe([]core.ToolCall{call}, nil, ro) {
			t.Fatalf("distinct searches under one path are progress; search %d wrongly flagged redundant", i)
		}
	}
	// The same search repeated, however, is a revisit and should eventually stall.
	same := core.ToolCall{Name: "grep", Input: `{"path":"/x","pattern":"same"}`}
	streak := 0
	for i := 0; i < targetRevisitThreshold+maxConsecutiveRedundantRounds+1; i++ {
		if p.observe([]core.ToolCall{same}, nil, ro) {
			streak++
		} else {
			streak = 0
		}
	}
	if streak < maxConsecutiveRedundantRounds {
		t.Fatalf("repeating one identical search should stall, got streak=%d", streak)
	}
}

func TestProgressTrackerPagingLargeFileIsProgress(t *testing.T) {
	p := &progressTracker{}
	ro := readOnlyByName("read_file")
	// Page forward through one large file: each round advances by a full page
	// and covers fresh content. This must never be flagged as a no-progress
	// loop, however many pages it takes (the P2 paged-read regression).
	for page := 0; page < 60; page++ {
		call := core.ToolCall{Name: "read_file", Input: fmt.Sprintf(`{"file_path":"/x/big.go","offset":%d,"limit":200}`, page*200)}
		if p.observe([]core.ToolCall{call}, nil, ro) {
			t.Fatalf("paging a large file is progress; page %d wrongly flagged redundant", page)
		}
	}
}

func TestProgressTrackerEmptyReadsStall(t *testing.T) {
	p := &progressTracker{}
	ro := readOnlyByName("read_file")
	emptyRes := func(id string) core.ToolResult {
		return core.ToolResult{
			ToolCallID: id,
			Name:       "read_file",
			Payload:    map[string]any{"metrics": map[string]any{"returned_lines": float64(0)}},
		}
	}
	// Reads at ever-changing offsets past EOF request fresh, non-overlapping
	// ranges but return zero lines. Judged by the requested range alone these
	// would look like progress forever; judged by returned_lines they stall (P2).
	streak := 0
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("c%d", i)
		call := core.ToolCall{ID: id, Name: "read_file", Input: fmt.Sprintf(`{"file_path":"/x/y.go","offset":%d,"limit":100}`, 100000+i*100)}
		if p.observe([]core.ToolCall{call}, []core.ToolResult{emptyRes(id)}, ro) {
			streak++
		} else {
			streak = 0
		}
	}
	if streak < maxConsecutiveRedundantRounds {
		t.Fatalf("empty (past-EOF) reads should accumulate redundant rounds, got streak=%d", streak)
	}
}

func TestProgressTrackerPollingToolNeverStalls(t *testing.T) {
	p := &progressTracker{}
	ro := readOnlyByName("shell_wait")
	call := core.ToolCall{Name: "shell_wait", Input: `{"task_id":"bg-1"}`}
	// Polling a background command with the same task_id many times is progress,
	// not a redundant loop — it must never accrue a redundant round (P2).
	for i := 0; i < targetRevisitThreshold+maxConsecutiveRedundantRounds+5; i++ {
		if p.observe([]core.ToolCall{call}, nil, ro) {
			t.Fatalf("polling shell_wait wrongly flagged redundant at poll %d", i)
		}
	}
}

func TestProgressTrackerReReadingSameRegionStalls(t *testing.T) {
	p := &progressTracker{}
	ro := readOnlyByName("read_file")
	call := core.ToolCall{Name: "read_file", Input: `{"file_path":"/x/y.go","offset":0,"limit":50}`}
	// First read is progress; identical re-reads add no new lines and stall.
	streak := 0
	for i := 0; i < 10; i++ {
		if p.observe([]core.ToolCall{call}, nil, ro) {
			streak++
		} else {
			streak = 0
		}
	}
	if streak < maxConsecutiveRedundantRounds {
		t.Fatalf("re-reading the same region should accumulate redundant rounds, got streak=%d", streak)
	}
}
