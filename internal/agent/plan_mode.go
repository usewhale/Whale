package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/session"
)

// planFinalizationPrefix is prefilled onto the assistant turn during plan
// finalization recovery so the model is forced to begin its reply inside the
// plan block and cannot emit another preamble, tool call, or reasoning-only
// response. deepseek (and other prefix-completion providers) continue from it.
const planFinalizationPrefix = core.ProposedPlanOpenTag + "\n"

// assistantHasProposedPlan reports whether a finished assistant turn actually
// produced a proposed plan, i.e. a parsed plan part. It deliberately does NOT
// match a raw <proposed_plan> mention in visible text: a well-formed block is
// stripped from Text into a plan part, so a tag remaining in Text means the
// model only referenced the tag in prose (e.g. "I'll output the plan in
// <proposed_plan>") without emitting a real block — exactly the no-plan case
// recovery must still handle.
func assistantHasProposedPlan(msg core.Message) bool {
	for _, part := range msg.Parts {
		if part.Type == core.MessagePartPlan && strings.TrimSpace(part.Text) != "" {
			return true
		}
	}
	return false
}

// planFinalizationMissing reports whether a finished plan-mode turn ended with
// real content but no <proposed_plan> block — the condition the TUI surfaces
// passively via markMissingProposedPlanIfNeeded. The model commonly drafts the
// plan into reasoning or writes an analysis preamble and stops.
func planFinalizationMissing(msg core.Message) bool {
	if assistantHasProposedPlan(msg) {
		return false
	}
	return strings.TrimSpace(msg.Text) != "" || strings.TrimSpace(msg.Reasoning) != ""
}

// planFinalizationNeeded bundles the Plan-mode check with planFinalizationMissing
// so the turn loop need not import session.
func (a *Agent) planFinalizationNeeded(msg core.Message) bool {
	return a != nil && a.mode == session.ModePlan && planFinalizationMissing(msg)
}

// inPlanMode reports whether the agent is currently in Plan mode.
func (a *Agent) inPlanMode() bool {
	return a != nil && a.mode == session.ModePlan
}

// hideRecoveredEmptyAssistant neutralizes the empty assistant message that
// collectAssistantStream persisted with FinishReasonError before a recoverable
// empty completion. Without this, transcript replay and resumed provider history
// (rebuilt from the store) would carry a spurious failed turn ahead of the
// recovered plan. Best-effort: the store exposes no delete, so the row is marked
// hidden + canceled instead. Only acts on a trailing empty assistant turn.
func (a *Agent) hideRecoveredEmptyAssistant(ctx context.Context, sessionID string) {
	if a == nil || a.store == nil {
		return
	}
	msgs, err := a.store.List(ctx, sessionID)
	if err != nil || len(msgs) == 0 {
		return
	}
	last := msgs[len(msgs)-1]
	if last.Role != core.RoleAssistant || last.Hidden {
		return
	}
	if strings.TrimSpace(last.Text) != "" || len(last.ToolCalls) > 0 || len(last.Parts) > 0 {
		return
	}
	last.Hidden = true
	last.FinishReason = core.FinishReasonCanceled
	last.ErrorDetail = ""
	_ = a.store.Update(ctx, last)
}

// planState groups the plan-tracking vars that would otherwise be scattered
// as individual locals across streamAndHandle.
type planState struct {
	parser    core.ProposedPlanParser
	text      strings.Builder
	visible   strings.Builder
	started   bool
	completed bool
}

func (a *Agent) emitAssistantContentDelta(ctx context.Context, delta string, ps *planState, events chan<- AgentEvent) (string, bool) {
	if a.mode != session.ModePlan {
		return delta, sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypeAssistantDelta, Content: delta})
	}
	for _, seg := range ps.parser.Parse(delta) {
		if !a.emitProposedPlanSegment(ctx, seg, ps, events) {
			return "", false
		}
	}
	return ps.visible.String(), true
}

func (a *Agent) emitProposedPlanSegment(ctx context.Context, seg core.ProposedPlanSegment, ps *planState, events chan<- AgentEvent) bool {
	switch seg.Kind {
	case core.ProposedPlanSegmentNormal:
		if seg.Text != "" {
			ps.visible.WriteString(seg.Text)
			return sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypeAssistantDelta, Content: seg.Text})
		}
	case core.ProposedPlanSegmentStart:
		ps.started = true
		ps.completed = false
		ps.text.Reset()
	case core.ProposedPlanSegmentDelta:
		if ps.started && seg.Text != "" {
			ps.text.WriteString(seg.Text)
			return sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypePlanDelta, Content: seg.Text})
		}
	case core.ProposedPlanSegmentEnd:
		if ps.started && !ps.completed {
			ps.completed = true
			return sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypePlanCompleted, Content: ps.text.String()})
		}
	}
	return true
}

func (a *Agent) emitFinalProposedPlan(ctx context.Context, text string, ps *planState, events chan<- AgentEvent) (string, bool) {
	visible := core.StripProposedPlanBlocks(text)
	plan, ok := core.ExtractProposedPlanText(text)
	if !ok {
		return visible, true
	}
	ps.started = true
	ps.completed = true
	ps.text.Reset()
	ps.text.WriteString(plan)
	if plan != "" {
		if !sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypePlanDelta, Content: plan}) {
			return "", false
		}
	}
	return visible, sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypePlanCompleted, Content: plan})
}

func finalizeAssistantPlanParts(msg *core.Message, ps *planState) {
	if msg == nil || strings.TrimSpace(ps.text.String()) == "" {
		return
	}
	msg.Parts = removeAssistantTextPlanParts(msg.Parts)
	if strings.TrimSpace(msg.Text) != "" {
		msg.Parts = append([]core.MessagePart{{Type: core.MessagePartText, Text: msg.Text}}, msg.Parts...)
	}
	msg.Parts = append(msg.Parts, core.MessagePart{Type: core.MessagePartPlan, Text: ps.text.String()})
}

func (a *Agent) recoverProposedPlanToolCall(ctx context.Context, msg *core.Message, ps *planState, events chan<- AgentEvent) bool {
	if a == nil || a.mode != session.ModePlan || msg == nil || len(msg.ToolCalls) == 0 {
		return false
	}
	for _, call := range msg.ToolCalls {
		if call.Name != "proposed_plan" {
			continue
		}
		plan := proposedPlanToolCallText(call.Input)
		if strings.TrimSpace(plan) == "" {
			continue
		}
		ps.started = true
		ps.completed = true
		ps.text.Reset()
		ps.text.WriteString(plan)
		if !sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypePlanDelta, Content: plan}) {
			return false
		}
		if !sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypePlanCompleted, Content: plan}) {
			return false
		}
		msg.ToolCalls = nil
		msg.FinishReason = core.FinishReasonEndTurn
		finalizeAssistantPlanParts(msg, ps)
		return true
	}
	return false
}

func proposedPlanToolCallText(input string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(input), &payload); err != nil {
		return ""
	}
	for _, key := range []string{"plan", "content", "text"} {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func removeAssistantTextPlanParts(parts []core.MessagePart) []core.MessagePart {
	if len(parts) == 0 {
		return nil
	}
	out := parts[:0]
	for _, part := range parts {
		if part.Type != core.MessagePartText && part.Type != core.MessagePartPlan {
			out = append(out, part)
		}
	}
	return out
}
