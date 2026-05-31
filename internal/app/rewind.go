package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/checkpoint"
	"github.com/usewhale/whale/internal/core"
)

func isRewindCommand(line string) bool {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return false
	}
	return fields[0] == "/rewind" || fields[0] == "/checkpoint"
}

func (a *App) executeRewindCommand(line string) (CommandExecution, error) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 || !isRewindCommand(line) {
		return CommandExecution{}, nil
	}
	if len(fields) == 1 {
		result, err := a.buildRewindListLocalResult()
		if err != nil {
			return CommandExecution{Handled: true}, err
		}
		return CommandExecution{Handled: true, Text: result.PlainText, LocalResult: result}, nil
	}
	return CommandExecution{Handled: true}, fmt.Errorf("usage: /rewind")
}

func (a *App) buildRewindListLocalResult() (*LocalResult, error) {
	users, err := a.ListRewindMessages(a.ctx)
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		text := "No visible user messages to rewind."
		return &LocalResult{Kind: "rewind", Title: "Rewind", PlainText: text}, nil
	}
	const limit = 12
	start := 0
	if len(users) > limit {
		start = len(users) - limit
	}
	fields := make([]LocalResultField, 0, len(users)-start+1)
	for _, msg := range users[start:] {
		label := msg.ID
		if a.checkpoints != nil && a.checkpoints.CanRestore(a.sessionID, msg.ID) {
			label += " (code)"
		}
		fields = append(fields, LocalResultField{Label: label, Value: singleLinePreview(msg.Text, 120)})
	}
	text := "Rewind target messages\n\n"
	for _, field := range fields {
		text += fmt.Sprintf("%s  %s\n", field.Label, field.Value)
	}
	text += "\nUse /rewind in the TUI to choose a message."
	return &LocalResult{
		Kind:      "rewind",
		Title:     "Rewind",
		Fields:    fields,
		PlainText: strings.TrimRight(text, "\n"),
	}, nil
}

func (a *App) ListRewindMessages(ctx context.Context) ([]core.Message, error) {
	msgs, err := a.msgStore.List(ctx, a.sessionID)
	if err != nil {
		return nil, err
	}
	return visibleUserMessages(msgs), nil
}

func (a *App) RewindToMessage(ctx context.Context, messageID string) (string, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return "", fmt.Errorf("usage: /rewind")
	}
	msgs, err := a.msgStore.List(ctx, a.sessionID)
	if err != nil {
		return "", err
	}
	idx := -1
	for i, msg := range msgs {
		if msg.ID == messageID && msg.Role == core.RoleUser && !msg.Hidden {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", fmt.Errorf("visible user message %q not found", messageID)
	}
	restoreInput := msgs[idx].Text

	if a.checkpoints != nil {
		_, err := a.checkpoints.Restore(a.sessionID, messageID)
		if err != nil {
			if !errors.Is(err, checkpoint.ErrNoCheckpoint) {
				return "", err
			}
		}
	}
	kept := append([]core.Message(nil), msgs[:idx]...)
	if rewriter, ok := any(a.msgStore).(interface {
		RewriteSession(context.Context, string, []core.Message) error
	}); ok {
		if err := rewriter.RewriteSession(ctx, a.sessionID, kept); err != nil {
			return "", err
		}
	} else {
		return "", fmt.Errorf("session store does not support rewrite")
	}
	a.a = nil

	return restoreInput, nil
}

func visibleUserMessages(msgs []core.Message) []core.Message {
	out := make([]core.Message, 0, len(msgs))
	for _, msg := range msgs {
		if msg.Role == core.RoleUser && !msg.Hidden {
			out = append(out, msg)
		}
	}
	return out
}

func singleLinePreview(s string, limit int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if limit <= 0 || len(s) <= limit {
		return s
	}
	if limit <= 3 {
		return s[:limit]
	}
	return s[:limit-3] + "..."
}
