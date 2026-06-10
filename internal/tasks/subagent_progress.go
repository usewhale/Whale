package tasks

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/core"
)

func emitSubagentProgress(progress func(core.ToolProgress), role, model string, count int, status, summary string, metadata map[string]any) {
	emitSubagentProgressWithSteps(progress, role, model, count, status, summary, nil, metadata)
}

func emitSubagentProgressWithSteps(progress func(core.ToolProgress), role, model string, count int, status, summary string, steps []core.SubagentStep, metadata map[string]any) {
	if progress == nil {
		return
	}
	p := core.ToolProgress{
		ToolName: "spawn_subagent",
		Role:     role,
		Model:    model,
		Count:    count,
		Status:   status,
		Summary:  strings.TrimSpace(summary),
		Metadata: metadata,
	}
	if len(steps) > 0 {
		p.ProgressMessages = steps
	}
	progress(p)
}

func summarizeChildAgentEvent(ev agent.AgentEvent) (status, summary string, metadata map[string]any, ok bool) {
	switch ev.Type {
	case agent.AgentEventTypeContextCompacted:
		if ev.Compact == nil {
			return "", "", nil, false
		}
		if ev.Compact.Compacted {
			return "compacted", fmt.Sprintf("Compacted child context (%d -> %d messages)", ev.Compact.MessagesBefore, ev.Compact.MessagesAfter), map[string]any{
				"before_estimate": ev.Compact.BeforeEstimate,
				"after_estimate":  ev.Compact.AfterEstimate,
			}, true
		}
		return "compacted", "Checked child context compaction", map[string]any{
			"before_estimate": ev.Compact.BeforeEstimate,
			"after_estimate":  ev.Compact.AfterEstimate,
		}, true
	case agent.AgentEventTypeToolRecoveryExhausted:
		if ev.Recovery == nil {
			return "", "", nil, false
		}
		target := core.FirstNonEmpty(ev.Recovery.ToolName, "tool")
		action := strings.TrimSpace(ev.Recovery.Action)
		reason := strings.TrimSpace(ev.Recovery.Reason)
		if reason == "" {
			reason = ev.Recovery.FailureClass
		}
		metadata := map[string]any{
			"child_tool":      ev.Recovery.ToolName,
			"failure_class":   ev.Recovery.FailureClass,
			"recovery_action": action,
			"attempt":         ev.Recovery.Attempt,
			"max_attempts":    ev.Recovery.MaxAttempts,
			"executed":        ev.Recovery.Executed,
			"replan_injected": ev.Recovery.ReplanInjected,
		}
		if ev.Recovery.Executed {
			if ev.Recovery.ReplanInjected {
				if action != "" {
					return "tool_recovery_replanned", fmt.Sprintf("Requested replan for %s via %s", target, action), metadata, true
				}
				return "tool_recovery_replanned", fmt.Sprintf("Requested replan for %s", target), metadata, true
			}
			if action != "" {
				return "tool_recovered", fmt.Sprintf("Recovered %s via %s", target, action), metadata, true
			}
			return "tool_recovered", fmt.Sprintf("Recovered %s", target), metadata, true
		}
		if reason != "" {
			return "tool_recovery_failed", fmt.Sprintf("Recovery exhausted for %s: %s", target, reason), metadata, true
		}
		return "tool_recovery_failed", fmt.Sprintf("Recovery exhausted for %s", target), metadata, true
	case agent.AgentEventTypeBudgetWarning:
		if ev.Budget == nil {
			return "", "", nil, false
		}
		return "budget_warning", fmt.Sprintf("Child budget warning: %d%% of $%.2f cap", ev.Budget.Percent, ev.Budget.CapUSD), map[string]any{
			"budget_percent": ev.Budget.Percent,
			"budget_cap_usd": ev.Budget.CapUSD,
			"spent_usd":      ev.Budget.SpentUSD,
			"turn_cost_usd":  ev.Budget.TurnCostUSD,
		}, true
	case agent.AgentEventTypeForcedSummaryStarted:
		reason := strings.TrimSpace(ev.Content)
		if reason == "" {
			reason = "turn cap reached"
		}
		return "forced_summary_started", "Summarizing child agent because " + reason, map[string]any{
			"reason": reason,
		}, true
	case agent.AgentEventTypeForcedSummaryDone:
		summary := strings.TrimSpace(ev.Content)
		if summary == "" {
			summary = "Forced child summary completed"
		}
		return "forced_summary_done", summary, nil, true
	case agent.AgentEventTypeForcedSummaryFailed:
		reason := strings.TrimSpace(ev.Content)
		if reason == "" && ev.Err != nil {
			reason = ev.Err.Error()
		}
		if reason == "" {
			reason = "forced child summary failed"
		}
		return "forced_summary_failed", reason, nil, true
	default:
		return "", "", nil, false
	}
}

type childToolAction struct {
	ToolName string
	Target   string
	Running  string
	DoneVerb string
}

func summarizeChildToolCall(call core.ToolCall) childToolAction {
	var args map[string]any
	_ = json.Unmarshal([]byte(call.Input), &args)
	switch call.Name {
	case "read_file":
		target := compactProgressTarget(core.FirstNonEmpty(core.AsString(args["file_path"]), core.AsString(args["path"]), "file"))
		return childToolAction{ToolName: call.Name, Target: target, Running: "Reading " + target, DoneVerb: "Read"}
	case "list_dir":
		target := compactProgressTarget(core.FirstNonEmpty(core.AsString(args["path"]), "."))
		return childToolAction{ToolName: call.Name, Target: target, Running: "Listing " + target, DoneVerb: "Listed"}
	case "grep", "search_content":
		target := summarizeSearchTarget(args)
		return childToolAction{ToolName: call.Name, Target: target, Running: "Searching " + target, DoneVerb: "Searched"}
	case "search_files":
		pattern := quoteProgressTerm(core.FirstNonEmpty(core.AsString(args["pattern"]), core.AsString(args["query"]), "files"))
		return childToolAction{ToolName: call.Name, Target: pattern, Running: "Searching files " + pattern, DoneVerb: "Searched files"}
	case "web_search":
		target := quoteProgressTerm(core.FirstNonEmpty(core.AsString(args["query"]), "query"))
		return childToolAction{ToolName: call.Name, Target: target, Running: "Searching web " + target, DoneVerb: "Searched web"}
	case "fetch", "web_fetch":
		target := compactURLForProgress(core.FirstNonEmpty(core.AsString(args["url"]), "url"))
		return childToolAction{ToolName: call.Name, Target: target, Running: "Fetching " + target, DoneVerb: "Fetched"}
	default:
		if call.Name != "" {
			return childToolAction{ToolName: call.Name, Target: call.Name, Running: "Using " + call.Name, DoneVerb: "Used"}
		}
		return childToolAction{ToolName: call.Name, Target: "tool", Running: "Using tool", DoneVerb: "Used"}
	}
}

func summarizeSearchTarget(args map[string]any) string {
	pattern := quoteProgressTerm(core.FirstNonEmpty(core.AsString(args["pattern"]), core.AsString(args["query"]), "content"))
	path := compactProgressTarget(core.FirstNonEmpty(core.AsString(args["path"]), core.AsString(args["directory"]), ""))
	include := compactProgressTarget(core.FirstNonEmpty(core.AsString(args["include"]), ""))
	if path != "" && include != "" {
		return fmt.Sprintf("%s in %s (%s)", pattern, path, include)
	}
	if path != "" {
		return fmt.Sprintf("%s in %s", pattern, path)
	}
	if include != "" {
		return fmt.Sprintf("%s (%s)", pattern, include)
	}
	return pattern
}

func summarizeChildToolResult(res core.ToolResult, action childToolAction) string {
	if res.IsError {
		if action.Target != "" {
			return action.DoneVerb + " " + action.Target + " failed"
		}
		return res.Name + " failed"
	}
	if action.Target == "" {
		return res.Name + " completed"
	}
	summary := action.DoneVerb + " " + action.Target
	if suffix := childResultMetricSuffix(res); suffix != "" {
		summary += " · " + suffix
	}
	return summary
}

func childResultMetricSuffix(res core.ToolResult) string {
	var data map[string]any
	if payload, ok := res.Payload.(map[string]any); ok && res.Outcome != "" {
		if res.Outcome != core.OutcomeSuccess && res.Outcome != core.OutcomeNoResult {
			return ""
		}
		data = payload
	} else {
		env, ok := core.ParseToolEnvelope(core.ToolResultModelText(res))
		if !ok || !env.OK || !env.Success {
			return ""
		}
		data = env.Data
	}
	metrics := asMap(data["metrics"])
	payload := asMap(data["payload"])
	switch res.Name {
	case "read_file":
		total := asInt(metrics["total_lines"])
		returned := asInt(metrics["returned_lines"])
		if total > 0 && returned > 0 {
			return fmt.Sprintf("%d/%d lines", returned, total)
		}
	case "list_dir":
		items := core.AsAnySlice(payload["items"])
		if len(items) == 0 {
			items = core.AsAnySlice(data["items"])
		}
		if len(items) > 0 {
			return fmt.Sprintf("%d items", len(items))
		}
	case "grep", "search_content":
		total := asInt(metrics["total_matches"])
		files := asInt(metrics["files_matched"])
		if files > 0 {
			return fmt.Sprintf("%d matches in %d files", total, files)
		}
		if total >= 0 {
			return fmt.Sprintf("%d matches", total)
		}
	case "search_files":
		total := asInt(metrics["total_matches"])
		if total > 0 {
			return fmt.Sprintf("%d matches", total)
		}
		items := core.AsAnySlice(payload["items"])
		if len(items) > 0 {
			return fmt.Sprintf("%d matches", len(items))
		}
	case "web_search":
		count := asInt(data["count"])
		if count > 0 {
			return fmt.Sprintf("%d results", count)
		}
	case "fetch", "web_fetch":
		status := asInt(data["status_code"])
		if status > 0 {
			return fmt.Sprintf("HTTP %d", status)
		}
	}
	return ""
}
