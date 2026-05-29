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
	started   bool
	completed bool
}

func (a *Agent) emitAssistantContentDelta(ctx context.Context, delta string, ps *planState, events chan<- AgentEvent) bool {
	if a.mode != session.ModePlan {
		return sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypeAssistantDelta, Content: delta})
	}
	for _, seg := range ps.parser.Parse(delta) {
		if !a.emitProposedPlanSegment(ctx, seg, ps, events) {
			return false
		}
	}
	return true
}

func (a *Agent) emitProposedPlanSegment(ctx context.Context, seg core.ProposedPlanSegment, ps *planState, events chan<- AgentEvent) bool {
	switch seg.Kind {
	case core.ProposedPlanSegmentNormal:
		if seg.Text != "" {
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

func (a *Agent) emitFinalProposedPlan(ctx context.Context, text string, ps *planState, events chan<- AgentEvent) bool {
	plan, ok := core.ExtractProposedPlanText(text)
	if !ok {
		return true
	}
	ps.started = true
	ps.completed = true
	ps.text.Reset()
	ps.text.WriteString(plan)
	if plan != "" {
		if !sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypePlanDelta, Content: plan}) {
			return false
		}
	}
	return sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypePlanCompleted, Content: plan})
}
