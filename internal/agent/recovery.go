package agent

import (
	"encoding/json"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

type FailureClass string

const (
	FailureClassTimeout          FailureClass = "timeout"
	FailureClassExecFailed       FailureClass = "exec_failed"
	FailureClassParseFailed      FailureClass = "parse_failed"
	FailureClassEmptyOutput      FailureClass = "empty_output"
	FailureClassPolicyDenied     FailureClass = "policy_denied"
	FailureClassApprovalDenied   FailureClass = "approval_denied"
	FailureClassPlanRequired     FailureClass = "plan_required"
	FailureClassPermissionDenied FailureClass = "permission_denied"
	FailureClassMCPToolError     FailureClass = "mcp_tool_error"
	FailureClassToolUnavailable  FailureClass = "tool_unavailable"
	FailureClassUnknown          FailureClass = "unknown"
)

type RecoveryAction string

const (
	RecoveryActionRetrySame        RecoveryAction = "retry_same"
	RecoveryActionRetryWithBackoff RecoveryAction = "retry_with_backoff"
	RecoveryActionFallbackReadOnly RecoveryAction = "fallback_readonly"
	RecoveryActionRequestReplan    RecoveryAction = "request_replan"
	RecoveryActionHardBlock        RecoveryAction = "hard_block"
	RecoveryActionPassThrough      RecoveryAction = "pass_through"
)

type RecoveryRule struct {
	Action      RecoveryAction
	MaxAttempts int
	BackoffMS   int
}

type RecoveryPolicy struct {
	Enabled bool
	Rules   map[FailureClass]RecoveryRule
}

func DefaultRecoveryPolicy() RecoveryPolicy {
	return RecoveryPolicy{
		Enabled: true,
		Rules: map[FailureClass]RecoveryRule{
			FailureClassTimeout:          {Action: RecoveryActionRetryWithBackoff, MaxAttempts: 2, BackoffMS: 200},
			FailureClassParseFailed:      {Action: RecoveryActionRetrySame, MaxAttempts: 1},
			FailureClassEmptyOutput:      {Action: RecoveryActionRetrySame, MaxAttempts: 1},
			FailureClassExecFailed:       {Action: RecoveryActionPassThrough, MaxAttempts: 0},
			FailureClassPolicyDenied:     {Action: RecoveryActionHardBlock, MaxAttempts: 0},
			FailureClassApprovalDenied:   {Action: RecoveryActionHardBlock, MaxAttempts: 0},
			FailureClassPlanRequired:     {Action: RecoveryActionHardBlock, MaxAttempts: 0},
			FailureClassPermissionDenied: {Action: RecoveryActionPassThrough, MaxAttempts: 0},
			FailureClassMCPToolError:     {Action: RecoveryActionPassThrough, MaxAttempts: 0},
			FailureClassToolUnavailable:  {Action: RecoveryActionPassThrough, MaxAttempts: 0},
			FailureClassUnknown:          {Action: RecoveryActionPassThrough, MaxAttempts: 0},
		},
	}
}

func classifyToolFailure(res core.ToolResult, dispatchErr error) FailureClass {
	if dispatchErr != nil {
		msg := strings.ToLower(dispatchErr.Error())
		if strings.Contains(msg, "timeout") {
			return FailureClassTimeout
		}
		return FailureClassUnknown
	}
	if !res.IsError() {
		if strings.TrimSpace(core.ToolResultModelText(res)) == "" {
			return FailureClassEmptyOutput
		}
		return ""
	}
	code := strings.TrimSpace(res.Code)
	errText := toolResultPayloadMessage(res)
	if code == "" {
		// Legacy result without channel-separated fields: fall back to
		// parsing the envelope text.
		var env struct {
			Code  string `json:"code"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal([]byte(core.ToolResultModelText(res)), &env); err == nil {
			code = strings.TrimSpace(env.Code)
			errText = env.Error
		}
	}
	switch code {
	case "cancelled", "canceled":
		return ""
	case "timeout":
		return FailureClassTimeout
	case "exec_failed":
		return FailureClassExecFailed
	case "mcp_call_failed":
		return FailureClassToolUnavailable
	case "mcp_tool_error":
		if isAccessDeniedText(errText) {
			return FailureClassPermissionDenied
		}
		return FailureClassMCPToolError
	case "invalid_input", "invalid_args", "invalid_pattern":
		return ""
	case "parse_failed", "invalid_plan_update":
		return FailureClassParseFailed
	case "not_found", "read_failed", "permission_denied", "read_required", "snapshot_mismatch", "stale_read", "search_not_found":
		return ""
	case "policy_denied", "read_only_turn_denied":
		return FailureClassPolicyDenied
	case "approval_denied":
		return FailureClassApprovalDenied
	case "plan_required":
		return FailureClassPlanRequired
	}
	lc := strings.ToLower(core.ToolResultModelText(res))
	if strings.Contains(lc, "timeout") {
		return FailureClassTimeout
	}
	return FailureClassUnknown
}

// toolResultPayloadMessage reads the reserved "message" key from the
// canonical payload.
func toolResultPayloadMessage(res core.ToolResult) string {
	payload, ok := res.Payload.(map[string]any)
	if !ok {
		return ""
	}
	msg, _ := payload["message"].(string)
	return msg
}

func isAccessDeniedText(s string) bool {
	lc := strings.ToLower(s)
	return strings.Contains(lc, "access denied") ||
		strings.Contains(lc, "outside allowed directories") ||
		strings.Contains(lc, "permission denied")
}
