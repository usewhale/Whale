package tui

import (
	"fmt"
	"strings"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/usewhale/whale/internal/core"
)

const shellOutputPreviewLines = 6
const shellOutputHeadLines = 2
const shellOutputTailLines = 2
const shellOutputLineRunes = 220

func summarizeToolResultForChat(toolName, raw string) (string, string) {
	body := strings.TrimSpace(raw)
	if body == "" {
		return "result", body
	}
	env, ok := parseToolEnvelopeOK(body)
	if !ok {
		return "result_error", "ERROR · malformed tool result"
	}

	successBySignal := env.success
	if !env.hasSuccess {
		switch {
		case env.hasOK && env.ok:
			successBySignal = true
		case env.code == "ok":
			successBySignal = true
		default:
			successBySignal = true
		}
	}
	if env.status != "" && env.status != "ok" && env.status != "running" && env.status != "done" && env.status != "completed" && env.status != "success" && env.status != "exited" {
		successBySignal = false
	}

	switch toolDisplayKind(toolName) {
	case "shell":
		return summarizeShellResult(env, successBySignal)
	case "explore":
		return summarizeExploreResult(toolName, env, successBySignal)
	case "edit":
		return summarizeEditResult(toolName, env, successBySignal)
	case "task":
		return summarizeTaskResult(toolName, env, successBySignal)
	case "mcp":
		return summarizeMCPResult(env, successBySignal)
	default:
		if !successBySignal {
			return summarizeFailedResult(env, "tool failed")
		}
		return "result_ok", "✓"
	}
}

func summarizeTaskResult(toolName string, env toolResultEnvelope, successBySignal bool) (string, string) {
	if !successBySignal {
		return summarizeFailedResult(env, "task failed")
	}
	duration := formatDurationMS(asInt64(env.metadata["duration_ms"]))
	parts := []string{"✓"}
	if duration != "" {
		parts = append(parts, duration)
	}
	switch toolName {
	case "parallel_reason":
		if count := len(anySlice(env.data["results"])); count > 0 {
			parts = append(parts, fmt.Sprintf("%d result(s)", count))
		}
		return "result_ok", strings.Join(parts, " · ")
	case "spawn_subagent":
		role := core.FirstNonEmpty(core.AsString(env.data["role"]), "explore")
		parts = append(parts, role)
		if summary := firstLine(core.FirstNonEmpty(core.AsString(env.data["summary"]), env.summary)); summary != "" {
			return "result_ok", strings.Join(parts, " · ") + "\n" + summary
		}
		return "result_ok", strings.Join(parts, " · ")
	default:
		return "result_ok", strings.Join(parts, " · ")
	}
}

func anySlice(v any) []any {
	if xs, ok := v.([]any); ok {
		return xs
	}
	return nil
}

type toolResultEnvelope struct {
	success    bool
	hasSuccess bool
	ok         bool
	hasOK      bool
	code       string
	message    string
	summary    string
	status     string
	data       map[string]any
	metrics    map[string]any
	payload    map[string]any
	diagnosis  map[string]any
	metadata   map[string]any
}

func summarizeShellResult(env toolResultEnvelope, successBySignal bool) (string, string) {
	exitCode := asInt(env.metrics["exit_code"])
	hasExitCode := hasInt(env.metrics["exit_code"])
	duration := formatDurationMS(asInt64(env.metrics["duration_ms"]))
	if env.status == "running" {
		taskID := core.AsString(env.payload["task_id"])
		reason := shellDiagnosisLabel(core.AsString(env.diagnosis["reason"]))
		if taskID != "" {
			if reason != "" && duration != "" {
				return "result_running", reason + " · " + duration + " · " + taskID
			}
			if reason != "" {
				return "result_running", reason + " · " + taskID
			}
			if duration != "" {
				return "result_running", "running in background · " + duration + " · " + taskID
			}
			return "result_running", "running in background · " + taskID
		}
		if duration != "" {
			return "result_running", "running · " + duration
		}
		return "result_running", "running"
	}
	if env.status == "cancelled" || env.status == "canceled" {
		return "result_canceled", "CANCELED"
	}

	if !successBySignal {
		return summarizeFailedShellResult(env)
	}

	_ = exitCode
	_ = hasExitCode
	parts := []string{"✓"}
	if duration != "" {
		parts = append(parts, duration)
	}
	output := summarizeShellOutput(shellPayloadOutput(env, false))
	if output != "" {
		return "result_ok", strings.Join(parts, " · ") + "\n" + output
	}
	return "result_ok", strings.Join(parts, " · ")
}

func summarizeFailedShellResult(env toolResultEnvelope) (string, string) {
	if shellFailureUsesGenericSummary(env) {
		return summarizeFailedResult(env, "command failed")
	}
	output := summarizeShellOutput(shellPayloadOutput(env, true))
	if output == "" {
		return summarizeFailedResult(env, "command failed")
	}
	exitCode := asInt(env.metrics["exit_code"])
	hasExitCode := hasInt(env.metrics["exit_code"])
	duration := formatDurationMS(asInt64(env.metrics["duration_ms"]))
	prefix := "✗"
	if hasExitCode && exitCode > 0 {
		prefix = fmt.Sprintf("✗ (exit %d)", exitCode)
	}
	parts := []string{prefix}
	if duration != "" {
		parts = append(parts, duration)
	}
	if reason := shellDiagnosisLabel(core.AsString(env.diagnosis["reason"])); reason != "" {
		parts = append(parts, reason)
	}
	return "result_failed", strings.Join(parts, " · ") + "\n" + output
}

func shellFailureUsesGenericSummary(env toolResultEnvelope) bool {
	switch env.code {
	case "request_replan", "approval_denied", "policy_denied", "permission_denied", "timeout", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

func shellPayloadOutput(env toolResultEnvelope, preferStderr bool) string {
	stdout := strings.TrimRight(core.AsString(env.payload["stdout"]), "\n")
	stderr := strings.TrimRight(core.AsString(env.payload["stderr"]), "\n")
	if preferStderr {
		return joinShellOutput(stderr, stdout)
	}
	return joinShellOutput(stdout, stderr)
}

func joinShellOutput(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(xansi.Strip(part)) != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, "\n")
}

func summarizeShellOutput(text string) string {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(xansi.Strip(text)) == "" {
		return ""
	}
	rawLines := strings.Split(text, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		lines = append(lines, truncateShellOutputLine(strings.TrimRight(line, "\r")))
	}
	if len(lines) <= shellOutputPreviewLines {
		return strings.Join(lines, "\n")
	}
	head := minInt(shellOutputHeadLines, len(lines))
	tail := minInt(shellOutputTailLines, len(lines)-head)
	omitted := len(lines) - head - tail
	out := make([]string, 0, head+1+tail)
	out = append(out, lines[:head]...)
	out = append(out, fmt.Sprintf("... %d lines omitted", omitted))
	out = append(out, lines[len(lines)-tail:]...)
	return strings.Join(out, "\n")
}

func truncateShellOutputLine(line string) string {
	if xansi.StringWidth(line) <= shellOutputLineRunes {
		return line
	}
	return xansi.Truncate(line, shellOutputLineRunes, "...")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func summarizeFailedResult(env toolResultEnvelope, fallback string) (string, string) {
	exitCode := asInt(env.metrics["exit_code"])
	hasExitCode := hasInt(env.metrics["exit_code"])
	duration := formatDurationMS(asInt64(env.metrics["duration_ms"]))
	detail := firstLine(core.FirstNonEmpty(
		env.summary,
		core.AsString(env.payload["stderr"]),
		core.AsString(env.payload["stdout"]),
		env.message,
		core.AsString(env.data["summary"]),
		fallback,
	))

	switch env.code {
	case "request_replan":
		return "result_failed", summarizeReplanRequired(env)
	case "approval_denied", "policy_denied", "permission_denied", "mcp_allowed_dirs_denied":
		return "result_denied", "DENIED · " + detail
	case "timeout":
		if reason := shellDiagnosisLabel(core.AsString(env.diagnosis["reason"])); reason != "" {
			if duration != "" {
				return "result_timeout", "TIMEOUT · " + duration + " · " + reason
			}
			return "result_timeout", "TIMEOUT · " + reason
		}
		if duration != "" {
			return "result_timeout", "TIMEOUT · " + duration
		}
		return "result_timeout", "TIMEOUT"
	case "cancelled", "canceled":
		return "result_canceled", "CANCELED"
	}

	lower := strings.ToLower(detail + " " + env.code)
	if strings.Contains(lower, "denied") || strings.Contains(lower, "approval") || strings.Contains(lower, "policy") {
		return "result_denied", "DENIED · " + detail
	}
	if strings.Contains(lower, "timeout") {
		if duration != "" {
			return "result_timeout", "TIMEOUT · " + duration
		}
		return "result_timeout", "TIMEOUT"
	}
	if strings.Contains(lower, "cancel") {
		return "result_canceled", "CANCELED"
	}

	prefix := "✗"
	if hasExitCode && exitCode > 0 {
		prefix = fmt.Sprintf("✗ (exit %d)", exitCode)
	}
	if duration != "" {
		return "result_failed", fmt.Sprintf("%s · %s · %s", prefix, duration, detail)
	}
	return "result_failed", fmt.Sprintf("%s · %s", prefix, detail)
}

func shellDiagnosisLabel(reason string) string {
	switch reason {
	case "build_test_long_running":
		return "build/test running"
	case "package_manager_long_running":
		return "package manager running"
	case "download_long_running":
		return "download running"
	case "watch_long_running":
		return "watch running"
	case "remote_command_long_running":
		return "remote command running"
	case "unknown_long_running":
		return "running in background"
	case "interactive_prompt":
		return "waiting for input"
	case "network_blocked":
		return "network blocked"
	case "foreground_timeout_too_short":
		return "timeout too short"
	case "build_or_test_timeout":
		return "build/test timeout"
	case "background_runtime_timeout":
		return "background timeout"
	case "interactive_or_auth":
		return "interactive/auth"
	case "ordinary_timeout":
		return "ordinary timeout"
	default:
		return ""
	}
}

func summarizeReplanRequired(env toolResultEnvelope) string {
	last := strings.TrimSpace(core.AsString(env.data["last_error"]))
	if last != "" {
		if inner, ok := parseToolEnvelopeOK(last); ok {
			_, text := summarizeFailedResult(inner, "tool failed")
			if strings.TrimSpace(text) != "" {
				return text
			}
		}
		return "✗ · " + firstLine(last)
	}
	if tool := strings.TrimSpace(core.AsString(env.data["tool_name"])); tool != "" {
		return "✗ · " + tool + " failed; choose a different approach"
	}
	return "✗ · tool failed; choose a different approach"
}

func summarizeExploreResult(toolName string, env toolResultEnvelope, successBySignal bool) (string, string) {
	if !successBySignal {
		return summarizeFailedResult(env, "exploration failed")
	}
	switch toolName {
	case "read_file":
		total := asInt(env.metrics["total_lines"])
		returned := asInt(env.metrics["returned_lines"])
		if total > 0 {
			return "result_ok", fmt.Sprintf("✓ · %d/%d lines", returned, total)
		}
	case "list_dir":
		items := stringSlice(firstNonEmptyAny(env.payload["items"], env.data["items"]))
		return "result_ok", fmt.Sprintf("✓ · %d items", len(items))
	case "search_files":
		total := asInt(env.metrics["total_matches"])
		if total > 0 {
			return "result_ok", fmt.Sprintf("✓ · %d matches", total)
		}
		items := stringSlice(firstNonEmptyAny(env.payload["items"], env.data["items"]))
		return "result_ok", fmt.Sprintf("✓ · %d matches", len(items))
	case "grep", "search_content":
		total := asInt(env.metrics["total_matches"])
		files := asInt(env.metrics["files_matched"])
		if files > 0 {
			return "result_ok", fmt.Sprintf("✓ · %d matches in %d files", total, files)
		}
		return "result_ok", fmt.Sprintf("✓ · %d matches", total)
	case "fetch", "web_fetch":
		status := asInt(firstNonEmptyAny(env.payload["status_code"], env.data["status_code"]))
		format := core.FirstNonEmpty(core.AsString(env.payload["format"]), core.AsString(env.data["format"]))
		if status > 0 && format != "" {
			return "result_ok", fmt.Sprintf("✓ · HTTP %d · %s", status, format)
		}
	case "web_search":
		return "result_ok", "✓"
	}
	return "result_ok", "✓"
}

func summarizeEditResult(toolName string, env toolResultEnvelope, successBySignal bool) (string, string) {
	if !successBySignal {
		return summarizeFailedResult(env, "edit failed")
	}
	switch toolName {
	case "write_file", "write":
		if n := asInt(firstNonEmptyAny(env.payload["bytes"], env.data["bytes"])); n > 0 {
			return "result_ok", fmt.Sprintf("✓ · %d bytes", n)
		}
	case "edit_file", "edit":
		if n := asInt(firstNonEmptyAny(env.payload["replacements"], env.data["replacements"])); n > 0 {
			return "result_ok", fmt.Sprintf("✓ · %d %s", n, pluralize(n, "replacement", "replacements"))
		}
	case "apply_patch":
		additions := asInt(firstNonEmptyAny(env.payload["additions"], env.data["additions"]))
		deletions := asInt(firstNonEmptyAny(env.payload["deletions"], env.data["deletions"]))
		if additions > 0 || deletions > 0 {
			return "result_ok", fmt.Sprintf("✓ · +%d -%d", additions, deletions)
		}
	}
	return "result_ok", "✓"
}
