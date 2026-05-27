package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/app"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func (m model) focusEnabled() bool {
	return strings.TrimSpace(m.viewMode) == app.ViewModeFocus
}

func (m model) focusMessages(messages []tuirender.UIMessage) []tuirender.UIMessage {
	if len(messages) == 0 {
		return messages
	}
	if m.focusEnabled() {
		return projectFocusMessages(messages)
	}
	return projectExpandedFocusMessages(messages)
}

func (m *model) toggleFocusView() bool {
	next := app.ViewModeFocus
	if m.focusEnabled() {
		next = app.ViewModeDefault
	}
	m.viewMode = next
	m.persistViewMode(next)
	m.setEphemeralInfo(app.ViewModeToggleMessage(next))
	if !m.busy {
		m.status = "ready"
	}
	m.refreshViewportContentFollow(true)
	return true
}

func (m *model) persistViewMode(mode string) {
	if m.svc == nil {
		return
	}
	if err := m.svc.SetViewMode(mode); err != nil {
		m.append("error", err.Error())
	}
}

func (m *model) redrawTranscriptForFocusToggleCmd() tea.Cmd {
	if m.page != pageChat || len(m.transcript) == 0 {
		m.refreshViewportContentFollow(true)
		return nil
	}
	m.nativeScrollbackPrinted = 0
	printCmd := m.flushNativeScrollbackCmd()
	m.refreshViewportContentFollow(true)
	if printCmd == nil {
		return nil
	}
	return tea.Sequence(clearScreenCmd(), printCmd)
}

func projectFocusMessages(messages []tuirender.UIMessage) []tuirender.UIMessage {
	out := make([]tuirender.UIMessage, 0, len(messages))
	var tools focusToolSummary
	flushTools := func() {
		if !tools.used {
			return
		}
		text := tools.text()
		if text != "" {
			out = append(out, tuirender.UIMessage{
				Role: "tool_summary",
				Kind: tuirender.KindToolSummary,
				Text: text + " (ctrl+o to expand)",
			})
		}
		tools = focusToolSummary{}
	}
	for _, msg := range messages {
		if isFocusHiddenToolMessage(msg) {
			tools.add(msg)
			continue
		}
		if isFocusHiddenMessage(msg) {
			continue
		}
		flushTools()
		out = append(out, msg)
	}
	flushTools()
	return out
}

func projectExpandedFocusMessages(messages []tuirender.UIMessage) []tuirender.UIMessage {
	out := make([]tuirender.UIMessage, 0, len(messages))
	for _, msg := range messages {
		if isFocusHiddenMessage(msg) || isFocusHiddenToolMessage(msg) {
			msg.Text = appendFocusToggleHint(msg.Text, "collapse")
		}
		out = append(out, msg)
	}
	return out
}

func appendFocusToggleHint(text, action string) string {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return text
	}
	hint := " (ctrl+o to " + action + ")"
	lines := strings.Split(text, "\n")
	lines[0] = strings.TrimRight(lines[0], " ") + hint
	return strings.Join(lines, "\n")
}

func isFocusHiddenMessage(msg tuirender.UIMessage) bool {
	return msg.Kind == tuirender.KindThinking || msg.Role == "think"
}

func isFocusHiddenToolMessage(msg tuirender.UIMessage) bool {
	return msg.Kind == tuirender.KindToolCall || msg.Kind == tuirender.KindToolResult || msg.Kind == tuirender.KindSubagent
}

type focusToolSummary struct {
	used   bool
	shell  focusSummaryBucket
	search focusSummaryBucket
	read   focusSummaryBucket
	list   focusSummaryBucket
	edit   focusSummaryBucket
	task   focusSummaryBucket
	plan   focusSummaryBucket
	todo   focusSummaryBucket
	other  focusSummaryBucket
}

type focusSummaryBucket struct {
	count   int
	running int
	failed  int
	denied  int
	details []string
}

func (s *focusToolSummary) add(msg tuirender.UIMessage) {
	s.used = true
	state := focusToolState(msg)
	detail := focusToolDetail(msg)
	switch focusToolKind(msg) {
	case "shell":
		s.shell.add(state, detail)
	case "search":
		s.search.add(state, detail)
	case "read":
		s.read.add(state, detail)
	case "list":
		s.list.add(state, detail)
	case "edit":
		s.edit.add(state, detail)
	case "task":
		s.task.add(state, focusTaskDetail(msg))
	case "plan":
		s.plan.add(state, "")
	case "todo":
		s.todo.add(state, "")
	default:
		s.other.add(state, "")
	}
}

func (s focusToolSummary) text() string {
	parts := make([]string, 0, 8)
	add := func(text string) {
		if text != "" {
			parts = append(parts, text)
		}
	}
	add(focusHintSummary(s.search, "Searching for", "Searched for", "Denied", "pattern", "patterns", focusQuoteHint))
	add(focusHintSummary(s.read, "Reading", "Read", "Denied", "file", "files", focusPlainHint))
	add(focusHintSummary(s.list, "Listing", "Listed", "Denied", "directory", "directories", focusPlainHint))
	add(focusShellSummary(s.shell))
	add(focusHintSummary(s.edit, "Editing", "Edited", "Denied", "file", "files", focusPlainHint))
	add(focusTaskSummary(s.task))
	add(focusSimpleSummary(s.plan, "Updating plan", "Updated plan", "plan update", "plan updates"))
	add(focusSimpleSummary(s.todo, "Updating todos", "Updated todos", "todo update", "todo updates"))
	add(focusCountSummary(s.other, "Running", "Ran", "tool", "tools"))
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}

func (b *focusSummaryBucket) add(state, detail string) {
	b.count++
	switch state {
	case "running":
		b.running++
	case "failed":
		b.failed++
	case "denied":
		b.denied++
	}
	if detail != "" {
		b.details = append(b.details, detail)
	}
}

func (b focusSummaryBucket) activeVerb(running, done string) string {
	if b.running > 0 {
		return running
	}
	return done
}

func (b focusSummaryBucket) allDenied() bool {
	return b.count > 0 && b.denied == b.count
}

func (b focusSummaryBucket) statusSuffix() string {
	status := make([]string, 0, 3)
	if b.running > 0 {
		status = append(status, fmt.Sprintf("%d running", b.running))
	}
	if b.failed > 0 {
		status = append(status, fmt.Sprintf("%d failed", b.failed))
	}
	if b.denied > 0 {
		status = append(status, fmt.Sprintf("%d denied/canceled", b.denied))
	}
	if len(status) == 0 {
		return ""
	}
	return " (" + strings.Join(status, ", ") + ")"
}

func focusShellSummary(b focusSummaryBucket) string {
	if b.count == 0 {
		return ""
	}
	if b.allDenied() {
		return fmt.Sprintf("Denied %d %s%s", b.count, pluralize(b.count, "shell command", "shell commands"), b.statusSuffix())
	}
	verb := b.activeVerb("Running", "Ran")
	if len(b.details) == 1 {
		return fmt.Sprintf("%s shell: %s%s", verb, b.details[0], b.statusSuffix())
	}
	if len(b.details) > 1 && len(b.details) == b.count && len(strings.Join(b.details, "; ")) <= 120 {
		return fmt.Sprintf("%s %d shell commands: %s%s", verb, b.count, strings.Join(b.details, "; "), b.statusSuffix())
	}
	return fmt.Sprintf("%s %d %s%s", verb, b.count, pluralize(b.count, "shell command", "shell commands"), b.statusSuffix())
}

func focusCountSummary(b focusSummaryBucket, runningVerb, doneVerb, singular, plural string) string {
	if b.count == 0 {
		return ""
	}
	return fmt.Sprintf("%s %d %s%s", b.activeVerb(runningVerb, doneVerb), b.count, pluralize(b.count, singular, plural), b.statusSuffix())
}

func focusHintSummary(b focusSummaryBucket, runningVerb, doneVerb, deniedVerb, singular, plural string, formatHint func(string) string) string {
	if b.count == 0 {
		return ""
	}
	verb := b.activeVerb(runningVerb, doneVerb)
	if b.allDenied() {
		verb = deniedVerb
	}
	text := fmt.Sprintf("%s %d %s", verb, b.count, pluralize(b.count, singular, plural))
	if b.running > 0 && !b.allDenied() {
		if hint := latestFocusHint(b.details, formatHint); hint != "" {
			text += ": " + hint
		}
	}
	return text + b.statusSuffix()
}

func latestFocusHint(details []string, format func(string) string) string {
	for i := len(details) - 1; i >= 0; i-- {
		if detail := format(strings.TrimSpace(details[i])); detail != "" {
			return detail
		}
	}
	return ""
}

func focusPlainHint(detail string) string {
	return truncateFocusToolDetail(detail)
}

func focusQuoteHint(detail string) string {
	detail = strings.Trim(detail, `"`)
	if detail == "" {
		return ""
	}
	return `"` + truncateFocusToolDetail(detail) + `"`
}

func focusSimpleSummary(b focusSummaryBucket, runningText, doneText, singular, plural string) string {
	if b.count == 0 {
		return ""
	}
	if b.count == 1 {
		return b.activeVerb(runningText, doneText) + b.statusSuffix()
	}
	return fmt.Sprintf("%s: %d %s%s", b.activeVerb(runningText, doneText), b.count, pluralize(b.count, singular, plural), b.statusSuffix())
}

func focusTaskSummary(b focusSummaryBucket) string {
	if b.count == 0 {
		return ""
	}
	if b.count == 1 && len(b.details) == 1 {
		return b.details[0] + b.statusSuffix()
	}
	return focusCountSummary(b, "Running", "Ran", "subagent task", "subagent tasks")
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

func focusToolState(msg tuirender.UIMessage) string {
	switch strings.TrimSpace(msg.Role) {
	case "tool", "result_running", "shell_result_running":
		return "running"
	case "result_failed", "result_error", "result_timeout", "shell_result_failed", "shell_result_error", "shell_result_timeout":
		return "failed"
	case "result_denied", "result_canceled", "shell_result_denied", "shell_result_canceled":
		return "denied"
	default:
		return "done"
	}
}

func focusToolDetail(msg tuirender.UIMessage) string {
	text := strings.TrimSpace(msg.Text)
	kind := focusToolKindFromName(msg.ToolName)
	if kind == "shell" {
		for _, prefix := range []string{"Running ", "Ran "} {
			if after, ok := strings.CutPrefix(text, prefix); ok {
				line := strings.TrimSpace(strings.SplitN(after, "\n", 2)[0])
				return truncateFocusToolDetail(line)
			}
		}
	}
	if line := focusActionDetail(text); line != "" {
		return truncateFocusToolDetail(line)
	}
	if detail := focusRunningDetail(text, msg.ToolName); detail != "" {
		return truncateFocusToolDetail(detail)
	}
	return ""
}

func focusTaskDetail(msg tuirender.UIMessage) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(msg.Text), "\n", 2)[0])
	if line == "" {
		return ""
	}
	return truncateFocusToolDetail(line)
}

func truncateFocusToolDetail(text string) string {
	const max = 96
	text = strings.Join(strings.Fields(text), " ")
	if len([]rune(text)) <= max {
		return text
	}
	runes := []rune(text)
	return string(runes[:max-1]) + "..."
}

func focusToolKind(msg tuirender.UIMessage) string {
	if kind := focusToolKindFromName(msg.ToolName); kind != "unknown" {
		return kind
	}
	text := strings.TrimSpace(msg.Text)
	if kind := focusToolKindFromText(text); kind != "unknown" {
		return kind
	}
	return "unknown"
}

func focusToolKindFromName(toolName string) string {
	name := strings.TrimSpace(toolName)
	switch name {
	case "shell_run", "shell_wait":
		return "shell"
	case "read_file", "fetch", "web_fetch":
		return "read"
	case "list_dir":
		return "list"
	case "search_files", "grep", "search_content", "web_search":
		return "search"
	case "write_file", "edit_file", "apply_patch", "write", "edit":
		return "edit"
	case "parallel_reason", "spawn_subagent":
		return "task"
	case "update_plan":
		return "plan"
	case "todo_add", "todo_list", "todo_update", "todo_remove", "todo_clear_done":
		return "todo"
	}
	name = strings.ToLower(name)
	switch {
	case strings.Contains(name, "read_text_file"), strings.Contains(name, "read_file"), strings.Contains(name, "fetch"):
		return "read"
	case strings.Contains(name, "list_directory"), strings.Contains(name, "list_dir"):
		return "list"
	case strings.Contains(name, "search"), strings.Contains(name, "grep"):
		return "search"
	case strings.Contains(name, "write_file"), strings.Contains(name, "edit_file"), strings.Contains(name, "apply_patch"):
		return "edit"
	default:
		return "unknown"
	}
}

func focusToolKindFromText(text string) string {
	switch {
	case strings.HasPrefix(text, "Running shell:"), strings.HasPrefix(text, "Ran shell:"):
		return "shell"
	case strings.HasPrefix(text, "Exploring"), strings.HasPrefix(text, "Explored"):
		return focusExploreKindFromAction(focusActionLine(text))
	case strings.HasPrefix(text, "Subagent"), strings.HasPrefix(text, "Parallel reasoning"):
		return "task"
	case strings.HasPrefix(text, "Updating plan"), strings.HasPrefix(text, "Updated plan"):
		return "plan"
	case strings.Contains(text, "todo"):
		return "todo"
	case strings.HasPrefix(text, "Edited "), strings.HasPrefix(text, "Created "), strings.HasPrefix(text, "Deleted "), strings.HasPrefix(text, "Wrote "):
		return "edit"
	default:
		return "unknown"
	}
}

func focusRunningDetail(text, toolName string) string {
	for _, prefix := range []string{"Running ", "Run "} {
		if after, ok := strings.CutPrefix(strings.TrimSpace(text), prefix); ok {
			detail := strings.TrimSpace(strings.SplitN(after, "\n", 2)[0])
			if focusIsToolNameDetail(detail, toolName) {
				return ""
			}
			return detail
		}
	}
	return ""
}

func focusIsToolNameDetail(detail, toolName string) bool {
	detail = strings.TrimSpace(detail)
	toolName = strings.TrimSpace(toolName)
	if detail == "" || toolName == "" {
		return false
	}
	if detail == toolName {
		return true
	}
	if strings.HasPrefix(toolName, "mcp__") {
		parts := strings.Split(toolName, "__")
		return len(parts) > 0 && detail == parts[len(parts)-1]
	}
	return false
}

func focusExploreKindFromAction(action string) string {
	action = strings.TrimSpace(action)
	switch {
	case strings.HasPrefix(action, "Read "), strings.HasPrefix(action, "Fetch "):
		return "read"
	case strings.HasPrefix(action, "List "):
		return "list"
	case strings.HasPrefix(action, "Search "):
		return "search"
	default:
		return "read"
	}
}

func focusActionDetail(text string) string {
	line := focusActionLine(text)
	if line == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(line, "Search web for "):
		return strings.TrimSpace(strings.TrimPrefix(line, "Search web for "))
	case strings.HasPrefix(line, "Search "):
		return strings.TrimSpace(strings.TrimPrefix(line, "Search "))
	case strings.HasPrefix(line, "Read "):
		return strings.TrimSpace(strings.TrimPrefix(line, "Read "))
	case strings.HasPrefix(line, "List "):
		return strings.TrimSpace(strings.TrimPrefix(line, "List "))
	case strings.HasPrefix(line, "Fetch "):
		return strings.TrimSpace(strings.TrimPrefix(line, "Fetch "))
	case strings.HasPrefix(line, "Edited "):
		return strings.TrimSpace(strings.TrimPrefix(line, "Edited "))
	case strings.HasPrefix(line, "Created "):
		return strings.TrimSpace(strings.TrimPrefix(line, "Created "))
	case strings.HasPrefix(line, "Deleted "):
		return strings.TrimSpace(strings.TrimPrefix(line, "Deleted "))
	case strings.HasPrefix(line, "Wrote "):
		return strings.TrimSpace(strings.TrimPrefix(line, "Wrote "))
	default:
		return ""
	}
}

func focusActionLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i := 1; i < len(lines); i++ {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	if len(lines) == 1 {
		return strings.TrimSpace(lines[0])
	}
	return ""
}
