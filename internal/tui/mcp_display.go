package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	mcpKindUnknown = "unknown"
	mcpKindList    = "list"
	mcpKindRead    = "read"
	mcpKindSearch  = "search"
	mcpKindWrite   = "write"
)

type mcpDisplayInfo struct {
	Server string
	Tool   string
	Args   map[string]any
	Kind   string
}

func parseMCPDisplayInfo(toolName, text string) (mcpDisplayInfo, bool) {
	server, tool, ok := parseMCPToolName(toolName)
	if !ok {
		return mcpDisplayInfo{}, false
	}
	return mcpDisplayInfo{
		Server: server,
		Tool:   tool,
		Args:   parseMCPArgsFromToolText(text),
		Kind:   classifyMCPTool(tool),
	}, true
}

func parseMCPToolName(toolName string) (server, tool string, ok bool) {
	parts := strings.SplitN(strings.TrimSpace(toolName), "__", 3)
	if len(parts) != 3 || parts[0] != "mcp" {
		return "", "", false
	}
	server = strings.TrimSpace(parts[1])
	tool = strings.TrimSpace(parts[2])
	return server, tool, server != "" && tool != ""
}

func classifyMCPTool(tool string) string {
	switch strings.TrimSpace(strings.ToLower(tool)) {
	case "list_allowed_directories", "list_directory", "list_directory_with_sizes", "directory_tree":
		return mcpKindList
	case "read_text_file", "read_file", "read_multiple_files", "read_media_file", "get_file_info":
		return mcpKindRead
	case "search_files", "grep", "search", "query":
		return mcpKindSearch
	case "write_file", "edit_file", "create_directory", "move_file":
		return mcpKindWrite
	default:
		return mcpKindUnknown
	}
}

func parseMCPArgsFromToolText(text string) map[string]any {
	body := strings.TrimSpace(text)
	if body == "" {
		return nil
	}
	if before, after, ok := strings.Cut(body, ":"); ok && strings.TrimSpace(before) != "" {
		body = strings.TrimSpace(after)
	}
	if !strings.HasPrefix(body, "{") {
		return parseMCPRenderedArgs(text)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(body), &args); err != nil || len(args) == 0 {
		return parseMCPRenderedArgs(text)
	}
	return args
}

func (m mcpDisplayInfo) label() string {
	if m.Server == "" && m.Tool == "" {
		return "MCP tool"
	}
	if m.Server == "" {
		return "MCP " + m.Tool
	}
	if m.Tool == "" {
		return "MCP " + m.Server
	}
	return "MCP " + m.Server + " · " + m.Tool
}

func (m mcpDisplayInfo) invocation() string {
	if m.Server == "" {
		return m.Tool
	}
	if m.Tool == "" {
		return m.Server
	}
	return m.Server + "." + m.Tool
}

func (m mcpDisplayInfo) primaryArgDetail() string {
	for _, key := range []string{"path", "file_path", "pattern", "query", "url", "name", "id"} {
		if value := asDisplayString(m.Args[key]); value != "" {
			return value
		}
	}
	return ""
}

func (m mcpDisplayInfo) focusDetail(text string) string {
	detail := focusStandardDetail(focusToolSummaryInput{ToolName: "mcp", Text: text})
	if detail != "" && detail != m.Tool && detail != m.invocation() {
		return detail
	}
	if arg := m.primaryArgDetail(); arg != "" {
		return truncateFocusToolDetail(arg)
	}
	if arg := mcpRenderedArgDetail(text); arg != "" {
		return truncateFocusToolDetail(arg)
	}
	return ""
}

func mcpStartedText(toolName, text string) string {
	info, ok := parseMCPDisplayInfo(toolName, text)
	if !ok {
		return "Running " + firstNonEmpty(toolCallDetail(text), toolName)
	}
	lines := []string{"Calling " + info.label()}
	lines = append(lines, mcpArgLines(info.Args, 3)...)
	return strings.Join(lines, "\n")
}

func mcpCompletedTitle(toolName, raw, previous string) string {
	info, ok := parseMCPDisplayInfo(toolName, previous)
	if !ok {
		info, ok = parseMCPDisplayInfo(toolName, "")
	}
	if !ok {
		return "Called " + firstNonEmpty(toolName, "MCP tool")
	}
	env := parseToolEnvelope(raw)
	if server := strings.TrimSpace(asString(env.data["server"])); server != "" {
		info.Server = server
	}
	if tool := strings.TrimSpace(asString(env.data["tool"])); tool != "" {
		info.Tool = tool
	}
	lines := []string{"Called " + info.label()}
	lines = append(lines, mcpArgLines(info.Args, 3)...)
	return strings.Join(lines, "\n")
}

func summarizeMCPResult(env toolResultEnvelope, successBySignal bool) (string, string) {
	if !successBySignal {
		return summarizeFailedResult(env, "MCP tool failed")
	}
	duration := formatDurationMS(asInt64(env.metadata["duration_ms"]))
	parts := []string{"✓"}
	if duration != "" {
		parts = append(parts, duration)
	}
	text := summarizeMCPOutput(asString(env.data["text"]))
	if text == "" {
		text = summarizeMCPStructuredContent(env.data["structured_content"])
	}
	if media := countMCPMediaItems(firstNonEmptyAny(env.data["media"], env.payload["media"])); media > 0 {
		if text != "" {
			text += "\n"
		}
		text += fmt.Sprintf("%d media item(s)", media)
	}
	if text != "" {
		return "result_ok", strings.Join(parts, " · ") + "\n" + text
	}
	return "result_ok", strings.Join(parts, " · ")
}

func summarizeMCPOutput(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if flat := flattenSmallJSONObject(text); flat != "" {
		return flat
	}
	return summarizeShellOutput(text)
}

func summarizeMCPStructuredContent(value any) string {
	text := asDisplayString(value)
	if text == "" {
		return ""
	}
	return summarizeMCPOutput(text)
}

func flattenSmallJSONObject(text string) string {
	const maxJSONChars = 5000
	const maxKeys = 12
	if len(text) > maxJSONChars || !strings.HasPrefix(strings.TrimSpace(text), "{") {
		return ""
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(text), &body); err != nil || len(body) == 0 || len(body) > maxKeys {
		return ""
	}
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		value := asDisplayString(body[key])
		if value == "" {
			continue
		}
		lines = append(lines, truncateShellOutputLine(key+": "+value))
	}
	return strings.Join(lines, "\n")
}

func mcpArgLines(args map[string]any, max int) []string {
	if len(args) == 0 || max <= 0 {
		return nil
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, minInt(max, len(keys)))
	for _, key := range keys {
		value := asDisplayString(args[key])
		if value == "" {
			continue
		}
		out = append(out, key+": "+truncateDisplayText(value, 120))
		if len(out) == max {
			break
		}
	}
	return out
}

func mcpRenderedArgDetail(text string) string {
	args := parseMCPRenderedArgs(text)
	for _, key := range []string{"path", "file_path", "pattern", "query", "url", "name", "id"} {
		if value := asDisplayString(args[key]); value != "" {
			return value
		}
	}
	return ""
}

func parseMCPRenderedArgs(text string) map[string]any {
	args := map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "path", "file_path", "pattern", "query", "url", "name", "id":
			if value = strings.TrimSpace(value); value != "" {
				args[strings.TrimSpace(key)] = value
			}
		}
	}
	if len(args) == 0 {
		return nil
	}
	return args
}

func asDisplayString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case fmt.Stringer:
		return strings.TrimSpace(t.String())
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return strings.TrimSpace(fmt.Sprint(t))
		}
		return strings.TrimSpace(string(b))
	}
}

func isMCPDisplayTool(toolName string) bool {
	_, _, ok := parseMCPToolName(toolName)
	return ok
}

func countMCPMediaItems(v any) int {
	switch t := v.(type) {
	case []any:
		return len(t)
	case []map[string]any:
		return len(t)
	default:
		return 0
	}
}
