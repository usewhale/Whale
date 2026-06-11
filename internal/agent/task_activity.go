package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

func taskStartedEvent(call core.ToolCall) (AgentEvent, bool) {
	info := TaskActivityInfo{
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Status:     "started",
	}
	var args map[string]any
	_ = json.Unmarshal([]byte(call.Input), &args)
	switch call.Name {
	case "parallel_reason":
		info.Count = len(core.AsAnySlice(args["prompts"]))
		info.Model = strings.TrimSpace(core.AsString(args["model"]))
		info.Summary = fmt.Sprintf("parallel reasoning · %d prompt(s)", info.Count)
		return AgentEvent{Type: AgentEventTypeParallelReasonStarted, Task: &info}, true
	case "spawn_subagent":
		info.Role = strings.TrimSpace(core.AsString(args["role"]))
		if info.Role == "" {
			info.Role = "explore"
		}
		info.Model = strings.TrimSpace(core.AsString(args["model"]))
		info.Summary = core.FirstLine(core.AsString(args["task"]))
		return AgentEvent{Type: AgentEventTypeSubagentStarted, Task: &info}, true
	default:
		return AgentEvent{}, false
	}
}

func taskCompletedEvent(res core.ToolResult) (AgentEvent, bool) {
	info := TaskActivityInfo{
		ToolCallID: res.ToolCallID,
		ToolName:   res.Name,
		Status:     "completed",
	}
	if res.IsError() {
		info.Status = "failed"
	}
	if env, ok := core.ToolEnvelopeView(res); ok {
		if !env.OK || !env.Success {
			info.Status = "failed"
		}
		info.Model = strings.TrimSpace(core.AsString(env.Data["model"]))
		info.Role = strings.TrimSpace(core.AsString(env.Data["role"]))
		info.Summary = strings.TrimSpace(core.FirstNonEmpty(
			core.AsString(env.Data["summary"]),
			env.Summary,
			env.Message,
			env.Error,
		))
		info.DurationMS = asInt64(env.Metadata["duration_ms"])
		switch res.Name {
		case "parallel_reason":
			info.Count = len(core.AsAnySlice(env.Data["results"]))
		case "spawn_subagent":
			if info.Role == "" {
				info.Role = "explore"
			}
			childSessionID := strings.TrimSpace(core.FirstNonEmpty(core.AsString(env.Data["child_session_id"]), core.AsString(env.Data["session_id"])))
			if childSessionID != "" {
				info.Metadata = map[string]any{"child_session_id": childSessionID}
			}
		}
	}
	switch res.Name {
	case "parallel_reason":
		if info.Summary == "" {
			info.Summary = fmt.Sprintf("parallel reasoning · %d result(s)", info.Count)
		}
		return AgentEvent{Type: AgentEventTypeParallelReasonDone, Task: &info}, true
	case "spawn_subagent":
		if info.Summary == "" {
			info.Summary = "subagent completed"
		}
		return AgentEvent{Type: AgentEventTypeSubagentDone, Task: &info}, true
	default:
		return AgentEvent{}, false
	}
}

func asInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	default:
		return 0
	}
}
