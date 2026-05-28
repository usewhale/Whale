package agent

import "context"

func editApprovalPolicy() ToolPolicy {
	return DefaultToolPolicy{Rules: []PermissionRule{
		{Permission: "edit", Pattern: "*", Action: PermissionAsk},
	}}
}

func eventStream(events ...ProviderEvent) <-chan ProviderEvent {
	out := make(chan ProviderEvent, len(events))
	for _, ev := range events {
		out <- ev
	}
	close(out)
	return out
}

func endTurnEvent(content string) ProviderEvent {
	return ProviderEvent{
		Type: EventComplete,
		Response: &ProviderResponse{
			FinishReason: FinishReasonEndTurn,
			Content:      content,
		},
	}
}

func toolUseEvent(calls ...ToolCall) ProviderEvent {
	return ProviderEvent{
		Type: EventComplete,
		Response: &ProviderResponse{
			FinishReason: FinishReasonToolUse,
			ToolCalls:    calls,
		},
	}
}

func toolCall(id, name, input string) ToolCall {
	return ToolCall{ID: id, Name: name, Input: input}
}

type echoTool struct{}

func (e echoTool) Name() string { return "echo" }
func (e echoTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "ok:" + call.Input}, nil
}

type oneToolProvider struct {
	calls int
	tool  string
	input string
}

func (p *oneToolProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	if p.calls == 1 {
		return eventStream(toolUseEvent(toolCall("tc-one", p.tool, p.input)))
	}
	return eventStream(endTurnEvent("done"))
}

type flakyTool struct {
	n int
}

func (f *flakyTool) Name() string { return "flaky" }
func (f *flakyTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	f.n++
	if f.n == 1 {
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Content:    `{"success":false,"error":"command failed","code":"exec_failed"}`,
			IsError:    true,
		}, nil
	}
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: `{"success":true,"data":{"ok":true}}`}, nil
}

type writeLikeTool struct{}

func (w writeLikeTool) Name() string { return "write" }
func (w writeLikeTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "ok:" + call.Input}, nil
}

type viewLikeTool struct{}

func (v viewLikeTool) Name() string { return "read_file" }
func (v viewLikeTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "scavenged-ok:" + call.Input}, nil
}
