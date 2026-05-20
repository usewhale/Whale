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
	ShellAllowPrefixes []string
}

func (a *Agent) RunStreamWithOptions(ctx context.Context, sessionID, input string, hiddenInput bool) (<-chan AgentEvent, error) {
	return a.RunStreamWithTurnOptions(ctx, sessionID, input, RunOptions{HiddenInput: hiddenInput})
}

func (a *Agent) RunStreamWithTurnOptions(ctx context.Context, sessionID, input string, opts RunOptions) (<-chan AgentEvent, error) {
	return a.runStreamWithNewMessages(ctx, sessionID, []core.Message{{
		SessionID: sessionID,
		Role:      core.RoleUser,
		Text:      input,
		Hidden:    opts.HiddenInput,
	}}, opts)
}

func (a *Agent) RunStreamWithInjectedInput(ctx context.Context, sessionID, visibleInput, hiddenInput string) (<-chan AgentEvent, error) {
	return a.RunStreamWithInjectedInputOptions(ctx, sessionID, visibleInput, hiddenInput, RunOptions{})
}

func (a *Agent) RunStreamWithInjectedInputOptions(ctx context.Context, sessionID, visibleInput, hiddenInput string, opts RunOptions) (<-chan AgentEvent, error) {
	return a.runStreamWithNewMessages(ctx, sessionID, []core.Message{
		{SessionID: sessionID, Role: core.RoleUser, Text: visibleInput},
		{SessionID: sessionID, Role: core.RoleUser, Text: hiddenInput, Hidden: true},
	}, opts)
}

func (a *Agent) runStreamWithNewMessages(ctx context.Context, sessionID string, newMessages []core.Message, opts RunOptions) (<-chan AgentEvent, error) {
	if _, loaded := a.active.LoadOrStore(sessionID, struct{}{}); loaded {
		return nil, ErrSessionBusy
	}
	if spent, blocked := a.budgetExceeded(sessionID); blocked {
		a.active.Delete(sessionID)
		return nil, fmt.Errorf("%w: spent $%.6f >= cap $%.6f", ErrBudgetExceeded, spent, a.budgetWarningUSD)
	}

	for _, msg := range newMessages {
		msg.SessionID = sessionID
		if _, err := a.store.Create(ctx, msg); err != nil {
			a.active.Delete(sessionID)
			return nil, fmt.Errorf("create user message: %w", err)
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
		rt := memory.HydrateRuntime(memory.NewImmutablePrefix(a.buildImmutableSystemBlocks()), history)
		expectedPrefixFingerprint := rt.Prefix.Fingerprint()
		toolIters := 0
		if a.repairer != nil {
			a.repairer.resetStorm()
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
		for {
			rt.Scratch.ResetTurn()
			rt.Prefix.Refresh(a.buildImmutableSystemBlocks())
			if got := rt.Prefix.Fingerprint(); got != expectedPrefixFingerprint {
				out <- AgentEvent{
					Type: AgentEventTypePrefixDrift,
					PrefixDrift: &PrefixDriftInfo{
						Expected: expectedPrefixFingerprint,
						Actual:   got,
					},
				}
				expectedPrefixFingerprint = got
			}
			if a.autoCompact {
				before := compact.EstimateMessagesTokens(rt.BuildProviderHistory())
				if float64(before)/float64(max(1, a.contextWindow)) > a.compactThresh {
					replacement, info, err := a.compactHistory(ctx, sessionID, history, true)
					if err != nil {
						out <- AgentEvent{Type: AgentEventTypeError, Err: err}
						return
					}
					if info.Compacted {
						_ = rt.Log.RewriteWithReason(memory.RewriteReasonCompact, replacement)
						history = replacement
						info.BeforeEstimate = before
						info.AfterEstimate = compact.EstimateMessagesTokens(rt.BuildProviderHistory())
						out <- AgentEvent{
							Type:    AgentEventTypeContextCompacted,
							Compact: &info,
						}
					} else {
						info.BeforeEstimate = before
						info.AfterEstimate = before
						out <- AgentEvent{
							Type:    AgentEventTypeContextCompacted,
							Compact: &info,
						}
					}
				}
			}
			assistant, toolMsg, usage, modelName, abortTurn, sErr := a.streamAndHandle(ctx, sessionID, history, rt, out, turnPolicy)
			if sErr != nil {
				if errors.Is(sErr, context.Canceled) || errors.Is(sErr, context.DeadlineExceeded) {
					a.persistInterruptedTurnMarker(sessionID)
					out <- AgentEvent{Type: AgentEventTypeTurnCancelled, Content: "turn cancelled"}
					return
				}
				out <- AgentEvent{Type: AgentEventTypeError, Err: sErr}
				return
			}
			turnCost := a.recordTurnCost(sessionID, usage, modelName, rt.Prefix.Fingerprint())
			if m := buildPrefixCacheMetrics(modelName, usage, rt.Prefix.Fingerprint()); m != nil {
				out <- AgentEvent{Type: AgentEventTypePrefixCacheMetrics, CacheMetrics: m}
			}
			a.emitBudgetWarningIfNeeded(sessionID, turnCost, out)
			if abortTurn {
				if toolMsg != nil {
					rt.Log.Append(assistant)
					rt.Log.Append(*toolMsg)
					history = append(history, assistant, *toolMsg)
				}
				done := assistant
				done.FinishReason = core.FinishReasonEndTurn
				out <- AgentEvent{Type: AgentEventTypeDone, Message: &done}
				return
			}
			if assistant.FinishReason == core.FinishReasonToolUse && toolMsg != nil {
				toolIters++
				rt.Log.Append(assistant)
				rt.Log.Append(*toolMsg)
				history = append(history, assistant, *toolMsg)
				if a.maxToolIters > 0 && toolIters >= a.maxToolIters {
					out <- AgentEvent{Type: AgentEventTypeForcedSummaryStarted, Content: "tool iteration cap reached"}
					sum, serr := a.forceSummary(ctx, sessionID, history, "tool iteration cap reached")
					if serr != nil {
						out <- AgentEvent{Type: AgentEventTypeForcedSummaryFailed, Content: serr.Error()}
						out <- AgentEvent{Type: AgentEventTypeError, Err: serr}
						return
					}
					out <- AgentEvent{Type: AgentEventTypeForcedSummaryDone, Content: "forced summary completed"}
					out <- AgentEvent{Type: AgentEventTypeDone, Message: &sum}
					return
				}
				continue
			}
			out <- AgentEvent{Type: AgentEventTypeDone, Message: &assistant}
			return
		}
	}()

	return out, nil
}
