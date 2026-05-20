package render

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

func ChatLines(messages []UIMessage, width int) []string {
	if len(messages) == 0 {
		return nil
	}
	if width < 20 {
		width = 20
	}
	out := make([]string, 0, len(messages)*6)
	pendingWorkSeparator := false
	for _, e := range messages {
		block := strings.TrimSpace(e.Text)
		if block == "" {
			continue
		}
		if pendingWorkSeparator && NeedsWorkSeparatorBefore(e) {
			out = append(out, WorkSeparator(width))
			out = append(out, "")
			pendingWorkSeparator = false
		}
		out = append(out, renderCard(e, block, width)...)
		out = append(out, "")
		switch {
		case IsWorkEvent(e):
			pendingWorkSeparator = true
		case e.Role == "you" || (e.Role == "assistant" && e.Kind == KindText):
			pendingWorkSeparator = false
		}
	}
	return out
}

func IsWorkEvent(m UIMessage) bool {
	return m.Kind == KindToolCall || m.Kind == KindToolResult
}

func NeedsWorkSeparatorBefore(m UIMessage) bool {
	return m.Role == "assistant" && m.Kind == KindText
}

func spacedCardStyle(width int, borderColor lipgloss.Color) lipgloss.Style {
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.ThickBorder()).
		BorderLeft(true).
		BorderTop(false).
		BorderRight(false).
		BorderBottom(false).
		BorderForeground(borderColor).
		PaddingLeft(1).
		PaddingTop(1).
		PaddingBottom(1).
		Width(width - 1)
}

func renderEntryText(role, text string, width int) string {
	quiet := role == "you"
	switch role {
	case "assistant", "think", "plan", "status", "result", "result_ok", "result_denied", "result_failed", "result_timeout", "result_canceled", "result_error", "result_running", "error", "info", "tool", "tool_summary":
		return Markdown(text, width, quiet)
	case "shell_result_ok", "shell_result_denied", "shell_result_failed", "shell_result_timeout", "shell_result_canceled", "shell_result_error", "shell_result_running":
		return text
	default:
		return text
	}
}

func renderCard(m UIMessage, block string, width int) []string {
	if m.Role == "header" {
		return strings.Split(strings.TrimRight(block, "\n"), "\n")
	}
	if m.Role == "you" {
		return renderUserPrompt(block, width)
	}
	if m.Role == "assistant" && m.Kind == KindText {
		return renderAssistantMarkdown(block, width)
	}
	if m.Kind == KindNotice || m.Role == "notice" {
		return renderNotice(block, width)
	}
	if m.Kind == KindStatus || m.Role == "status" {
		return renderStatusCard(m, block, width)
	}
	if m.Kind == KindThinking || m.Role == "think" {
		return renderThinkingCard(m, block, width)
	}
	if m.Kind == KindPlanUpdate {
		return renderPlanUpdateCard(m, block, width)
	}
	if IsWorkEvent(m) {
		return renderToolEvent(m, block, width)
	}
	borderColor := roleBorderColor(m)

	contentWidth := width - 6
	if contentWidth < 16 {
		contentWidth = 16
	}

	rendered := hardWrapRendered(renderEntryText(m.Role, block, contentWidth), contentWidth)

	card := spacedCardStyle(width, borderColor).
		Render(strings.TrimRight(rendered, "\n"))

	return strings.Split(strings.TrimRight(card, "\n"), "\n")
}

func renderPlanUpdateCard(m UIMessage, block string, width int) []string {
	contentWidth := width - 6
	if contentWidth < 16 {
		contentWidth = 16
	}
	title := lipgloss.NewStyle().
		Foreground(roleBorderColor(m)).
		Bold(true).
		Render("Updated Plan")
	body := renderPlanUpdateBody(block, contentWidth)
	rendered := joinTitleAndBody(title, body)
	card := spacedCardStyle(width, roleBorderColor(m)).
		Render(rendered)
	return strings.Split(strings.TrimRight(card, "\n"), "\n")
}

func renderPlanUpdateBody(block string, width int) string {
	lines := strings.Split(strings.TrimSpace(block), "\n")
	body := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(body) > 0 && body[len(body)-1] != "" {
				body = append(body, "")
			}
			continue
		}
		status, text, ok := parsePlanUpdateLine(line)
		if !ok {
			style := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted).Italic(true)
			body = append(body, wrapWithPrefixes(style.Render(line), "", "", width-4)...)
			continue
		}
		body = append(body, renderPlanUpdateStep(status, text, width-4)...)
	}
	if len(body) == 0 {
		return ""
	}
	return strings.TrimRight(strings.Join(prefixPlanUpdateBody(body), "\n"), "\n")
}

func parsePlanUpdateLine(line string) (string, string, bool) {
	for _, prefix := range []string{"[x] ", "[X] ", "[~] ", "[ ] "} {
		if strings.HasPrefix(line, prefix) {
			return prefix, strings.TrimSpace(strings.TrimPrefix(line, prefix)), true
		}
	}
	return "", "", false
}

func renderPlanUpdateStep(status, text string, width int) []string {
	var marker string
	var style lipgloss.Style
	switch status {
	case "[x] ", "[X] ":
		marker = "✔ "
		style = lipgloss.NewStyle().
			Foreground(tuitheme.Default.Muted).
			Strikethrough(true)
	case "[~] ":
		marker = "□ "
		style = lipgloss.NewStyle().
			Foreground(tuitheme.Default.Plan).
			Bold(true)
	default:
		marker = "□ "
		style = lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)
	}
	return wrapWithPrefixes(style.Render(marker+text), "", "  ", width)
}

func prefixPlanUpdateBody(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if len(out) == 0 {
			out = append(out, "  └ "+line)
			continue
		}
		out = append(out, "    "+line)
	}
	return out
}

func wrapWithPrefixes(text, firstPrefix, nextPrefix string, width int) []string {
	prefixWidth := lipgloss.Width(firstPrefix)
	if nextWidth := lipgloss.Width(nextPrefix); nextWidth > prefixWidth {
		prefixWidth = nextWidth
	}
	wrapWidth := width - prefixWidth
	if wrapWidth < 8 {
		wrapWidth = 8
	}
	wrapped := hardWrapRendered(text, wrapWidth)
	lines := strings.Split(strings.TrimRight(wrapped, "\n"), "\n")
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			out = append(out, firstPrefix+line)
			continue
		}
		out = append(out, nextPrefix+line)
	}
	return out
}

func joinTitleAndBody(title, body string) string {
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return title
	}
	return title + "\n\n" + body
}

func renderAssistantMarkdown(block string, width int) []string {
	contentWidth := width - 2
	if contentWidth < 16 {
		contentWidth = 16
	}
	rendered := strings.TrimRight(hardWrapRendered(renderEntryText("assistant", block, contentWidth), contentWidth), "\n")
	if rendered == "" {
		return nil
	}
	lines := strings.Split(rendered, "\n")
	out := make([]string, 0, len(lines))
	firstContent := true
	for _, line := range lines {
		if strings.TrimSpace(xansi.Strip(line)) == "" {
			out = append(out, "")
			continue
		}
		if firstContent {
			out = append(out, tuitheme.MutedStyle().Render("•")+" "+line)
			firstContent = false
			continue
		}
		out = append(out, "  "+line)
	}
	return out
}

func hardWrapRendered(text string, width int) string {
	if width < 1 || text == "" {
		return text
	}
	return xansi.Hardwrap(text, width, true)
}

func renderNotice(block string, width int) []string {
	contentWidth := width - 2
	if contentWidth < 16 {
		contentWidth = 16
	}
	rendered := strings.TrimRight(renderEntryText("notice", block, contentWidth), "\n")
	lines := strings.Split(rendered, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, "  "+line)
	}
	return out
}

func renderStatusCard(m UIMessage, block string, width int) []string {
	contentWidth := width - 6
	if contentWidth < 16 {
		contentWidth = 16
	}
	title := lipgloss.NewStyle().
		Foreground(roleBorderColor(m)).
		Bold(true).
		Render("Reasoning only")
	body := lipgloss.NewStyle().
		Foreground(tuitheme.Default.Muted).
		Render(hardWrapRendered(renderEntryText(m.Role, block, contentWidth), contentWidth))
	rendered := joinTitleAndBody(title, body)
	card := spacedCardStyle(width, roleBorderColor(m)).
		Render(strings.TrimRight(rendered, "\n"))
	return strings.Split(strings.TrimRight(card, "\n"), "\n")
}

func renderUserPrompt(block string, width int) []string {
	contentWidth := width - 4
	if contentWidth < 16 {
		contentWidth = 16
	}
	rendered := strings.TrimRight(hardWrapRendered(renderEntryText("you", block, contentWidth), contentWidth), "\n")
	lines := strings.Split(rendered, "\n")
	glyph := tuitheme.UserPromptGlyphStyle().Render("›")
	rowStyle := tuitheme.UserPromptStyle().Width(width).MaxWidth(width)
	out := make([]string, 0, len(lines)+2)
	out = append(out, rowStyle.Render(""))
	for i, line := range lines {
		if i == 0 {
			out = append(out, rowStyle.Render(glyph+" "+line))
			continue
		}
		out = append(out, rowStyle.Render("  "+line))
	}
	out = append(out, rowStyle.Render(""))
	return out
}

func renderThinkingCard(m UIMessage, block string, width int) []string {
	contentWidth := width - 6
	if contentWidth < 16 {
		contentWidth = 16
	}
	title := lipgloss.NewStyle().
		Foreground(tuitheme.Default.Muted).
		Bold(true).
		Render("Thinking")
	body := lipgloss.NewStyle().
		Foreground(tuitheme.Default.Muted).
		Italic(true).
		Render(hardWrapRendered(renderEntryText("think", block, contentWidth), contentWidth))
	rendered := joinTitleAndBody(title, body)
	card := spacedCardStyle(width, roleBorderColor(m)).
		Render(rendered)
	return strings.Split(strings.TrimRight(card, "\n"), "\n")
}

func renderToolEvent(m UIMessage, block string, width int) []string {
	contentWidth := width - 6
	if contentWidth < 16 {
		contentWidth = 16
	}
	rawLines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	if len(rawLines) == 0 {
		return nil
	}
	header := strings.TrimSpace(rawLines[0])
	if header == "" {
		return nil
	}
	out := make([]string, 0, len(rawLines)+2)
	out = append(out, renderToolEventHeader(m, header, width)...)
	for _, raw := range rawLines[1:] {
		line := strings.TrimRight(raw, "\n")
		if strings.TrimSpace(line) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, renderToolEventChild(line, contentWidth)...)
	}
	return out
}

func renderToolEventHeader(m UIMessage, header string, width int) []string {
	contentWidth := width - 2
	if contentWidth < 16 {
		contentWidth = 16
	}
	bullet := toolEventBulletStyle(m).Render("•")
	verb, rest := splitEventHeader(header)
	verbStyle := lipgloss.NewStyle().Bold(true).Foreground(toolEventVerbColor(m))
	var rendered string
	if rest == "" {
		rendered = bullet + " " + verbStyle.Render(verb)
	} else {
		rendered = bullet + " " + verbStyle.Render(verb) + " " + RenderCommandLike(rest)
	}
	return wrapWithPrefixes(rendered, "", "  ", width)
}

func renderToolEventChild(line string, width int) []string {
	line = strings.TrimRight(line, "\r\n")
	if strings.TrimSpace(line) == "" {
		return nil
	}
	if hasLeadingCommandSpace(line) {
		return renderIndentedToolEventChild(line, width)
	}
	action, rest := splitStatusLinePreservingSpace(line)
	if normalizedStatusToken(action) == "" {
		return renderPlainToolEventChild(line, width)
	}
	actionStyle := toolEventStatusStyle(action)
	var rendered string
	if rest == "" {
		rendered = actionStyle.Render(action)
	} else {
		rendered = actionStyle.Render(action) + toolEventDetailStyle(action).Render(rest)
	}
	return wrapWithPrefixes(rendered, tuitheme.MutedStyle().Render("  └ "), "    ", width)
}

func renderIndentedToolEventChild(line string, width int) []string {
	return renderPlainToolEventChild(line, width)
}

func renderPlainToolEventChild(line string, width int) []string {
	prefix := tuitheme.MutedStyle().Render("  └ ")
	rendered := line
	if lipgloss.Width(prefix)+lipgloss.Width(rendered) <= width {
		return []string{prefix + rendered}
	}
	return wrapWithPrefixes(rendered, prefix, "    ", width)
}

func hasLeadingCommandSpace(text string) bool {
	if text == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(text)
	return r == ' ' || r == '\t'
}

func splitStatusLinePreservingSpace(text string) (string, string) {
	text = strings.TrimRight(text, "\r\n")
	if text == "" {
		return "", ""
	}
	for i, r := range text {
		if r == ' ' || r == '\t' {
			return text[:i], text[i:]
		}
	}
	return text, ""
}

func toolEventStatusStyle(token string) lipgloss.Style {
	style := lipgloss.NewStyle().Foreground(tuitheme.Default.InfoSoft)
	switch normalizedStatusToken(token) {
	case "success":
		return style.Foreground(tuitheme.Default.Success).Bold(true)
	case "denied":
		return style.Foreground(tuitheme.Default.ResultDenied).Bold(true)
	case "error":
		return style.Foreground(tuitheme.Default.Error).Bold(true)
	case "timeout":
		return style.Foreground(tuitheme.Default.ResultTimeout).Bold(true)
	case "warning":
		return style.Foreground(tuitheme.Default.Warn).Bold(true)
	case "canceled":
		return style.Foreground(tuitheme.Default.Muted).Bold(true)
	default:
		return style
	}
}

func toolEventDetailStyle(token string) lipgloss.Style {
	switch normalizedStatusToken(token) {
	case "canceled":
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)
	default:
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Text)
	}
}

func normalizedStatusToken(token string) string {
	switch strings.ToUpper(strings.TrimSpace(token)) {
	case "✓", "OK", "DONE", "SUCCESS":
		return "success"
	case "DENIED":
		return "denied"
	case "✗", "ERROR", "FAILED", "FAIL":
		return "error"
	case "TIMEOUT":
		return "timeout"
	case "WARN", "WARNING":
		return "warning"
	case "CANCELED", "CANCELLED":
		return "canceled"
	default:
		return ""
	}
}

func splitEventHeader(text string) (string, string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return "", ""
	}
	verb := parts[0]
	rest := strings.TrimSpace(strings.TrimPrefix(text, verb))
	return verb, rest
}

func RenderCommandLike(text string) string {
	if text == "" {
		return ""
	}
	var out strings.Builder
	tokenIndex := 0
	for _, part := range splitCommandPreservingSpace(text) {
		if part == "" {
			continue
		}
		if isCommandSpace(part) {
			out.WriteString(part)
			continue
		}
		out.WriteString(styleCommandToken(part, tokenIndex))
		tokenIndex++
	}
	return out.String()
}

func splitCommandPreservingSpace(text string) []string {
	parts := make([]string, 0, 8)
	start := 0
	inQuote := rune(0)
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if inQuote != 0 {
			if r == inQuote {
				inQuote = 0
			}
			i += size
			continue
		}
		switch r {
		case '\'', '"':
			inQuote = r
			i += size
		case ' ', '\t', '\n', '\r':
			if start < i {
				parts = append(parts, text[start:i])
			}
			spaceStart := i
			spaceEnd := i + size
			for spaceEnd < len(text) {
				next, nextSize := utf8.DecodeRuneInString(text[spaceEnd:])
				if next != ' ' && next != '\t' && next != '\n' && next != '\r' {
					break
				}
				spaceEnd += nextSize
			}
			parts = append(parts, text[spaceStart:spaceEnd])
			start = spaceEnd
			i = spaceEnd
		default:
			i += size
		}
	}
	if start < len(text) {
		parts = append(parts, text[start:])
	}
	return parts
}

func isCommandSpace(text string) bool {
	for _, r := range text {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return text != ""
}

func styleCommandToken(token string, index int) string {
	style := lipgloss.NewStyle().Foreground(tuitheme.Default.Text)
	switch {
	case isShellOperator(token):
		style = lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)
	case strings.HasPrefix(token, "-"):
		style = lipgloss.NewStyle().Foreground(tuitheme.Default.Warn)
	case strings.HasPrefix(token, "\"") || strings.HasPrefix(token, "'"):
		style = lipgloss.NewStyle().Foreground(tuitheme.Default.Success)
	case index == 0:
		style = lipgloss.NewStyle().Foreground(tuitheme.Default.InfoSoft)
	}
	return style.Render(token)
}

func isShellOperator(token string) bool {
	switch token {
	case "&&", "||", "|", ";", ">", ">>", "<", "2>", "2>>":
		return true
	default:
		return false
	}
}

func toolEventBulletStyle(m UIMessage) lipgloss.Style {
	style := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted).Bold(true)
	switch m.Role {
	case "result_ok", "shell_result_ok":
		return style.Foreground(tuitheme.Default.Success)
	case "result_denied", "shell_result_denied":
		return style.Foreground(tuitheme.Default.ResultDenied)
	case "result_failed", "shell_result_failed", "result_error", "shell_result_error", "error":
		return style.Foreground(tuitheme.Default.Error)
	case "result_timeout", "shell_result_timeout":
		return style.Foreground(tuitheme.Default.ResultTimeout)
	case "result_canceled", "shell_result_canceled":
		return style.Foreground(tuitheme.Default.Muted)
	case "result_running", "shell_result_running", "tool":
		return style.Foreground(tuitheme.Default.Tool)
	default:
		return style
	}
}

func toolEventVerbColor(m UIMessage) lipgloss.Color {
	switch m.Role {
	case "result_ok", "shell_result_ok":
		return tuitheme.Default.Success
	case "result_denied", "shell_result_denied":
		return tuitheme.Default.ResultDenied
	case "result_failed", "shell_result_failed", "result_error", "shell_result_error", "error":
		return tuitheme.Default.Error
	case "result_timeout", "shell_result_timeout":
		return tuitheme.Default.ResultTimeout
	case "result_canceled", "shell_result_canceled":
		return tuitheme.Default.Muted
	case "result_running", "shell_result_running", "tool":
		return tuitheme.Default.Tool
	default:
		return tuitheme.Default.Text
	}
}

func WorkSeparator(width int) string {
	if width < 1 {
		width = 1
	}
	return tuitheme.MutedStyle().Render(strings.Repeat("─", width))
}

func roleBorderColor(m UIMessage) lipgloss.Color {
	return tuitheme.RoleBorder(m.Role)
}

func toolNamePrefix(text string) string {
	idx := strings.Index(text, ":")
	if idx <= 0 {
		return ""
	}
	name := strings.TrimSpace(text[:idx])
	name = strings.TrimPrefix(name, "[")
	name = strings.TrimSuffix(name, "]")
	return name
}
