package tasks

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
)

func compactPrompts(in []string) []string {
	out := make([]string, 0, len(in))
	for _, prompt := range in {
		if trimmed := strings.TrimSpace(prompt); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func addUsage(a, b llm.Usage) llm.Usage {
	a.PromptTokens += b.PromptTokens
	a.CompletionTokens += b.CompletionTokens
	a.TotalTokens += b.TotalTokens
	a.PromptCacheHitTokens += b.PromptCacheHitTokens
	a.PromptCacheMissTokens += b.PromptCacheMissTokens
	a.ReasoningReplayTokens += b.ReasoningReplayTokens
	a.ToolResultRawChars += b.ToolResultRawChars
	a.ToolResultReplayChars += b.ToolResultReplayChars
	a.ToolResultRawTokens += b.ToolResultRawTokens
	a.ToolResultReplayTokens += b.ToolResultReplayTokens
	a.ToolResultTokensSaved += b.ToolResultTokensSaved
	a.ToolResultsCompacted += b.ToolResultsCompacted
	return a
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func quoteProgressTerm(v string) string {
	v = compactProgressTarget(v)
	if v == "" {
		return ""
	}
	return `"` + v + `"`
}

func compactProgressTarget(v string) string {
	v = strings.Join(strings.Fields(strings.TrimSpace(v)), " ")
	const limit = 100
	if len(v) <= limit {
		return v
	}
	return v[:limit-3] + "..."
}

func compactURLForProgress(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return compactProgressTarget(raw)
	}
	target := u.Host + u.EscapedPath()
	if target == u.Host && u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	return compactProgressTarget(target)
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func asAnySlice(v any) []any {
	if v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if ok {
		return arr
	}
	return nil
}

func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return -1
	}
}

func truncateString(v string, limit int) (string, bool) {
	if limit <= 0 || len(v) <= limit {
		return v, false
	}
	return v[:limit], true
}

func marshalSuccess(call core.ToolCall, data map[string]any) (core.ToolResult, error) {
	content, err := core.MarshalToolEnvelope(core.NewToolSuccessEnvelope(data))
	if err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content}, nil
}

func marshalError(call core.ToolCall, code, msg string) (core.ToolResult, error) {
	content, err := core.MarshalToolEnvelope(core.NewToolErrorEnvelope(code, msg))
	if err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content, IsError: true}, nil
}

func marshalErrorWithData(call core.ToolCall, code, msg string, data map[string]any) (core.ToolResult, error) {
	env := core.NewToolErrorEnvelope(code, msg)
	env.Data = data
	content, err := core.MarshalToolEnvelope(env)
	if err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content, IsError: true}, nil
}

func decodeInput[T any](call core.ToolCall) (T, error) {
	var out T
	if err := json.Unmarshal([]byte(call.Input), &out); err != nil {
		return out, err
	}
	return out, nil
}
