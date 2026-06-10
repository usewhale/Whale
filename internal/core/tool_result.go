package core

import (
	"encoding/json"
	"strings"
)

// ToolOutcome is the protocol-level disposition of a tool result. It is the
// single source for "is this an error" across the agent loop, the TUI, and
// telemetry; OutcomeNoResult exists so "searched fine, found nothing" stops
// being conflated with failure.
type ToolOutcome string

const (
	OutcomeSuccess   ToolOutcome = "success"
	OutcomeNoResult  ToolOutcome = "no_result"
	OutcomeFailure   ToolOutcome = "failure"
	OutcomeTimeout   ToolOutcome = "timeout"
	OutcomeCancelled ToolOutcome = "cancelled"
	OutcomeBlocked   ToolOutcome = "blocked"
)

// OutcomeForErrorCode maps a machine-readable error code to its outcome so
// every error producer classifies uniformly.
func OutcomeForErrorCode(code string) ToolOutcome {
	switch strings.TrimSpace(code) {
	case "timeout":
		return OutcomeTimeout
	case "cancelled", "canceled":
		return OutcomeCancelled
	case "policy_denied", "approval_denied", "permission_denied", "plan_required",
		"ask_mode_blocked", "plan_mode_blocked", "mode_blocked",
		"read_only_turn_denied", "tool_call_cap_reached":
		return OutcomeBlocked
	default:
		return OutcomeFailure
	}
}

// outcomeForEnvelope derives the outcome of an already-built envelope.
func outcomeForEnvelope(env ToolEnvelope) ToolOutcome {
	if env.OK || env.Success {
		return OutcomeSuccess
	}
	return OutcomeForErrorCode(env.Code)
}

// CanonicalizeToolPayload converts envelope data into the canonical payload
// form: a JSON-typed map (strings, float64, bool, nested maps/slices) that
// is byte- and type-identical whether read before persistence or after a
// save/load cycle. Envelope fields without a struct home travel as reserved
// keys ("message", "summary", "truncated").
func CanonicalizeToolPayload(env ToolEnvelope) map[string]any {
	out := map[string]any{}
	for k, v := range env.Data {
		out[k] = v
	}
	if msg := FirstNonEmpty(env.Message, env.Error); strings.TrimSpace(msg) != "" {
		out["message"] = msg
	}
	if strings.TrimSpace(env.Summary) != "" {
		out["summary"] = env.Summary
	}
	if env.Truncated {
		out["truncated"] = true
	}
	if _, exists := out["metadata"]; !exists {
		meta := env.Metadata
		if meta == nil {
			meta = env.Meta
		}
		if len(meta) > 0 {
			out["metadata"] = meta
		}
	}
	if len(out) == 0 {
		return nil
	}
	b, err := MarshalToolJSON(out)
	if err != nil {
		return out
	}
	var canonical map[string]any
	if err := json.Unmarshal(b, &canonical); err != nil {
		return out
	}
	return canonical
}

// NewToolResultFromEnvelope is the sanctioned producer for tool results.
// ModelText is rendered exactly once here; phase 1 renders it with the
// legacy envelope serializer so model-visible bytes are unchanged, phase 2
// swaps the renderer for plain text.
func NewToolResultFromEnvelope(call ToolCall, env ToolEnvelope, metadata map[string]any) ToolResult {
	outcome := outcomeForEnvelope(env)
	text, err := MarshalToolEnvelope(env)
	if err != nil {
		text = fallbackEnvelopeJSON(env)
	}
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Outcome:    outcome,
		Code:       env.Code,
		Payload:    CanonicalizeToolPayload(env),
		ModelText:  text,
		Content:    text,
		Metadata:   metadata,
		IsError:    outcome != OutcomeSuccess && outcome != OutcomeNoResult,
	}
}

// NewToolResultSuccess wraps map data in the standard success envelope.
func NewToolResultSuccess(call ToolCall, data map[string]any, metadata map[string]any) ToolResult {
	return NewToolResultFromEnvelope(call, NewToolSuccessEnvelope(data), metadata)
}

// NewToolResultError wraps an error code/message (with optional data) in the
// standard error envelope.
func NewToolResultError(call ToolCall, code, msg string, data map[string]any) ToolResult {
	env := NewToolErrorEnvelope(code, msg)
	if len(data) > 0 {
		env.Data = data
	}
	return NewToolResultFromEnvelope(call, env, nil)
}

func fallbackEnvelopeJSON(env ToolEnvelope) string {
	b, err := json.Marshal(map[string]string{
		"success": "false",
		"code":    env.Code,
		"message": FirstNonEmpty(env.Message, env.Error),
	})
	if err != nil {
		return `{"success":false,"code":"marshal_failed"}`
	}
	return string(b)
}

// ToolResultModelText returns the model-visible text of a result.
// Transitional: falls back to Content for results deserialized from
// legacy session files; once the legacy decoder populates ModelText on
// load, this collapses to the field read.
func ToolResultModelText(r ToolResult) string {
	if r.ModelText != "" {
		return r.ModelText
	}
	return r.Content
}

// ToolResultOutcome returns the protocol outcome, deriving it from legacy
// fields when the result predates the channel separation.
func ToolResultOutcome(r ToolResult) ToolOutcome {
	if r.Outcome != "" {
		return r.Outcome
	}
	return FinalizeToolResultChannels(r).Outcome
}

// FinalizeToolResultChannels backfills the channel-separated fields on a
// result produced outside the dispatch funnel (agent special tools, blocked
// markers, recovery wrappers, abort-skip placeholders). ModelText takes the
// Content bytes verbatim; Outcome/Code/Payload are derived exactly the way
// the legacy session decoder derives them, so a result finalized live and
// the same result reloaded from an old session file classify identically.
// Idempotent: results that already carry ModelText pass through unchanged.
func FinalizeToolResultChannels(res ToolResult) ToolResult {
	if res.ModelText != "" {
		return res
	}
	res.ModelText = res.Content
	env, parsed := ParseToolEnvelope(res.Content)
	if parsed {
		if res.Code == "" {
			res.Code = env.Code
		}
		if res.Payload == nil {
			res.Payload = CanonicalizeToolPayload(env)
		}
	}
	if res.Outcome == "" {
		if res.IsError {
			res.Outcome = OutcomeForErrorCode(res.Code)
		} else {
			res.Outcome = OutcomeSuccess
		}
	}
	return res
}

func (r ToolResult) outcomeIsError() bool {
	switch r.Outcome {
	case OutcomeSuccess, OutcomeNoResult:
		return false
	case "":
		// Legacy or hand-built results that predate Outcome: fall back to
		// the stored flag until the field is universally populated.
		return r.IsError
	}
	return true
}
