package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/usewhale/whale/internal/core"
	tuirender "github.com/usewhale/whale/internal/tui/render"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

func (m model) renderApprovalPrompt() string {
	title := lipgloss.NewStyle().Foreground(tuitheme.Default.Palette).Bold(true)
	tool := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)
	body := lipgloss.NewStyle()
	hint := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)

	review := isFileDiffApproval(m.approval.toolName, m.approval.metadata)
	memory := memoryApprovalKind(m.approval.metadata)
	externalDirectory := approvalPermissionKind(m.approval.metadata) == "external_directory"
	workflowName := approvalWorkflowName(m.approval.metadata)
	titleText := "Approval required"
	if workflowName != "" {
		titleText = `Tool use · from the "` + workflowName + `" workflow`
	} else if review {
		titleText = "Approval required: file diff review"
	} else if memory != "" {
		titleText = "Approval required: " + memory
	} else if externalDirectory {
		titleText = "Approval required: file access"
	}
	bodyParts := []string{}
	if review {
		bodyParts = append(bodyParts, "Review file changes before Whale applies them.")
	} else if memory == "memory write" {
		bodyParts = append(bodyParts, "Review memory before Whale saves it.")
	} else if memory == "memory delete" {
		bodyParts = append(bodyParts, "Review memory before Whale deletes it.")
	} else if externalDirectory {
		bodyParts = append(bodyParts, "Allow access to this path.")
		if target := approvalPermissionTarget(m.approval.metadata); target != "" {
			bodyParts = append(bodyParts, "Path: "+target)
		}
	} else if workflowName != "" {
		if workflowBody := m.renderWorkflowApprovalBody(); workflowBody != "" {
			bodyParts = append(bodyParts, workflowBody)
		}
	}
	if memory != "" {
		if memoryPreview := renderApprovalMemoryMetadata(m.approval.metadata); memoryPreview != "" {
			bodyParts = append(bodyParts, memoryPreview)
		}
	}
	if detail := approvalDisplayBody(m.approval.toolName, m.approval.reason); detail != "" && !approvalWorkflowBodyIncludesDetail(workflowName, m.approval.toolName) {
		bodyParts = append(bodyParts, renderApprovalDetail(m.approval.toolName, detail))
	}
	if risk := approvalShellRiskExplanation(m.approval.toolName, m.approval.metadata); risk != "" {
		bodyParts = append(bodyParts, risk)
	}
	approvalBody := body.Render(indentApprovalBody(strings.Join(bodyParts, "\n")))
	if diff := renderApprovalDiffMetadata(m.approval.metadata, approvalFileDiffPreviewMaxLines); diff != "" {
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
	if queued := len(m.approvalQueue); queued > 0 {
		queueLine := strconv.Itoa(queued) + " more approval request"
		if queued != 1 {
			queueLine += "s"
		}
		queueLine += " queued."
		if approvalBody != "" {
			approvalBody += "\n\n" + hint.Render(queueLine)
		} else {
			approvalBody = hint.Render(queueLine)
		}
	}

	opts := m.renderApprovalOptions()

	return lipgloss.JoinVertical(
		lipgloss.Left,
		title.Render(titleText)+"  "+tool.Render(approvalToolDisplayName(m.approval.toolName)),
		"",
		approvalBody,
		"",
		"  "+strings.Join(opts, "   "),
		"",
		hint.Render("Enter confirm · Esc cancel · ←/→/tab switch"),
	)
}

func approvalWorkflowBodyIncludesDetail(workflowName, toolName string) bool {
	if strings.TrimSpace(workflowName) == "" {
		return false
	}
	switch toolName {
	case "web_search", "fetch", "web_fetch":
		return true
	default:
		return false
	}
}

func (m model) renderApprovalOptions() []string {
	if approvalWorkflowName(m.approval.metadata) != "" {
		return []string{
			renderApprovalOption("Yes", "1", "", m.approval.selected == 0, false),
			renderApprovalOption("Yes, and don't ask again for "+approvalSessionOptionSubject(m.approval.toolName, m.approval.metadata, m.cwdPath), "2", "", m.approval.selected == 1, false),
			renderApprovalOption("Yes, and switch to auto mode", "3", "workflows run best with it on", m.approval.selected == 2, false),
			renderApprovalOption("No", "4", "", m.approval.selected == 3, true),
		}
	}
	return []string{
		renderApprovalOption("Allow once", "a", "", m.approval.selected == 0, false),
		renderApprovalOption(approvalSessionOptionLabel(m.approval.metadata), "s", "", m.approval.selected == 1, false),
		renderApprovalOption("Deny", "d", "", m.approval.selected == 2, true),
	}
}

func (m model) renderWorkflowApprovalBody() string {
	detail := approvalDisplayBody(m.approval.toolName, m.approval.reason)
	switch m.approval.toolName {
	case "web_search":
		if detail == "" {
			return "Whale wants to search the web."
		}
		return "Web Search(" + strconvQuote(detail) + ")\nWhale wants to search the web for: " + detail
	case "fetch", "web_fetch":
		host := approvalSessionScope(m.approval.metadata)
		if host == "" {
			host = "this host"
		}
		if detail == "" {
			return "Whale wants to fetch content from " + host + "."
		}
		return approvalToolDisplayName(m.approval.toolName) + "\nurl: " + strconvQuote(detail) + "\nWhale wants to fetch content from " + host + "."
	default:
		return ""
	}
}

func isFileDiffApproval(toolName string, metadata map[string]any) bool {
	if strings.TrimSpace(core.AsString(metadata["approval_kind"])) == "file_diff_review" {
		return true
	}
	switch toolName {
	case "edit", "write", "multi_edit":
		return true
	default:
		return false
	}
}

func memoryApprovalKind(metadata map[string]any) string {
	switch strings.TrimSpace(core.AsString(metadata["approval_kind"])) {
	case "memory_write":
		return "memory write"
	case "memory_delete":
		return "memory delete"
	default:
		return ""
	}
}

func approvalPermissionKind(metadata map[string]any) string {
	return strings.TrimSpace(core.AsString(metadata["permission_kind"]))
}

func approvalPermissionTarget(metadata map[string]any) string {
	return strings.TrimSpace(core.AsString(metadata["permission_target"]))
}

func approvalWorkflowName(metadata map[string]any) string {
	return strings.TrimSpace(core.AsString(metadata["workflow_name"]))
}

func approvalSessionScope(metadata map[string]any) string {
	return strings.TrimSpace(core.AsString(metadata["approval_session_scope"]))
}

func approvalShellRiskExplanation(toolName string, metadata map[string]any) string {
	if toolName != "shell_run" {
		return ""
	}
	if strings.TrimSpace(core.AsString(metadata["shell_risk_code"])) != "parse_failed" {
		return ""
	}
	reason := strings.TrimSpace(core.AsString(metadata["shell_risk_reason"]))
	if reason == "" {
		reason = "it is not a simple shell command"
	}
	return "This command could not be proven read-only because " + reason + "."
}

func approvalSessionOptionSubject(toolName string, metadata map[string]any, cwd string) string {
	scope := approvalSessionScope(metadata)
	switch toolName {
	case "web_search":
		if strings.TrimSpace(cwd) != "" {
			return "Web Search commands in " + strings.TrimSpace(cwd)
		}
		return "Web Search commands"
	case "fetch", "web_fetch":
		if scope != "" {
			return scope
		}
		return "this host"
	default:
		if scope != "" {
			return scope
		}
		return "this tool request"
	}
}

func approvalSessionOptionLabel(metadata map[string]any) string {
	if asBool(metadata["shell_approval_family"]) {
		return "Allow similar commands"
	}
	return "Allow session"
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

func renderApprovalDetail(toolName, detail string) string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return ""
	}
	if toolName == "shell_run" {
		return "$ " + tuirender.RenderCommandLike(detail)
	}
	return detail
}

func strconvQuote(v string) string {
	v = strings.ReplaceAll(v, "\\", "\\\\")
	v = strings.ReplaceAll(v, `"`, `\"`)
	return `"` + v + `"`
}

func renderApprovalMemoryMetadata(metadata map[string]any) string {
	kind := strings.TrimSpace(core.AsString(metadata["approval_kind"]))
	scope := strings.TrimSpace(core.AsString(metadata["memory_scope"]))
	typ := strings.TrimSpace(core.AsString(metadata["memory_type"]))
	name := strings.TrimSpace(core.AsString(metadata["memory_name"]))
	description := strings.TrimSpace(core.AsString(metadata["memory_description"]))
	content := strings.TrimSpace(core.AsString(metadata["memory_content_preview"]))
	status := strings.TrimSpace(core.AsString(metadata["memory_write_status"]))

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

func renderApprovalOption(label, shortcut, description string, selected, destructive bool) string {
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
	out := prefix + style.Render(label) + " " + key
	if description = strings.TrimSpace(description); description != "" {
		out += " " + lipgloss.NewStyle().Foreground(tuitheme.Default.Muted).Render(description)
	}
	return out
}

func mutedSelectionPrefix(selected bool) string {
	if selected {
		return lipgloss.NewStyle().Foreground(tuitheme.Default.InfoSoft).Bold(true).Render("› ")
	}
	return lipgloss.NewStyle().Foreground(tuitheme.Default.Muted).Render("  ")
}
