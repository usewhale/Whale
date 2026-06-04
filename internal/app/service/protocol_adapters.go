package service

import (
	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/plugins"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/runtime/protocol"
	"github.com/usewhale/whale/internal/skills"
)

func protocolLocalResult(result *app.LocalResult) *protocol.LocalResult {
	if result == nil {
		return nil
	}
	out := &protocol.LocalResult{
		Kind:                  result.Kind,
		Title:                 result.Title,
		PlainText:             result.PlainText,
		Fields:                protocolLocalResultFields(result.Fields),
		Actions:               protocolLocalResultActions(result.Actions),
		WorkflowPanelSnapshot: protocolWorkflowPanelSnapshot(result.WorkflowPanelSnapshot),
	}
	if len(result.Sections) > 0 {
		out.Sections = make([]protocol.LocalResultSection, 0, len(result.Sections))
		for _, section := range result.Sections {
			out.Sections = append(out.Sections, protocol.LocalResultSection{
				Title:  section.Title,
				Fields: protocolLocalResultFields(section.Fields),
			})
		}
	}
	return out
}

func protocolLocalResultActions(actions []app.LocalResultAction) []protocol.LocalResultAction {
	if len(actions) == 0 {
		return nil
	}
	out := make([]protocol.LocalResultAction, 0, len(actions))
	for _, action := range actions {
		out = append(out, protocol.LocalResultAction{
			Label:              action.Label,
			Description:        action.Description,
			Command:            action.Command,
			Tone:               action.Tone,
			WorkflowName:       action.WorkflowName,
			WorkflowArgs:       action.WorkflowArgs,
			WorkflowResume:     action.WorkflowResume,
			WorkflowTrust:      action.WorkflowTrust,
			WorkflowScript:     action.WorkflowScript,
			WorkflowSaveAs:     action.WorkflowSaveAs,
			WorkflowScriptPath: action.WorkflowScriptPath,
		})
	}
	return out
}

func protocolWorkflowPanelSnapshot(snapshot *app.WorkflowPanelSnapshot) *protocol.WorkflowPanelSnapshot {
	if snapshot == nil {
		return nil
	}
	out := &protocol.WorkflowPanelSnapshot{
		RunID:        snapshot.RunID,
		Status:       snapshot.Status,
		Summary:      snapshot.Summary,
		Error:        snapshot.Error,
		Budget:       snapshot.Budget,
		CurrentPhase: snapshot.CurrentPhase,
		StartedAt:    snapshot.StartedAt,
		EndedAt:      snapshot.EndedAt,
		ElapsedMS:    snapshot.ElapsedMS,
		Logs:         append([]string(nil), snapshot.Logs...),
		Result:       snapshot.Result,
	}
	if len(snapshot.Phases) > 0 {
		out.Phases = make([]protocol.WorkflowPanelPhase, 0, len(snapshot.Phases))
		for _, phase := range snapshot.Phases {
			out.Phases = append(out.Phases, protocolWorkflowPanelPhase(phase))
		}
	}
	return out
}

func protocolWorkflowPanelPhase(phase app.WorkflowPanelPhase) protocol.WorkflowPanelPhase {
	out := protocol.WorkflowPanelPhase{
		Name:      phase.Name,
		Status:    phase.Status,
		Done:      phase.Done,
		Running:   phase.Running,
		Failed:    phase.Failed,
		Cancelled: phase.Cancelled,
		Cached:    phase.Cached,
		Total:     phase.Total,
	}
	if len(phase.Tasks) > 0 {
		out.Tasks = make([]protocol.WorkflowPanelTask, 0, len(phase.Tasks))
		for _, task := range phase.Tasks {
			out.Tasks = append(out.Tasks, protocolWorkflowPanelTask(task))
		}
	}
	return out
}

func protocolWorkflowPanelTask(task app.WorkflowPanelTask) protocol.WorkflowPanelTask {
	out := protocol.WorkflowPanelTask{
		ID:               task.ID,
		Sequence:         task.Sequence,
		Phase:            task.Phase,
		Label:            task.Label,
		Status:           task.Status,
		Model:            task.Model,
		ActorKind:        task.ActorKind,
		Prompt:           task.Prompt,
		Outcome:          task.Outcome,
		Error:            task.Error,
		Message:          task.Message,
		Cached:           task.Cached,
		IsChild:          task.IsChild,
		StartedAt:        task.StartedAt,
		CompletedAt:      task.CompletedAt,
		DurationMS:       task.DurationMS,
		PromptTokens:     task.PromptTokens,
		CompletionTokens: task.CompletionTokens,
		TotalTokens:      task.TotalTokens,
		PromptCacheHit:   task.PromptCacheHit,
		PromptCacheMiss:  task.PromptCacheMiss,
		ReasoningReplay:  task.ReasoningReplay,
		ToolReplayTokens: task.ToolReplayTokens,
		ToolRawTokens:    task.ToolRawTokens,
		ToolTokensSaved:  task.ToolTokensSaved,
		ToolCompacted:    task.ToolCompacted,
		ToolCalls:        task.ToolCalls,
		ToolCallNames:    append([]string(nil), task.ToolCallNames...),
	}
	if len(task.Activity) > 0 {
		out.Activity = make([]protocol.WorkflowPanelActivity, 0, len(task.Activity))
		for _, activity := range task.Activity {
			out.Activity = append(out.Activity, protocol.WorkflowPanelActivity{
				Time:     activity.Time,
				Message:  activity.Message,
				ToolName: activity.ToolName,
			})
		}
	}
	return out
}

func protocolLocalResultFields(fields []app.LocalResultField) []protocol.LocalResultField {
	if len(fields) == 0 {
		return nil
	}
	out := make([]protocol.LocalResultField, 0, len(fields))
	for _, field := range fields {
		out = append(out, protocol.LocalResultField{
			Label: field.Label,
			Value: field.Value,
			Tone:  field.Tone,
		})
	}
	return out
}

func protocolSkills(views []skills.SkillView) []protocol.SkillView {
	if len(views) == 0 {
		return nil
	}
	out := make([]protocol.SkillView, 0, len(views))
	for _, view := range views {
		out = append(out, protocol.SkillView{
			Name:          view.Name,
			Description:   view.Description,
			When:          view.When,
			Path:          view.Path,
			SkillFilePath: view.SkillFilePath,
			Source:        view.Source,
			Status:        string(view.Status),
			Reason:        view.Reason,
			Missing:       protocolMissingRequirements(view.Missing),
		})
	}
	return out
}

func protocolMissingRequirements(missing []skills.MissingRequirement) []protocol.MissingRequirement {
	if len(missing) == 0 {
		return nil
	}
	out := make([]protocol.MissingRequirement, 0, len(missing))
	for _, item := range missing {
		out = append(out, protocol.MissingRequirement{
			Kind:   item.Kind,
			Name:   item.Name,
			Detail: item.Detail,
		})
	}
	return out
}

func protocolPlugins(statuses []plugins.PluginStatus) []protocol.PluginStatus {
	if len(statuses) == 0 {
		return nil
	}
	out := make([]protocol.PluginStatus, 0, len(statuses))
	for _, status := range statuses {
		out = append(out, protocol.PluginStatus{
			Manifest:    protocolPluginManifest(status.Manifest),
			Enabled:     status.Enabled,
			Commands:    protocolPluginCommands(status.Commands),
			Tools:       append([]string(nil), status.Tools...),
			Skills:      append([]string(nil), status.Skills...),
			Agents:      append([]string(nil), status.Agents...),
			Rules:       append([]string(nil), status.Rules...),
			Hooks:       append([]string(nil), status.Hooks...),
			Services:    protocolPluginServices(status.Services),
			Diagnostics: protocolPluginDiagnostics(status.Diagnostics),
			Paths:       cloneStringMap(status.Paths),
		})
	}
	return out
}

func protocolConfigSettings(state app.ConfigSettingsState) *protocol.ConfigManagerState {
	out := &protocol.ConfigManagerState{Items: make([]protocol.ConfigSettingView, 0, len(state.Items))}
	for _, item := range state.Items {
		out.Items = append(out.Items, protocol.ConfigSettingView{
			ID:          item.ID,
			Label:       item.Label,
			Description: item.Description,
			Type:        string(item.Type),
			Value:       item.Value,
			Default:     item.Default,
			Scope:       item.Scope,
			Source:      item.Source,
		})
	}
	return out
}

func protocolHooks(entries []agent.HookListEntry) *protocol.HooksManagerState {
	state := &protocol.HooksManagerState{}
	for _, info := range agent.HookEvents() {
		summary := protocol.HookEventSummary{Event: string(info.Event), Description: info.Description}
		for _, entry := range entries {
			if entry.Event != info.Event {
				continue
			}
			summary.Installed++
			if entry.Active {
				summary.Active++
			}
			if agent.HookNeedsReview(entry) {
				summary.Review++
				state.ReviewNeededCount++
			}
		}
		state.Events = append(state.Events, summary)
	}
	for _, entry := range entries {
		state.Entries = append(state.Entries, protocol.HookEntry{
			Key:         entry.Key,
			Event:       string(entry.Event),
			Type:        entry.Type,
			Name:        entry.Name,
			Source:      entry.Source,
			Match:       entry.Match,
			Command:     entry.Command,
			Description: entry.Description,
			TimeoutSec:  entry.TimeoutSec,
			CWD:         entry.CWD,
			Hash:        entry.Hash,
			Enabled:     entry.Enabled,
			Managed:     entry.Managed,
			Active:      entry.Active,
			Trust:       string(entry.Trust),
		})
	}
	return state
}

func protocolPluginManifest(manifest plugins.Manifest) protocol.PluginManifest {
	capabilities := make([]string, 0, len(manifest.Capabilities))
	for _, capability := range manifest.Capabilities {
		capabilities = append(capabilities, string(capability))
	}
	permissions := make([]string, 0, len(manifest.Permissions))
	for _, permission := range manifest.Permissions {
		permissions = append(permissions, string(permission))
	}
	return protocol.PluginManifest{
		ID:           manifest.ID,
		Name:         manifest.Name,
		Version:      manifest.Version,
		Description:  manifest.Description,
		Official:     manifest.Official,
		Capabilities: capabilities,
		Permissions:  permissions,
		Status:       manifest.Status,
	}
}

func protocolPluginCommands(commands []plugins.SlashCommand) []protocol.PluginCommand {
	if len(commands) == 0 {
		return nil
	}
	out := make([]protocol.PluginCommand, 0, len(commands))
	for _, command := range commands {
		out = append(out, protocol.PluginCommand{
			Name:        command.Name,
			Usage:       command.Usage,
			Description: command.Description,
			Class:       string(command.Class),
			StartsTurn:  command.StartsTurn,
		})
	}
	return out
}

func protocolPluginServices(services []plugins.ServiceStatus) []protocol.PluginService {
	if len(services) == 0 {
		return nil
	}
	out := make([]protocol.PluginService, 0, len(services))
	for _, service := range services {
		out = append(out, protocol.PluginService{
			Name:   service.Name,
			Status: service.Status,
			Detail: service.Detail,
		})
	}
	return out
}

func protocolPluginDiagnostics(diags []plugins.Diagnostic) []protocol.PluginDiagnostic {
	if len(diags) == 0 {
		return nil
	}
	out := make([]protocol.PluginDiagnostic, 0, len(diags))
	for _, diag := range diags {
		out = append(out, protocol.PluginDiagnostic{
			PluginID: diag.PluginID,
			Level:    string(diag.Level),
			Label:    diag.Label,
			Detail:   diag.Detail,
		})
	}
	return out
}

func protocolWorktreeExitSummary(summary app.WorktreeExitSummary) *protocol.WorktreeExitSummary {
	return &protocol.WorktreeExitSummary{
		Session: protocol.WorktreeSession{
			Name:               summary.Session.Name,
			Workspace:          summary.Session.Workspace,
			Path:               summary.Session.Path,
			Branch:             summary.Session.Branch,
			OriginalWorkspace:  summary.Session.OriginalWorkspace,
			OriginalBranch:     summary.Session.OriginalBranch,
			OriginalHeadCommit: summary.Session.OriginalHeadCommit,
		},
		ChangedFiles: summary.ChangedFiles,
		IgnoredFiles: summary.IgnoredFiles,
		Commits:      summary.Commits,
	}
}

func protocolMessages(messages []core.Message) []protocol.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]protocol.Message, 0, len(messages))
	for _, message := range messages {
		out = append(out, protocol.Message{
			ID:           message.ID,
			SessionID:    message.SessionID,
			Role:         string(message.Role),
			Text:         core.MessagePlainText(message),
			Hidden:       message.Hidden,
			Reasoning:    message.Reasoning,
			ToolCalls:    protocolToolCalls(message.ToolCalls),
			ToolResults:  protocolToolResults(message.ToolResults),
			FinishReason: string(message.FinishReason),
			CreatedAt:    message.CreatedAt,
			UpdatedAt:    message.UpdatedAt,
		})
	}
	return out
}

func protocolToolCalls(calls []core.ToolCall) []protocol.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]protocol.ToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, protocol.ToolCall{ID: call.ID, Name: call.Name, Input: call.Input})
	}
	return out
}

func protocolToolResults(results []core.ToolResult) []protocol.ToolResult {
	if len(results) == 0 {
		return nil
	}
	out := make([]protocol.ToolResult, 0, len(results))
	for _, result := range results {
		out = append(out, protocol.ToolResult{
			ToolCallID: result.ToolCallID,
			Name:       result.Name,
			Content:    result.Content,
			Metadata:   cloneAnyMap(result.Metadata),
			IsError:    result.IsError,
		})
	}
	return out
}

func protocolProgressSteps(steps []core.SubagentStep) []protocol.ProgressStep {
	if len(steps) == 0 {
		return nil
	}
	out := make([]protocol.ProgressStep, 0, len(steps))
	for _, step := range steps {
		out = append(out, protocol.ProgressStep{
			ToolName: step.ToolName,
			Status:   step.Status,
			Summary:  step.Summary,
		})
	}
	return out
}

func protocolUserInputQuestions(questions []core.UserInputQuestion) []protocol.UserInputQuestion {
	if len(questions) == 0 {
		return nil
	}
	out := make([]protocol.UserInputQuestion, 0, len(questions))
	for _, question := range questions {
		out = append(out, protocol.UserInputQuestion{
			Header:   question.Header,
			ID:       question.ID,
			Question: question.Question,
			Options:  protocolUserInputOptions(question.Options),
		})
	}
	return out
}

func protocolUserInputOptions(options []core.UserInputOption) []protocol.UserInputOption {
	if len(options) == 0 {
		return nil
	}
	out := make([]protocol.UserInputOption, 0, len(options))
	for _, option := range options {
		out = append(out, protocol.UserInputOption{
			Label:       option.Label,
			Description: option.Description,
		})
	}
	return out
}

func protocolApprovalRequest(req policy.ApprovalRequest, keys []string, metadata map[string]any) *protocol.ApprovalRequest {
	return &protocol.ApprovalRequest{
		SessionID: req.SessionID,
		ToolCall:  protocol.ToolCall{ID: req.ToolCall.ID, Name: req.ToolCall.Name, Input: req.ToolCall.Input},
		Spec: protocol.ToolSpec{
			Name:             req.Spec.Name,
			Description:      req.Spec.Description,
			Parameters:       req.Spec.Parameters,
			ReadOnly:         req.Spec.ReadOnly,
			Capabilities:     append([]string(nil), req.Spec.Capabilities...),
			ApprovalHint:     req.Spec.ApprovalHint,
			SupportsParallel: req.Spec.SupportsParallel,
		},
		Reason:   req.Reason,
		Code:     req.Code,
		Key:      req.Key,
		Keys:     append([]string(nil), keys...),
		Metadata: cloneAnyMap(metadata),
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
