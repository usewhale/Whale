package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/session"
)

type approvalProvider struct {
	calls     int
	histories [][]Message
}

func (p *approvalProvider) StreamResponse(_ context.Context, history []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	p.histories = append(p.histories, append([]Message(nil), history...))
	if p.calls == 1 {
		return eventStream(toolUseEvent(toolCall("tc-w-1", "write", `{"file_path":"a.txt","content":"x"}`)))
	}
	return eventStream(endTurnEvent("done"))
}

type requestUserInputProvider struct {
	calls int
}

func (p *requestUserInputProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	out := make(chan ProviderEvent, 1)
	p.calls++
	if p.calls == 1 {
		out <- ProviderEvent{
			Type: EventComplete,
			Response: &ProviderResponse{
				FinishReason: FinishReasonToolUse,
				ToolCalls: []ToolCall{
					{
						ID:    "rui-1",
						Name:  "request_user_input",
						Input: `{"questions":[{"header":"Mode","id":"mode","question":"Pick mode","options":[{"label":"Agent","description":"execute"},{"label":"Plan","description":"read-only"}]}]}`,
					},
				},
			},
		}
		close(out)
		return out
	}
	return eventStream(endTurnEvent("after-answer"))
}

func TestRequestUserInputRoundTrip(t *testing.T) {
	store := NewInMemoryStore()
	dir := t.TempDir()
	a := NewAgentWithRegistry(
		&requestUserInputProvider{},
		store,
		NewToolRegistry(nil),
		WithSessionsDir(dir),
		WithUserInputFunc(func(req UserInputRequest) (core.UserInputResponse, bool) {
			if req.ToolCall.Name != "request_user_input" || len(req.Questions) != 1 {
				t.Fatalf("unexpected user input request: %+v", req)
			}
			return core.UserInputResponse{
				Answers: []core.UserInputAnswer{
					{ID: "mode", Label: "Agent", Value: "Agent"},
				},
			}, true
		}),
	)
	msg, err := a.Run(context.Background(), "s-rui", "start")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if msg.Text != "after-answer" {
		t.Fatalf("unexpected final message: %+v", msg)
	}
	ust, err := session.LoadUserInputState(dir, "s-rui")
	if err != nil {
		t.Fatalf("load user input state: %v", err)
	}
	if ust.Pending {
		t.Fatalf("expected pending state cleared: %+v", ust)
	}
	all, _ := store.List(context.Background(), "s-rui")
	if len(all) < 3 {
		t.Fatalf("expected tool roundtrip messages, got %d", len(all))
	}
}

func TestApprovalRequiredAndDenied(t *testing.T) {
	store := NewInMemoryStore()
	prov := &approvalProvider{}
	asked := 0
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry([]Tool{writeLikeTool{}}),
		WithApprovalFunc(func(req ApprovalRequest) ApprovalDecision {
			asked++
			return ApprovalDeny
		}),
	)

	events, err := a.RunStream(context.Background(), "s-approval-deny", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawApproval bool
	var sawDenied bool
	var sawDone bool
	for ev := range events {
		if ev.Type == AgentEventTypeToolApprovalRequired {
			sawApproval = true
		}
		if ev.Type == AgentEventTypeToolResult && ev.Result != nil && ev.Result.IsError {
			if strings.Contains(ev.Result.Content, "approval_denied") {
				sawDenied = true
			}
		}
		if ev.Type == AgentEventTypeDone {
			sawDone = true
		}
	}
	if !sawApproval {
		t.Fatal("expected approval required event")
	}
	if !sawDenied {
		t.Fatal("expected approval denied tool result")
	}
	if asked != 1 {
		t.Fatalf("expected asked=1, got %d", asked)
	}
	if prov.calls != 1 {
		t.Fatalf("expected provider to stop after denied approval, got calls=%d", prov.calls)
	}
	if !sawDone {
		t.Fatal("expected turn to finish after denied approval")
	}
	assertApprovalDeniedMarker(t, store, "s-approval-deny", "write")
}

func TestApprovalCancelDoesNotPersistDeniedMarker(t *testing.T) {
	store := NewInMemoryStore()
	prov := &approvalProvider{}
	asked := 0
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry([]Tool{writeLikeTool{}}),
		WithApprovalFunc(func(req ApprovalRequest) ApprovalDecision {
			asked++
			return ApprovalCancel
		}),
	)

	events, err := a.RunStream(context.Background(), "s-approval-cancel", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawApproval bool
	var sawCancelled bool
	for ev := range events {
		if ev.Type == AgentEventTypeToolApprovalRequired {
			sawApproval = true
		}
		if ev.Type == AgentEventTypeTurnCancelled {
			sawCancelled = true
		}
		if ev.Type == AgentEventTypeDone {
			t.Fatalf("unexpected done event after approval cancel")
		}
		if ev.Type == AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.Content, "approval_denied") {
			t.Fatalf("approval cancel produced denial result: %+v", ev.Result)
		}
	}
	if !sawApproval {
		t.Fatal("expected approval required event")
	}
	if !sawCancelled {
		t.Fatal("expected turn cancelled event")
	}
	if asked != 1 {
		t.Fatalf("expected asked=1, got %d", asked)
	}
	if prov.calls != 1 {
		t.Fatalf("expected provider to stop after canceled approval, got calls=%d", prov.calls)
	}
	msgs, err := store.List(context.Background(), "s-approval-cancel")
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if historyContainsApprovalDeniedMarker(msgs, "write") {
		t.Fatalf("approval cancel should not persist approval-denied marker:\n%+v", msgs)
	}
	if !historyContainsInterruptedMarker(msgs) {
		t.Fatalf("expected approval cancel to persist interrupted marker:\n%+v", msgs)
	}
}

func TestApprovalDeniedMarkerIsVisibleToNextTurn(t *testing.T) {
	store := NewInMemoryStore()
	prov := &approvalProvider{}
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry([]Tool{writeLikeTool{}}),
		WithApprovalFunc(func(req ApprovalRequest) ApprovalDecision {
			return ApprovalDeny
		}),
	)

	events, err := a.RunStream(context.Background(), "s-approval-deny-next", "do the denied task")
	if err != nil {
		t.Fatalf("first run stream failed: %v", err)
	}
	for range events {
	}
	events, err = a.RunStream(context.Background(), "s-approval-deny-next", "make build")
	if err != nil {
		t.Fatalf("second run stream failed: %v", err)
	}
	for range events {
	}

	if prov.calls != 2 {
		t.Fatalf("expected provider calls=2, got %d", prov.calls)
	}
	if len(prov.histories) != 2 {
		t.Fatalf("expected two provider histories, got %d", len(prov.histories))
	}
	if !historyContainsApprovalDeniedMarker(prov.histories[1], "write") {
		t.Fatalf("expected second provider history to include approval-denied marker:\n%+v", prov.histories[1])
	}
}

type multiToolApprovalProvider struct{}

func (p *multiToolApprovalProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	return eventStream(toolUseEvent(
		toolCall("tc-w-1", "write", `{"file_path":"a.txt","content":"x"}`),
		toolCall("tc-count-1", "counting", `{}`),
	))
}

type countingTool struct {
	calls int
}

func (c *countingTool) Name() string { return "counting" }
func (c *countingTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	c.calls++
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "ok"}, nil
}

func TestApprovalDeniedSkipsRemainingToolCalls(t *testing.T) {
	store := NewInMemoryStore()
	counting := &countingTool{}
	a := NewAgentWithRegistry(
		&multiToolApprovalProvider{},
		store,
		NewToolRegistry([]Tool{writeLikeTool{}, counting}),
		WithApprovalFunc(func(req ApprovalRequest) ApprovalDecision {
			return ApprovalDeny
		}),
	)

	events, err := a.RunStream(context.Background(), "s-approval-deny-multi", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	for range events {
	}
	if counting.calls != 0 {
		t.Fatalf("expected later tool calls to be skipped after approval deny, got %d", counting.calls)
	}
	assertApprovalDeniedMarker(t, store, "s-approval-deny-multi", "write")
}

func assertApprovalDeniedMarker(t *testing.T, store interface {
	List(context.Context, string) ([]Message, error)
}, sessionID, toolName string) {
	t.Helper()
	msgs, err := store.List(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if !historyContainsApprovalDeniedMarker(msgs, toolName) {
		t.Fatalf("expected approval-denied marker for %s in history:\n%+v", toolName, msgs)
	}
}

func historyContainsApprovalDeniedMarker(msgs []Message, toolName string) bool {
	for _, msg := range msgs {
		if msg.Role != RoleUser || !msg.Hidden || msg.FinishReason != FinishReasonCanceled {
			continue
		}
		if strings.Contains(msg.Text, "<approval_denied>") &&
			strings.Contains(msg.Text, "tool: "+toolName) &&
			strings.Contains(msg.Text, "Do not retry or continue") {
			return true
		}
	}
	return false
}

func historyContainsInterruptedMarker(msgs []Message) bool {
	for _, msg := range msgs {
		if msg.Role != RoleUser || !msg.Hidden || msg.FinishReason != FinishReasonCanceled {
			continue
		}
		if strings.Contains(msg.Text, "<turn_aborted>") {
			return true
		}
	}
	return false
}

type approvalCacheProvider struct {
	calls int
}

func (p *approvalCacheProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	if p.calls == 1 || p.calls == 2 {
		return eventStream(toolUseEvent(toolCall("tc-c-1", "write", `{"file_path":"a.txt","content":"x"}`)))
	}
	return eventStream(endTurnEvent("done"))
}

func TestApprovalAllowOnceDoesNotCacheBySessionKey(t *testing.T) {
	store := NewInMemoryStore()
	prov := &approvalCacheProvider{}
	asked := 0
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry([]Tool{writeLikeTool{}}),
		WithApprovalFunc(func(req ApprovalRequest) ApprovalDecision {
			asked++
			return ApprovalAllow
		}),
	)

	if _, err := a.Run(context.Background(), "s-approval-cache-once", "t1"); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if asked != 2 {
		t.Fatalf("expected allow-once approval to ask twice for repeated key, got %d", asked)
	}
}

func TestApprovalAllowForSessionCachesBySessionKey(t *testing.T) {
	store := NewInMemoryStore()
	prov := &approvalCacheProvider{}
	asked := 0
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry([]Tool{writeLikeTool{}}),
		WithApprovalFunc(func(req ApprovalRequest) ApprovalDecision {
			asked++
			return ApprovalAllowForSession
		}),
	)

	if _, err := a.Run(context.Background(), "s-approval-cache", "t1"); err != nil {
		t.Fatalf("run1 failed: %v", err)
	}
	if _, err := a.Run(context.Background(), "s-approval-cache", "t2"); err != nil {
		t.Fatalf("run2 failed: %v", err)
	}
	if asked != 1 {
		t.Fatalf("expected asked once due to approval cache, got %d", asked)
	}
}

type namedNoopTool string

func (n namedNoopTool) Name() string { return string(n) }
func (n namedNoopTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: "ok"}, nil
}

type failingNamedTool string

func (f failingNamedTool) Name() string { return string(f) }
func (f failingNamedTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: `{"success":false,"error":"bad hunk","code":"patch_apply_failed"}`, IsError: true}, nil
}

type notFoundEditTool struct{}

func (n notFoundEditTool) Name() string { return "edit" }
func (n notFoundEditTool) Run(_ context.Context, call ToolCall) (ToolResult, error) {
	return ToolResult{ToolCallID: call.ID, Name: call.Name, Content: `{"success":false,"error":"file not found","code":"not_found"}`, IsError: true}, nil
}

type fileScopedApprovalProvider struct {
	calls int
}

func (p *fileScopedApprovalProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	switch p.calls {
	case 1:
		return eventStream(toolUseEvent(toolCall("tc-edit", "edit", `{"file_path":"a.txt","search":"old","replace":"new"}`)))
	case 3:
		return eventStream(toolUseEvent(toolCall("tc-write", "write", `{"file_path":"a.txt","content":"newer"}`)))
	default:
		return eventStream(endTurnEvent("done"))
	}
}

func TestApprovalAllowForSessionCachesEditAndWriteByFile(t *testing.T) {
	store := NewInMemoryStore()
	prov := &fileScopedApprovalProvider{}
	asked := 0
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry([]Tool{namedNoopTool("edit"), namedNoopTool("write")}),
		WithApprovalFunc(func(req ApprovalRequest) ApprovalDecision {
			asked++
			if got, want := req.Keys, []string{"file:a.txt"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("approval keys = %v, want %v", got, want)
			}
			return ApprovalAllowForSession
		}),
	)

	if _, err := a.Run(context.Background(), "s-file-approval", "edit"); err != nil {
		t.Fatalf("run1 failed: %v", err)
	}
	if _, err := a.Run(context.Background(), "s-file-approval", "write"); err != nil {
		t.Fatalf("run2 failed: %v", err)
	}
	if asked != 1 {
		t.Fatalf("expected edit approval to cover later write to same file, got %d approvals", asked)
	}
}

type failedFileApprovalProvider struct {
	calls int
}

func (p *failedFileApprovalProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	switch p.calls {
	case 1:
		return eventStream(toolUseEvent(toolCall("tc-bad-patch", "apply_patch", patchApprovalInput("*** Begin Patch\n*** Update File: a.txt\n@@\n-missing\n+new\n*** End Patch"))))
	case 2:
		return eventStream(toolUseEvent(toolCall("tc-write-after-fail", "write", `{"file_path":"a.txt","content":"new"}`)))
	default:
		return eventStream(endTurnEvent("done"))
	}
}

func TestApprovalAllowForSessionDoesNotCacheFailedFileMutation(t *testing.T) {
	store := NewInMemoryStore()
	prov := &failedFileApprovalProvider{}
	asked := 0
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry([]Tool{failingNamedTool("apply_patch"), namedNoopTool("write")}),
		WithApprovalFunc(func(req ApprovalRequest) ApprovalDecision {
			asked++
			return ApprovalAllowForSession
		}),
	)

	if _, err := a.Run(context.Background(), "s-failed-file-approval", "patch then write"); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if asked != 2 {
		t.Fatalf("expected failed file mutation not to cache approval for later write, got %d approvals", asked)
	}
}

type unclassifiedFailedFileApprovalProvider struct {
	calls int
}

func (p *unclassifiedFailedFileApprovalProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	switch p.calls {
	case 1:
		return eventStream(toolUseEvent(toolCall("tc-missing-edit", "edit", `{"file_path":"a.txt","search":"old","replace":"new"}`)))
	case 2:
		return eventStream(toolUseEvent(toolCall("tc-write-after-missing", "write", `{"file_path":"a.txt","content":"new"}`)))
	default:
		return eventStream(endTurnEvent("done"))
	}
}

func TestApprovalAllowForSessionDoesNotCacheUnclassifiedFailedFileMutation(t *testing.T) {
	store := NewInMemoryStore()
	prov := &unclassifiedFailedFileApprovalProvider{}
	asked := 0
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry([]Tool{notFoundEditTool{}, namedNoopTool("write")}),
		WithApprovalFunc(func(req ApprovalRequest) ApprovalDecision {
			asked++
			return ApprovalAllowForSession
		}),
	)

	if _, err := a.Run(context.Background(), "s-unclassified-failed-file-approval", "edit then write"); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if asked != 2 {
		t.Fatalf("expected unclassified failed file mutation not to cache approval for later write, got %d approvals", asked)
	}
}

type fallbackWriteApprovalProvider struct {
	calls int
}

func (p *fallbackWriteApprovalProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	if p.calls == 1 || p.calls == 2 {
		return eventStream(toolUseEvent(toolCall("tc-fallback-write", "write", `{"file_path":"a.txt","content":"x"}`)))
	}
	return eventStream(endTurnEvent("done"))
}

func TestApprovalAllowForSessionDoesNotCacheRecoveredReadonlyFallback(t *testing.T) {
	store := NewInMemoryStore()
	prov := &fallbackWriteApprovalProvider{}
	asked := 0
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry([]Tool{failWriteTool{}, readOnlyViewTool{}}),
		WithRecoveryPolicy(RecoveryPolicy{
			Enabled: true,
			Rules: map[FailureClass]RecoveryRule{
				FailureClassExecFailed: {Action: RecoveryActionFallbackReadOnly, MaxAttempts: 0},
			},
		}),
		WithApprovalFunc(func(req ApprovalRequest) ApprovalDecision {
			asked++
			return ApprovalAllowForSession
		}),
	)

	if _, err := a.Run(context.Background(), "s-fallback-file-approval", "fallback then write"); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if asked != 2 {
		t.Fatalf("expected readonly fallback not to cache file approval for later write, got %d approvals", asked)
	}
}

type patchScopedApprovalProvider struct {
	calls int
}

func (p *patchScopedApprovalProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	switch p.calls {
	case 1:
		return eventStream(toolUseEvent(toolCall("tc-patch-ab", "apply_patch", patchApprovalInput("*** Begin Patch\n*** Update File: a.txt\n@@\n-old\n+new\n*** Add File: b.txt\n+created\n*** End Patch"))))
	case 3:
		return eventStream(toolUseEvent(toolCall("tc-patch-a", "apply_patch", patchApprovalInput("*** Begin Patch\n*** Update File: a.txt\n@@\n-new\n+newer\n*** End Patch"))))
	case 5:
		return eventStream(toolUseEvent(toolCall("tc-patch-ac", "apply_patch", patchApprovalInput("*** Begin Patch\n*** Update File: a.txt\n@@\n-newer\n+final\n*** Add File: c.txt\n+created\n*** End Patch"))))
	default:
		return eventStream(endTurnEvent("done"))
	}
}

func TestApprovalAllowForSessionCachesApplyPatchByIndividualFiles(t *testing.T) {
	store := NewInMemoryStore()
	prov := &patchScopedApprovalProvider{}
	asked := 0
	a := NewAgentWithRegistry(
		prov,
		store,
		NewToolRegistry([]Tool{namedNoopTool("apply_patch")}),
		WithApprovalFunc(func(req ApprovalRequest) ApprovalDecision {
			asked++
			switch req.ToolCall.ID {
			case "tc-patch-ab":
				if got, want := req.Keys, []string{"file:a.txt", "file:b.txt"}; !reflect.DeepEqual(got, want) {
					t.Fatalf("first patch approval keys = %v, want %v", got, want)
				}
			case "tc-patch-ac":
				if got, want := req.Keys, []string{"file:a.txt", "file:c.txt"}; !reflect.DeepEqual(got, want) {
					t.Fatalf("third patch approval keys = %v, want %v", got, want)
				}
			default:
				t.Fatalf("unexpected approval for %s", req.ToolCall.ID)
			}
			return ApprovalAllowForSession
		}),
	)

	if _, err := a.Run(context.Background(), "s-patch-approval", "patch ab"); err != nil {
		t.Fatalf("run1 failed: %v", err)
	}
	if _, err := a.Run(context.Background(), "s-patch-approval", "patch a"); err != nil {
		t.Fatalf("run2 failed: %v", err)
	}
	if _, err := a.Run(context.Background(), "s-patch-approval", "patch ac"); err != nil {
		t.Fatalf("run3 failed: %v", err)
	}
	if asked != 2 {
		t.Fatalf("expected approvals for first file set and later new file only, got %d", asked)
	}
}

func patchApprovalInput(patch string) string {
	raw, _ := json.Marshal(map[string]string{"patch": patch})
	return string(raw)
}
