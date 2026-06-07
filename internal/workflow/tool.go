package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

type Tool struct {
	runner              *ScriptRunner
	library             *Library
	parentSessionIDFunc func() string
	enabled             bool
}

func NewTool(runner *ScriptRunner, parentSessionIDFunc ...func() string) Tool {
	var fn func() string
	if len(parentSessionIDFunc) > 0 {
		fn = parentSessionIDFunc[0]
	}
	return Tool{runner: runner, library: workflowLibraryFromRunner(runner), parentSessionIDFunc: fn, enabled: true}
}

type ToolOptions struct {
	ParentSessionIDFunc   func() string
	KeywordTriggerEnabled bool
	Enabled               bool
	Library               *Library
}

func NewToolWithOptions(runner *ScriptRunner, opts ToolOptions) Tool {
	library := opts.Library
	if library == nil {
		library = workflowLibraryFromRunner(runner)
	}
	return Tool{runner: runner, library: library, parentSessionIDFunc: opts.ParentSessionIDFunc, enabled: opts.Enabled}
}

func workflowLibraryFromRunner(runner *ScriptRunner) *Library {
	if runner == nil {
		return nil
	}
	return runner.Library
}

const workflowDisabledUserMessage = "Dynamic workflows are disabled in Whale. Enable them in /config before using workflows."

func (t Tool) Name() string { return "workflow" }

func (t Tool) Description() string {
	return strings.Join([]string{
		"Official Whale workflow resolver and launcher.",
		"Use status only when the user explicitly asks whether dynamic workflows are enabled; use list or resolve for discovery.",
		"Do not inspect .whale/workflows, search files, list directories, or run shell commands to discover workflow names.",
		"Use run, or omit action, when the user clearly asks to launch a named workflow; do not preflight with status first.",
		"Use script plus saveAs only when the user clearly asks to create a new workflow.",
		"If workflows are disabled, this tool returns workflow_disabled; for that tool result only, report that state and stop. On a later user request to list or run workflows, call this tool again because /config may have changed. Use the product name Whale, never Whisper. Do not ask what to do next, present choices, read workflow directories, edit configuration, retry within the same turn, offer shell/manual substitutes for the named workflow, or say you can help after the user enables workflows.",
	}, " ")
}

func (t Tool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"status", "list", "resolve", "run", "create"},
				"description": "Optional workflow action. Use status, list, or resolve for discovery; run launches; create uses script+saveAs. Omit for backward-compatible run.",
			},
			"script": map[string]any{
				"type":        "string",
				"maxLength":   MaxWorkflowScriptBytes,
				"description": "Self-contained workflow script used only with saveAs when creating a workflow.",
			},
			"saveAs": map[string]any{
				"type":        "string",
				"description": "Optional kebab-case workflow name used only with script when creating a workflow.",
			},
			"scriptPath": map[string]any{
				"type":        "string",
				"description": "Path to an existing workflow script. Takes precedence over script.",
			},
			"args": map[string]any{
				"description": "Optional JSON-serializable args. Omit this field when the user did not provide workflow input.",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Named workflow used with resolve or run.",
			},
			"resumeFromRunId": map[string]any{
				"type":        "string",
				"description": "Optional source run id for same-session workflow resume.",
			},
			"budgetTokens": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "Optional completion-token budget for the workflow.",
			},
		},
	}
}

func (t Tool) Run(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	var input WorkflowInput
	if err := json.Unmarshal([]byte(call.Input), &input); err != nil {
		return workflowToolError(call, "invalid_input", err.Error())
	}
	action := workflowToolAction(input.Action)
	switch action {
	case "status":
		return t.statusWorkflows(call)
	case "list":
		if !t.enabled {
			return t.workflowDisabled(call)
		}
		return t.listWorkflows(ctx, call)
	case "resolve":
		if !t.enabled {
			return t.workflowDisabled(call)
		}
		return t.resolveWorkflow(ctx, call, input)
	case "run", "create":
	default:
		return workflowToolError(call, "invalid_input", "workflow action must be one of status, list, resolve, run, or create")
	}
	if !t.enabled {
		return t.workflowDisabled(call)
	}
	if t.runner == nil {
		return workflowToolError(call, "not_configured", "workflow runner is not configured")
	}
	if action == "create" {
		if strings.TrimSpace(input.Script) == "" || strings.TrimSpace(input.SaveAs) == "" {
			return workflowToolError(call, "invalid_input", "workflow create requires script and saveAs")
		}
		if strings.TrimSpace(input.Name) != "" || strings.TrimSpace(input.ScriptPath) != "" {
			return workflowToolError(call, "invalid_input", "workflow create cannot be combined with name or scriptPath")
		}
	}
	if strings.TrimSpace(input.SaveAs) != "" {
		prepared, err := t.prepareGenerated(ctx, input)
		if err != nil {
			return workflowToolError(call, "workflow_save_failed", err.Error())
		}
		data := workflowConfirmationData(prepared, workflowToolArgsActionString(input.Args), input.ResumeFromRunID)
		data["workflowScript"] = prepared.Script
		data["workflowSaveAs"] = prepared.Definition.Name
		content, err := core.MarshalToolEnvelope(core.ToolEnvelope{
			OK:      true,
			Success: true,
			Code:    "workflow_confirmation_required",
			Summary: fmt.Sprintf("Workflow %q requires user confirmation before save and launch.", prepared.Definition.Name),
			Data:    data,
		})
		if err != nil {
			return core.ToolResult{}, err
		}
		return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content, Metadata: workflowConfirmationMetadata(data)}, nil
	}
	if strings.TrimSpace(input.Script) != "" {
		if err := validateWorkflowScriptForConfirmation(input.Script); err != nil {
			return workflowToolError(call, "workflow_save_failed", err.Error())
		}
		return workflowToolError(call, "workflow_confirmation_required", "workflow scripts must be saved as a named workflow before launch confirmation")
	}
	if strings.TrimSpace(input.ScriptPath) != "" {
		if strings.TrimSpace(input.Name) != "" || strings.TrimSpace(input.Script) != "" {
			return workflowToolError(call, "invalid_input", "scriptPath cannot be combined with name or script")
		}
		resolved, err := ResolveScriptPath(ctx, input.ScriptPath)
		if err != nil {
			return workflowToolError(call, "workflow_failed", err.Error())
		}
		data := workflowConfirmationData(resolved, workflowToolArgsActionString(input.Args), input.ResumeFromRunID)
		data["workflowScriptPath"] = resolved.Definition.Path
		content, err := core.MarshalToolEnvelope(core.ToolEnvelope{
			OK:      true,
			Success: true,
			Code:    "workflow_confirmation_required",
			Summary: fmt.Sprintf("Workflow %q requires user confirmation before launch.", resolved.Definition.Name),
			Data:    data,
		})
		if err != nil {
			return core.ToolResult{}, err
		}
		return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content, Metadata: workflowConfirmationMetadata(data)}, nil
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return workflowToolError(call, "invalid_input", "workflow name is required")
	}
	resolved, err := t.resolveNamedWorkflow(ctx, name)
	if err != nil {
		return workflowToolError(call, "workflow_failed", err.Error())
	}
	data := workflowConfirmationData(resolved, workflowToolArgsActionString(input.Args), input.ResumeFromRunID)
	content, err := core.MarshalToolEnvelope(core.ToolEnvelope{
		OK:      true,
		Success: true,
		Code:    "workflow_confirmation_required",
		Summary: fmt.Sprintf("Workflow %q requires user confirmation before launch.", resolved.Definition.Name),
		Data:    data,
	})
	if err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content, Metadata: workflowConfirmationMetadata(data)}, nil
}

func (t Tool) prepareGenerated(ctx context.Context, input WorkflowInput) (ResolvedScript, error) {
	if t.library == nil {
		return ResolvedScript{}, errors.New("workflow library is not configured")
	}
	if strings.TrimSpace(input.Script) == "" {
		return ResolvedScript{}, errors.New("saveAs requires script")
	}
	if strings.TrimSpace(input.Name) != "" || strings.TrimSpace(input.ScriptPath) != "" {
		return ResolvedScript{}, errors.New("saveAs cannot be combined with name or scriptPath")
	}
	return t.library.PrepareGenerated(ctx, input.Script, input.SaveAs)
}

func validateWorkflowScriptForConfirmation(script string) error {
	parsed, err := parseWorkflowScript(script)
	if err != nil {
		return err
	}
	if err := validateWorkflowCompile(parsed.Executable); err != nil {
		return err
	}
	return validateGeneratedWorkflowScript(parsed.Executable)
}

func (t Tool) resolveNamedWorkflow(ctx context.Context, name string) (ResolvedScript, error) {
	if t.library == nil {
		return ResolvedScript{}, errors.New("workflow library is not configured")
	}
	return t.library.Resolve(ctx, name)
}

func workflowToolAction(action string) string {
	action = strings.TrimSpace(strings.ToLower(action))
	if action == "" {
		return "run"
	}
	return action
}

func (t Tool) statusWorkflows(call core.ToolCall) (core.ToolResult, error) {
	data := t.workflowStatusData()
	code := "workflow_status"
	summary := "Dynamic workflows are enabled."
	if !t.enabled {
		code = "workflow_disabled"
		summary = workflowDisabledUserMessage
	}
	content, err := core.MarshalToolEnvelope(core.ToolEnvelope{
		OK:      true,
		Success: true,
		Code:    code,
		Summary: summary,
		Data:    data,
	})
	if err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content}, nil
}

func (t Tool) workflowDisabled(call core.ToolCall) (core.ToolResult, error) {
	res, err := workflowToolErrorWithData(call, "workflow_disabled", workflowDisabledUserMessage, t.workflowStatusData())
	if err != nil {
		return res, err
	}
	res.Metadata = map[string]any{"abort_turn_after_tool_result": true}
	return res, nil
}

func (t Tool) workflowStatusData() map[string]any {
	data := map[string]any{
		"enabled":          t.enabled,
		"enableHint":       "Tell the user to enable Dynamic workflows in Whale /config, then stop.",
		"canList":          t.enabled,
		"canResolve":       t.enabled,
		"canRun":           t.enabled,
		"canCreate":        t.enabled,
		"autoEnable":       false,
		"fallbackAllowed":  false,
		"disabledAction":   "When disabled, report workflow_disabled and stop for this tool result. On a later user request to list or run workflows, call workflow again because /config may have changed. Do not ask what to do next, present choices, read or edit Whale configuration, retry within the same turn, offer shell/manual substitutes, or say you can help after workflows are enabled.",
		"brandName":        "Whale",
		"forbiddenBrand":   "Whisper",
		"modelGuidance":    "Use the product name Whale. Do not say Whisper. This disabled result applies only to the current tool result. On a later user request to list or run workflows, call workflow again because /config may have changed. Do not ask what to do next, present choices, inspect workflow directories, read configuration, edit configuration, retry within the same turn, auto-enable workflows, or offer shell/manual substitutes.",
		"responseContract": "Reply only with: Dynamic workflows are disabled in Whale. Enable them in /config before using workflows.",
		"directoryProbing": "Do not inspect .whale/workflows with file or shell tools for workflow discovery.",
	}
	if t.enabled {
		data["roots"] = workflowRootData(t.library)
	} else {
		data["workflowDirectoriesHidden"] = true
	}
	return data
}

func (t Tool) listWorkflows(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	if t.library == nil {
		return workflowToolError(call, "not_configured", "workflow library is not configured")
	}
	defs, err := t.library.List(ctx)
	if err != nil {
		return workflowToolError(call, "workflow_list_failed", err.Error())
	}
	data := workflowDiscoveryData(t.library, defs)
	content, err := core.MarshalToolEnvelope(core.ToolEnvelope{
		OK:      true,
		Success: true,
		Code:    "workflow_list",
		Summary: workflowListSummary(defs),
		Data:    data,
	})
	if err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content}, nil
}

func (t Tool) resolveWorkflow(ctx context.Context, call core.ToolCall, input WorkflowInput) (core.ToolResult, error) {
	if t.library == nil {
		return workflowToolError(call, "not_configured", "workflow library is not configured")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return workflowToolError(call, "invalid_input", "workflow name is required for action=resolve")
	}
	defs, err := t.library.List(ctx)
	if err != nil {
		return workflowToolError(call, "workflow_resolve_failed", err.Error())
	}
	data := workflowDiscoveryData(t.library, defs)
	data["query"] = name
	for _, def := range defs {
		if def.Name != name {
			continue
		}
		if def.Status != DefinitionReady {
			data["workflow"] = workflowDefinitionData(def)
			return workflowToolErrorWithData(call, "workflow_problem", def.Error, data)
		}
		data["workflow"] = workflowDefinitionData(def)
		content, err := core.MarshalToolEnvelope(core.ToolEnvelope{
			OK:      true,
			Success: true,
			Code:    "workflow_resolved",
			Summary: fmt.Sprintf("Workflow %q resolved from %s.", def.Name, workflowNonEmpty(def.Source, "workflow library")),
			Data:    data,
		})
		if err != nil {
			return core.ToolResult{}, err
		}
		return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content}, nil
	}
	return workflowToolErrorWithData(call, "workflow_not_found", workflowNotFoundMessage(name, defs), data)
}

func (t Tool) parentSessionID() string {
	if t.parentSessionIDFunc == nil {
		return ""
	}
	return strings.TrimSpace(t.parentSessionIDFunc())
}

func workflowDiscoveryData(library *Library, defs []Definition) map[string]any {
	ready := make([]map[string]any, 0, len(defs))
	problems := make([]map[string]any, 0)
	available := make([]string, 0, len(defs))
	for _, def := range defs {
		item := workflowDefinitionData(def)
		if def.Status == DefinitionReady {
			ready = append(ready, item)
			if strings.TrimSpace(def.Name) != "" {
				available = append(available, def.Name)
			}
			continue
		}
		problems = append(problems, item)
	}
	return map[string]any{
		"workflows": ready,
		"problems":  problems,
		"available": available,
		"roots":     workflowRootData(library),
		"count":     len(ready),
	}
}

func workflowDefinitionData(def Definition) map[string]any {
	item := map[string]any{
		"name":   def.Name,
		"source": def.Source,
		"status": string(def.Status),
	}
	if desc := strings.TrimSpace(def.Description); desc != "" {
		item["description"] = desc
	}
	if when := strings.TrimSpace(def.WhenToUse); when != "" {
		item["whenToUse"] = when
	}
	if path := strings.TrimSpace(def.Path); path != "" {
		item["path"] = path
	}
	if root := strings.TrimSpace(def.Root); root != "" {
		item["root"] = root
	}
	if len(def.Phases) > 0 {
		phases := make([]map[string]any, 0, len(def.Phases))
		for _, phase := range def.Phases {
			p := map[string]any{"title": strings.TrimSpace(phase.Title)}
			if detail := strings.TrimSpace(phase.Detail); detail != "" {
				p["detail"] = detail
			}
			phases = append(phases, p)
		}
		item["phases"] = phases
	}
	if def.EstimatedAgents > 0 {
		item["estimatedAgents"] = def.EstimatedAgents
	}
	if def.DefaultBudgetTokens > 0 {
		item["defaultBudgetTokens"] = def.DefaultBudgetTokens
	}
	if err := strings.TrimSpace(def.Error); err != "" {
		item["error"] = err
	}
	return item
}

func workflowRootData(library *Library) []map[string]any {
	if library == nil {
		return nil
	}
	roots := make([]map[string]any, 0, len(library.Roots))
	for _, root := range library.Roots {
		item := map[string]any{
			"source": root.Source,
			"path":   root.Path,
			"rank":   root.Rank,
		}
		exists, status := workflowRootStatus(root.Path)
		item["exists"] = exists
		item["status"] = status
		roots = append(roots, item)
	}
	return roots
}

func workflowRootStatus(path string) (bool, string) {
	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			return true, "present"
		}
		return true, "not_directory"
	}
	if os.IsNotExist(err) {
		return false, "missing"
	}
	return false, "error: " + err.Error()
}

func workflowListSummary(defs []Definition) string {
	ready, problems := 0, 0
	for _, def := range defs {
		if def.Status == DefinitionReady {
			ready++
		} else {
			problems++
		}
	}
	if problems > 0 {
		return fmt.Sprintf("%d workflow(s) available; %d workflow definition(s) have problems.", ready, problems)
	}
	return fmt.Sprintf("%d workflow(s) available.", ready)
}

func workflowNotFoundMessage(name string, defs []Definition) string {
	available := make([]string, 0, len(defs))
	for _, def := range defs {
		if def.Status == DefinitionReady && strings.TrimSpace(def.Name) != "" {
			available = append(available, def.Name)
		}
	}
	if len(available) == 0 {
		return fmt.Sprintf("workflow not found: %s; no workflows are currently available", name)
	}
	return fmt.Sprintf("workflow not found: %s; available workflows: %s", name, strings.Join(available, ", "))
}

func workflowNonEmpty(v, fallback string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return fallback
}

func workflowConfirmationData(resolved ResolvedScript, args, resume string) map[string]any {
	data := map[string]any{
		"confirmationRequired": true,
		"workflowName":         resolved.Definition.Name,
		"workflowArgs":         args,
		"userGuidance":         "Tell the user a workflow confirmation has been shown. Do not say the workflow has started until the user confirms it.",
	}
	if description := strings.TrimSpace(resolved.Definition.Description); description != "" {
		data["description"] = description
	}
	if path := strings.TrimSpace(resolved.Definition.Path); path != "" {
		data["scriptPath"] = path
	}
	if resume = strings.TrimSpace(resume); resume != "" {
		data["workflowResume"] = resume
	}
	return data
}

func workflowToolArgsActionString(args any) string {
	switch v := args.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func workflowConfirmationMetadata(data map[string]any) map[string]any {
	meta := map[string]any{
		"workflow_confirmation_required": true,
		"abort_turn_after_tool_result":   true,
	}
	for _, key := range []string{"workflowName", "workflowArgs", "workflowResume", "scriptPath", "workflowSaveAs", "workflowScriptPath"} {
		if v, ok := data[key]; ok {
			meta[key] = v
		}
	}
	return meta
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
