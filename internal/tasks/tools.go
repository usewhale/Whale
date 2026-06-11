package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

func NewTools(r *Runner) []core.Tool {
	return []core.Tool{
		parallelReasonTool{runner: r},
		spawnSubagentTool{runner: r},
		subagentStatusTool{runner: r},
		cancelSubagentTool{runner: r},
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
	return "Run one bounded child agent for independent exploration, research, or review. Prefer direct tools for small follow-ups; use a child agent mainly for parallel fan-out or when a task needs roughly 10+ read/search steps whose trail does not need to stay in the parent context. Each fresh child has its own provider request/prefix and may pay a prefix-cache miss plus a full child loop. Select a built-in role or named agent definition; advanced agent definitions are configured outside this tool schema. Omit tools to use the selected agent defaults, pass [] for model-only synthesis, or pass workspace.read or exact tool names for a custom allowlist. Use subagent_status or cancel_subagent for background lifecycle follow-up."
}
func (t spawnSubagentTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"task":           map[string]any{"type": "string", "description": "Self-contained task for the child agent. The available tools are determined by the requested role, named agent definition, or explicit tools allowlist."},
			"role":           map[string]any{"type": "string", "description": "Built-in role (explore, research, review) or an agent definition name from .whale/agents."},
			"model":          map[string]any{"type": "string", "description": "Optional model override. Defaults to the configured cheap model."},
			"max_tool_iters": map[string]any{"type": "integer", "minimum": 1, "maximum": 64},
			"max_tool_calls": map[string]any{"type": "integer", "minimum": 1, "maximum": 128},
			"tools": map[string]any{
				"type":        "array",
				"description": "Optional least-privilege tool selectors. Omit to use the selected agent defaults. Pass [] for model-only. Selectors may be known aliases such as workspace.read or exact tool names exposed to the parent agent.",
				"items":       map[string]any{"type": "string"},
			},
		},
		"required": []string{"task"},
	}
}

func (t spawnSubagentTool) ReadOnly() bool { return true }
func (t spawnSubagentTool) ReadOnlyCheck(args map[string]any) bool {
	return spawnSubagentLaunchReadOnly(args, t.runner)
}
func (t spawnSubagentTool) Run(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	return t.RunWithProgress(ctx, call, nil)
}

func spawnSubagentLaunchReadOnly(args map[string]any, runner *Runner) bool {
	if args == nil {
		return false
	}
	if _, ok := args["capabilities"]; ok {
		return false
	}
	if _, ok := args["agent"]; ok {
		return false
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return false
	}
	var req SpawnSubagentRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return false
	}
	if strings.TrimSpace(req.Task) == "" {
		return false
	}
	var library *AgentDefinitionLibrary
	if runner != nil {
		library = runner.agentDefinitions
	}
	cfg, err := ResolveAgentRuntimeConfigWithLibrary(req, RunnerDefaults{}, library)
	if err != nil {
		return false
	}
	if spawnSubagentSelectorsMutating(cfg.ToolSelectors) {
		return false
	}
	if cfg.PermissionProfile != AgentPermissionReadOnly {
		return false
	}
	if cfg.Isolation == AgentIsolationWorktree {
		return false
	}
	if len(cfg.Hooks) > 0 {
		return false
	}
	return true
}

func spawnSubagentSelectorsMutating(values []string) bool {
	for _, value := range values {
		switch strings.TrimSpace(value) {
		case CapabilityWorkspaceWrite, CapabilityShellRun:
			return true
		}
	}
	return false
}

func (t spawnSubagentTool) RunWithProgress(ctx context.Context, call core.ToolCall, progress func(core.ToolProgress)) (core.ToolResult, error) {
	if t.runner == nil {
		return marshalError(call, "not_configured", "task runner is not configured")
	}
	if spawnSubagentInputHasDeprecatedCapabilities(call.Input) {
		return marshalError(call, "invalid_input", "spawn_subagent no longer accepts capabilities; use tools instead")
	}
	if spawnSubagentInputHasInlineAgent(call.Input) {
		return marshalError(call, "invalid_input", "spawn_subagent no longer accepts inline agent definitions; use a named role or .whale/agents definition instead")
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
		permissionProfile := AgentPermissionReadOnly
		if cfg, cfgErr := ResolveAgentRuntimeConfig(req, RunnerDefaults{}); cfgErr == nil && cfg.PermissionProfile != "" {
			permissionProfile = cfg.PermissionProfile
		}
		var subErr *SpawnSubagentError
		if errors.As(err, &subErr) {
			code := core.FirstNonEmpty(subErr.Code, "spawn_subagent_failed")
			return marshalErrorWithData(call, code, subErr.Error(), map[string]any{
				"session_id":         subErr.SessionID,
				"child_session_id":   subErr.SessionID,
				"permission_profile": permissionProfile,
				"status":             code,
			})
		}
		return marshalError(call, "spawn_subagent_failed", err.Error())
	}
	data := map[string]any{
		"session_id":         res.SessionID,
		"child_session_id":   res.SessionID,
		"role":               res.Role,
		"model":              res.Model,
		"permission_profile": res.PermissionProfile,
		"status":             res.Status,
		"summary":            res.Summary,
		"truncated":          res.Truncated,
		"tool_calls":         res.ToolCalls,
		"requested_tools":    res.RequestedTools,
		"resolved_tools":     res.ResolvedTools,
		"tool_mode":          res.ToolMode,
		"duration_ms":        res.DurationMS,
		"completed_at":       res.CompletedAt,
	}
	if res.SubagentBudget.SpawnCount > 0 {
		budget := map[string]any{
			"spawn_count":  res.SubagentBudget.SpawnCount,
			"total_tokens": res.SubagentBudget.TotalTokens,
		}
		if strings.TrimSpace(res.SubagentBudget.Hint) != "" {
			budget["hint"] = res.SubagentBudget.Hint
		}
		data["subagent_budget"] = budget
	}
	content, err := core.MarshalToolEnvelope(core.ToolEnvelope{
		OK:       true,
		Success:  true,
		Code:     "ok",
		Data:     data,
		Metadata: map[string]any{"duration_ms": res.DurationMS},
	})
	if err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: content}, nil
}

func spawnSubagentInputHasDeprecatedCapabilities(input string) bool {
	var raw map[string]any
	if err := json.Unmarshal([]byte(input), &raw); err != nil {
		return false
	}
	_, ok := raw["capabilities"]
	return ok
}

func spawnSubagentInputHasInlineAgent(input string) bool {
	var raw map[string]any
	if err := json.Unmarshal([]byte(input), &raw); err != nil {
		return false
	}
	_, ok := raw["agent"]
	return ok
}

type subagentStatusTool struct {
	runner *Runner
}

type subagentStatusRequest struct {
	SessionID string `json:"session_id"`
}

func (t subagentStatusTool) Name() string { return "subagent_status" }
func (t subagentStatusTool) Description() string {
	return "Read a child subagent lifecycle record by session_id. Use after launching a background child agent to observe running/completed/failed/cancelled state and recover its result summary."
}
func (t subagentStatusTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
		},
		"required": []string{"session_id"},
	}
}
func (t subagentStatusTool) ReadOnly() bool { return true }
func (t subagentStatusTool) Run(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	if t.runner == nil {
		return marshalError(call, "not_configured", "task runner is not configured")
	}
	req, err := decodeInput[subagentStatusRequest](call)
	if err != nil {
		return marshalError(call, "invalid_input", err.Error())
	}
	meta, err := t.runner.SubagentStatus(req.SessionID)
	if err != nil {
		return marshalError(call, "subagent_status_failed", err.Error())
	}
	return marshalSuccess(call, map[string]any{
		"session_id":           req.SessionID,
		"kind":                 meta.Kind,
		"parent_session_id":    meta.ParentSessionID,
		"role":                 meta.Role,
		"model":                meta.Model,
		"task":                 meta.Task,
		"status":               meta.Status,
		"summary":              meta.Summary,
		"error":                meta.Error,
		"workspace":            meta.Workspace,
		"worktree_path":        meta.WorktreePath,
		"started_at":           meta.StartedAt,
		"completed_at":         meta.CompletedAt,
		"original_workspace":   meta.OriginalWorkspace,
		"original_branch":      meta.OriginalBranch,
		"original_head_commit": meta.OriginalHeadCommit,
	})
}

type cancelSubagentTool struct {
	runner *Runner
}

func (t cancelSubagentTool) Name() string { return "cancel_subagent" }
func (t cancelSubagentTool) Description() string {
	return "Cancel a running background child subagent by session_id. Returns the latest lifecycle metadata; completed or unknown subagents are not cancelled."
}
func (t cancelSubagentTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
		},
		"required": []string{"session_id"},
	}
}
func (t cancelSubagentTool) ReadOnly() bool { return false }
func (t cancelSubagentTool) Capabilities() []string {
	return []string{"mutates_state"}
}
func (t cancelSubagentTool) Run(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	if t.runner == nil {
		return marshalError(call, "not_configured", "task runner is not configured")
	}
	req, err := decodeInput[subagentStatusRequest](call)
	if err != nil {
		return marshalError(call, "invalid_input", err.Error())
	}
	meta, cancelled, err := t.runner.CancelBackgroundSubagent(req.SessionID)
	if err != nil {
		return marshalError(call, "cancel_subagent_failed", err.Error())
	}
	return marshalSuccess(call, map[string]any{
		"session_id":   req.SessionID,
		"cancelled":    cancelled,
		"status":       meta.Status,
		"summary":      meta.Summary,
		"error":        meta.Error,
		"completed_at": meta.CompletedAt,
	})
}

func encodeInput(v any) string {
	b, _ := core.MarshalToolJSON(v)
	return string(b)
}

func errorContent(code, message string) string {
	return fmt.Sprintf(`{"ok":false,"code":%q,"message":%q}`, code, message)
}
