package agent

import (
	"strings"
	"sync"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/telemetry"
)

const (
	approvalEventRequired       = "approval_required"
	approvalEventCachedAllowed  = "approval_cached_allowed"
	approvalEventAllowedOnce    = "approval_allowed_once"
	approvalEventAllowedForSess = "approval_allowed_for_session"
	approvalEventDenied         = "approval_denied"
	approvalEventCanceled       = "approval_canceled"
	approvalEventGrantPersisted = "approval_grant_persisted"
	approvalEventPolicyDenied   = "approval_policy_denied"
	approvalEventModeBlocked    = "approval_mode_blocked"
)

var approvalTelemetryAppendMu sync.Mutex

func (a *Agent) recordApprovalEvent(rec telemetry.ApprovalEvent) {
	if a == nil || strings.TrimSpace(a.sessionsDir) == "" {
		return
	}
	if strings.TrimSpace(rec.Source) == "" {
		rec.Source = "agent"
	}
	approvalTelemetryAppendMu.Lock()
	defer approvalTelemetryAppendMu.Unlock()
	_ = telemetry.AppendApprovalEvent(a.sessionsDir, rec, time.Now())
}

func (a *Agent) recordApprovalForCall(sessionID, model, assistantMessageID, event string, call core.ToolCall, decision policy.PolicyDecision, key string, keys []string, scope string) {
	a.recordApprovalEvent(telemetry.ApprovalEvent{
		Session:            sessionID,
		Model:              model,
		AssistantMessageID: assistantMessageID,
		ToolCallID:         call.ID,
		Tool:               call.Name,
		Event:              event,
		Reason:             decision.Reason,
		Code:               decision.Code,
		Phase:              decision.Phase,
		MatchedRule:        decision.MatchedRule,
		Key:                key,
		Keys:               keys,
		Scope:              scope,
	})
}

func approvalDecisionEvent(decision policy.ApprovalDecision) string {
	switch decision {
	case policy.ApprovalAllow:
		return approvalEventAllowedOnce
	case policy.ApprovalAllowForSession:
		return approvalEventAllowedForSess
	case policy.ApprovalCancel:
		return approvalEventCanceled
	default:
		return approvalEventDenied
	}
}
