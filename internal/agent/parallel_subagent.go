package agent

import "github.com/usewhale/whale/internal/core"

const parallelSubagentToolName = "spawn_subagent"

type parallelSubagentGroup struct {
	Start int
	Calls []core.ToolCall
}

type readyParallelSubagentCall struct {
	Index int
	Call  core.ToolCall
}

func eligibleParallelSubagentGroups(calls []core.ToolCall) []parallelSubagentGroup {
	ready := make([]readyParallelSubagentCall, 0, len(calls))
	for i, call := range calls {
		ready = append(ready, readyParallelSubagentCall{Index: i, Call: call})
	}
	return eligibleReadyParallelSubagentGroups(ready)
}

func maybeReadyParallelSubagentCall(index int, call core.ToolCall) (readyParallelSubagentCall, bool) {
	if call.Name != parallelSubagentToolName {
		return readyParallelSubagentCall{}, false
	}
	return readyParallelSubagentCall{Index: index, Call: call}, true
}

func eligibleReadyParallelSubagentGroups(ready []readyParallelSubagentCall) []parallelSubagentGroup {
	var groups []parallelSubagentGroup
	for i := 0; i < len(ready); {
		if ready[i].Call.Name != parallelSubagentToolName {
			i++
			continue
		}

		start := i
		for i < len(ready) && ready[i].Call.Name == parallelSubagentToolName {
			if i > start && ready[i].Index != ready[i-1].Index+1 {
				break
			}
			i++
		}
		if i-start < 2 {
			continue
		}

		groupCalls := make([]core.ToolCall, i-start)
		for j := start; j < i; j++ {
			groupCalls[j-start] = ready[j].Call
		}
		groups = append(groups, parallelSubagentGroup{
			Start: ready[start].Index,
			Calls: groupCalls,
		})
	}
	return groups
}
