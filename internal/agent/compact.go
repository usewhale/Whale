package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/compact"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/memory"
	"github.com/usewhale/whale/internal/store"
)

const compactTailBudgetDivisor = 4

type summaryRequestContext struct {
	systemBlocks      []string
	runtimeBlocks     []string
	prefixFingerprint string
}

func (a *Agent) CompactSession(ctx context.Context, sessionID string) (CompactInfo, error) {
	history, err := a.store.List(ctx, sessionID)
	if err != nil {
		return CompactInfo{}, fmt.Errorf("list messages: %w", err)
	}
	reqCtx, err := a.buildSummaryRequestContext(ctx, RunOptions{})
	if err != nil {
		return CompactInfo{}, err
	}
	_, info, err := a.compactHistory(ctx, sessionID, history, false, nil, reqCtx)
	return info, err
}

func (a *Agent) compactHistory(ctx context.Context, sessionID string, history []core.Message, auto bool, observer HookRunObserver, reqCtx summaryRequestContext) ([]core.Message, CompactInfo, error) {
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
	head, tail := a.splitCompactHistory(history)
	summary, err := a.generateCompactSummary(ctx, sessionID, head, preContext, reqCtx)
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
	replacement := make([]core.Message, 0, 1+len(tail))
	replacement = append(replacement, summaryMsg)
	replacement = append(replacement, tail...)
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

func (a *Agent) generateCompactSummary(ctx context.Context, sessionID string, history []core.Message, hookContext string, reqCtx summaryRequestContext) (string, error) {
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
	tmpHistory := buildSummaryProviderHistory(sessionID, reqCtx, history, prompt)
	var toolList []core.Tool
	ch := a.provider.StreamResponse(ctx, tmpHistory, toolList)
	var summary strings.Builder
	lastUsage := llm.Usage{}
	lastModel := ""
	for ev := range ch {
		switch ev.Type {
		case llm.EventContentDelta:
			summary.WriteString(ev.Content)
		case llm.EventComplete:
			if ev.Response != nil {
				lastUsage = ev.Response.Usage
				lastModel = ev.Response.Model
				if strings.TrimSpace(ev.Response.Content) != "" {
					summary.Reset()
					summary.WriteString(ev.Response.Content)
				}
			}
		case llm.EventError:
			if ev.Err != nil {
				return "", ev.Err
			}
		}
	}
	a.recordTurnCost(sessionID, lastUsage, lastModel, reqCtx.prefixFingerprint, buildCacheShapeForRequestWithRuntime(cacheShapeRequestCompact, tmpHistory, toolList, "", reqCtx.systemBlocks, reqCtx.runtimeBlocks))
	out := strings.TrimSpace(summary.String())
	if out == "" {
		return "", errors.New("compact summary was empty")
	}
	return out, nil
}

func (a *Agent) buildSummaryRequestContext(_ context.Context, opts RunOptions) (summaryRequestContext, error) {
	prefix := memory.NewImmutablePrefix(a.buildImmutableSystemBlocks(opts))
	return summaryRequestContextFromPrefix(prefix, a.buildRuntimeSystemBlocks(opts)), nil
}

func summaryRequestContextFromPrefix(prefix *memory.ImmutablePrefix, runtimeBlocks []string) summaryRequestContext {
	if prefix == nil {
		prefix = memory.NewImmutablePrefix(nil)
	}
	return summaryRequestContext{
		systemBlocks:      prefix.SystemBlocks(),
		runtimeBlocks:     append([]string(nil), runtimeBlocks...),
		prefixFingerprint: prefix.Fingerprint(),
	}
}

func buildSummaryProviderHistory(sessionID string, reqCtx summaryRequestContext, history []core.Message, prompt string) []core.Message {
	prefix := memory.NewImmutablePrefix(reqCtx.systemBlocks)
	out := prefix.ToMessages()
	if len(reqCtx.runtimeBlocks) > 0 {
		out = append(out, core.Message{Role: core.RoleSystem, Text: strings.Join(reqCtx.runtimeBlocks, "\n\n")})
	}
	out = append(out, append([]core.Message(nil), history...)...)
	out = append(out, core.Message{SessionID: sessionID, Role: core.RoleUser, Text: prompt})
	return out
}

func (a *Agent) splitCompactHistory(history []core.Message) ([]core.Message, []core.Message) {
	if len(history) <= 2 {
		return append([]core.Message(nil), history...), nil
	}
	tailBudget := a.compactTailTokenBudget()
	if tailBudget <= 0 {
		return append([]core.Message(nil), history...), nil
	}
	tailStart := len(history)
	tailTokens := 0
	for i := len(history) - 1; i >= 0; i-- {
		msgTokens := compact.EstimateMessagesTokens(history[i : i+1])
		if tailStart < len(history) && tailTokens+msgTokens > tailBudget {
			break
		}
		tailStart = i
		tailTokens += msgTokens
	}
	for tailStart > 0 && tailStart < len(history) && history[tailStart].Role == core.RoleTool {
		tailStart--
	}
	if tailStart <= 0 || tailStart >= len(history) {
		return append([]core.Message(nil), history...), nil
	}
	head := append([]core.Message(nil), history[:tailStart]...)
	tail := append([]core.Message(nil), history[tailStart:]...)
	return head, tail
}

func (a *Agent) compactTailTokenBudget() int {
	if a == nil || a.contextWindow <= 0 {
		return 0
	}
	budget := a.contextWindow / compactTailBudgetDivisor
	if budget < 1 {
		return 1
	}
	return budget
}
