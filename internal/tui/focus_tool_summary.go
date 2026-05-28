package tui

import (
	"strings"

	xansi "github.com/charmbracelet/x/ansi"

	tuirender "github.com/usewhale/whale/internal/tui/render"
)

type focusToolSummaryInput struct {
	ToolName     string
	ToolIdentity string
	Role         string
	Text         string
	Kind         tuirender.MessageKind
}

type focusToolSummaryItem struct {
	Kind     string
	Detail   string
	Identity string
}

type focusToolSummaryProvider interface {
	Match(focusToolSummaryInput) bool
	Summarize(focusToolSummaryInput) focusToolSummaryItem
}

var focusToolSummaryProviders = []focusToolSummaryProvider{
	focusShellSummaryProvider{},
	focusMCPSummaryProvider{},
	focusExploreSummaryProvider{},
	focusEditSummaryProvider{},
	focusTaskSummaryProvider{},
	focusSimpleSummaryProvider{},
}

func focusSummarizeToolMessage(msg tuirender.UIMessage) focusToolSummaryItem {
	input := focusToolSummaryInput{
		ToolName:     strings.TrimSpace(msg.ToolName),
		ToolIdentity: strings.TrimSpace(msg.ToolIdentity),
		Role:         strings.TrimSpace(msg.Role),
		Text:         strings.TrimSpace(msg.Text),
		Kind:         msg.Kind,
	}
	for _, provider := range focusToolSummaryProviders {
		if provider.Match(input) {
			item := provider.Summarize(input)
			if item.Kind != "" {
				return item
			}
		}
	}
	return focusToolSummaryItem{Kind: "other"}
}

type focusShellSummaryProvider struct{}

func (focusShellSummaryProvider) Match(input focusToolSummaryInput) bool {
	return focusToolKindFromName(input.ToolName) == "shell" ||
		strings.HasPrefix(input.Text, "Running shell:") ||
		strings.HasPrefix(input.Text, "Ran shell:")
}

func (focusShellSummaryProvider) Summarize(input focusToolSummaryInput) focusToolSummaryItem {
	return focusToolSummaryItem{
		Kind:     "shell",
		Detail:   focusShellDetail(input.Text),
		Identity: focusShellIdentity(input),
	}
}

type focusExploreSummaryProvider struct{}

type focusMCPSummaryProvider struct{}

func (focusMCPSummaryProvider) Match(input focusToolSummaryInput) bool {
	return isMCPDisplayTool(input.ToolName)
}

func (focusMCPSummaryProvider) Summarize(input focusToolSummaryInput) focusToolSummaryItem {
	info, ok := parseMCPDisplayInfo(input.ToolName, input.Text)
	if !ok {
		return focusToolSummaryItem{Kind: "mcp"}
	}
	switch info.Kind {
	case mcpKindRead, mcpKindList, mcpKindSearch:
		return focusToolSummaryItem{Kind: info.Kind, Detail: info.focusDetail(input.Text)}
	default:
		return focusToolSummaryItem{Kind: "mcp", Detail: info.invocation()}
	}
}

func (focusExploreSummaryProvider) Match(input focusToolSummaryInput) bool {
	switch focusToolKindFromName(input.ToolName) {
	case "read", "web", "search", "list":
		return true
	}
	if strings.HasPrefix(input.Text, "Exploring") || strings.HasPrefix(input.Text, "Explored") {
		switch focusExploreKindFromAction(focusActionLine(input.Text)) {
		case "read", "web", "search", "list":
			return true
		}
	}
	return false
}

func (focusExploreSummaryProvider) Summarize(input focusToolSummaryInput) focusToolSummaryItem {
	kind := focusToolKindFromName(input.ToolName)
	if kind != "read" && kind != "web" && kind != "search" && kind != "list" {
		kind = focusExploreKindFromAction(focusActionLine(input.Text))
	}
	return focusToolSummaryItem{Kind: kind, Detail: focusStandardDetail(input)}
}

type focusEditSummaryProvider struct{}

func (focusEditSummaryProvider) Match(input focusToolSummaryInput) bool {
	if focusToolKindFromName(input.ToolName) == "edit" {
		return true
	}
	return strings.HasPrefix(input.Text, "Edited ") ||
		strings.HasPrefix(input.Text, "Created ") ||
		strings.HasPrefix(input.Text, "Deleted ") ||
		strings.HasPrefix(input.Text, "Wrote ")
}

func (focusEditSummaryProvider) Summarize(input focusToolSummaryInput) focusToolSummaryItem {
	return focusToolSummaryItem{Kind: "edit", Detail: focusEditDetail(input)}
}

type focusTaskSummaryProvider struct{}

func (focusTaskSummaryProvider) Match(input focusToolSummaryInput) bool {
	return input.Kind == tuirender.KindSubagent ||
		focusToolKindFromName(input.ToolName) == "task" ||
		strings.HasPrefix(input.Text, "Subagent") ||
		strings.HasPrefix(input.Text, "Parallel reasoning")
}

func (focusTaskSummaryProvider) Summarize(input focusToolSummaryInput) focusToolSummaryItem {
	return focusToolSummaryItem{Kind: "task", Detail: focusTaskDetail(input.Text)}
}

type focusSimpleSummaryProvider struct{}

func (focusSimpleSummaryProvider) Match(input focusToolSummaryInput) bool {
	switch focusToolKindFromName(input.ToolName) {
	case "plan", "todo":
		return true
	}
	return strings.HasPrefix(input.Text, "Updating plan") ||
		strings.HasPrefix(input.Text, "Updated plan") ||
		strings.Contains(input.Text, "todo")
}

func (focusSimpleSummaryProvider) Summarize(input focusToolSummaryInput) focusToolSummaryItem {
	kind := focusToolKindFromName(input.ToolName)
	if kind != "plan" && kind != "todo" {
		if strings.HasPrefix(input.Text, "Updating plan") || strings.HasPrefix(input.Text, "Updated plan") {
			kind = "plan"
		} else {
			kind = "todo"
		}
	}
	return focusToolSummaryItem{Kind: kind}
}

func focusStandardDetail(input focusToolSummaryInput) string {
	if line := focusActionDetail(input.Text); line != "" {
		return truncateFocusToolDetail(line)
	}
	if detail := focusRunningDetail(input.Text, input.ToolName); detail != "" {
		return truncateFocusToolDetail(detail)
	}
	return ""
}

func focusEditDetail(input focusToolSummaryInput) string {
	if detail := focusEditDiffHeaderDetail(input.Text); detail != "" {
		return truncateFocusToolDetail(detail)
	}
	if detail := focusEditActionDetail(input.Text); detail != "" {
		return truncateFocusToolDetail(detail)
	}
	return focusStandardDetail(input)
}

func focusEditDiffHeaderDetail(text string) string {
	headers := make([]string, 0, 2)
	seen := map[string]struct{}{}
	for _, raw := range strings.Split(strings.TrimSpace(text), "\n") {
		line := strings.TrimSpace(xansi.Strip(raw))
		if !looksLikeFocusEditDiffHeader(line) {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		headers = append(headers, line)
		if len(headers) == 2 {
			break
		}
	}
	if len(headers) == 0 {
		return ""
	}
	return strings.Join(headers, "; ")
}

func looksLikeFocusEditDiffHeader(line string) bool {
	if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
		return false
	}
	open := strings.LastIndex(line, " (+")
	close := strings.LastIndex(line, ")")
	if open <= 0 || close != len(line)-1 {
		return false
	}
	stats := strings.TrimSuffix(strings.TrimPrefix(line[open:], " ("), ")")
	parts := strings.Fields(stats)
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "+") || !strings.HasPrefix(parts[1], "-") {
		return false
	}
	return focusUnsignedNumber(parts[0][1:]) && focusUnsignedNumber(parts[1][1:])
}

func focusUnsignedNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func focusEditActionDetail(text string) string {
	for _, raw := range strings.Split(strings.TrimSpace(text), "\n") {
		line := strings.TrimSpace(xansi.Strip(raw))
		switch {
		case strings.HasPrefix(line, "Edited "):
			return strings.TrimSpace(strings.TrimPrefix(line, "Edited "))
		case strings.HasPrefix(line, "Created "):
			return strings.TrimSpace(strings.TrimPrefix(line, "Created "))
		case strings.HasPrefix(line, "Deleted "):
			return strings.TrimSpace(strings.TrimPrefix(line, "Deleted "))
		case strings.HasPrefix(line, "Wrote "):
			return strings.TrimSpace(strings.TrimPrefix(line, "Wrote "))
		}
	}
	return ""
}

func focusShellDetail(text string) string {
	command := focusShellDisplayCommand(text)
	if strings.TrimSpace(command) == "shell command" {
		return ""
	}
	return truncateFocusToolDetail(command)
}

func focusShellIdentity(input focusToolSummaryInput) string {
	command := strings.TrimSpace(input.ToolIdentity)
	if command == "" {
		command = focusShellRawCommand(input.Text)
	}
	if strings.TrimSpace(command) == "shell command" {
		return ""
	}
	return command
}

func focusShellDisplayCommand(text string) string {
	command := focusShellRawCommand(text)
	line := strings.TrimSpace(strings.SplitN(command, "\n", 2)[0])
	return strings.Join(strings.Fields(line), " ")
}

func focusShellRawCommand(text string) string {
	text = strings.TrimSpace(text)
	for _, prefix := range []string{"Running ", "Ran "} {
		if after, ok := strings.CutPrefix(text, prefix); ok {
			command := strings.TrimSpace(after)
			if afterShell, ok := strings.CutPrefix(command, "shell:"); ok {
				command = strings.TrimSpace(afterShell)
			}
			return command
		}
	}
	return ""
}

func focusTaskDetail(text string) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0])
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

func focusToolKindFromName(toolName string) string {
	name := strings.TrimSpace(toolName)
	switch name {
	case "shell_run", "shell_wait", "shell_cancel":
		return "shell"
	case "read_file":
		return "read"
	case "fetch", "web_fetch":
		return "web"
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
	case strings.Contains(name, "fetch"):
		return "web"
	case strings.Contains(name, "read_text_file"), strings.Contains(name, "read_file"):
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

func focusExploreKindFromAction(action string) string {
	action = strings.TrimSpace(action)
	switch {
	case strings.HasPrefix(action, "Read "):
		return "read"
	case strings.HasPrefix(action, "Fetch "):
		return "web"
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
