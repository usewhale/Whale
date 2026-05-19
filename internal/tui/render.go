package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/usewhale/whale/internal/app/service"
	"github.com/usewhale/whale/internal/build"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

func (m model) renderBody(mainWidth, bodyHeight int) string {
	if bodyHeight <= 0 {
		return ""
	}
	m.ensureViewportContentForSize(mainWidth, bodyHeight)
	if m.page != pageChat {
		return lipgloss.NewStyle().
			Width(mainWidth).
			Height(bodyHeight).
			Border(lipgloss.NormalBorder()).
			BorderForeground(tuitheme.Default.Border).
			Render(m.viewport.View())
	}
	return lipgloss.NewStyle().Width(mainWidth).Render(m.chat.View())
}

func (m model) View() string {
	mainWidth, _ := m.layoutDims()
	bottom := m.renderBottom(mainWidth)
	bodyHeight := m.height - countVisibleLines(bottom)
	if m.height <= 0 {
		bodyHeight = 0
	}
	bodyHeight = max(0, bodyHeight)
	body := m.renderBody(mainWidth, bodyHeight)
	body = padVisibleLines(body, bodyHeight, mainWidth)
	if body == "" {
		return bottom
	}
	return body + "\n" + bottom
}

func (m model) viewportBodyHeight(mainWidth int) int {
	if m.height <= 0 {
		return 0
	}
	return max(0, m.height-countVisibleLines(m.renderBottom(mainWidth)))
}

func (m model) renderBottom(mainWidth int) string {
	footerText := "model: " + m.model + "  effort: " + m.effort + "  thinking: " + m.thinking
	if m.chatMode == "ask" || m.chatMode == "plan" {
		footerText += "  mode: " + m.chatMode + " (Shift+Tab to switch)"
	}
	viewIndicator := ""
	if m.focusEnabled() {
		viewIndicator = "focus"
	}
	dirReserve := 0
	if m.cwd != "" {
		dirReserve = footerDirReserve(m.cwd)
	}
	viewReserve := footerViewIndicatorReserve(viewIndicator)
	footerText = appendFooterHint(footerText, mainWidth, dirReserve+viewReserve)
	if m.cwd != "" {
		footerText = appendFooterDir(footerText, m.cwd, mainWidth, viewReserve)
	}
	if viewIndicator != "" {
		footerText = appendFooterViewIndicator(footerText, viewIndicator, mainWidth)
	}
	footer := lipgloss.NewStyle().Width(mainWidth).MaxWidth(mainWidth).Render(lipgloss.JoinHorizontal(lipgloss.Left, footerText))
	bottomParts := m.bottomPartsBeforeInput(mainWidth)
	bottomParts = append(bottomParts, m.input.View(), footer)
	return strings.Join(bottomParts, "\n")
}

func (m model) bottomPartsBeforeInput(mainWidth int) []string {
	bottomParts := make([]string, 0, 8)
	if statusLine := m.renderBusyStatusLine(mainWidth); statusLine != "" {
		bottomParts = append(bottomParts, statusLine)
	}
	if m.mode == modeChat && m.hasSlashSuggestions() {
		bottomParts = append(bottomParts, m.renderSlashSuggestions())
	}
	if m.mode == modeChat && !m.hasSlashSuggestions() && m.hasSkillSuggestions() {
		bottomParts = append(bottomParts, m.renderSkillSuggestions())
	}
	if m.mode == modeApproval {
		if len(bottomParts) > 0 {
			bottomParts = append(bottomParts, "")
		}
		bottomParts = append(bottomParts, m.renderApprovalPrompt())
	}
	if m.mode == modePlanImplementation {
		bottomParts = append(bottomParts, m.renderPlanImplementationPicker())
	}
	if m.mode == modeSkillsMenu {
		bottomParts = append(bottomParts, m.renderSkillsMenu())
	}
	if m.mode == modeSkillsManager {
		bottomParts = append(bottomParts, m.renderSkillsManager())
	}
	if m.mode == modeSessionPicker {
		rows := []string{"sessions (↑/↓ select, enter confirm, esc cancel):"}
		for i, row := range m.sessionChoices {
			if isSessionHeaderRow(row) {
				rows = append(rows, row)
				continue
			}
			prefix := "  "
			if i == m.sessionIndex {
				prefix = "> "
			}
			rows = append(rows, prefix+displaySessionChoiceRow(row))
		}
		bottomParts = append(bottomParts, lipgloss.NewStyle().Foreground(tuitheme.Default.Plan).Render(strings.Join(rows, "\n")))
	}
	if m.mode == modeUserInput {
		if m.userInput.index < len(m.userInput.questions) {
			q := m.userInput.questions[m.userInput.index]
			rows := make([]string, 0, len(q.Options)+3)
			rows = append(rows, q.Question)
			rows = append(rows, "")
			for i, opt := range q.Options {
				prefix := "  "
				if i == m.userInput.selectedOption {
					prefix = "> "
				}
				rows = append(rows, fmt.Sprintf("%s%s - %s", prefix, opt.Label, opt.Description))
			}
			rows = append(rows, "", "(up/down choose, enter confirm, esc cancel)")
			bottomParts = append(bottomParts, lipgloss.NewStyle().Foreground(tuitheme.Default.Info).Render(strings.Join(rows, "\n")))
		}
	}
	if m.mode == modeModelPicker {
		bottomParts = append(bottomParts, m.renderModelPicker())
	}
	if m.mode == modePermissionsPicker {
		bottomParts = append(bottomParts, m.renderPermissionsPicker())
	}
	if m.mode == modePermissionsProjectTrustConfirm {
		bottomParts = append(bottomParts, m.renderPermissionsProjectTrustConfirm())
	}
	if m.mode == modePermissionsProjectClearConfirm {
		bottomParts = append(bottomParts, m.renderPermissionsProjectClearConfirm())
	}
	if queued := m.renderQueuedPrompts(mainWidth); queued != "" {
		bottomParts = append(bottomParts, queued)
	}
	return bottomParts
}

func (m model) renderApprovalPrompt() string {
	title := lipgloss.NewStyle().Foreground(tuitheme.Default.Palette).Bold(true)
	tool := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)
	body := lipgloss.NewStyle()
	hint := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)

	review := isFileDiffApproval(m.approval.toolName, m.approval.metadata)
	memory := memoryApprovalKind(m.approval.metadata)
	titleText := "Approval required"
	if review {
		titleText = "Approval required: file diff review"
	} else if memory != "" {
		titleText = "Approval required: " + memory
	}
	bodyParts := []string{}
	if review {
		bodyParts = append(bodyParts, "Review file changes before Whale applies them.")
	} else if memory == "memory write" {
		bodyParts = append(bodyParts, "Review memory before Whale saves it.")
	} else if memory == "memory delete" {
		bodyParts = append(bodyParts, "Review memory before Whale deletes it.")
	}
	if memory != "" {
		if memoryPreview := renderApprovalMemoryMetadata(m.approval.metadata); memoryPreview != "" {
			bodyParts = append(bodyParts, memoryPreview)
		}
	}
	if detail := approvalDisplayBody(m.approval.toolName, m.approval.reason); detail != "" {
		bodyParts = append(bodyParts, detail)
	}
	if scope := approvalSessionScope(m.approval.metadata); scope != "" {
		bodyParts = append(bodyParts, "Allow for session = "+scope)
	}
	approvalBody := body.Render(indentApprovalBody(strings.Join(bodyParts, "\n")))
	if diff := renderApprovalDiffMetadata(m.approval.metadata, 80); diff != "" {
		if isReadableApprovalDiff(diff) {
			if approvalBody != "" {
				approvalBody += "\n\n" + diff
			} else {
				approvalBody = diff
			}
		} else if approvalBody != "" {
			approvalBody += "\n\n" + diff
		} else {
			approvalBody = diff
		}
	}

	opts := []string{
		renderApprovalOption("Allow once", "a", m.approval.selected == 0, false),
		renderApprovalOption("Allow for session", "s", m.approval.selected == 1, false),
		renderApprovalOption("Deny", "d", m.approval.selected == 2, true),
	}

	return lipgloss.JoinVertical(
		lipgloss.Left,
		title.Render(titleText)+"  "+tool.Render(approvalToolDisplayName(m.approval.toolName)),
		"",
		approvalBody,
		"",
		"  "+strings.Join(opts, "   "),
		"",
		hint.Render("Enter confirm · Esc deny · ←/→/tab switch"),
	)
}

func isFileDiffApproval(toolName string, metadata map[string]any) bool {
	if strings.TrimSpace(asString(metadata["approval_kind"])) == "file_diff_review" {
		return true
	}
	switch toolName {
	case "edit", "write", "apply_patch":
		return true
	default:
		return false
	}
}

func memoryApprovalKind(metadata map[string]any) string {
	switch strings.TrimSpace(asString(metadata["approval_kind"])) {
	case "memory_write":
		return "memory write"
	case "memory_delete":
		return "memory delete"
	default:
		return ""
	}
}

func approvalSessionScope(metadata map[string]any) string {
	return strings.TrimSpace(asString(metadata["approval_session_scope"]))
}

func approvalToolDisplayName(toolName string) string {
	switch toolName {
	case "shell_run":
		return "shell command"
	default:
		return toolName
	}
}

func approvalDisplayBody(toolName, summary string) string {
	if detail, ok := strings.CutPrefix(summary, toolName+":"); ok {
		detail = strings.TrimSpace(detail)
		if detail != "" {
			return detail
		}
	}
	return strings.TrimSpace(summary)
}

func renderApprovalMemoryMetadata(metadata map[string]any) string {
	kind := strings.TrimSpace(asString(metadata["approval_kind"]))
	scope := strings.TrimSpace(asString(metadata["memory_scope"]))
	typ := strings.TrimSpace(asString(metadata["memory_type"]))
	name := strings.TrimSpace(asString(metadata["memory_name"]))
	description := strings.TrimSpace(asString(metadata["memory_description"]))
	content := strings.TrimSpace(asString(metadata["memory_content_preview"]))
	status := strings.TrimSpace(asString(metadata["memory_write_status"]))

	var lines []string
	switch kind {
	case "memory_write":
		label := "Save memory"
		if status == "created" {
			label = "Created memory"
		} else if status == "updated" {
			label = "Updated memory"
		}
		lines = append(lines, label+memoryScopeTypeSuffix(scope, typ))
	case "memory_delete":
		lines = append(lines, "Delete memory"+memoryScopeTypeSuffix(scope, typ))
	default:
		return ""
	}
	if name != "" {
		lines = append(lines, "Name: "+name)
	}
	if description != "" {
		lines = append(lines, "Description: "+description)
	}
	if content != "" {
		lines = append(lines, "", "Content:", content)
	}
	return strings.Join(lines, "\n")
}

func memoryScopeTypeSuffix(scope, typ string) string {
	if scope == "" && typ == "" {
		return ""
	}
	if scope == "" {
		return ": " + typ
	}
	if typ == "" {
		return ": " + scope
	}
	return ": " + scope + "/" + typ
}

func indentApprovalBody(body string) string {
	if body == "" {
		return ""
	}
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
}

func renderApprovalOption(label, shortcut string, selected, destructive bool) string {
	prefix := mutedSelectionPrefix(selected)
	key := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted).Render("(" + shortcut + ")")
	style := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)
	if selected {
		color := tuitheme.Default.InfoSoft
		if destructive {
			color = tuitheme.Default.ResultDenied
		}
		style = lipgloss.NewStyle().Foreground(color).Bold(true)
	}
	return prefix + style.Render(label) + " " + key
}

func mutedSelectionPrefix(selected bool) string {
	if selected {
		return lipgloss.NewStyle().Foreground(tuitheme.Default.InfoSoft).Bold(true).Render("› ")
	}
	return lipgloss.NewStyle().Foreground(tuitheme.Default.Muted).Render("  ")
}

func countVisibleLines(s string) int {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func padVisibleLines(s string, targetLines, width int) string {
	if targetLines <= 0 {
		return ""
	}
	s = strings.TrimRight(s, "\n")
	lines := []string{}
	if s != "" {
		lines = strings.Split(s, "\n")
	}
	if len(lines) > targetLines {
		lines = lines[len(lines)-targetLines:]
	}
	for len(lines) < targetLines {
		lines = append(lines, "")
	}
	style := lipgloss.NewStyle().Width(width).MaxWidth(width)
	for i, line := range lines {
		lines[i] = style.Render(line)
	}
	return strings.Join(lines, "\n")
}

func appendFooterDir(base, cwd string, width, reserve int) string {
	segment := "  "
	available := width - lipgloss.Width(base) - lipgloss.Width(segment) - reserve
	if available <= 0 {
		return base
	}
	return base + segment + fitTail(cwd, available)
}

func appendFooterViewIndicator(base, indicator string, width int) string {
	indicator = strings.TrimSpace(indicator)
	if indicator == "" {
		return base
	}
	segment := "  " + indicator
	if lipgloss.Width(base)+lipgloss.Width(segment) > width {
		return base
	}
	return base + segment
}

func appendFooterHint(base string, width, reserve int) string {
	for _, hint := range []string{"PgUp/PgDn scroll"} {
		segment := "  " + hint
		if lipgloss.Width(base)+lipgloss.Width(segment)+reserve <= width {
			return base + segment
		}
	}
	return base
}

func footerDirReserve(cwd string) int {
	trimmed := strings.TrimRight(cwd, "/")
	if trimmed == "" {
		trimmed = cwd
	}
	tail := trimmed
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 && idx < len(trimmed)-1 {
		tail = trimmed[idx+1:]
	}
	if tail == "" {
		return 0
	}
	return lipgloss.Width("  ") + lipgloss.Width(tail)
}

func footerViewIndicatorReserve(indicator string) int {
	indicator = strings.TrimSpace(indicator)
	if indicator == "" {
		return 0
	}
	return lipgloss.Width("  ") + lipgloss.Width(indicator)
}

func fitTail(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	runes := []rune(s)
	tail := ""
	for i := len(runes) - 1; i >= 0; i-- {
		next := string(runes[i:])
		if lipgloss.Width("..."+next) > width {
			break
		}
		tail = next
	}
	return "..." + tail
}

func (m model) renderBusyStatusLine(width int) string {
	if !m.busy {
		return ""
	}
	label := "Working"
	if m.stopping {
		label = "Stopping"
	} else if status := strings.TrimSpace(m.providerRetryStatus); status != "" && time.Now().Before(m.providerRetryUntil) {
		label = status
	}
	line := fmt.Sprintf("%s (%s)", label, formatElapsedCompact(m.busyElapsed()))
	if !m.stopping {
		if m.mode == modeChat {
			line += " · Esc/Ctrl+C to interrupt"
		} else {
			line += " · Ctrl+C to interrupt"
		}
	}
	return lipgloss.NewStyle().
		Width(width).
		Foreground(tuitheme.Default.Warn).
		Render(line)
}

func (m model) renderQueuedPrompts(width int) string {
	if len(m.queuedPrompts) == 0 || width <= 0 {
		return ""
	}
	limit := 3
	if len(m.queuedPrompts) < limit {
		limit = len(m.queuedPrompts)
	}
	rows := make([]string, 0, limit+2)
	rows = append(rows, lipgloss.NewStyle().
		Foreground(tuitheme.Default.Warn).
		Render(fmt.Sprintf("queued (%d)", len(m.queuedPrompts))))
	for i := 0; i < limit; i++ {
		preview := queuedPromptPreview(m.queuedPrompts[i].Text, max(1, width-4))
		rows = append(rows, lipgloss.NewStyle().
			Foreground(tuitheme.Default.Muted).
			Render("  "+preview))
	}
	if hidden := len(m.queuedPrompts) - limit; hidden > 0 {
		rows = append(rows, lipgloss.NewStyle().
			Foreground(tuitheme.Default.Muted).
			Render(fmt.Sprintf("  ... %d more", hidden)))
	}
	return lipgloss.NewStyle().Width(width).MaxWidth(width).Render(strings.Join(rows, "\n"))
}

func queuedPromptPreview(text string, width int) string {
	text = strings.Join(strings.Fields(text), " ")
	if width <= 0 || text == "" {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	runes := []rune(text)
	out := ""
	for i := range runes {
		next := string(runes[:i+1])
		if lipgloss.Width(next+"...") > width {
			break
		}
		out = next
	}
	if out == "" {
		return "..."
	}
	return out + "..."
}

func (m model) busyElapsed() time.Duration {
	if m.busySince.IsZero() {
		return 0
	}
	return time.Since(m.busySince)
}

func formatElapsedCompact(elapsed time.Duration) string {
	seconds := int(elapsed / time.Second)
	if seconds < 0 {
		seconds = 0
	}
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		minutes := seconds / 60
		remSeconds := seconds % 60
		return fmt.Sprintf("%dm %02ds", minutes, remSeconds)
	}
	hours := seconds / 3600
	minutes := (seconds % 3600) / 60
	remSeconds := seconds % 60
	return fmt.Sprintf("%dh %02dm %02ds", hours, minutes, remSeconds)
}

func resolveVersion() string {
	return build.CurrentVersion()
}

func buildHeaderBanner(modelName, effort, thinking, cwd, version string) string {
	return fmt.Sprintf("▸ Whale %s   model: %s   effort: %s   thinking: %s   dir: %s",
		version, modelName, effort, thinking, cwd)
}

func resolveWorkingDirectory() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	home, hErr := os.UserHomeDir()
	if hErr == nil {
		if rel, rErr := filepath.Rel(home, wd); rErr == nil && rel != "" && rel != "." && !strings.HasPrefix(rel, "..") {
			return "~/" + rel
		}
		if filepath.Clean(wd) == filepath.Clean(home) {
			return "~"
		}
	}
	return wd
}

func (m model) pageLabel() string {
	if m.page == pageLogs {
		return "logs"
	}
	if m.page == pageDiff {
		return "diff"
	}
	return "chat"
}

func (m model) renderPalette() string {
	rows := []string{"Command Palette (enter to run, esc to close)"}
	for i, it := range m.palette.actions {
		prefix := "  "
		if i == m.palette.selected {
			prefix = "> "
		}
		rows = append(rows, prefix+it.Label)
	}
	return lipgloss.NewStyle().Foreground(tuitheme.Default.Palette).Render(strings.Join(rows, "\n"))
}

func (m model) renderModelPicker() string {
	rows := []string{"Select Model and Effort"}
	rows = append(rows, "")
	rows = append(rows, "Model:")
	for i, item := range m.modelPicker.models {
		prefix := "  "
		if m.modelPicker.stage == 0 && i == m.modelPicker.modelIx {
			prefix = "> "
		}
		rows = append(rows, prefix+item)
	}
	if m.modelPicker.stage >= 1 {
		rows = append(rows, "")
		rows = append(rows, "Effort:")
		for i, item := range m.modelPicker.efforts {
			prefix := "  "
			if m.modelPicker.stage == 1 && i == m.modelPicker.effIx {
				prefix = "> "
			}
			rows = append(rows, prefix+item)
		}
	}
	if m.modelPicker.stage >= 2 {
		rows = append(rows, "", "Thinking:")
		for i, item := range m.modelPicker.thinkings {
			prefix := "  "
			if m.modelPicker.stage == 2 && i == m.modelPicker.thinkIx {
				prefix = "> "
			}
			rows = append(rows, prefix+item)
		}
	}
	rows = append(rows, "", "(up/down choose, enter next/confirm, esc back)")
	return lipgloss.NewStyle().Foreground(tuitheme.Default.Info).Render(strings.Join(rows, "\n"))
}

func (m model) renderPermissionsPicker() string {
	rows := []string{"Permissions", ""}
	descriptions := map[string]string{
		service.ApprovalChoiceAskFirst:           "Prompt before write, patch, shell, or MCP tools run.",
		service.ApprovalChoiceAutoApproveSession: "No approval prompts until Whale exits.",
		service.ApprovalChoiceTrustProject:       "Auto-approve by default in this workspace.",
		service.ApprovalChoiceClearProject:       "Remove permissions.mode from ./.whale/config.toml.",
	}
	projectSectionRendered := false
	for i, item := range m.permissionsPicker.choices {
		if !projectSectionRendered && isProjectPermissionChoice(item) {
			rows = append(rows, "", "Project default")
			projectSectionRendered = true
		}
		if i == 0 {
			rows = append(rows, "Session")
		}
		prefix := "  "
		if i == m.permissionsPicker.index {
			prefix = "> "
		}
		if desc := descriptions[item]; desc != "" {
			rows = append(rows, fmt.Sprintf("%s%s - %s", prefix, item, desc))
		} else {
			rows = append(rows, prefix+item)
		}
	}
	rows = append(rows, "", "(up/down choose, enter confirm, esc cancel)")
	return lipgloss.NewStyle().Foreground(tuitheme.Default.Info).Render(strings.Join(rows, "\n"))
}

func isProjectPermissionChoice(item string) bool {
	return item == service.ApprovalChoiceTrustProject || item == service.ApprovalChoiceClearProject
}

func (m model) renderPermissionsProjectTrustConfirm() string {
	return m.renderPermissionsProjectConfirm(
		"Trust this project?",
		[]string{
			"Auto-approve write, patch, shell, and MCP tools by default in this workspace.",
			"This affects future sessions in this workspace.",
			"Config: ./.whale/config.toml",
		},
		"Trust this project",
	)
}

func (m model) renderPermissionsProjectClearConfirm() string {
	return m.renderPermissionsProjectConfirm(
		"Clear project default?",
		[]string{
			"Remove permissions.mode from ./.whale/config.toml.",
			"Future sessions will use the global/default approval setting.",
		},
		"Clear project default",
	)
}

func (m model) renderPermissionsProjectConfirm(title string, bodyLines []string, confirmLabel string) string {
	rows := []string{title, ""}
	rows = append(rows, bodyLines...)
	rows = append(rows, "")
	choices := []string{confirmLabel, "Cancel"}
	for i, item := range choices {
		prefix := "  "
		if i == m.permissionsProjectConfirm.index {
			prefix = "> "
		}
		rows = append(rows, prefix+item)
	}
	rows = append(rows, "", "(up/down choose, enter confirm, esc back)")
	return lipgloss.NewStyle().Foreground(tuitheme.Default.Info).Render(strings.Join(rows, "\n"))
}

func (m model) renderPlanImplementationPicker() string {
	rows := []string{"Implement this plan?", ""}
	items := []struct {
		label string
	}{
		{"Yes, implement this plan"},
		{"No, stay in Plan mode"},
	}
	for i, item := range items {
		prefix := "  "
		if i == m.planImplementation.index {
			prefix = "> "
		}
		rows = append(rows, prefix+item.label)
	}
	rows = append(rows, "", "(up/down choose, enter confirm, esc cancel)")
	return lipgloss.NewStyle().Foreground(tuitheme.Default.Info).Render(strings.Join(rows, "\n"))
}

func (m model) layoutDims() (mainWidth, bodyHeight int) {
	bodyHeight = max(3, m.height-6)
	mainWidth = m.width
	if m.sidebar && m.width > 80 {
		mainWidth = int(float64(m.width) * 0.72)
	}
	return mainWidth, bodyHeight
}

func (m model) chatRenderWidth() int {
	mainWidth, _ := m.layoutDims()
	return max(20, max(10, mainWidth-2))
}
