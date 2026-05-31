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
			toolResults = append(toolResults, protocol.ToolResult{ToolCallID: result.ToolCallID, Name: result.Name, Content: result.Content, Metadata: result.Metadata, IsError: result.IsError})
		}
		out = append(out, protocol.Message{
			ID:           message.ID,
			SessionID:    message.SessionID,
			Role:         string(message.Role),
			Text:         message.Text,
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
				Content:    `{"success":true,"data":{"explanation":"resume checklist","plan":[{"step":"Inspect","status":"completed"},{"step":"Patch","status":"in_progress"},{"step":"Test","status":"pending"}]}}`,
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

func TestRewindHydrationClearsVisibleTranscriptAndRestoresInput(t *testing.T) {
	m := model{assembler: tuirender.NewAssembler(), mode: modeChat, width: 80, height: 24}
	m.append("you", "a")
	m.append("you", "b")
	m.append("you", "c")
	m.commitLiveTranscript(true)

	next, cmd := m.Update(svcMsg(protocol.Event{
		Kind: protocol.EventSessionHydrated,
		Messages: protocolMessagesForTest([]core.Message{
			{Role: core.RoleUser, Text: "a"},
		}),
		Metadata: map[string]any{
			"rewind":        true,
			"restore_input": "b",
		},
	}))
	m = next.(model)
	if cmd == nil {
		t.Fatal("expected rewind hydration to request a screen clear")
	}
	rendered := strings.Join(tuirender.ChatLines(m.chatMessages(), 80), "\n")
	if !strings.Contains(rendered, "a") || strings.Contains(rendered, "c") {
		t.Fatalf("expected rewind view to show only messages before target:\n%s", rendered)
	}
	if got := m.input.Value(); got != "b" {
		t.Fatalf("expected target prompt restored to composer, got %q", got)
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
