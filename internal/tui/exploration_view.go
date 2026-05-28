package tui

import (
	"strings"

	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func projectExplorationMessages(messages []tuirender.UIMessage) []tuirender.UIMessage {
	out := make([]tuirender.UIMessage, 0, len(messages))
	var group explorationGroup
	flush := func() {
		if !group.used {
			return
		}
		out = append(out, group.message())
		group = explorationGroup{}
	}
	for _, msg := range messages {
		item, ok := explorationItemFromMessage(msg)
		if !ok {
			flush()
			out = append(out, msg)
			continue
		}
		group.add(item)
	}
	flush()
	return out
}

type explorationGroup struct {
	used    bool
	running bool
	items   []explorationItem
}

type explorationItem struct {
	kind    string
	detail  string
	running bool
}

func (g *explorationGroup) add(item explorationItem) {
	g.used = true
	if item.running {
		g.running = true
	}
	g.items = append(g.items, item)
}

func (g explorationGroup) message() tuirender.UIMessage {
	title := "Explored"
	role := "exploration_group"
	if g.running {
		title = "Exploring"
		role = "exploration_group_running"
	}
	lines := []string{title}
	lines = append(lines, g.lines()...)
	return tuirender.UIMessage{
		Role:     role,
		Kind:     tuirender.KindToolCall,
		Text:     strings.Join(lines, "\n"),
		ToolName: "exploration_group",
	}
}

func (g explorationGroup) lines() []string {
	out := make([]string, 0, len(g.items))
	for i := 0; i < len(g.items); i++ {
		item := g.items[i]
		if item.kind == "read" {
			names := []string{item.detail}
			seen := map[string]struct{}{item.detail: {}}
			for i+1 < len(g.items) && g.items[i+1].kind == "read" {
				i++
				next := g.items[i].detail
				if _, ok := seen[next]; ok {
					continue
				}
				seen[next] = struct{}{}
				names = append(names, next)
			}
			out = append(out, "Read "+strings.Join(names, ", "))
			continue
		}
		out = append(out, explorationItemLine(item))
	}
	return out
}

func explorationItemFromMessage(msg tuirender.UIMessage) (explorationItem, bool) {
	if msg.Kind != tuirender.KindToolCall && msg.Kind != tuirender.KindToolResult {
		return explorationItem{}, false
	}
	state := focusToolState(msg)
	if state != "running" && state != "done" {
		return explorationItem{}, false
	}
	kind := normalExplorationKind(msg)
	if kind == "" {
		return explorationItem{}, false
	}
	detail := normalExplorationDetail(msg, kind)
	if detail == "" {
		if isMCPToolName(msg.ToolName) {
			return explorationItem{}, false
		}
		switch kind {
		case "read":
			detail = "file"
		case "list":
			detail = "."
		case "search":
			detail = "content"
		}
	}
	return explorationItem{kind: kind, detail: detail, running: state == "running"}, true
}

func isMCPToolName(toolName string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(toolName)), "mcp__")
}

func normalExplorationKind(msg tuirender.UIMessage) string {
	name := strings.ToLower(strings.TrimSpace(msg.ToolName))
	switch name {
	case "read_file":
		return "read"
	case "list_dir":
		return "list"
	case "search_files", "grep", "search_content":
		return "search"
	}
	if isMCPToolName(name) {
		switch {
		case strings.Contains(name, "read_text_file"), strings.Contains(name, "read_file"):
			return "read"
		case strings.Contains(name, "list_directory"), strings.Contains(name, "list_dir"):
			return "list"
		case strings.Contains(name, "search"), strings.Contains(name, "grep"):
			return "search"
		}
	}
	line := focusActionLine(msg.Text)
	switch {
	case strings.HasPrefix(line, "Read "):
		return "read"
	case strings.HasPrefix(line, "List "):
		return "list"
	case strings.HasPrefix(line, "Search ") && !strings.HasPrefix(line, "Search web"):
		return "search"
	default:
		return ""
	}
}

func normalExplorationDetail(msg tuirender.UIMessage, kind string) string {
	line := focusActionLine(msg.Text)
	switch kind {
	case "read":
		if detail, ok := strings.CutPrefix(line, "Read "); ok {
			return strings.TrimSpace(detail)
		}
	case "list":
		if detail, ok := strings.CutPrefix(line, "List "); ok {
			return strings.TrimSpace(detail)
		}
	case "search":
		if detail, ok := strings.CutPrefix(line, "Search "); ok && !strings.HasPrefix(detail, "web") {
			return strings.TrimSpace(detail)
		}
	}
	return focusStandardDetail(focusToolSummaryInput{ToolName: msg.ToolName, Text: msg.Text})
}

func explorationItemLine(item explorationItem) string {
	switch item.kind {
	case "read":
		return "Read " + item.detail
	case "list":
		return "List " + item.detail
	case "search":
		return "Search " + item.detail
	default:
		return item.detail
	}
}
