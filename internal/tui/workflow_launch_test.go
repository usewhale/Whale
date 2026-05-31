package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/usewhale/whale/internal/runtime/protocol"
	tuirender "github.com/usewhale/whale/internal/tui/render"
	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

func TestWorkflowLaunchLocalResultOpensInteractiveModal(t *testing.T) {
	m := model{
		assembler:           tuirender.NewAssembler(),
		mode:                modeChat,
		width:               100,
		height:              30,
		localSubmitCommands: []string{"/deep-research question"},
	}
	result := workflowLaunchTestResult()

	cmd := m.handleLocalSubmitResultEvent(protocol.Event{Status: "info", Text: result.PlainText, LocalResult: result})
	if cmd != nil {
		t.Fatal("did not expect command while opening workflow launch modal")
	}
	if m.mode != modeWorkflowLaunch {
		t.Fatalf("mode = %v, want workflow launch", m.mode)
	}
	if m.workflowLaunch.result == nil || m.workflowLaunch.result.Kind != "workflow-launch" {
		t.Fatalf("missing workflow launch result: %+v", m.workflowLaunch.result)
	}
	if got := m.renderWorkflowLaunch(); got == "" || !strings.Contains(got, "Run a dynamic workflow?") || !strings.Contains(got, "Yes, run it") || !strings.Contains(got, "Esc to cancel") {
		t.Fatalf("launch render missing expected content:\n%s", got)
	}
}

func TestWorkflowLaunchEnterRunsSelectedAction(t *testing.T) {
	intents := []protocol.Intent{}
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeWorkflowLaunch,
		width:     100,
		height:    30,
		workflowLaunch: struct {
			result   *protocol.LocalResult
			selected int
		}{result: workflowLaunchTestResult()},
		dispatch: func(in protocol.Intent) {
			intents = append(intents, in)
		},
	}

	cmd := m.handleWorkflowLaunchKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("did not expect async command")
	}
	if m.mode != modeChat {
		t.Fatalf("mode = %v, want chat", m.mode)
	}
	if len(intents) != 1 {
		t.Fatalf("intents = %+v, want one", intents)
	}
	if intents[0].Kind != protocol.IntentStartWorkflow || intents[0].WorkflowName != "deep-research" || intents[0].WorkflowArgs != "question" || intents[0].WorkflowResume != "run-source" {
		t.Fatalf("unexpected intent: %+v", intents[0])
	}
}

func TestWorkflowRunLocalResultRendersAsPlainLaunchNotice(t *testing.T) {
	m := model{
		assembler:           tuirender.NewAssembler(),
		mode:                modeChat,
		width:               100,
		height:              30,
		localSubmitCommands: []string{"/deep-research question"},
	}
	result := &protocol.LocalResult{
		Kind:      "workflow-run",
		Title:     "deep-research is running in background",
		PlainText: "Workflow(dynamic workflow: deep-research)\n/workflows to view dynamic workflow runs\n\nThe deep-research workflow is now running in the background.\n\n✻ Waiting for 1 dynamic workflow to finish",
		Fields: []protocol.LocalResultField{
			{Label: "Status", Value: "async_launched"},
			{Label: "Run", Value: "run-123"},
			{Label: "Script", Value: "/tmp/run-123/script.js"},
		},
	}

	cmd := m.handleLocalSubmitResultEvent(protocol.Event{Status: "info", Text: result.PlainText, LocalResult: result})
	if cmd != nil {
		t.Fatal("did not expect command for workflow run launch notice")
	}
	if m.mode != modeChat {
		t.Fatalf("mode = %v, want chat", m.mode)
	}
	got := strings.Join(tuirender.ChatLines(m.transcript, 120), "\n")
	if !strings.Contains(got, "Workflow(dynamic workflow: deep-research)") || !strings.Contains(got, "✻ Waiting for 1 dynamic workflow to finish") {
		t.Fatalf("workflow launch notice missing expected plain text:\n%s", got)
	}
	if strings.Contains(got, "async_launched") || strings.Contains(got, "/tmp/run-123/script.js") {
		t.Fatalf("workflow run should not render structured fields in chat:\n%s", got)
	}
}

func TestWorkflowEventsLocalResultDoesNotOpenPanel(t *testing.T) {
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		width:     100,
		height:    30,
	}
	result := &protocol.LocalResult{
		Kind:      "workflow",
		Title:     "Workflow Events run-123",
		PlainText: "Workflow events\n\nrun: run-123\nstatus: completed\n\nevents:\n  - log: searched docs",
		Sections: []protocol.LocalResultSection{{
			Title: "Events",
			Fields: []protocol.LocalResultField{
				{Label: "log", Value: "searched docs"},
			},
		}},
	}

	cmd := m.handleLocalSubmitResultEvent(protocol.Event{Status: "info", Text: result.PlainText, LocalResult: result})
	if cmd != nil {
		t.Fatal("did not expect command for workflow events result")
	}
	if m.mode != modeChat {
		t.Fatalf("mode = %v, want chat", m.mode)
	}
	got := strings.Join(tuirender.ChatLines(m.transcript, 120), "\n")
	if !strings.Contains(got, "Workflow Events") || !strings.Contains(got, "searched docs") {
		t.Fatalf("workflow events should render in chat, got:\n%s", got)
	}
}

func TestWorkflowPanelModeRendersPanel(t *testing.T) {
	m := model{
		mode:   modeWorkflowPanel,
		width:  100,
		height: 30,
		workflowPanel: workflowPanelState{
			result: &protocol.LocalResult{
				Kind:      "workflows",
				Title:     "Dynamic workflows",
				PlainText: "Dynamic workflows",
				Sections: []protocol.LocalResultSection{{
					Title: "run-123",
					Fields: []protocol.LocalResultField{
						{Label: "Status", Value: "running"},
					},
				}},
			},
		},
	}

	got := m.renderBody(100, 24)
	if !strings.Contains(got, "Dynamic workflows") || !strings.Contains(got, "run-123") || !strings.Contains(got, "Live refreshes every second") {
		t.Fatalf("workflow panel mode did not render panel:\n%s", got)
	}
	if bottom := strings.Join(m.bottomPartsBeforeInput(100), "\n"); strings.Contains(bottom, "Dynamic workflows") {
		t.Fatalf("workflow panel should render in the main body only, got bottom panel:\n%s", bottom)
	}
}

func TestWorkflowPanelViewRendersWithoutChatContent(t *testing.T) {
	m := model{
		mode:   modeWorkflowPanel,
		page:   pageChat,
		width:  100,
		height: 30,
		workflowPanel: workflowPanelState{
			result: &protocol.LocalResult{
				Kind:      "workflows",
				Title:     "Dynamic workflows",
				PlainText: "Dynamic workflows",
				Sections: []protocol.LocalResultSection{{
					Title: "run-123",
					Fields: []protocol.LocalResultField{
						{Label: "Status", Value: "running"},
					},
				}},
			},
		},
	}

	got := m.View()
	if !strings.Contains(got, "Dynamic workflows") || !strings.Contains(got, "run-123") {
		t.Fatalf("workflow panel view should render even with an empty chat transcript:\n%s", got)
	}
}

func TestWorkflowPanelUsesSemanticColorSegments(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	if got := workflowPanelStatusStyle("completed").GetForeground(); got != tuitheme.Default.Success {
		t.Fatalf("completed status color: want %s, got %s", tuitheme.Default.Success, got)
	}
	if got := workflowPanelStatusStyle("running").GetForeground(); got != tuitheme.Default.InfoSoft {
		t.Fatalf("running status color: want %s, got %s", tuitheme.Default.InfoSoft, got)
	}
	if got := workflowPanelStatusStyle("failed").GetForeground(); got != tuitheme.Default.Error {
		t.Fatalf("failed status color: want %s, got %s", tuitheme.Default.Error, got)
	}

	m := model{width: 120, height: 32}
	snapshot := &protocol.WorkflowPanelSnapshot{
		RunID:     "run-themed",
		Status:    "running",
		Summary:   "short summary",
		ElapsedMS: 42_000,
		Phases: []protocol.WorkflowPanelPhase{{
			Name:   "Search",
			Status: "running",
			Done:   1,
			Total:  2,
			Tasks: []protocol.WorkflowPanelTask{{
				ID:               "task-1",
				Label:            "search:docs",
				Status:           "completed",
				Model:            "deepseek",
				CompletionTokens: 3800,
				ToolCalls:        4,
				DurationMS:       11_000,
				Outcome:          "found official docs",
			}},
		}},
	}

	rendered := strings.Join(m.renderWorkflowPanelSnapshot(snapshot), "\n")
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("expected workflow panel to include ANSI color segments:\n%s", rendered)
	}
	plain := xansi.Strip(rendered)
	for _, want := range []string{"run-themed", "running", "Search", "search:docs", "3.8k out", "4 tools", "11s"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected stripped workflow panel to contain %q, got:\n%s", want, plain)
		}
	}
}

func workflowLaunchTestResult() *protocol.LocalResult {
	return &protocol.LocalResult{
		Kind:      "workflow-launch",
		Title:     "Run a dynamic workflow?",
		PlainText: "Workflow(dynamic workflow: deep-research)\n\nRun a dynamic workflow?\n\nDeep research harness",
		Fields: []protocol.LocalResultField{
			{Label: "Workflow", Value: "deep-research"},
			{Label: "Args", Value: "question"},
		},
		Sections: []protocol.LocalResultSection{{
			Title: "Phases",
			Fields: []protocol.LocalResultField{
				{Label: "1. Scope", Value: "Decompose question"},
			},
		}},
		Actions: []protocol.LocalResultAction{
			{Label: "Yes, run it", WorkflowName: "deep-research", WorkflowArgs: "question", WorkflowResume: "run-source"},
			{Label: "No"},
		},
	}
}
