package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/policy/effects"
	"github.com/usewhale/whale/internal/store"
	"github.com/usewhale/whale/internal/telemetry"
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
	// External-directory grants are persisted at project scope so they widen
	// across every session in the same project; merge them into this session's
	// cache so they take effect for path-scoped approvals here.
	if scope := a.projectApprovalScope(); scope != "" && scope != sessionID {
		if projectKeys, err := as.GetApprovals(ctx, scope); err == nil {
			a.approvalCache.Merge(sessionID, projectKeys)
		}
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
	projectScope := a.projectApprovalScope()
	for _, key := range keys {
		target := sessionID
		// Directory grants are path-scoped, not session-specific: persist them
		// at project scope so a later session in the same project does not have
		// to re-approve the same directory subtree.
		if projectScope != "" && isExternalDirectoryGrant(key) {
			target = projectScope
		}
		_ = as.GrantApproval(ctx, target, key)
	}
}

// projectApprovalScope returns a stable pseudo-session ID used to persist
// project-scoped grants. It is derived from the original workspace (the real
// project even when running inside a worktree), falling back to the active
// workspace root. Returns "" when no project root is known, in which case
// grants stay session-scoped.
func (a *Agent) projectApprovalScope() string {
	root := strings.TrimSpace(a.originalWorkspace)
	if root == "" {
		root = strings.TrimSpace(a.workspaceRoot)
	}
	if root == "" {
		return ""
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = filepath.Clean(abs)
	}
	sum := sha256.Sum256([]byte(root))
	return "project-" + hex.EncodeToString(sum[:8])
}

func isExternalDirectoryGrant(key string) bool {
	grant, ok := effects.ParseGrantKey(key)
	return ok && grant.Kind == effects.ExternalDirectory
}

func (a *Agent) grantApprovals(ctx context.Context, sessionID string, call core.ToolCall, key string, keys []string, events chan<- AgentEvent) bool {
	a.approvalCache.GrantAll(sessionID, keys)
	a.persistApprovals(ctx, sessionID, keys)
	a.recordApprovalEvent(telemetry.ApprovalEvent{
		Session:    sessionID,
		ToolCallID: call.ID,
		Tool:       call.Name,
		Event:      approvalEventGrantPersisted,
		Key:        key,
		Keys:       keys,
		Scope:      policy.ApprovalScope(call),
	})
	if events != nil {
		return sendAgentEvent(ctx, events, AgentEvent{
			Type: AgentEventTypeToolApprovalGranted,
			ApprovalGrant: &ToolApprovalGranted{
				SessionID:  sessionID,
				ToolCallID: call.ID,
				ToolName:   call.Name,
				Key:        key,
				Keys:       keys,
			},
		})
	}
	return true
}
