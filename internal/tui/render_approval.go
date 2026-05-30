package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/usewhale/whale/internal/core"
	tuirender "github.com/usewhale/whale/internal/tui/render"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
	"strings"
)

func (m model) renderApprovalPrompt() string {
	title := lipgloss.NewStyle().Foreground(tuitheme.Default.Palette).Bold(true)
	tool := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)
	body := lipgloss.NewStyle()
	hint := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)

	review := isFileDiffApproval(m.approval.toolName, m.approval.metadata)
	memory := memoryApprovalKind(m.approval.metadata)
	externalDirectory := approvalPermissionKind(m.approval.metadata) == "external_directory"
	titleText := "Approval required"
	if review {
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
	}
	if memory != "" {
		if memoryPreview := renderApprovalMemoryMetadata(m.approval.metadata); memoryPreview != "" {
			bodyParts = append(bodyParts, memoryPreview)
		}
	}
	if detail := approvalDisplayBody(m.approval.toolName, m.approval.reason); detail != "" {
		bodyParts = append(bodyParts, renderApprovalDetail(m.approval.toolName, detail))
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

	opts := []string{
		renderApprovalOption("Allow once", "a", "", m.approval.selected == 0, false),
		renderApprovalOption(approvalSessionOptionLabel(m.approval.metadata), "s", "", m.approval.selected == 1, false),
		renderApprovalOption("Deny", "d", "", m.approval.selected == 2, true),
	}

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

func isFileDiffApproval(toolName string, metadata map[string]any) bool {
	if strings.TrimSpace(core.AsString(metadata["approval_kind"])) == "file_diff_review" {
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
