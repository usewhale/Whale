package tools

import (
	"context"
	"encoding/json"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/webfetch"
)

func (b *Toolset) fetch(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	return b.runWebFetch(ctx, call)
}

func (b *Toolset) runWebFetch(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	var in struct {
		URL       string `json:"url"`
		Prompt    string `json:"prompt"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := decodeInput(call.Input, &in); err != nil {
		return marshalToolError(call, "invalid_args", err.Error()), nil
	}
	b.syncWebFetchClient()
	result, err := b.webFetchClient.Fetch(ctx, webfetch.Request{
		URL:       in.URL,
		Prompt:    in.Prompt,
		TimeoutMS: in.TimeoutMS,
	})
	if err != nil {
		if fetchErr, ok := err.(*webfetch.Error); ok {
			return marshalWebFetchError(call, fetchErr), nil
		}
		return marshalToolError(call, "fetch_failed", err.Error()), nil
	}
	return marshalToolResult(call, webFetchResultData(result))
}

func webFetchResultData(result webfetch.Result) map[string]any {
	raw, err := json.Marshal(result)
	if err != nil {
		return map[string]any{"content": result.Content}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{"content": result.Content}
	}
	return out
}

func marshalWebFetchError(call core.ToolCall, err *webfetch.Error) core.ToolResult {
	data := map[string]any{}
	if err.Result.URL != "" {
		data["url"] = err.Result.URL
	}
	if err.Result.FinalURL != "" {
		data["final_url"] = err.Result.FinalURL
	}
	if err.Result.StatusCode != 0 {
		data["status_code"] = err.Result.StatusCode
		data["code_text"] = err.Result.CodeText
	}
	if err.Result.Recovery != nil {
		data["recovery"] = err.Result.Recovery
	}
	env := core.ToolEnvelope{
		OK:      false,
		Success: false,
		Code:    err.Code,
		Message: err.Message,
		Data:    data,
	}
	if hint, ok := core.ToolInputRecoveryHint(call.Name, err.Message); ok {
		env.Summary = hint
		data["recovery"] = hint
	}
	content, marshalErr := core.MarshalToolEnvelope(env)
	if marshalErr != nil {
		return marshalToolError(call, err.Code, err.Message)
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: content, Outcome: core.OutcomeForErrorCode(err.Code), Code: err.Code}
}
