package llm

import "context"
import "errors"

import "github.com/usewhale/whale/internal/core"
import llmretry "github.com/usewhale/whale/internal/llm/retry"

// ErrEmptyCompletion marks a terminal completion that carried no assistant
// content and no tool calls (e.g. a reasoning-only response). Providers wrap
// their terminal-empty errors with this so callers can recognize the case
// without depending on provider-internal error types.
var ErrEmptyCompletion = errors.New("provider returned no assistant content or tool calls")

type EventType string

const (
	EventContentDelta   EventType = "content_delta"
	EventReasoningDelta EventType = "reasoning_delta"
	EventToolArgsDelta  EventType = "tool_args_delta"
	EventToolUseStart   EventType = "tool_use_start"
	EventToolUseStop    EventType = "tool_use_stop"
	EventRetryScheduled EventType = "retry_scheduled"
	EventComplete       EventType = "complete"
	EventError          EventType = "error"
)

type ToolArgsDelta struct {
	ToolCallIndex int
	ToolName      string
	ArgsDelta     string
	ArgsChars     int
	ReadyCount    int
}

type Usage struct {
	PromptTokens             int
	CompletionTokens         int
	TotalTokens              int
	PromptCacheHitTokens     int
	PromptCacheMissTokens    int
	PrefixCompletionRequests int
	ReasoningReplayTokens    int
	ToolResultRawChars       int
	ToolResultReplayChars    int
	ToolResultRawTokens      int
	ToolResultReplayTokens   int
	ToolResultTokensSaved    int
	ToolResultsCompacted     int
}

type ProviderEvent struct {
	Type           EventType
	Content        string
	ReasoningDelta string
	ToolArgsDelta  *ToolArgsDelta
	ToolCall       *core.ToolCall
	Retry          *llmretry.Info
	Response       *ProviderResponse
	Err            error
}

type ProviderResponse struct {
	Content      string
	Reasoning    string
	ToolCalls    []core.ToolCall
	Usage        Usage
	Model        string
	FinishReason core.FinishReason
}

type Provider interface {
	StreamResponse(ctx context.Context, history []core.Message, tools []core.Tool) <-chan ProviderEvent
}

type PrefixCompletionProvider interface {
	StreamResponseWithPrefix(ctx context.Context, history []core.Message, prefix string, stop []string) <-chan ProviderEvent
}
