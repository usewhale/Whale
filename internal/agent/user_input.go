package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/session"
)

// normalizeUserInputRequest validates and auto-repairs request_user_input arguments.
// It repairs out-of-bounds option counts (0, 1, or >4) so the model does not
// need to self-correct on retry. Only a hard failure — e.g. all questions
// missing options after repair — is returned as an error.
func normalizeUserInputRequest(req *core.UserInputRequest) error {
	if len(req.Questions) < 1 {
		return fmt.Errorf("questions must contain at least 1 item")
	}
	if len(req.Questions) > 3 {
		return fmt.Errorf("questions must contain 1 to 3 items")
	}
	for i := range req.Questions {
		q := &req.Questions[i]
		q.Header = strings.TrimSpace(q.Header)
		q.ID = strings.TrimSpace(q.ID)
		q.Question = strings.TrimSpace(q.Question)
		if q.Header == "" {
			return fmt.Errorf("question.header is required")
		}
		if q.ID == "" {
			return fmt.Errorf("question.id is required")
		}
		if q.Question == "" {
			return fmt.Errorf("question.question is required")
		}
		// Repair option counts: 0 → add placeholder, >4 → truncate.
		// The 1-option case is intentionally left as-is: the TUI already
		// appends a free-form "None of the above" row, so a single
		// model-provided option plus the TUI's Other gives the user two
		// meaningful choices.
		switch {
		case len(q.Options) == 0:
			q.Options = []core.UserInputOption{
				{Label: "Continue", Description: "Proceed as described above"},
				{Label: "Let me clarify", Description: "I'll provide more detail"},
			}
		case len(q.Options) > 4:
			q.Options = q.Options[:4]
		}
		// Drop options with empty label or description.
		filtered := q.Options[:0]
		for _, opt := range q.Options {
			if strings.TrimSpace(opt.Label) == "" || strings.TrimSpace(opt.Description) == "" {
				continue
			}
			opt.Label = strings.TrimSpace(opt.Label)
			opt.Description = strings.TrimSpace(opt.Description)
			filtered = append(filtered, opt)
		}
		q.Options = filtered
		// Re-check after filtering: if all options had empty labels/descriptions
		// and were dropped, restore the placeholder pair so the question is
		// always answerable.
		if len(q.Options) == 0 {
			q.Options = []core.UserInputOption{
				{Label: "Continue", Description: "Proceed as described above"},
				{Label: "Let me clarify", Description: "I'll provide more detail"},
			}
		}
	}
	return nil
}

func (a *Agent) handleRequestUserInput(ctx context.Context, call core.ToolCall, sessionID string, events chan<- AgentEvent) (core.ToolResult, error) {
	var in core.UserInputRequest
	if err := json.Unmarshal([]byte(call.Input), &in); err != nil {
		// Surface the concrete parse error (position + reason) so the model can
		// self-correct on its retry instead of guessing. Malformed JSON cannot be
		// repaired upstream, so an actionable message is the only recovery path.
		return core.ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			ModelText: fmt.Sprintf(
				`{"success":false,"error":%q,"code":"invalid_request_user_input"}`,
				fmt.Sprintf("failed to parse request_user_input arguments as JSON: %s", err.Error()),
			),
			Code: "invalid_request_user_input",
		}, nil
	}
	if err := normalizeUserInputRequest(&in); err != nil {
		return core.ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			ModelText:  fmt.Sprintf(`{"success":false,"error":%q,"code":"invalid_request_user_input"}`, err.Error()),
			Code:       "invalid_request_user_input",
		}, nil
	}
	if a.sessionRuntime != nil && a.sessionRuntime.Enabled() {
		_ = a.sessionRuntime.SaveUserInput(sessionID, session.UserInputState{
			Pending:    true,
			ToolCallID: call.ID,
			Questions:  in.Questions,
		})
	}
	if !sendAgentEvent(ctx, events, AgentEvent{
		Type:         AgentEventTypeUserInputRequired,
		ToolCall:     &call,
		UserInputReq: &in,
	}) {
		return core.ToolResult{}, ctx.Err()
	}

	if a.userInput == nil {
		if a.sessionRuntime != nil && a.sessionRuntime.Enabled() {
			_ = a.sessionRuntime.ClearUserInput(sessionID)
		}
		if !sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypeUserInputCancelled, ToolCall: &call}) {
			return core.ToolResult{}, ctx.Err()
		}
		return core.ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			ModelText:  `{"success":false,"error":"no user input handler configured","code":"user_input_unavailable"}`,
			Code:       "user_input_unavailable",
		}, nil
	}
	resp, ok := a.userInput(UserInputRequest{
		SessionID: sessionID,
		ToolCall:  call,
		Questions: in.Questions,
	})
	if a.sessionRuntime != nil && a.sessionRuntime.Enabled() {
		_ = a.sessionRuntime.ClearUserInput(sessionID)
	}
	if !ok {
		if !sendAgentEvent(ctx, events, AgentEvent{Type: AgentEventTypeUserInputCancelled, ToolCall: &call}) {
			return core.ToolResult{}, ctx.Err()
		}
		return core.ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			ModelText:  `{"success":false,"error":"user input cancelled","code":"user_input_cancelled"}`,
			Code:       "user_input_cancelled",
		}, nil
	}
	if !sendAgentEvent(ctx, events, AgentEvent{
		Type:          AgentEventTypeUserInputSubmitted,
		ToolCall:      &call,
		UserInputResp: &resp,
	}) {
		return core.ToolResult{}, ctx.Err()
	}
	b, err := core.MarshalToolJSON(map[string]any{
		"success": true,
		"data":    resp,
	})
	if err != nil {
		return core.ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: `{"success":false,"error":"failed to encode user input response","code":"user_input_encode_failed"}`, Code: "user_input_encode_failed"}, nil
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: string(b)}, nil
}
