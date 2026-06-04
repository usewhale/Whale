package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/session"
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
	previous = strings.TrimSpace(previous)
	next = strings.TrimSpace(next)
	if previous == "" {
		previous = "unknown"
	}
	if next == "" {
		next = "agent"
	}
	return "<mode_changed>\nThe active session mode is now " + next + ", changed from " + previous + ". Treat any earlier statements, hidden markers, tool results, assistant reasoning, or summaries that claim the current session mode is anything other than " + next + " as stale. This mode change does not by itself approve any previously declined plan.\n\n" + modeChangedInstruction(next) + "\n</mode_changed>"
}

func modeChangedInstruction(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "ask":
		return "Ask mode instruction: answer the user's question directly. Use read-only inspection tools when useful, but do not modify files, launch writable workflows, create branches, or act as though you are implementing changes. If implementation is needed, explain the change or outline it instead."
	case "plan":
		return "Plan mode instruction: design the work before implementation. Treat execution requests such as implement, fix, publish, create a branch, or open a worktree as requests to plan that execution. Explore with non-mutating tools when helpful, but do not edit, write, patch, format, migrate, create branches or worktrees, or run commands whose purpose is to carry out the plan. When the plan is decision-complete, output exactly one <proposed_plan> block with concise Markdown inside it, and do not ask whether to proceed after the block."
	default:
		return "Agent mode instruction: execute the user's current goal using available read-only and mutating tools as appropriate, subject to policy, mode restrictions, tool results, and user approval."
	}
}

func (a *App) RecordModeChanged(previous, next string) {
	if a == nil || a.msgStore == nil {
		return
	}
	_ = a.recordModeChanged(previous, next)
}

func (a *App) recordModeChanged(previous, next string) error {
	if a == nil || a.msgStore == nil {
		return nil
	}
	_, err := a.msgStore.Create(context.Background(), core.Message{
		SessionID:    a.sessionID,
		Role:         core.RoleUser,
		Text:         modeChangedMarkerText(previous, next),
		Hidden:       true,
		FinishReason: core.FinishReasonEndTurn,
	})
	return err
}

func (a *App) ensureCurrentModeMarker() error {
	if a == nil || a.msgStore == nil {
		return nil
	}
	current := a.currentMode
	if current == "" {
		current = session.ModeAgent
	}
	messages, err := a.msgStore.List(context.Background(), a.sessionID)
	if err != nil {
		return fmt.Errorf("list messages for mode marker: %w", err)
	}
	if latest, ok := latestModeMarkerMode(messages); ok {
		if latest == string(current) {
			return nil
		}
		return a.recordModeChanged(latest, string(current))
	}
	return a.recordModeChanged("unknown", string(current))
}

func latestModeMarkerMode(messages []core.Message) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !msg.Hidden || !strings.Contains(msg.Text, "<mode_changed>") {
			continue
		}
		if mode, ok := parseModeChangedMarkerMode(msg.Text); ok {
			return mode, true
		}
	}
	return "", false
}

func parseModeChangedMarkerMode(text string) (string, bool) {
	const prefix = "The active session mode is now "
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		rest := strings.TrimPrefix(line, prefix)
		mode, _, ok := strings.Cut(rest, ",")
		if !ok {
			mode = rest
		}
		mode = strings.TrimSpace(mode)
		if mode == "" {
			return "", false
		}
		return mode, true
	}
	return "", false
}
