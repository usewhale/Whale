package tui

import (
	"strconv"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/runtime/protocol"
	"github.com/usewhale/whale/internal/runtime/timeline"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func (m *model) ensureTimeline() *timeline.TurnTimelineBuilder {
	if m.timeline == nil {
		m.timeline = timeline.NewTurnTimelineBuilder()
	}
	return m.timeline
}

func (m *model) resetTimeline() {
	if m.timeline != nil {
		m.timeline.Reset()
	}
}

func (m model) timelineSnapshotMessages() []tuirender.UIMessage {
	if m.timeline == nil {
		return nil
	}
	return m.renderTimelineMessages(m.timeline.Snapshot())
}

func (m model) hasPendingLifecycleItems() bool {
	if m.timeline == nil {
		return false
	}
	return m.timeline.Snapshot().HasPendingItems()
}

func (m model) renderTimelineMessages(snapshot timeline.Snapshot) []tuirender.UIMessage {
	out := make([]tuirender.UIMessage, 0, len(snapshot.Items))
	for _, item := range snapshot.Items {
		if msg := m.timelineApprovalNotice(item); msg != nil {
			out = append(out, *msg)
		}
		if msg := m.timelineLifecycleMessage(item); msg != nil {
			out = append(out, *msg)
		}
	}
	return out
}

func (m model) timelineApprovalNotice(item timeline.Item) *tuirender.UIMessage {
	decision := strings.TrimSpace(item.Decision)
	if decision == "" {
		for _, ev := range item.Events {
			if ev.Kind == protocol.EventApprovalDecision {
				decision = strings.TrimSpace(ev.Decision)
			}
		}
	}
	if decision == "" {
		return nil
	}
	reason := ""
	for _, ev := range item.Events {
		if ev.Kind == protocol.EventApprovalRequired && strings.TrimSpace(ev.Text) != "" {
			reason = ev.Text
		}
	}
	if reason == "" {
		reason = item.ToolName
	}
	notice := approvalNoticeFromDecision(reason, decision, item.DecisionScope)
	if notice == nil {
		return nil
	}
	return &tuirender.UIMessage{
		Role:   "notice",
		Kind:   tuirender.KindNotice,
		Text:   notice.Text(),
		Notice: notice,
	}
}

func approvalNoticeFromDecision(reason, decision, scope string) *tuirender.SystemNotice {
	detail, command := approvalNoticeActionParts(reason)
	switch strings.TrimSpace(decision) {
	case "allow":
		return &tuirender.SystemNotice{Kind: "approval_allowed", Tone: "success", Action: "Approved", Detail: detail, Command: command, Scope: approvalScopeText(scope, "this time")}
	case "allow_session":
		return &tuirender.SystemNotice{Kind: "approval_allowed_session", Tone: "success", Action: "Approved", Detail: detail, Command: command, Scope: approvalScopeText(scope, "for this session")}
	case "cancel":
		return &tuirender.SystemNotice{Kind: "approval_canceled", Tone: "muted", Action: "Canceled", Subject: "request", Detail: detail, Command: command}
	default:
		return &tuirender.SystemNotice{Kind: "approval_denied", Tone: "error", Action: "Denied", Subject: "request", Detail: detail, Command: command}
	}
}

func approvalScopeText(scope, fallback string) string {
	switch strings.TrimSpace(scope) {
	case "this_time":
		return "this time"
	case "session":
		return "for this session"
	case "":
		return fallback
	default:
		return fallback
	}
}

func (m model) timelineLifecycleMessage(item timeline.Item) *tuirender.UIMessage {
	switch item.Kind {
	case timeline.ItemKindSubagent:
		return m.timelineSubagentMessage(item)
	case timeline.ItemKindWorkflow:
		return m.timelineWorkflowMessage(item)
	case timeline.ItemKindHook:
		return m.timelineHookMessage(item)
	case timeline.ItemKindUserInput:
		return m.timelineUserInputMessage(item)
	default:
		return m.timelineToolMessage(item)
	}
}

func (m model) timelineToolMessage(item timeline.Item) *tuirender.UIMessage {
	toolName := strings.TrimSpace(item.ToolName)
	if toolName == "" || toolName == "update_plan" {
		return nil
	}
	if decisionStopsExecution(item.Decision) && lastToolResultText(item) == "" {
		return nil
	}
	callText := ""
	resultText := ""
	resultRole := ""
	progressText := ""
	for _, ev := range item.Events {
		switch ev.Kind {
		case protocol.EventToolCall:
			callText = ev.Text
		case protocol.EventTaskProgress:
			progressText = ev.Text
		case protocol.EventToolResult:
			resultRole, resultText = summarizeToolResultStructured(toolName, ev.Text, ev.ToolOutcome, ev.ToolCode, ev.ToolPayload)
		}
	}
	if resultText != "" {
		text := timelineCompletedToolTitle(toolName, resultText, resultRole, item)
		role := resultRole
		identity := ""
		if toolDisplayKind(toolName) == "shell" {
			role = shellResultRole(role)
			identity = shellCommandIdentityFromResult(lastToolResultText(item))
			if identity == "" {
				identity = focusShellRawCommand(firstToolCallTitle(toolName, item))
			}
		}
		return &tuirender.UIMessage{
			ID:           item.ToolCallID,
			Role:         role,
			Kind:         tuirender.KindToolCall,
			Text:         text,
			ToolName:     toolName,
			ToolIdentity: identity,
		}
	}
	if progressText != "" {
		return &tuirender.UIMessage{ID: item.ToolCallID, Role: "result_running", Kind: tuirender.KindToolCall, Text: summarizeTaskProgressForChat(toolName, progressText), ToolName: toolName}
	}
	if callText == "" {
		return nil
	}
	return &tuirender.UIMessage{
		ID:       item.ToolCallID,
		Role:     "tool",
		Kind:     tuirender.KindToolCall,
		Text:     summarizeToolCallForChat(toolName, callText),
		ToolName: toolName,
	}
}

func timelineCompletedToolTitle(toolName, summary, role string, item timeline.Item) string {
	raw := lastToolResultText(item)
	previous := firstToolCallTitle(toolName, item)
	title := completedToolTitle(toolName, raw, previous)
	if toolDisplayKind(toolName) == "shell" && strings.TrimSpace(role) == "result_running" {
		title = runningShellTitle(item, previous)
	}
	if summary != "" && summary != "✓" {
		title += "\n" + summary
	}
	if diff := renderFileDiffMetadataForChat(item.Metadata, fileDiffPreviewMaxLines); diff != "" && role == "result_ok" {
		title += "\n\n" + diff
	}
	return title
}

func runningShellTitle(item timeline.Item, previous string) string {
	title := strings.TrimSpace(previous)
	if title != "" {
		return title
	}
	for i := len(item.Events) - 1; i >= 0; i-- {
		ev := item.Events[i]
		if ev.Kind != protocol.EventToolResult {
			continue
		}
		if cmd := shellCommandFromToolPayload(ev.ToolPayload); cmd != "" {
			return "Running " + cmd
		}
		if cmd := shellCommandFromResultText(ev.Text); cmd != "" {
			return "Running " + cmd
		}
	}
	return "Running shell command"
}

func shellCommandFromToolPayload(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	inner, _ := payload["payload"].(map[string]any)
	return strings.TrimSpace(core.AsString(inner["command"]))
}

func shellCommandFromResultText(text string) string {
	env := parseToolEnvelope(text)
	return strings.TrimSpace(core.AsString(env.payload["command"]))
}

func firstToolCallTitle(toolName string, item timeline.Item) string {
	for _, ev := range item.Events {
		if ev.Kind == protocol.EventToolCall {
			return summarizeToolCallForChat(toolName, ev.Text)
		}
	}
	return ""
}

func lastToolResultText(item timeline.Item) string {
	for i := len(item.Events) - 1; i >= 0; i-- {
		if item.Events[i].Kind == protocol.EventToolResult {
			return item.Events[i].Text
		}
	}
	return ""
}

func (m model) timelineSubagentMessage(item timeline.Item) *tuirender.UIMessage {
	if decisionStopsExecution(item.Decision) && lastToolResultText(item) == "" {
		return nil
	}
	previous := ""
	role := "tool"
	steps := []protocol.ProgressStep(nil)
	for _, ev := range item.Events {
		switch ev.Kind {
		case protocol.EventToolCall:
			previous = subagentStartedText(ev.Text)
		case protocol.EventTaskProgress:
			previous = subagentProgressText(ev.Text, ev.Status, ev.Metadata, previous)
			role = subagentProgressRole(ev.Status, ev.Text)
			if len(ev.ProgressMessages) > 0 {
				steps = ev.ProgressMessages
			}
		case protocol.EventToolResult:
			previous = subagentCompletedTextStructured(ev.Text, ev.ToolOutcome, ev.ToolCode, ev.ToolPayload, previous)
			role = "result_ok"
			if !toolResultSucceededStructured(ev.Text, ev.ToolOutcome) {
				role = "result_failed"
			}
		}
	}
	if strings.TrimSpace(previous) == "" {
		return nil
	}
	return &tuirender.UIMessage{
		ID:            item.ToolCallID,
		Role:          role,
		Kind:          tuirender.KindSubagent,
		Text:          previous,
		ToolName:      "spawn_subagent",
		SubagentSteps: steps,
	}
}

func decisionStopsExecution(decision string) bool {
	switch strings.TrimSpace(decision) {
	case "cancel", "deny":
		return true
	default:
		return false
	}
}

// toolResultSucceededStructured prefers the structured outcome, falling
// back to text parsing for events that predate it.
func toolResultSucceededStructured(text, outcome string) bool {
	if strings.TrimSpace(outcome) != "" {
		return outcome == string(core.OutcomeSuccess) || outcome == string(core.OutcomeNoResult)
	}
	return toolResultSucceeded(text)
}

func toolResultSucceeded(raw string) bool {
	env := parseToolEnvelope(raw)
	if v, ok := env.payload["success"]; ok {
		return asBool(v)
	}
	if v, ok := env.payload["ok"]; ok {
		return asBool(v)
	}
	return true
}

func (m model) timelineWorkflowMessage(item timeline.Item) *tuirender.UIMessage {
	if msg := m.timelineWorkflowResultMessage(item); msg != nil {
		return msg
	}
	if msg := m.timelineWorkflowSnapshotMessage(item); msg != nil {
		return msg
	}
	return m.timelineToolMessage(item)
}

func (m model) timelineWorkflowResultMessage(item timeline.Item) *tuirender.UIMessage {
	for i := len(item.Events) - 1; i >= 0; i-- {
		ev := item.Events[i]
		if ev.Kind != protocol.EventWorkflowResult && ev.Kind != protocol.EventWorkflowTerminal {
			continue
		}
		text := strings.TrimSpace(ev.Text)
		if ev.LocalResult != nil {
			if plain := strings.TrimSpace(ev.LocalResult.PlainText); plain != "" {
				text = plain
			}
		}
		if text == "" {
			return nil
		}
		if ev.LocalResult != nil {
			return &tuirender.UIMessage{Role: "assistant", Kind: tuirender.KindLocalStatus, Text: text, Local: ev.LocalResult}
		}
		return &tuirender.UIMessage{Role: "assistant", Kind: tuirender.KindText, Text: text}
	}
	return nil
}

func (m model) timelineWorkflowSnapshotMessage(item timeline.Item) *tuirender.UIMessage {
	for i := len(item.Events) - 1; i >= 0; i-- {
		ev := item.Events[i]
		if ev.Kind != protocol.EventWorkflowSnapshot || ev.LocalResult == nil || ev.LocalResult.WorkflowPanelSnapshot == nil {
			continue
		}
		snapshot := ev.LocalResult.WorkflowPanelSnapshot
		text := workflowTimelineText(snapshot)
		if text == "" {
			return nil
		}
		return &tuirender.UIMessage{
			ID:       item.ID,
			Role:     workflowTimelineRole(snapshot.Status),
			Kind:     tuirender.KindStatus,
			Text:     text,
			ToolName: "workflow",
		}
	}
	return nil
}

func workflowTimelineText(snapshot *protocol.WorkflowPanelSnapshot) string {
	if snapshot == nil {
		return ""
	}
	parts := []string{"Workflow"}
	if s := strings.TrimSpace(snapshot.RunID); s != "" {
		parts = append(parts, s)
	}
	if s := strings.TrimSpace(snapshot.Status); s != "" {
		parts = append(parts, s)
	}
	lines := []string{strings.Join(parts, " · ")}
	if s := strings.TrimSpace(snapshot.CurrentPhase); s != "" {
		lines = append(lines, "phase: "+s)
	}
	if s := strings.TrimSpace(snapshot.Summary); s != "" {
		lines = append(lines, "summary: "+firstNonEmptyLine(s))
	}
	if s := strings.TrimSpace(snapshot.Error); s != "" {
		lines = append(lines, "error: "+firstNonEmptyLine(s))
	}
	return strings.Join(lines, "\n")
}

func workflowTimelineRole(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "done", "success", "succeeded":
		return "result_ok"
	case "failed", "error":
		return "result_failed"
	case "canceled", "cancelled":
		return "result_canceled"
	default:
		return "result_running"
	}
}

func (m model) timelineHookMessage(item timeline.Item) *tuirender.UIMessage {
	for i := len(item.Events) - 1; i >= 0; i-- {
		ev := item.Events[i]
		if ev.Kind != protocol.EventHookCompleted && ev.Kind != protocol.EventHookStarted {
			continue
		}
		text := strings.TrimSpace(ev.Text)
		role := "result_running"
		if ev.Hook != nil {
			role = hookTimelineRole(ev.Hook.Status)
			if text == "" {
				text = strings.TrimSpace(ev.Hook.Message)
			}
			if text == "" {
				text = strings.TrimSpace(ev.Hook.Name)
			}
		}
		if text == "" {
			return nil
		}
		return &tuirender.UIMessage{ID: item.ID, Role: role, Kind: tuirender.KindNotice, Text: text}
	}
	return nil
}

func hookTimelineRole(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "blocked", "failed", "error":
		return "result_failed"
	case "warning":
		return "result_neutral"
	case "completed", "done", "ok", "success":
		return "result_ok"
	default:
		return "result_running"
	}
}

func (m model) timelineUserInputMessage(item timeline.Item) *tuirender.UIMessage {
	for i := len(item.Events) - 1; i >= 0; i-- {
		ev := item.Events[i]
		switch ev.Kind {
		case protocol.EventUserInputDone:
			return &tuirender.UIMessage{ID: item.ID, Role: "notice", Kind: tuirender.KindNotice, Text: userInputDoneText(ev)}
		case protocol.EventUserInputRequired:
			return &tuirender.UIMessage{ID: item.ID, Role: "result_running", Kind: tuirender.KindStatus, Text: userInputRequiredText(ev)}
		}
	}
	return nil
}

func userInputRequiredText(ev timeline.TimelineEvent) string {
	count := len(ev.Questions)
	if count == 1 {
		return "User input required · 1 question"
	}
	if count > 1 {
		return "User input required · " + strconv.Itoa(count) + " questions"
	}
	return "User input required"
}

func userInputDoneText(ev timeline.TimelineEvent) string {
	switch strings.ToLower(strings.TrimSpace(ev.Status)) {
	case "canceled", "cancelled":
		return "User input canceled"
	default:
		return "User input submitted"
	}
}
