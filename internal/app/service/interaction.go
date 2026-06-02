package service

import (
	"strings"
	"time"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/telemetry"
)

type pendingApproval struct {
	ch       chan policy.ApprovalDecision
	toolName string
	key      string
	keys     []string
	scope    string
}

func (s *Service) awaitApproval(req policy.ApprovalRequest) policy.ApprovalDecision {
	toolCallID := req.ToolCall.ID
	keys := policy.ApprovalRequestKeys(req)
	if blocked, out := s.app.RunPermissionRequestHook(req, s.hookObserver()); blocked {
		if out != "" {
			s.emit(Event{Kind: EventInfo, Text: out})
		}
		s.recordApprovalPromptEvent(req, "approval_prompt_hook_blocked", keys)
		s.emit(approvalDecisionEvent(req, keys, policy.ApprovalDeny))
		return policy.ApprovalDeny
	} else if out != "" {
		s.emit(Event{Kind: EventInfo, Text: out})
	}
	s.interactionMu.Lock()
	if s.shutdownRequested {
		s.interactionMu.Unlock()
		return policy.ApprovalCancel
	}
	s.approveMu.Lock()
	if s.sessionGrantAllLocked(req.SessionID, keys) {
		s.approveMu.Unlock()
		s.interactionMu.Unlock()
		s.recordApprovalPromptEvent(req, "approval_prompt_cached_allowed", keys)
		s.emit(approvalDecisionEvent(req, keys, policy.ApprovalAllowForSession))
		return policy.ApprovalAllowForSession
	}
	ch := make(chan policy.ApprovalDecision, 1)
	s.approvals[toolCallID] = pendingApproval{
		ch:       ch,
		toolName: req.ToolCall.Name,
		key:      firstApprovalKey(req, keys),
		keys:     append([]string(nil), keys...),
		scope:    policy.ApprovalScope(req.ToolCall),
	}
	s.approveMu.Unlock()
	s.interactionMu.Unlock()
	metadata := policy.ApprovalMetadata(req.ToolCall, keys, req.Metadata)
	s.recordApprovalPromptEvent(req, "approval_prompt_shown", keys)
	s.emit(Event{Kind: EventApprovalRequired, ToolCallID: toolCallID, ToolName: req.ToolCall.Name, Text: policy.ApprovalSummary(req.ToolCall), Metadata: metadata, Approval: protocolApprovalRequest(req, keys, metadata)})
	decision := <-ch
	s.recordApprovalPromptEvent(req, approvalPromptDecisionEvent(decision), keys)
	s.approveMu.Lock()
	delete(s.approvals, toolCallID)
	if decision == policy.ApprovalAllowForSession && !policy.ApprovalKeysFileScoped(keys) {
		s.grantSessionAllLocked(req.SessionID, keys)
	}
	s.approveMu.Unlock()
	return decision
}

func (s *Service) recordApprovalPromptEvent(req policy.ApprovalRequest, event string, keys []string) {
	if s == nil || s.app == nil {
		return
	}
	_ = telemetry.AppendApprovalEvent(s.app.SessionsDir(), telemetry.ApprovalEvent{
		Session:    req.SessionID,
		ToolCallID: req.ToolCall.ID,
		Tool:       req.ToolCall.Name,
		Event:      event,
		Source:     "service",
		Reason:     req.Reason,
		Code:       req.Code,
		Key:        req.Key,
		Keys:       keys,
		Scope:      policy.ApprovalScope(req.ToolCall),
	}, time.Now())
}

func approvalPromptDecisionEvent(decision policy.ApprovalDecision) string {
	switch decision {
	case policy.ApprovalAllow:
		return "approval_prompt_allowed_once"
	case policy.ApprovalAllowForSession:
		return "approval_prompt_allowed_for_session"
	case policy.ApprovalCancel:
		return "approval_prompt_canceled"
	default:
		return "approval_prompt_denied"
	}
}

func approvalDecisionEvent(req policy.ApprovalRequest, keys []string, decision policy.ApprovalDecision) Event {
	return Event{
		Kind:          EventApprovalDecision,
		ToolCallID:    req.ToolCall.ID,
		ToolName:      req.ToolCall.Name,
		ApprovalID:    firstApprovalKey(req, keys),
		Decision:      approvalDecisionName(decision),
		DecisionScope: approvalDecisionScope(decision, policy.ApprovalScope(req.ToolCall)),
		ApprovalKeys:  append([]string(nil), keys...),
	}
}

func (s *Service) resolveApproval(toolCallID string, decision policy.ApprovalDecision) {
	s.approveMu.Lock()
	pending, ok := s.approvals[toolCallID]
	if ok {
		delete(s.approvals, toolCallID)
	}
	s.approveMu.Unlock()
	if !ok {
		if s.interactionShutdownRequested() {
			return
		}
		s.emit(Event{Kind: EventError, Text: "no pending approval for tool call"})
		return
	}
	s.emit(Event{
		Kind:          EventApprovalDecision,
		ToolCallID:    toolCallID,
		ToolName:      pending.toolName,
		ApprovalID:    pending.key,
		Decision:      approvalDecisionName(decision),
		DecisionScope: approvalDecisionScope(decision, pending.scope),
		ApprovalKeys:  append([]string(nil), pending.keys...),
	})
	select {
	case pending.ch <- decision:
	default:
	}
}

func firstApprovalKey(req policy.ApprovalRequest, keys []string) string {
	for _, key := range keys {
		if strings.TrimSpace(key) != "" {
			return strings.TrimSpace(key)
		}
	}
	if strings.TrimSpace(req.Key) != "" {
		return strings.TrimSpace(req.Key)
	}
	return strings.TrimSpace(req.ToolCall.ID)
}

func approvalDecisionName(decision policy.ApprovalDecision) string {
	switch decision {
	case policy.ApprovalAllow:
		return "allow"
	case policy.ApprovalAllowForSession:
		return "allow_session"
	case policy.ApprovalCancel:
		return "cancel"
	default:
		return "deny"
	}
}

func approvalDecisionScope(decision policy.ApprovalDecision, scope string) string {
	if decision == policy.ApprovalAllow {
		return "this_time"
	}
	if decision == policy.ApprovalAllowForSession {
		if s := strings.TrimSpace(scope); s != "" {
			return s
		}
		return "session"
	}
	return ""
}

func (s *Service) sessionGrantLocked(sessionID, key string) bool {
	return s.sessionGrantAllLocked(sessionID, []string{key})
}

func (s *Service) sessionGrantAllLocked(sessionID string, keys []string) bool {
	if len(keys) == 0 {
		return false
	}
	bySession, ok := s.sessionGrants[sessionID]
	if !ok {
		return false
	}
	for _, key := range keys {
		if key == "" || !policy.ApprovalGrantKeysAllowAll(bySession, []string{key}) {
			return false
		}
	}
	return true
}

func (s *Service) grantSessionLocked(sessionID, key string) {
	s.grantSessionAllLocked(sessionID, []string{key})
}

func (s *Service) grantSessionAllLocked(sessionID string, keys []string) {
	bySession, ok := s.sessionGrants[sessionID]
	if !ok {
		bySession = map[string]bool{}
		s.sessionGrants[sessionID] = bySession
	}
	for _, key := range keys {
		if key != "" {
			bySession[key] = true
		}
	}
}

func (s *Service) syncApprovalGrant(grant *agent.ToolApprovalGranted) {
	if grant == nil {
		return
	}
	s.approveMu.Lock()
	s.grantSessionAllLocked(grant.SessionID, grant.Keys)
	s.approveMu.Unlock()
}

func (s *Service) awaitUserInput(req agent.UserInputRequest) (core.UserInputResponse, bool) {
	toolCallID := req.ToolCall.ID
	ch := make(chan userInputDecision, 1)
	s.interactionMu.Lock()
	if s.shutdownRequested {
		s.interactionMu.Unlock()
		return core.UserInputResponse{}, false
	}
	s.inputMu.Lock()
	s.inputs[toolCallID] = ch
	s.inputMu.Unlock()
	s.interactionMu.Unlock()
	s.emit(Event{Kind: EventUserInputRequired, ToolCallID: toolCallID, ToolName: req.ToolCall.Name, Questions: protocolUserInputQuestions(req.Questions)})
	decision := <-ch
	s.inputMu.Lock()
	delete(s.inputs, toolCallID)
	s.inputMu.Unlock()
	return decision.response, decision.ok
}

func (s *Service) resolveUserInput(toolCallID string, resp core.UserInputResponse, ok bool) {
	s.inputMu.Lock()
	ch, exists := s.inputs[toolCallID]
	if exists {
		delete(s.inputs, toolCallID)
	}
	s.inputMu.Unlock()
	if !exists {
		if s.interactionShutdownRequested() {
			return
		}
		s.emit(Event{Kind: EventError, Text: "no pending user input"})
		return
	}
	select {
	case ch <- userInputDecision{response: resp, ok: ok}:
	default:
	}
}

func (s *Service) cancelPendingInteractions() {
	s.interactionMu.Lock()
	s.shutdownRequested = true
	s.approveMu.Lock()
	approvals := make([]pendingApproval, 0, len(s.approvals))
	for id, pending := range s.approvals {
		approvals = append(approvals, pending)
		delete(s.approvals, id)
	}
	s.approveMu.Unlock()
	for _, pending := range approvals {
		select {
		case pending.ch <- policy.ApprovalCancel:
		default:
		}
	}

	s.inputMu.Lock()
	inputs := make([]chan userInputDecision, 0, len(s.inputs))
	for id, ch := range s.inputs {
		inputs = append(inputs, ch)
		delete(s.inputs, id)
	}
	s.inputMu.Unlock()
	s.interactionMu.Unlock()
	for _, ch := range inputs {
		select {
		case ch <- userInputDecision{}:
		default:
		}
	}
}

func (s *Service) resetInteractionShutdown() {
	s.interactionMu.Lock()
	s.shutdownRequested = false
	s.interactionMu.Unlock()
}

func (s *Service) interactionShutdownRequested() bool {
	s.interactionMu.Lock()
	defer s.interactionMu.Unlock()
	return s.shutdownRequested
}
