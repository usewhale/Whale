package agent

import (
	"context"
	"path/filepath"
	"testing"
)

type persistApprovalProvider struct {
	calls int
}

func (p *persistApprovalProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 1)
	p.calls++
	if p.calls%2 == 1 {
		out <- ProviderEvent{
			Type: EventComplete,
			Response: &ProviderResponse{
				FinishReason: FinishReasonToolUse,
				ToolCalls: []ToolCall{
					{ID: "tc-p", Name: "write", Input: `{"file_path":"a.txt","content":"x"}`},
				},
			},
		}
	} else {
		out <- ProviderEvent{
			Type: EventComplete,
			Response: &ProviderResponse{
				FinishReason: FinishReasonEndTurn,
				Content:      "done",
			},
		}
	}
	close(out)
	return out
}

func TestApprovalPersistsAcrossAgentInstances(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	reg := NewToolRegistry([]Tool{writeLikeTool{}})
	prov := &persistApprovalProvider{}
	asked1 := 0
	a1 := NewAgentWithRegistry(
		prov,
		store,
		reg,
		WithApprovalFunc(func(req ApprovalRequest) ApprovalDecision {
			asked1++
			return ApprovalAllowForSession
		}),
	)
	if _, err := a1.Run(context.Background(), "s-persist", "run1"); err != nil {
		t.Fatalf("run1 failed: %v", err)
	}
	if asked1 != 1 {
		t.Fatalf("expected first instance asked once, got %d", asked1)
	}

	asked2 := 0
	a2 := NewAgentWithRegistry(
		prov,
		store,
		reg,
		WithApprovalFunc(func(req ApprovalRequest) ApprovalDecision {
			asked2++
			return ApprovalAllowForSession
		}),
	)
	if _, err := a2.Run(context.Background(), "s-persist", "run2"); err != nil {
		t.Fatalf("run2 failed: %v", err)
	}
	if asked2 != 0 {
		t.Fatalf("expected second instance ask=0 due to persisted approval, got %d", asked2)
	}
}

type persistPatchApprovalProvider struct {
	calls int
}

func (p *persistPatchApprovalProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	switch p.calls {
	case 1:
		return eventStream(toolUseEvent(toolCall("tc-persist-ab", "apply_patch", patchApprovalInput("*** Begin Patch\n*** Update File: a.txt\n@@\n-old\n+new\n*** Add File: b.txt\n+created\n*** End Patch"))))
	case 3:
		return eventStream(toolUseEvent(toolCall("tc-persist-a", "apply_patch", patchApprovalInput("*** Begin Patch\n*** Update File: a.txt\n@@\n-new\n+newer\n*** End Patch"))))
	default:
		return eventStream(endTurnEvent("done"))
	}
}

func TestApprovalPersistsApplyPatchFileKeysAcrossAgentInstances(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	reg := NewToolRegistry([]Tool{namedNoopTool("apply_patch")})
	prov := &persistPatchApprovalProvider{}
	asked1 := 0
	a1 := NewAgentWithRegistry(
		prov,
		store,
		reg,
		WithApprovalFunc(func(req ApprovalRequest) ApprovalDecision {
			asked1++
			return ApprovalAllowForSession
		}),
	)
	if _, err := a1.Run(context.Background(), "s-persist-patch", "run1"); err != nil {
		t.Fatalf("run1 failed: %v", err)
	}
	if asked1 != 1 {
		t.Fatalf("expected first instance asked once, got %d", asked1)
	}

	asked2 := 0
	a2 := NewAgentWithRegistry(
		prov,
		store,
		reg,
		WithApprovalFunc(func(req ApprovalRequest) ApprovalDecision {
			asked2++
			return ApprovalAllowForSession
		}),
	)
	if _, err := a2.Run(context.Background(), "s-persist-patch", "run2"); err != nil {
		t.Fatalf("run2 failed: %v", err)
	}
	if asked2 != 0 {
		t.Fatalf("expected persisted file key to cover subset patch, got %d approvals", asked2)
	}
}
