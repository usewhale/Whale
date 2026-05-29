package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/memory"
	"github.com/usewhale/whale/internal/session"
)

func (a *Agent) collectAssistantStream(ctx context.Context, sessionID string, rt *memory.RuntimeState, events chan<- AgentEvent, tools *core.ToolRegistry) (core.Message, llm.Usage, string, error) {
	assistant, err := a.store.Create(ctx, core.Message{SessionID: sessionID, Role: core.RoleAssistant})
	if err != nil {
		return core.Message{}, llm.Usage{}, "", fmt.Errorf("create assistant message: %w", err)
	}
	lastUsage := llm.Usage{}
	lastModel := ""
	var ps planState
	assistantDeltaSeen := false
	streamPersisted := false
	emit := func(ev AgentEvent) bool {
		return sendAgentEvent(ctx, events, ev)
	}

	ch := a.provider.StreamResponse(ctx, a.buildTurnProviderHistory(sessionID, rt), tools.Tools())
	for ev := range ch {
		switch ev.Type {
		case llm.EventContentDelta:
			assistant.Text += ev.Content
			assistantDeltaSeen = true
			if !a.emitAssistantContentDelta(ctx, ev.Content, &ps, events) {
				return core.Message{}, llm.Usage{}, "", ctx.Err()
			}
			// Intentionally do not persist on every delta: rewriting the full
			// session JSONL per token produced O(n·m) disk I/O and caused TUI
			// stutter in long-context sessions (issue #22). The accumulated
			// state is flushed by the EventToolUseStart / EventComplete /
			// bestEffortUpdateAssistant paths below.
		case llm.EventReasoningDelta:
			assistant.Reasoning += ev.ReasoningDelta
			rt.Scratch.Reasoning += ev.ReasoningDelta
			if !emit(AgentEvent{Type: AgentEventTypeReasoningDelta, ReasoningDelta: ev.ReasoningDelta}) {
				return core.Message{}, llm.Usage{}, "", ctx.Err()
			}
		case llm.EventToolArgsDelta:
			if ev.ToolArgsDelta != nil {
				rt.Scratch.UpdateToolArgs(ev.ToolArgsDelta.ToolCallIndex, ev.ToolArgsDelta.ToolName, ev.ToolArgsDelta.ArgsChars, ev.ToolArgsDelta.ReadyCount)
				if !emit(AgentEvent{
					Type: AgentEventTypeToolArgsDelta,
					ToolArgs: &ToolArgsProgress{
						ToolCallIndex: ev.ToolArgsDelta.ToolCallIndex,
						ToolName:      ev.ToolArgsDelta.ToolName,
						ArgsChars:     ev.ToolArgsDelta.ArgsChars,
						ReadyCount:    ev.ToolArgsDelta.ReadyCount,
					},
				}) {
					return core.Message{}, llm.Usage{}, "", ctx.Err()
				}
			}
		case llm.EventToolUseStart:
			if ev.ToolCall != nil {
				assistant.ToolCalls = append(assistant.ToolCalls, *ev.ToolCall)
				if err := a.store.Update(ctx, assistant); err != nil {
					return core.Message{}, llm.Usage{}, "", err
				}
			}
		case llm.EventToolUseStop:
			// no-op in this minimal version
		case llm.EventRetryScheduled:
			if ev.Retry != nil {
				info := *ev.Retry
				if info.StreamReset {
					assistant.Text = ""
					assistant.Reasoning = ""
					assistant.ToolCalls = nil
					assistant.FinishReason = ""
					rt.Scratch.ResetTurn()
					ps = planState{}
					assistantDeltaSeen = false
					streamPersisted = false
					if err := a.store.Update(ctx, assistant); err != nil {
						return core.Message{}, llm.Usage{}, "", err
					}
				}
				if !emit(AgentEvent{Type: AgentEventTypeProviderRetryScheduled, ProviderRetry: &info}) {
					return core.Message{}, llm.Usage{}, "", ctx.Err()
				}
			}
		case llm.EventComplete:
			if ev.Response != nil {
				lastUsage = ev.Response.Usage
				lastModel = ev.Response.Model
				assistant.FinishReason = ev.Response.FinishReason
				if ev.Response.Reasoning != "" {
					assistant.Reasoning = ev.Response.Reasoning
				} else if strings.TrimSpace(rt.Scratch.Reasoning) != "" {
					assistant.Reasoning = rt.Scratch.Reasoning
				}
				if len(ev.Response.ToolCalls) > 0 {
					assistant.ToolCalls = ev.Response.ToolCalls
					// Emit tool call events now that Input is fully populated.
					for i := range ev.Response.ToolCalls {
						tc := ev.Response.ToolCalls[i]
						if !emit(AgentEvent{Type: AgentEventTypeToolCall, ToolCall: &tc}) {
							return core.Message{}, llm.Usage{}, "", ctx.Err()
						}
						if taskEvent, ok := taskStartedEvent(tc); ok {
							if !emit(taskEvent) {
								return core.Message{}, llm.Usage{}, "", ctx.Err()
							}
						}
					}
				}
				if ev.Response.Content != "" {
					assistant.Text = ev.Response.Content
					if !assistantDeltaSeen {
						if !a.emitAssistantContentDelta(ctx, ev.Response.Content, &ps, events) {
							return core.Message{}, llm.Usage{}, "", ctx.Err()
						}
					} else if a.mode == session.ModePlan && !ps.completed {
						if !a.emitFinalProposedPlan(ctx, ev.Response.Content, &ps, events) {
							return core.Message{}, llm.Usage{}, "", ctx.Err()
						}
					}
				}
				if err := a.store.Update(ctx, assistant); err != nil {
					return core.Message{}, llm.Usage{}, "", err
				}
				streamPersisted = true
			}
		case llm.EventError:
			if ev.Err != nil {
				assistant.FinishReason = core.FinishReasonError
				a.bestEffortUpdateAssistant(assistant)
				return core.Message{}, llm.Usage{}, "", ev.Err
			}
		}
	}
	// Provider channel can close without a terminal EventComplete/EventError
	// (e.g. SSE EOF before [DONE], or EventComplete with nil Response). Since
	// deltas no longer persist, flush any accumulated assistant state so
	// resume/history doesn't see an empty assistant message.
	if !streamPersisted && (assistant.Text != "" || assistant.Reasoning != "" || len(assistant.ToolCalls) > 0) {
		a.bestEffortUpdateAssistant(assistant)
	}
	if a.mode == session.ModePlan && !ps.completed {
		for _, seg := range ps.parser.Finish() {
			if !a.emitProposedPlanSegment(ctx, seg, &ps, events) {
				return core.Message{}, llm.Usage{}, "", ctx.Err()
			}
		}
	}
	return assistant, lastUsage, lastModel, nil
}
