package timeline

import (
	"fmt"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/runtime/protocol"
)

type ItemKind string

const (
	ItemKindTool           ItemKind = "tool"
	ItemKindSubagent       ItemKind = "subagent"
	ItemKindParallelReason ItemKind = "parallel_reason"
	ItemKindWorkflow       ItemKind = "workflow"
	ItemKindHook           ItemKind = "hook"
	ItemKindUserInput      ItemKind = "user_input"
)

type Phase string

const (
	PhaseRequested        Phase = "requested"
	PhaseApprovalRequired Phase = "approval_required"
	PhaseApprovalDecided  Phase = "approval_decided"
	PhaseStarted          Phase = "started"
	PhaseProgress         Phase = "progress"
	PhaseCompleted        Phase = "completed"
	PhaseFailed           Phase = "failed"
	PhaseCanceled         Phase = "canceled"
)

type TimelineEvent struct {
	Kind             protocol.EventKind
	Phase            Phase
	Sequence         int64
	Text             string
	Status           string
	Decision         string
	DecisionScope    string
	ApprovalKeys     []string
	Metadata         map[string]any
	LocalResult      *protocol.LocalResult
	Hook             *protocol.HookRun
	StartedAt        time.Time
	ProgressMessages []protocol.ProgressStep
	Questions        []protocol.UserInputQuestion
	ToolOutcome      string
	ToolCode         string
	ToolPayload      map[string]any
}

type Item struct {
	ID            string
	TurnID        string
	ItemID        string
	ParentID      string
	ToolCallID    string
	ApprovalID    string
	WorkflowRunID string
	ToolName      string
	Kind          ItemKind
	Phase         Phase
	Status        string
	Text          string
	Decision      string
	DecisionScope string
	ApprovalKeys  []string
	Metadata      map[string]any
	Events        []TimelineEvent
}

type Snapshot struct {
	Items []Item
}

func (s Snapshot) HasPendingItems() bool {
	for _, item := range s.Items {
		if item.Pending() {
			return true
		}
	}
	return false
}

func (item Item) Pending() bool {
	switch item.Phase {
	case PhaseRequested, PhaseApprovalRequired, PhaseApprovalDecided, PhaseStarted, PhaseProgress:
		return true
	default:
		return false
	}
}

type TurnTimelineBuilder struct {
	items      map[string]*Item
	order      []string
	nextSeq    int64
	nextOrphan int64
}

func NewTurnTimelineBuilder() *TurnTimelineBuilder {
	return &TurnTimelineBuilder{items: map[string]*Item{}}
}

func HydrationEventsFromMessage(msg protocol.Message) []protocol.Event {
	events := make([]protocol.Event, 0, len(msg.ToolCalls)+len(msg.ToolResults))
	for _, tc := range msg.ToolCalls {
		events = append(events, protocol.Event{
			Kind:       protocol.EventToolCall,
			ToolCallID: tc.ID,
			ToolName:   tc.Name,
			Text:       tc.Input,
			StartedAt:  msg.CreatedAt,
		})
	}
	for _, tr := range msg.ToolResults {
		status := ""
		if tr.IsError {
			status = "failed"
		}
		events = append(events, protocol.Event{
			Kind:        protocol.EventToolResult,
			ToolCallID:  tr.ToolCallID,
			ToolName:    tr.Name,
			Text:        tr.Content,
			Metadata:    cloneMetadata(tr.Metadata),
			Status:      status,
			ToolOutcome: tr.Outcome,
			ToolCode:    tr.Code,
			ToolPayload: tr.Payload,
			StartedAt:   msg.CreatedAt,
		})
	}
	return events
}

func (b *TurnTimelineBuilder) Reset() {
	b.items = map[string]*Item{}
	b.order = nil
	b.nextSeq = 0
	b.nextOrphan = 0
}

func (b *TurnTimelineBuilder) HandleEvent(ev protocol.Event) {
	phase, ok := phaseForEvent(ev)
	if !ok {
		return
	}
	id := itemIdentity(ev)
	if id == "" {
		b.nextOrphan++
		id = fmt.Sprintf("orphan:%s:%d", ev.Kind, b.nextOrphan)
	}
	item := b.item(id)
	applyEventIdentity(item, id, ev)
	if item.Kind == "" {
		item.Kind = classifyItem(ev)
	}
	if item.Kind == "" {
		item.Kind = ItemKindTool
	}
	if phase == PhaseCompleted && resultLooksFailed(ev) {
		phase = PhaseFailed
	}
	if ev.Kind == protocol.EventHookCompleted {
		phase = phaseForHookCompleted(ev)
	}
	if ev.Kind == protocol.EventApprovalDecision {
		phase = phaseForApprovalDecision(ev.Decision)
	}
	item.Phase = phase
	item.Status = strings.TrimSpace(ev.Status)
	item.Text = strings.TrimSpace(ev.Text)
	item.Decision = firstNonEmpty(ev.Decision, item.Decision)
	item.DecisionScope = firstNonEmpty(ev.DecisionScope, item.DecisionScope)
	if len(ev.ApprovalKeys) > 0 {
		item.ApprovalKeys = append([]string(nil), ev.ApprovalKeys...)
	}
	if len(ev.Metadata) > 0 {
		item.Metadata = cloneMetadata(ev.Metadata)
	}
	seq := ev.Sequence
	if seq == 0 {
		b.nextSeq++
		seq = b.nextSeq
	}
	item.Events = append(item.Events, TimelineEvent{
		Kind:             ev.Kind,
		Phase:            phase,
		Sequence:         seq,
		Text:             ev.Text,
		Status:           ev.Status,
		Decision:         ev.Decision,
		DecisionScope:    ev.DecisionScope,
		ApprovalKeys:     append([]string(nil), ev.ApprovalKeys...),
		Metadata:         cloneMetadata(ev.Metadata),
		LocalResult:      ev.LocalResult,
		Hook:             ev.Hook,
		StartedAt:        ev.StartedAt,
		ProgressMessages: append([]protocol.ProgressStep(nil), ev.ProgressMessages...),
		Questions:        append([]protocol.UserInputQuestion(nil), ev.Questions...),
		ToolOutcome:      ev.ToolOutcome,
		ToolCode:         ev.ToolCode,
		ToolPayload:      ev.ToolPayload,
	})
}

func auditOnlyMetadata(metadata map[string]any) bool {
	if metadata == nil {
		return false
	}
	visibility, _ := metadata["ui_visibility"].(string)
	return strings.TrimSpace(visibility) == "audit"
}

func (b *TurnTimelineBuilder) Snapshot() Snapshot {
	items := make([]Item, 0, len(b.order))
	for _, id := range b.order {
		item := b.items[id]
		if item == nil {
			continue
		}
		if auditOnlyMetadata(item.Metadata) {
			continue
		}
		cp := *item
		cp.Metadata = cloneMetadata(item.Metadata)
		cp.ApprovalKeys = append([]string(nil), item.ApprovalKeys...)
		cp.Events = append([]TimelineEvent(nil), item.Events...)
		items = append(items, cp)
	}
	return Snapshot{Items: items}
}

func phaseForApprovalDecision(decision string) Phase {
	switch strings.TrimSpace(decision) {
	case "cancel":
		return PhaseCanceled
	case "deny":
		return PhaseFailed
	default:
		return PhaseApprovalDecided
	}
}

func (b *TurnTimelineBuilder) item(id string) *Item {
	if b.items == nil {
		b.items = map[string]*Item{}
	}
	if item := b.items[id]; item != nil {
		return item
	}
	item := &Item{ID: id}
	b.items[id] = item
	b.order = append(b.order, id)
	return item
}

func phaseForEvent(ev protocol.Event) (Phase, bool) {
	switch ev.Kind {
	case protocol.EventToolCall:
		return PhaseRequested, true
	case protocol.EventApprovalRequired:
		return PhaseApprovalRequired, true
	case protocol.EventApprovalDecision:
		return PhaseApprovalDecided, true
	case protocol.EventTaskStarted, protocol.EventHookStarted, protocol.EventUserInputRequired:
		return PhaseStarted, true
	case protocol.EventTaskProgress:
		return PhaseProgress, true
	case protocol.EventWorkflowSnapshot:
		return phaseForWorkflowSnapshot(ev), true
	case protocol.EventTaskCompleted, protocol.EventHookCompleted, protocol.EventUserInputDone, protocol.EventWorkflowResult, protocol.EventWorkflowTerminal:
		return PhaseCompleted, true
	case protocol.EventToolResult:
		if resultLooksFailed(ev) {
			return PhaseFailed, true
		}
		return PhaseCompleted, true
	default:
		return "", false
	}
}

func itemIdentity(ev protocol.Event) string {
	if s := strings.TrimSpace(ev.ItemID); s != "" {
		return s
	}
	if ev.Kind == protocol.EventWorkflowSnapshot || ev.Kind == protocol.EventWorkflowResult || ev.Kind == protocol.EventWorkflowTerminal {
		if s := workflowRunID(ev); s != "" {
			return "workflow:" + s
		}
	}
	if s := strings.TrimSpace(ev.ToolCallID); s != "" {
		return "tool:" + s
	}
	if ev.Approval != nil {
		if s := strings.TrimSpace(ev.Approval.ToolCall.ID); s != "" {
			return "tool:" + s
		}
	}
	if ev.Hook != nil {
		if s := strings.TrimSpace(ev.Hook.ID); s != "" {
			return "hook:" + s
		}
	}
	if ev.Kind == protocol.EventUserInputDone {
		return "user_input:active"
	}
	return ""
}

func applyEventIdentity(item *Item, id string, ev protocol.Event) {
	item.ID = id
	item.TurnID = firstNonEmpty(item.TurnID, ev.TurnID)
	item.ItemID = firstNonEmpty(item.ItemID, ev.ItemID)
	item.ParentID = firstNonEmpty(item.ParentID, ev.ParentID)
	item.ApprovalID = firstNonEmpty(item.ApprovalID, ev.ApprovalID)
	item.WorkflowRunID = firstNonEmpty(item.WorkflowRunID, workflowRunID(ev))
	item.ToolCallID = firstNonEmpty(item.ToolCallID, ev.ToolCallID)
	item.ToolName = firstNonEmpty(item.ToolName, ev.ToolName)
	if ev.Approval != nil {
		item.ToolCallID = firstNonEmpty(item.ToolCallID, ev.Approval.ToolCall.ID)
		item.ToolName = firstNonEmpty(item.ToolName, ev.Approval.ToolCall.Name)
		item.ApprovalID = firstNonEmpty(item.ApprovalID, ev.Approval.Key)
	}
	if ev.Hook != nil {
		item.ToolName = firstNonEmpty(item.ToolName, ev.Hook.Name)
	}
}

func classifyItem(ev protocol.Event) ItemKind {
	name := strings.TrimSpace(ev.ToolName)
	if ev.Approval != nil && name == "" {
		name = ev.Approval.ToolCall.Name
	}
	switch name {
	case "spawn_subagent":
		return ItemKindSubagent
	case "parallel_reason":
		return ItemKindParallelReason
	case "workflow":
		return ItemKindWorkflow
	}
	switch ev.Kind {
	case protocol.EventHookStarted, protocol.EventHookCompleted:
		return ItemKindHook
	case protocol.EventUserInputRequired, protocol.EventUserInputDone:
		return ItemKindUserInput
	case protocol.EventWorkflowSnapshot, protocol.EventWorkflowResult, protocol.EventWorkflowTerminal:
		return ItemKindWorkflow
	default:
		return ItemKindTool
	}
}

func phaseForWorkflowSnapshot(ev protocol.Event) Phase {
	status := strings.ToLower(strings.TrimSpace(ev.Status))
	if status == "" && ev.LocalResult != nil && ev.LocalResult.WorkflowPanelSnapshot != nil {
		status = strings.ToLower(strings.TrimSpace(ev.LocalResult.WorkflowPanelSnapshot.Status))
	}
	switch status {
	case "completed", "done", "success", "succeeded":
		return PhaseCompleted
	case "failed", "error":
		return PhaseFailed
	case "canceled", "cancelled":
		return PhaseCanceled
	case "running", "active", "in_progress":
		return PhaseProgress
	default:
		return PhaseProgress
	}
}

func phaseForHookCompleted(ev protocol.Event) Phase {
	status := strings.ToLower(strings.TrimSpace(ev.Status))
	if status == "" && ev.Hook != nil {
		status = strings.ToLower(strings.TrimSpace(ev.Hook.Status))
	}
	switch status {
	case "blocked", "failed", "error":
		return PhaseFailed
	case "canceled", "cancelled":
		return PhaseCanceled
	default:
		return PhaseCompleted
	}
}

func workflowRunID(ev protocol.Event) string {
	if s := strings.TrimSpace(ev.WorkflowRunID); s != "" {
		return s
	}
	for _, key := range []string{"workflow_run_id", "run_id", "runId"} {
		if s := metadataString(ev.Metadata, key); s != "" {
			return s
		}
	}
	if ev.LocalResult != nil && ev.LocalResult.WorkflowPanelSnapshot != nil {
		return strings.TrimSpace(ev.LocalResult.WorkflowPanelSnapshot.RunID)
	}
	return ""
}

func resultLooksFailed(ev protocol.Event) bool {
	status := strings.ToLower(strings.TrimSpace(ev.Status))
	if status == "failed" || status == "error" || status == "canceled" || status == "cancelled" {
		return true
	}
	text := strings.ToLower(ev.Text)
	return strings.Contains(text, `"success":false`) ||
		strings.Contains(text, `"ok":false`) ||
		strings.Contains(text, `"is_error":true`)
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	raw, ok := metadata[key]
	if !ok || raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func cloneMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]any, len(metadata))
	for k, v := range metadata {
		out[k] = v
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if s := strings.TrimSpace(value); s != "" {
			return s
		}
	}
	return ""
}
