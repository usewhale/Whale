//go:build livetest

// Live end-to-end test of the plan-as-reply flow against the real DeepSeek API.
//
//	DEEPSEEK_API_KEY=sk-... go test -tags livetest -run TestLivePlanDeepSeek \
//	    -v -count=1 -timeout 300s ./internal/agent/
//
// It is excluded from normal builds/CI by the livetest build tag. The test
// exercises exactly the path that used to fail: a Plan-mode turn against DeepSeek
// must (1) explore read-only without storm-blocking and (2) finish with a plan
// reply that surfaces as PlanCompleted — no <proposed_plan> sentinel involved.
// Phase 1 (plan production) is strictly asserted — that is what this change
// owns. Phase 2 exercises the approve→implement handoff best-effort: whether
// DeepSeek actually edits is agent-mode nondeterminism, so a non-edit run is
// logged, not failed.
package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm/deepseek"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/tools"
)

const liveModel = "deepseek-chat"

func liveProvider(t *testing.T) *deepseek.Client {
	t.Helper()
	key := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	if key == "" {
		t.Skip("DEEPSEEK_API_KEY not set; skipping live DeepSeek plan test")
	}
	client, err := deepseek.New(deepseek.WithAPIKey(key), deepseek.WithModel(liveModel))
	if err != nil {
		t.Fatalf("deepseek.New: %v", err)
	}
	return client
}

// liveWorkspace creates a tiny Go project the model can read while planning and
// write to while implementing, so the run never touches the real repo.
func liveWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	main := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatalf("seed main.go: %v", err)
	}
	return dir
}

func TestLivePlanDeepSeek(t *testing.T) {
	workspace := liveWorkspace(t)
	toolset, err := tools.NewToolset(workspace)
	if err != nil {
		t.Fatalf("NewToolset: %v", err)
	}
	store := NewInMemoryStore()
	const sid = "s-live-plan"
	const prompt = "Plan how to add a --upper flag to this program that uppercases the printed greeting. " +
		"Explore the code first, then give the plan directly. Do not ask me any clarifying questions."

	// ---- Phase 1: plan ----
	planAgent := NewAgentWithRegistry(
		liveProvider(t),
		store,
		core.NewToolRegistry(toolset.Tools()),
		WithSessionMode(session.ModePlan),
		WithUserInputFunc(func(UserInputRequest) (core.UserInputResponse, bool) {
			// Force the model to commit to a plan instead of stalling on a question.
			return core.UserInputResponse{}, false
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Second)
	defer cancel()

	events, err := planAgent.RunStreamWithTurnOptions(ctx, sid, prompt, RunOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("plan RunStream: %v", err)
	}

	var (
		planText     string
		planDeltas   int
		toolCalls    int
		stormBlocked int
		forced       string
	)
	for ev := range events {
		switch ev.Type {
		case AgentEventTypePlanDelta:
			planDeltas++
		case AgentEventTypePlanCompleted:
			planText = ev.Content
		case AgentEventTypeToolCall:
			toolCalls++
		case AgentEventTypeToolResult:
			if ev.Result != nil && ev.Result.Code == "storm_blocked" {
				stormBlocked++
			}
		case AgentEventTypeForcedSummaryStarted:
			forced = ev.Content
		case AgentEventTypeError:
			t.Fatalf("plan turn errored: %v", ev.Err)
		}
	}

	t.Logf("plan-mode run: toolCalls=%d planDeltas=%d stormBlocked=%d", toolCalls, planDeltas, stormBlocked)
	if forced != "" {
		t.Fatalf("plan turn ended via forced summary (loop guard): %q", forced)
	}
	if stormBlocked != 0 {
		t.Fatalf("plan turn hit %d storm-blocked tool calls", stormBlocked)
	}
	if strings.TrimSpace(planText) == "" {
		t.Fatal("plan turn produced no PlanCompleted plan — the failure this change fixes")
	}
	t.Logf("===== PROPOSED PLAN =====\n%s\n=========================", planText)

	// The plan must be persisted as a plan part and visible to the model in history.
	msgs, err := store.List(ctx, sid)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	var sawPlanPart bool
	for _, m := range msgs {
		if m.Role != core.RoleAssistant {
			continue
		}
		for _, part := range m.Parts {
			if part.Type == core.MessagePartPlan && strings.TrimSpace(part.Text) != "" {
				sawPlanPart = true
			}
		}
	}
	if !sawPlanPart {
		t.Fatal("approved plan was not persisted as a plan part")
	}

	// ---- Phase 2: approve → implement (same session/store, agent mode) ----
	implAgent := NewAgentWithRegistry(
		liveProvider(t),
		store,
		core.NewToolRegistry(toolset.Tools()),
		WithSessionMode(session.ModeAgent),
		WithApprovalFunc(func(policy.ApprovalRequest) policy.ApprovalDecision {
			return policy.ApprovalAllowForSession
		}),
		WithMaxTurns(12),
	)

	// Drive the implement handoff like a real user would: ask, and if the model
	// narrated instead of editing, nudge it to actually apply the change. This is
	// ordinary agent-mode nondeterminism (DeepSeek sometimes restates the plan
	// before acting), independent of the plan-as-reply change under test.
	implPrompts := []struct{ visible, hidden string }{
		{"Implement the plan.", "Implement the plan now. Make the actual code edits with the edit tools."},
		{"You haven't edited any files yet. Apply the change to main.go right now using the edit/multi_edit tools — do not just describe it.", ""},
		{"Edit main.go now to add the --upper flag exactly as planned. Use the edit tool.", ""},
	}
	editTools := map[string]bool{"edit": true, "multi_edit": true, "create_file": true, "write_file": true}
	var implemented bool
	for i, p := range implPrompts {
		implCtx, implCancel := context.WithTimeout(context.Background(), 200*time.Second)
		var implEvents <-chan AgentEvent
		if p.hidden != "" {
			implEvents, err = implAgent.RunStreamWithInjectedInput(implCtx, sid, p.visible, p.hidden)
		} else {
			implEvents, err = implAgent.RunStream(implCtx, sid, p.visible)
		}
		if err != nil {
			implCancel()
			t.Fatalf("implement RunStream (turn %d): %v", i+1, err)
		}
		var editCalls int
		for ev := range implEvents {
			switch ev.Type {
			case AgentEventTypeToolCall:
				if ev.ToolCall != nil && editTools[ev.ToolCall.Name] {
					editCalls++
				}
			case AgentEventTypeError:
				implCancel()
				t.Fatalf("implement turn %d errored: %v", i+1, ev.Err)
			}
		}
		implCancel()

		got, err := os.ReadFile(filepath.Join(workspace, "main.go"))
		if err != nil {
			t.Fatalf("read implemented main.go: %v", err)
		}
		lower := strings.ToLower(string(got))
		t.Logf("implement turn %d: editToolCalls=%d fileChanged=%v", i+1, editCalls, strings.Contains(lower, "upper"))
		if strings.Contains(lower, "upper") || strings.Contains(lower, "toupper") {
			implemented = true
			t.Logf("===== IMPLEMENTED main.go =====\n%s\n===============================", got)
			break
		}
	}
	// The implement handoff is best-effort here: whether DeepSeek actually calls
	// the edit tools (vs. narrating the code) is ordinary agent-mode behavior
	// outside the scope of the plan-as-reply change. The strict assertions above
	// cover what this change owns — that Plan mode reliably proposes an approvable
	// plan. A non-edit run is logged, not failed, so this stays a stable validator.
	if !implemented {
		got, _ := os.ReadFile(filepath.Join(workspace, "main.go"))
		t.Logf("NOTE: implement handoff ran cleanly but DeepSeek narrated instead of editing across %d turns (agent-mode nondeterminism, not the plan change). main.go unchanged:\n%s", len(implPrompts), got)
	}
}
