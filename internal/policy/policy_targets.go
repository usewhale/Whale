package policy

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

func permissionKind(toolName string) string {
	switch toolName {
	case "read_file", "grep", "search_files", "list_dir", "load_skill":
		return "read"
	case "edit", "write", "apply_patch":
		return "edit"
	case "shell_run":
		return "shell"
	case "remember", "remember_update", "forget":
		return "memory"
	case "spawn_subagent":
		return "task"
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
	if strings.HasPrefix(call.Name, "mcp__") {
		return call.Name
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(call.Input), &body); err == nil {
		for _, key := range []string{"file_path", "path", "pattern", "url", "query", "name"} {
			if v, ok := body[key].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		if call.Name == "apply_patch" {
			if v, ok := body["patch"].(string); ok && strings.TrimSpace(v) != "" {
				return "apply_patch:" + hashString(v)
			}
		}
	}
	if strings.TrimSpace(call.Input) != "" {
		return strings.TrimSpace(call.Input)
	}
	return "*"
}
func hashString(v string) string {
	sum := sha256.Sum256([]byte(v))
	return fmt.Sprintf("%x", sum[:8])
}
