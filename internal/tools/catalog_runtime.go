package tools

import (
	"context"

	"github.com/usewhale/whale/internal/core"
)

func (b *Toolset) requestInputTools() []core.Tool {
	return []core.Tool{
		toolFn{
			name:        "request_user_input",
			description: "Request user input for one to three short questions and wait for the response. Use this for branch decisions and key assumptions. The UI will add a free-form \"None of the above\" option automatically — do not include an \"Other\" option in your list.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"questions": map[string]any{
						"type":        "array",
						"minItems":    1,
						"maxItems":    3,
						"description": "Questions to show the user. Prefer one.",
						"items": map[string]any{
							"type":                 "object",
							"additionalProperties": false,
							"properties": map[string]any{
								"id":       map[string]any{"type": "string"},
								"header":   map[string]any{"type": "string"},
								"question": map[string]any{"type": "string"},
								"options": map[string]any{
									"type":        "array",
									"minItems":    1,
									"maxItems":    4,
									"description": "Provide 1-4 mutually exclusive choices. The UI will add a free-form \"None of the above\" automatically; do not include an Other option.",
									"items": map[string]any{
										"type":                 "object",
										"additionalProperties": false,
										"properties": map[string]any{
											"label":       map[string]any{"type": "string"},
											"description": map[string]any{"type": "string"},
										},
										"required": []string{"label", "description"},
									},
								},
							},
							"required": []string{"id", "header", "question", "options"},
						},
					},
				},
				"required": []string{"questions"},
			},
			readOnly: true,
			fn:       b.requestUserInputPlaceholder,
		},
	}
}

func (b *Toolset) todoRuntimeTools() []core.Tool {
	return []core.Tool{
		toolFn{
			name:        "todo_add",
			description: "Add a todo item to current session checklist.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"text":     map[string]any{"type": "string"},
					"priority": map[string]any{"type": "integer", "minimum": 0, "maximum": 9},
				},
				"required": []string{"text"},
			},
			readOnly: true,
			fn:       b.sessionRuntimePlaceholder,
		},
		toolFn{
			name:        "todo_list",
			description: "List current session todo items.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"include_done": map[string]any{"type": "boolean"},
				},
			},
			readOnly: true,
			fn:       b.sessionRuntimePlaceholder,
		},
		toolFn{
			name:        "todo_update",
			description: "Update a todo item fields such as done/text/priority.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"id":       map[string]any{"type": "string"},
					"text":     map[string]any{"type": "string"},
					"done":     map[string]any{"type": "boolean"},
					"priority": map[string]any{"type": "integer", "minimum": 0, "maximum": 9},
				},
				"required": []string{"id"},
			},
			readOnly: true,
			fn:       b.sessionRuntimePlaceholder,
		},
		toolFn{
			name:        "todo_remove",
			description: "Remove a todo item by id.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"id": map[string]any{"type": "string"},
				},
				"required": []string{"id"},
			},
			readOnly: true,
			fn:       b.sessionRuntimePlaceholder,
		},
		toolFn{
			name:        "todo_clear_done",
			description: "Remove all completed todo items from current session checklist.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties":           map[string]any{},
			},
			readOnly: true,
			fn:       b.sessionRuntimePlaceholder,
		},
	}
}

func (b *Toolset) sessionRuntimePlaceholder(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	return marshalToolError(call, "tool_unavailable", "session runtime tool is handled by agent runtime"), nil
}

func (b *Toolset) requestUserInputPlaceholder(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	return marshalToolError(call, "tool_unavailable", "request_user_input is handled by agent runtime"), nil
}
