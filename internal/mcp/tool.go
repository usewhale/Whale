package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/usewhale/whale/internal/core"
)

type Tool struct {
	manager        *Manager
	serverName     string
	toolName       string
	registeredName string
	spec           *sdk.Tool
	allowedDirs    []string
	workspaceRoot  string
}

func (t *Tool) Name() string { return t.registeredName }

func (t *Tool) Description() string {
	desc := ""
	if t.spec != nil {
		desc = strings.TrimSpace(t.spec.Description)
	}
	if desc == "" {
		desc = "MCP tool"
	}
	detail := fmt.Sprintf("%s (MCP server: %s, tool: %s)", desc, t.serverName, t.toolName)
	if len(t.allowedDirs) > 0 {
		detail += fmt.Sprintf(" Allowed directories: %s.", strings.Join(t.allowedDirs, ", "))
		if root := strings.TrimSpace(t.workspaceRoot); root != "" && !pathInAllowedDirs(root, t.allowedDirs) {
			detail += " Current workspace is outside those directories; use Whale built-in read_file, list_dir, search_files, or grep for workspace files."
		}
	}
	return detail
}

func (t *Tool) Parameters() map[string]any {
	if t.spec == nil || t.spec.InputSchema == nil {
		return emptySchema()
	}
	b, err := json.Marshal(t.spec.InputSchema)
	if err != nil {
		return emptySchema()
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil || out == nil {
		return emptySchema()
	}
	if _, ok := out["type"]; !ok {
		out["type"] = "object"
	}
	if _, ok := out["properties"]; !ok {
		out["properties"] = map[string]any{}
	}
	return out
}

func (t *Tool) ReadOnly() bool {
	return t.spec != nil && t.spec.Annotations != nil && t.spec.Annotations.ReadOnlyHint
}

func (t *Tool) Capabilities() []string {
	out := []string{}
	if t.ReadOnly() {
		out = append(out, "mcp.read")
	}
	if len(t.allowedDirs) == 0 {
		return out
	}
	out = append(out, "mcp_filesystem")
	for _, dir := range t.allowedDirs {
		if dir = strings.TrimSpace(dir); dir != "" {
			out = append(out, "mcp_filesystem_allowed_dir:"+dir)
		}
	}
	return out
}

func (t *Tool) ApprovalHint() string {
	return fmt.Sprintf("Calls MCP server %s tool %s; external tools may have side effects.", t.serverName, t.toolName)
}

func (t *Tool) Run(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	args := map[string]any{}
	if strings.TrimSpace(call.Input) != "" {
		if err := json.Unmarshal([]byte(call.Input), &args); err != nil {
			return mcpError(call, "invalid_mcp_input", err.Error()), nil
		}
	}
	if deniedPath, ok := t.deniedPath(args); ok {
		return mcpAllowedDirsDenied(call, deniedPath, t.allowedDirs), nil
	}
	result, err := t.manager.CallTool(ctx, t.serverName, t.toolName, args)
	if err != nil {
		return mcpError(call, "mcp_call_failed", err.Error()), nil
	}
	return mcpResult(call, t.serverName, t.toolName, result), nil
}

func emptySchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": true,
	}
}

func mcpResult(call core.ToolCall, serverName, toolName string, result *sdk.CallToolResult) core.ToolResult {
	text, media := flattenContent(result.Content)
	data := map[string]any{
		"server": serverName,
		"tool":   toolName,
		"text":   text,
	}
	if len(media) > 0 {
		data["media"] = media
	}
	if result.StructuredContent != nil {
		data["structured_content"] = result.StructuredContent
	}
	env := core.NewToolSuccessEnvelope(data)
	if result.IsError {
		env.OK = false
		env.Success = false
		env.Code = "mcp_tool_error"
		env.Error = strings.TrimSpace(text)
		env.Message = strings.TrimSpace(text)
		if env.Error == "" {
			env.Error = "mcp tool returned an error"
			env.Message = env.Error
		}
	}
	content, err := core.MarshalToolEnvelope(env)
	if err != nil {
		return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: fmt.Sprintf(`{"ok":false,"error":%q,"code":"mcp_result_encode_failed"}`, err.Error()), IsError: true}
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content, IsError: result.IsError}
}

func mcpError(call core.ToolCall, code, message string) core.ToolResult {
	content, err := core.MarshalToolEnvelope(core.NewToolErrorEnvelope(code, message))
	if err != nil {
		content = fmt.Sprintf(`{"ok":false,"error":%q,"code":%q}`, message, code)
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content, IsError: true}
}

func mcpAllowedDirsDenied(call core.ToolCall, path string, allowedDirs []string) core.ToolResult {
	message := fmt.Sprintf("MCP filesystem server cannot access %s; allowed directories: %s. Use Whale built-in file tools for this path, or add the directory to the MCP server configuration.", path, strings.Join(allowedDirs, ", "))
	env := core.NewToolErrorEnvelope("mcp_allowed_dirs_denied", message)
	env.Data = map[string]any{
		"path":         path,
		"allowed_dirs": append([]string(nil), allowedDirs...),
	}
	content, err := core.MarshalToolEnvelope(env)
	if err != nil {
		content = fmt.Sprintf(`{"ok":false,"success":false,"error":%q,"code":"mcp_allowed_dirs_denied"}`, message)
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content, IsError: true}
}

func (t *Tool) deniedPath(args map[string]any) (string, bool) {
	if len(t.allowedDirs) == 0 {
		return "", false
	}
	for _, path := range mcpPathArgs(args) {
		path = strings.TrimSpace(path)
		if path == "" || !filepath.IsAbs(expandHomePath(path)) {
			continue
		}
		if !pathInAllowedDirs(path, t.allowedDirs) {
			return cleanAbsPath(path), true
		}
	}
	return "", false
}

func mcpPathArgs(args map[string]any) []string {
	keys := []string{"path", "file_path", "root", "directory", "source", "destination"}
	out := []string{}
	for _, key := range keys {
		if s, ok := args[key].(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	if xs, ok := args["paths"].([]any); ok {
		for _, x := range xs {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	}
	return out
}

func pathInAllowedDirs(path string, allowedDirs []string) bool {
	path = canonicalAccessPath(path)
	for _, dir := range allowedDirs {
		dir = canonicalAccessPath(dir)
		if path == dir {
			return true
		}
		rel, err := filepath.Rel(dir, path)
		if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func canonicalAccessPath(path string) string {
	clean := cleanAbsPath(path)
	if clean == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(clean); err == nil {
		return filepath.Clean(real)
	}
	cur := clean
	var suffix []string
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return clean
		}
		suffix = append([]string{filepath.Base(cur)}, suffix...)
		if real, err := filepath.EvalSymlinks(parent); err == nil {
			parts := append([]string{filepath.Clean(real)}, suffix...)
			return filepath.Clean(filepath.Join(parts...))
		}
		cur = parent
	}
}

func cleanAbsPath(path string) string {
	path = expandHomePath(strings.TrimSpace(path))
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func expandHomePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "~" || strings.HasPrefix(path, "~/") {
		if userHome, err := os.UserHomeDir(); err == nil && userHome != "" {
			if path == "~" {
				return userHome
			}
			return filepath.Join(userHome, strings.TrimPrefix(path, "~/"))
		}
	}
	if path == "$HOME" || strings.HasPrefix(path, "$HOME/") {
		if userHome, err := os.UserHomeDir(); err == nil && userHome != "" {
			if path == "$HOME" {
				return userHome
			}
			return filepath.Join(userHome, strings.TrimPrefix(path, "$HOME/"))
		}
	}
	return path
}

func flattenContent(content []sdk.Content) (string, []map[string]any) {
	textParts := []string{}
	media := []map[string]any{}
	for _, item := range content {
		switch v := item.(type) {
		case *sdk.TextContent:
			textParts = append(textParts, v.Text)
		case *sdk.ImageContent:
			media = append(media, map[string]any{"type": "image", "mime_type": v.MIMEType, "bytes": len(v.Data)})
		case *sdk.AudioContent:
			media = append(media, map[string]any{"type": "audio", "mime_type": v.MIMEType, "bytes": len(v.Data)})
		default:
			b, err := json.Marshal(v)
			if err != nil {
				textParts = append(textParts, fmt.Sprintf("%v", v))
			} else {
				textParts = append(textParts, string(b))
			}
		}
	}
	return strings.TrimSpace(strings.Join(textParts, "\n")), media
}
