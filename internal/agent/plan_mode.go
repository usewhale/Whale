package agent

import (
	"context"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/session"
)

func (a *Agent) emitAssistantContentDelta(ctx context.Context, delta string, parser *core.ProposedPlanParser, planText *strings.Builder, planStarted, planCompleted *bool, events chan<- AgentEvent) bool {
	if a.mode != session.ModePlan {
		return sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypeAssistantDelta, Content: delta})
	}
	for _, seg := range parser.Parse(delta) {
		if !a.emitProposedPlanSegment(ctx, seg, planText, planStarted, planCompleted, events) {
			return false
		}
	}
	return true
}

func (a *Agent) emitProposedPlanSegment(ctx context.Context, seg core.ProposedPlanSegment, planText *strings.Builder, planStarted, planCompleted *bool, events chan<- AgentEvent) bool {
	switch seg.Kind {
	case core.ProposedPlanSegmentNormal:
		if seg.Text != "" {
			return sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypeAssistantDelta, Content: seg.Text})
		}
	case core.ProposedPlanSegmentStart:
		*planStarted = true
		*planCompleted = false
		planText.Reset()
	case core.ProposedPlanSegmentDelta:
		if *planStarted && seg.Text != "" {
			planText.WriteString(seg.Text)
			return sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypePlanDelta, Content: seg.Text})
		}
	case core.ProposedPlanSegmentEnd:
		if *planStarted && !*planCompleted {
			*planCompleted = true
			return sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypePlanCompleted, Content: planText.String()})
		}
	}
	return true
}

func (a *Agent) emitFinalProposedPlan(ctx context.Context, text string, planText *strings.Builder, planStarted, planCompleted *bool, events chan<- AgentEvent) bool {
	plan, ok := core.ExtractProposedPlanText(text)
	if !ok {
		return true
	}
	*planStarted = true
	*planCompleted = true
	planText.Reset()
	planText.WriteString(plan)
	if plan != "" {
		if !sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypePlanDelta, Content: plan}) {
			return false
		}
	}
	return sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypePlanCompleted, Content: plan})
}
