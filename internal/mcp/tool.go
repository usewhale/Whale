package mcp

import (
	"context"
	"encoding/json"
	"fmt"
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
	return fmt.Sprintf("%s (MCP server: %s, tool: %s)", desc, t.serverName, t.toolName)
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
