package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/memory"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
)

type preparedToolDispatch struct {
	Index             int
	Call              core.ToolCall
	PreHookContext    string
	GrantOnSuccess    bool
	GrantKey          string
	GrantKeys         []string
	ExternalReadRoots []string
}

type toolDispatchOutcome struct {
	Prepared         preparedToolDispatch
	Result           core.ToolResult
	OK               bool
	PrimarySucceeded bool
}

func (a *Agent) streamAndHandle(ctx context.Context, sessionID string, checkpointMessageID string, history []core.Message, rt *memory.RuntimeState, events chan<- AgentEvent, toolPolicy policy.ToolPolicy, tools *core.ToolRegistry) (core.Message, *core.Message, llm.Usage, string, bool, error) {
	assistant, lastUsage, lastModel, err := a.collectAssistantStream(ctx, sessionID, rt, events, tools)
	if err != nil {
		return core.Message{}, nil, llm.Usage{}, "", false, err
	}

	dispatchCalls, blocked, err := a.prepareToolDispatches(ctx, sessionID, lastModel, assistant, events, tools)
	if err != nil {
		return core.Message{}, nil, llm.Usage{}, "", false, err
	}
	if len(dispatchCalls) == 0 {
		return assistant, nil, lastUsage, lastModel, false, nil
	}
	toolMsg, abortTurn, err := a.dispatchToolCalls(ctx, streamDispatchContext{
		SessionID:           sessionID,
		Assistant:           assistant,
		Model:               lastModel,
		Policy:              toolPolicy,
		Tools:               tools,
		Events:              events,
		CheckpointMessageID: checkpointMessageID,
	}, dispatchCalls, blocked)
	if err != nil {
		return core.Message{}, nil, llm.Usage{}, "", false, err
	}
	return assistant, toolMsg, lastUsage, lastModel, abortTurn, nil
}

func (a *Agent) flushPendingParallelSubagents(ctx context.Context, sessionID, assistantMessageID, model string, pending []preparedToolDispatch, events chan<- AgentEvent, results *[]core.ToolResult, tools *core.ToolRegistry) error {
	ready := make([]readyParallelSubagentCall, 0, len(pending))
	for _, prepared := range pending {
		ready = append(ready, readyParallelSubagentCall{Index: prepared.Index, Call: prepared.Call})
	}
	groups := eligibleReadyParallelSubagentGroups(ready)

	var outcomes []toolDispatchOutcome
	if len(groups) != 1 || groups[0].Start != pending[0].Index || len(groups[0].Calls) != len(pending) {
		for _, prepared := range pending {
			finalRes, ok, primarySucceeded := a.dispatchWithRecovery(ctx, sessionID, assistantMessageID, "", model, prepared.Call, prepared.ExternalReadRoots, events, tools)
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
		outcomes, err = a.dispatchParallelSubagentsWithRecovery(ctx, sessionID, assistantMessageID, model, pending, events, tools)
		if err != nil {
			return err
		}
	}
	for _, outcome := range outcomes {
		if !outcome.OK {
			continue
		}
		if !a.appendDispatchedToolResult(ctx, sessionID, outcome.Prepared, outcome.Result, outcome.PrimarySucceeded, events, results) {
			return ctx.Err()
		}
	}
	return nil
}

func (a *Agent) dispatchParallelSubagentsWithRecovery(ctx context.Context, sessionID, assistantMessageID, model string, pending []preparedToolDispatch, events chan<- AgentEvent, tools *core.ToolRegistry) ([]toolDispatchOutcome, error) {
	limit := a.maxParallelSubagents
	if limit <= 0 {
		limit = defaultMaxParallelSubagents()
	}
	if limit > len(pending) {
		limit = len(pending)
	}
	outcomeCh := make(chan toolDispatchOutcome, len(pending))
	workCh := make(chan preparedToolDispatch)
	for i := 0; i < limit; i++ {
		go func() {
			for prepared := range workCh {
				finalRes, ok, primarySucceeded := a.dispatchWithRecovery(ctx, sessionID, assistantMessageID, "", model, prepared.Call, prepared.ExternalReadRoots, events, tools)
				// outcomeCh is buffered for every pending call so a worker that
				// already started can report without blocking after parent
				// cancellation. The actual stop still depends on
				// dispatchWithRecovery and the tool honoring ctx.
				outcomeCh <- toolDispatchOutcome{
					Prepared:         prepared,
					Result:           finalRes,
					OK:               ok,
					PrimarySucceeded: primarySucceeded,
				}
			}
		}()
	}
	go func() {
		defer close(workCh)
		for _, prepared := range pending {
			select {
			case workCh <- prepared:
			case <-ctx.Done():
				return
			}
		}
	}()

	outcomes := make([]toolDispatchOutcome, 0, len(pending))
	for range pending {
		select {
		case outcome := <-outcomeCh:
			outcomes = append(outcomes, outcome)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
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
	// Parallel spawn_subagent batches run post hooks only after the whole batch
	// returns, in original tool-call order, so stored tool results and events
	// stay deterministic even when the underlying subagents finish out of order.
	if !a.hooks.Empty() {
		var toolArgs any
		_ = json.Unmarshal([]byte(call.Input), &toolArgs)
		report := a.hooks.RunHook(ctx, NewPostToolUsePayload(sessionID, call, toolArgs, finalRes.Content))
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
	return modeBlockedDetailsForCall(mode, core.ToolCall{})
}

func modeBlockedDetailsForCall(mode session.Mode, call core.ToolCall) (code, message, summary string, data map[string]any) {
	switch mode {
	case session.ModeAsk:
		if call.Name == "shell_run" {
			return "ask_mode_blocked",
				"shell command not confirmed read-only in ask mode",
				"Ask mode blocked this shell command; do not retry the same shell operation with another shell command in this mode.",
				map[string]any{
					"current_mode":    "ask",
					"tool":            call.Name,
					"action":          "do_not_retry_same_command",
					"retryable":       false,
					"suggested_modes": []string{"/agent", "/plan", "Shift+Tab"},
				}
		}
		return "ask_mode_blocked",
			"tool unavailable in ask mode",
			"Ask mode blocked this tool call; do not retry the same call in this mode.",
			map[string]any{
				"current_mode":    "ask",
				"tool":            call.Name,
				"action":          "do_not_retry_same_call",
				"retryable":       false,
				"suggested_modes": []string{"/agent", "/plan", "Shift+Tab"},
			}
	case session.ModePlan:
		if call.Name == "shell_run" {
			return "plan_mode_blocked",
				"shell command not confirmed read-only in plan mode",
				"Plan mode blocked this shell command; do not retry the same shell operation with another shell command in this mode. Continue with allowed read-only tools, or output the final plan in a <proposed_plan> block.",
				map[string]any{
					"current_mode": "plan",
					"tool":         call.Name,
					"action":       "do_not_retry_same_command",
					"retryable":    false,
				}
		}
		return "plan_mode_blocked",
			"tool unavailable in plan mode",
			"Plan mode blocked this tool call; do not retry the same call in this mode. Continue with allowed read-only tools, or output the final plan in a <proposed_plan> block.",
			map[string]any{
				"current_mode": "plan",
				"tool":         call.Name,
				"action":       "do_not_retry_same_call",
				"retryable":    false,
			}
	default:
		return "mode_blocked",
			"tool unavailable in current mode",
			"Current mode blocked this tool call; do not retry the same call in this mode.",
			map[string]any{
				"tool":      call.Name,
				"action":    "do_not_retry_same_call",
				"retryable": false,
			}
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

func addPolicyApprovalMetadata(metadata map[string]any, decision policy.PolicyDecision) map[string]any {
	if metadata == nil {
		metadata = map[string]any{}
	}
	if strings.TrimSpace(decision.MatchedRule) != "" {
		metadata["matched_rule"] = decision.MatchedRule
	}
	if strings.TrimSpace(decision.Permission) != "" {
		metadata["permission_kind"] = decision.Permission
	}
	if strings.TrimSpace(decision.Pattern) != "" {
		metadata["permission_target"] = decision.Pattern
	}
	return metadata
}
