package tui

import (
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/app"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func (m model) focusEnabled() bool {
	return strings.TrimSpace(m.viewMode) == app.ViewModeFocus
}

func (m model) focusMessages(messages []tuirender.UIMessage) []tuirender.UIMessage {
	if !m.focusEnabled() || len(messages) == 0 {
		return messages
	}
	return projectFocusMessages(messages)
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
				Text: text,
			})
		}
		tools = focusToolSummary{}
	}
	for _, msg := range messages {
		if isFocusHiddenToolMessage(msg) {
			tools.add(msg)
			continue
		}
		flushTools()
		if isFocusHiddenMessage(msg) {
			continue
		}
		out = append(out, msg)
	}
	flushTools()
	return out
}

func isFocusHiddenMessage(msg tuirender.UIMessage) bool {
	return msg.Kind == tuirender.KindThinking || msg.Role == "think"
}

func isFocusHiddenToolMessage(msg tuirender.UIMessage) bool {
	return msg.Kind == tuirender.KindToolCall || msg.Kind == tuirender.KindToolResult
}

type focusToolSummary struct {
	used    bool
	shell   int
	explore int
	edit    int
	task    int
	plan    int
	todo    int
	other   int
	running int
	failed  int
	denied  int
}

func (s *focusToolSummary) add(msg tuirender.UIMessage) {
	s.used = true
	switch focusToolKind(msg) {
	case "shell":
		s.shell++
	case "explore":
		s.explore++
	case "edit":
		s.edit++
	case "task":
		s.task++
	case "plan":
		s.plan++
	case "todo":
		s.todo++
	default:
		s.other++
	}
	switch strings.TrimSpace(msg.Role) {
	case "tool", "result_running", "shell_result_running":
		s.running++
	case "result_failed", "result_error", "result_timeout", "shell_result_failed", "shell_result_error", "shell_result_timeout":
		s.failed++
	case "result_denied", "result_canceled", "shell_result_denied", "shell_result_canceled":
		s.denied++
	}
}

func (s focusToolSummary) text() string {
	parts := make([]string, 0, 8)
	add := func(n int, singular, plural string) {
		if n == 0 {
			return
		}
		label := plural
		if n == 1 {
			label = singular
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, label))
	}
	add(s.shell, "shell command", "shell commands")
	add(s.explore, "file/search read", "file/search reads")
	add(s.edit, "edit", "edits")
	add(s.task, "subagent task", "subagent tasks")
	add(s.plan, "plan update", "plan updates")
	add(s.todo, "todo update", "todo updates")
	add(s.other, "tool", "tools")
	if len(parts) == 0 {
		return ""
	}
	status := make([]string, 0, 3)
	if s.running > 0 {
		status = append(status, fmt.Sprintf("%d running", s.running))
	}
	if s.failed > 0 {
		status = append(status, fmt.Sprintf("%d failed", s.failed))
	}
	if s.denied > 0 {
		status = append(status, fmt.Sprintf("%d denied/canceled", s.denied))
	}
	text := "Ran " + strings.Join(parts, ", ")
	if len(status) > 0 {
		text += " (" + strings.Join(status, ", ") + ")"
	}
	return text
}

func focusToolKind(msg tuirender.UIMessage) string {
	if kind := toolDisplayKind(msg.ToolName); kind != "unknown" {
		return kind
	}
	text := strings.TrimSpace(msg.Text)
	switch {
	case strings.HasPrefix(text, "Running "), strings.HasPrefix(text, "Ran "):
		return "shell"
	case strings.HasPrefix(text, "Exploring"), strings.HasPrefix(text, "Explored"):
		return "explore"
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
