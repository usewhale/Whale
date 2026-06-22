package agent

import (
	"context"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/session"
)

// maxPlanLoopNudges bounds how many times a spinning Plan-mode turn is nudged to
// finalize before the runaway-loop guard ends it. One is enough: a plan turn
// that keeps re-running the same read-only checks should be told to commit to a
// plan rather than die as an execution-limit summary with no plan and no gate.
const maxPlanLoopNudges = 1

const planLoopNudgeText = "<plan_investigation_stalled>\nYou keep repeating the same tool calls without moving toward a plan. If you already understand enough, STOP investigating and write your plan now as your final reply (plain Markdown, no special tags). If you genuinely need a decision from the user before you can finalize, call request_user_input instead.\n</plan_investigation_stalled>"

func (a *Agent) persistPlanLoopNudge(ctx context.Context, sessionID string) (core.Message, error) {
	return a.store.Create(ctx, core.Message{
		SessionID:    sessionID,
		Role:         core.RoleUser,
		Text:         planLoopNudgeText,
		Hidden:       true,
		FinishReason: core.FinishReasonEndTurn,
	})
}

// planState accumulates the assistant's reply while in Plan mode.
//
// In the plan-as-reply model there is no <proposed_plan> sentinel to parse: the
// plan is simply the model's final answer. Content streams to the plan pane as
// it arrives (AgentEventTypePlanDelta) and accumulates into text, which becomes
// the assistant message's text and plan part. The turn loop decides when the
// turn has ended with a plan and emits AgentEventTypePlanCompleted.
type planState struct {
	text strings.Builder
}

// emitAssistantContentDelta streams one content delta to the client.
//
// Outside Plan mode it is an ordinary assistant delta and the raw delta is
// returned so the caller can append it to the running message text. In Plan mode
// the whole reply IS the plan, so the delta streams to the plan pane and is
// accumulated; the returned string is the full accumulated plan (the caller
// overwrites the message text with it).
func (a *Agent) emitAssistantContentDelta(ctx context.Context, delta string, ps *planState, events chan<- AgentEvent) (string, bool) {
	if a.mode != session.ModePlan {
		return delta, sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypeAssistantDelta, Content: delta})
	}
	if delta == "" {
		return ps.text.String(), true
	}
	ps.text.WriteString(delta)
	return ps.text.String(), sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypePlanDelta, Content: delta})
}

// finalizeAssistantPlanParts attaches the accumulated plan as a plan part. This
// makes the message render as a plan on session reload (hydration) and keeps the
// plan visible to the model in provider history. It is a no-op outside Plan mode
// (and whenever no plan text was produced), since text is only written while
// streaming Plan-mode content.
//
// Only the final text turn — the one that ends planning with a reply instead of
// another tool call — is the proposed plan. A round that still finishes in
// tool_use is an intermediate step (e.g. a short preamble before a read); its
// text must remain ordinary assistant text. Relabeling such a preamble a plan
// would pollute the persisted history and hydration with non-final text.
func finalizeAssistantPlanParts(msg *core.Message, ps *planState) {
	if msg == nil || len(msg.ToolCalls) > 0 {
		return
	}
	plan := strings.TrimSpace(ps.text.String())
	if plan == "" {
		return
	}
	msg.Parts = []core.MessagePart{{Type: core.MessagePartPlan, Text: plan}}
	msg.Text = plan
}
