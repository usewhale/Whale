package policy

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

func permissionKind(toolName string) string {
	switch toolName {
	case "read_file", "grep", "search_files", "list_dir", "load_skill":
		return "read"
	case "edit", "write", "multi_edit":
		return "edit"
	case "shell_run":
		return "shell"
	case "write_stdin":
		return "terminal"
	case "remember", "remember_update", "forget":
		return "memory"
	case "spawn_subagent":
		return "task"
	case "cancel_subagent":
		return "mutating_tool"
	case "web_search":
		return "web_search"
	case "fetch", "web_fetch":
		return "web_fetch"
	default:
		if strings.HasPrefix(toolName, "mcp__") {
			return "mcp"
		}
		return toolName
	}
}

// readScopeTarget returns the directory a read-scoped search tool operates on,
// ignoring regex/glob query fields. It falls back to "*" when no search root
// is given (the tool searches the whole workspace).
func readScopeTarget(call core.ToolCall) string {
	var body map[string]any
	if err := json.Unmarshal([]byte(call.Input), &body); err == nil {
		for _, key := range []string{"file_path", "path", "dir", "directory"} {
			if v, ok := body[key].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	return "*"
}
func permissionTarget(call core.ToolCall) string {
	if call.Name == "write_stdin" {
		return "write_stdin"
	}
	if strings.HasPrefix(call.Name, "mcp__") {
		return call.Name
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(call.Input), &body); err == nil {
		if call.Name == "fetch" || call.Name == "web_fetch" {
			if host := webFetchPermissionHost(body); host != "" {
				return "host:" + host
			}
		}
		for _, key := range []string{"file_path", "path", "pattern", "url", "query", "name"} {
			if v, ok := body[key].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	if strings.TrimSpace(call.Input) != "" {
		return strings.TrimSpace(call.Input)
	}
	return "*"
}
func webFetchPermissionHost(body map[string]any) string {
	raw, _ := body["url"].(string)
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || strings.TrimSpace(u.Hostname()) == "" {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	return strings.TrimPrefix(host, "www.")
}
func hashString(v string) string {
	sum := sha256.Sum256([]byte(v))
	return fmt.Sprintf("%x", sum[:8])
}

func stringSliceValue(v any) []string {
	switch raw := v.(type) {
	case []string:
		return append([]string(nil), raw...)
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
