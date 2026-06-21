package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
)

// maxConsecutiveStormRounds is how many back-to-back rounds of entirely
// storm-blocked tool calls end the turn. The storm breaker blocks individual
// repeated calls, but without this a capless main agent would keep re-issuing
// them forever; a few fully-blocked rounds in a row is an unambiguous loop.
const maxConsecutiveStormRounds = 3

// isAllStormBlocked reports whether a tool-result message represents a round
// that made no progress: it has at least one result and every one was
// storm-blocked. A round with any non-blocked result resets the loop guard.
func isAllStormBlocked(toolMsg core.Message) bool {
	if len(toolMsg.ToolResults) == 0 {
		return false
	}
	for _, r := range toolMsg.ToolResults {
		if r.Code != "storm_blocked" {
			return false
		}
	}
	return true
}

// forceSummaryAndFinish emits the forced-summary lifecycle events and the
// terminal Done event for a turn that is being stopped early (cap hit or loop
// detected). The caller must return immediately after calling it.
func (a *Agent) forceSummaryAndFinish(ctx context.Context, sessionID string, history []core.Message, reason string, reqCtx summaryRequestContext, emit func(AgentEvent) bool) {
	if !emit(AgentEvent{Type: AgentEventTypeForcedSummaryStarted, Content: reason}) {
		return
	}
	sum, serr := a.forceSummary(ctx, sessionID, history, reason, reqCtx)
	if serr != nil {
		if !emit(AgentEvent{Type: AgentEventTypeForcedSummaryFailed, Content: serr.Error()}) {
			return
		}
		emit(AgentEvent{Type: AgentEventTypeError, Err: serr})
		return
	}
	if !emit(AgentEvent{Type: AgentEventTypeForcedSummaryDone, Content: "forced summary completed"}) {
		return
	}
	emit(AgentEvent{Type: AgentEventTypeDone, Message: &sum})
}

// forcedSummaryBanner renders the truncation notice prepended to a forced
// summary. It must read as an interruption, not a completion: it states the run
// was auto-stopped, why, and that it can be resumed.
func forcedSummaryBanner(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "execution limit reached"
	}
	return fmt.Sprintf("⚠️ This turn was auto-interrupted after reaching an execution limit (%s). "+
		"The summary below is PROGRESS, not a completed task — work may remain. "+
		"Send another message or use /retry to continue from the current context.", reason)
}

func (a *Agent) forceSummary(ctx context.Context, sessionID string, history []core.Message, reason string, reqCtx summaryRequestContext) (core.Message, error) {
	assistant, err := a.store.Create(ctx, core.Message{SessionID: sessionID, Role: core.RoleAssistant})
	if err != nil {
		return core.Message{}, fmt.Errorf("create forced summary assistant: %w", err)
	}
	prompt := fmt.Sprintf("The run stopped: %s. Summarize completed work, findings, and remaining next steps concisely. Do not call tools.", strings.TrimSpace(reason))
	tmpHistory := buildSummaryProviderHistory(sessionID, reqCtx, history, prompt)
	ch := a.provider.StreamResponse(ctx, tmpHistory, nil)
	lastUsage := llm.Usage{}
	lastModel := ""
	for ev := range ch {
		switch ev.Type {
		case llm.EventContentDelta:
			assistant.Text += ev.Content
		case llm.EventReasoningDelta:
			assistant.Reasoning += ev.ReasoningDelta
		case llm.EventComplete:
			if ev.Response != nil {
				lastUsage = ev.Response.Usage
				lastModel = ev.Response.Model
				if strings.TrimSpace(ev.Response.Content) != "" {
					assistant.Text = ev.Response.Content
				}
				assistant.FinishReason = core.FinishReasonEndTurn
			}
		case llm.EventError:
			if ev.Err != nil {
				return core.Message{}, ev.Err
			}
		}
	}
	a.recordTurnCost(sessionID, lastUsage, lastModel, reqCtx.prefixFingerprint, buildCacheShapeForRequestWithRuntime(cacheShapeRequestForceSummary, tmpHistory, nil, "", reqCtx.systemBlocks, reqCtx.runtimeBlocks))
	if strings.TrimSpace(assistant.Text) == "" {
		assistant.Text = "Run stopped before completion. Please retry with /retry to continue from current context."
	}
	// Prepend a persisted banner so this turn can never be mistaken for a
	// completed task — by the user reading it, or by a resuming model that sees
	// it in history. The banner names the reason and points at how to continue.
	assistant.Text = forcedSummaryBanner(reason) + "\n\n" + assistant.Text
	if assistant.FinishReason == "" {
		assistant.FinishReason = core.FinishReasonEndTurn
	}
	if err := a.store.Update(ctx, assistant); err != nil {
		return core.Message{}, err
	}
	return assistant, nil
}
