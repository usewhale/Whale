package history

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

var environmentInventoryKeys = []string{
	"system:",
	"system：",
	"version:",
	"version：",
	"build:",
	"build：",
	"系统:",
	"系统：",
	"版本:",
	"版本：",
	"构建号:",
	"构建号：",
}

func IsEnvironmentInventoryBlock(text string) bool {
	block := strings.ToLower(strings.TrimSpace(text))
	if block == "" {
		return false
	}
	matched := 0
	for _, key := range environmentInventoryKeys {
		if strings.Contains(block, strings.ToLower(key)) {
			matched++
			if matched >= 2 {
				return true
			}
		}
	}
	return false
}

func SummarizeHydratedToolCall(call core.ToolCall) string {
	if strings.TrimSpace(call.Input) == "" {
		return call.Name
	}
	if call.Name == "shell_run" {
		var body map[string]any
		if err := json.Unmarshal([]byte(call.Input), &body); err == nil {
			if cmd, _ := body["command"].(string); strings.TrimSpace(cmd) != "" {
				return "Running " + strings.TrimSpace(cmd)
			}
		}
	}
	if call.Name == "workflow" {
		var body map[string]any
		if err := json.Unmarshal([]byte(call.Input), &body); err == nil {
			action, _ := body["action"].(string)
			saveAs, _ := body["saveAs"].(string)
			name, _ := body["name"].(string)
			scriptPath, _ := body["scriptPath"].(string)
			action = strings.TrimSpace(action)
			switch {
			case strings.TrimSpace(saveAs) != "":
				if action == "" {
					action = "create"
				}
				return "workflow: " + action + " " + strings.TrimSpace(saveAs)
			case strings.TrimSpace(scriptPath) != "":
				return "workflow: " + strings.TrimSpace(scriptPath)
			case strings.TrimSpace(name) != "":
				if action == "" {
					return "workflow: " + strings.TrimSpace(name)
				}
				return "workflow: " + action + " " + strings.TrimSpace(name)
			case action == "list":
				return "workflow: list"
			}
		}
	}
	switch call.Name {
	case "todo_add", "todo_update":
		var body map[string]any
		if err := json.Unmarshal([]byte(call.Input), &body); err == nil {
			if text, _ := body["text"].(string); strings.TrimSpace(text) != "" {
				return call.Name + ": " + strings.TrimSpace(text)
			}
			if id, _ := body["id"].(string); strings.TrimSpace(id) != "" {
				return call.Name + ": " + strings.TrimSpace(id)
			}
		}
	case "todo_remove":
		var body map[string]any
		if err := json.Unmarshal([]byte(call.Input), &body); err == nil {
			if id, _ := body["id"].(string); strings.TrimSpace(id) != "" {
				return call.Name + ": " + strings.TrimSpace(id)
			}
		}
	case "todo_list", "todo_clear_done":
		return call.Name
	}
	return fmt.Sprintf("%s: %s", call.Name, strings.TrimSpace(call.Input))
}
