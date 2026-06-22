package agent

import (
	"context"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

// maxLeakedToolCallNudges bounds how many times a turn that ended by writing
// tool calls as plain text (instead of using the API tool-call channel) is
// scrubbed and nudged back toward the structured channel before the turn is
// allowed to finish anyway. Two is enough to recover a transient format slip
// without looping forever on a genuine final answer that merely *quotes* a
// wrapper (e.g. a reply that discusses this very bug).
const maxLeakedToolCallNudges = 2

// leakedToolCallNudgeText is the hidden user message injected after a leaked
// tool-call wrapper is scrubbed. It tells the model that text-formatted tool
// calls are never executed and to re-issue through the structured channel — or
// to answer normally if it is actually done.
const leakedToolCallNudgeText = "<tool_call_format_error>\nYour previous reply wrote tool calls as plain text (e.g. <tool_calls> ... <read_file ...> ... </tool_calls>) instead of using the API tool-call channel. Text-formatted tool calls are NOT executed — nothing ran. If you still need to call a tool, emit it through the structured tool-call API, not as message text. If you are actually finished, reply normally with no tool-call tags.\n</tool_call_format_error>"

// leakedToolCallWrapper is a start/end tag pair that DeepSeek occasionally
// emits in content when it forges a tool call as text rather than using the
// structured tool_calls channel. Containers are listed before <invoke so an
// outer wrapper is stripped whole (taking any nested <invoke>/<read_file> with
// it) before the inner-tag pass runs.
type leakedToolCallWrapper struct {
	start string
	end   string
}

var leakedToolCallWrappers = []leakedToolCallWrapper{
	{start: "<tool_calls", end: "</tool_calls>"},
	{start: "<function_calls>", end: "</function_calls>"},
	{start: "<deepseek:tool_call", end: "</deepseek:tool_call>"},
	{start: "<tool_call", end: "</tool_call>"},
	{start: "<invoke ", end: "</invoke>"},
}

// containsLeakedToolCall reports whether text carries a balanced leaked
// tool-call wrapper — a start marker with a matching end marker after it. It is
// deliberately conservative: a stray mention of "<tool_calls>" with no closing
// tag is not treated as a leak, so prose about tool calls survives untouched.
func containsLeakedToolCall(text string) bool {
	return stripLeakedToolCalls(text) != text
}

// stripLeakedToolCalls removes every balanced leaked tool-call wrapper span
// from text, leaving any surrounding prose intact. An unbalanced start marker
// (no closing tag) is left in place so a truncated or merely-quoted opening tag
// never eats the rest of the message.
func stripLeakedToolCalls(text string) string {
	out := text
	for _, w := range leakedToolCallWrappers {
		for {
			si := strings.Index(out, w.start)
			if si < 0 {
				break
			}
			after := si + len(w.start)
			rel := strings.Index(out[after:], w.end)
			if rel < 0 {
				break // unbalanced — leave this start marker alone
			}
			end := after + rel + len(w.end)
			out = out[:si] + out[end:]
		}
	}
	return out
}

// persistLeakedToolCallNudge stores the hidden nudge that follows a scrubbed
// leaked tool-call turn, mirroring persistPlanLoopNudge.
func (a *Agent) persistLeakedToolCallNudge(ctx context.Context, sessionID string) (core.Message, error) {
	return a.store.Create(ctx, core.Message{
		SessionID:    sessionID,
		Role:         core.RoleUser,
		Text:         leakedToolCallNudgeText,
		Hidden:       true,
		FinishReason: core.FinishReasonEndTurn,
	})
}
