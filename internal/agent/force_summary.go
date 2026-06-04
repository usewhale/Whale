package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
)

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
	if assistant.FinishReason == "" {
		assistant.FinishReason = core.FinishReasonEndTurn
	}
	if err := a.store.Update(ctx, assistant); err != nil {
		return core.Message{}, err
	}
	return assistant, nil
}
