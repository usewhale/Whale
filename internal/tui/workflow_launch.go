package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	appcommands "github.com/usewhale/whale/internal/runtime/commands"
	"github.com/usewhale/whale/internal/runtime/protocol"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

func (m *model) openWorkflowLaunch(result *protocol.LocalResult) tea.Cmd {
	m.workflowLaunch.result = result
	m.workflowLaunch.selected = 0
	m.mode = modeWorkflowLaunch
	m.status = "workflow"
	return nil
}

func (m *model) handleWorkflowLaunchKey(msg tea.KeyMsg) tea.Cmd {
	actions := workflowLaunchActions(m.workflowLaunch.result)
	switch msg.String() {
	case "esc", "q":
		m.mode = modeChat
		m.status = "ready"
	case "up", "k":
		if len(actions) > 0 && m.workflowLaunch.selected > 0 {
			m.workflowLaunch.selected--
		}
	case "down", "j":
		if len(actions) > 0 && m.workflowLaunch.selected < len(actions)-1 {
			m.workflowLaunch.selected++
		}
	case "enter":
		if len(actions) == 0 {
			return nil
		}
		action := actions[m.workflowLaunch.selected]
		if strings.TrimSpace(action.WorkflowName) != "" {
			m.mode = modeChat
			m.status = "command pending"
			m.localSubmitPending++
			m.dispatchIntent(protocol.Intent{
				Kind:           protocol.IntentStartWorkflow,
				WorkflowName:   action.WorkflowName,
				WorkflowArgs:   action.WorkflowArgs,
				WorkflowResume: action.WorkflowResume,
				WorkflowTrust:  action.WorkflowTrust,
			})
			m.refreshViewportContent()
			return nil
		}
		cmd := strings.TrimSpace(action.Command)
		m.mode = modeChat
		m.status = "ready"
		if cmd == "" {
			return nil
		}
		submit := appcommands.ClassifySubmit(cmd, appcommands.CommandsHelp())
		return m.submitLocalNoTurn(submit)
	}
	return nil
}

func (m model) renderWorkflowLaunch() string {
	result := m.workflowLaunch.result
	if result == nil {
		return ""
	}
	width := m.width
	if width <= 0 {
		width = 100
	}
	contentWidth := max(40, min(width-4, 120))
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(tuitheme.Default.Palette)
	muted := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)
	lines := []string{titleStyle.Render("Workflow(dynamic workflow: " + workflowLaunchField(result, "Workflow") + ")"), "", result.Title}
	if desc := workflowLaunchDescription(result); desc != "" {
		lines = append(lines, "", desc)
	}
	if phaseSection := workflowPanelSection(result, "Phases"); phaseSection != nil && len(phaseSection.Fields) > 0 {
		lines = append(lines, "", "Phases")
		for _, field := range phaseSection.Fields {
			lines = append(lines, workflowLaunchWrapLine(field.Label+"  "+field.Value, contentWidth-4)...)
		}
	}
	if args := workflowLaunchField(result, "Args"); args != "" {
		lines = append(lines, "", "args: "+args)
	}
	if risk := workflowLaunchField(result, "Risk"); risk != "" {
		lines = append(lines, "", risk)
	}
	actions := workflowLaunchActions(result)
	if len(actions) > 0 {
		lines = append(lines, "", "Actions")
		for i, action := range actions {
			prefix := "  "
			if i == m.workflowLaunch.selected {
				prefix = "❯ "
			}
			label := strings.TrimSpace(action.Label)
			if label == "" {
				label = "Action"
			}
			lines = append(lines, workflowLaunchWrapLine(prefix+label, contentWidth-4)...)
		}
	}
	lines = append(lines, "", muted.Render("Esc to cancel · ↑↓ select · Enter run"))
	return workflowPanelSingleColumnBox(lines, contentWidth)
}

func workflowLaunchActions(result *protocol.LocalResult) []protocol.LocalResultAction {
	if result == nil {
		return nil
	}
	return result.Actions
}

func workflowLaunchField(result *protocol.LocalResult, label string) string {
	if result == nil {
		return ""
	}
	return localResultFieldValue(result.Fields, label)
}

func workflowLaunchDescription(result *protocol.LocalResult) string {
	if result == nil {
		return ""
	}
	text := strings.TrimSpace(result.PlainText)
	if text == "" {
		return ""
	}
	lines := workflowPanelTextLines(text)
	for i, line := range lines {
		if strings.TrimSpace(line) == "Run a dynamic workflow?" {
			for j := i + 1; j < len(lines); j++ {
				if candidate := strings.TrimSpace(lines[j]); candidate != "" {
					return candidate
				}
			}
		}
	}
	return ""
}

func workflowLaunchWrapLine(line string, width int) []string {
	wrapped := workflowPanelWrapLine(strings.TrimSpace(line), max(1, width))
	if len(wrapped) == 0 {
		return []string{""}
	}
	return wrapped
}
