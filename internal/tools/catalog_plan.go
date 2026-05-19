package tools

import (
	"context"

	"github.com/usewhale/whale/internal/core"
)

func (b *Toolset) planRuntimeTools() []core.Tool {
	return []core.Tool{
		toolFn{
			name:        "update_plan",
			description: "Update the execution checklist with pending, in_progress, and completed steps.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"explanation": map[string]any{"type": "string"},
					"plan": map[string]any{
						"type":     "array",
						"minItems": 1,
						"items": map[string]any{
							"type":                 "object",
							"additionalProperties": false,
							"properties": map[string]any{
								"step":   map[string]any{"type": "string"},
								"status": map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}},
							},
							"required": []string{"step", "status"},
						},
					},
				},
				"required": []string{"plan"},
			},
			readOnly: false,
			fn:       b.planRuntimePlaceholder,
		},
	}
}

func (b *Toolset) planRuntimePlaceholder(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	return marshalToolError(call, "tool_unavailable", "plan runtime tool is handled by agent runtime"), nil
}
