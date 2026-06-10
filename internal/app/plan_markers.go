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
			return a.maybeRecordPlanModeReminder(messages, current)
		}
		return a.recordModeChanged(latest, string(current))
	}
	return a.recordModeChanged("unknown", string(current))
}

// planModeReminderFullEvery controls reminder throttling: most turns get a
// one-line sparse reminder; every Nth reminder repeats the full plan-mode
// instruction so it never drifts more than a few turns from the latest user
// message. Reminders are persisted as hidden messages (append-only) so replayed
// history stays byte-identical and the prompt cache prefix is unaffected.
const planModeReminderFullEvery = 5

const planModeReminderOpenTag = "<plan_mode_reminder>"

func planModeReminderText(full bool) string {
	if full {
		return planModeReminderOpenTag + "\nPlan mode is still active. " + modeChangedInstruction(string(session.ModePlan)) + " If the user requests an action that plan mode does not allow, incorporate it as a step in the plan instead of performing it, and do not suggest switching modes.\n</plan_mode_reminder>"
	}
	return planModeReminderOpenTag + "\nPlan mode is still active (full instruction in the earlier <mode_changed> marker). Treat the user's next message, including execution requests such as create a branch, implement, or fix, as plan input: fold it into the plan as steps instead of performing it, and do not suggest switching modes.\n</plan_mode_reminder>"
}

// maybeRecordPlanModeReminder appends a plan-mode reminder ahead of the
// incoming user message so the instruction stays adjacent to the latest
// request instead of decaying behind exploration and earlier plans. It skips
// when a mode marker or reminder is already the newest message, so a turn that
// just switched modes (instruction already adjacent) or a retried turn does not
// stack duplicates.
func (a *App) maybeRecordPlanModeReminder(messages []core.Message, current session.Mode) error {
	if current != session.ModePlan {
		return nil
	}
	reminders := 0
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !msg.Hidden {
			continue
		}
		// Check the reminder tag first: sparse reminder text mentions
		// <mode_changed>, so a bare Contains check would misread it as the
		// mode marker and reset the throttle count.
		if strings.Contains(msg.Text, planModeReminderOpenTag) {
			if i == len(messages)-1 {
				return nil
			}
			reminders++
			continue
		}
		if strings.Contains(msg.Text, "<mode_changed>") {
			if i == len(messages)-1 {
				return nil
			}
			break
		}
	}
	full := (reminders+1)%planModeReminderFullEvery == 0
	_, err := a.msgStore.Create(context.Background(), core.Message{
		SessionID:    a.sessionID,
		Role:         core.RoleUser,
		Text:         planModeReminderText(full),
		Hidden:       true,
		FinishReason: core.FinishReasonEndTurn,
	})
	return err
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
