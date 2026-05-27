package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/memory"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
)

type preparedToolDispatch struct {
	Index          int
	Call           core.ToolCall
	PreHookContext string
	GrantOnSuccess bool
	GrantKey       string
	GrantKeys      []string
}

type toolDispatchOutcome struct {
	Prepared         preparedToolDispatch
	Result           core.ToolResult
	OK               bool
	PrimarySucceeded bool
}

func (a *Agent) streamAndHandle(ctx context.Context, sessionID string, history []core.Message, rt *memory.RuntimeState, events chan<- AgentEvent, toolPolicy policy.ToolPolicy) (core.Message, *core.Message, llm.Usage, string, bool, error) {
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
	emit := func(ev AgentEvent) bool {
		return sendAgentEvent(ctx, events, ev)
	}

	ch := a.provider.StreamResponse(ctx, a.buildTurnProviderHistory(sessionID, rt), a.tools.Tools())
	for ev := range ch {
		switch ev.Type {
		case llm.EventContentDelta:
			assistant.Text += ev.Content
			assistantDeltaSeen = true
			if !a.emitAssistantContentDelta(ctx, ev.Content, &planParser, &planText, &planStarted, &planCompleted, events) {
				return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
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
				return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
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
					return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
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
		case llm.EventRetryScheduled:
			if ev.Retry != nil {
				info := *ev.Retry
				if info.StreamReset {
					assistant.Text = ""
					assistant.Reasoning = ""
					assistant.ToolCalls = nil
					assistant.FinishReason = ""
					rt.Scratch.ResetTurn()
					planParser = core.ProposedPlanParser{}
					planText.Reset()
					planStarted = false
					planCompleted = false
					assistantDeltaSeen = false
					streamPersisted = false
					if err := a.store.Update(ctx, assistant); err != nil {
						return core.Message{}, nil, llm.Usage{}, "", false, err
					}
				}
				if !emit(AgentEvent{Type: AgentEventTypeProviderRetryScheduled, ProviderRetry: &info}) {
					return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
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
							return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
						}
						if taskEvent, ok := taskStartedEvent(tc); ok {
							if !emit(taskEvent) {
								return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
							}
						}
					}
				}
				if ev.Response.Content != "" {
					assistant.Text = ev.Response.Content
					if !assistantDeltaSeen {
						if !a.emitAssistantContentDelta(ctx, ev.Response.Content, &planParser, &planText, &planStarted, &planCompleted, events) {
							return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
						}
					} else if a.mode == session.ModePlan && !planCompleted {
						if !a.emitFinalProposedPlan(ctx, ev.Response.Content, &planText, &planStarted, &planCompleted, events) {
							return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
						}
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
			if !a.emitProposedPlanSegment(ctx, seg, &planText, &planStarted, &planCompleted, events) {
				return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
			}
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
			if !emit(AgentEvent{Type: AgentEventTypeToolCallScavenged, Scavenged: &ToolCallScavenged{Count: report.scavenged}}) {
				return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
			}
		}
		if report.truncationsFixed > 0 {
			for i := range report.repairedCalls {
				a.recordToolInputRepair(sessionID, lastModel, assistant.ID, report.repairedCalls[i], "truncated_json")
				if !emit(AgentEvent{
					Type: AgentEventTypeToolArgsRepaired,
					ToolArgsRepair: &ToolArgsRepair{
						ToolCallIndex: i,
						ToolName:      report.repairedCalls[i].Name,
					},
				}) {
					return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
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
		if !emit(AgentEvent{
			Type: AgentEventTypeToolCallBlocked,
			ToolBlocked: &ToolCallBlocked{
				ToolCallID: br.ToolCallID,
				ToolName:   br.Name,
				ReasonCode: "storm_blocked",
			},
		}) {
			return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
		}
		results = append(results, br)
		if !emit(AgentEvent{Type: AgentEventTypeToolResult, Result: &br}) {
			return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
		}
	}
	pendingParallelSubagents := []preparedToolDispatch{}
	flushPendingParallelSubagents := func() error {
		if len(pendingParallelSubagents) == 0 {
			return nil
		}
		pending := append([]preparedToolDispatch(nil), pendingParallelSubagents...)
		pendingParallelSubagents = pendingParallelSubagents[:0]

		ready := make([]readyParallelSubagentCall, 0, len(pending))
		for _, prepared := range pending {
			ready = append(ready, readyParallelSubagentCall{Index: prepared.Index, Call: prepared.Call})
		}
		groups := eligibleReadyParallelSubagentGroups(ready)

		var outcomes []toolDispatchOutcome
		if len(groups) != 1 || groups[0].Start != pending[0].Index || len(groups[0].Calls) != len(pending) {
			for _, prepared := range pending {
				finalRes, ok, primarySucceeded := a.dispatchWithRecovery(ctx, sessionID, assistant.ID, lastModel, prepared.Call, events)
				if err := ctx.Err(); err != nil {
					return err
				}
				outcomes = append(outcomes, toolDispatchOutcome{
					Prepared:         prepared,
					Result:           finalRes,
					OK:               ok,
					PrimarySucceeded: primarySucceeded,
				})
			}
		} else {
			var err error
			outcomes, err = a.dispatchParallelSubagentsWithRecovery(ctx, sessionID, assistant.ID, lastModel, pending, events)
			if err != nil {
				return err
			}
		}
		for _, outcome := range outcomes {
			if !outcome.OK {
				continue
			}
			if !a.appendDispatchedToolResult(ctx, sessionID, outcome.Prepared, outcome.Result, outcome.PrimarySucceeded, events, &results) {
				return ctx.Err()
			}
		}
		return nil
	}
	for i, call := range dispatchCalls {
		if call.Name != parallelSubagentToolName {
			if err := flushPendingParallelSubagents(); err != nil {
				return core.Message{}, nil, llm.Usage{}, "", false, err
			}
		}
		if spec, ok := a.tools.Spec(call.Name); ok {
			if fixed, changed := core.RenestFlatInputForSpec(spec, call.Input); changed {
				call.Input = fixed
				a.recordToolInputRepair(sessionID, lastModel, assistant.ID, call, "renest_flat_input")
				if !emit(AgentEvent{
					Type: AgentEventTypeToolArgsRepaired,
					ToolArgsRepair: &ToolArgsRepair{
						ToolCallIndex: i,
						ToolName:      call.Name,
					},
				}) {
					return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
				}
			}
			if fixed, repairs := core.RepairToolInputForSpec(spec, call.Input); len(repairs) > 0 {
				call.Input = fixed
				for _, repair := range repairs {
					a.recordToolInputRepairDetail(sessionID, lastModel, assistant.ID, call, repair)
				}
				if !emit(AgentEvent{
					Type: AgentEventTypeToolArgsRepaired,
					ToolArgsRepair: &ToolArgsRepair{
						ToolCallIndex: i,
						ToolName:      call.Name,
					},
				}) {
					return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
				}
			}
		}
		var preHookContext string
		a.ensureApprovalCacheLoaded(ctx, sessionID)
		spec, ok := a.tools.Spec(call.Name)
		if !ok {
			spec = core.ToolSpec{Name: call.Name}
		}
		// Evaluate policy before PreToolUse hooks so that denied calls do not
		// trigger user-defined hook side effects. The second Decide() further
		// below is intentional: hooks may rewrite call.Input, which must be
		// re-evaluated by policy.
		earlyDecision := toolPolicy.Decide(spec, call)
		if !earlyDecision.Allow {
			if err := flushPendingParallelSubagents(); err != nil {
				return core.Message{}, nil, llm.Usage{}, "", false, err
			}
			if !emit(AgentEvent{
				Type: AgentEventTypeToolPolicyDecision,
				Policy: &ToolPolicyDecision{
					ToolCallID:    call.ID,
					ToolName:      call.Name,
					Allow:         earlyDecision.Allow,
					NeedsApproval: earlyDecision.RequiresApproval,
					Reason:        earlyDecision.Reason,
					Code:          earlyDecision.Code,
					Phase:         earlyDecision.Phase,
					MatchedRule:   earlyDecision.MatchedRule,
				},
			}) {
				return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
			}
			tr := core.ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Content:    policyDenialEnvelope(earlyDecision),
				IsError:    true,
			}
			results = append(results, tr)
			if !emit(AgentEvent{Type: AgentEventTypeToolResult, Result: &tr}) {
				return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
			}
			continue
		}
		if !a.hooks.Empty() {
			var toolArgs any
			_ = json.Unmarshal([]byte(call.Input), &toolArgs)
			report := a.hooks.Run(ctx, NewPreToolUsePayload(sessionID, call, toolArgs))
			if !a.emitHookReport(ctx, events, report) {
				return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
			}
			if report.Blocked {
				if err := flushPendingParallelSubagents(); err != nil {
					return core.Message{}, nil, llm.Usage{}, "", false, err
				}
				msg := "blocked by PreToolUse hook"
				code := "hook_blocked"
				if report.Halted {
					code = "hook_halted"
					msg = "halted by PreToolUse hook"
				}
				if len(report.Outcomes) > 0 {
					last := report.Outcomes[len(report.Outcomes)-1]
					if strings.TrimSpace(hookOutcomeMessage(last)) != "" {
						msg = strings.TrimSpace(hookOutcomeMessage(last))
					}
				}
				tr := core.ToolResult{
					ToolCallID: call.ID,
					Name:       call.Name,
					Content:    fmt.Sprintf(`{"success":false,"error":%q,"code":%q}`, msg, code),
					IsError:    true,
				}
				results = append(results, tr)
				if !emit(AgentEvent{Type: AgentEventTypeToolResult, Result: &tr}) {
					return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
				}
				continue
			}
			if strings.TrimSpace(report.UpdatedInput) != "" {
				call.Input = strings.TrimSpace(report.UpdatedInput)
			}
			preHookContext = strings.TrimSpace(report.AdditionalContext)
		}
		if (a.mode == session.ModePlan || a.mode == session.ModeAsk) && !core.IsReadOnlyToolCall(spec, call) {
			if err := flushPendingParallelSubagents(); err != nil {
				return core.Message{}, nil, llm.Usage{}, "", false, err
			}
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
			if !emit(AgentEvent{
				Type: AgentEventTypeToolModeBlocked,
				ToolBlocked: &ToolCallBlocked{
					ToolCallID: call.ID,
					ToolName:   call.Name,
					ReasonCode: blockedCode,
				},
			}) {
				return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
			}
			results = append(results, tr)
			if !emit(AgentEvent{Type: AgentEventTypeToolResult, Result: &tr}) {
				return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
			}
			continue
		}
		if call.Name == "update_plan" {
			res, err := a.handleUpdatePlan(ctx, call, events)
			if err != nil {
				tr := core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: err.Error(), IsError: true}
				results = append(results, tr)
				if !emit(AgentEvent{Type: AgentEventTypeToolResult, Result: &tr}) {
					return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
				}
				continue
			}
			results = append(results, res)
			r := res
			if !emit(AgentEvent{Type: AgentEventTypeToolResult, Result: &r}) {
				return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
			}
			continue
		}
		decision := toolPolicy.Decide(spec, call)
		if !emit(AgentEvent{
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
		}) {
			return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
		}
		if !decision.Allow {
			if err := flushPendingParallelSubagents(); err != nil {
				return core.Message{}, nil, llm.Usage{}, "", false, err
			}
			tr := core.ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Content:    policyDenialEnvelope(decision),
				IsError:    true,
			}
			results = append(results, tr)
			if !emit(AgentEvent{Type: AgentEventTypeToolResult, Result: &tr}) {
				return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
			}
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
				if !emit(AgentEvent{
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
				}) {
					return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
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
						if !a.grantApprovals(ctx, sessionID, call, key, keys, events) {
							return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
						}
					}
				}
			}
			if !approved {
				if err := flushPendingParallelSubagents(); err != nil {
					return core.Message{}, nil, llm.Usage{}, "", false, err
				}
				tr := core.ToolResult{
					ToolCallID: call.ID,
					Name:       call.Name,
					Content:    `{"success":false,"error":"tool approval denied","code":"approval_denied"}`,
					IsError:    true,
				}
				results = append(results, tr)
				if !emit(AgentEvent{Type: AgentEventTypeToolResult, Result: &tr}) {
					return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
				}
				toolMsg, err := a.store.Create(ctx, core.Message{SessionID: sessionID, Role: core.RoleTool, ToolResults: results})
				if err != nil {
					return core.Message{}, nil, llm.Usage{}, "", false, fmt.Errorf("create tool message: %w", err)
				}
				a.persistApprovalDeniedMarker(sessionID, call.Name)
				return assistant, &toolMsg, lastUsage, lastModel, true, nil
			}
		}
		prepared := preparedToolDispatch{
			Index:          i,
			Call:           call,
			PreHookContext: preHookContext,
			GrantOnSuccess: grantOnSuccess,
			GrantKey:       grantKey,
			GrantKeys:      grantKeys,
		}
		if _, ok := maybeReadyParallelSubagentCall(i, call); ok {
			pendingParallelSubagents = append(pendingParallelSubagents, prepared)
			continue
		}

		if call.Name == "request_user_input" {
			res, err := a.handleRequestUserInput(ctx, call, sessionID, events)
			if err != nil {
				tr := core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: err.Error(), IsError: true}
				results = append(results, tr)
				if !emit(AgentEvent{Type: AgentEventTypeToolResult, Result: &tr}) {
					return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
				}
				continue
			}
			results = append(results, res)
			r := res
			if !emit(AgentEvent{Type: AgentEventTypeToolResult, Result: &r}) {
				return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
			}
			continue
		}
		switch call.Name {
		case "todo_add", "todo_list", "todo_update", "todo_remove", "todo_clear_done":
			res, err := a.handleTodo(call, sessionID)
			if err != nil {
				tr := core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: err.Error(), IsError: true}
				results = append(results, tr)
				if !emit(AgentEvent{Type: AgentEventTypeToolResult, Result: &tr}) {
					return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
				}
				continue
			}
			results = append(results, res)
			r := res
			if !emit(AgentEvent{Type: AgentEventTypeToolResult, Result: &r}) {
				return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
			}
			continue
		}

		finalRes, ok, primarySucceeded := a.dispatchWithRecovery(ctx, sessionID, assistant.ID, lastModel, call, events)
		if err := ctx.Err(); err != nil {
			return core.Message{}, nil, llm.Usage{}, "", false, err
		}
		if ok {
			if !a.appendDispatchedToolResult(ctx, sessionID, prepared, finalRes, primarySucceeded, events, &results) {
				return core.Message{}, nil, llm.Usage{}, "", false, ctx.Err()
			}
		}
	}
	if err := flushPendingParallelSubagents(); err != nil {
		return core.Message{}, nil, llm.Usage{}, "", false, err
	}

	toolMsg, err := a.store.Create(ctx, core.Message{SessionID: sessionID, Role: core.RoleTool, ToolResults: results})
	if err != nil {
		return core.Message{}, nil, llm.Usage{}, "", false, fmt.Errorf("create tool message: %w", err)
	}
	return assistant, &toolMsg, lastUsage, lastModel, false, nil
}

func (a *Agent) dispatchParallelSubagentsWithRecovery(ctx context.Context, sessionID, assistantMessageID, model string, pending []preparedToolDispatch, events chan<- AgentEvent) ([]toolDispatchOutcome, error) {
	outcomes := make([]toolDispatchOutcome, len(pending))
	var wg sync.WaitGroup
	for i, prepared := range pending {
		i, prepared := i, prepared
		wg.Add(1)
		go func() {
			defer wg.Done()
			finalRes, ok, primarySucceeded := a.dispatchWithRecovery(ctx, sessionID, assistantMessageID, model, prepared.Call, events)
			outcomes[i] = toolDispatchOutcome{
				Prepared:         prepared,
				Result:           finalRes,
				OK:               ok,
				PrimarySucceeded: primarySucceeded,
			}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(outcomes, func(i, j int) bool {
		return outcomes[i].Prepared.Index < outcomes[j].Prepared.Index
	})
	return outcomes, nil
}

func (a *Agent) appendDispatchedToolResult(ctx context.Context, sessionID string, prepared preparedToolDispatch, finalRes core.ToolResult, primarySucceeded bool, events chan<- AgentEvent, results *[]core.ToolResult) bool {
	emit := func(ev AgentEvent) bool {
		return sendAgentEvent(ctx, events, ev)
	}
	call := prepared.Call
	if prepared.GrantOnSuccess && primarySucceeded {
		if !a.grantApprovals(ctx, sessionID, call, prepared.GrantKey, prepared.GrantKeys, events) {
			return false
		}
	}
	if prepared.PreHookContext != "" {
		finalRes.Content = addHookContextToToolContent(finalRes.Content, prepared.PreHookContext)
	}
	if !a.hooks.Empty() {
		var toolArgs any
		_ = json.Unmarshal([]byte(call.Input), &toolArgs)
		report := a.hooks.Run(ctx, NewPostToolUsePayload(sessionID, call, toolArgs, finalRes.Content))
		if !a.emitHookReport(ctx, events, report) {
			return false
		}
		if strings.TrimSpace(report.AdditionalContext) != "" {
			finalRes.Content = addHookContextToToolContent(finalRes.Content, report.AdditionalContext)
		}
	}
	*results = append(*results, finalRes)
	r := finalRes
	if taskEvent, ok := taskCompletedEvent(finalRes); ok {
		if !emit(taskEvent) {
			return false
		}
	}
	if !emit(AgentEvent{Type: AgentEventTypeToolResult, Result: &r}) {
		return false
	}
	return true
}

func (a *Agent) bestEffortUpdateAssistant(msg core.Message) {
	// Preserve the terminal assistant state even if the caller's turn context
	// was canceled. This is diagnostic persistence; failure must not mask the
	// original provider error.
	_ = a.store.Update(context.Background(), msg)
}

func addHookContextToToolContent(content, hookContext string) string {
	hookContext = strings.TrimSpace(hookContext)
	if hookContext == "" {
		return content
	}
	if env, ok := core.ParseToolEnvelope(content); ok {
		if env.Metadata == nil {
			env.Metadata = map[string]any{}
		}
		env.Metadata["hook_context"] = appendHookContextValue(env.Metadata["hook_context"], hookContext)
		if encoded, err := core.MarshalToolEnvelope(env); err == nil {
			return encoded
		}
	}
	return strings.TrimSpace(content + "\n" + hookContext)
}

func appendHookContextValue(existing any, hookContext string) any {
	switch v := existing.(type) {
	case nil:
		return []string{hookContext}
	case string:
		if strings.TrimSpace(v) == "" {
			return []string{hookContext}
		}
		return []string{v, hookContext}
	case []string:
		return append(v, hookContext)
	case []any:
		return append(v, hookContext)
	default:
		return []any{v, hookContext}
	}
}

func modeBlockedDetails(mode session.Mode) (code, message, summary string, data map[string]any) {
	switch mode {
	case session.ModeAsk:
		return "ask_mode_blocked",
			"tool unavailable in ask mode",
			"Current mode: ask. Ask mode only allows read-only tools. To execute or modify files, switch to agent mode with /agent or Shift+Tab. To propose a reviewed approach first, switch to plan mode with /plan or Shift+Tab.",
			map[string]any{
				"current_mode":    "ask",
				"suggested_modes": []string{"/agent", "/plan", "Shift+Tab"},
			}
	case session.ModePlan:
		return "plan_mode_blocked",
			"tool unavailable in plan mode",
			"Current mode: plan. Plan mode is read-only until the plan is approved. Stay here to refine the plan, or switch to agent mode with /agent or Shift+Tab when it's time to implement.",
			map[string]any{
				"current_mode":    "plan",
				"suggested_modes": []string{"/agent", "Shift+Tab"},
			}
	default:
		return "mode_blocked",
			"tool unavailable in current mode",
			"Tool unavailable in the current mode.",
			map[string]any{}
	}
}

func policyDenialEnvelope(d policy.PolicyDecision) string {
	code := strings.TrimSpace(d.Code)
	if code == "" {
		code = "policy_denied"
	}
	reason := strings.TrimSpace(d.Reason)
	if reason == "" {
		reason = "policy denied tool call"
	}
	content, err := core.MarshalToolEnvelope(core.ToolEnvelope{
		OK:      false,
		Success: false,
		Code:    code,
		Error:   reason,
		Message: reason,
	})
	if err != nil {
		return fmt.Sprintf(`{"success":false,"error":%q,"code":%q}`, reason, code)
	}
	return content
}
