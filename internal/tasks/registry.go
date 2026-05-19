package tasks

import (
	"context"
	"encoding/json"

	"github.com/usewhale/whale/internal/core"
)

var excludedChildTools = map[string]bool{
	"parallel_reason":    true,
	"spawn_subagent":     true,
	"request_user_input": true,
	"shell_run":          true,
	"shell_wait":         true,
	"edit":               true,
	"write":              true,
	"apply_patch":        true,
	"update_plan":        true,
	"todo_add":           true,
	"todo_update":        true,
	"todo_remove":        true,
	"todo_clear_done":    true,
	"todo_list":          true,
}

func BuildReadOnlyRegistry(parent *core.ToolRegistry) (*core.ToolRegistry, error) {
	return core.NewToolRegistryChecked(ReadOnlyTools(parent))
}

func ReadOnlyTools(parent *core.ToolRegistry) []core.Tool {
	if parent == nil {
		return nil
	}
	out := []core.Tool{}
	for _, tool := range parent.Tools() {
		if tool == nil {
			continue
		}
		spec := core.DescribeTool(tool)
		if excludedChildTools[spec.Name] {
			continue
		}
		if !spec.ReadOnly && spec.ReadOnlyCheck == nil {
			continue
		}
		out = append(out, guardedReadOnlyTool{tool: tool, spec: spec})
	}
	return out
}

type guardedReadOnlyTool struct {
	tool core.Tool
	spec core.ToolSpec
}

func (t guardedReadOnlyTool) Name() string               { return t.spec.Name }
func (t guardedReadOnlyTool) Description() string        { return t.spec.Description }
func (t guardedReadOnlyTool) Parameters() map[string]any { return t.spec.Parameters }
func (t guardedReadOnlyTool) ReadOnly() bool             { return true }
func (t guardedReadOnlyTool) SupportsParallel() bool     { return t.spec.SupportsParallel }
func (t guardedReadOnlyTool) Capabilities() []string {
	return append([]string(nil), t.spec.Capabilities...)
}
func (t guardedReadOnlyTool) ApprovalHint() string { return t.spec.ApprovalHint }

func (t guardedReadOnlyTool) Run(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	if t.spec.Name == "shell_run" && shellBackgroundRequested(call.Input) {
		return core.ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Content:    errorContent("background_not_allowed", "subagents cannot start background shell tasks"),
			IsError:    true,
		}, nil
	}
	if !core.IsReadOnlyToolCall(t.spec, call) {
		return core.ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Content:    errorContent("read_only_required", "subagent tools are restricted to read-only calls"),
			IsError:    true,
		}, nil
	}
	return t.tool.Run(ctx, call)
}

func shellBackgroundRequested(raw string) bool {
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return false
	}
	background, _ := args["background"].(bool)
	return background
}
