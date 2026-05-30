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
	"regexp"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/compact"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/defaults"
	"github.com/usewhale/whale/internal/llm"
	llmretry "github.com/usewhale/whale/internal/llm/retry"
)

const (
	defaultBaseURL               = "https://api.deepseek.com"
	defaultStreamMaxAttempts     = 6
	maxToolResultReplayTokens    = 2000
	maxToolResultReplayChars     = 12 * 1024
	compactedToolResultKeepRunes = 3000
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

func supportsNativeTools(model string) bool {
	m := strings.ToLower(model)
	return strings.Contains(m, "deepseek") || strings.Contains(m, "codex")
}

var reToolCallCodeBlock = regexp.MustCompile("(?s)```json\\s*\\n?(.*?)\\n?```")
var reToolCallInline = regexp.MustCompile(`(?s)\{\s*"tool_call"\s*:\s*\{[^}]+\}\s*\}`)

type textToolCallPayload struct {
	ToolCall textToolCallInner `json:"tool_call"`
}

type textToolCallInner struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type altToolCallPayload struct {
	Command   string          `json:"command"`
	Tool      string          `json:"tool"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Args      json.RawMessage `json:"args"`
	Params    json.RawMessage `json:"params"`
	Input     json.RawMessage `json:"input"`
	Path      string          `json:"path"`
	Query     string          `json:"query"`
	Pattern   string          `json:"pattern"`
}

func parseTextToolCalls(content string) ([]core.ToolCall, string) {
	var jsonStr string
	cleanContent := content

	if matches := reToolCallCodeBlock.FindStringSubmatch(content); len(matches) >= 2 {
		jsonStr = strings.TrimSpace(matches[1])
		cleanContent = strings.TrimSpace(reToolCallCodeBlock.ReplaceAllString(content, ""))
	} else if match := reToolCallInline.FindString(content); match != "" {
		jsonStr = match
		cleanContent = strings.TrimSpace(strings.Replace(content, match, "", 1))
	} else {
		jsonStr, cleanContent = extractAnyToolJSON(content)
	}

	if jsonStr == "" {
		return nil, content
	}

	if tc := parseStandardToolCall(jsonStr); tc != nil {
		return []core.ToolCall{*tc}, cleanContent
	}

	if tc := parseAltToolCall(jsonStr); tc != nil {
		return []core.ToolCall{*tc}, cleanContent
	}

	return nil, content
}

func extractAnyToolJSON(content string) (string, string) {
	idx := strings.Index(content, "{")
	if idx < 0 {
		return "", content
	}
	rest := content[idx:]
	depth := 0
	inString := false
	escaped := false
	end := -1
	for i, ch := range rest {
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				end = i + 1
				break
			}
		}
	}
	if end <= 0 {
		return "", content
	}
	candidate := rest[:end]
	var test map[string]any
	if json.Unmarshal([]byte(candidate), &test) != nil {
		return "", content
	}
	if _, hasName := test["name"]; !hasName {
		if _, hasToolCall := test["tool_call"]; !hasToolCall {
			if _, hasCommand := test["command"]; !hasCommand {
				if _, hasTool := test["tool"]; !hasTool {
					return "", content
				}
			}
		}
	}
	cleanContent := strings.TrimSpace(content[:idx] + content[idx+end:])
	return candidate, cleanContent
}

func parseStandardToolCall(jsonStr string) *core.ToolCall {
	var parsed textToolCallPayload
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return nil
	}
	if parsed.ToolCall.Name == "" {
		return nil
	}
	argsJSON, err := json.Marshal(parsed.ToolCall.Arguments)
	if err != nil {
		argsJSON = []byte("{}")
	}
	return &core.ToolCall{
		ID:    fmt.Sprintf("call_%s", generateShortID()),
		Name:  parsed.ToolCall.Name,
		Input: string(argsJSON),
	}
}

func parseAltToolCall(jsonStr string) *core.ToolCall {
	var alt altToolCallPayload
	if err := json.Unmarshal([]byte(jsonStr), &alt); err != nil {
		return nil
	}

	tc := &core.ToolCall{
		ID: fmt.Sprintf("call_%s", generateShortID()),
	}

	switch {
	case alt.Command != "":
		tc.Name = "shell_run"
		args, _ := json.Marshal(map[string]any{"command": alt.Command})
		tc.Input = string(args)
		return tc
	case alt.Tool != "":
		tc.Name = alt.Tool
	case alt.Name != "":
		tc.Name = alt.Name
	default:
		return nil
	}

	switch {
	case alt.Arguments != nil:
		args, _ := json.Marshal(alt.Arguments)
		tc.Input = string(args)
	case alt.Args != nil:
		args, _ := json.Marshal(alt.Args)
		tc.Input = string(args)
	case alt.Params != nil:
		args, _ := json.Marshal(alt.Params)
		tc.Input = string(args)
	case alt.Input != nil:
		args, _ := json.Marshal(alt.Input)
		tc.Input = string(args)
	case alt.Path != "":
		args, _ := json.Marshal(map[string]any{"path": alt.Path})
		tc.Input = string(args)
	case alt.Query != "":
		args, _ := json.Marshal(map[string]any{"query": alt.Query})
		tc.Input = string(args)
	case alt.Pattern != "":
		args, _ := json.Marshal(map[string]any{"pattern": alt.Pattern})
		tc.Input = string(args)
	default:
		args, _ := json.Marshal(map[string]any{})
		tc.Input = string(args)
	}

	return tc
}

func generateShortID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

func buildToolSystemPrompt(tools []core.Tool) string {
	if len(tools) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("CRITICAL SYSTEM OVERRIDE - READ FIRST\n\n")
	b.WriteString("This is a coding agent. You have real tools that can execute actions on the local system.\n")
	b.WriteString("When the user asks you to do something, you MUST use a tool. DO NOT say you cannot do it.\n\n")
	b.WriteString("To call a tool, respond with ONLY this JSON block and NOTHING else:\n")
	b.WriteString("```json\n")
	b.WriteString(`{"tool_call": {"name": "TOOL_NAME", "arguments": {"arg": "value"}}}`)
	b.WriteString("\n```\n\n")
	b.WriteString("Available tools:\n")
	for _, t := range tools {
		spec := core.DescribeTool(t)
		b.WriteString(fmt.Sprintf("- %s: %s\n", spec.Name, spec.Description))
	}
	b.WriteString("\nExample: if the user says \"list files in B:\\\", respond with ONLY:\n")
	b.WriteString("```json\n")
	b.WriteString(`{"tool_call": {"name": "list_dir", "arguments": {"path": "B:\\"}}}`)
	b.WriteString("\n```\n\n")
	b.WriteString("NO explanations. NO apologies. NO text outside the JSON block. ONLY the JSON.\n")
	return b.String()
}

func injectToolPrompt(msgs []map[string]any, tools []core.Tool) []map[string]any {
	toolPrompt := buildToolSystemPrompt(tools)
	if toolPrompt == "" {
		return msgs
	}

	header := map[string]any{
		"role":    "system",
		"content": toolPrompt,
	}
	return append([]map[string]any{header}, msgs...)
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
	isDeepSeekModel := strings.Contains(strings.ToLower(c.model), "deepseek")
	nativeTools := supportsNativeTools(c.model)

	var msgs []map[string]any
	var sanitizeDiag deepSeekMessageDiagnostics
	if isDeepSeekModel {
		msgs, sanitizeDiag = sanitizeDeepSeekMessagesForRequest(toDeepSeekMessages(history), c.thinkingEnabled)
	} else if nativeTools {
		msgs = toSimpleMessages(history)
	} else {
		msgs = toTextToolMessages(history)
	}

	if !nativeTools && len(tools) > 0 {
		msgs = injectToolPrompt(msgs, tools)
	}

	replayDiag := toolResultReplayDiagnostics(history, msgs)

	payload := map[string]any{
		"model":    c.model,
		"stream":   true,
		"messages": msgs,
	}

	if isDeepSeekModel {
		payload["stream_options"] = map[string]any{"include_usage": true}
		payload["thinking"] = map[string]any{"type": "disabled"}
		if c.thinkingEnabled {
			payload["thinking"] = map[string]any{"type": "enabled"}
			if strings.TrimSpace(c.reasoningEffort) != "" {
				payload["reasoning_effort"] = c.reasoningEffort
			}
		}
	}

	if nativeTools && len(tools) > 0 {
		payload["tools"] = toDeepSeekTools(tools)
	}
	if c.maxTokens > 0 {
		payload["max_tokens"] = c.maxTokens
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	return c.streamWithRetries(ctx, body, msgs, sanitizeDiag, replayDiag, out)
}

func (c *Client) streamWithRetries(ctx context.Context, body []byte, msgs []map[string]any, sanitizeDiag deepSeekMessageDiagnostics, replayDiag deepSeekReplayDiagnostics, out chan<- llm.ProviderEvent) error {
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
				return deepSeekRequestError(err, sanitizeDiag)
			}
			delay := llmretry.Backoff(policy, requestAttempt, err)
			out <- retryScheduledEvent(requestAttempt, policy.MaxAttempts, delay, err, "request", false)
			requestAttempt++
			if sleepErr := c.retrySleeper(ctx, delay); sleepErr != nil {
				return sleepErr
			}
			continue
		}

		parseErr := parseSSE(resp.Body, c.model, estimateReasoningReplayTokens(msgs), replayDiag, out)
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

func deepSeekRequestError(err error, diag deepSeekMessageDiagnostics) error {
	var httpErr *llmretry.HTTPError
	if errors.As(err, &httpErr) {
		msg := fmt.Sprintf("deepseek %d: %s", httpErr.StatusCode, strings.TrimSpace(httpErr.Body))
		if httpErr.StatusCode == http.StatusBadRequest && !diag.empty() {
			msg += " (" + diag.String() + ")"
		}
		return errors.New(msg)
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

func parseSSE(r io.Reader, model string, replayTokens int, replayDiag deepSeekReplayDiagnostics, out chan<- llm.ProviderEvent) error {
	reader := bufio.NewReader(r)
	var dataLines []string
	acc := streamAccumulator{callsByIndex: map[int]*toolCallState{}, readyIndices: map[int]bool{}, reasoningReplayTokens: replayTokens, replayDiag: replayDiag}
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
			// Process any remaining data
			if len(dataLines) > 0 {
				data := strings.Join(dataLines, "\n")
				if done, perr := parseSSEData(data, model, out, &acc); perr != nil {
					return perr
				} else if done {
					return nil
				}
			}
			// If we already received some content, consider it complete even without [DONE]
			if acc.content.Len() > 0 || acc.finishReason != "" {
				emitComplete(out, model, &acc)
				return nil
			}
			// Otherwise, it's an incomplete stream
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
	replayDiag            deepSeekReplayDiagnostics
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

	content := acc.content.String()
	finishReason := mapFinishReason(acc.finishReason)

	if !supportsNativeTools(model) && len(calls) == 0 && content != "" {
		parsed, cleanContent := parseTextToolCalls(content)
		if len(parsed) > 0 {
			content = cleanContent
			calls = parsed
			finishReason = core.FinishReasonToolUse
		}
	}

	out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{
		Content:   content,
		Reasoning: acc.reasoning.String(),
		ToolCalls: calls,
		Usage: llm.Usage{
			PromptTokens:           acc.usage.PromptTokens,
			CompletionTokens:       acc.usage.CompletionTokens,
			TotalTokens:            acc.usage.TotalTokens,
			PromptCacheHitTokens:   acc.usage.PromptCacheHitTokens,
			PromptCacheMissTokens:  acc.usage.PromptCacheMissTokens,
			ReasoningReplayTokens:  acc.reasoningReplayTokens,
			ToolResultRawChars:     acc.replayDiag.rawChars,
			ToolResultReplayChars:  acc.replayDiag.replayChars,
			ToolResultRawTokens:    acc.replayDiag.rawTokens,
			ToolResultReplayTokens: acc.replayDiag.replayTokens,
			ToolResultTokensSaved:  acc.replayDiag.tokensSaved(),
			ToolResultsCompacted:   acc.replayDiag.compacted,
		},
		Model:        model,
		FinishReason: finishReason,
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

type deepSeekReplayDiagnostics struct {
	rawChars     int
	replayChars  int
	rawTokens    int
	replayTokens int
	compacted    int
}

func (d deepSeekReplayDiagnostics) tokensSaved() int {
	if d.rawTokens <= d.replayTokens {
		return 0
	}
	return d.rawTokens - d.replayTokens
}

func toolResultReplayDiagnostics(history []core.Message, messages []map[string]any) deepSeekReplayDiagnostics {
	diag, rawByCallID := rawToolResultReplayDiagnosticsWithContent(history)
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		if role != "tool" {
			continue
		}
		toolCallID, _ := msg["tool_call_id"].(string)
		content, _ := msg["content"].(string)
		diag.replayChars += len(content)
		diag.replayTokens += compact.EstimateTokens(content)
		if raw, ok := rawByCallID[toolCallID]; ok && content != raw {
			diag.compacted++
		}
	}
	return diag
}

func rawToolResultReplayDiagnostics(history []core.Message) deepSeekReplayDiagnostics {
	diag, _ := rawToolResultReplayDiagnosticsWithContent(history)
	return diag
}

func rawToolResultReplayDiagnosticsWithContent(history []core.Message) (deepSeekReplayDiagnostics, map[string]string) {
	var diag deepSeekReplayDiagnostics
	rawByCallID := map[string]string{}
	pendingToolCalls := map[string]struct{}{}
	flushPending := func() {
		for id := range pendingToolCalls {
			delete(pendingToolCalls, id)
		}
	}
	for _, msg := range history {
		switch msg.Role {
		case core.RoleSystem, core.RoleUser:
			flushPending()
		case core.RoleAssistant:
			flushPending()
			for _, tc := range msg.ToolCalls {
				if strings.TrimSpace(tc.ID) != "" {
					pendingToolCalls[tc.ID] = struct{}{}
				}
			}
		case core.RoleTool:
			for _, tr := range msg.ToolResults {
				if _, ok := pendingToolCalls[tr.ToolCallID]; !ok {
					continue
				}
				rawTokens := compact.EstimateTokens(tr.Content)
				diag.rawChars += len(tr.Content)
				diag.rawTokens += rawTokens
				rawByCallID[tr.ToolCallID] = tr.Content
				delete(pendingToolCalls, tr.ToolCallID)
			}
		}
	}
	flushPending()
	return diag, rawByCallID
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
					"content":      compactToolResultForReplay(tr.Content),
				})
				delete(pendingToolCalls, tr.ToolCallID)
			}
		}
	}
	flushPending()
	return out
}

type deepSeekMessageDiagnostics struct {
	strippedReasoning         int
	preservedToolReasoning    int
	syntheticToolResults      int
	droppedStrayTools         int
	repairedMissingToolCallID int
}

func (d deepSeekMessageDiagnostics) empty() bool {
	return d.strippedReasoning == 0 &&
		d.preservedToolReasoning == 0 &&
		d.syntheticToolResults == 0 &&
		d.droppedStrayTools == 0 &&
		d.repairedMissingToolCallID == 0
}

func (d deepSeekMessageDiagnostics) String() string {
	parts := []string{
		fmt.Sprintf("stripped_reasoning=%d", d.strippedReasoning),
		fmt.Sprintf("preserved_tool_reasoning=%d", d.preservedToolReasoning),
		fmt.Sprintf("synthetic_tool_results=%d", d.syntheticToolResults),
		fmt.Sprintf("dropped_stray_tools=%d", d.droppedStrayTools),
		fmt.Sprintf("repaired_missing_tool_call_ids=%d", d.repairedMissingToolCallID),
	}
	return "message diagnostics: " + strings.Join(parts, " ")
}

func sanitizeDeepSeekMessagesForRequest(messages []map[string]any, thinkingEnabled bool) ([]map[string]any, deepSeekMessageDiagnostics) {
	out := make([]map[string]any, 0, len(messages))
	var diag deepSeekMessageDiagnostics
	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		role, _ := msg["role"].(string)
		switch role {
		case "tool":
			diag.droppedStrayTools++
			continue
		case "assistant":
			calls, hasCalls := deepSeekToolCalls(msg)
			if !hasCalls {
				clean := cloneDeepSeekMessage(msg)
				if reasoningContentNonEmpty(clean) {
					diag.strippedReasoning++
				}
				delete(clean, "reasoning_content")
				out = append(out, clean)
				continue
			}

			clean := cloneDeepSeekMessage(msg)
			repairedCalls := cloneDeepSeekToolCalls(calls)
			needed := make([]string, 0, len(repairedCalls))
			for callIdx, call := range repairedCalls {
				id, _ := call["id"].(string)
				if strings.TrimSpace(id) == "" {
					id = fmt.Sprintf("whale_synthetic_call_%d_%d", i, callIdx)
					call["id"] = id
					diag.repairedMissingToolCallID++
				}
				needed = append(needed, id)
			}
			clean["tool_calls"] = repairedCalls
			if reasoningContentNonEmpty(clean) {
				diag.preservedToolReasoning++
			} else if thinkingEnabled {
				clean["reasoning_content"] = ""
			} else {
				delete(clean, "reasoning_content")
			}
			out = append(out, clean)

			next := i + 1
			for _, id := range needed {
				if next < len(messages) {
					nextMsg := messages[next]
					nextRole, _ := nextMsg["role"].(string)
					if nextRole == "tool" {
						toolID, _ := nextMsg["tool_call_id"].(string)
						if strings.TrimSpace(toolID) == id {
							out = append(out, cloneDeepSeekMessage(nextMsg))
							next++
							continue
						}
						if strings.TrimSpace(toolID) == "" {
							tool := cloneDeepSeekMessage(nextMsg)
							tool["tool_call_id"] = id
							out = append(out, tool)
							next++
							continue
						}
					}
				}
				out = append(out, syntheticMissingToolResult(id))
				diag.syntheticToolResults++
			}
			for next < len(messages) {
				nextRole, _ := messages[next]["role"].(string)
				if nextRole != "tool" {
					break
				}
				diag.droppedStrayTools++
				next++
			}
			i = next - 1
		default:
			out = append(out, cloneDeepSeekMessage(msg))
		}
	}
	return out, diag
}

func deepSeekToolCalls(msg map[string]any) ([]map[string]any, bool) {
	raw, ok := msg["tool_calls"]
	if !ok {
		return nil, false
	}
	switch calls := raw.(type) {
	case []map[string]any:
		if len(calls) == 0 {
			return nil, false
		}
		return calls, true
	case []any:
		out := make([]map[string]any, 0, len(calls))
		for _, item := range calls {
			call, ok := item.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, call)
		}
		if len(out) == 0 {
			return nil, false
		}
		return out, true
	default:
		return nil, false
	}
}

func cloneDeepSeekMessage(msg map[string]any) map[string]any {
	out := make(map[string]any, len(msg))
	for k, v := range msg {
		out[k] = v
	}
	return out
}

func cloneDeepSeekToolCalls(calls []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		cloned := cloneDeepSeekMessage(call)
		if fn, ok := call["function"].(map[string]any); ok {
			cloned["function"] = cloneDeepSeekMessage(fn)
		}
		out = append(out, cloned)
	}
	return out
}

func reasoningContentNonEmpty(msg map[string]any) bool {
	reasoning, _ := msg["reasoning_content"].(string)
	return strings.TrimSpace(reasoning) != ""
}

func syntheticMissingToolResult(id string) map[string]any {
	return map[string]any{
		"role":         "tool",
		"tool_call_id": id,
		"content":      `{"success":false,"error":"missing tool result recovered before provider send","code":"missing_tool_result_recovered"}`,
	}
}

func compactToolResultForReplay(content string) string {
	estimatedTokens := compact.EstimateTokens(content)
	if estimatedTokens <= maxToolResultReplayTokens && len(content) <= maxToolResultReplayChars {
		return content
	}
	runes := []rune(content)
	if len(runes) <= compactedToolResultKeepRunes {
		return content
	}
	headRunes := compactedToolResultKeepRunes / 2
	tailRunes := compactedToolResultKeepRunes - headRunes
	head := string(runes[:headRunes])
	tail := string(runes[len(runes)-tailRunes:])
	return fmt.Sprintf(
		"[tool result compacted for model replay]\n"+
			"original_estimated_tokens=%d original_chars=%d retained_head_runes=%d retained_tail_runes=%d\n"+
			"Full raw tool result remains in Whale session history; this provider replay is abbreviated.\n\n"+
			"--- head ---\n%s\n\n"+
			"--- omitted ---\n[... omitted %d runes from tool result replay ...]\n\n"+
			"--- tail ---\n%s",
		estimatedTokens,
		len(content),
		headRunes,
		tailRunes,
		head,
		len(runes)-headRunes-tailRunes,
		tail,
	)
}

func toTextToolMessages(history []core.Message) []map[string]any {
	out := make([]map[string]any, 0, len(history))
	for _, msg := range history {
		switch msg.Role {
		case core.RoleSystem:
			out = append(out, map[string]any{"role": "system", "content": msg.Text})
		case core.RoleUser:
			out = append(out, map[string]any{"role": "user", "content": msg.Text})
		case core.RoleAssistant:
			m := map[string]any{
				"role":    "assistant",
				"content": msg.Text,
			}
			if len(msg.ToolCalls) > 0 {
				var callDescs []string
				for _, tc := range msg.ToolCalls {
					callDescs = append(callDescs, fmt.Sprintf("%s(%s)", tc.Name, tc.Input))
				}
				if m["content"] == "" {
					m["content"] = "[Tool Calls: " + strings.Join(callDescs, ", ") + "]"
				}
			}
			out = append(out, m)
		case core.RoleTool:
			for _, tr := range msg.ToolResults {
				summary := compactToolResultForReplay(tr.Content)
				const maxPreview = 2000
				if len(summary) > maxPreview {
					summary = summary[:maxPreview] + "\n... (truncated)"
				}
				out = append(out, map[string]any{
					"role":    "user",
					"content": fmt.Sprintf("[Tool Result: %s]\n%s", tr.Name, summary),
				})
			}
		}
	}
	return out
}

func toSimpleMessages(history []core.Message) []map[string]any {
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
				"role":    "assistant",
				"content": msg.Text,
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
					"content":      compactToolResultForReplay(tr.Content),
				})
				delete(pendingToolCalls, tr.ToolCallID)
			}
		}
	}
	flushPending()
	return out
}
