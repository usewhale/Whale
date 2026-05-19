package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func (m *model) appendToolCall(toolCallID, toolName, text string) {
	if m.assembler == nil {
		m.assembler = tuirender.NewAssembler()
	}
	m.markToolCallPending(toolCallID)
	m.assembler.AddToolCall(toolCallID, toolName, summarizeToolCallForChat(toolName, text))
	m.refreshLiveViewportContent()
}

func (m *model) updateToolCallFromResult(toolCallID, toolName, result, role, summary string, metadata map[string]any) bool {
	if toolCallID == "" {
		return false
	}
	previous := ""
	if m.assembler != nil {
		previous = m.assembler.ToolCallText(toolCallID)
	}
	title := completedToolTitle(toolName, result, previous)
	if summary != "" && summary != "✓" {
		title += "\n" + summary
	}
	if diff := renderFileDiffMetadataMarkdown(metadata, 80); diff != "" && role == "result_ok" {
		title += "\n\n" + diff
	}
	if toolDisplayKind(toolName) == "shell" {
		role = shellResultRole(role)
	}
	ok := m.assembler.UpdateToolCall(toolCallID, title, role)
	m.markToolCallResolved(toolCallID)
	if ok {
		m.refreshLiveViewportContent()
	}
	return ok
}

func (m *model) updateTaskProgress(toolCallID, toolName, text string) bool {
	if toolCallID == "" || m.assembler == nil {
		return false
	}
	title := summarizeTaskProgressForChat(toolName, text)
	ok := m.assembler.UpdateToolCall(toolCallID, title, "result_running")
	if ok {
		m.refreshLiveViewportContent()
	}
	return ok
}

func (m *model) markToolCallPending(toolCallID string) {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return
	}
	if m.pendingToolCalls == nil {
		m.pendingToolCalls = map[string]struct{}{}
	}
	m.pendingToolCalls[toolCallID] = struct{}{}
}

func (m *model) markToolCallResolved(toolCallID string) {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" || m.pendingToolCalls == nil {
		return
	}
	delete(m.pendingToolCalls, toolCallID)
}

func (m *model) hasPendingToolCalls() bool {
	return len(m.pendingToolCalls) > 0
}

func (m *model) clearPendingToolCalls() {
	for k := range m.pendingToolCalls {
		delete(m.pendingToolCalls, k)
	}
}

func summarizeToolCallForChat(toolName, text string) string {
	detail := toolCallDetail(text)
	switch toolDisplayKind(toolName) {
	case "shell":
		if detail == "" {
			detail = "shell command"
		}
		return "Running " + detail
	case "explore":
		line := explorationLine(toolName, detail, toolResultEnvelope{})
		return "Exploring\n" + line
	case "edit":
		line := editLine(toolName, detail, toolResultEnvelope{})
		return line
	case "task":
		if strings.TrimSpace(toolName) == "parallel_reason" {
			return "Parallel reasoning\n" + firstNonEmpty(detail, "working")
		}
		role := taskRoleFromText(text)
		return "Subagent " + role + "\n" + firstNonEmpty(detail, "starting")
	case "plan":
		return "Updating plan"
	case "todo":
		return todoToolTitle(toolName, text, "running")
	default:
		if detail == "" {
			detail = toolName
		}
		return "Running " + detail
	}
}

func summarizeTaskProgressForChat(toolName, text string) string {
	detail := taskProgressDetail(text)
	if strings.TrimSpace(toolName) == "parallel_reason" {
		return "Parallel reasoning\n" + firstNonEmpty(detail, "running")
	}
	role := taskRoleFromText(text)
	if taskProgressFailed(text) {
		return "Subagent " + role + "\nFailed: " + firstNonEmpty(detail, "subagent failed")
	}
	return "Subagent " + role + "\n" + firstNonEmpty(detail, "running")
}

func taskProgressDetail(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return ""
	}
	parts := strings.SplitN(t, "·", 3)
	if len(parts) == 3 {
		return strings.TrimSpace(parts[2])
	}
	parts = strings.Split(t, "·")
	return strings.TrimSpace(parts[len(parts)-1])
}

func taskRoleFromText(text string) string {
	t := strings.TrimSpace(text)
	if before, _, ok := strings.Cut(t, "·"); ok {
		if _, role, hasColon := strings.Cut(before, ":"); hasColon {
			role = strings.TrimSpace(role)
			if role != "" {
				return role
			}
		}
	}
	parts := strings.Split(t, "·")
	if len(parts) >= 2 {
		role := strings.TrimSpace(parts[1])
		if role != "" && !strings.Contains(role, "prompt") {
			return role
		}
	}
	return "explore"
}

func taskProgressFailed(text string) bool {
	first := strings.TrimSpace(strings.SplitN(text, "·", 2)[0])
	return strings.Contains(first, "failed")
}

func completedToolTitle(toolName, raw, previous string) string {
	env := parseToolEnvelope(raw)
	switch toolDisplayKind(toolName) {
	case "shell":
		cmd := strings.TrimSpace(asString(env.payload["command"]))
		if cmd == "" {
			cmd = "shell command"
		}
		return "Ran " + cmd
	case "explore":
		return "Explored\n" + explorationLine(toolName, previousToolActionLine(previous), env)
	case "edit":
		return editLine(toolName, "", env)
	case "task":
		if toolName == "parallel_reason" {
			return "Parallel reasoning"
		}
		role := firstNonEmpty(asString(env.data["role"]), "explore")
		return "Subagent " + role
	case "plan":
		return "Updated plan"
	case "todo":
		return todoToolTitle(toolName, firstNonEmpty(previousToolActionLine(previous), raw), "done")
	default:
		label := toolName
		if label == "" {
			label = "tool"
		}
		return "Ran " + label
	}
}

func shellResultRole(role string) string {
	switch strings.TrimSpace(role) {
	case "result_ok":
		return "shell_result_ok"
	case "result_failed":
		return "shell_result_failed"
	case "result_timeout":
		return "shell_result_timeout"
	case "result_denied":
		return "shell_result_denied"
	case "result_canceled":
		return "shell_result_canceled"
	case "result_error":
		return "shell_result_error"
	case "result_running":
		return "shell_result_running"
	default:
		return role
	}
}

func shouldShowUnmatchedToolResult(toolName, role, text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	if (toolDisplayKind(toolName) == "todo" || toolDisplayKind(toolName) == "plan") && strings.TrimSpace(role) == "result_ok" {
		return false
	}
	return true
}

func toolDisplayKind(toolName string) string {
	switch strings.TrimSpace(toolName) {
	case "shell_run", "shell_wait":
		return "shell"
	case "read_file", "list_dir", "search_files", "grep", "search_content", "fetch", "web_fetch", "web_search":
		return "explore"
	case "write_file", "edit_file", "apply_patch", "write", "edit":
		return "edit"
	case "parallel_reason", "spawn_subagent":
		return "task"
	case "update_plan":
		return "plan"
	case "todo_add", "todo_list", "todo_update", "todo_remove", "todo_clear_done":
		return "todo"
	default:
		return "unknown"
	}
}

func toolCallDetail(text string) string {
	t := strings.TrimSpace(text)
	if t == "" {
		return ""
	}
	if idx := strings.Index(t, ":"); idx >= 0 {
		t = strings.TrimSpace(t[idx+1:])
	}
	if strings.HasPrefix(t, "{") {
		var body map[string]any
		if err := json.Unmarshal([]byte(t), &body); err == nil {
			detail := firstNonEmpty(
				asString(body["command"]),
				asString(body["file_path"]),
				asString(body["path"]),
				asString(body["pattern"]),
				asString(body["query"]),
				asString(body["url"]),
				asString(body["task_id"]),
				asString(body["text"]),
				asString(body["id"]),
			)
			if detail != "" {
				return detail
			}
			// Fall back to showing what we can: first non-empty value
			for _, v := range body {
				if s := asString(v); strings.TrimSpace(s) != "" {
					return truncateDisplayText(s, 80)
				}
			}
			return ""
		}
	}
	return t
}

func explorationLine(toolName, fallback string, env toolResultEnvelope) string {
	payload := env.payload
	data := env.data
	switch toolName {
	case "read_file":
		path := firstNonEmpty(asString(payload["file_path"]), asString(data["file_path"]), fallback, "file")
		return "Read " + path
	case "list_dir":
		path := firstNonEmpty(asString(payload["path"]), asString(data["path"]), fallback, ".")
		return "List " + path
	case "search_files":
		return formatSearchActionLine(firstNonEmpty(searchDetailFromPayload(payload, data, ""), fallback), "files")
	case "grep", "search_content":
		return formatSearchActionLine(firstNonEmpty(searchDetailFromPayload(payload, data, asString(payload["include"])), fallback), "content")
	case "fetch", "web_fetch":
		return "Fetch " + firstNonEmpty(asString(payload["url"]), asString(data["url"]), fallback, "url")
	case "web_search":
		query := firstNonEmpty(webSearchQueryFromMaps(payload, data), fallback)
		if query != "" {
			return "Search web for " + query
		}
		return "Search web"
	default:
		return "Run " + firstNonEmpty(fallback, toolName)
	}
}

func searchDetailFromPayload(payload, data map[string]any, includeFallback string) string {
	pattern := firstNonEmpty(asString(payload["pattern"]), asString(data["pattern"]))
	path := firstNonEmpty(asString(payload["path"]), asString(data["path"]))
	include := firstNonEmpty(asString(payload["include"]), asString(data["include"]), includeFallback)
	return appendSearchDetail(pattern, path, include)
}

func webSearchQueryFromMaps(payload, data map[string]any) string {
	return firstNonEmpty(asString(payload["query"]), asString(data["query"]))
}

func formatSearchActionLine(detail, fallback string) string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		detail = fallback
	}
	if strings.HasPrefix(detail, "Search ") {
		return detail
	}
	return "Search " + detail
}

func appendSearchDetail(subject, path, include string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return ""
	}
	if strings.TrimSpace(path) != "" {
		subject += " in " + strings.TrimSpace(path)
	}
	if strings.TrimSpace(include) != "" {
		subject += " (" + strings.TrimSpace(include) + ")"
	}
	return subject
}

func previousToolActionLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) >= 2 {
		return strings.TrimSpace(lines[1])
	}
	if len(lines) == 1 {
		return strings.TrimSpace(lines[0])
	}
	return ""
}

func todoToolTitle(toolName, text, state string) string {
	action := todoToolAction(toolName, state)
	detail := todoToolDetail(text)
	if detail == "" {
		return action
	}
	return action + "\n" + detail
}

func todoToolAction(toolName, state string) string {
	done := state == "done"
	switch strings.TrimSpace(toolName) {
	case "todo_add":
		if done {
			return "Todo added"
		}
		return "Adding todo"
	case "todo_update":
		if done {
			return "Todo updated"
		}
		return "Updating todo"
	case "todo_remove":
		if done {
			return "Todo removed"
		}
		return "Removing todo"
	case "todo_clear_done":
		if done {
			return "Cleared completed todos"
		}
		return "Clearing completed todos"
	case "todo_list":
		if done {
			return "Listed todos"
		}
		return "Listing todos"
	default:
		if done {
			return "Todo completed"
		}
		return "Updating todo"
	}
}

func todoToolDetail(text string) string {
	detail := toolCallDetail(text)
	if detail != "" && !strings.HasPrefix(detail, "{") {
		return detail
	}
	t := strings.TrimSpace(text)
	if idx := strings.Index(t, ":"); idx >= 0 {
		t = strings.TrimSpace(t[idx+1:])
	}
	if !strings.HasPrefix(t, "{") {
		return firstLine(t)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(t), &body); err != nil {
		return ""
	}
	return firstNonEmpty(asString(body["text"]), asString(body["id"]))
}

func editLine(toolName, fallback string, env toolResultEnvelope) string {
	payload := env.payload
	data := env.data
	switch toolName {
	case "write_file", "write":
		return "Edited " + firstNonEmpty(asString(payload["file_path"]), asString(data["file_path"]), fallback, "file")
	case "edit_file", "edit":
		return "Edited " + firstNonEmpty(asString(payload["file_path"]), asString(data["file_path"]), fallback, "file")
	case "apply_patch":
		files := stringSlice(firstNonEmptyAny(payload["files_changed"], data["files_changed"]))
		if len(files) == 1 {
			return "Edited " + files[0]
		}
		if len(files) > 1 {
			return fmt.Sprintf("Edited %d files", len(files))
		}
		return "Edited files"
	default:
		return "Edited " + firstNonEmpty(fallback, "files")
	}
}
