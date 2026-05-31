package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/usewhale/whale/internal/runtime/protocol"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

func (m model) renderWorkflowPanel() string {
	result := m.workflowPanel.result
	info := lipgloss.NewStyle().Foreground(tuitheme.Default.Info)
	muted := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)
	errorStyle := lipgloss.NewStyle().Foreground(tuitheme.Default.Error)
	lines := []string{workflowPanelTitleStyle().Render("Dynamic workflows")}
	if result == nil {
		lines = append(lines, "", muted.Render("Loading workflow state..."), "", muted.Render("Esc back"))
		return strings.Join(lines, "\n")
	}
	switch result.Kind {
	case "workflows":
		lines = append(lines, renderWorkflowPanelRuns(result, m.workflowPanel.selected)...)
	case "workflow", "workflow-run":
		if result.WorkflowPanelSnapshot != nil {
			if m.workflowPanel.detail {
				lines = append(lines, m.renderWorkflowPanelTaskDetail(result.WorkflowPanelSnapshot)...)
			} else {
				lines = append(lines, m.renderWorkflowPanelSnapshot(result.WorkflowPanelSnapshot)...)
			}
		} else {
			lines = append(lines, renderWorkflowPanelRun(result)...)
		}
	default:
		lines = append(lines, "", result.PlainText)
	}
	if err := localResultFieldValue(result.Fields, "Error"); err != "" {
		lines = append(lines, "", errorStyle.Render(err))
	}
	hint := "Esc back"
	if result.Kind == "workflows" {
		if len(workflowPanelRunSections(result)) > 0 {
			hint = "↑↓ select · Enter details · x stop · Esc back"
		}
	} else {
		if result.WorkflowPanelSnapshot != nil {
			if m.workflowPanel.detail {
				hint = "↑↓ select · Tab/→ details · Enter expand · PgUp/PgDown scroll · ← run · Esc back"
			} else {
				hint = "↑↓ select · Tab/→ switch · Enter details · ← list/focus · x stop workflow · Esc back"
			}
		} else {
			hint = "x stop workflow · ← list · Esc back"
		}
	}
	lines = append(lines, "", muted.Render(hint), info.Render("Live refreshes every second."))
	return strings.Join(lines, "\n")
}

func workflowPanelSnapshot(result *protocol.LocalResult) *protocol.WorkflowPanelSnapshot {
	if result == nil {
		return nil
	}
	return result.WorkflowPanelSnapshot
}

func workflowPanelHasSnapshot(result *protocol.LocalResult) bool {
	snapshot := workflowPanelSnapshot(result)
	return snapshot != nil && len(snapshot.Phases) > 0
}

func (m model) renderWorkflowPanelSnapshot(snapshot *protocol.WorkflowPanelSnapshot) []string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	contentWidth := max(40, width-2)
	leftWidth := clampInt(contentWidth/4, 22, 34)
	rightWidth := max(30, contentWidth-leftWidth-1)
	lines := []string{"", workflowPanelRunHeader(snapshot, contentWidth)}
	if snapshot.Error != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(tuitheme.Default.Error).Render("Error · "+snapshot.Error))
	} else if snapshot.Summary != "" {
		lines = append(lines, workflowPanelSectionLabel("Summary")+" "+tuitheme.MutedStyle().Render("·")+" "+workflowPanelTruncate(snapshot.Summary, contentWidth-10))
	}
	lines = append(lines, "", workflowPanelTwoColumnBox(
		workflowPanelPhaseLines(snapshot, m.workflowPanel.selectedPhase, m.workflowPanel.focus == workflowPanelFocusPhase, leftWidth),
		workflowPanelTaskLines(snapshot, m.workflowPanel.selectedPhase, m.workflowPanel.selectedTask, m.workflowPanel.focus == workflowPanelFocusTask, rightWidth),
		leftWidth,
		rightWidth,
	))
	return lines
}

func (m model) renderWorkflowPanelTaskDetail(snapshot *protocol.WorkflowPanelSnapshot) []string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	contentWidth := max(40, width-2)
	leftWidth := clampInt(contentWidth/4, 24, 36)
	rightWidth := max(32, contentWidth-leftWidth-1)
	phase, task, ok := workflowPanelSelectedTask(snapshot, m.workflowPanel.selectedPhase, m.workflowPanel.selectedTask)
	lines := []string{"", workflowPanelRunHeader(snapshot, contentWidth)}
	if !ok {
		lines = append(lines, "", workflowPanelTwoColumnBox(
			[]string{workflowPanelTruncate("Tasks", leftWidth), "Not started yet"},
			[]string{workflowPanelTruncate("Details", rightWidth), "Not available"},
			leftWidth,
			rightWidth,
		))
		return lines
	}
	if m.workflowPanel.detailExpanded && (m.workflowPanel.expandedSection == workflowPanelDetailPrompt || m.workflowPanel.expandedSection == workflowPanelDetailOutcome) {
		lines = append(lines, "", m.workflowPanelExpandedDetailBox(task, contentWidth))
		return lines
	}
	lines = append(lines, "", workflowPanelTwoColumnBox(
		workflowPanelDetailTaskListLines(phase, m.workflowPanel.selectedTask, !m.workflowPanel.detailRight, leftWidth),
		m.workflowPanelDetailLines(task, m.workflowPanel.detailRight, rightWidth),
		leftWidth,
		rightWidth,
	))
	return lines
}

func workflowPanelSelectedTask(snapshot *protocol.WorkflowPanelSnapshot, phaseIndex, taskIndex int) (protocol.WorkflowPanelPhase, protocol.WorkflowPanelTask, bool) {
	if snapshot == nil || phaseIndex < 0 || phaseIndex >= len(snapshot.Phases) {
		return protocol.WorkflowPanelPhase{}, protocol.WorkflowPanelTask{}, false
	}
	phase := snapshot.Phases[phaseIndex]
	if taskIndex < 0 || taskIndex >= len(phase.Tasks) {
		return phase, protocol.WorkflowPanelTask{}, false
	}
	return phase, phase.Tasks[taskIndex], true
}

func workflowPanelDetailTaskListLines(phase protocol.WorkflowPanelPhase, selected int, focused bool, width int) []string {
	title := phase.Name
	if title == "" {
		title = "Tasks"
	}
	if phase.Total > 0 {
		title = fmt.Sprintf("%s · %d agents", title, phase.Total)
	}
	lines := []string{workflowPanelTruncate(workflowPanelSectionLabel(title), width)}
	for i, task := range phase.Tasks {
		pointer := "  "
		if i == selected && focused {
			pointer = workflowPanelAction("❯") + " "
		}
		label := workflowPanelOneLine(task.Label)
		if label == "" {
			label = workflowPanelOneLine(task.ID)
		}
		parts := []string{pointer + workflowPanelStatusIconStyled(task.Status) + " " + workflowPanelMaybeSelected(label, i == selected && focused)}
		if tokens := workflowPanelTaskOutputTokens(task); tokens != "" {
			parts = append(parts, workflowPanelMeta(tokens))
		}
		if duration := workflowPanelDuration(task.DurationMS); duration != "" {
			parts = append(parts, workflowPanelMeta(duration))
		}
		lines = append(lines, workflowPanelTruncate(workflowPanelJoinMeta(parts...), width))
	}
	return lines
}

func (m model) workflowPanelDetailLines(task protocol.WorkflowPanelTask, focused bool, width int) []string {
	title := strings.TrimSpace(task.Label)
	if title == "" {
		title = task.ID
	}
	lines := []string{workflowPanelTruncate(workflowPanelSectionLabel(title), width)}
	status := workflowPanelStatusDisplay(task.Status)
	if task.Model != "" {
		status += workflowPanelMutedSep() + workflowPanelMeta(task.Model)
	}
	if task.ActorKind != "" {
		status += workflowPanelMutedSep() + workflowPanelMeta(task.ActorKind)
	}
	lines = append(lines, workflowPanelTruncate(status, width))
	lines = append(lines, workflowPanelTaskDetailMetaLines(task, width)...)
	lines = append(lines, "")
	lines = append(lines, m.workflowPanelDetailBlock(workflowPanelDetailPrompt, "Prompt", task.Prompt, focused, width)...)
	lines = append(lines, "")
	lines = append(lines, workflowPanelActivityBlock(task, m.workflowPanel.detailSection == workflowPanelDetailActivity && focused, width)...)
	lines = append(lines, "")
	outcome := task.Error
	if outcome == "" {
		outcome = task.Outcome
	}
	if outcome == "" {
		outcome = task.Message
	}
	lines = append(lines, m.workflowPanelDetailBlock(workflowPanelDetailOutcome, "Outcome", outcome, focused, width)...)
	return lines
}

func workflowPanelStatusDisplay(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		status = "queued"
	}
	return workflowPanelStatusIconStyled(status) + " " + workflowPanelStatusStyle(status).Render(strings.Title(status))
}

func workflowPanelTaskDetailMetaLines(task protocol.WorkflowPanelTask, width int) []string {
	primary := []string{}
	if tokens := workflowPanelTaskOutputTokens(task); tokens != "" {
		primary = append(primary, tokens)
	}
	if task.ToolCalls > 0 {
		primary = append(primary, fmt.Sprintf("%d %s", task.ToolCalls, workflowPanelPlural(task.ToolCalls, "tool call", "tool calls")))
	}
	if duration := workflowPanelDuration(task.DurationMS); duration != "" {
		primary = append(primary, duration)
	}
	lines := []string{}
	if len(primary) > 0 {
		lines = append(lines, workflowPanelTruncate(workflowPanelMeta(strings.Join(primary, " · ")), width))
	}
	usage := []string{}
	if task.TotalTokens > 0 {
		usage = append(usage, workflowPanelTokenCount(task.TotalTokens)+" total")
	}
	if task.PromptTokens > 0 {
		usage = append(usage, workflowPanelTokenCount(task.PromptTokens)+" in")
	}
	if task.PromptCacheHit > 0 {
		usage = append(usage, workflowPanelTokenCount(task.PromptCacheHit)+" cached")
	}
	if task.ReasoningReplay > 0 {
		usage = append(usage, workflowPanelTokenCount(task.ReasoningReplay)+" reasoning replay")
	}
	if task.ToolReplayTokens > 0 {
		usage = append(usage, workflowPanelTokenCount(task.ToolReplayTokens)+" tool replay")
	}
	if task.ToolTokensSaved > 0 {
		usage = append(usage, workflowPanelTokenCount(task.ToolTokensSaved)+" saved")
	}
	if len(usage) > 0 {
		lines = append(lines, workflowPanelTruncate(workflowPanelSectionLabel("Usage")+" "+tuitheme.MutedStyle().Render("·")+" "+workflowPanelMeta(strings.Join(usage, " · ")), width))
	}
	return lines
}

func (m model) workflowPanelDetailBlock(section workflowPanelDetailSection, title, body string, focused bool, width int) []string {
	body = strings.TrimSpace(body)
	selected := focused && m.workflowPanel.detailSection == section
	prefix := "  "
	if selected {
		prefix = workflowPanelAction("❯") + " "
	}
	bodyLines := workflowPanelTextLines(body)
	header := title
	if len(bodyLines) > 0 {
		header = fmt.Sprintf("%s · %d %s", title, len(bodyLines), workflowPanelPlural(len(bodyLines), "line", "lines"))
	}
	expandable := section == workflowPanelDetailPrompt || section == workflowPanelDetailOutcome
	expanded := expandable && m.workflowPanel.detailExpanded && m.workflowPanel.expandedSection == section
	if expandable && !expanded {
		header += " · ⏎ expand"
	}
	lines := []string{workflowPanelTruncate(prefix+workflowPanelDetailHeader(header), width)}
	if body == "" {
		return append(lines, "  "+workflowPanelMeta("Not available"))
	}
	if expanded {
		return append(lines, workflowPanelVisibleTextLines(bodyLines, m.workflowPanel.detailScroll, workflowPanelExpandedVisibleLines, width)...)
	}
	lines = append(lines, "  "+workflowPanelTruncate(bodyLines[0], max(1, width-2)))
	if hidden := len(bodyLines) - 1; hidden > 0 {
		lines = append(lines, "  "+workflowPanelMeta(workflowPanelTruncate(fmt.Sprintf("… %d more lines", hidden), max(1, width-2))))
	}
	return lines
}

func workflowPanelActivityBlock(task protocol.WorkflowPanelTask, selected bool, width int) []string {
	prefix := "  "
	if selected {
		prefix = workflowPanelAction("❯") + " "
	}
	items := workflowPanelActivityItems(task)
	header := "Activity"
	if len(items) > 0 {
		header = fmt.Sprintf("Activity · last %d of %d", min(3, len(items)), len(items))
	}
	lines := []string{workflowPanelTruncate(prefix+workflowPanelDetailHeader(header), width)}
	if len(items) == 0 {
		return append(lines, "  "+workflowPanelMeta("Not available"))
	}
	start := max(0, len(items)-3)
	for _, item := range items[start:] {
		lines = append(lines, "  "+workflowPanelTruncate(item, max(1, width-2)))
	}
	return lines
}

func workflowPanelActivityItems(task protocol.WorkflowPanelTask) []string {
	items := []string{}
	for _, activity := range task.Activity {
		msg := workflowPanelOneLine(activity.Message)
		if msg == "" {
			continue
		}
		if activity.ToolName != "" {
			msg = workflowPanelOneLine(activity.ToolName) + ": " + msg
		}
		items = append(items, msg)
	}
	for _, name := range task.ToolCallNames {
		if name = workflowPanelOneLine(name); name != "" {
			items = append(items, name)
		}
	}
	return items
}

const workflowPanelExpandedVisibleLines = 12

func (m model) workflowPanelExpandedDetailBox(task protocol.WorkflowPanelTask, width int) string {
	title := "Prompt"
	body := task.Prompt
	if m.workflowPanel.expandedSection == workflowPanelDetailOutcome {
		title = "Outcome"
		body = task.Error
		if body == "" {
			body = task.Outcome
		}
		if body == "" {
			body = task.Message
		}
	}
	body = strings.TrimSpace(body)
	contentWidth := max(20, width-4)
	bodyLines := workflowPanelWrappedTextLines(body, max(1, contentWidth-2))
	header := fmt.Sprintf("%s · %d %s · Enter collapse", title, len(workflowPanelTextLines(body)), workflowPanelPlural(len(workflowPanelTextLines(body)), "line", "lines"))
	if len(bodyLines) > 0 {
		header += " · ↑↓ scroll"
	}
	visible := max(6, m.height-8)
	start := clampInt(m.workflowPanel.detailScroll, 0, max(0, len(bodyLines)-1))
	end := min(len(bodyLines), start+visible)
	rows := []string{workflowPanelTruncate(workflowPanelDetailHeader(header), contentWidth), ""}
	if len(bodyLines) == 0 {
		rows = append(rows, "  "+workflowPanelMeta("Not available"))
	} else {
		if start > 0 {
			rows = append(rows, "  "+workflowPanelMeta(workflowPanelTruncate(fmt.Sprintf("↑ %d previous lines", start), max(1, contentWidth-2))))
		}
		for _, line := range bodyLines[start:end] {
			rows = append(rows, workflowPanelTruncate("  "+line, contentWidth))
		}
		if end < len(bodyLines) {
			rows = append(rows, "  "+workflowPanelMeta(workflowPanelTruncate(fmt.Sprintf("↓ %d more lines", len(bodyLines)-end), max(1, contentWidth-2))))
		}
	}
	return workflowPanelSingleColumnBox(rows, contentWidth)
}

func workflowPanelTextLines(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	raw := strings.Split(text, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			lines = append(lines, "")
		} else {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	return lines
}

func workflowPanelVisibleTextLines(lines []string, scroll, limit, width int) []string {
	if len(lines) == 0 {
		return []string{"  Not available"}
	}
	if scroll < 0 {
		scroll = 0
	}
	if scroll >= len(lines) {
		scroll = max(0, len(lines)-1)
	}
	end := min(len(lines), scroll+limit)
	out := make([]string, 0, end-scroll+1)
	for _, line := range lines[scroll:end] {
		out = append(out, "  "+workflowPanelTruncate(line, max(1, width-2)))
	}
	if end < len(lines) {
		out = append(out, "  "+workflowPanelTruncate(fmt.Sprintf("… %d more lines", len(lines)-end), max(1, width-2)))
	}
	return out
}

func workflowPanelWrappedTextLines(text string, width int) []string {
	lines := workflowPanelTextLines(text)
	if len(lines) == 0 {
		return nil
	}
	width = max(1, width)
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			out = append(out, "")
			continue
		}
		out = append(out, workflowPanelWrapLine(line, width)...)
	}
	return out
}

func workflowPanelWrapLine(line string, width int) []string {
	line = workflowPanelCellLine(line)
	if line == "" {
		return []string{""}
	}
	if width <= 0 || xansi.StringWidth(line) <= width {
		return []string{line}
	}
	var wrapped []string
	var b strings.Builder
	lineWidth := 0
	for _, r := range line {
		part := string(r)
		partWidth := xansi.StringWidth(part)
		if lineWidth > 0 && lineWidth+partWidth > width {
			wrapped = append(wrapped, b.String())
			b.Reset()
			lineWidth = 0
		}
		b.WriteRune(r)
		lineWidth += partWidth
	}
	if b.Len() > 0 {
		wrapped = append(wrapped, b.String())
	}
	return wrapped
}

func workflowPanelRunHeader(snapshot *protocol.WorkflowPanelSnapshot, width int) string {
	title := snapshot.RunID
	if title == "" {
		title = "Workflow"
	}
	status := strings.TrimSpace(snapshot.Status)
	elapsed := workflowPanelDuration(snapshot.ElapsedMS)
	count := workflowPanelAgentCount(snapshot)
	parts := []string{workflowPanelTitleName(title)}
	if status != "" {
		parts = append(parts, workflowPanelStatusStyle(status).Render(status))
	}
	if count != "" {
		parts = append(parts, workflowPanelMeta(count))
	}
	if elapsed != "" {
		parts = append(parts, workflowPanelMeta(elapsed))
	}
	return workflowPanelTruncate(workflowPanelJoinMeta(parts...), width)
}

func workflowPanelAgentCount(snapshot *protocol.WorkflowPanelSnapshot) string {
	if snapshot == nil {
		return ""
	}
	done := 0
	total := 0
	for _, phase := range snapshot.Phases {
		done += phase.Done
		total += phase.Total
	}
	if total == 0 {
		return ""
	}
	return fmt.Sprintf("%d/%d agents", done, total)
}

func workflowPanelPhaseLines(snapshot *protocol.WorkflowPanelSnapshot, selected int, focused bool, width int) []string {
	lines := []string{workflowPanelTruncate(workflowPanelSectionLabel("Phases"), width)}
	for i, phase := range snapshot.Phases {
		pointer := "  "
		if i == selected && focused {
			pointer = workflowPanelAction("❯") + " "
		}
		label := phase.Name
		if label == "" {
			label = "Phase"
		}
		count := ""
		if phase.Total > 0 {
			count = fmt.Sprintf(" %d/%d", phase.Done, phase.Total)
		}
		line := pointer + workflowPanelStatusIconStyled(phase.Status) + " " + workflowPanelMaybeSelected(label, i == selected && focused) + workflowPanelMeta(count)
		lines = append(lines, workflowPanelTruncate(line, width))
	}
	return lines
}

func workflowPanelTaskLines(snapshot *protocol.WorkflowPanelSnapshot, phaseIndex, selected int, focused bool, width int) []string {
	if len(snapshot.Phases) == 0 {
		return []string{workflowPanelTruncate("Tasks", width), "Not started yet"}
	}
	phase := snapshot.Phases[phaseIndex]
	title := phase.Name
	if title == "" {
		title = "Tasks"
	}
	if phase.Total > 0 {
		title = fmt.Sprintf("%s · %d agents", title, phase.Total)
	}
	lines := []string{workflowPanelTruncate(workflowPanelSectionLabel(title), width)}
	if len(phase.Tasks) == 0 {
		return append(lines, workflowPanelMeta("Not started yet"))
	}
	for i, task := range phase.Tasks {
		pointer := "  "
		if i == selected && focused {
			pointer = workflowPanelAction("❯") + " "
		}
		lines = append(lines, workflowPanelTruncate(pointer+workflowPanelTaskLine(task), width))
	}
	return lines
}

func workflowPanelTaskLine(task protocol.WorkflowPanelTask) string {
	label := workflowPanelOneLine(task.Label)
	if label == "" {
		label = workflowPanelOneLine(task.ID)
	}
	parts := []string{workflowPanelStatusIcon(task.Status) + " " + label}
	if task.Model != "" {
		parts = append(parts, workflowPanelMeta(task.Model))
	}
	if tokens := workflowPanelTaskOutputTokens(task); tokens != "" {
		parts = append(parts, workflowPanelMeta(tokens))
	}
	if task.ToolCalls > 0 {
		parts = append(parts, workflowPanelMeta(fmt.Sprintf("%d %s", task.ToolCalls, workflowPanelPlural(task.ToolCalls, "tool", "tools"))))
	}
	if duration := workflowPanelDuration(task.DurationMS); duration != "" {
		parts = append(parts, workflowPanelMeta(duration))
	}
	if activity := workflowPanelOneLine(workflowPanelTaskActivity(task)); activity != "" {
		parts = append(parts, workflowPanelActivityPreview(task.Status, activity))
	}
	parts[0] = workflowPanelStatusIconStyled(task.Status) + " " + label
	return workflowPanelJoinMeta(parts...)
}

func workflowPanelTaskActivity(task protocol.WorkflowPanelTask) string {
	if task.Error != "" {
		return task.Error
	}
	if task.Outcome != "" {
		return task.Outcome
	}
	for i := len(task.Activity) - 1; i >= 0; i-- {
		if msg := strings.TrimSpace(task.Activity[i].Message); msg != "" {
			return msg
		}
	}
	return strings.TrimSpace(task.Message)
}

func workflowPanelTwoColumnBox(left, right []string, leftWidth, rightWidth int) string {
	border := lipgloss.NewStyle().Foreground(tuitheme.Default.Border)
	height := max(len(left), len(right))
	for len(left) < height {
		left = append(left, "")
	}
	for len(right) < height {
		right = append(right, "")
	}
	sep := "┬"
	top := border.Render("╭" + strings.Repeat("─", leftWidth+2) + sep + strings.Repeat("─", rightWidth+2) + "╮")
	rows := []string{top}
	for i := 0; i < height; i++ {
		rows = append(rows,
			border.Render("│ ")+workflowPanelPad(left[i], leftWidth)+border.Render(" │ ")+workflowPanelPad(right[i], rightWidth)+border.Render(" │"),
		)
	}
	bottom := border.Render("╰" + strings.Repeat("─", leftWidth+2) + "┴" + strings.Repeat("─", rightWidth+2) + "╯")
	rows = append(rows, bottom)
	return strings.Join(rows, "\n")
}

func workflowPanelSingleColumnBox(lines []string, width int) string {
	border := lipgloss.NewStyle().Foreground(tuitheme.Default.Border)
	top := border.Render("╭" + strings.Repeat("─", width+2) + "╮")
	rows := []string{top}
	for _, line := range lines {
		rows = append(rows, border.Render("│ ")+workflowPanelPad(line, width)+border.Render(" │"))
	}
	bottom := border.Render("╰" + strings.Repeat("─", width+2) + "╯")
	rows = append(rows, bottom)
	return strings.Join(rows, "\n")
}

func renderWorkflowPanelRuns(result *protocol.LocalResult, selected int) []string {
	lines := []string{}
	runs := workflowPanelRunSections(result)
	if len(runs) == 0 {
		lines = append(lines, "", workflowPanelMeta("No dynamic workflows in this session."))
	} else {
		lines = append(lines, "")
		for i, section := range runs {
			prefix := "  "
			if i == selected {
				prefix = workflowPanelAction("❯") + " "
			}
			status := localResultFieldValue(section.Fields, "Status")
			tasks := localResultFieldValue(section.Fields, "Tasks")
			phase := localResultFieldValue(section.Fields, "Phase")
			summary := localResultFieldValue(section.Fields, "Summary")
			line := fmt.Sprintf("%s%s", prefix, workflowPanelMaybeSelected(section.Title, i == selected))
			if status != "" {
				line += workflowPanelMutedSep() + workflowPanelStatusStyle(status).Render(status)
			}
			if phase != "" {
				line += workflowPanelMutedSep() + workflowPanelMeta(phase)
			}
			if tasks != "" {
				line += workflowPanelMutedSep() + workflowPanelMeta(tasks)
			}
			lines = append(lines, line)
			if summary != "" {
				lines = append(lines, "    "+workflowPanelMeta(summary))
			}
		}
	}
	if section := workflowPanelSection(result, "Available workflows"); section != nil {
		lines = append(lines, "", workflowPanelSectionLabel("Available workflows"))
		for _, field := range section.Fields {
			lines = append(lines, "  "+workflowPanelTitleName(field.Label)+workflowPanelMutedSep()+workflowPanelMeta(field.Value))
		}
	}
	return lines
}

func renderWorkflowPanelRun(result *protocol.LocalResult) []string {
	lines := []string{}
	errorStyle := lipgloss.NewStyle().Foreground(tuitheme.Default.Error)
	runID := localResultFieldValue(result.Fields, "Run")
	status := localResultFieldValue(result.Fields, "Status")
	if runID != "" || status != "" {
		line := workflowPanelTitleName(strings.TrimSpace(runID))
		if status != "" {
			if line != "" {
				line += workflowPanelMutedSep()
			}
			line += workflowPanelStatusStyle(status).Render(status)
		}
		lines = append(lines, "", line)
	}
	if errText := localResultFieldValue(result.Fields, "Error"); errText != "" {
		lines = append(lines, errorStyle.Render("Error · "+errText))
	} else if summary := localResultFieldValue(result.Fields, "Summary"); summary != "" {
		lines = append(lines, workflowPanelSectionLabel("Summary")+workflowPanelMutedSep()+summary)
	}
	for _, section := range result.Sections {
		if section.Title == "Available workflows" || section.Title == "Events" {
			continue
		}
		lines = append(lines, "", workflowPanelSectionLabel(section.Title))
		for _, field := range section.Fields {
			value := strings.TrimSpace(field.Value)
			if value == "" {
				lines = append(lines, "  "+workflowPanelTitleName(field.Label))
			} else {
				lines = append(lines, "  "+workflowPanelTitleName(field.Label)+workflowPanelMutedSep()+workflowPanelMeta(value))
			}
		}
	}
	return lines
}

func workflowPanelRunID(result *protocol.LocalResult) string {
	if result == nil {
		return ""
	}
	return localResultFieldValue(result.Fields, "Run")
}

func workflowPanelRunSections(result *protocol.LocalResult) []protocol.LocalResultSection {
	if result == nil || result.Kind != "workflows" {
		return nil
	}
	out := []protocol.LocalResultSection{}
	for _, section := range result.Sections {
		if section.Title == "" || section.Title == "Available workflows" {
			continue
		}
		out = append(out, section)
	}
	return out
}

func workflowPanelSection(result *protocol.LocalResult, title string) *protocol.LocalResultSection {
	if result == nil {
		return nil
	}
	for i := range result.Sections {
		if result.Sections[i].Title == title {
			return &result.Sections[i]
		}
	}
	return nil
}

func localResultFieldValue(fields []protocol.LocalResultField, label string) string {
	for _, field := range fields {
		if field.Label == label {
			return strings.TrimSpace(field.Value)
		}
	}
	return ""
}
