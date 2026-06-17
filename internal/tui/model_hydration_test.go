package tui

import (
	"fmt"
	"github.com/usewhale/whale/internal/runtime/protocol"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func protocolMessagesForTest(messages []core.Message) []protocol.Message {
	out := make([]protocol.Message, 0, len(messages))
	for _, message := range messages {
		toolCalls := make([]protocol.ToolCall, 0, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			toolCalls = append(toolCalls, protocol.ToolCall{ID: call.ID, Name: call.Name, Input: call.Input})
		}
		toolResults := make([]protocol.ToolResult, 0, len(message.ToolResults))
		for _, result := range message.ToolResults {
			toolResults = append(toolResults, protocol.ToolResult{ToolCallID: result.ToolCallID, Name: result.Name, Content: result.ModelText, Metadata: result.Metadata, IsError: result.IsError()})
		}
		out = append(out, protocol.Message{
			ID:           message.ID,
			SessionID:    message.SessionID,
			Role:         string(message.Role),
			Text:         message.Text,
			Parts:        protocolPartsForTest(message.Parts),
			Hidden:       message.Hidden,
			Reasoning:    message.Reasoning,
			ToolCalls:    toolCalls,
			ToolResults:  toolResults,
			FinishReason: string(message.FinishReason),
			CreatedAt:    message.CreatedAt,
			UpdatedAt:    message.UpdatedAt,
		})
	}
	return out
}

func protocolPartsForTest(parts []core.MessagePart) []protocol.MessagePart {
	if len(parts) == 0 {
		return nil
	}
	out := make([]protocol.MessagePart, 0, len(parts))
	for _, part := range parts {
		out = append(out, protocol.MessagePart{Type: string(part.Type), Text: part.Text})
	}
	return out
}

func hydrateSessionMessagesForTest(m *model, messages []core.Message) {
	m.hydrateSessionMessages(protocolMessagesForTest(messages))
}

func TestIsEnvironmentInventoryBlock_PositiveChinese(t *testing.T) {
	text := "- 系统： macOS\n- 版本： 26.0\n- 构建号： 25A354"
	if !isEnvironmentInventoryBlock(text) {
		t.Fatalf("expected environment inventory block to be detected")
	}
}
func TestIsEnvironmentInventoryBlock_NegativeNormalAssistantText(t *testing.T) {
	text := "I checked the version mismatch in package constraints and suggest bumping one dependency."
	if isEnvironmentInventoryBlock(text) {
		t.Fatalf("did not expect normal assistant text to be detected as environment inventory block")
	}
}
func TestHydrateSessionMessages_SuppressesEnvironmentInventoryAssistantBlock(t *testing.T) {
	m := &model{assembler: tuirender.NewAssembler()}
	msgs := []core.Message{
		{
			Role: core.RoleAssistant,
			Text: "- 系统： macOS\n- 版本： 26.0\n- 构建号： 25A354",
		},
	}
	hydrateSessionMessagesForTest(m, msgs)
	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected no chat entries for environment inventory block, got %d", got)
	}
	if len(m.logs) != 1 || m.logs[0].Kind != "env_summary" || m.logs[0].Source != "assistant" {
		t.Fatalf("expected env_summary log for environment inventory block, got %+v", m.logs)
	}
}

func TestHydrateSessionMessages_LogsEnvironmentInventoryAssistantPart(t *testing.T) {
	m := &model{assembler: tuirender.NewAssembler()}
	env := "- 系统： macOS\n- 版本： 26.0\n- 构建号： 25A354"
	msgs := []core.Message{
		{
			Role: core.RoleAssistant,
			Text: env,
			Parts: []core.MessagePart{
				{Type: core.MessagePartText, Text: env},
			},
		},
	}
	hydrateSessionMessagesForTest(m, msgs)
	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected no chat entries for environment inventory part, got %d", got)
	}
	if len(m.logs) != 1 || m.logs[0].Kind != "env_summary" || m.logs[0].Source != "assistant" || m.logs[0].Raw != env {
		t.Fatalf("expected env_summary log for environment inventory part, got %+v", m.logs)
	}
}
func TestHydrateSessionMessages_KeptForNormalAssistantText(t *testing.T) {
	m := &model{assembler: tuirender.NewAssembler()}
	msgs := []core.Message{
		{
			Role: core.RoleAssistant,
			Text: "Implemented the layout update and kept footer semantics unchanged.",
		},
	}
	hydrateSessionMessagesForTest(m, msgs)
	snap := m.assembler.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected one assistant entry, got %d", len(snap))
	}
	if snap[0].Role != "assistant" {
		t.Fatalf("expected role assistant, got %q", snap[0].Role)
	}
}
func TestHydrateSessionMessages_RendersReasoningAsThinkingOnly(t *testing.T) {
	m := &model{assembler: tuirender.NewAssembler()}
	msgs := []core.Message{
		{
			Role:      core.RoleAssistant,
			Reasoning: "I should answer the age question.",
		},
	}
	hydrateSessionMessagesForTest(m, msgs)
	snap := m.assembler.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected only thinking entry, got %+v", snap)
	}
	if snap[0].Role != "think" || snap[0].Kind != tuirender.KindThinking {
		t.Fatalf("expected first entry to be thinking, got %+v", snap[0])
	}
}
func TestHydrateSessionMessages_RendersReasoningAndAssistantSeparately(t *testing.T) {
	m := &model{assembler: tuirender.NewAssembler()}
	msgs := []core.Message{
		{
			Role:      core.RoleAssistant,
			Reasoning: "I should answer succinctly.",
			Text:      "I do not have an age.",
		},
	}
	hydrateSessionMessagesForTest(m, msgs)
	snap := m.assembler.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected thinking plus assistant entries, got %+v", snap)
	}
	if snap[0].Kind != tuirender.KindThinking || snap[0].Role != "think" {
		t.Fatalf("expected thinking entry first, got %+v", snap[0])
	}
	if snap[1].Role != "assistant" || snap[1].Kind != tuirender.KindText {
		t.Fatalf("expected assistant text second, got %+v", snap[1])
	}
}
func TestHydrateSessionMessages_SuppressesHiddenUserText(t *testing.T) {
	m := &model{assembler: tuirender.NewAssembler()}
	msgs := []core.Message{
		{
			Role:   core.RoleUser,
			Text:   "Generate a file named AGENTS.md",
			Hidden: true,
		},
	}
	hydrateSessionMessagesForTest(m, msgs)
	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected no chat entries for hidden user text, got %d", got)
	}
}
func TestHydrateSessionMessages_RestoresUpdatePlanAsPlanUpdate(t *testing.T) {
	m := &model{assembler: tuirender.NewAssembler()}
	msgs := []core.Message{
		{
			Role: core.RoleAssistant,
			ToolCalls: []core.ToolCall{{
				ID:    "plan-1",
				Name:  "update_plan",
				Input: `{"plan":[{"step":"Inspect","status":"completed"},{"step":"Patch","status":"in_progress"},{"step":"Test","status":"pending"}]}`,
			}},
		},
		{
			Role: core.RoleTool,
			ToolResults: []core.ToolResult{{
				ToolCallID: "plan-1",
				Name:       "update_plan",
				ModelText:  `{"success":true,"data":{"explanation":"resume checklist","plan":[{"step":"Inspect","status":"completed"},{"step":"Patch","status":"in_progress"},{"step":"Test","status":"pending"}]}}`,
			}},
		},
	}
	hydrateSessionMessagesForTest(m, msgs)
	snap := m.assembler.Snapshot()
	if len(snap) != 1 || snap[0].Kind != tuirender.KindPlanUpdate {
		t.Fatalf("expected hydrated plan update only, got %+v", snap)
	}
	if strings.Contains(snap[0].Text, "Updated plan") || strings.Contains(snap[0].Text, "update_plan") {
		t.Fatalf("expected checklist content, not generic tool row: %+v", snap[0])
	}
	rendered := strings.Join(tuirender.ChatLines(snap, 80), "\n")
	for _, want := range []string{"Updated Plan", "resume checklist", "✔ Inspect", "□ Patch", "□ Test"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in hydrated plan update:\n%s", want, rendered)
		}
	}
}

func TestSessionHydrationRestoresHiddenWorkflowResultInsteadOfLaunchReasoning(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 100, height: 24}
	next, _ := m.Update(svcMsg(protocol.Event{
		Kind: protocol.EventSessionHydrated,
		Messages: protocolMessagesForTest([]core.Message{
			{
				Role: core.RoleUser,
				Text: "run issue251-live workflow",
			},
			{
				Role:      core.RoleAssistant,
				Reasoning: "The user wants me to run the issue251-live workflow. Let me launch it directly.",
				ToolCalls: []core.ToolCall{{
					ID:    "call-workflow-1",
					Name:  "workflow",
					Input: `{"name":"issue251-live"}`,
				}},
			},
			{
				Role: core.RoleTool,
				ToolResults: []core.ToolResult{{
					ToolCallID: "call-workflow-1",
					Name:       "workflow",
					ModelText:  `{"success":true,"data":{"status":"workflow_confirmation_required","name":"issue251-live"}}`,
				}},
			},
			{
				Role:   core.RoleUser,
				Hidden: true,
				Text: strings.Join([]string{
					"<workflow_result>",
					"run: run-0b379e23-7a1c-4632-9f1f-c0060510a24c",
					"",
					"The background workflow completed. Treat this as the authoritative workflow result for later user questions about this run.",
					"",
					"Dynamic workflow \"issue251-live\" completed",
					"",
					"Result:",
					"- executiveSummary: visible final result",
					"</workflow_result>",
				}, "\n"),
			},
		}),
	}))
	m = next.(model)

	rendered := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	for _, want := range []string{
		"run issue251-live workflow",
		"Dynamic workflow \"issue251-live\" completed",
		"executiveSummary: visible final result",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in hydrated workflow result transcript:\n%s", want, rendered)
		}
	}
	for _, notWant := range []string{
		"Reasoning only",
		"Let me launch it directly",
		"workflow_confirmation_required",
	} {
		if strings.Contains(rendered, notWant) {
			t.Fatalf("did not expect stale workflow launch output %q in transcript:\n%s", notWant, rendered)
		}
	}
	if m.hasPendingLifecycleItems() {
		t.Fatalf("workflow result marker should not leave pending lifecycle items: %+v", m.timeline.Snapshot())
	}
}

func TestSessionHydrationPreservesAssistantOutputBeforeHiddenWorkflowResult(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 100, height: 24}
	next, _ := m.Update(svcMsg(protocol.Event{
		Kind: protocol.EventSessionHydrated,
		Messages: protocolMessagesForTest([]core.Message{
			{
				Role: core.RoleUser,
				Text: "what changed?",
			},
			{
				Role:      core.RoleAssistant,
				Reasoning: "I should summarize the unrelated code change.",
				Text:      "The unrelated assistant answer should stay visible.",
			},
			{
				Role:   core.RoleUser,
				Hidden: true,
				Text: strings.Join([]string{
					"<workflow_result>",
					"run: run-background",
					"",
					"The background workflow completed. Treat this as the authoritative workflow result for later user questions about this run.",
					"",
					"Dynamic workflow \"background\" completed",
					"",
					"Result:",
					"- done",
					"</workflow_result>",
				}, "\n"),
			},
		}),
	}))
	m = next.(model)

	rendered := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	for _, want := range []string{
		"what changed?",
		"I should summarize the unrelated code change.",
		"The unrelated assistant answer should stay visible.",
		"Dynamic workflow \"background\" completed",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q after hydrating interleaved workflow marker:\n%s", want, rendered)
		}
	}
}
func TestHydrateSessionMessages_LimitsVisibleResumeHistory(t *testing.T) {
	m := &model{assembler: tuirender.NewAssembler()}
	msgs := make([]core.Message, 0, 12)
	for i := 0; i < 12; i++ {
		msgs = append(msgs, core.Message{
			Role: core.RoleUser,
			Text: fmt.Sprintf("user-%02d", i),
		})
	}
	hydrateSessionMessagesForTest(m, msgs)
	snap := m.assembler.Snapshot()
	if len(snap) != maxHydratedVisibleMessages {
		t.Fatalf("expected %d visible messages, got %d", maxHydratedVisibleMessages, len(snap))
	}
	joined := strings.Join(tuirender.ChatLines(snap, 80), "\n")
	if strings.Contains(joined, "user-03") || !strings.Contains(joined, "user-04") || !strings.Contains(joined, "user-11") {
		t.Fatalf("expected only recent resume messages in UI hydrate:\n%s", joined)
	}
}
func TestSessionHydrationTrimsRenderedResumeHistoryLines(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 80
	m.height = 24
	msgs := make([]core.Message, 0, 8)
	for i := 0; i < 8; i++ {
		msgs = append(msgs, core.Message{
			Role: core.RoleUser,
			Text: fmt.Sprintf("msg-%02d\n%s", i, strings.Repeat("line\n", 70)),
		})
	}
	next, _ := m.Update(svcMsg(protocol.Event{
		Kind:     protocol.EventSessionHydrated,
		Messages: protocolMessagesForTest(msgs),
	}))
	m = next.(model)
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n")
	if strings.Contains(rendered, "msg-00") || !strings.Contains(rendered, "msg-07") {
		t.Fatalf("expected rendered resume transcript to keep recent tail only:\n%s", rendered)
	}
	if got := len(tuirender.ChatLines(m.transcript[1:], m.chatRenderWidth())); got > maxHydratedTranscriptLines {
		t.Fatalf("expected hydrated transcript to be bounded, got %d lines", got)
	}
}
func TestStartupResumeMenuClearsAfterSessionHydration(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.resumeMenu = true
	m.mode = modeSessionPicker

	next, _ := m.Update(svcMsg(protocol.Event{
		Kind:      protocol.EventSessionHydrated,
		SessionID: "s1",
	}))
	m = next.(model)

	if m.resumeMenu {
		t.Fatal("startup resume flag should clear after session hydration")
	}
	if m.mode != modeChat {
		t.Fatalf("mode = %v, want chat", m.mode)
	}
}
func TestCrossWorkspaceResumeInfoRendersInTUI(t *testing.T) {
	m := newModel(nil, "", "", "")
	m.width = 100
	m.height = 24
	msg := strings.Join([]string{
		"This conversation is from a different directory.",
		"",
		"To resume, run:",
		"  cd '/tmp/other workspace' && '/usr/local/bin/whale' resume sess-1",
	}, "\n")

	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventInfo, Text: msg}))
	m = next.(model)
	view := m.View()
	if !strings.Contains(view, "This conversation is from a different directory.") ||
		!strings.Contains(view, "To resume, run:") ||
		!strings.Contains(view, "resume sess-1") {
		t.Fatalf("expected cross-workspace resume message in TUI:\n%s", view)
	}
}
func TestHydrateSessionMessages_RestoresProposedPlanStyle(t *testing.T) {
	m := &model{assembler: tuirender.NewAssembler()}
	msgs := []core.Message{
		{
			Role: core.RoleAssistant,
			Text: "drafting...\n<proposed_plan>\n# Plan\n- Patch renderer\n</proposed_plan>",
		},
	}
	hydrateSessionMessagesForTest(m, msgs)
	snap := m.assembler.Snapshot()
	if len(snap) != 2 || snap[0].Kind != tuirender.KindText || snap[1].Kind != tuirender.KindPlan {
		t.Fatalf("expected assistant text and proposed plan, got %+v", snap)
	}
	rendered := strings.Join(tuirender.ChatLines(snap, 80), "\n")
	for _, want := range []string{"drafting", "Proposed Plan", "Patch renderer"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in hydrated proposed plan:\n%s", want, rendered)
		}
	}
}

func TestHydrateSessionMessages_RestoresStructuredPlanPart(t *testing.T) {
	m := &model{assembler: tuirender.NewAssembler()}
	msgs := []core.Message{
		{
			Role: core.RoleAssistant,
			Text: "drafting...\n# Plan\n- Patch renderer",
			Parts: []core.MessagePart{
				{Type: core.MessagePartText, Text: "drafting..."},
				{Type: core.MessagePartPlan, Text: "# Plan\n- Patch renderer"},
			},
		},
	}
	hydrateSessionMessagesForTest(m, msgs)
	snap := m.assembler.Snapshot()
	if len(snap) != 2 || snap[0].Kind != tuirender.KindText || snap[1].Kind != tuirender.KindPlan {
		t.Fatalf("expected assistant text and structured proposed plan, got %+v", snap)
	}
	rendered := strings.Join(tuirender.ChatLines(snap, 80), "\n")
	for _, want := range []string{"drafting", "Proposed Plan", "Patch renderer"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in hydrated proposed plan:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "<proposed_plan>") {
		t.Fatalf("structured hydration should not render proposed_plan tags:\n%s", rendered)
	}
	if strings.Count(rendered, "Patch renderer") != 1 {
		t.Fatalf("structured hydration should render plan once, got:\n%s", rendered)
	}
}

func TestHydrateSessionMessages_RestoresLegacyProposedPlanFromTextPart(t *testing.T) {
	m := &model{assembler: tuirender.NewAssembler()}
	legacy := "drafting...\n<proposed_plan>\n# Plan\n- Patch renderer\n</proposed_plan>"
	msgs := []core.Message{
		{
			Role: core.RoleAssistant,
			Text: legacy,
			Parts: []core.MessagePart{
				{Type: core.MessagePartText, Text: legacy},
			},
		},
	}
	hydrateSessionMessagesForTest(m, msgs)
	snap := m.assembler.Snapshot()
	if len(snap) != 2 || snap[0].Kind != tuirender.KindText || snap[1].Kind != tuirender.KindPlan {
		t.Fatalf("expected assistant text and legacy proposed plan from text part, got %+v", snap)
	}
	rendered := strings.Join(tuirender.ChatLines(snap, 80), "\n")
	if strings.Contains(rendered, "<proposed_plan>") || strings.Contains(rendered, "</proposed_plan>") {
		t.Fatalf("legacy text part should not render proposed_plan tags:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Patch renderer") {
		t.Fatalf("expected legacy proposed plan text:\n%s", rendered)
	}
}

func TestSessionHydrationCommitsTranscriptAndClearsLiveAssembler(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 80, height: 24}
	next, cmd := m.Update(svcMsg(protocol.Event{
		Kind: protocol.EventSessionHydrated,
		Messages: protocolMessagesForTest([]core.Message{
			{Role: core.RoleUser, Text: "hi"},
			{Role: core.RoleAssistant, Text: "hello"},
		}),
	}))
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected hydration to return wait-event command")
	}
	if got := len(m.assembler.Snapshot()); got != 0 {
		t.Fatalf("expected hydrated transcript committed out of live assembler, got %d entries", got)
	}
	if got := strings.Join(tuirender.ChatLines(m.transcript, 80), "\n"); !strings.Contains(got, "hi") || !strings.Contains(got, "hello") {
		t.Fatalf("expected hydrated messages in transcript:\n%s", got)
	}
}

func TestSessionHydrationRendersStoredToolLifecycleThroughTimelineInOrder(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 100, height: 24}
	next, _ := m.Update(svcMsg(protocol.Event{
		Kind: protocol.EventSessionHydrated,
		Messages: protocolMessagesForTest([]core.Message{
			{Role: core.RoleAssistant, Text: "before tool"},
			{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{
				ID:    "read-1",
				Name:  "read_file",
				Input: `{"file_path":"README.md"}`,
			}}},
			{Role: core.RoleTool, ToolResults: []core.ToolResult{{
				ToolCallID: "read-1",
				Name:       "read_file",
				ModelText:  `{"success":true,"data":{"content":"ok"}}`,
			}}},
			{Role: core.RoleAssistant, Text: "after tool"},
		}),
	}))
	m = next.(model)

	if m.hasPendingLifecycleItems() {
		t.Fatalf("hydrated completed tool should not remain pending: %+v", m.timeline.Snapshot())
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	before := strings.Index(rendered, "before tool")
	tool := strings.Index(rendered, "Read")
	after := strings.Index(rendered, "after tool")
	if before < 0 || tool < 0 || after < 0 {
		t.Fatalf("expected assistant/tool/assistant transcript:\n%s", rendered)
	}
	if !(before < tool && tool < after) {
		t.Fatalf("expected hydrated tool row between assistant messages:\n%s", rendered)
	}
}

func TestSessionHydrationSuppressesStoredAuditOnlyToolLifecycle(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 100, height: 24}
	next, _ := m.Update(svcMsg(protocol.Event{
		Kind: protocol.EventSessionHydrated,
		Messages: protocolMessagesForTest([]core.Message{
			{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{
				ID:    "deny-1",
				Name:  "shell_run",
				Input: `{"command":"printf whale-auto-deny-test >/tmp/whale-auto-deny-test"}`,
			}}},
			{Role: core.RoleTool, ToolResults: []core.ToolResult{{
				ToolCallID: "deny-1",
				Name:       "shell_run",
				ModelText:  `{"success":false,"code":"plan_mode_blocked"}`,
				Outcome:    core.OutcomeFailure,
				Metadata: map[string]any{
					"ui_visibility":       "audit",
					"auto_denied":         true,
					"blocked_reason_code": "plan_mode_blocked",
				},
			}}},
		}),
	}))
	m = next.(model)

	if m.hasPendingLifecycleItems() {
		t.Fatalf("hydrated audit-only tool should not remain pending: %+v", m.timeline.Snapshot())
	}
	if got := m.timeline.Snapshot().Items; len(got) != 0 {
		t.Fatalf("hydrated audit-only tool should be hidden from timeline snapshot: %+v", got)
	}
	rendered := strings.Join(tuirender.ChatLines(m.transcript, 100), "\n")
	if strings.Contains(rendered, "Shell") || strings.Contains(rendered, "plan_mode_blocked") || strings.Contains(rendered, "whale-auto-deny-test") {
		t.Fatalf("hydrated audit-only tool leaked into transcript:\n%s", rendered)
	}
	if m.assembler != nil && len(m.assembler.Snapshot()) != 0 {
		t.Fatalf("hydrated audit-only tool should not leave live entries: %+v", m.assembler.Snapshot())
	}
}

func TestSessionHydratedUpdatesAutoAcceptFooterState(t *testing.T) {
	m := newModel(nil, "deepseek-v4-pro", "high", "on")
	m.width = 100
	m.height = 24
	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventSessionHydrated, AutoAccept: true, AutoAcceptKnown: true}))
	m = next.(model)
	if !m.autoAccept {
		t.Fatal("expected hydrated auto-accept state")
	}
	assertFooterLastLine(t, m.View(), "auto-accept on")
}
func TestSessionHydratedPreservesPrintedStartupHeaderForInitialEmptySession(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "max", "off")
	m.width = 80
	m.height = 24
	if cmd := m.startupHeaderPrintCmd(); cmd == nil {
		t.Fatal("expected startup header to be printed to native scrollback")
	}

	next, cmd := m.Update(svcMsg(protocol.Event{Kind: protocol.EventSessionHydrated, SessionID: "s1"}))
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected wait command after hydration")
	}
	if !m.startupHeaderPrinted || m.startupHeaderOnce == nil || !*m.startupHeaderOnce {
		t.Fatal("expected initial empty hydration to preserve printed startup header")
	}
}
func TestSessionHydratedResetsStartupHeaderForNewEmptySession(t *testing.T) {
	m := newModel(nil, "deepseek-v4-flash", "max", "off")
	m.width = 80
	m.height = 24
	m.sessionID = "old"
	if cmd := m.startupHeaderPrintCmd(); cmd == nil {
		t.Fatal("expected startup header to be printed to native scrollback")
	}

	next, _ := m.Update(svcMsg(protocol.Event{Kind: protocol.EventSessionHydrated, SessionID: "new"}))
	m = next.(model)
	if !m.startupHeaderPrinted || m.startupHeaderOnce == nil || !*m.startupHeaderOnce {
		t.Fatal("expected new empty session hydration to schedule startup header print")
	}
	if m.sessionID != "new" {
		t.Fatalf("expected session id to update, got %q", m.sessionID)
	}
}
