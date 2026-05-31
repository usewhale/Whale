package tasks

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

const (
	CapabilityWorkspaceRead = "workspace.read"
	CapabilityWebSearch     = "web.search"
	CapabilityWebFetch      = "web.fetch"
	CapabilityMCPRead       = "mcp.read"
)

var knownSubagentCapabilities = map[string]bool{
	CapabilityWorkspaceRead: true,
	CapabilityWebSearch:     true,
	CapabilityWebFetch:      true,
	CapabilityMCPRead:       true,
}

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
	return BuildCapabilityRegistry(parent, nil)
}

func BuildCapabilityRegistry(parent *core.ToolRegistry, capabilities []string) (*core.ToolRegistry, error) {
	tools, err := CapabilityTools(parent, capabilities)
	if err != nil {
		return nil, err
	}
	return core.NewToolRegistryChecked(tools)
}

func AllowedCapabilityToolNames(parent *core.ToolRegistry, capabilities []string) ([]string, error) {
	tools, err := CapabilityTools(parent, capabilities)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool != nil {
			out = append(out, tool.Name())
		}
	}
	return out, nil
}

func ReadOnlyTools(parent *core.ToolRegistry) []core.Tool {
	tools, _ := CapabilityTools(parent, nil)
	return tools
}

func CapabilityTools(parent *core.ToolRegistry, capabilities []string) ([]core.Tool, error) {
	if parent == nil {
		return nil, nil
	}
	capSet, err := normalizeCapabilities(capabilities)
	if err != nil {
		return nil, err
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
		if !capabilityAllowed(spec, capSet) {
			continue
		}
		out = append(out, guardedReadOnlyTool{tool: tool, spec: spec})
	}
	return out, nil
}

func normalizeCapabilities(capabilities []string) (map[string]bool, error) {
	if capabilities == nil {
		capabilities = []string{CapabilityWorkspaceRead}
	}
	out := map[string]bool{}
	for _, cap := range capabilities {
		cap = strings.TrimSpace(cap)
		if cap == "" {
			continue
		}
		if !knownSubagentCapabilities[cap] {
			known := []string{CapabilityWorkspaceRead, CapabilityWebSearch, CapabilityWebFetch, CapabilityMCPRead}
			return nil, fmt.Errorf("unknown subagent capability %q; allowed: %s", cap, strings.Join(known, ", "))
		}
		out[cap] = true
	}
	return out, nil
}

func capabilityAllowed(spec core.ToolSpec, capSet map[string]bool) bool {
	if len(capSet) == 0 {
		return false
	}
	for _, cap := range spec.Capabilities {
		if capSet[strings.TrimSpace(cap)] {
			return true
		}
	}
	// Compatibility for read-only MCP tools that predate the explicit mcp.read
	// capability marker.
	if capSet[CapabilityMCPRead] && spec.ReadOnly && slices.ContainsFunc(spec.Capabilities, func(cap string) bool {
		return strings.HasPrefix(cap, "mcp_") || strings.HasPrefix(cap, "mcp.")
	}) {
		return true
	}
	return false
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
	if res, blocked := t.guardReadOnly(call); blocked {
		return res, nil
	}
	return t.tool.Run(ctx, call)
}

func (t guardedReadOnlyTool) RunWithProgress(ctx context.Context, call core.ToolCall, progress func(core.ToolProgress)) (core.ToolResult, error) {
	if res, blocked := t.guardReadOnly(call); blocked {
		return res, nil
	}
	if runner, ok := t.tool.(core.ToolProgressRunner); ok {
		return runner.RunWithProgress(ctx, call, progress)
	}
	return t.tool.Run(ctx, call)
}

func (t guardedReadOnlyTool) guardReadOnly(call core.ToolCall) (core.ToolResult, bool) {
	if core.IsReadOnlyToolCall(t.spec, call) {
		return core.ToolResult{}, false
	}
	return core.ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Content:    errorContent("read_only_required", "subagent tools are restricted to read-only calls"),
		IsError:    true,
	}, true
}
