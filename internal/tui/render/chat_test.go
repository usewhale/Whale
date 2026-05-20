package render

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
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
	if !strings.Contains(plain, "  └ (no output)") {
		t.Fatalf("expected child output row, got: %q", plain)
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
		"  └   yaml:",
		"  └ \t- item",
		"  └     nested: value",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected indented output row %q in:\n%s", want, plain)
		}
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
			name: "error",
			role: "shell_result_failed",
			text: "Ran make test\n✗ · command failed",
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
	for _, want := range []string{"✓ · 23ms", "284 model.go", "202 model_events.go", "975 total", "NAME       SIZE", "file.txt   12K"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected shell output line %q, got: %q", want, joined)
		}
	}
	if strings.Contains(joined, "NAME SIZE") || strings.Contains(joined, "file.txt 12K") {
		t.Fatalf("shell output spacing collapsed: %q", joined)
	}
}

func TestMarkdown_NarrowWidthFallback(t *testing.T) {
	input := "a **b** c"
	got := Markdown(input, 10, false)
	if got != input {
		t.Fatalf("expected markdown fallback to plain text for narrow width, got: %q", got)
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
	if strings.Count(got, "https://example.com") != 1 {
		t.Fatalf("expected bare URL once, got: %q", got)
	}
}

func TestMarkdown_TableSelfLinkDoesNotDuplicate(t *testing.T) {
	input := "| 项目 | 地址 |\n|---|---|\n| A | [https://example.com](https://example.com) |\n"
	got := Markdown(input, 80, false)
	if strings.Count(got, "https://example.com") != 1 {
		t.Fatalf("expected self link once, got: %q", got)
	}
}

func TestMarkdown_ExplicitLinkShowsTextAndURL(t *testing.T) {
	input := "[示例](https://example.com)"
	got := Markdown(input, 80, false)
	if !strings.Contains(got, "示例") || !strings.Contains(got, "https://example.com") {
		t.Fatalf("expected link text and URL, got: %q", got)
	}
	if !strings.Contains(got, "示例 (https://example.com)") {
		t.Fatalf("expected terminal link format, got: %q", got)
	}
}

func TestMarkdown_AutolinkDoesNotDuplicate(t *testing.T) {
	input := "PR 已创建：<https://github.com/usewhale/DeepSeek-Code-Whale/pull/92>"
	got := Markdown(input, 100, false)
	if strings.Count(got, "https://github.com/usewhale/DeepSeek-Code-Whale/pull/92") != 1 {
		t.Fatalf("expected autolink URL once, got: %q", got)
	}
	if strings.Contains(got, "<https://github.com/usewhale/DeepSeek-Code-Whale/pull/92>") {
		t.Fatalf("expected autolink brackets removed, got: %q", got)
	}
}

func TestMarkdown_AutolinkPreservesMarkdownPunctuation(t *testing.T) {
	input := "URL：<https://example.com/a*b*c>"
	got := Markdown(input, 100, false)
	if strings.Count(got, "https://example.com/a*b*c") != 1 {
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
	joined := strings.Join(lines, "\n")
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
