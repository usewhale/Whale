package tasks

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/mcp"
)

const (
	CapabilityWorkspaceRead  = "workspace.read"
	CapabilityWorkspaceWrite = "workspace.write"
	CapabilityShellRead      = "shell.read"
	CapabilityShellRun       = "shell.run"
	CapabilityWebSearch      = "web.search"
	CapabilityWebFetch       = "web.fetch"
	CapabilityMCPRead        = "mcp.read"
)

var knownSubagentCapabilities = map[string]bool{
	CapabilityWorkspaceRead:  true,
	CapabilityWorkspaceWrite: true,
	CapabilityShellRead:      true,
	CapabilityShellRun:       true,
	CapabilityWebSearch:      true,
	CapabilityWebFetch:       true,
	CapabilityMCPRead:        true,
}

var excludedChildTools = map[string]bool{
	"parallel_reason":    true,
	"spawn_subagent":     true,
	"request_user_input": true,
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
	tools, err := CapabilityToolsForPermission(parent, capabilities, AgentPermissionReadOnly)
	if err != nil {
		return nil, err
	}
	return core.NewToolRegistryChecked(tools)
}

func BuildAgentRegistry(parent *core.ToolRegistry, capabilities []string, permissionMode string) (*core.ToolRegistry, error) {
	return BuildAgentRegistryForMCPServers(parent, capabilities, permissionMode, nil)
}

func BuildAgentRegistryForMCPServers(parent *core.ToolRegistry, capabilities []string, permissionMode string, mcpServers []string, disallowed ...[]string) (*core.ToolRegistry, error) {
	tools, err := CapabilityToolsForPermission(parent, capabilities, permissionMode, firstStringSlice(disallowed))
	if err != nil {
		return nil, err
	}
	tools = filterMCPServerTools(tools, mcpServers)
	return core.NewToolRegistryChecked(tools)
}

func AllowedCapabilityToolNames(parent *core.ToolRegistry, capabilities []string) ([]string, error) {
	tools, err := CapabilityToolsForPermission(parent, capabilities, AgentPermissionReadOnly)
	if err != nil {
		return nil, err
	}
	return toolNames(tools), nil
}

func AllowedAgentToolNames(parent *core.ToolRegistry, capabilities []string, permissionMode string) ([]string, error) {
	return AllowedAgentToolNamesForMCPServers(parent, capabilities, permissionMode, nil)
}

func AllowedAgentToolNamesForMCPServers(parent *core.ToolRegistry, capabilities []string, permissionMode string, mcpServers []string, disallowed ...[]string) ([]string, error) {
	tools, err := CapabilityToolsForPermission(parent, capabilities, permissionMode, firstStringSlice(disallowed))
	if err != nil {
		return nil, err
	}
	tools = filterMCPServerTools(tools, mcpServers)
	return toolNames(tools), nil
}

func toolNames(tools []core.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool != nil {
			out = append(out, tool.Name())
		}
	}
	return out
}

func ReadOnlyTools(parent *core.ToolRegistry) []core.Tool {
	tools, _ := CapabilityToolsForPermission(parent, nil, AgentPermissionReadOnly)
	return tools
}

func CapabilityTools(parent *core.ToolRegistry, capabilities []string) ([]core.Tool, error) {
	return CapabilityToolsForPermission(parent, capabilities, AgentPermissionReadOnly)
}

func CapabilityToolsForPermission(parent *core.ToolRegistry, capabilities []string, permissionMode string, disallowed ...[]string) ([]core.Tool, error) {
	if parent == nil {
		return nil, nil
	}
	selection, err := normalizeToolSelection(parent, capabilities, firstStringSlice(disallowed))
	if err != nil {
		return nil, err
	}
	readonly := strings.TrimSpace(permissionMode) == "" || strings.TrimSpace(permissionMode) == AgentPermissionReadOnly
	out := []core.Tool{}
	for _, tool := range parent.Tools() {
		if tool == nil {
			continue
		}
		spec := core.DescribeTool(tool)
		if excludedChildTools[spec.Name] {
			continue
		}
		if !toolSelectionAllowed(spec, selection) {
			continue
		}
		if readonly {
			if !spec.ReadOnly && spec.ReadOnlyCheck == nil {
				continue
			}
			out = append(out, guardedReadOnlyTool{tool: tool, spec: spec})
			continue
		}
		if shouldGuardCapabilityReadOnly(spec, selection) {
			out = append(out, guardedReadOnlyTool{tool: tool, spec: spec})
			continue
		}
		out = append(out, tool)
	}
	return out, nil
}

type toolSelection struct {
	capabilities map[string]bool
	tools        map[string]bool
	denyCaps     map[string]bool
	denyTools    map[string]bool
}

func normalizeToolSelection(parent *core.ToolRegistry, selectors, disallowed []string) (toolSelection, error) {
	if selectors == nil {
		selectors = []string{CapabilityWorkspaceRead}
	}
	out := toolSelection{
		capabilities: map[string]bool{},
		tools:        map[string]bool{},
		denyCaps:     map[string]bool{},
		denyTools:    map[string]bool{},
	}
	if err := addToolSelectors(parent, selectors, out.capabilities, out.tools, "tools"); err != nil {
		return toolSelection{}, err
	}
	if err := addToolSelectors(parent, disallowed, out.denyCaps, out.denyTools, "disallowedTools"); err != nil {
		return toolSelection{}, err
	}
	return out, nil
}

func addToolSelectors(parent *core.ToolRegistry, selectors []string, caps, tools map[string]bool, field string) error {
	for _, selector := range selectors {
		selector = strings.TrimSpace(selector)
		if selector == "" {
			continue
		}
		if selector == "*" {
			return fmt.Errorf("agent %s selector %q is only supported by fork/trusted child agents", field, selector)
		}
		if knownSubagentCapabilities[selector] {
			caps[selector] = true
			continue
		}
		if parent == nil || parent.Get(selector) == nil {
			known := []string{CapabilityWorkspaceRead, CapabilityWorkspaceWrite, CapabilityShellRead, CapabilityShellRun, CapabilityWebSearch, CapabilityWebFetch, CapabilityMCPRead}
			return fmt.Errorf("unknown agent %s selector %q; use a known capability (%s) or an available tool name", field, selector, strings.Join(known, ", "))
		}
		if excludedChildTools[selector] {
			return fmt.Errorf("agent %s selector %q is not allowed for child agents", field, selector)
		}
		tools[selector] = true
	}
	return nil
}

func agentToolMode(selectors, resolvedTools []string, permissionMode string) string {
	if len(resolvedTools) == 0 {
		return "model_only"
	}
	switch strings.TrimSpace(permissionMode) {
	case AgentPermissionTrusted:
		return "trusted"
	case AgentPermissionReadOnly, "":
		return "read_only"
	default:
		return "custom"
	}
}

func toolSelectionAllowed(spec core.ToolSpec, selection toolSelection) bool {
	if selection.denyTools[spec.Name] || specHasDeniedCapability(spec, selection.denyCaps) {
		return false
	}
	if len(selection.capabilities) == 0 && len(selection.tools) == 0 {
		return false
	}
	if selection.tools[spec.Name] {
		return true
	}
	if spec.Name == "shell_run" && selection.capabilities[CapabilityShellRead] && spec.ReadOnlyCheck != nil {
		return true
	}
	if (spec.Name == "shell_wait" || spec.Name == "shell_cancel") && (selection.capabilities[CapabilityShellRead] || selection.capabilities[CapabilityShellRun]) {
		return true
	}
	for _, cap := range spec.Capabilities {
		if selection.capabilities[strings.TrimSpace(cap)] {
			return true
		}
	}
	// Compatibility for read-only MCP tools that predate the explicit mcp.read
	// capability marker.
	if selection.capabilities[CapabilityMCPRead] && spec.ReadOnly && slices.ContainsFunc(spec.Capabilities, func(cap string) bool {
		return strings.HasPrefix(cap, "mcp_") || strings.HasPrefix(cap, "mcp.")
	}) {
		return true
	}
	return false
}

func specHasDeniedCapability(spec core.ToolSpec, denyCaps map[string]bool) bool {
	for _, cap := range spec.Capabilities {
		if denyCaps[strings.TrimSpace(cap)] {
			return true
		}
	}
	if denyCaps[CapabilityMCPRead] && spec.ReadOnly && slices.ContainsFunc(spec.Capabilities, func(cap string) bool {
		return strings.HasPrefix(cap, "mcp_") || strings.HasPrefix(cap, "mcp.")
	}) {
		return true
	}
	return false
}

func shouldGuardCapabilityReadOnly(spec core.ToolSpec, selection toolSelection) bool {
	return spec.Name == "shell_run" && selection.capabilities[CapabilityShellRead] && !selection.capabilities[CapabilityShellRun] && !selection.tools[spec.Name]
}

func firstStringSlice(values [][]string) []string {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

func filterMCPServerTools(tools []core.Tool, mcpServers []string) []core.Tool {
	if len(mcpServers) == 0 {
		return tools
	}
	allowed := map[string]bool{}
	for _, server := range mcpServers {
		server = strings.TrimSpace(server)
		if server == "" {
			continue
		}
		allowed[mcp.NormalizeServerNameForToolName(server)] = true
	}
	if len(allowed) == 0 {
		return tools
	}
	out := make([]core.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		server, _, ok := mcp.ParseQualifiedToolName(tool.Name())
		if ok && !allowed[server] {
			continue
		}
		out = append(out, tool)
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
