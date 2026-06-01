package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/compact"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/store"
)

func (a *Agent) CompactSession(ctx context.Context, sessionID string) (CompactInfo, error) {
	history, err := a.store.List(ctx, sessionID)
	if err != nil {
		return CompactInfo{}, fmt.Errorf("list messages: %w", err)
	}
	_, info, err := a.compactHistory(ctx, sessionID, history, false, nil)
	return info, err
}

func (a *Agent) compactHistory(ctx context.Context, sessionID string, history []core.Message, auto bool, observer HookRunObserver) ([]core.Message, CompactInfo, error) {
	info := CompactInfo{
		Auto:           auto,
		MessagesBefore: len(history),
		MessagesAfter:  len(history),
		BeforeEstimate: compact.EstimateMessagesTokens(history),
		AfterEstimate:  compact.EstimateMessagesTokens(history),
	}
	if len(history) == 0 {
		return append([]core.Message(nil), history...), info, nil
	}
	if isCompactSummaryMessage(history[len(history)-1]) {
		return append([]core.Message(nil), history...), info, nil
	}
	rewriter, ok := a.store.(store.SessionRewriteStore)
	if !ok {
		return nil, info, errors.New("session store does not support compact rewrite")
	}
	preContext := ""
	if a.hooks != nil && !a.hooks.Empty() {
		report := a.hooks.RunHookWithObserver(ctx, NewPreCompactPayload(sessionID, a.workspaceRoot, len(history)), observer)
		if report.Blocked {
			return nil, info, errors.New("blocked by PreCompact hook")
		}
		preContext = strings.TrimSpace(report.AdditionalContext)
	}
	summary, err := a.generateCompactSummary(ctx, sessionID, history, preContext)
	if err != nil {
		return nil, info, err
	}
	summaryMsg, err := a.store.Create(ctx, core.Message{
		SessionID:    sessionID,
		Role:         core.RoleUser,
		Text:         summary,
		FinishReason: core.FinishReasonEndTurn,
	})
	if err != nil {
		return nil, info, fmt.Errorf("create compact summary: %w", err)
	}
	replacement := []core.Message{summaryMsg}
	if err := rewriter.RewriteSession(ctx, sessionID, replacement); err != nil {
		return nil, info, fmt.Errorf("rewrite compacted session: %w", err)
	}
	info.Compacted = true
	info.MessagesAfter = len(replacement)
	info.AfterEstimate = compact.EstimateMessagesTokens(replacement)
	if a.hooks != nil && !a.hooks.Empty() {
		report := a.hooks.RunHookWithObserver(ctx, NewPostCompactPayload(sessionID, a.workspaceRoot, summary, len(history), len(replacement)), observer)
		if report.Blocked {
			return nil, info, errors.New("blocked by PostCompact hook")
		}
	}
	return replacement, info, nil
}

func isCompactSummaryMessage(msg core.Message) bool {
	return msg.Role == core.RoleUser &&
		msg.FinishReason == core.FinishReasonEndTurn &&
		strings.TrimSpace(msg.Text) == "compact summary"
}

func (a *Agent) generateCompactSummary(ctx context.Context, sessionID string, history []core.Message, hookContext string) (string, error) {
	prompt := strings.TrimSpace(`Summarize the conversation so far so another assistant can continue from the compacted context.

Preserve:
- user goals and decisions
- current task state and next steps
- important files, commands, errors, and test results
- tool results and code changes at a useful level of detail
- constraints, preferences, and open questions

Do not call tools. Output only the summary.`)
	if strings.TrimSpace(hookContext) != "" {
		prompt += "\n\nAdditional context from PreCompact hooks:\n" + strings.TrimSpace(hookContext)
	}
	tmpHistory := append(append([]core.Message(nil), history...), core.Message{SessionID: sessionID, Role: core.RoleUser, Text: prompt})
	ch := a.provider.StreamResponse(ctx, tmpHistory, nil)
	var summary strings.Builder
	for ev := range ch {
		switch ev.Type {
		case llm.EventContentDelta:
			summary.WriteString(ev.Content)
		case llm.EventComplete:
			if ev.Response != nil && strings.TrimSpace(ev.Response.Content) != "" {
				summary.Reset()
				summary.WriteString(ev.Response.Content)
			}
		case llm.EventError:
			if ev.Err != nil {
				return "", ev.Err
			}
		}
	}
	out := strings.TrimSpace(summary.String())
	if out == "" {
		return "", errors.New("compact summary was empty")
	}
	return out, nil
}
