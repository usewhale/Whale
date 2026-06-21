package agent

import (
	"context"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/session"
)

// maxPlanProposalNudges bounds how many times a Plan-mode turn that finished
// without a <proposed_plan> block is re-prompted to emit one. DeepSeek sometimes
// stops generation right after the announcing preamble ("Here's the plan:") and
// before the block, so the turn ends cleanly (finish_reason=stop) with content
// but no plan to approve — the "couldn't make plan" failure. This is distinct
// from the reasoning-only empty completion handled in the provider client: here
// content is non-empty, so no terminal error fires. A couple of nudges recover
// it; past the cap we surface the planless answer rather than loop forever.
const maxPlanProposalNudges = 2

const planProposalNudgeText = "<plan_not_finalized>\nYour previous turn ended without proposing a plan: it contained no <proposed_plan> block. Plan mode requires finishing with exactly one <proposed_plan> block, opening and closing tags each on their own line. Output the complete plan now as a single <proposed_plan> block. If you genuinely need a decision from the user before you can finalize, call request_user_input instead — do not end the turn with neither.\n</plan_not_finalized>"

// maxMidTurnPlanNudges bounds how many times a Plan-mode turn that is spinning
// mid-turn (re-reading the same files without finalizing) is nudged to produce
// the plan. The end-of-turn nudge (planProposalNudgeText) can't help here: that
// loop never ends the turn — it keeps calling tools — so it must be interrupted
// in-flight. One nudge is enough; past it the progress guard ends the turn.
const maxMidTurnPlanNudges = 1

// planRedundancyNudgeThreshold is how many consecutive redundant rounds trigger
// the mid-turn nudge. Kept below maxConsecutiveRedundantRounds so a Plan-mode
// turn gets a chance to self-correct before the hard progress guard fires.
const planRedundancyNudgeThreshold = 3

const planProgressNudgeText = "<plan_investigation_stalled>\nYou keep re-reading the same files without moving toward a plan. If you already understand enough, STOP investigating and output your plan now as a single <proposed_plan> block (opening and closing tags each on their own line). If you genuinely need a decision from the user before you can finalize, call request_user_input instead.\n</plan_investigation_stalled>"

// assistantProposedPlan reports whether a finalized assistant message carries a
// plan part — i.e. a <proposed_plan> block was emitted and captured this turn.
func assistantProposedPlan(msg core.Message) bool {
	for _, part := range msg.Parts {
		if part.Type == core.MessagePartPlan {
			return true
		}
	}
	return false
}

// planProposalMissing reports whether a completed (non-tool) Plan-mode turn ended
// without proposing a plan, so the run loop should nudge the model to emit one.
// Turns that call a tool (including request_user_input) finish as tool_use and
// never reach this check, so legitimate clarifying questions are not disturbed.
func (a *Agent) planProposalMissing(assistant core.Message) bool {
	return a.mode == session.ModePlan &&
		assistant.FinishReason == core.FinishReasonEndTurn &&
		!assistantProposedPlan(assistant)
}

func (a *Agent) persistPlanProposalNudge(ctx context.Context, sessionID string) (core.Message, error) {
	return a.store.Create(ctx, core.Message{
		SessionID:    sessionID,
		Role:         core.RoleUser,
		Text:         planProposalNudgeText,
		Hidden:       true,
		FinishReason: core.FinishReasonEndTurn,
	})
}

func (a *Agent) persistPlanProgressNudge(ctx context.Context, sessionID string) (core.Message, error) {
	return a.store.Create(ctx, core.Message{
		SessionID:    sessionID,
		Role:         core.RoleUser,
		Text:         planProgressNudgeText,
		Hidden:       true,
		FinishReason: core.FinishReasonEndTurn,
	})
}
