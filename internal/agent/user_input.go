package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/session"
)

func validateUserInputRequest(req core.UserInputRequest) error {
	if len(req.Questions) < 1 || len(req.Questions) > 3 {
		return fmt.Errorf("questions must contain 1 to 3 items")
	}
	for _, q := range req.Questions {
		if strings.TrimSpace(q.Header) == "" {
			return fmt.Errorf("question.header is required")
		}
		if strings.TrimSpace(q.ID) == "" {
			return fmt.Errorf("question.id is required")
		}
		if strings.TrimSpace(q.Question) == "" {
			return fmt.Errorf("question.question is required")
		}
		if len(q.Options) < 2 || len(q.Options) > 3 {
			return fmt.Errorf("question.options must contain 2 to 3 items")
		}
		for _, opt := range q.Options {
			if strings.TrimSpace(opt.Label) == "" || strings.TrimSpace(opt.Description) == "" {
				return fmt.Errorf("option.label and option.description are required")
			}
		}
	}
	return nil
}

func (a *Agent) handleRequestUserInput(ctx context.Context, call core.ToolCall, sessionID string, events chan<- AgentEvent) (core.ToolResult, error) {
	var in core.UserInputRequest
	if err := json.Unmarshal([]byte(call.Input), &in); err != nil {
		return core.ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			ModelText:  `{"success":false,"error":"invalid request_user_input input","code":"invalid_request_user_input"}`,
			Code:       "invalid_request_user_input",
		}, nil
	}
	if err := validateUserInputRequest(in); err != nil {
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
