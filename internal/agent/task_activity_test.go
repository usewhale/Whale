package agent

import (
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func TestTaskActivityEventsForParallelReason(t *testing.T) {
	start, ok := taskStartedEvent(core.ToolCall{ID: "tc-1", Name: "parallel_reason", Input: `{"prompts":["a","b"]}`})
	if !ok || start.Type != AgentEventTypeParallelReasonStarted || start.Task == nil || start.Task.Count != 2 {
		t.Fatalf("unexpected start event: %+v ok=%v", start, ok)
	}
	done, ok := taskCompletedEvent(core.ToolResult{
		ToolCallID: "tc-1",
		Name:       "parallel_reason",
		ModelText:  `{"ok":true,"success":true,"data":{"model":"deepseek-v4-flash","results":[{"index":0},{"index":1}]},"metadata":{"duration_ms":25}}`,
	})
	if !ok || done.Type != AgentEventTypeParallelReasonDone || done.Task == nil || done.Task.Count != 2 || done.Task.DurationMS != 25 {
		t.Fatalf("unexpected done event: %+v ok=%v", done, ok)
	}
}

func TestTaskActivityEventsForSpawnSubagent(t *testing.T) {
	start, ok := taskStartedEvent(core.ToolCall{ID: "tc-2", Name: "spawn_subagent", Input: `{"role":"review","task":"review internal/tasks"}`})
	if !ok || start.Type != AgentEventTypeSubagentStarted || start.Task == nil || start.Task.Role != "review" || start.Task.Summary != "review internal/tasks" {
		t.Fatalf("unexpected start event: %+v ok=%v", start, ok)
	}
	done, ok := taskCompletedEvent(core.ToolResult{
		ToolCallID: "tc-2",
		Name:       "spawn_subagent",
		ModelText:  `{"ok":true,"success":true,"data":{"role":"review","summary":"looks fine"},"metadata":{"duration_ms":120}}`,
	})
	if !ok || done.Type != AgentEventTypeSubagentDone || done.Task == nil || done.Task.Role != "review" || done.Task.Summary != "looks fine" {
		t.Fatalf("unexpected done event: %+v ok=%v", done, ok)
	}
}
