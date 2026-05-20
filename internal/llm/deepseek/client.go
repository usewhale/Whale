package deepseek

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/llm"
	llmretry "github.com/usewhale/whale/internal/llm/retry"
)

const (
	defaultBaseURL           = "https://api.deepseek.com"
	defaultStreamMaxAttempts = 6
)

var errIncompleteStream = errors.New("stream disconnected before completion")

type Client struct {
	apiKey            string
	baseURL           string
	httpClient        *http.Client
	model             string
	reasoningEffort   string
	thinkingEnabled   bool
	maxTokens         int
	retryPolicy       llmretry.Policy
	retrySleeper      llmretry.Sleeper
	streamMaxAttempts int
}

type Option func(*Client)

func WithBaseURL(v string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(v, "/") }
}

func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.httpClient = h }
}

func WithModel(model string) Option {
	return func(c *Client) { c.model = model }
}

func WithAPIKey(key string) Option {
	return func(c *Client) { c.apiKey = key }
}

func WithReasoningEffort(v string) Option {
	return func(c *Client) { c.reasoningEffort = strings.ToLower(strings.TrimSpace(v)) }
}

func WithThinking(enabled bool) Option {
	return func(c *Client) { c.thinkingEnabled = enabled }
}

func WithMaxTokens(v int) Option {
	return func(c *Client) { c.maxTokens = v }
}

func WithRetryPolicy(policy llmretry.Policy) Option {
	return func(c *Client) { c.retryPolicy = llmretry.NormalizePolicy(policy) }
}

func WithStreamMaxAttempts(v int) Option {
	return func(c *Client) {
		if v > 0 {
			c.streamMaxAttempts = v
		}
	}
}

func withRetrySleeper(s llmretry.Sleeper) Option {
	return func(c *Client) {
		if s != nil {
			c.retrySleeper = s
		}
	}
}

func New(opts ...Option) (*Client, error) {
	c := &Client{
		baseURL: strings.TrimRight(envOr("DEEPSEEK_BASE_URL", defaultBaseURL), "/"),
		httpClient: &http.Client{
			Timeout: 11 * time.Minute,
		},
		model:             defaults.DefaultModel,
		reasoningEffort:   defaults.DefaultReasoningEffort,
		thinkingEnabled:   defaults.DefaultThinkingEnabled,
		retryPolicy:       llmretry.DefaultPolicy(),
		retrySleeper:      llmretry.Sleep,
		streamMaxAttempts: defaultStreamMaxAttempts,
	}
	for _, opt := range opts {
		opt(c)
	}
	if strings.TrimSpace(c.apiKey) == "" {
		c.apiKey = strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	}
	if strings.TrimSpace(c.apiKey) == "" {
		return nil, errors.New("DeepSeek API key is not configured. Run `whale setup` to save one for Whale, or set `DEEPSEEK_API_KEY` in your environment")
	}
	if c.baseURL == "" {
		c.baseURL = defaultBaseURL
	}
	c.retryPolicy = llmretry.NormalizePolicy(c.retryPolicy)
	if c.retrySleeper == nil {
		c.retrySleeper = llmretry.Sleep
	}
	if c.streamMaxAttempts <= 0 {
		c.streamMaxAttempts = defaultStreamMaxAttempts
	}
	return c, nil
}

func envOr(name, fallback string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	return v
}

func (c *Client) StreamResponse(ctx context.Context, history []core.Message, tools []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent)
	go func() {
		defer close(out)
		if err := c.stream(ctx, history, tools, out); err != nil {
			out <- llm.ProviderEvent{Type: llm.EventError, Err: err}
		}
	}()
	return out
}

func (c *Client) stream(ctx context.Context, history []core.Message, tools []core.Tool, out chan<- llm.ProviderEvent) error {
	msgs := toDeepSeekMessages(history)
	payload := map[string]any{
		"model":          c.model,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
		"messages":       msgs,
		"thinking":       map[string]any{"type": "disabled"},
	}
	if c.thinkingEnabled {
		payload["thinking"] = map[string]any{"type": "enabled"}
		if strings.TrimSpace(c.reasoningEffort) != "" {
			payload["reasoning_effort"] = c.reasoningEffort
		}
	}
	if len(tools) > 0 {
		payload["tools"] = toDeepSeekTools(tools)
	}
	if c.maxTokens > 0 {
		payload["max_tokens"] = c.maxTokens
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	return c.streamWithRetries(ctx, body, msgs, out)
}

func (c *Client) streamWithRetries(ctx context.Context, body []byte, msgs []map[string]any, out chan<- llm.ProviderEvent) error {
	policy := llmretry.NormalizePolicy(c.retryPolicy)
	requestAttempt := 1
	streamAttempt := 1
	for {
		resp, err := c.sendStreamRequest(ctx, body)
		if err != nil {
			var buildErr *requestBuildError
			if errors.As(err, &buildErr) {
				return err
			}
			if !llmretry.ShouldRetry(policy, err) || requestAttempt >= policy.MaxAttempts {
				return deepSeekRequestError(err)
			}
			delay := llmretry.Backoff(policy, requestAttempt, err)
			out <- retryScheduledEvent(requestAttempt, policy.MaxAttempts, delay, err, "request", false)
			requestAttempt++
			if sleepErr := c.retrySleeper(ctx, delay); sleepErr != nil {
				return sleepErr
			}
			continue
		}

		parseErr := parseSSE(resp.Body, c.model, estimateReasoningReplayTokens(msgs), out)
		_ = resp.Body.Close()
		if parseErr == nil {
			return nil
		}
		if !shouldRetryStreamError(parseErr) || streamAttempt >= c.streamMaxAttempts {
			return parseErr
		}
		delay := llmretry.Backoff(policy, streamAttempt, parseErr)
		out <- retryScheduledEvent(streamAttempt, c.streamMaxAttempts, delay, parseErr, "stream", true)
		streamAttempt++
		if sleepErr := c.retrySleeper(ctx, delay); sleepErr != nil {
			return sleepErr
		}
	}
}

func (c *Client) sendStreamRequest(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, &requestBuildError{err: fmt.Errorf("new request: %w", err)}
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		_ = resp.Body.Close()
		return nil, &llmretry.HTTPError{
			StatusCode: resp.StatusCode,
			Header:     resp.Header.Clone(),
			Body:       string(b),
		}
	}
	return resp, nil
}

type requestBuildError struct {
	err error
}

func (e *requestBuildError) Error() string {
	return e.err.Error()
}

func (e *requestBuildError) Unwrap() error {
	return e.err
}

func deepSeekRequestError(err error) error {
	var httpErr *llmretry.HTTPError
	if errors.As(err, &httpErr) {
		return fmt.Errorf("deepseek %d: %s", httpErr.StatusCode, strings.TrimSpace(httpErr.Body))
	}
	return err
}

func retryStatusCode(err error) int {
	var httpErr *llmretry.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode
	}
	return 0
}

func retryScheduledEvent(attempt, maxAttempts int, delay time.Duration, err error, stage string, streamReset bool) llm.ProviderEvent {
	return llm.ProviderEvent{
		Type: llm.EventRetryScheduled,
		Retry: &llmretry.Info{
			Attempt:     attempt,
			MaxAttempts: maxAttempts,
			Delay:       delay,
			StatusCode:  retryStatusCode(err),
			Reason:      retryReason(err, stage),
			Stage:       stage,
			StreamReset: streamReset,
		},
	}
}

func retryReason(err error, stage string) string {
	if stage == "stream" {
		return "API stream disconnected"
	}
	return llmretry.Reason(err)
}

func shouldRetryStreamError(err error) bool {
	if err == nil {
		return false
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func parseSSE(r io.Reader, model string, replayTokens int, out chan<- llm.ProviderEvent) error {
	reader := bufio.NewReader(r)
	var dataLines []string
	acc := streamAccumulator{callsByIndex: map[int]*toolCallState{}, readyIndices: map[int]bool{}, reasoningReplayTokens: replayTokens}
	for {
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(trimmed, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(trimmed, "data:")))
		}
		if trimmed == "" && len(dataLines) > 0 {
			data := strings.Join(dataLines, "\n")
			if done, perr := parseSSEData(data, model, out, &acc); perr != nil {
				return perr
			} else if done {
				return nil
			}
			dataLines = dataLines[:0]
		}
		if errors.Is(err, io.EOF) {
			if len(dataLines) > 0 {
				data := strings.Join(dataLines, "\n")
				if done, perr := parseSSEData(data, model, out, &acc); perr != nil {
					return perr
				} else if done {
					return nil
				}
			}
			return errIncompleteStream
		}
	}
}

type sseChoiceDelta struct {
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content"`
	ToolCalls        []struct {
		Index    *int   `json:"index"`
		ID       string `json:"id"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

type sseFrame struct {
	Choices []struct {
		Delta        sseChoiceDelta `json:"delta"`
		FinishReason string         `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens          int `json:"prompt_tokens"`
		CompletionTokens      int `json:"completion_tokens"`
		TotalTokens           int `json:"total_tokens"`
		PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
		PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
	} `json:"usage"`
}

type toolCallState struct {
	id        string
	name      string
	arguments strings.Builder
	started   bool
}

type streamAccumulator struct {
	content               strings.Builder
	reasoning             strings.Builder
	callsByIndex          map[int]*toolCallState
	readyIndices          map[int]bool
	finishReason          string
	usage                 llm.Usage
	reasoningReplayTokens int
}

func parseSSEData(data, model string, out chan<- llm.ProviderEvent, acc *streamAccumulator) (bool, error) {
	if data == "[DONE]" {
		emitComplete(out, model, acc)
		return true, nil
	}
	if strings.TrimSpace(data) == "" {
		return false, nil
	}

	var frame sseFrame
	if err := json.Unmarshal([]byte(data), &frame); err != nil {
		return false, nil // skip malformed frame
	}
	if len(frame.Choices) == 0 {
		// some providers emit usage-only terminal frames
		if frame.Usage.TotalTokens > 0 || frame.Usage.PromptTokens > 0 || frame.Usage.CompletionTokens > 0 {
			acc.usage = llm.Usage{
				PromptTokens:          frame.Usage.PromptTokens,
				CompletionTokens:      frame.Usage.CompletionTokens,
				TotalTokens:           frame.Usage.TotalTokens,
				PromptCacheHitTokens:  frame.Usage.PromptCacheHitTokens,
				PromptCacheMissTokens: frame.Usage.PromptCacheMissTokens,
			}
		}
		return false, nil
	}
	if frame.Usage.TotalTokens > 0 || frame.Usage.PromptTokens > 0 || frame.Usage.CompletionTokens > 0 {
		acc.usage = llm.Usage{
			PromptTokens:          frame.Usage.PromptTokens,
			CompletionTokens:      frame.Usage.CompletionTokens,
			TotalTokens:           frame.Usage.TotalTokens,
			PromptCacheHitTokens:  frame.Usage.PromptCacheHitTokens,
			PromptCacheMissTokens: frame.Usage.PromptCacheMissTokens,
		}
	}
	ch := frame.Choices[0]
	if ch.FinishReason != "" {
		acc.finishReason = ch.FinishReason
	}
	if ch.Delta.Content != "" {
		acc.content.WriteString(ch.Delta.Content)
		out <- llm.ProviderEvent{Type: llm.EventContentDelta, Content: ch.Delta.Content}
	}
	if ch.Delta.ReasoningContent != "" {
		acc.reasoning.WriteString(ch.Delta.ReasoningContent)
		out <- llm.ProviderEvent{
			Type:           llm.EventReasoningDelta,
			ReasoningDelta: ch.Delta.ReasoningContent,
		}
	}
	for _, tc := range ch.Delta.ToolCalls {
		idx := 0
		if tc.Index != nil {
			idx = *tc.Index
		}
		st, ok := acc.callsByIndex[idx]
		if !ok {
			st = &toolCallState{}
			acc.callsByIndex[idx] = st
		}
		if tc.ID != "" {
			st.id = tc.ID
		}
		if tc.Function.Name != "" {
			st.name = tc.Function.Name
		}
		if tc.Function.Arguments != "" {
			st.arguments.WriteString(tc.Function.Arguments)
		}
		if st.name != "" {
			if !acc.readyIndices[idx] && looksLikeCompleteJSON(st.arguments.String()) {
				acc.readyIndices[idx] = true
			}
			out <- llm.ProviderEvent{
				Type: llm.EventToolArgsDelta,
				ToolArgsDelta: &llm.ToolArgsDelta{
					ToolCallIndex: idx,
					ToolName:      st.name,
					ArgsDelta:     tc.Function.Arguments,
					ArgsChars:     st.arguments.Len(),
					ReadyCount:    len(acc.readyIndices),
				},
			}
		}
		if !st.started && (st.id != "" || st.name != "") {
			st.started = true
			out <- llm.ProviderEvent{Type: llm.EventToolUseStart, ToolCall: &core.ToolCall{ID: st.id, Name: st.name}}
		}
	}
	if ch.FinishReason == "tool_calls" {
		out <- llm.ProviderEvent{Type: llm.EventToolUseStop}
		emitComplete(out, model, acc)
		return true, nil
	}
	return false, nil
}

func emitComplete(out chan<- llm.ProviderEvent, model string, acc *streamAccumulator) {
	calls := make([]core.ToolCall, 0, len(acc.callsByIndex))
	for i := 0; i < len(acc.callsByIndex); i++ {
		st := acc.callsByIndex[i]
		if st == nil {
			continue
		}
		calls = append(calls, core.ToolCall{ID: st.id, Name: st.name, Input: st.arguments.String()})
	}
	out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
		Content:   acc.content.String(),
		Reasoning: acc.reasoning.String(),
		ToolCalls: calls,
		Usage: llm.Usage{
			PromptTokens:          acc.usage.PromptTokens,
			CompletionTokens:      acc.usage.CompletionTokens,
			TotalTokens:           acc.usage.TotalTokens,
			PromptCacheHitTokens:  acc.usage.PromptCacheHitTokens,
			PromptCacheMissTokens: acc.usage.PromptCacheMissTokens,
			ReasoningReplayTokens: acc.reasoningReplayTokens,
		},
		Model:        model,
		FinishReason: mapFinishReason(acc.finishReason),
	}}
}

func estimateReasoningReplayTokens(messages []map[string]any) int {
	if len(messages) == 0 {
		return 0
	}
	var chars int
	for _, m := range messages {
		role, _ := m["role"].(string)
		if role != "assistant" {
			continue
		}
		rsn, _ := m["reasoning_content"].(string)
		if strings.TrimSpace(rsn) == "" {
			continue
		}
		chars += len(rsn)
	}
	if chars <= 0 {
		return 0
	}
	return chars / 4
}

func mapFinishReason(v string) core.FinishReason {
	switch v {
	case "tool_calls":
		return core.FinishReasonToolUse
	case "stop":
		return core.FinishReasonEndTurn
	default:
		return core.FinishReasonEndTurn
	}
}

func looksLikeCompleteJSON(s string) bool {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return false
	}
	var v any
	return json.Unmarshal([]byte(raw), &v) == nil
}

func toDeepSeekTools(tools []core.Tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		spec := core.DescribeTool(t)
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        spec.Name,
				"description": spec.Description,
				"parameters":  core.FlattenSchemaForModel(spec.Parameters),
			},
		})
	}
	return out
}

func toDeepSeekMessages(history []core.Message) []map[string]any {
	out := make([]map[string]any, 0, len(history))
	pendingToolCalls := map[string]struct{}{}
	flushPending := func() {
		for id := range pendingToolCalls {
			out = append(out, map[string]any{
				"role":         "tool",
				"tool_call_id": id,
				"content":      `{"success":false,"error":"missing tool result recovered before provider send","code":"missing_tool_result_recovered"}`,
			})
			delete(pendingToolCalls, id)
		}
	}
	for _, msg := range history {
		switch msg.Role {
		case core.RoleSystem:
			flushPending()
			out = append(out, map[string]any{"role": "system", "content": msg.Text})
		case core.RoleUser:
			flushPending()
			out = append(out, map[string]any{"role": "user", "content": msg.Text})
		case core.RoleAssistant:
			flushPending()
			m := map[string]any{
				"role":              "assistant",
				"content":           msg.Text,
				"reasoning_content": msg.Reasoning,
			}
			if len(msg.ToolCalls) > 0 {
				tcs := make([]map[string]any, 0, len(msg.ToolCalls))
				for _, tc := range msg.ToolCalls {
					tcs = append(tcs, map[string]any{
						"id":   tc.ID,
						"type": "function",
						"function": map[string]any{
							"name":      tc.Name,
							"arguments": tc.Input,
						},
					})
					if strings.TrimSpace(tc.ID) != "" {
						pendingToolCalls[tc.ID] = struct{}{}
					}
				}
				m["tool_calls"] = tcs
			}
			out = append(out, m)
		case core.RoleTool:
			for _, tr := range msg.ToolResults {
				if _, ok := pendingToolCalls[tr.ToolCallID]; !ok {
					continue
				}
				out = append(out, map[string]any{
					"role":         "tool",
					"tool_call_id": tr.ToolCallID,
					"content":      tr.Content,
				})
				delete(pendingToolCalls, tr.ToolCallID)
			}
		}
	}
	flushPending()
	return out
}
