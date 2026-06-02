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
	return "Run one bounded child agent for exploration, research, or review. Child agents are resolved through an agent definition with least-privilege tools. Omit capabilities/tools for workspace.read, pass shell.read for safe read-only shell commands, shell.run or workspace.write only with an explicit non-read-only permissionMode, or [] for model-only synthesis. Set agent.background=true to launch and return a child session id immediately; use subagent_status or cancel_subagent for lifecycle follow-up."
}
func (t spawnSubagentTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"task":           map[string]any{"type": "string", "description": "Self-contained task for the child agent. The available tools are determined by the requested role or agent definition."},
			"role":           map[string]any{"type": "string", "description": "Built-in role (explore, research, review) or an agent definition name from .whale/agents."},
			"agent":          agentDefinitionSchema(),
			"model":          map[string]any{"type": "string", "description": "Optional model override. Defaults to the configured cheap model."},
			"max_tool_iters": map[string]any{"type": "integer", "minimum": 1, "maximum": 64},
			"max_tool_calls": map[string]any{"type": "integer", "minimum": 1, "maximum": 128},
			"capabilities": map[string]any{
				"type":        "array",
				"description": "Optional least-privilege tool capabilities. Omit for workspace.read. Pass [] for model-only.",
				"items":       map[string]any{"type": "string", "enum": agentCapabilityEnum()},
			},
		},
		"required": []string{"task"},
	}
}

func agentDefinitionSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          "Optional long-term child agent definition. Top-level role/model/capabilities still override or seed this definition.",
		"additionalProperties": false,
		"properties": map[string]any{
			"name":            map[string]any{"type": "string"},
			"description":     map[string]any{"type": "string"},
			"whenToUse":       map[string]any{"type": "string"},
			"prompt":          map[string]any{"type": "string", "description": "Agent-level system prompt instructions for this child agent."},
			"tools":           map[string]any{"type": "array", "description": "Tool selectors: known capabilities such as workspace.read or exact tool names exposed to the parent agent.", "items": map[string]any{"type": "string"}},
			"disallowedTools": map[string]any{"type": "array", "description": "Tool selectors to remove from this child agent: known capabilities or exact tool names.", "items": map[string]any{"type": "string"}},
			"skills":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"mcpServers":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"hooks":           agentHooksSchema(),
			"model":           map[string]any{"type": "string"},
			"effort":          map[string]any{"type": "string"},
			"permissionMode":  map[string]any{"type": "string", "enum": []string{AgentPermissionReadOnly, AgentPermissionAsk, AgentPermissionAuto, AgentPermissionTrusted}},
			"maxTurns":        map[string]any{"type": "integer", "minimum": 1, "maximum": 256},
			"initialPrompt":   map[string]any{"type": "string"},
			"memory":          map[string]any{"type": "string", "enum": []string{"user", "project", "local"}},
			"background":      map[string]any{"type": "boolean"},
			"isolation":       map[string]any{"type": "string", "enum": []string{"none", "worktree"}},
			"generation":      agentGenerationSchema(),
		},
	}
}

func agentGenerationSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          "Optional assistant generation steering for this child agent.",
		"additionalProperties": false,
		"properties": map[string]any{
			"assistantPrefix":  map[string]any{"type": "string", "description": "Assistant text prefix to continue from when prefixCompletion is enabled."},
			"prefixCompletion": map[string]any{"type": "boolean", "description": "Use provider prefix completion when supported."},
		},
	}
}

func agentHooksSchema() map[string]any {
	hookConfig := map[string]any{
		"type":                 "object",
		"additionalProperties": true,
		"properties": map[string]any{
			"type":           map[string]any{"type": "string", "enum": []string{"command", "shell", "prompt", "http", "agent"}},
			"command":        map[string]any{"type": "string"},
			"prompt":         map[string]any{"type": "string"},
			"url":            map[string]any{"type": "string"},
			"model":          map[string]any{"type": "string"},
			"match":          map[string]any{"type": "string"},
			"if":             map[string]any{"type": "string"},
			"shell":          map[string]any{"type": "string"},
			"cwd":            map[string]any{"type": "string"},
			"description":    map[string]any{"type": "string"},
			"timeout":        map[string]any{"type": "number"},
			"once":           map[string]any{"type": "boolean"},
			"async":          map[string]any{"type": "boolean"},
			"asyncRewake":    map[string]any{"type": "boolean"},
			"statusMessage":  map[string]any{"type": "string"},
			"headers":        map[string]any{"type": "object", "additionalProperties": true},
			"allowedEnvVars": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
	matcherHook := map[string]any{
		"type":                 "object",
		"additionalProperties": true,
		"properties": map[string]any{
			"matcher": map[string]any{"type": "string"},
			"hooks":   map[string]any{"type": "array", "items": hookConfig},
		},
		"required": []string{"hooks"},
	}
	return map[string]any{
		"type":                 "object",
		"description":          "Optional hooks for child agents. Supports Whale-native event arrays or Claude Code-style matcher entries for PreToolUse, PostToolUse, SubagentStart, and Stop/SubagentStop.",
		"additionalProperties": false,
		"properties": map[string]any{
			"PreToolUse":    map[string]any{"type": "array", "items": hookOrMatcherSchema(hookConfig, matcherHook)},
			"PostToolUse":   map[string]any{"type": "array", "items": hookOrMatcherSchema(hookConfig, matcherHook)},
			"SubagentStart": map[string]any{"type": "array", "items": hookOrMatcherSchema(hookConfig, matcherHook)},
			"SubagentStop":  map[string]any{"type": "array", "items": hookOrMatcherSchema(hookConfig, matcherHook)},
			"Stop":          map[string]any{"type": "array", "items": hookOrMatcherSchema(hookConfig, matcherHook)},
		},
	}
}

func hookOrMatcherSchema(hookConfig, matcherHook map[string]any) map[string]any {
	props := map[string]any{}
	for key, value := range hookConfig["properties"].(map[string]any) {
		props[key] = value
	}
	for key, value := range matcherHook["properties"].(map[string]any) {
		props[key] = value
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": true,
		"properties":           props,
	}
}

func agentCapabilityEnum() []string {
	return []string{CapabilityWorkspaceRead, CapabilityWorkspaceWrite, CapabilityShellRead, CapabilityShellRun, CapabilityWebSearch, CapabilityWebFetch, CapabilityMCPRead}
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
	if spawnSubagentSelectorsMutating(cfg.Capabilities) {
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
			"capabilities":       res.Capabilities,
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
	b, _ := json.Marshal(v)
	return string(b)
}

func errorContent(code, message string) string {
	return fmt.Sprintf(`{"ok":false,"code":%q,"message":%q}`, code, message)
}
