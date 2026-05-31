package workflow

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

type Tool struct {
	runner              *ScriptRunner
	parentSessionIDFunc func() string
}

func NewTool(runner *ScriptRunner, parentSessionIDFunc ...func() string) Tool {
	var fn func() string
	if len(parentSessionIDFunc) > 0 {
		fn = parentSessionIDFunc[0]
	}
	return Tool{runner: runner, parentSessionIDFunc: fn}
}

func (t Tool) Name() string { return "workflow" }

func (t Tool) Description() string {
	return strings.Join([]string{
		"Launch a restricted Whale workflow script asynchronously for decomposable multi-agent work such as fan-out research, repository inspection, or multi-perspective review.",
		"Use this when the user explicitly asks for a workflow, fan-out, multi-agent orchestration, or names/describes an available workflow from the system prompt catalog.",
		"When the user clearly asks to run a named workflow, launch it directly. Do not first inspect files, search the workspace, or block launch because you think an expected input might be missing unless the user asked for a preflight check.",
		"Use ordinary tools instead for a single quick read, edit, or answer.",
		"When an available named workflow fits, pass name instead of generating a new script; include args only when the user supplied useful input or the workflow contract clearly requires it. Do not ask for a missing args value merely because the args field exists. Use scriptPath for an existing file; generate script only for an explicit ad-hoc workflow with no matching named workflow.",
		"Workflow scripts are not Node scripts: export const meta must be a pure literal first statement; meta/args/budget/phase/log/agent/workflow/parallel/pipeline are runtime globals; host APIs like require/process/fetch are unavailable.",
		"Use parallel() with thunks, not promises: () => agent(...). Give every agent() a short unique label, include enough context in each prompt, use JSON Schema for structured output, and add a synthesis/verification agent when combining branches.",
		"File, shell, and network effects must happen through agent() leaves. Returns an async launch receipt; tell the user only that /workflows opens the workflow panel. Do not mention /workflows with run ids or hidden subcommands.",
	}, " ")
}

func (t Tool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"script": map[string]any{
				"type":        "string",
				"maxLength":   MaxWorkflowScriptBytes,
				"description": "Self-contained workflow script beginning with a pure literal export const meta = {...}. Use phase(), log(), agent(), workflow(), parallel(thunks), pipeline(), args, and budget; every agent should have a short label.",
			},
			"scriptPath": map[string]any{
				"type":        "string",
				"description": "Path to a workflow script on disk. Takes precedence over script.",
			},
			"args": map[string]any{
				"description": "Optional JSON-serializable args exposed to the script as read-only args. Omit this field when the user did not provide workflow input and the workflow contract does not clearly require it. May be a string, object, array, number, boolean, or null depending on the workflow contract.",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Named workflow from project or user .whale/workflows. Used only when scriptPath and script are omitted.",
			},
			"resumeFromRunId": map[string]any{
				"type":        "string",
				"description": "Optional source run id for same-session resume. Unchanged agent() calls reuse cached results; the first changed call and later calls rerun.",
			},
			"budgetTokens": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "Optional completion-token budget shared by this workflow and child workflows. agent() calls are blocked once spent completion tokens reach the cap.",
			},
		},
	}
}

func (t Tool) Run(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	if t.runner == nil {
		return workflowToolError(call, "not_configured", "workflow runner is not configured")
	}
	var input WorkflowInput
	if err := json.Unmarshal([]byte(call.Input), &input); err != nil {
		return workflowToolError(call, "invalid_input", err.Error())
	}
	out, err := t.runner.StartWorkflow(ctx, t.parentSessionID(), input)
	if err != nil {
		return workflowToolError(call, "workflow_failed", err.Error())
	}
	data := workflowOutputData(out)
	if strings.TrimSpace(out.Error) != "" {
		return workflowToolErrorWithData(call, "workflow_rejected", out.Error, data)
	}
	content, err := core.MarshalToolEnvelope(core.ToolEnvelope{
		OK:      true,
		Success: true,
		Code:    "ok",
		Summary: out.Summary,
		Data:    data,
	})
	if err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content, Metadata: workflowToolMetadata(out)}, nil
}

func (t Tool) parentSessionID() string {
	if t.parentSessionIDFunc == nil {
		return ""
	}
	return strings.TrimSpace(t.parentSessionIDFunc())
}

func workflowOutputData(out WorkflowOutput) map[string]any {
	data := map[string]any{}
	if out.Status != "" {
		data["status"] = out.Status
	}
	if out.TaskID != "" {
		data["taskId"] = out.TaskID
	}
	if out.RunID != "" {
		data["runId"] = string(out.RunID)
	}
	if out.Summary != "" {
		data["summary"] = out.Summary
	}
	if out.TranscriptDir != "" {
		data["transcriptDir"] = out.TranscriptDir
	}
	if out.ScriptPath != "" {
		data["scriptPath"] = out.ScriptPath
	}
	if out.SessionURL != "" {
		data["sessionUrl"] = out.SessionURL
	}
	if out.Warning != "" {
		data["warning"] = out.Warning
	}
	if out.Error != "" {
		data["error"] = out.Error
	}
	if out.RunID != "" {
		data["userGuidance"] = "Tell the user /workflows opens the workflow panel. Do not suggest /workflows with a run id, events, or cancel subcommands."
	}
	return data
}

func workflowToolMetadata(out WorkflowOutput) map[string]any {
	meta := map[string]any{}
	if out.RunID != "" {
		meta["workflow_run_id"] = string(out.RunID)
	}
	if out.Status != "" {
		meta["workflow_status"] = out.Status
	}
	if out.ScriptPath != "" {
		meta["workflow_script_path"] = out.ScriptPath
	}
	return meta
}

func workflowToolError(call core.ToolCall, code, msg string) (core.ToolResult, error) {
	return workflowToolErrorWithData(call, code, msg, nil)
}

func workflowToolErrorWithData(call core.ToolCall, code, msg string, data map[string]any) (core.ToolResult, error) {
	env := core.NewToolErrorEnvelope(code, msg)
	env.Data = data
	content, err := core.MarshalToolEnvelope(env)
	if err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content, IsError: true}, nil
}
