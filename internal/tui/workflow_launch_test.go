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
	if got := m.renderWorkflowLaunch(); !strings.Contains(got, "View raw script") {
		t.Fatalf("launch render missing raw script action:\n%s", got)
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
			result    *protocol.LocalResult
			selected  int
			rawScroll int
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

func TestWorkflowLaunchViewRawScriptIsReadOnlyAndReturnsToConfirmation(t *testing.T) {
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeWorkflowLaunch,
		width:     100,
		height:    16,
		workflowLaunch: struct {
			result    *protocol.LocalResult
			selected  int
			rawScroll int
		}{result: workflowLaunchTestResult(), selected: 1},
	}

	cmd := m.handleWorkflowLaunchKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("did not expect async command")
	}
	if m.mode != modeWorkflowRawScript {
		t.Fatalf("mode = %v, want raw script", m.mode)
	}
	got := m.renderWorkflowRawScript()
	for _, want := range []string{"Workflow raw script", "Script    builtin:deep-research", "export const meta", "Esc back"} {
		if !strings.Contains(got, want) {
			t.Fatalf("raw script render missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(strings.ToLower(got), "edit") || strings.Contains(got, "$EDITOR") {
		t.Fatalf("raw script view should be read-only, got:\n%s", got)
	}
	m.handleWorkflowRawScriptKey(tea.KeyMsg{Type: tea.KeyDown})
	if m.workflowLaunch.rawScroll == 0 {
		t.Fatalf("expected raw script view to scroll")
	}
	m.handleWorkflowRawScriptKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeWorkflowLaunch {
		t.Fatalf("mode = %v, want workflow launch", m.mode)
	}
}

func TestWorkflowRunLocalResultDoesNotRenderPlainLaunchNotice(t *testing.T) {
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
		PlainText: "Started the deep-research workflow in the background.\n\nOpen /workflows to watch progress and inspect details. I'll report back here when it completes.",
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
	messages := m.chatMessages()
	messages = nonHeaderMessages(messages)
	if len(messages) != 1 {
		t.Fatalf("expected only command echo; workflow lifecycle is rendered from workflow_snapshot, got %+v", messages)
	}
	got := strings.Join(tuirender.ChatLines(messages, 120), "\n")
	last := messages[len(messages)-1]
	if last.Role != "you" || last.Kind != tuirender.KindText || last.Local != nil {
		t.Fatalf("workflow local result should not enter chat as assistant text, got %+v", last)
	}
	if !strings.Contains(got, "/deep-research question") {
		t.Fatalf("workflow command echo missing:\n%s", got)
	}
	if strings.Contains(got, "Started the deep-research workflow") || strings.Contains(got, "async_launched") || strings.Contains(got, "/tmp/run-123/script.js") {
		t.Fatalf("workflow run local result should not render transcript lifecycle text:\n%s", got)
	}
}

func nonHeaderMessages(messages []tuirender.UIMessage) []tuirender.UIMessage {
	out := messages[:0]
	for _, msg := range messages {
		if msg.Role == "header" {
			continue
		}
		out = append(out, msg)
	}
	return out
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
		}, {
			Title: "Raw script",
			Fields: []protocol.LocalResultField{
				{Label: "Source", Value: "builtin"},
				{Label: "Path", Value: "builtin:deep-research"},
				{Label: "Script", Value: strings.Join([]string{
					"export const meta = {",
					"  name: 'deep-research',",
					"}",
					"phase('Scope')",
					"await agent('scope')",
					"phase('Search')",
					"await agent('search')",
					"phase('Synthesize')",
					"return {}",
				}, "\n")},
			},
		}},
		Actions: []protocol.LocalResultAction{
			{Label: "Yes, run it", WorkflowName: "deep-research", WorkflowArgs: "question", WorkflowResume: "run-source"},
			{Label: "View raw script"},
			{Label: "No"},
		},
	}
}
