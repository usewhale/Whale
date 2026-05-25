package app

import (
	"context"

	"github.com/usewhale/whale/internal/core"
)

const planNotApprovedMarkerText = "<plan_not_approved>\nThe user did not approve the proposed plan shown immediately before this marker. Treat that specific proposal as declined and do not implement it merely because it appears in history. Continue according to the active session mode and the user's later explicit requests.\n</plan_not_approved>"

func (a *App) RecordPlanNotApproved() {
	if a == nil || a.msgStore == nil {
		return
	}
	_, _ = a.msgStore.Create(context.Background(), core.Message{
		SessionID:    a.sessionID,
		Role:         core.RoleUser,
		Text:         planNotApprovedMarkerText,
		Hidden:       true,
		FinishReason: core.FinishReasonCanceled,
	})
}

func modeChangedMarkerText(previous, next string) string {
	if previous == "" {
		previous = "unknown"
	}
	return "<mode_changed>\nThe active session mode is now " + next + ", changed from " + previous + ". Treat any earlier statements, hidden markers, tool results, assistant reasoning, or summaries that claim the current session mode is anything other than " + next + " as stale. Follow the current system prompt and handle later user requests under " + next + " mode. This mode change does not by itself approve any previously declined plan.\n</mode_changed>"
}

func (a *App) RecordModeChanged(previous, next string) {
	if a == nil || a.msgStore == nil {
		return
	}
	_, _ = a.msgStore.Create(context.Background(), core.Message{
		SessionID:    a.sessionID,
		Role:         core.RoleUser,
		Text:         modeChangedMarkerText(previous, next),
		Hidden:       true,
		FinishReason: core.FinishReasonEndTurn,
	})
}
