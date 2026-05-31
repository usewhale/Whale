package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/runtime/protocol"
)

const workflowPanelRefreshInterval = time.Second

type workflowPanelFocus int

const (
	workflowPanelFocusPhase workflowPanelFocus = iota
	workflowPanelFocusTask
)

type workflowPanelDetailSection int

const (
	workflowPanelDetailPrompt workflowPanelDetailSection = iota
	workflowPanelDetailActivity
	workflowPanelDetailOutcome
)

type workflowPanelState struct {
	result          *protocol.LocalResult
	selected        int
	runID           string
	tickID          int
	selectedPhase   int
	selectedTask    int
	focus           workflowPanelFocus
	detail          bool
	detailRight     bool
	detailSection   workflowPanelDetailSection
	expandedSection workflowPanelDetailSection
	detailExpanded  bool
	detailScroll    int
}

type workflowPanelRefreshMsg struct {
	id    int
	runID string
}

func workflowPanelRefreshCmd(id int, runID string) tea.Cmd {
	return tea.Tick(workflowPanelRefreshInterval, func(time.Time) tea.Msg {
		return workflowPanelRefreshMsg{id: id, runID: runID}
	})
}

func (m *model) openWorkflowPanel(result *protocol.LocalResult) tea.Cmd {
	if result != nil {
		m.workflowPanel.result = result
		if runID := workflowPanelRunID(result); runID != "" {
			m.workflowPanel.runID = runID
		}
	}
	m.mode = modeWorkflowPanel
	m.status = "workflows"
	m.clampWorkflowPanelSelection()
	m.clampWorkflowPanelSnapshotSelection()
	m.workflowPanel.tickID++
	return workflowPanelRefreshCmd(m.workflowPanel.tickID, m.workflowPanel.runID)
}

func (m *model) handleWorkflowPanelRefresh(msg workflowPanelRefreshMsg) tea.Cmd {
	if m.mode != modeWorkflowPanel || msg.id != m.workflowPanel.tickID {
		return nil
	}
	m.dispatchIntent(protocol.Intent{Kind: protocol.IntentRequestWorkflowPanel, WorkflowRunID: m.workflowPanel.runID})
	m.workflowPanel.tickID++
	return workflowPanelRefreshCmd(m.workflowPanel.tickID, m.workflowPanel.runID)
}

func (m *model) handleWorkflowPanelEvent(result *protocol.LocalResult) tea.Cmd {
	if result != nil {
		m.workflowPanel.result = result
		if runID := workflowPanelRunID(result); runID != "" {
			m.workflowPanel.runID = runID
		}
	}
	m.clampWorkflowPanelSelection()
	m.clampWorkflowPanelSnapshotSelection()
	if m.mode != modeWorkflowPanel {
		return m.openWorkflowPanel(result)
	}
	m.status = "workflows"
	return nil
}

func shouldOpenWorkflowPanelForLocalResult(result *protocol.LocalResult) bool {
	if result == nil {
		return false
	}
	switch result.Kind {
	case "workflows":
		return true
	case "workflow":
		return result.WorkflowPanelSnapshot != nil
	default:
		return false
	}
}
