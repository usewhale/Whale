package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/usewhale/whale/internal/compact"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/memory"
	"github.com/usewhale/whale/internal/policy"
)

type RunOptions struct {
	HiddenInput        bool
	ReadOnly           bool
	GoalContinuation   bool
	ShellAllowPrefixes []string
	ViewMode           string
	AssistantPrefix    string
	PrefixCompletion   bool
}

func (a *Agent) RunStreamWithOptions(ctx context.Context, sessionID, input string, hiddenInput bool) (<-chan AgentEvent, error) {
	return a.RunStreamWithTurnOptions(ctx, sessionID, input, RunOptions{HiddenInput: hiddenInput})
}

func (a *Agent) RunStreamWithTurnOptions(ctx context.Context, sessionID, input string, opts RunOptions) (<-chan AgentEvent, error) {
	return a.RunStreamWithContentOptions(ctx, sessionID, []core.MessagePart{{Type: core.MessagePartText, Text: input}}, opts)
}

func (a *Agent) RunStreamWithContentOptions(ctx context.Context, sessionID string, parts []core.MessagePart, opts RunOptions) (<-chan AgentEvent, error) {
	return a.runStreamWithNewMessages(ctx, sessionID, []core.Message{
		core.UserMessageFromParts(sessionID, parts, opts.HiddenInput),
	}, opts)
}

func (a *Agent) RunStreamWithInjectedInput(ctx context.Context, sessionID, visibleInput, hiddenInput string) (<-chan AgentEvent, error) {
	return a.RunStreamWithInjectedInputOptions(ctx, sessionID, visibleInput, hiddenInput, RunOptions{})
}

func (a *Agent) RunStreamWithInjectedInputOptions(ctx context.Context, sessionID, visibleInput, hiddenInput string, opts RunOptions) (<-chan AgentEvent, error) {
	return a.RunStreamWithInjectedContentOptions(ctx, sessionID, []core.MessagePart{{Type: core.MessagePartText, Text: visibleInput}}, hiddenInput, opts)
}

func (a *Agent) RunStreamWithInjectedContentOptions(ctx context.Context, sessionID string, visibleParts []core.MessagePart, hiddenInput string, opts RunOptions) (<-chan AgentEvent, error) {
	return a.runStreamWithNewMessages(ctx, sessionID, []core.Message{
		core.UserMessageFromParts(sessionID, visibleParts, false),
		core.TextMessage(sessionID, core.RoleUser, hiddenInput, true),
	}, opts)
}

func (a *Agent) InjectTurnInput(ctx context.Context, sessionID string, newMessages []core.Message) (bool, error) {
	state, ok := a.active.Load(sessionID)
	if !ok {
		return false, nil
	}
	turnState, ok := state.(*activeTurnState)
	if !ok {
		return false, nil
	}
	createdMessages := make([]core.Message, 0, len(newMessages))
	for _, msg := range newMessages {
		msg.SessionID = sessionID
		created, err := a.store.Create(ctx, msg)
		if err != nil {
			return true, fmt.Errorf("create injected user message: %w", err)
		}
		createdMessages = append(createdMessages, created)
	}
	if checkpointMessageID := firstVisibleMessageID(createdMessages); checkpointMessageID != "" && a.checkpoints != nil {
		if err := a.checkpoints.CreateSnapshot(sessionID, checkpointMessageID); err != nil {
			return true, fmt.Errorf("create injected checkpoint: %w", err)
		}
	}
	turnState.appendPending(createdMessages)
	return true, nil
}

func (a *Agent) runStreamWithNewMessages(ctx context.Context, sessionID string, newMessages []core.Message, opts RunOptions) (<-chan AgentEvent, error) {
	turnState := &activeTurnState{}
	if _, loaded := a.active.LoadOrStore(sessionID, turnState); loaded {
		return nil, ErrSessionBusy
	}
	if spent, blocked := a.budgetExceeded(sessionID); blocked {
		a.active.Delete(sessionID)
		return nil, fmt.Errorf("%w: spent $%.6f >= cap $%.6f", ErrBudgetExceeded, spent, a.budgetWarningUSD)
	}

	createdMessages := make([]core.Message, 0, len(newMessages))
	for _, msg := range newMessages {
		msg.SessionID = sessionID
		msg = core.NormalizeMessageContent(msg)
		created, err := a.store.Create(ctx, msg)
		if err != nil {
			a.active.Delete(sessionID)
			return nil, fmt.Errorf("create user message: %w", err)
		}
		createdMessages = append(createdMessages, created)
	}
	checkpointMessageID := firstVisibleMessageID(createdMessages)
	if checkpointMessageID != "" && a.checkpoints != nil {
		if err := a.checkpoints.CreateSnapshot(sessionID, checkpointMessageID); err != nil {
			a.active.Delete(sessionID)
			return nil, fmt.Errorf("create checkpoint: %w", err)
		}
	}

	history, err := a.store.List(ctx, sessionID)
	if err != nil {
		a.active.Delete(sessionID)
		return nil, fmt.Errorf("list messages: %w", err)
	}

	out := make(chan AgentEvent, 16)
	go func() {
		defer close(out)
		defer a.active.Delete(sessionID)
		toolSnapshot, err := a.refreshToolSnapshot(ctx)
		if err != nil {
			emit := func(ev AgentEvent) bool {
				return sendAgentEvent(ctx, out, ev)
			}
			emit(AgentEvent{Type: AgentEventTypeError, Err: err})
			return
		}
		rt := memory.HydrateRuntime(memory.NewImmutablePrefix(a.buildImmutableSystemBlocksWithTools(toolSnapshot, opts)), history)
		rt.SetRuntimeBlocks(a.buildRuntimeSystemBlocks(opts))
		modelTurns := 0
		toolIters := 0
		toolCalls := 0
		if a.repairer != nil {
			a.repairer.resetStorm()
		}
		emit := func(ev AgentEvent) bool {
			return sendAgentEvent(ctx, out, ev)
		}
		turnPolicy := a.policy
		if len(opts.ShellAllowPrefixes) > 0 {
			turnPolicy = policy.ScopedAllowPolicy{
				Base:               a.policy,
				ShellAllowPrefixes: append([]string(nil), opts.ShellAllowPrefixes...),
			}
		}
		if opts.ReadOnly {
			turnPolicy = policy.ReadOnlyTurnPolicy{Base: turnPolicy}
		}
		firstRequest := true
		for {
			if pending := turnState.drainPending(); len(pending) > 0 {
				for _, msg := range pending {
					rt.Log.Append(msg)
					history = append(history, msg)
				}
			}
			rt.Scratch.ResetTurn()
			if !firstRequest {
				var err error
				toolSnapshot, err = a.refreshToolSnapshot(ctx)
				if err != nil {
					emit(AgentEvent{Type: AgentEventTypeError, Err: err})
					return
				}
			}
			firstRequest = false
			rt.SetRuntimeBlocks(a.buildRuntimeSystemBlocks(opts))
			if a.autoCompact {
				before := compact.EstimateMessagesTokens(rt.BuildProviderHistory())
				if float64(before)/float64(max(1, a.contextWindow)) > a.compactThresh {
					summaryCtx := summaryRequestContextFromPrefix(rt.Prefix, rt.RuntimeBlocks())
					replacement, info, err := a.compactHistory(ctx, sessionID, history, true, a.hookRunObserver(ctx, out), summaryCtx)
					if err != nil {
						emit(AgentEvent{Type: AgentEventTypeError, Err: err})
						return
					}
					if info.Compacted {
						_ = rt.Log.RewriteWithReason(memory.RewriteReasonCompact, replacement)
						history = replacement
						info.BeforeEstimate = before
						info.AfterEstimate = compact.EstimateMessagesTokens(rt.BuildProviderHistory())
						if !emit(AgentEvent{
							Type:    AgentEventTypeContextCompacted,
							Compact: &info,
						}) {
							return
						}
					} else {
						info.BeforeEstimate = before
						info.AfterEstimate = before
						if !emit(AgentEvent{
							Type:    AgentEventTypeContextCompacted,
							Compact: &info,
						}) {
							return
						}
					}
				}
			}
			remainingToolCalls := 0
			if a.maxToolCalls > 0 {
				remainingToolCalls = a.maxToolCalls - toolCalls
			}
			if actual, ok := rt.Prefix.VerifyFingerprint(); !ok {
				if !emit(AgentEvent{Type: AgentEventTypePrefixDrift, PrefixDrift: &PrefixDriftInfo{Expected: rt.Prefix.Fingerprint(), Actual: actual}}) {
					return
				}
			}
			assistant, toolMsg, usage, modelName, cacheShape, abortTurn, attemptedToolCalls, sErr := a.streamAndHandle(ctx, sessionID, checkpointMessageID, history, rt, out, turnPolicy, toolSnapshot, remainingToolCalls, opts)
			if sErr != nil {
				if errors.Is(sErr, context.Canceled) || errors.Is(sErr, context.DeadlineExceeded) {
					a.persistInterruptedTurnMarker(sessionID)
					emit(AgentEvent{Type: AgentEventTypeTurnCancelled, Content: "turn cancelled"})
					return
				}
				emit(AgentEvent{Type: AgentEventTypeError, Err: sErr})
				return
			}
			modelTurns++
			turnCost := a.recordTurnCost(sessionID, usage, modelName, rt.Prefix.Fingerprint(), cacheShape)
			if !emit(AgentEvent{Type: AgentEventTypeUsage, Usage: &UsageInfo{Model: modelName, Usage: usage}}) {
				return
			}
			if m := buildPrefixCacheMetrics(modelName, usage, rt.Prefix.Fingerprint(), cacheShape); m != nil {
				if !emit(AgentEvent{Type: AgentEventTypePrefixCacheMetrics, CacheMetrics: m}) {
					return
				}
			}
			if !a.emitBudgetWarningIfNeeded(ctx, sessionID, turnCost, out) {
				return
			}
			if abortTurn {
				if toolMsg != nil {
					rt.Log.Append(assistant)
					rt.Log.Append(*toolMsg)
					history = append(history, assistant, *toolMsg)
				}
				if turnState.hasPending() {
					if !emit(AgentEvent{Type: AgentEventTypeResponseReset}) {
						return
					}
					continue
				}
				done := assistant
				done.FinishReason = core.FinishReasonEndTurn
				emit(AgentEvent{Type: AgentEventTypeDone, Message: &done})
				return
			}
			if assistant.FinishReason == core.FinishReasonToolUse && toolMsg != nil {
				toolIters++
				toolCalls += attemptedToolCalls
				rt.Log.Append(assistant)
				rt.Log.Append(*toolMsg)
				history = append(history, assistant, *toolMsg)
				if a.maxTurns > 0 && modelTurns >= a.maxTurns {
					if !emit(AgentEvent{Type: AgentEventTypeForcedSummaryStarted, Content: "turn cap reached"}) {
						return
					}
					sum, serr := a.forceSummary(ctx, sessionID, history, "turn cap reached", summaryRequestContextFromPrefix(rt.Prefix, rt.RuntimeBlocks()))
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
					return
				}
				if a.maxToolCalls > 0 && toolCalls >= a.maxToolCalls {
					if !emit(AgentEvent{Type: AgentEventTypeForcedSummaryStarted, Content: "tool call cap reached"}) {
						return
					}
					sum, serr := a.forceSummary(ctx, sessionID, history, "tool call cap reached", summaryRequestContextFromPrefix(rt.Prefix, rt.RuntimeBlocks()))
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
					return
				}
				if a.maxToolIters > 0 && toolIters >= a.maxToolIters {
					if !emit(AgentEvent{Type: AgentEventTypeForcedSummaryStarted, Content: "tool iteration cap reached"}) {
						return
					}
					sum, serr := a.forceSummary(ctx, sessionID, history, "tool iteration cap reached", summaryRequestContextFromPrefix(rt.Prefix, rt.RuntimeBlocks()))
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
					return
				}
				if turnState.hasPending() {
					if !emit(AgentEvent{Type: AgentEventTypeResponseReset}) {
						return
					}
				}
				continue
			}
			if turnState.hasPending() {
				rt.Log.Append(assistant)
				history = append(history, assistant)
				if !emit(AgentEvent{Type: AgentEventTypeResponseReset}) {
					return
				}
				continue
			}
			emit(AgentEvent{Type: AgentEventTypeDone, Message: &assistant})
			return
		}
	}()

	return out, nil
}

func firstVisibleMessageID(msgs []core.Message) string {
	for _, msg := range msgs {
		if !msg.Hidden && msg.ID != "" {
			return msg.ID
		}
	}
	for _, msg := range msgs {
		if msg.ID != "" {
			return msg.ID
		}
	}
	return ""
}
