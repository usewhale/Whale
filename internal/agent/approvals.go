package agent

import (
	"context"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/store"
)

func (a *Agent) ensureApprovalCacheLoaded(ctx context.Context, sessionID string) {
	if a.approvalCache.IsLoaded(sessionID) {
		return
	}
	as, ok := a.store.(store.ApprovalStore)
	if !ok {
		a.approvalCache.SetLoaded(sessionID)
		return
	}
	keys, err := as.GetApprovals(ctx, sessionID)
	if err == nil {
		a.approvalCache.Merge(sessionID, keys)
	}
	a.approvalCache.SetLoaded(sessionID)
}

func (a *Agent) persistApproval(ctx context.Context, sessionID, key string) {
	a.persistApprovals(ctx, sessionID, []string{key})
}

func (a *Agent) persistApprovals(ctx context.Context, sessionID string, keys []string) {
	as, ok := a.store.(store.ApprovalStore)
	if !ok {
		return
	}
	for _, key := range keys {
		_ = as.GrantApproval(ctx, sessionID, key)
	}
}

func (a *Agent) grantApprovals(ctx context.Context, sessionID string, call core.ToolCall, key string, keys []string, events chan<- AgentEvent) {
	a.approvalCache.GrantAll(sessionID, keys)
	a.persistApprovals(ctx, sessionID, keys)
	if events != nil {
		events <- AgentEvent{
			Type: AgentEventTypeToolApprovalGranted,
			ApprovalGrant: &ToolApprovalGranted{
				SessionID:  sessionID,
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Key:        key,
				Keys:       keys,
			},
		}
	}
}
