package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/usewhale/whale/internal/core"
)

func NewTools(r *Runner) []core.Tool {
	return []core.Tool{
		parallelReasonTool{runner: r},
		spawnSubagentTool{runner: r},
	}
}

type parallelReasonTool struct {
	runner *Runner
}

func (t parallelReasonTool) Name() string { return "parallel_reason" }
func (t parallelReasonTool) Description() string {
	return "Run 1-8 independent cheap model-only subqueries in parallel. Use for comparison, classification, critique, or brainstorming when no tools, files, shell, web access, or agent loop are needed. Returns ordered results."
}
func (t parallelReasonTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"prompts": map[string]any{
				"type":        "array",
				"minItems":    1,
				"maxItems":    MaxParallelPrompts,
				"description": "Independent subqueries to answer in parallel.",
				"items":       map[string]any{"type": "string"},
			},
			"model":      map[string]any{"type": "string", "description": "Optional model override. Defaults to the configured cheap model."},
			"max_tokens": map[string]any{"type": "integer", "minimum": 1, "maximum": 4096},
		},
		"required": []string{"prompts"},
	}
}
func (t parallelReasonTool) ReadOnly() bool         { return true }
func (t parallelReasonTool) SupportsParallel() bool { return true }
func (t parallelReasonTool) Run(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	if t.runner == nil {
		return marshalError(call, "not_configured", "task runner is not configured")
	}
	req, err := decodeInput[ParallelReasonRequest](call)
	if err != nil {
		return marshalError(call, "invalid_input", err.Error())
	}
	res, err := t.runner.ParallelReason(ctx, req)
	if err != nil {
		return marshalError(call, "parallel_reason_failed", err.Error())
	}
	return marshalSuccess(call, map[string]any{
		"model":   res.Model,
		"results": res.Results,
		"usage":   res.Usage,
	})
}

type spawnSubagentTool struct {
	runner *Runner
}

func (t spawnSubagentTool) Name() string { return "spawn_subagent" }
func (t spawnSubagentTool) Description() string {
	return "Run one bounded child agent for exploration, research, review, or a plugin-provided role. Omit capabilities to use the role default, pass explicit capabilities for least privilege, or [] for model-only synthesis. This is inline, not a background worker; progress is streamed and the parent receives the final summary."
}
func (t spawnSubagentTool) Parameters() map[string]any {
	roles := []string{"explore", "research", "review"}
	if t.runner != nil && t.runner.agentRegistry != nil {
		roles = t.runner.agentRegistry.RoleNames()
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"task":           map[string]any{"type": "string", "description": "Self-contained read-only task for the child agent."},
			"role":           map[string]any{"type": "string", "enum": roles},
			"model":          map[string]any{"type": "string", "description": "Optional model override. Defaults to the configured cheap model."},
			"max_tool_iters": map[string]any{"type": "integer", "minimum": 1, "maximum": 64},
			"max_tool_calls": map[string]any{"type": "integer", "minimum": 1, "maximum": 128},
			"capabilities": map[string]any{
				"type":        "array",
				"description": "Optional least-privilege tool capabilities. Omit for workspace.read. Pass [] for model-only.",
				"items":       map[string]any{"type": "string", "enum": KnownCapabilityNames()},
			},
		},
		"required": []string{"task"},
	}
}
func (t spawnSubagentTool) ReadOnly() bool { return true }
func (t spawnSubagentTool) Run(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	return t.RunWithProgress(ctx, call, nil)
}

func (t spawnSubagentTool) RunWithProgress(ctx context.Context, call core.ToolCall, progress func(core.ToolProgress)) (core.ToolResult, error) {
	if t.runner == nil {
		return marshalError(call, "not_configured", "task runner is not configured")
	}
	req, err := decodeInput[SpawnSubagentRequest](call)
	if err != nil {
		return marshalError(call, "invalid_input", err.Error())
	}
	req.ParentToolCallID = call.ID
	res, err := t.runner.SpawnSubagentWithProgress(ctx, req, func(p core.ToolProgress) {
		if progress == nil {
			return
		}
		p.ToolCallID = call.ID
		p.ToolName = call.Name
		progress(p)
	})
	if err != nil {
		var subErr *SpawnSubagentError
		if errors.As(err, &subErr) {
			code := core.FirstNonEmpty(subErr.Code, "spawn_subagent_failed")
			return marshalErrorWithData(call, code, subErr.Error(), map[string]any{
				"session_id":         subErr.SessionID,
				"child_session_id":   subErr.SessionID,
				"permission_profile": "read_only",
				"status":             code,
			})
		}
		return marshalError(call, "spawn_subagent_failed", err.Error())
	}
	content, err := core.MarshalToolEnvelope(core.ToolEnvelope{
		OK:      true,
		Success: true,
		Code:    "ok",
		Data: map[string]any{
			"session_id":         res.SessionID,
			"child_session_id":   res.SessionID,
			"role":               res.Role,
			"model":              res.Model,
			"permission_profile": res.PermissionProfile,
			"status":             res.Status,
			"summary":            res.Summary,
			"truncated":          res.Truncated,
			"tool_calls":         res.ToolCalls,
			"capabilities":       req.Capabilities,
			"duration_ms":        res.DurationMS,
			"completed_at":       res.CompletedAt,
		},
		Metadata: map[string]any{"duration_ms": res.DurationMS},
	})
	if err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content}, nil
}

func encodeInput(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func errorContent(code, message string) string {
	return fmt.Sprintf(`{"ok":false,"code":%q,"message":%q}`, code, message)
}
