package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/memory"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
)

func (a *Agent) streamAndHandle(ctx context.Context, sessionID string, history []core.Message, rt *memory.RuntimeState, events chan<- AgentEvent) (core.Message, *core.Message, llm.Usage, string, bool, error) {
	assistant, err := a.store.Create(ctx, core.Message{SessionID: sessionID, Role: core.RoleAssistant})
	if err != nil {
		return core.Message{}, nil, llm.Usage{}, "", false, fmt.Errorf("create assistant message: %w", err)
	}
	lastUsage := llm.Usage{}
	lastModel := ""
	var planParser core.ProposedPlanParser
	var planText strings.Builder
	planStarted := false
	planCompleted := false
	assistantDeltaSeen := false
	streamPersisted := false

	ch := a.provider.StreamResponse(ctx, a.buildTurnProviderHistory(sessionID, rt), a.tools.Tools())
	for ev := range ch {
		switch ev.Type {
		case llm.EventContentDelta:
			assistant.Text += ev.Content
			assistantDeltaSeen = true
			a.emitAssistantContentDelta(ev.Content, &planParser, &planText, &planStarted, &planCompleted, events)
			// Intentionally do not persist on every delta: rewriting the full
			// session JSONL per token produced O(n·m) disk I/O and caused TUI
			// stutter in long-context sessions (issue #22). The accumulated
			// state is flushed by the EventToolUseStart / EventComplete /
			// bestEffortUpdateAssistant paths below.
		case llm.EventReasoningDelta:
			assistant.Reasoning += ev.ReasoningDelta
			rt.Scratch.Reasoning += ev.ReasoningDelta
			events <- AgentEvent{Type: AgentEventTypeReasoningDelta, ReasoningDelta: ev.ReasoningDelta}
		case llm.EventToolArgsDelta:
			if ev.ToolArgsDelta != nil {
				rt.Scratch.UpdateToolArgs(ev.ToolArgsDelta.ToolCallIndex, ev.ToolArgsDelta.ToolName, ev.ToolArgsDelta.ArgsChars, ev.ToolArgsDelta.ReadyCount)
				events <- AgentEvent{
					Type: AgentEventTypeToolArgsDelta,
					ToolArgs: &ToolArgsProgress{
						ToolCallIndex: ev.ToolArgsDelta.ToolCallIndex,
						ToolName:      ev.ToolArgsDelta.ToolName,
						ArgsChars:     ev.ToolArgsDelta.ArgsChars,
						ReadyCount:    ev.ToolArgsDelta.ReadyCount,
					},
				}
			}
		case llm.EventToolUseStart:
			if ev.ToolCall != nil {
				assistant.ToolCalls = append(assistant.ToolCalls, *ev.ToolCall)
				if err := a.store.Update(ctx, assistant); err != nil {
					return core.Message{}, nil, llm.Usage{}, "", false, err
				}
			}
		case llm.EventToolUseStop:
			// no-op in this minimal version
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
						events <- AgentEvent{Type: AgentEventTypeToolCall, ToolCall: &tc}
						if taskEvent, ok := taskStartedEvent(tc); ok {
							events <- taskEvent
						}
					}
				}
				if ev.Response.Content != "" {
					assistant.Text = ev.Response.Content
					if !assistantDeltaSeen {
						a.emitAssistantContentDelta(ev.Response.Content, &planParser, &planText, &planStarted, &planCompleted, events)
					} else if a.mode == session.ModePlan && !planCompleted {
						a.emitFinalProposedPlan(ev.Response.Content, &planText, &planStarted, &planCompleted, events)
					}
				}
				if err := a.store.Update(ctx, assistant); err != nil {
					return core.Message{}, nil, llm.Usage{}, "", false, err
				}
				streamPersisted = true
			}
		case llm.EventError:
			if ev.Err != nil {
				assistant.FinishReason = core.FinishReasonError
				a.bestEffortUpdateAssistant(assistant)
				return core.Message{}, nil, llm.Usage{}, "", false, ev.Err
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
	if a.mode == session.ModePlan && !planCompleted {
		for _, seg := range planParser.Finish() {
			a.emitProposedPlanSegment(seg, &planText, &planStarted, &planCompleted, events)
		}
	}

	dispatchCalls := assistant.ToolCalls
	blocked := []core.ToolResult{}
	if a.repairer != nil {
		allowed := map[string]bool{}
		for _, spec := range a.tools.Specs() {
			allowed[spec.Name] = true
		}
		isMutating := func(c core.ToolCall) bool {
			spec, ok := a.tools.Spec(c.Name)
			if !ok {
				return true
			}
			return !core.IsReadOnlyToolCall(spec, c)
		}
		var report repairReport
		dispatchCalls, blocked, report = a.repairer.process(assistant.ToolCalls, assistant.Reasoning, assistant.Text, allowed, isMutating)
		if report.scavenged > 0 {
			events <- AgentEvent{Type: AgentEventTypeToolCallScavenged, Scavenged: &ToolCallScavenged{Count: report.scavenged}}
		}
		if report.truncationsFixed > 0 {
			for i := range report.repairedCalls {
				a.recordToolInputRepair(sessionID, lastModel, assistant.ID, report.repairedCalls[i], "truncated_json")
				events <- AgentEvent{
					Type: AgentEventTypeToolArgsRepaired,
					ToolArgsRepair: &ToolArgsRepair{
						ToolCallIndex: i,
						ToolName:      report.repairedCalls[i].Name,
					},
				}
			}
		}
	}
	if len(dispatchCalls) == 0 {
		return assistant, nil, lastUsage, lastModel, false, nil
	}
	results := make([]core.ToolResult, 0, len(assistant.ToolCalls))
	for _, blockedRes := range blocked {
		br := blockedRes
		events <- AgentEvent{
			Type: AgentEventTypeToolCallBlocked,
			ToolBlocked: &ToolCallBlocked{
				ToolCallID: br.ToolCallID,
				ToolName:   br.Name,
				ReasonCode: "storm_blocked",
			},
		}
		results = append(results, br)
		events <- AgentEvent{Type: AgentEventTypeToolResult, Result: &br}
	}
	for i, call := range dispatchCalls {
		if spec, ok := a.tools.Spec(call.Name); ok {
			if fixed, changed := core.RenestFlatInputForSpec(spec, call.Input); changed {
				call.Input = fixed
				a.recordToolInputRepair(sessionID, lastModel, assistant.ID, call, "renest_flat_input")
				events <- AgentEvent{
					Type: AgentEventTypeToolArgsRepaired,
					ToolArgsRepair: &ToolArgsRepair{
						ToolCallIndex: i,
						ToolName:      call.Name,
					},
				}
			}
			if fixed, repairs := core.RepairToolInputForSpec(spec, call.Input); len(repairs) > 0 {
				call.Input = fixed
				for _, repair := range repairs {
					a.recordToolInputRepairDetail(sessionID, lastModel, assistant.ID, call, repair)
				}
				events <- AgentEvent{
					Type: AgentEventTypeToolArgsRepaired,
					ToolArgsRepair: &ToolArgsRepair{
						ToolCallIndex: i,
						ToolName:      call.Name,
					},
				}
			}
		}
		if !a.hooks.Empty() {
			var toolArgs any
			_ = json.Unmarshal([]byte(call.Input), &toolArgs)
			report := a.hooks.Run(ctx, NewPreToolUsePayload(sessionID, call, toolArgs))
			a.emitHookReport(events, report)
			if report.Blocked {
				msg := "blocked by PreToolUse hook"
				if len(report.Outcomes) > 0 {
					last := report.Outcomes[len(report.Outcomes)-1]
					if strings.TrimSpace(last.Stderr) != "" {
						msg = strings.TrimSpace(last.Stderr)
					} else if strings.TrimSpace(last.Stdout) != "" {
						msg = strings.TrimSpace(last.Stdout)
					}
				}
				tr := core.ToolResult{
					ToolCallID: call.ID,
					Name:       call.Name,
					Content:    fmt.Sprintf(`{"success":false,"error":%q,"code":"hook_blocked"}`, msg),
					IsError:    true,
				}
				results = append(results, tr)
				events <- AgentEvent{Type: AgentEventTypeToolResult, Result: &tr}
				continue
			}
		}
		a.ensureApprovalCacheLoaded(ctx, sessionID)
		spec, ok := a.tools.Spec(call.Name)
		if !ok {
			spec = core.ToolSpec{Name: call.Name}
		}
		if (a.mode == session.ModePlan || a.mode == session.ModeAsk) && !core.IsReadOnlyToolCall(spec, call) {
			blockedCode, blockedMsg, blockedSummary, blockedData := modeBlockedDetails(a.mode)
			content, err := core.MarshalToolEnvelope(core.ToolEnvelope{
				OK:      false,
				Success: false,
				Code:    blockedCode,
				Error:   blockedMsg,
				Message: blockedMsg,
				Summary: blockedSummary,
				Data:    blockedData,
			})
			if err != nil {
				content = fmt.Sprintf(`{"success":false,"error":%q,"code":%q}`, blockedMsg, blockedCode)
			}
			tr := core.ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Content:    content,
				IsError:    true,
			}
			events <- AgentEvent{
				Type: AgentEventTypeToolModeBlocked,
				ToolBlocked: &ToolCallBlocked{
					ToolCallID: call.ID,
					ToolName:   call.Name,
					ReasonCode: blockedCode,
				},
			}
			results = append(results, tr)
			events <- AgentEvent{Type: AgentEventTypeToolResult, Result: &tr}
			continue
		}
		decision := a.policy.Decide(spec, call)
		events <- AgentEvent{
			Type: AgentEventTypeToolPolicyDecision,
			Policy: &ToolPolicyDecision{
				ToolCallID:    call.ID,
				ToolName:      call.Name,
				Allow:         decision.Allow,
				NeedsApproval: decision.RequiresApproval,
				Reason:        decision.Reason,
				Code:          decision.Code,
				Phase:         decision.Phase,
				MatchedRule:   decision.MatchedRule,
			},
		}
		if !decision.Allow {
			tr := core.ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Content:    `{"success":false,"error":"policy denied tool call","code":"policy_denied"}`,
				IsError:    true,
			}
			results = append(results, tr)
			events <- AgentEvent{Type: AgentEventTypeToolResult, Result: &tr}
			continue
		}
		var grantOnSuccess bool
		var grantKey string
		var grantKeys []string
		if decision.RequiresApproval {
			keys := policy.ApprovalKeys(call)
			key := policy.ApprovalKey(call)
			approved := a.approvalCache.HasAll(sessionID, keys)
			if !approved {
				metadata := a.previewTool(ctx, call)
				metadata = policy.ApprovalMetadata(call, keys, metadata)
				events <- AgentEvent{
					Type: AgentEventTypeToolApprovalRequired,
					Approval: &ToolApprovalRequired{
						ToolCallID: call.ID,
						ToolName:   call.Name,
						Reason:     decision.Reason,
						Code:       decision.Code,
						Key:        key,
						Keys:       keys,
						Summary:    policy.ApprovalSummary(call),
						Scope:      policy.ApprovalScope(call),
						Metadata:   metadata,
					},
				}
				approvalDecision := policy.ApprovalDeny
				if a.approve != nil {
					approvalDecision = a.approve(policy.ApprovalRequest{
						SessionID: sessionID,
						ToolCall:  call,
						Spec:      spec,
						Reason:    decision.Reason,
						Code:      decision.Code,
						Key:       key,
						Keys:      keys,
						Metadata:  metadata,
					})
				}
				approved = approvalDecision.Approved()
				if approvalDecision.Canceled() {
					return core.Message{}, nil, llm.Usage{}, "", false, context.Canceled
				}
				if approvalDecision.ForSession() {
					if policy.ApprovalKeysFileScoped(keys) {
						grantOnSuccess = true
						grantKey = key
						grantKeys = keys
					} else {
						a.grantApprovals(ctx, sessionID, call, key, keys, events)
					}
				}
			}
			if !approved {
				tr := core.ToolResult{
					ToolCallID: call.ID,
					Name:       call.Name,
					Content:    `{"success":false,"error":"tool approval denied","code":"approval_denied"}`,
					IsError:    true,
				}
				results = append(results, tr)
				events <- AgentEvent{Type: AgentEventTypeToolResult, Result: &tr}
				toolMsg, err := a.store.Create(ctx, core.Message{SessionID: sessionID, Role: core.RoleTool, ToolResults: results})
				if err != nil {
					return core.Message{}, nil, llm.Usage{}, "", false, fmt.Errorf("create tool message: %w", err)
				}
				a.persistApprovalDeniedMarker(sessionID, call.Name)
				return assistant, &toolMsg, lastUsage, lastModel, true, nil
			}
		}

		if call.Name == "request_user_input" {
			res, err := a.handleRequestUserInput(call, sessionID, events)
			if err != nil {
				tr := core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: err.Error(), IsError: true}
				results = append(results, tr)
				events <- AgentEvent{Type: AgentEventTypeToolResult, Result: &tr}
				continue
			}
			results = append(results, res)
			r := res
			events <- AgentEvent{Type: AgentEventTypeToolResult, Result: &r}
			continue
		}
		switch call.Name {
		case "todo_add", "todo_list", "todo_update", "todo_remove", "todo_clear_done":
			res, err := a.handleTodo(call, sessionID)
			if err != nil {
				tr := core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: err.Error(), IsError: true}
				results = append(results, tr)
				events <- AgentEvent{Type: AgentEventTypeToolResult, Result: &tr}
				continue
			}
			results = append(results, res)
			r := res
			events <- AgentEvent{Type: AgentEventTypeToolResult, Result: &r}
			continue
		}

		finalRes, ok, primarySucceeded := a.dispatchWithRecovery(ctx, sessionID, assistant.ID, lastModel, call, events)
		if err := ctx.Err(); err != nil {
			return core.Message{}, nil, llm.Usage{}, "", false, err
		}
		if ok {
			if grantOnSuccess && primarySucceeded {
				a.grantApprovals(ctx, sessionID, call, grantKey, grantKeys, events)
			}
			if !a.hooks.Empty() {
				var toolArgs any
				_ = json.Unmarshal([]byte(call.Input), &toolArgs)
				report := a.hooks.Run(ctx, NewPostToolUsePayload(sessionID, call, toolArgs, finalRes.Content))
				a.emitHookReport(events, report)
			}
			results = append(results, finalRes)
			r := finalRes
			if taskEvent, ok := taskCompletedEvent(finalRes); ok {
				events <- taskEvent
			}
			events <- AgentEvent{Type: AgentEventTypeToolResult, Result: &r}
		}
	}

	toolMsg, err := a.store.Create(ctx, core.Message{SessionID: sessionID, Role: core.RoleTool, ToolResults: results})
	if err != nil {
		return core.Message{}, nil, llm.Usage{}, "", false, fmt.Errorf("create tool message: %w", err)
	}
	return assistant, &toolMsg, lastUsage, lastModel, false, nil
}

func (a *Agent) bestEffortUpdateAssistant(msg core.Message) {
	// Preserve the terminal assistant state even if the caller's turn context
	// was canceled. This is diagnostic persistence; failure must not mask the
	// original provider error.
	_ = a.store.Update(context.Background(), msg)
}

func modeBlockedDetails(mode session.Mode) (code, message, summary string, data map[string]any) {
	switch mode {
	case session.ModeAsk:
		return "ask_mode_blocked",
			"tool unavailable in ask mode",
			"Current mode: ask. Ask mode only allows read-only tools. To execute or modify files, switch to agent mode. To propose a reviewed approach first, switch to plan mode.",
			map[string]any{
				"current_mode":    "ask",
				"suggested_modes": []string{"agent", "plan"},
			}
	case session.ModePlan:
		return "plan_mode_blocked",
			"tool unavailable in plan mode",
			"Current mode: plan. Plan mode is read-only until the plan is approved. Stay here to refine the plan, or switch to agent mode when it's time to implement.",
			map[string]any{
				"current_mode":    "plan",
				"suggested_modes": []string{"agent"},
			}
	default:
		return "mode_blocked",
			"tool unavailable in current mode",
			"Tool unavailable in the current mode.",
			map[string]any{}
	}
}
