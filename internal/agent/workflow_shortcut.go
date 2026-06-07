package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

func (a *Agent) runWorkflowShortcut(ctx context.Context, sessionID, input string, tools *core.ToolRegistry, events chan<- AgentEvent) error {
	tool := tools.Get("workflow")
	if tool == nil {
		return fmt.Errorf("workflow tool unavailable")
	}
	call := core.ToolCall{
		ID:    "workflow_shortcut",
		Name:  "workflow",
		Input: workflowShortcutToolInput(input),
	}
	assistant, err := a.store.Create(ctx, core.Message{
		SessionID:    sessionID,
		Role:         core.RoleAssistant,
		ToolCalls:    []core.ToolCall{call},
		FinishReason: core.FinishReasonToolUse,
	})
	if err != nil {
		return fmt.Errorf("create workflow shortcut assistant message: %w", err)
	}
	if !sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypeToolCall, ToolCall: &call}) {
		return ctx.Err()
	}
	res, err := tool.Run(ctx, call)
	if err != nil {
		return fmt.Errorf("run workflow shortcut: %w", err)
	}
	if _, err := a.store.Create(ctx, core.Message{
		SessionID:   sessionID,
		Role:        core.RoleTool,
		ToolResults: []core.ToolResult{res},
	}); err != nil {
		return fmt.Errorf("create workflow shortcut tool message: %w", err)
	}
	if !sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypeToolResult, Result: &res}) {
		return ctx.Err()
	}
	done := assistant
	done.FinishReason = core.FinishReasonEndTurn
	if !sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypeDone, Message: &done}) {
		return ctx.Err()
	}
	return nil
}

func workflowShortcutToolInput(input string) string {
	text := strings.ToLower(strings.TrimSpace(input))
	body := map[string]string{"action": "status"}
	if strings.Contains(text, "run") || strings.Contains(text, "运行") {
		body["action"] = "run"
		if name := workflowNameFromDisabledShortcutInput(input); name != "" {
			body["name"] = name
		}
	} else if strings.Contains(text, "list") || strings.Contains(text, "哪些") || strings.Contains(text, "available") || strings.Contains(text, "可用") {
		body["action"] = "list"
	} else if strings.Contains(text, "create") || strings.Contains(text, "generate") || strings.Contains(text, "创建") || strings.Contains(text, "新增") {
		body["action"] = "create"
	}
	b, err := json.Marshal(body)
	if err != nil {
		return `{"action":"status"}`
	}
	return string(b)
}

func workflowNameFromDisabledShortcutInput(input string) string {
	for _, token := range strings.Fields(input) {
		token = strings.Trim(token, "`'\".,;:，。；：")
		lower := strings.ToLower(token)
		if lower == "" || lower == "run" || lower == "workflow" || lower == "workflows" {
			continue
		}
		if strings.Contains(lower, "workflow") {
			continue
		}
		if strings.Contains(lower, "-") || strings.Contains(lower, "_") {
			return token
		}
	}
	return ""
}
