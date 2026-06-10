package agent

import (
	"context"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/session"
)

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
