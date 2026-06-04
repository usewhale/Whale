package render

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/usewhale/whale/internal/runtime/protocol"
)

func assertVisibleWidthAtMost(t *testing.T, lines []string, maxWidth int) {
	t.Helper()
	for _, line := range lines {
		if got := xansi.StringWidth(line); got > maxWidth {
			t.Fatalf("line width %d exceeds %d: %q", got, maxWidth, line)
		}
	}
}

func joinedPlain(lines []string) string {
	return xansi.Strip(strings.Join(lines, "\n"))
}

func displayLineIsBlank(line string) bool {
	plain := xansi.Strip(line)
	plain = strings.ReplaceAll(plain, "┃", "")
	plain = strings.ReplaceAll(plain, "│", "")
	return strings.TrimSpace(plain) == ""
}

func assertBlankLineBetween(t *testing.T, lines []string, before, after string) {
	t.Helper()
	beforeIdx, afterIdx := -1, -1
	for i, line := range lines {
		plain := xansi.Strip(line)
		if beforeIdx < 0 && strings.Contains(plain, before) {
			beforeIdx = i
			continue
		}
		if beforeIdx >= 0 && strings.Contains(plain, after) {
			afterIdx = i
			break
		}
	}
	if beforeIdx < 0 || afterIdx < 0 {
		t.Fatalf("could not find %q before %q in %q", before, after, strings.Join(lines, "\n"))
	}
	for _, line := range lines[beforeIdx+1 : afterIdx] {
		if displayLineIsBlank(line) {
			return
		}
	}
	t.Fatalf("expected blank line between %q and %q, got: %q", before, after, strings.Join(lines, "\n"))
}

func containsANSIColor(text, color string) bool {
	return strings.Contains(text, "\x1b[38;5;"+color+"m") ||
		strings.Contains(text, "\x1b[1;38;5;"+color+"m")
}

func TestChatLines_MarkdownBoldAndList(t *testing.T) {
	entries := []UIMessage{
		{Role: "assistant", Kind: KindText, Text: "Hello **world**\n- one\n- two"},
	}
	lines := ChatLines(entries, 80)
	if len(lines) == 0 {
		t.Fatalf("expected rendered lines")
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "world") {
		t.Fatalf("expected markdown content, got: %q", joined)
	}
	if !strings.Contains(joined, "one") || !strings.Contains(joined, "two") {
		t.Fatalf("expected list items rendered, got: %q", joined)
	}
}

func TestChatLines_FocusSummaryUsesStructuredStyles(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	entries := []UIMessage{{
		Role: "tool_summary",
		Kind: KindToolSummary,
		Text: "Searched for 1 pattern, Read 1 file, Ran shell: git status --short (ctrl+o to expand)",
		FocusSummary: &FocusSummary{
			Hint: "(ctrl+o to expand)",
			Parts: []FocusSummaryPart{
				{Kind: "search", Action: "Searched for 1 pattern"},
				{Kind: "read", Action: "Read 1 file"},
				{Kind: "shell", Action: "Ran shell", Detail: "git status --short"},
			},
		},
	}}

	lines := ChatLines(entries, 80)
	plain := joinedPlain(lines)
	for _, want := range []string{"Searched for 1 pattern", "Read 1 file", "Ran shell: git status --short", "ctrl+o", "expand"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected focus summary text %q, got:\n%s", want, plain)
		}
	}
	raw := strings.Join(lines, "\n")
	for _, color := range []string{"212", "111", "220", "245"} {
		if !containsANSIColor(raw, color) {
			t.Fatalf("expected focus summary color %s in %q", color, raw)
		}
	}
	if strings.Contains(raw, "\x1b[38;5;78m") {
		t.Fatalf("focus summary should not use success green for completed work, got %q", raw)
	}
	assertVisibleWidthAtMost(t, lines, 80)
}

func TestChatLines_FocusSummaryStaysWithinNarrowWidth(t *testing.T) {
	entries := []UIMessage{{
		Role: "tool_summary",
		Kind: KindToolSummary,
		Text: "Ran shell: git log --oneline --no-decorate v0.1.20..HEAD, 1 file/search read (ctrl+o to expand)",
		FocusSummary: &FocusSummary{
			Hint: "(ctrl+o to expand)",
			Parts: []FocusSummaryPart{
				{Kind: "shell", Action: "Ran shell", Detail: "git log --oneline --no-decorate v0.1.20..HEAD"},
				{Kind: "read", Action: "Read 1 file"},
				{Kind: "search", Action: "Searched for 1 pattern"},
			},
		},
	}}

	lines := ChatLines(entries, 24)
	plain := joinedPlain(lines)
	if !strings.Contains(plain, "Ran shell") || !strings.Contains(plain, "ctrl+o") {
		t.Fatalf("expected narrow focus summary to remain readable, got:\n%s", plain)
	}
	assertVisibleWidthAtMost(t, lines, 24)
}

func TestChatLines_FocusSummaryStylesFromState(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	entries := []UIMessage{{
		Role: "tool_summary",
		Kind: KindToolSummary,
		Text: "Denied 1 shell command, Command failed: go test ./..., Access blocked: ../src, Mode hint: switch to /agent, HTTP 403 Forbidden: https://example.test, Reading 1 file: internal/tui/focus_view.go, Searched for 1 pattern",
		FocusSummary: &FocusSummary{
			Parts: []FocusSummaryPart{
				{Kind: "shell", State: "denied", Count: 1, Action: "Denied 1 shell command"},
				{Kind: "shell", State: "failed", Count: 1, Action: "Command failed", Detail: "go test ./..."},
				{Kind: "read", State: "blocked", Count: 1, Action: "Access blocked", Detail: "../src"},
				{Kind: "mode", State: "mode_hint", Count: 1, Action: "Mode hint", Detail: "switch to /agent"},
				{Kind: "web", State: "http_error", Count: 1, Action: "HTTP 403 Forbidden", Detail: "https://example.test"},
				{Kind: "read", State: "running", Count: 1, Action: "Reading 1 file", Detail: "internal/tui/focus_view.go"},
				{Kind: "search", State: "done", Count: 1, Action: "Searched for 1 pattern"},
			},
		},
	}}

	raw := strings.Join(ChatLines(entries, 100), "\n")
	for _, color := range []string{"214", "203", "220", "117", "212"} {
		if !containsANSIColor(raw, color) {
			t.Fatalf("expected state-driven focus summary color %s in %q", color, raw)
		}
	}
}

func TestChatLines_FocusSummaryFallsBackToStatusStyle(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	entries := []UIMessage{{
		Role: "tool_summary",
		Kind: KindToolSummary,
		Text: "Legacy denied (1 denied/canceled), Legacy failed (1 failed), Legacy running (1 running)",
		FocusSummary: &FocusSummary{
			Parts: []FocusSummaryPart{
				{Kind: "shell", Action: "Legacy denied", Status: "(1 denied/canceled)"},
				{Kind: "shell", Action: "Legacy failed", Status: "(1 failed)"},
				{Kind: "read", Action: "Legacy running", Status: "(1 running)"},
			},
		},
	}}

	raw := strings.Join(ChatLines(entries, 100), "\n")
	for _, color := range []string{"214", "203", "117"} {
		if !containsANSIColor(raw, color) {
			t.Fatalf("expected status fallback focus summary color %s in %q", color, raw)
		}
	}
}

func TestAssembler_PreservesStreamingBlankLineBeforeTable(t *testing.T) {
	a := NewAssembler()
	a.AppendDelta("assistant", "全部完成！以下是操作记录：\n\n")
	a.AppendDelta("assistant", "| 步骤 | 结果 |\n|------|------|\n| ✅ PR | #93 |\n")

	snap := a.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected one coalesced assistant message, got %+v", snap)
	}
	if !strings.Contains(snap[0].Text, "操作记录：\n\n| 步骤") {
		t.Fatalf("streaming blank line before table was not preserved: %q", snap[0].Text)
	}
	rendered := strings.Join(ChatLines(snap, 80), "\n")
	if strings.Contains(rendered, "操作记录：| 步骤") {
		t.Fatalf("table collapsed into preceding paragraph:\n%s", rendered)
	}
	if !strings.Contains(rendered, "步骤") || !strings.Contains(rendered, "结果") || !strings.Contains(rendered, "✅ PR") {
		t.Fatalf("expected rendered table content:\n%s", rendered)
	}
}

func TestAssembler_SetPlanReplacesExistingPlanOnly(t *testing.T) {
	a := NewAssembler()
	a.AddPlan("old plan")
	a.AddNotice("keep notice")
	a.AddPlan("duplicate plan")

	a.SetPlan("new plan")

	snap := a.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected plan and notice rows, got %+v", snap)
	}
	if snap[0].Kind != KindPlan || snap[0].Text != "new plan" {
		t.Fatalf("expected first plan to be replaced, got %+v", snap[0])
	}
	if snap[1].Kind != KindNotice || snap[1].Text != "keep notice" {
		t.Fatalf("expected notice to remain after plan replacement, got %+v", snap[1])
	}
}

func TestChatLines_ThinkingCardHasDistinctLabel(t *testing.T) {
	entries := []UIMessage{
		{Role: "think", Kind: KindThinking, Text: "I should answer carefully."},
	}
	lines := ChatLines(entries, 80)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Thinking") {
		t.Fatalf("expected thinking label, got: %q", joined)
	}
	if !strings.Contains(joined, "I should answer carefully.") {
		t.Fatalf("expected reasoning body, got: %q", joined)
	}
	assertBlankLineBetween(t, lines, "Thinking", "I should answer carefully.")
}

func TestChatLines_ThinkingStreamingShowsTailPreview(t *testing.T) {
	entries := []UIMessage{
		{
			Role:      "think",
			Kind:      KindThinking,
			Streaming: true,
			Text:      strings.Join([]string{"alpha", "bravo", "charlie", "delta", "echo"}, "\n"),
		},
	}
	plain := joinedPlain(ChatLines(entries, 80))
	for _, hidden := range []string{"alpha", "bravo"} {
		if strings.Contains(plain, hidden) {
			t.Fatalf("streaming thinking preview leaked %q:\n%s", hidden, plain)
		}
	}
	for _, want := range []string{"...", "charlie", "delta", "echo"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("streaming thinking preview missing %q:\n%s", want, plain)
		}
	}
}

func TestChatLines_ThinkingSettledShowsHeadTailPreview(t *testing.T) {
	entries := []UIMessage{
		{
			Role: "think",
			Kind: KindThinking,
			Text: strings.Join([]string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf"}, "\n"),
		},
	}
	plain := joinedPlain(ChatLines(entries, 80))
	for _, want := range []string{"alpha", "bravo", "... 3 lines omitted", "foxtrot", "golf"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("settled thinking preview missing %q:\n%s", want, plain)
		}
	}
	for _, hidden := range []string{"charlie", "delta", "echo"} {
		if strings.Contains(plain, hidden) {
			t.Fatalf("settled thinking preview leaked %q:\n%s", hidden, plain)
		}
	}
}

func TestChatLines_ThinkingLargeShowsTailPreview(t *testing.T) {
	lines := make([]string, 85)
	for i := range lines {
		lines[i] = "line-" + string(rune('a'+i%26)) + "-" + string(rune('a'+(i/26)))
	}
	entries := []UIMessage{
		{Role: "think", Kind: KindThinking, Text: strings.Join(lines, "\n")},
	}
	plain := joinedPlain(ChatLines(entries, 80))
	if !strings.Contains(plain, "... reasoning scrolled past") {
		t.Fatalf("large thinking preview missing scrolled-past hint:\n%s", plain)
	}
	if strings.Contains(plain, lines[0]) || strings.Contains(plain, lines[40]) {
		t.Fatalf("large thinking preview leaked early/middle lines:\n%s", plain)
	}
	for _, want := range lines[len(lines)-2:] {
		if !strings.Contains(plain, want) {
			t.Fatalf("large thinking preview missing tail %q:\n%s", want, plain)
		}
	}
}

func TestDisplayThinkingTextCapsLongStreamingSingleLine(t *testing.T) {
	text := strings.Repeat("a", thinkingPreviewLineRuneLimit+50) + "TAIL"

	got := displayThinkingText(text, true, false)
	if strings.Contains(got, "TAIL") {
		t.Fatalf("streaming single-line preview leaked tail suffix")
	}
	if gotRunes := len([]rune(got)); gotRunes > thinkingPreviewLineRuneLimit+3 {
		t.Fatalf("streaming single-line preview length = %d, want capped", gotRunes)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("streaming single-line preview should show truncation marker, got suffix %q", got[len(got)-8:])
	}
}

func TestDisplayThinkingTextCapsLongSettledSingleLine(t *testing.T) {
	text := strings.Repeat("b", thinkingLargeCharThreshold+50) + "TAIL"

	got := displayThinkingText(text, false, false)
	if !strings.Contains(got, "... reasoning scrolled past") {
		t.Fatalf("settled single-line preview missing large-reasoning marker:\n%s", got)
	}
	if strings.Contains(got, "TAIL") {
		t.Fatalf("settled single-line preview leaked tail suffix")
	}
	if gotRunes := len([]rune(got)); gotRunes > thinkingPreviewLineRuneLimit+len([]rune("... reasoning scrolled past\n...")) {
		t.Fatalf("settled single-line preview length = %d, want capped", gotRunes)
	}
}

func TestChatLines_ThinkingFullReasoningBypassesPreview(t *testing.T) {
	text := strings.Join([]string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf"}, "\n")
	entries := []UIMessage{
		{Role: "think", Kind: KindThinking, Streaming: true, FullReasoning: true, Text: text},
	}
	plain := joinedPlain(ChatLines(entries, 80))
	for _, want := range []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("full thinking missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "omitted") || strings.Contains(plain, "scrolled past") {
		t.Fatalf("full thinking should not show preview elision:\n%s", plain)
	}
}

func TestAssemblerMarksLiveThinkingStreaming(t *testing.T) {
	a := NewAssembler()
	a.AppendDelta("think", "alpha")
	a.AppendDelta("think", "\nbravo")
	snap := a.Snapshot()
	if len(snap) != 1 || !snap[0].Streaming {
		t.Fatalf("expected live thinking to be marked streaming, got %+v", snap)
	}
}

func TestChatLines_PlanUpdateHasDistinctLabel(t *testing.T) {
	entries := []UIMessage{
		{Role: "plan", Kind: KindPlanUpdate, Text: "[x] Inspect\n[~] Patch\n[ ] Test"},
	}
	lines := ChatLines(entries, 80)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Updated Plan") {
		t.Fatalf("expected updated plan label, got: %q", joined)
	}
	if !strings.Contains(joined, "Patch") || !strings.Contains(joined, "Test") {
		t.Fatalf("expected plan update body, got: %q", joined)
	}
	for _, want := range []string{"✔ Inspect", "□ Patch", "□ Test"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected friendly plan marker %q, got: %q", want, joined)
		}
	}
	if strings.Contains(joined, "[x]") || strings.Contains(joined, "[~]") || strings.Contains(joined, "[ ]") {
		t.Fatalf("expected raw plan markers to be hidden, got: %q", joined)
	}
	if strings.Contains(joined, "✔ Inspect □ Patch") {
		t.Fatalf("expected plan update lines to stay separate, got: %q", joined)
	}
	assertBlankLineBetween(t, lines, "Updated Plan", "Inspect")
}

func TestChatLines_ProposedPlanHasDistinctLabel(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	entries := []UIMessage{
		{Role: "plan", Kind: KindPlan, Text: "# Plan\n\n- Inspect renderer\n- Add tests"},
	}
	lines := ChatLines(entries, 80)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Proposed Plan") {
		t.Fatalf("expected proposed plan label, got: %q", joined)
	}
	if !strings.Contains(joined, "Plan") || !strings.Contains(joined, "Inspect renderer") || !strings.Contains(joined, "Add tests") {
		t.Fatalf("expected markdown plan body, got: %q", joined)
	}
	if strings.Contains(joined, "Updated Plan") {
		t.Fatalf("proposed plan should not use update-plan label, got: %q", joined)
	}
	if !strings.Contains(joined, "\x1b[48;5;236m") {
		t.Fatalf("expected proposed plan body background styling, got: %q", joined)
	}
	assertBlankLineBetween(t, lines, "Proposed Plan", "Plan")
}

func TestChatLines_UserPromptGlyphAndContinuationIndent(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	entries := []UIMessage{
		{Role: "you", Kind: KindText, Text: "first line\nsecond line"},
	}
	lines := ChatLines(entries, 80)
	joined := joinedPlain(lines)
	if len(lines) < 4 {
		t.Fatalf("expected user prompt vertical padding, got: %q", strings.Join(lines, "\n"))
	}
	if !displayLineIsBlank(lines[0]) || !displayLineIsBlank(lines[len(lines)-2]) {
		t.Fatalf("expected user prompt top and bottom padding, got: %q", strings.Join(lines, "\n"))
	}
	if !strings.Contains(joined, "› first line") {
		t.Fatalf("expected user prompt glyph, got: %q", joined)
	}
	if !strings.Contains(joined, "\n  second line") {
		t.Fatalf("expected continuation indent, got: %q", joined)
	}
	if strings.Contains(joined, "┃") || strings.Contains(joined, "│") {
		t.Fatalf("user prompt should not render as a bordered card: %q", joined)
	}
	raw := strings.Join(lines, "\n")
	if !strings.Contains(raw, "\x1b[48;5;236m") {
		t.Fatalf("expected user prompt background styling, got: %q", raw)
	}
}

func TestChatLines_UserPromptHardWrapsLongLines(t *testing.T) {
	entries := []UIMessage{
		{Role: "you", Kind: KindText, Text: "我想参考 https://github.com/deepseek-ai/awesome-deepseek-integration/pull/584 把 whale 也提一个 PR"},
	}
	lines := ChatLines(entries, 54)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "github.com") || !strings.Contains(joined, "pull/584") {
		t.Fatalf("expected user prompt content, got: %q", joined)
	}
	assertVisibleWidthAtMost(t, lines, 54)
}

func TestChatLines_UserPromptRemainsFullWidthOnWideTerminals(t *testing.T) {
	entries := []UIMessage{
		{Role: "you", Kind: KindText, Text: "what is your model id"},
	}
	lines := ChatLines(entries, 160)
	if len(lines) < 3 {
		t.Fatalf("expected user prompt padding rows, got: %q", strings.Join(lines, "\n"))
	}
	if got := xansi.StringWidth(lines[0]); got != 160 {
		t.Fatalf("expected user prompt background row to stay full width, got %d", got)
	}
	assertVisibleWidthAtMost(t, lines, 160)
}

func TestChatLines_NoticeRendersAsPlainHint(t *testing.T) {
	entries := []UIMessage{
		{Role: "notice", Kind: KindNotice, Text: "✔ You approved whale to run uptime this time"},
	}
	lines := ChatLines(entries, 80)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "✔ You approved whale") {
		t.Fatalf("expected notice text, got: %q", joined)
	}
	if strings.Contains(joined, "┃") || strings.Contains(joined, "│") {
		t.Fatalf("notice should not render as a bordered card: %q", joined)
	}
}

func TestChatLines_SystemNoticeUsesStructuredStyles(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	entries := []UIMessage{{
		Role: "notice",
		Kind: KindNotice,
		Text: "Approved to run uptime · this time",
		Notice: &SystemNotice{
			Kind:    "approval_allowed",
			Tone:    "success",
			Action:  "Approved",
			Detail:  "to run",
			Command: "uptime",
			Scope:   "this time",
		},
	}}
	lines := ChatLines(entries, 80)
	joined := strings.Join(lines, "\n")
	plain := joinedPlain(lines)
	for _, want := range []string{"Approved to run uptime", "this time"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected structured notice to contain %q, got: %q", want, plain)
		}
	}
	if !containsANSIColor(joined, "78") {
		t.Fatalf("expected success color for approval state, got: %q", joined)
	}
	if !containsANSIColor(joined, "220") {
		t.Fatalf("expected command color for approval command, got: %q", joined)
	}
	if strings.Contains(joined, "┃") || strings.Contains(joined, "│") {
		t.Fatalf("notice should not render as a bordered card: %q", joined)
	}
}

func TestChatLines_SystemNoticeWrapsWithinNarrowWidth(t *testing.T) {
	entries := []UIMessage{{
		Role: "notice",
		Kind: KindNotice,
		Text: "Approved to run git log --oneline --decorate --graph --all · for this session",
		Notice: &SystemNotice{
			Kind:    "approval_allowed_session",
			Tone:    "success",
			Action:  "Approved",
			Detail:  "to run",
			Command: "git log --oneline --decorate --graph --all",
			Scope:   "for this session",
		},
	}}
	lines := ChatLines(entries, 32)
	plain := joinedPlain(lines)
	if !strings.Contains(plain, "for this session") {
		t.Fatalf("expected wrapped notice to keep scope, got: %q", plain)
	}
	assertVisibleWidthAtMost(t, lines, 32)
}

func TestChatLines_StatusRendersAsDistinctCard(t *testing.T) {
	entries := []UIMessage{
		{Role: "status", Kind: KindStatus, Text: "The model returned reasoning only and did not produce a visible answer."},
	}
	lines := ChatLines(entries, 80)
	joined := joinedPlain(lines)
	if !strings.Contains(joined, "Reasoning only") {
		t.Fatalf("expected status card title, got: %q", joined)
	}
	if !strings.Contains(joined, "did not produce a visible answer") {
		t.Fatalf("expected status card body, got: %q", joined)
	}
	if !strings.Contains(joined, "┃") {
		t.Fatalf("expected status to render as a bordered card, got: %q", joined)
	}
	assertBlankLineBetween(t, lines, "Reasoning only", "visible answer")
}

func TestChatLines_LocalStatusRendersStructuredCard(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	entries := []UIMessage{{
		Role: "local_status",
		Kind: KindLocalStatus,
		Text: "Status\n\n- session: sess-1",
		Local: &protocol.LocalResult{
			Kind:  "status",
			Title: "Status",
			Fields: []protocol.LocalResultField{
				{Label: "Session", Value: "sess-1"},
				{Label: "Mode", Value: "agent", Tone: "info"},
				{Label: "Model", Value: "deepseek-v4-pro", Tone: "info"},
			},
		},
	}}
	lines := ChatLines(entries, 80)
	joined := joinedPlain(lines)
	for _, want := range []string{"Status", "Session", "sess-1", "Mode", "agent", "Model", "deepseek-v4-pro"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected local status card to contain %q, got:\n%s", want, joined)
		}
	}
	raw := strings.Join(lines, "\n")
	if !strings.Contains(joined, "┃") {
		t.Fatalf("expected local status to render as a bordered card, got:\n%s", joined)
	}
	if strings.Contains(raw, "\x1b[38;5;78m") {
		t.Fatalf("local status should not use success green, got %q", raw)
	}
	assertVisibleWidthAtMost(t, lines, 80)
}

func TestChatLines_LocalMCPRendersStructuredSections(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	entries := []UIMessage{{
		Role: "local_mcp",
		Kind: KindLocalMCP,
		Text: "MCP\n\nconfig: /tmp/mcp.json\nservers: 2",
		Local: &protocol.LocalResult{
			Kind:  "mcp",
			Title: "MCP",
			Fields: []protocol.LocalResultField{
				{Label: "Config", Value: "/tmp/mcp.json"},
				{Label: "Servers", Value: "2", Tone: "info"},
			},
			Sections: []protocol.LocalResultSection{
				{
					Title: "context7",
					Fields: []protocol.LocalResultField{
						{Label: "Status", Value: "connected", Tone: "info"},
						{Label: "Tools", Value: "3"},
					},
				},
				{
					Title: "fs",
					Fields: []protocol.LocalResultField{
						{Label: "Status", Value: "failed", Tone: "error"},
						{Label: "Tools", Value: "0"},
						{Label: "Error", Value: "timeout", Tone: "error"},
					},
				},
			},
		},
	}}
	lines := ChatLines(entries, 80)
	joined := joinedPlain(lines)
	for _, want := range []string{"MCP", "Config", "/tmp/mcp.json", "Servers", "context7", "connected", "fs", "failed", "timeout"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected local mcp card to contain %q, got:\n%s", want, joined)
		}
	}
	raw := strings.Join(lines, "\n")
	if strings.Contains(raw, "\x1b[38;5;78m") {
		t.Fatalf("local mcp should not use success green, got %q", raw)
	}
	assertVisibleWidthAtMost(t, lines, 80)
}

func TestChatLines_LocalResultFieldsStayWithinNarrowCard(t *testing.T) {
	entries := []UIMessage{{
		Role: "local_status",
		Kind: KindLocalStatus,
		Text: "Status\n\n- original workspace: /very/long/workspace/path",
		Local: &protocol.LocalResult{
			Kind:  "status",
			Title: "Status",
			Fields: []protocol.LocalResultField{
				{Label: "Original workspace", Value: "/very/long/workspace/path/that/must/wrap"},
				{Label: "Context window", Value: "100% left (0 used / 128k)"},
			},
		},
	}}
	lines := ChatLines(entries, 20)
	joined := joinedPlain(lines)
	if !strings.Contains(joined, "Status") || !strings.Contains(joined, "/very") {
		t.Fatalf("expected narrow local result to remain readable, got:\n%s", joined)
	}
	assertVisibleWidthAtMost(t, lines, 20)
}

func TestChatLines_LocalResultMultilineMarkdownDoesNotExpandBlankLines(t *testing.T) {
	entries := []UIMessage{{
		Role: "local_workflow",
		Kind: KindLocalStatus,
		Text: "Workflow completed",
		Local: &protocol.LocalResult{
			Kind:  "workflow-terminal",
			Title: "Workflow completed",
			Sections: []protocol.LocalResultSection{{
				Title: "Result",
				Fields: []protocol.LocalResultField{{
					Label: "decision",
					Value: "## Release Decision\n\n| File | Purpose |\n|---|---|\n| `NewsletterSignup.vue` | Component |\n\nDO NOT SHIP",
				}},
			}},
		},
	}}
	lines := ChatLines(entries, 80)
	joined := joinedPlain(lines)
	for _, want := range []string{"Release Decision", "| File | Purpose |", "DO NOT SHIP"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected multiline workflow result to contain %q, got:\n%s", want, joined)
		}
	}
	blankRun := 0
	for _, line := range strings.Split(joined, "\n") {
		if strings.TrimSpace(strings.Trim(line, "┃╭╮╰╯─│ ")) == "" {
			blankRun++
			if blankRun > 3 {
				t.Fatalf("workflow result rendered too many consecutive blank lines:\n%s", joined)
			}
			continue
		}
		blankRun = 0
	}
	assertVisibleWidthAtMost(t, lines, 80)
}

func TestChatLines_ContinuationIndent(t *testing.T) {
	entries := []UIMessage{
		{Role: "assistant", Kind: KindText, Text: "line1\n\nline2"},
	}
	lines := ChatLines(entries, 80)
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %d", len(lines))
	}
	joined := joinedPlain(lines)
	if !strings.Contains(joined, "line1") || !strings.Contains(joined, "line2") {
		t.Fatalf("expected multiline content preserved: %q", joined)
	}
	if strings.Contains(joined, "⎿") {
		t.Fatalf("assistant text should render as body text, got: %q", joined)
	}
}

func TestChatLines_AssistantMarkdownDoesNotUseBorderCard(t *testing.T) {
	entries := []UIMessage{
		{Role: "assistant", Kind: KindText, Text: "```markdown\n<p align=\"center\">\n  <a href=\"https://github.com/usewhale/whale/stargazers\">\n    <img src=\"https://img.shields.io/github/stars/usewhale/whale?style=for-the-badge&logo=github\" alt=\"stars\">\n  </a>\n</p>\n```"},
	}
	lines := ChatLines(entries, 80)
	joined := joinedPlain(lines)
	if !strings.Contains(joined, "img src=") {
		t.Fatalf("expected code block content, got: %q", joined)
	}
	if strings.Contains(joined, "┃") || strings.Contains(joined, "│") {
		t.Fatalf("assistant markdown should not render as a bordered card: %q", joined)
	}
	if strings.Contains(joined, "⎿") {
		t.Fatalf("assistant markdown should render as body text, got: %q", joined)
	}
	assertVisibleWidthAtMost(t, lines, 80)
}

func TestChatLines_AssistantMarkdownHardWrapsLongLines(t *testing.T) {
	entries := []UIMessage{
		{
			Role: "assistant",
			Kind: KindText,
			Text: "我会选：\n\n这个缓存特性对 Agent 场景很有价值：Agent 每轮对话的 system prompt、tool spec 都是重复的，如果 context 布局设计得好，缓存命中率能非常高。\n\n```markdown\n<img src=\"https://img.shields.io/github/stars/usewhale/whale?style=for-the-badge&logo=github\" alt=\"stars\">\n```",
		},
	}
	lines := ChatLines(entries, 54)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "缓存特性") || !strings.Contains(joined, "img src=") {
		t.Fatalf("expected rendered content, got: %q", joined)
	}
	assertVisibleWidthAtMost(t, lines, 54)
}

func TestChatLines_AssistantMarkdownUsesReadableWidthOnWideTerminals(t *testing.T) {
	entries := []UIMessage{
		{
			Role: "assistant",
			Kind: KindText,
			Text: strings.Repeat("assistant-output-segment ", 12),
		},
	}
	lines := ChatLines(entries, 180)
	plain := joinedPlain(lines)
	if !strings.Contains(plain, "assistant-output-segment") {
		t.Fatalf("expected assistant output, got:\n%s", plain)
	}
	assertVisibleWidthAtMost(t, lines, 112)
}

func TestChatLines_ToolEventHardWrapsLongLines(t *testing.T) {
	entries := []UIMessage{
		{
			Role: "result",
			Kind: KindToolResult,
			Text: "git: " + strings.Repeat("very-long-output-segment-", 8),
		},
	}
	lines := ChatLines(entries, 54)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "git:") {
		t.Fatalf("expected tool result content, got: %q", joined)
	}
	if strings.Contains(joinedPlain(lines), "┃") || strings.Contains(joinedPlain(lines), "│") {
		t.Fatalf("tool result should render as event rows, got: %q", joinedPlain(lines))
	}
	assertVisibleWidthAtMost(t, lines, 54)
}

func TestChatLines_ToolEventKeepsFullAvailableWidthOnWideTerminals(t *testing.T) {
	entries := []UIMessage{
		{
			Role: "result",
			Kind: KindToolResult,
			Text: "git: " + strings.Repeat("wide-tool-output ", 12),
		},
	}
	lines := ChatLines(entries, 180)
	hasWideLine := false
	for _, line := range lines {
		if xansi.StringWidth(line) > 112 {
			hasWideLine = true
			break
		}
	}
	if !hasWideLine {
		t.Fatalf("expected tool output to use more than assistant readable width, got:\n%s", joinedPlain(lines))
	}
	assertVisibleWidthAtMost(t, lines, 180)
}

func TestChatLines_ShellToolRendersAsEventRows(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	entries := []UIMessage{
		{
			Role: "shell_result_ok",
			Kind: KindToolCall,
			Text: "Ran ./bin/whale --dangerously-skip-permissions\n(no output)",
		},
	}
	lines := ChatLines(entries, 100)
	plain := joinedPlain(lines)
	if !strings.Contains(plain, "• Ran ./bin/whale --dangerously-skip-permissions") {
		t.Fatalf("expected shell command event header, got: %q", plain)
	}
	if !strings.Contains(plain, "  (no output)") {
		t.Fatalf("expected child output row, got: %q", plain)
	}
	if strings.Contains(plain, "  └ (no output)") {
		t.Fatalf("shell output should render as body text, got: %q", plain)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "\x1b[") {
		t.Fatalf("expected styled command event, got: %q", strings.Join(lines, "\n"))
	}
}

func TestRenderCommandLikePreservesShellCommandText(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	cmd := "printf 'a  b'\n  echo \"c  d\" | head -1"
	rendered := RenderCommandLike(cmd)
	if got := xansi.Strip(rendered); got != cmd {
		t.Fatalf("command rendering changed text:\nwant %q\n got %q", cmd, got)
	}
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("expected command token styling, got %q", rendered)
	}
}

func TestRenderCommandLikeStylesCommandAfterOperator(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	cmd := "git status | head -1\nprintf 'done'"
	rendered := RenderCommandLike(cmd)
	if got := xansi.Strip(rendered); got != cmd {
		t.Fatalf("command rendering changed text:\nwant %q\n got %q", cmd, got)
	}
	for _, want := range []string{"\x1b[38;5;111mgit", "\x1b[38;5;212m|", "\x1b[38;5;111mhead", "\x1b[38;5;111mprintf"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected styled command token %q, got %q", want, rendered)
		}
	}
}

func TestRenderCommandLikeHighlightsBashSyntax(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	cmd := `for f in *.go; do echo "$f"; done # comment`
	rendered := RenderCommandLike(cmd)
	if got := xansi.Strip(rendered); got != cmd {
		t.Fatalf("command rendering changed text:\nwant %q\n got %q", cmd, got)
	}
	for _, want := range []string{
		"\x1b[38;5;212mfor",
		"\x1b[38;5;212m;",
		"\x1b[38;5;81m\"",
		"\x1b[38;5;245m# comment",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected bash syntax style %q, got %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "\x1b[38;5;78m") {
		t.Fatalf("command rendering should not use success green, got %q", rendered)
	}
}

func TestRenderCommandLikeFallbackPreservesHugeCommandText(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	cmd := strings.Repeat("x", maxShellHighlightBytes+1)
	rendered := RenderCommandLike(cmd)
	if got := xansi.Strip(rendered); got != cmd {
		t.Fatalf("fallback command rendering changed text:\nwant len %d\n got len %d", len(cmd), len(got))
	}
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("expected fallback command styling, got %q", rendered[:80])
	}
}

func TestChatLines_ToolEventPreservesIndentedOutputRows(t *testing.T) {
	entries := []UIMessage{
		{
			Role: "shell_result_ok",
			Kind: KindToolResult,
			Text: "Ran cat config.yml\n  yaml:\n\t- item\n    nested: value",
		},
	}

	plain := joinedPlain(ChatLines(entries, 100))
	for _, want := range []string{
		"    yaml:",
		"  \t- item",
		"      nested: value",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected indented output row %q in:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "  └   yaml:") {
		t.Fatalf("shell output should not render as nested action rows:\n%s", plain)
	}
}

func TestChatLines_ToolEventStatusWordsUseSemanticColors(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	cases := []struct {
		name string
		role string
		text string
		ansi string
	}{
		{
			name: "denied",
			role: "shell_result_denied",
			text: "Ran shell command\nDENIED · tool approval denied",
			ansi: "\x1b[38;5;214m",
		},
		{
			name: "warning",
			role: "result",
			text: "Ran hook\nWARNING · skipped optional hook",
			ansi: "\x1b[38;5;220m",
		},
		{
			name: "http error",
			role: "result_http_error",
			text: "Explored\nFetch https://httpbin.org/status/404\nHTTP 404 Not Found",
			ansi: "\x1b[38;5;220m",
		},
		{
			name: "legacy error token",
			role: "shell_result_failed",
			text: "Ran make test\n✗ · command failed",
			ansi: "\x1b[38;5;203m",
		},
		{
			name: "command failed label",
			role: "shell_result_failed",
			text: "Command failed (exit 2): make test\nCommand failed (exit 2) · 1.2s",
			ansi: "\x1b[38;5;203m",
		},
		{
			name: "timeout",
			role: "shell_result_timeout",
			text: "Ran sleep 30\nTIMEOUT · 30s",
			ansi: "\x1b[38;5;215m",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := ChatLines([]UIMessage{{Role: tc.role, Kind: KindToolCall, Text: tc.text}}, 100)
			raw := strings.Join(lines, "\n")
			if !strings.Contains(raw, tc.ansi) && !strings.Contains(raw, strings.Replace(tc.ansi, "[", "[1;", 1)) {
				t.Fatalf("expected semantic color %q in %q", tc.ansi, raw)
			}
			if !strings.Contains(joinedPlain(lines), strings.Split(tc.text, "\n")[1]) {
				t.Fatalf("expected status line in rendered output: %q", joinedPlain(lines))
			}
		})
	}
}

func TestChatLines_ExploreToolRendersNestedActions(t *testing.T) {
	entries := []UIMessage{
		{
			Role: "result_ok",
			Kind: KindToolCall,
			Text: "Explored\nSearch Focus view in internal/tui\nRead focus_view.go",
		},
	}
	lines := ChatLines(entries, 100)
	plain := joinedPlain(lines)
	for _, want := range []string{"• Explored", "  └ Search Focus view in internal/tui", "  └ Read focus_view.go"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected %q in event rows, got: %q", want, plain)
		}
	}
}

func TestChatLines_WorkSeparatorBeforeFinalAssistantAfterTool(t *testing.T) {
	entries := []UIMessage{
		{Role: "you", Kind: KindText, Text: "run tests"},
		{Role: "shell_result_ok", Kind: KindToolCall, Text: "Ran make test\n(no output)"},
		{Role: "assistant", Kind: KindText, Text: "Done."},
	}
	lines := ChatLines(entries, 80)
	plain := joinedPlain(lines)
	if !strings.Contains(plain, strings.Repeat("─", 80)) {
		t.Fatalf("expected work separator before final assistant, got: %q", plain)
	}
	if strings.Index(plain, "Ran make test") > strings.Index(plain, strings.Repeat("─", 80)) ||
		strings.Index(plain, strings.Repeat("─", 80)) > strings.Index(plain, "Done.") {
		t.Fatalf("expected separator between tool event and assistant answer, got: %q", plain)
	}
}

func TestChatLines_AssistantTextUsesInsetEventBullet(t *testing.T) {
	entries := []UIMessage{
		{Role: "assistant", Kind: KindText, Text: "line1\n\nline2"},
	}
	lines := ChatLines(entries, 80)
	plain := joinedPlain(lines)
	if !strings.Contains(plain, "• line1") || !strings.Contains(plain, "\n  line2") {
		t.Fatalf("expected assistant inset with continuation indent, got: %q", plain)
	}
}

func TestChatLines_ShellResultPreservesOutputLines(t *testing.T) {
	entries := []UIMessage{
		{
			Role: "shell_result_ok",
			Kind: KindToolCall,
			Text: "Ran cd internal/tui && wc -l model.go model_events.go model_keys.go model_prompt.go\n✓ · 23ms\n284 model.go\n202 model_events.go\n401 model_keys.go\n88 model_prompt.go\n975 total\nNAME       SIZE\nfile.txt   12K",
		},
	}
	lines := ChatLines(entries, 100)
	joined := joinedPlain(lines)
	if strings.Contains(joined, "23ms 284 model.go") {
		t.Fatalf("shell status and output collapsed onto one line: %q", joined)
	}
	if !strings.Contains(joined, "• Ran cd internal/tui && wc -l model.go model_events.go model_keys.go model_prompt.go  ✓ · 23ms") {
		t.Fatalf("expected shell status in header, got: %q", joined)
	}
	if strings.Contains(joined, "  └ 284 model.go") {
		t.Fatalf("shell output should render as body text, got: %q", joined)
	}
	for _, want := range []string{"✓ · 23ms", "284 model.go", "202 model_events.go", "975 total", "NAME       SIZE", "file.txt   12K"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected shell output line %q, got: %q", want, joined)
		}
	}
	if strings.Contains(joined, "NAME SIZE") || strings.Contains(joined, "file.txt 12K") {
		t.Fatalf("shell output spacing collapsed: %q", joined)
	}
}

func TestChatLines_ShellResultPreservesANSIOutput(t *testing.T) {
	entries := []UIMessage{
		{
			Role: "shell_result_ok",
			Kind: KindToolCall,
			Text: "Ran printf colors\n✓ · 23ms\n\x1b[31mRED\x1b[0m\n\x1b[34mBLUE\x1b[0m",
		},
	}
	lines := ChatLines(entries, 100)
	raw := strings.Join(lines, "\n")
	plain := xansi.Strip(raw)
	for _, want := range []string{"RED", "BLUE"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected visible output %q preserved, got: %q", want, plain)
		}
	}
	for _, want := range []string{"\x1b[31mRED\x1b[0m", "\x1b[34mBLUE\x1b[0m"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("expected ANSI output %q preserved, got: %q", want, raw)
		}
	}
}

func TestMarkdown_NarrowWidthFallback(t *testing.T) {
	input := "a **b** c"
	got := Markdown(input, 10, false)
	if got != input {
		t.Fatalf("expected markdown fallback to plain text for narrow width, got: %q", got)
	}
}

func TestMarkdown_InlineCodeUsesColor(t *testing.T) {
	input := "Run `git status` before `origin/main`."
	got := Markdown(input, 80, false)
	plain := xansi.Strip(got)
	for _, want := range []string{"git status", "origin/main"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected inline code text %q preserved, got: %q", want, got)
		}
	}
	if !strings.Contains(got, "\x1b[") {
		t.Fatalf("expected inline code styling, got: %q", got)
	}
}

func TestMarkdown_QuietInlineCodeKeepsBackticks(t *testing.T) {
	input := "Run `git status` before `origin/main`."
	got := Markdown(input, 80, true)
	plain := xansi.Strip(got)
	for _, want := range []string{"`git status`", "`origin/main`"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected quiet inline code marker %q preserved, got: %q", want, got)
		}
	}
}

func TestMarkdown_NarrowWidthAutolinkDoesNotLeakEscapes(t *testing.T) {
	input := "URL：<https://example.com/a-b.c>"
	got := Markdown(input, 10, false)
	want := "URL：https://example.com/a-b.c"
	if got != want {
		t.Fatalf("expected narrow autolink fallback without escapes, got %q want %q", got, want)
	}
}

func TestMarkdown_TableBareURLDoesNotDuplicate(t *testing.T) {
	input := "| 项目 | 地址 |\n|---|---|\n| A | https://example.com |\n"
	got := Markdown(input, 80, false)
	if strings.Count(xansi.Strip(got), "https://example.com") != 1 {
		t.Fatalf("expected bare URL once, got: %q", got)
	}
}

func TestMarkdown_TableSelfLinkDoesNotDuplicate(t *testing.T) {
	input := "| 项目 | 地址 |\n|---|---|\n| A | [https://example.com](https://example.com) |\n"
	got := Markdown(input, 80, false)
	if strings.Count(xansi.Strip(got), "https://example.com") != 1 {
		t.Fatalf("expected self link once, got: %q", got)
	}
}

func TestMarkdown_ExplicitLinkShowsTextAndURL(t *testing.T) {
	input := "[示例](https://example.com)"
	got := Markdown(input, 80, false)
	visible := xansi.Strip(got)
	if !strings.Contains(visible, "示例") || !strings.Contains(visible, "https://example.com") {
		t.Fatalf("expected link text and URL, got: %q", got)
	}
	if !strings.Contains(visible, "示例 (https://example.com)") {
		t.Fatalf("expected terminal link format, got: %q", got)
	}
}

func TestMarkdown_AutolinkDoesNotDuplicate(t *testing.T) {
	input := "PR 已创建：<https://github.com/usewhale/DeepSeek-Code-Whale/pull/92>"
	got := Markdown(input, 100, false)
	if strings.Count(xansi.Strip(got), "https://github.com/usewhale/DeepSeek-Code-Whale/pull/92") != 1 {
		t.Fatalf("expected autolink URL once, got: %q", got)
	}
	if strings.Contains(got, "<https://github.com/usewhale/DeepSeek-Code-Whale/pull/92>") {
		t.Fatalf("expected autolink brackets removed, got: %q", got)
	}
}

func TestMarkdown_AutolinkPreservesMarkdownPunctuation(t *testing.T) {
	input := "URL：<https://example.com/a*b*c>"
	got := Markdown(input, 100, false)
	if strings.Count(xansi.Strip(got), "https://example.com/a*b*c") != 1 {
		t.Fatalf("expected autolink with markdown punctuation preserved once, got: %q", got)
	}
	if strings.Contains(got, "abc") || strings.Contains(got, "\\*") {
		t.Fatalf("expected literal asterisks in rendered autolink, got: %q", got)
	}
}

func TestMarkdown_HTMLTagIsNotTreatedAsAutolink(t *testing.T) {
	input := `<p align="center"><a href="https://example.com">Example</a></p>`
	got := normalizeMarkdownLinks(input, escapeAutolinksForRenderer)
	if got != input {
		t.Fatalf("expected HTML tags to bypass autolink normalization, got: %q", got)
	}
}

func TestMarkdown_CodeBlockKeepsLinksLiteral(t *testing.T) {
	input := "```md\n[示例](https://example.com)\nhttps://example.com\n```"
	got := Markdown(input, 80, false)
	if strings.Count(got, "https://example.com") != 2 {
		t.Fatalf("expected code block links preserved literally, got: %q", got)
	}
	if !strings.Contains(got, "[示例](https://example.com)") {
		t.Fatalf("expected markdown link preserved in code block, got: %q", got)
	}
}

func TestMarkdown_InlineCodeKeepsLinksLiteral(t *testing.T) {
	input := "`[示例](https://example.com)` and `https://example.com`"
	got := Markdown(input, 80, false)
	if !strings.Contains(got, "[示例](https://example.com)") {
		t.Fatalf("expected inline markdown link preserved, got: %q", got)
	}
	if strings.Contains(got, "示例 (https://example.com)") {
		t.Fatalf("did not expect inline code link to be rewritten, got: %q", got)
	}
}

func TestMarkdown_InlineCodeKeepsWindowsPathLiteral(t *testing.T) {
	input := "`C:\\Users\\goranka\\.whale\\plugins\\memory`"
	got := Markdown(input, 100, false)
	if !strings.Contains(got, `C:\Users\goranka\.whale\plugins\memory`) {
		t.Fatalf("expected inline code Windows path preserved, got: %q", got)
	}
	if strings.Contains(got, `C:\Users\goranka.whale`) {
		t.Fatalf("Windows path lost backslash before dot: %q", got)
	}
}

func TestChatLines_ChineseParagraphAndList_NoCollapsedList(t *testing.T) {
	entries := []UIMessage{
		{
			Role: "assistant",
			Kind: KindText,
			Text: "你好！我是 Claude。\n我可以帮你完成各种任务，比如：\n- 阅读和编辑文件\n- 搜索和查找信息\n- 获取网页内容",
		},
	}
	lines := ChatLines(entries, 90)
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "比如：-") {
		t.Fatalf("list collapsed into paragraph: %q", joined)
	}
	if !strings.Contains(joined, "• 阅读和编辑文件") {
		t.Fatalf("expected first bullet rendered: %q", joined)
	}
	if !strings.Contains(joined, "• 搜索和查找信息") {
		t.Fatalf("expected second bullet rendered: %q", joined)
	}
	if strings.Contains(joined, "\n\n\n") {
		t.Fatalf("unexpected excessive blank lines: %q", joined)
	}
}

func TestChatLines_OrderedListKeepsDotSeparator(t *testing.T) {
	entries := []UIMessage{
		{
			Role: "assistant",
			Kind: KindText,
			Text: "1. `core.py`（720 行）\n2. `server.py` + `routing.py`\n3. 测试覆盖率",
		},
	}
	lines := ChatLines(entries, 90)
	joined := joinedPlain(lines)
	if strings.Contains(joined, "1core.py") || strings.Contains(joined, "2server.py") {
		t.Fatalf("ordered list marker collapsed into text: %q", joined)
	}
	if !strings.Contains(joined, "1. core.py") || !strings.Contains(joined, "2. server.py") {
		t.Fatalf("expected ordered list marker with dot separator: %q", joined)
	}
}

func TestChatLines_ToolJSON_PreservesMultilineBlock(t *testing.T) {
	entries := []UIMessage{
		{
			Role: "result",
			Kind: KindToolResult,
			Text: "shell_run: ```json\n{\"ok\":true,\"data\":{\"payload\":{\"command\":\"date\"}}}\n```",
		},
	}
	lines := ChatLines(entries, 100)
	if len(lines) < 2 {
		t.Fatalf("expected multiline render for tool json, got: %v", lines)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "shell_run:") {
		t.Fatalf("expected tool label: %q", joined)
	}
	if !strings.Contains(joined, "command") {
		t.Fatalf("expected json content: %q", joined)
	}
}
