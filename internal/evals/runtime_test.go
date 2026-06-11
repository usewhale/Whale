package evals

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/compact"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/store"
	"github.com/usewhale/whale/internal/tools"
)

func editApprovalPolicy() policy.ToolPolicy {
	return policy.DefaultToolPolicy{Rules: []policy.PermissionRule{
		{Permission: "edit", Pattern: "*", Action: policy.PermissionAsk},
	}}
}

type requestUserInputProvider struct {
	calls int
}

func (p *requestUserInputProvider) StreamResponse(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	p.calls++
	out := make(chan llm.ProviderEvent, 1)
	if p.calls == 1 {
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonToolUse,
				ToolCalls: []core.ToolCall{
					{
						ID:    "rui-1",
						Name:  "request_user_input",
						Input: `{"questions":[{"header":"Mode","id":"mode","question":"Pick mode","options":[{"label":"Agent","description":"execute"},{"label":"Plan","description":"read-only"}]}]}`,
					},
				},
			},
		}
	} else {
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonEndTurn,
				Content:      "after-answer",
			},
		}
	}
	close(out)
	return out
}

func TestRuntimeRequestUserInputRoundTrip(t *testing.T) {
	sessionsDir := t.TempDir()
	provider := &requestUserInputProvider{}
	answered := false
	a := agent.NewAgentWithRegistry(
		provider,
		store.NewInMemoryStore(),
		core.NewToolRegistry(nil),
		agent.WithSessionsDir(sessionsDir),
		agent.WithUserInputFunc(func(req agent.UserInputRequest) (core.UserInputResponse, bool) {
			answered = true
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

	msg, err := a.RunSession(context.Background(), "eval-user-input", "start")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if !answered {
		t.Fatal("expected user input handler to run")
	}
	if msg.Text != "after-answer" {
		t.Fatalf("unexpected final message: %+v", msg)
	}
	st, err := session.LoadUserInputState(sessionsDir, "eval-user-input")
	if err != nil {
		t.Fatalf("load user input state: %v", err)
	}
	if st.Pending {
		t.Fatalf("expected pending state cleared: %+v", st)
	}
}

func TestRuntimeRequestUserInputCancelPath(t *testing.T) {
	sessionsDir := t.TempDir()
	provider := &requestUserInputProvider{}
	a := agent.NewAgentWithRegistry(
		provider,
		store.NewInMemoryStore(),
		core.NewToolRegistry(nil),
		agent.WithSessionsDir(sessionsDir),
		agent.WithUserInputFunc(func(req agent.UserInputRequest) (core.UserInputResponse, bool) {
			return core.UserInputResponse{}, false
		}),
	)

	events, err := a.RunStream(context.Background(), "eval-user-input-cancel", "start")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawCancelled bool
	var sawResult bool
	for ev := range events {
		if ev.Type == agent.AgentEventTypeUserInputCancelled {
			sawCancelled = true
		}
		if ev.Type == agent.AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.ModelText, "user_input_cancelled") {
			sawResult = true
		}
	}
	if !sawCancelled {
		t.Fatal("expected user input cancelled event")
	}
	if !sawResult {
		t.Fatal("expected user_input_cancelled tool result")
	}
	st, err := session.LoadUserInputState(sessionsDir, "eval-user-input-cancel")
	if err != nil {
		t.Fatalf("load user input state: %v", err)
	}
	if st.Pending {
		t.Fatalf("expected pending state cleared after cancel: %+v", st)
	}
}

type invalidUserInputProvider struct{}

func (p *invalidUserInputProvider) StreamResponse(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 1)
	out <- llm.ProviderEvent{
		Type: llm.EventComplete,
		Response: &llm.ProviderResponse{
			FinishReason: core.FinishReasonToolUse,
			ToolCalls: []core.ToolCall{
				{ID: "rui-invalid-1", Name: "request_user_input", Input: `{"questions":[]}`},
			},
		},
	}
	close(out)
	return out
}

func TestRuntimeRequestUserInputInvalidPayload(t *testing.T) {
	a := agent.NewAgentWithRegistry(
		&invalidUserInputProvider{},
		store.NewInMemoryStore(),
		core.NewToolRegistry(nil),
		agent.WithSessionsDir(t.TempDir()),
	)

	events, err := a.RunStream(context.Background(), "eval-user-input-invalid", "start")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawInvalid bool
	var sawRequired bool
	for ev := range events {
		if ev.Type == agent.AgentEventTypeUserInputRequired {
			sawRequired = true
		}
		if ev.Type == agent.AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.ModelText, "invalid_request_user_input") {
			sawInvalid = true
		}
	}
	if sawRequired {
		t.Fatal("did not expect user input required event for invalid payload")
	}
	if !sawInvalid {
		t.Fatal("expected invalid_request_user_input result")
	}
}

func TestRuntimeRequestUserInputWithoutHandlerReturnsUnavailable(t *testing.T) {
	a := agent.NewAgentWithRegistry(
		&requestUserInputProvider{},
		store.NewInMemoryStore(),
		core.NewToolRegistry(nil),
		agent.WithSessionsDir(t.TempDir()),
	)

	events, err := a.RunStream(context.Background(), "eval-user-input-unavailable", "start")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawCancelled bool
	var sawUnavailable bool
	for ev := range events {
		if ev.Type == agent.AgentEventTypeUserInputCancelled {
			sawCancelled = true
		}
		if ev.Type == agent.AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.ModelText, "user_input_unavailable") {
			sawUnavailable = true
		}
	}
	if !sawCancelled || !sawUnavailable {
		t.Fatalf("expected cancelled+unavailable, got cancelled=%v unavailable=%v", sawCancelled, sawUnavailable)
	}
}

type approvalDeniedProvider struct {
	calls int
}

func (p *approvalDeniedProvider) StreamResponse(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	p.calls++
	out := make(chan llm.ProviderEvent, 1)
	out <- llm.ProviderEvent{
		Type: llm.EventComplete,
		Response: &llm.ProviderResponse{
			FinishReason: core.FinishReasonToolUse,
			ToolCalls: []core.ToolCall{
				{ID: "approval-1", Name: "write", Input: `{"file_path":"a.txt","content":"x"}`},
			},
		},
	}
	close(out)
	return out
}

func TestRuntimeApprovalDeniedStopsToolExecution(t *testing.T) {
	root := t.TempDir()
	toolset, err := tools.NewToolset(root)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	provider := &approvalDeniedProvider{}
	asked := 0
	a := agent.NewAgentWithRegistry(
		provider,
		store.NewInMemoryStore(),
		core.NewToolRegistry(toolset.Tools()),
		agent.WithToolPolicy(editApprovalPolicy()),
		agent.WithApprovalFunc(func(req policy.ApprovalRequest) policy.ApprovalDecision {
			asked++
			return policy.ApprovalDeny
		}),
	)

	events, err := a.RunStream(context.Background(), "eval-approval-denied", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawApproval bool
	var sawDenied bool
	for ev := range events {
		if ev.Type == agent.AgentEventTypeToolApprovalRequired {
			sawApproval = true
		}
		if ev.Type == agent.AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.ModelText, "approval_denied") {
			sawDenied = true
		}
	}
	if !sawApproval {
		t.Fatal("expected approval required event")
	}
	if !sawDenied {
		t.Fatal("expected approval denied result")
	}
	if asked != 1 {
		t.Fatalf("expected one approval prompt, got %d", asked)
	}
	if provider.calls != 1 {
		t.Fatalf("expected provider to stop after denial, got calls=%d", provider.calls)
	}
}

type multiToolApprovalProvider struct{}

func (p *multiToolApprovalProvider) StreamResponse(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 1)
	out <- llm.ProviderEvent{
		Type: llm.EventComplete,
		Response: &llm.ProviderResponse{
			FinishReason: core.FinishReasonToolUse,
			ToolCalls: []core.ToolCall{
				{ID: "approval-write-1", Name: "write", Input: `{"file_path":"a.txt","content":"x"}`},
				{ID: "approval-count-1", Name: "counting", Input: `{}`},
			},
		},
	}
	close(out)
	return out
}

type countingTool struct {
	calls int
}

func (c *countingTool) Name() string { return "counting" }
func (c *countingTool) Run(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	c.calls++
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: "ok"}, nil
}

func TestRuntimeApprovalDeniedSkipsRemainingToolCalls(t *testing.T) {
	counting := &countingTool{}
	a := agent.NewAgentWithRegistry(
		&multiToolApprovalProvider{},
		store.NewInMemoryStore(),
		core.NewToolRegistry([]core.Tool{writeLikeTool{}, counting}),
		agent.WithToolPolicy(editApprovalPolicy()),
		agent.WithApprovalFunc(func(req policy.ApprovalRequest) policy.ApprovalDecision {
			return policy.ApprovalDeny
		}),
	)

	events, err := a.RunStream(context.Background(), "eval-approval-deny-multi", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	for range events {
	}
	if counting.calls != 0 {
		t.Fatalf("expected later tool calls to be skipped after approval denial, got %d", counting.calls)
	}
}

type execFailureProvider struct{}

func (p *execFailureProvider) StreamResponse(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 1)
	out <- llm.ProviderEvent{
		Type: llm.EventComplete,
		Response: &llm.ProviderResponse{
			FinishReason: core.FinishReasonToolUse,
			ToolCalls: []core.ToolCall{
				{ID: "exec-fail-1", Name: "shell_run", Input: `{"command":"exit 7"}`},
			},
		},
	}
	close(out)
	return out
}

func TestRuntimeExecFailurePassesThroughWithoutReplan(t *testing.T) {
	root := t.TempDir()
	toolset, err := tools.NewToolset(root)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	a := agent.NewAgentWithRegistry(
		&execFailureProvider{},
		store.NewInMemoryStore(),
		core.NewToolRegistry(toolset.Tools()),
		agent.WithToolPolicy(policy.RulePolicy{Default: policy.PermissionAllow}),
	)

	events, err := a.RunStream(context.Background(), "eval-exec-fail", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawExecFailed bool
	for ev := range events {
		if ev.Type == agent.AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.ModelText, `error (request_replan)`) {
			t.Fatalf("unexpected request_replan for exec failure: %s", ev.Result.ModelText)
		}
		if ev.Type == agent.AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.ModelText, `exit 7`) {
			sawExecFailed = true
		}
	}
	if !sawExecFailed {
		t.Fatal("expected original exec_failed tool result")
	}
}

type execTimeoutProvider struct{}

func (p *execTimeoutProvider) StreamResponse(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 1)
	out <- llm.ProviderEvent{
		Type: llm.EventComplete,
		Response: &llm.ProviderResponse{
			FinishReason: core.FinishReasonToolUse,
			ToolCalls: []core.ToolCall{
				{ID: "exec-timeout-1", Name: "shell_run", Input: `{"command":"sleep 1","timeout_ms":10}`},
			},
		},
	}
	close(out)
	return out
}

func TestRuntimeExecYieldTimeoutReturnsRunningTaskWithoutRetry(t *testing.T) {
	root := t.TempDir()
	toolset, err := tools.NewToolset(root)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	a := agent.NewAgentWithRegistry(
		&execTimeoutProvider{},
		store.NewInMemoryStore(),
		core.NewToolRegistry(toolset.Tools()),
		agent.WithToolPolicy(policy.RulePolicy{Default: policy.PermissionAllow}),
	)

	events, err := a.RunStream(context.Background(), "eval-exec-timeout", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawScheduled bool
	var sawAttempt bool
	var sawRunning bool
	for ev := range events {
		if ev.Type == agent.AgentEventTypeToolRecoveryScheduled {
			sawScheduled = true
		}
		if ev.Type == agent.AgentEventTypeToolRecoveryAttempt {
			sawAttempt = true
		}
		if ev.Type == agent.AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.ModelText, `running in background (task_id=`) {
			sawRunning = true
		}
	}
	if sawScheduled || sawAttempt {
		t.Fatalf("yield timeout should not trigger recovery, got scheduled=%v attempt=%v", sawScheduled, sawAttempt)
	}
	if !sawRunning {
		t.Fatal("expected running task result")
	}
}

type backgroundShellProvider struct{}

func (p *backgroundShellProvider) StreamResponse(_ context.Context, history []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 1)
	lastUser := ""
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == core.RoleUser {
			lastUser = history[i].Text
			break
		}
	}
	switch strings.TrimSpace(lastUser) {
	case "start":
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonToolUse,
				ToolCalls: []core.ToolCall{
					{ID: "bg-start-1", Name: "shell_run", Input: `{"command":"sleep 1","background":true}`},
				},
			},
		}
	case "wait":
		taskID, err := shellTaskIDFromHistory(history)
		if err != nil {
			out <- llm.ProviderEvent{Type: llm.EventError, Err: err}
			close(out)
			return out
		}
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonToolUse,
				ToolCalls: []core.ToolCall{
					{ID: "bg-wait-1", Name: "shell_wait", Input: `{"task_id":"` + taskID + `","timeout_ms":1}`},
				},
			},
		}
	default:
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonEndTurn,
				Content:      "done",
			},
		}
	}
	close(out)
	return out
}

func TestRuntimeShellWaitReturnsRunningOnShortTimeout(t *testing.T) {
	root := t.TempDir()
	toolset, err := tools.NewToolset(root)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	a := agent.NewAgentWithRegistry(
		&backgroundShellProvider{},
		store.NewInMemoryStore(),
		core.NewToolRegistry(toolset.Tools()),
		agent.WithToolPolicy(policy.RulePolicy{Default: policy.PermissionAllow}),
	)

	if _, err := a.RunSession(context.Background(), "eval-bg-running", "start"); err != nil {
		t.Fatalf("start run failed: %v", err)
	}
	events, err := a.RunStream(context.Background(), "eval-bg-running", "wait")
	if err != nil {
		t.Fatalf("wait run failed: %v", err)
	}
	var sawRunning bool
	var toolResults []string
	for ev := range events {
		if ev.Type == agent.AgentEventTypeToolResult && ev.Result != nil {
			toolResults = append(toolResults, ev.Result.ModelText)
			if strings.Contains(ev.Result.ModelText, `running in background`) {
				sawRunning = true
			}
		}
	}
	if !sawRunning {
		t.Fatalf("expected shell_wait to return running on short timeout, got %v", toolResults)
	}
}

type shellWaitNotFoundProvider struct{}

func (p *shellWaitNotFoundProvider) StreamResponse(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 1)
	out <- llm.ProviderEvent{
		Type: llm.EventComplete,
		Response: &llm.ProviderResponse{
			FinishReason: core.FinishReasonToolUse,
			ToolCalls: []core.ToolCall{
				{ID: "wait-missing-1", Name: "shell_wait", Input: `{"task_id":"missing-task","timeout_ms":5}`},
			},
		},
	}
	close(out)
	return out
}

func TestRuntimeShellWaitUnknownTaskReturnsNotFound(t *testing.T) {
	root := t.TempDir()
	toolset, err := tools.NewToolset(root)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	a := agent.NewAgentWithRegistry(
		&shellWaitNotFoundProvider{},
		store.NewInMemoryStore(),
		core.NewToolRegistry(toolset.Tools()),
		agent.WithToolPolicy(policy.RulePolicy{Default: policy.PermissionAllow}),
		agent.WithRecoveryPolicy(agent.RecoveryPolicy{Enabled: false}),
	)

	events, err := a.RunStream(context.Background(), "eval-shellwait-missing", "wait")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawNotFound bool
	for ev := range events {
		if ev.Type == agent.AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.ModelText, `error (not_found)`) {
			sawNotFound = true
		}
	}
	if !sawNotFound {
		t.Fatal("expected not_found from shell_wait on unknown task id")
	}
}

type backgroundShellDoneProvider struct{}

func (p *backgroundShellDoneProvider) StreamResponse(_ context.Context, history []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 1)
	lastUser := ""
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == core.RoleUser {
			lastUser = history[i].Text
			break
		}
	}
	switch strings.TrimSpace(lastUser) {
	case "start":
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonToolUse,
				ToolCalls: []core.ToolCall{
					{ID: "bg-done-start-1", Name: "shell_run", Input: `{"command":"printf done","background":true}`},
				},
			},
		}
	case "wait":
		taskID, err := shellTaskIDFromHistory(history)
		if err != nil {
			out <- llm.ProviderEvent{Type: llm.EventError, Err: err}
			close(out)
			return out
		}
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonToolUse,
				ToolCalls: []core.ToolCall{
					{ID: "bg-done-wait-1", Name: "shell_wait", Input: `{"task_id":"` + taskID + `","timeout_ms":5000}`},
				},
			},
		}
	default:
		out <- llm.ProviderEvent{
			Type:     llm.EventComplete,
			Response: &llm.ProviderResponse{FinishReason: core.FinishReasonEndTurn, Content: "done"},
		}
	}
	close(out)
	return out
}

func TestRuntimeShellWaitReturnsExitedResult(t *testing.T) {
	root := t.TempDir()
	toolset, err := tools.NewToolset(root)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	a := agent.NewAgentWithRegistry(
		&backgroundShellDoneProvider{},
		store.NewInMemoryStore(),
		core.NewToolRegistry(toolset.Tools()),
		agent.WithToolPolicy(policy.RulePolicy{Default: policy.PermissionAllow}),
	)
	if _, err := a.RunSession(context.Background(), "eval-bg-done", "start"); err != nil {
		t.Fatalf("start run failed: %v", err)
	}
	events, err := a.RunStream(context.Background(), "eval-bg-done", "wait")
	if err != nil {
		t.Fatalf("wait run failed: %v", err)
	}
	var sawExited bool
	var collected []string
	for ev := range events {
		if ev.Type == agent.AgentEventTypeToolResult && ev.Result != nil {
			collected = append(collected, ev.Result.ModelText)
			if strings.Contains(ev.Result.ModelText, `exit 0`) && strings.Contains(ev.Result.ModelText, "done") {
				sawExited = true
			}
		}
	}
	if !sawExited {
		t.Fatalf("expected shell_wait exited result with stdout, got %q", collected)
	}
}

type backgroundShellFailProvider struct{}

func (p *backgroundShellFailProvider) StreamResponse(_ context.Context, history []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 1)
	lastUser := ""
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == core.RoleUser {
			lastUser = history[i].Text
			break
		}
	}
	switch strings.TrimSpace(lastUser) {
	case "start":
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonToolUse,
				ToolCalls: []core.ToolCall{
					{ID: "bg-fail-start-1", Name: "shell_run", Input: `{"command":"exit 7","background":true}`},
				},
			},
		}
	case "wait":
		taskID, err := shellTaskIDFromHistory(history)
		if err != nil {
			out <- llm.ProviderEvent{Type: llm.EventError, Err: err}
			close(out)
			return out
		}
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonToolUse,
				ToolCalls: []core.ToolCall{
					{ID: "bg-fail-wait-1", Name: "shell_wait", Input: `{"task_id":"` + taskID + `","timeout_ms":5000}`},
				},
			},
		}
	default:
		out <- llm.ProviderEvent{
			Type:     llm.EventComplete,
			Response: &llm.ProviderResponse{FinishReason: core.FinishReasonEndTurn, Content: "done"},
		}
	}
	close(out)
	return out
}

func TestRuntimeShellWaitReturnsFailedResult(t *testing.T) {
	root := t.TempDir()
	toolset, err := tools.NewToolset(root)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	a := agent.NewAgentWithRegistry(
		&backgroundShellFailProvider{},
		store.NewInMemoryStore(),
		core.NewToolRegistry(toolset.Tools()),
		agent.WithToolPolicy(policy.RulePolicy{Default: policy.PermissionAllow}),
	)
	if _, err := a.RunSession(context.Background(), "eval-bg-fail", "start"); err != nil {
		t.Fatalf("start run failed: %v", err)
	}
	events, err := a.RunStream(context.Background(), "eval-bg-fail", "wait")
	if err != nil {
		t.Fatalf("wait run failed: %v", err)
	}
	var sawFailed bool
	for ev := range events {
		if ev.Type == agent.AgentEventTypeToolResult && ev.Result != nil &&
			strings.Contains(ev.Result.ModelText, `exit 7`) {
			sawFailed = true
		}
	}
	if !sawFailed {
		t.Fatal("expected shell_wait failed result with exit code")
	}
}

type writeEchoTool struct{}

func (w writeEchoTool) Name() string { return "write" }
func (w writeEchoTool) Run(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: "ok:" + call.Input}, nil
}

type approvalCacheProvider struct {
	calls int
}

func (p *approvalCacheProvider) StreamResponse(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	p.calls++
	out := make(chan llm.ProviderEvent, 1)
	if p.calls == 1 || p.calls == 2 {
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonToolUse,
				ToolCalls: []core.ToolCall{
					{ID: "approval-cache-1", Name: "write", Input: `{"file_path":"a.txt","content":"x"}`},
				},
			},
		}
	} else {
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonEndTurn,
				Content:      "done",
			},
		}
	}
	close(out)
	return out
}

func TestRuntimeApprovalCacheBySessionKey(t *testing.T) {
	root := t.TempDir()
	toolset, err := tools.NewToolset(root)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	provider := &approvalCacheProvider{}
	asked := 0
	a := agent.NewAgentWithRegistry(
		provider,
		store.NewInMemoryStore(),
		core.NewToolRegistry(toolset.Tools()),
		agent.WithToolPolicy(editApprovalPolicy()),
		agent.WithApprovalFunc(func(req policy.ApprovalRequest) policy.ApprovalDecision {
			asked++
			return policy.ApprovalAllowForSession
		}),
	)

	if _, err := a.RunSession(context.Background(), "eval-approval-cache", "t1"); err != nil {
		t.Fatalf("run1 failed: %v", err)
	}
	if _, err := a.RunSession(context.Background(), "eval-approval-cache", "t2"); err != nil {
		t.Fatalf("run2 failed: %v", err)
	}
	if asked != 1 {
		t.Fatalf("expected one approval due to cache, got %d", asked)
	}
}

type oneApprovalRunProvider struct {
	calls int
}

func (p *oneApprovalRunProvider) StreamResponse(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 1)
	p.calls++
	if p.calls%2 == 1 {
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonToolUse,
				ToolCalls: []core.ToolCall{
					{ID: "approval-once-1", Name: "write", Input: `{"file_path":"a.txt","content":"x"}`},
				},
			},
		}
	} else {
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonEndTurn,
				Content:      "done",
			},
		}
	}
	close(out)
	return out
}

func TestRuntimeApprovalCacheDoesNotCrossSessions(t *testing.T) {
	root := t.TempDir()
	toolset, err := tools.NewToolset(root)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	provider := &oneApprovalRunProvider{}
	asked := 0
	a := agent.NewAgentWithRegistry(
		provider,
		store.NewInMemoryStore(),
		core.NewToolRegistry(toolset.Tools()),
		agent.WithToolPolicy(editApprovalPolicy()),
		agent.WithApprovalFunc(func(req policy.ApprovalRequest) policy.ApprovalDecision {
			asked++
			return policy.ApprovalAllowForSession
		}),
	)

	if _, err := a.RunSession(context.Background(), "eval-approval-cache-a", "t1"); err != nil {
		t.Fatalf("run1 failed: %v", err)
	}
	if _, err := a.RunSession(context.Background(), "eval-approval-cache-b", "t2"); err != nil {
		t.Fatalf("run2 failed: %v", err)
	}
	if asked != 2 {
		t.Fatalf("expected approval prompt per session, got %d", asked)
	}
}

type persistApprovalProvider struct {
	calls int
}

func (p *persistApprovalProvider) StreamResponse(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 1)
	p.calls++
	if p.calls%2 == 1 {
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonToolUse,
				ToolCalls: []core.ToolCall{
					{ID: "persist-approval-1", Name: "write", Input: `{"file_path":"a.txt","content":"x"}`},
				},
			},
		}
	} else {
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonEndTurn,
				Content:      "done",
			},
		}
	}
	close(out)
	return out
}

func TestRuntimeApprovalPersistsAcrossAgentInstances(t *testing.T) {
	dir := t.TempDir()
	msgStore, err := store.NewJSONLStore(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatalf("new jsonl store: %v", err)
	}
	reg := core.NewToolRegistry([]core.Tool{writeLikeTool{}})
	provider := &persistApprovalProvider{}
	asked1 := 0
	a1 := agent.NewAgentWithRegistry(
		provider,
		msgStore,
		reg,
		agent.WithToolPolicy(editApprovalPolicy()),
		agent.WithApprovalFunc(func(req policy.ApprovalRequest) policy.ApprovalDecision {
			asked1++
			return policy.ApprovalAllowForSession
		}),
	)
	if _, err := a1.RunSession(context.Background(), "eval-persisted-approval", "run1"); err != nil {
		t.Fatalf("run1 failed: %v", err)
	}
	if asked1 != 1 {
		t.Fatalf("expected first instance ask once, got %d", asked1)
	}

	asked2 := 0
	a2 := agent.NewAgentWithRegistry(
		provider,
		msgStore,
		reg,
		agent.WithToolPolicy(editApprovalPolicy()),
		agent.WithApprovalFunc(func(req policy.ApprovalRequest) policy.ApprovalDecision {
			asked2++
			return policy.ApprovalAllowForSession
		}),
	)
	if _, err := a2.RunSession(context.Background(), "eval-persisted-approval", "run2"); err != nil {
		t.Fatalf("run2 failed: %v", err)
	}
	if asked2 != 0 {
		t.Fatalf("expected persisted approval reuse across agent instances, got %d", asked2)
	}
}

type failWriteTool struct{}

func (f failWriteTool) Name() string { return "write" }
func (f failWriteTool) Run(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		ModelText:  `{"success":false,"error":"command failed","code":"exec_failed"}`,
		Outcome:    core.OutcomeFailure,
	}, nil
}

type failingWriteProvider struct{}

func (p *failingWriteProvider) StreamResponse(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 1)
	out <- llm.ProviderEvent{
		Type: llm.EventComplete,
		Response: &llm.ProviderResponse{
			FinishReason: core.FinishReasonToolUse,
			ToolCalls: []core.ToolCall{
				{ID: "fallback-write-1", Name: "write", Input: `{"file_path":"missing.txt","content":"x"}`},
			},
		},
	}
	close(out)
	return out
}

func TestRuntimeRecoveryFallbackReadonly(t *testing.T) {
	a := agent.NewAgentWithRegistry(
		&failingWriteProvider{},
		store.NewInMemoryStore(),
		core.NewToolRegistry([]core.Tool{failWriteTool{}, readOnlyViewTool{}}),
		agent.WithToolPolicy(policy.RulePolicy{Default: policy.PermissionAllow}),
		agent.WithRecoveryPolicy(agent.RecoveryPolicy{
			Enabled: true,
			Rules: map[agent.FailureClass]agent.RecoveryRule{
				agent.FailureClassExecFailed: {Action: agent.RecoveryActionFallbackReadOnly, MaxAttempts: 0},
			},
		}),
	)
	events, err := a.RunStream(context.Background(), "eval-fallback-readonly", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawFallback bool
	var sawRecovery bool
	for ev := range events {
		if ev.Type == agent.AgentEventTypeToolResult && ev.Result != nil &&
			strings.Contains(ev.Result.ModelText, "recovered_with_fallback") &&
			strings.Contains(ev.Result.ModelText, `"tool":"read_file"`) {
			sawFallback = true
		}
		if ev.Type == agent.AgentEventTypeToolRecoveryExhausted && ev.Recovery != nil &&
			ev.Recovery.Action == string(agent.RecoveryActionFallbackReadOnly) &&
			ev.Recovery.Executed {
			sawRecovery = true
		}
	}
	if !sawFallback || !sawRecovery {
		t.Fatalf("expected fallback result and recovery exhausted event, got fallback=%v recovery=%v", sawFallback, sawRecovery)
	}
}

type readOnlyViewTool struct{}

func (r readOnlyViewTool) Name() string   { return "read_file" }
func (r readOnlyViewTool) ReadOnly() bool { return true }
func (r readOnlyViewTool) Run(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: "ok"}, nil
}

type planReadOnlyProvider struct {
	calls int
}

func (p *planReadOnlyProvider) StreamResponse(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	p.calls++
	out := make(chan llm.ProviderEvent, 1)
	if p.calls == 1 {
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonToolUse,
				ToolCalls: []core.ToolCall{
					{ID: "plan-read-1", Name: "read_file", Input: `{"file_path":"x.txt"}`},
				},
			},
		}
	} else {
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonEndTurn,
				Content:      "done",
			},
		}
	}
	close(out)
	return out
}

func TestRuntimePlanModeAllowsReadOnlyToolsWithoutPlanRequired(t *testing.T) {
	a := agent.NewAgentWithRegistry(
		&planReadOnlyProvider{},
		store.NewInMemoryStore(),
		core.NewToolRegistry([]core.Tool{readOnlyViewTool{}}),
		agent.WithSessionMode(session.ModePlan),
		agent.WithSessionsDir(t.TempDir()),
	)

	events, err := a.RunStream(context.Background(), "eval-plan-readonly", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawReadResult bool
	var sawPlanRequired bool
	for ev := range events {
		if ev.Type == agent.AgentEventTypeToolResult && ev.Result != nil && ev.Result.Name == "read_file" && strings.Contains(ev.Result.ModelText, "ok") {
			sawReadResult = true
		}
		if ev.Type == agent.AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.ModelText, "plan_required") {
			sawPlanRequired = true
		}
	}
	if !sawReadResult {
		t.Fatal("expected read-only tool result")
	}
	if sawPlanRequired {
		t.Fatal("did not expect plan_required tool result")
	}
}

type planWriteProvider struct{}

func (p *planWriteProvider) StreamResponse(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 1)
	out <- llm.ProviderEvent{
		Type: llm.EventComplete,
		Response: &llm.ProviderResponse{
			FinishReason: core.FinishReasonToolUse,
			ToolCalls: []core.ToolCall{
				{ID: "plan-write-1", Name: "write", Input: `{"file_path":"x.txt","content":"hello"}`},
			},
		},
	}
	close(out)
	return out
}

type unsafeShellProvider struct{}

func (p *unsafeShellProvider) StreamResponse(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 1)
	out <- llm.ProviderEvent{
		Type: llm.EventComplete,
		Response: &llm.ProviderResponse{
			FinishReason: core.FinishReasonToolUse,
			ToolCalls: []core.ToolCall{
				{ID: "unsafe-shell-1", Name: "shell_run", Input: `{"command":"find . \"-exec\" rm {} +"}`},
			},
		},
	}
	close(out)
	return out
}

type writeLikeTool struct{}

func (w writeLikeTool) Name() string { return "write" }
func (w writeLikeTool) Run(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: "ok:" + call.Input}, nil
}

func TestRuntimePlanModeBlocksNonReadOnlyTools(t *testing.T) {
	a := agent.NewAgentWithRegistry(
		&planWriteProvider{},
		store.NewInMemoryStore(),
		core.NewToolRegistry([]core.Tool{writeLikeTool{}}),
		agent.WithSessionMode(session.ModePlan),
		agent.WithSessionsDir(t.TempDir()),
	)

	events, err := a.RunStream(context.Background(), "eval-plan-write-block", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawModeBlocked bool
	var sawBlockedResult bool
	for ev := range events {
		if ev.Type == agent.AgentEventTypeToolModeBlocked && ev.ToolBlocked != nil && ev.ToolBlocked.ReasonCode == "plan_mode_blocked" {
			sawModeBlocked = true
		}
		if ev.Type == agent.AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.ModelText, "plan_mode_blocked") {
			sawBlockedResult = true
		}
	}
	if !sawModeBlocked || !sawBlockedResult {
		t.Fatalf("expected plan_mode_blocked event and result, got event=%v result=%v", sawModeBlocked, sawBlockedResult)
	}
}

func TestRuntimeAskModeBlocksNonReadOnlyTools(t *testing.T) {
	a := agent.NewAgentWithRegistry(
		&planWriteProvider{},
		store.NewInMemoryStore(),
		core.NewToolRegistry([]core.Tool{writeLikeTool{}}),
		agent.WithSessionMode(session.ModeAsk),
		agent.WithSessionsDir(t.TempDir()),
	)

	events, err := a.RunStream(context.Background(), "eval-ask-write-block", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawModeBlocked bool
	var sawBlockedResult bool
	for ev := range events {
		if ev.Type == agent.AgentEventTypeToolModeBlocked && ev.ToolBlocked != nil && ev.ToolBlocked.ReasonCode == "ask_mode_blocked" {
			sawModeBlocked = true
		}
		if ev.Type == agent.AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.ModelText, "ask_mode_blocked") {
			sawBlockedResult = true
		}
	}
	if !sawModeBlocked || !sawBlockedResult {
		t.Fatalf("expected ask_mode_blocked event and result, got event=%v result=%v", sawModeBlocked, sawBlockedResult)
	}
}

func TestRuntimeAskModeBlocksQuotedUnsafeShellReadCommand(t *testing.T) {
	ts, err := tools.NewToolset(t.TempDir())
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	a := agent.NewAgentWithRegistry(
		&unsafeShellProvider{},
		store.NewInMemoryStore(),
		core.NewToolRegistry(ts.Tools()),
		agent.WithSessionMode(session.ModeAsk),
		agent.WithSessionsDir(t.TempDir()),
	)

	events, err := a.RunStream(context.Background(), "eval-ask-unsafe-shell-block", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawModeBlocked bool
	var sawBlockedResult bool
	for ev := range events {
		if ev.Type == agent.AgentEventTypeToolModeBlocked && ev.ToolBlocked != nil && ev.ToolBlocked.ReasonCode == "ask_mode_blocked" {
			sawModeBlocked = true
		}
		if ev.Type == agent.AgentEventTypeToolResult && ev.Result != nil && ev.Result.Name == "shell_run" && strings.Contains(ev.Result.ModelText, "ask_mode_blocked") {
			sawBlockedResult = true
		}
	}
	if !sawModeBlocked || !sawBlockedResult {
		t.Fatalf("expected quoted unsafe shell command to be ask_mode_blocked, got event=%v result=%v", sawModeBlocked, sawBlockedResult)
	}
}

type autoCompactProvider struct {
	histories [][]core.Message
}

func (p *autoCompactProvider) StreamResponse(_ context.Context, history []core.Message, tools []core.Tool) <-chan llm.ProviderEvent {
	p.histories = append(p.histories, append([]core.Message(nil), history...))
	out := make(chan llm.ProviderEvent, 1)
	content := "ok"
	if len(history) > 0 && strings.Contains(history[len(history)-1].Text, "Summarize the conversation") {
		content = "compact summary"
	}
	out <- llm.ProviderEvent{
		Type: llm.EventComplete,
		Response: &llm.ProviderResponse{
			FinishReason: core.FinishReasonEndTurn,
			Content:      content,
		},
	}
	close(out)
	return out
}

func TestRuntimeCompactSessionRewritesToSummaryOnly(t *testing.T) {
	msgStore := store.NewInMemoryStore()
	_, _ = msgStore.Create(context.Background(), core.Message{SessionID: "eval-compact", Role: core.RoleUser, Text: "keep this"})
	_, _ = msgStore.Create(context.Background(), core.Message{SessionID: "eval-compact", Role: core.RoleAssistant, Text: "old answer"})
	a := agent.NewAgentWithRegistry(&autoCompactProvider{}, msgStore, core.NewToolRegistry(nil))

	info, err := a.CompactSession(context.Background(), "eval-compact")
	if err != nil {
		t.Fatalf("compact failed: %v", err)
	}
	if !info.Compacted {
		t.Fatal("expected compact rewrite")
	}
	if info.MessagesBefore != 2 || info.MessagesAfter != 1 {
		t.Fatalf("unexpected compact counts: %+v", info)
	}
	msgs, err := msgStore.List(context.Background(), "eval-compact")
	if err != nil {
		t.Fatalf("list compacted messages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected summary-only session, got %+v", msgs)
	}
	if msgs[0].Role != core.RoleUser || msgs[0].Text != "compact summary" || msgs[0].FinishReason != core.FinishReasonEndTurn {
		t.Fatalf("unexpected compact summary: %+v", msgs[0])
	}
	if info.AfterEstimate != compact.EstimateMessagesTokens(msgs) {
		t.Fatalf("unexpected after token estimate: %+v", info)
	}
}

func TestRuntimeCompactSessionNoopsWhenAlreadyCompactSummaryTail(t *testing.T) {
	msgStore := store.NewInMemoryStore()
	_, _ = msgStore.Create(context.Background(), core.Message{SessionID: "eval-compact-noop", Role: core.RoleUser, Text: "compact summary", FinishReason: core.FinishReasonEndTurn})
	a := agent.NewAgentWithRegistry(&autoCompactProvider{}, msgStore, core.NewToolRegistry(nil))

	info, err := a.CompactSession(context.Background(), "eval-compact-noop")
	if err != nil {
		t.Fatalf("compact noop failed: %v", err)
	}
	if info.Compacted {
		t.Fatalf("expected no-op compact info, got %+v", info)
	}
	msgs, err := msgStore.List(context.Background(), "eval-compact-noop")
	if err != nil {
		t.Fatalf("list noop compact messages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Text != "compact summary" {
		t.Fatalf("unexpected noop compact state: %+v", msgs)
	}
}

func TestRuntimeAutoCompactEmitsEventAndRewritesHistory(t *testing.T) {
	msgStore := store.NewInMemoryStore()
	for i := 0; i < 8; i++ {
		_, _ = msgStore.Create(context.Background(), core.Message{
			SessionID: "eval-auto-compact",
			Role:      core.RoleUser,
			Text:      strings.Repeat("line ", 300),
		})
	}
	provider := &autoCompactProvider{}
	a := agent.NewAgentWithRegistry(
		provider,
		msgStore,
		core.NewToolRegistry(nil),
		agent.WithAutoCompact(true, 0.01, 1000),
	)

	events, err := a.RunStream(context.Background(), "eval-auto-compact", "next")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawCompact bool
	for ev := range events {
		if ev.Type == agent.AgentEventTypeContextCompacted && ev.Compact != nil && ev.Compact.Auto {
			sawCompact = true
		}
	}
	if !sawCompact {
		t.Fatal("expected auto compact event")
	}
	msgs, err := msgStore.List(context.Background(), "eval-auto-compact")
	if err != nil {
		t.Fatalf("list messages failed: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected compact summary plus retained current turn and result, got %+v", msgs)
	}
	if msgs[0].Role != core.RoleUser || msgs[0].Text != "compact summary" {
		t.Fatalf("expected summary rewrite, got %+v", msgs[0])
	}
	if msgs[1].Role != core.RoleUser || msgs[1].Text != "next" {
		t.Fatalf("expected retained current user turn, got %+v", msgs[1])
	}
	if len(provider.histories) < 2 {
		t.Fatalf("expected summary and normal provider calls, got %d", len(provider.histories))
	}
}

func TestRuntimeCompactSessionNoopsWithSummaryTailAndEarlierToolHistory(t *testing.T) {
	msgStore := store.NewInMemoryStore()
	_, _ = msgStore.Create(context.Background(), core.Message{SessionID: "eval-compact-rich-noop", Role: core.RoleUser, Text: "first prompt"})
	_, _ = msgStore.Create(context.Background(), core.Message{SessionID: "eval-compact-rich-noop", Role: core.RoleTool, ToolResults: []core.ToolResult{{ToolCallID: "t1", Name: "read_file", ModelText: `{"success":true}`}}})
	_, _ = msgStore.Create(context.Background(), core.Message{SessionID: "eval-compact-rich-noop", Role: core.RoleAssistant, Text: "old answer"})
	_, _ = msgStore.Create(context.Background(), core.Message{SessionID: "eval-compact-rich-noop", Role: core.RoleUser, Text: "compact summary", FinishReason: core.FinishReasonEndTurn})
	a := agent.NewAgentWithRegistry(&autoCompactProvider{}, msgStore, core.NewToolRegistry(nil))

	info, err := a.CompactSession(context.Background(), "eval-compact-rich-noop")
	if err != nil {
		t.Fatalf("compact noop failed: %v", err)
	}
	if info.Compacted {
		t.Fatalf("expected no-op compact info, got %+v", info)
	}
	msgs, err := msgStore.List(context.Background(), "eval-compact-rich-noop")
	if err != nil {
		t.Fatalf("list noop compact messages: %v", err)
	}
	if len(msgs) != 4 || msgs[len(msgs)-1].Text != "compact summary" || msgs[1].Role != core.RoleTool {
		t.Fatalf("unexpected noop compact rich state: %+v", msgs)
	}
}

func TestRuntimeAutoCompactRewritesLongHistoryWithToolMessages(t *testing.T) {
	msgStore := store.NewInMemoryStore()
	for i := 0; i < 3; i++ {
		_, _ = msgStore.Create(context.Background(), core.Message{
			SessionID: "eval-auto-compact-tools",
			Role:      core.RoleUser,
			Text:      strings.Repeat("user context ", 200),
		})
		_, _ = msgStore.Create(context.Background(), core.Message{
			SessionID: "eval-auto-compact-tools",
			Role:      core.RoleAssistant,
			Text:      strings.Repeat("assistant context ", 120),
		})
		_, _ = msgStore.Create(context.Background(), core.Message{
			SessionID: "eval-auto-compact-tools",
			Role:      core.RoleTool,
			ToolResults: []core.ToolResult{{
				ToolCallID: "tool",
				Name:       "read_file",
				ModelText:  strings.Repeat("tool payload ", 80),
			}},
		})
	}
	provider := &autoCompactProvider{}
	a := agent.NewAgentWithRegistry(
		provider,
		msgStore,
		core.NewToolRegistry(nil),
		agent.WithAutoCompact(true, 0.01, 1000),
	)

	events, err := a.RunStream(context.Background(), "eval-auto-compact-tools", "next")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawCompact bool
	for ev := range events {
		if ev.Type == agent.AgentEventTypeContextCompacted && ev.Compact != nil && ev.Compact.Auto {
			sawCompact = true
		}
	}
	if !sawCompact {
		t.Fatal("expected auto compact event")
	}
	msgs, err := msgStore.List(context.Background(), "eval-auto-compact-tools")
	if err != nil {
		t.Fatalf("list messages failed: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected compact summary plus retained current turn and result, got %+v", msgs)
	}
	if msgs[0].Role != core.RoleUser || msgs[0].Text != "compact summary" {
		t.Fatalf("expected summary rewrite, got %+v", msgs[0])
	}
	if msgs[1].Role != core.RoleUser || msgs[1].Text != "next" {
		t.Fatalf("expected retained current user turn, got %+v", msgs[1])
	}
	if msgs[2].Role != core.RoleAssistant || msgs[2].Text == "" {
		t.Fatalf("expected current assistant response after compact, got %+v", msgs[2])
	}
}

type hookToolProvider struct{ calls int }

func (p *hookToolProvider) StreamResponse(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 1)
	p.calls++
	if p.calls == 1 {
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonToolUse,
				ToolCalls: []core.ToolCall{
					{ID: "hook-tool-1", Name: "counting", Input: `{}`},
				},
			},
		}
	} else {
		out <- llm.ProviderEvent{
			Type: llm.EventComplete,
			Response: &llm.ProviderResponse{
				FinishReason: core.FinishReasonEndTurn,
				Content:      "done",
			},
		}
	}
	close(out)
	return out
}

func TestRuntimePreToolHookBlockSkipsDispatch(t *testing.T) {
	counting := &countingTool{}
	a := agent.NewAgentWithRegistry(
		&hookToolProvider{},
		store.NewInMemoryStore(),
		core.NewToolRegistry([]core.Tool{counting}),
		agent.WithHooks([]agent.ResolvedHook{{
			HookConfig: agent.HookConfig{Command: "echo blocked >&2; exit 2"},
			Event:      agent.HookEventPreToolUse,
		}}, "."),
	)
	events, err := a.RunStream(context.Background(), "eval-hook-pre-block", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawHookBlocked bool
	var sawToolBlocked bool
	for ev := range events {
		if ev.Type == agent.AgentEventTypeHookBlocked && ev.Hook != nil {
			sawHookBlocked = true
		}
		if ev.Type == agent.AgentEventTypeToolResult && ev.Result != nil && strings.Contains(ev.Result.ModelText, "hook_blocked") {
			sawToolBlocked = true
		}
	}
	if counting.calls != 0 {
		t.Fatalf("expected tool dispatch skipped by pre-hook block, got %d", counting.calls)
	}
	if !sawHookBlocked || !sawToolBlocked {
		t.Fatalf("expected hook blocked event and tool result, got hook=%v tool=%v", sawHookBlocked, sawToolBlocked)
	}
}

func TestRuntimePostToolHookWarnEmitsWarning(t *testing.T) {
	counting := &countingTool{}
	a := agent.NewAgentWithRegistry(
		&hookToolProvider{},
		store.NewInMemoryStore(),
		core.NewToolRegistry([]core.Tool{counting}),
		agent.WithHooks([]agent.ResolvedHook{{
			HookConfig: agent.HookConfig{Command: "echo post-warn >&2; exit 5"},
			Event:      agent.HookEventPostToolUse,
		}}, "."),
	)
	events, err := a.RunStream(context.Background(), "eval-hook-post-warn", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawHookWarned bool
	var sawHookCompleted bool
	for ev := range events {
		if ev.Type == agent.AgentEventTypeHookWarned && ev.Hook != nil {
			sawHookWarned = true
		}
		if ev.Type == agent.AgentEventTypeHookCompleted && ev.Hook != nil {
			sawHookCompleted = true
		}
	}
	if counting.calls != 1 {
		t.Fatalf("expected tool dispatch before post-hook warn, got %d", counting.calls)
	}
	if !sawHookWarned || sawHookCompleted {
		t.Fatalf("expected single hook warned terminal event, got warned=%v completed=%v", sawHookWarned, sawHookCompleted)
	}
}

type scavengeProvider struct{}

func (p *scavengeProvider) StreamResponse(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 1)
	out <- llm.ProviderEvent{
		Type: llm.EventComplete,
		Response: &llm.ProviderResponse{
			FinishReason: core.FinishReasonEndTurn,
			Content:      `I should inspect the file first. {"name":"read_file","arguments":{"file_path":"README.md","offset":0}}`,
		},
	}
	close(out)
	return out
}

func TestRuntimeScavengesToolCallFromAssistantContent(t *testing.T) {
	a := agent.NewAgentWithRegistry(
		&scavengeProvider{},
		store.NewInMemoryStore(),
		core.NewToolRegistry([]core.Tool{readOnlyViewTool{}}),
		agent.WithToolPolicy(policy.RulePolicy{Default: policy.PermissionAllow}),
	)
	events, err := a.RunStream(context.Background(), "eval-scavenge", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawScavenged bool
	var sawToolResult bool
	for ev := range events {
		if ev.Type == agent.AgentEventTypeToolCallScavenged && ev.Scavenged != nil && ev.Scavenged.Count > 0 {
			sawScavenged = true
		}
		if ev.Type == agent.AgentEventTypeToolResult && ev.Result != nil && ev.Result.Name == "read_file" && strings.Contains(ev.Result.ModelText, "ok") {
			sawToolResult = true
		}
	}
	if !sawScavenged || !sawToolResult {
		t.Fatalf("expected scavenged tool call and result, got scavenged=%v result=%v", sawScavenged, sawToolResult)
	}
}

type toolArgsDeltaProvider struct{}

func (p *toolArgsDeltaProvider) StreamResponse(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 3)
	out <- llm.ProviderEvent{
		Type: llm.EventToolArgsDelta,
		ToolArgsDelta: &llm.ToolArgsDelta{
			ToolCallIndex: 0,
			ToolName:      "write",
			ArgsDelta:     "{",
			ArgsChars:     1,
			ReadyCount:    0,
		},
	}
	out <- llm.ProviderEvent{
		Type: llm.EventToolArgsDelta,
		ToolArgsDelta: &llm.ToolArgsDelta{
			ToolCallIndex: 0,
			ToolName:      "write",
			ArgsDelta:     `"file_path":"a.txt","content":"x"}`,
			ArgsChars:     34,
			ReadyCount:    1,
		},
	}
	out <- llm.ProviderEvent{
		Type: llm.EventComplete,
		Response: &llm.ProviderResponse{
			FinishReason: core.FinishReasonEndTurn,
			Content:      "done",
		},
	}
	close(out)
	return out
}

func TestRuntimeEmitsToolArgsDeltaEvents(t *testing.T) {
	a := agent.NewAgentWithRegistry(
		&toolArgsDeltaProvider{},
		store.NewInMemoryStore(),
		core.NewToolRegistry([]core.Tool{writeEchoTool{}}),
	)
	events, err := a.RunStream(context.Background(), "eval-tool-args-delta", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawReady bool
	for ev := range events {
		if ev.Type == agent.AgentEventTypeToolArgsDelta && ev.ToolArgs != nil &&
			ev.ToolArgs.ToolName == "write" && ev.ToolArgs.ReadyCount == 1 {
			sawReady = true
		}
	}
	if !sawReady {
		t.Fatal("expected tool args delta ready event")
	}
}

type toolArgsRepairProvider struct{}

func (p *toolArgsRepairProvider) StreamResponse(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
	out := make(chan llm.ProviderEvent, 1)
	out <- llm.ProviderEvent{
		Type: llm.EventComplete,
		Response: &llm.ProviderResponse{
			FinishReason: core.FinishReasonToolUse,
			ToolCalls: []core.ToolCall{
				{ID: "repair-write-1", Name: "write", Input: `{"file_path":"a.txt","content":"x"`},
			},
		},
	}
	close(out)
	return out
}

func TestRuntimeRepairsTruncatedToolArgs(t *testing.T) {
	a := agent.NewAgentWithRegistry(
		&toolArgsRepairProvider{},
		store.NewInMemoryStore(),
		core.NewToolRegistry([]core.Tool{writeEchoTool{}}),
		agent.WithToolPolicy(policy.RulePolicy{Default: policy.PermissionAllow}),
	)
	events, err := a.RunStream(context.Background(), "eval-tool-args-repair", "go")
	if err != nil {
		t.Fatalf("run stream failed: %v", err)
	}
	var sawRepair bool
	var sawWriteResult bool
	for ev := range events {
		if ev.Type == agent.AgentEventTypeToolArgsRepaired && ev.ToolArgsRepair != nil && ev.ToolArgsRepair.ToolName == "write" {
			sawRepair = true
		}
		if ev.Type == agent.AgentEventTypeToolResult && ev.Result != nil && ev.Result.Name == "write" && core.ToolResultOutcome(*ev.Result) == core.OutcomeSuccess {
			sawWriteResult = true
		}
	}
	if !sawRepair || !sawWriteResult {
		t.Fatalf("expected repaired tool args and successful write result, got repair=%v result=%v", sawRepair, sawWriteResult)
	}
}
