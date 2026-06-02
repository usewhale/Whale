package service

import (
	"fmt"
	"github.com/usewhale/whale/internal/app"
	appcommands "github.com/usewhale/whale/internal/commands"
	"strings"
)

func (s *Service) enqueueLocalSubmit(line string) {
	if s.localSubmits == nil || s.ctx == nil {
		s.goTracked(func() { s.runLocalSubmitLine(line) })
		return
	}
	select {
	case s.localSubmits <- line:
	case <-s.ctx.Done():
	default:
		s.emit(localSubmitResultEvent("error", "local command queue is full; wait before running another command"))
		s.emit(localSubmitDoneEvent())
	}
}

func (s *Service) runLocalSubmitWorker() {
	for {
		select {
		case line := <-s.localSubmits:
			s.runLocalSubmitLine(line)
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *Service) runLocalSubmitLine(line string) {
	s.handleLocalSubmit(line)
	s.emit(localSubmitDoneEvent())
}

func (s *Service) handleLocalSubmit(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	submit := appcommands.ClassifySubmit(line, app.CommandsHelp, "/mcp")
	line = submit.Line
	if submit.Class == appcommands.SubmitText || submit.Class == appcommands.SubmitTurnStarting {
		s.emit(localSubmitResultEvent("error", "command is not available as a local submit"))
		return
	}
	if line == "/diff" {
		s.emit(Event{Kind: EventDiffResult, Text: s.app.BuildDiffText(s.ctx), Metadata: map[string]any{EventMetadataLocalSubmit: true}})
		return
	}
	prevSessionID := s.app.SessionID()
	if line == "/model" {
		s.emit(Event{
			Kind:            EventModelSelectionRequested,
			ModelChoices:    s.app.SupportedModels(),
			EffortChoices:   s.app.SupportedEfforts(),
			CurrentModel:    s.app.Model(),
			CurrentEffort:   s.app.ReasoningEffort(),
			ThinkingChoices: []string{"on", "off"},
			CurrentThinking: app.OnOff(s.app.ThinkingEnabled()),
		})
		return
	}
	if line == "/permissions" {
		s.emit(Event{Kind: EventPermissionsSelectionRequested, AutoAccept: s.app.AutoAcceptPermissions(), AutoAcceptKnown: true})
		return
	}
	if line == "/focus" {
		mode, err := s.app.ToggleViewMode()
		if err != nil {
			s.emit(localSubmitResultEvent("error", err.Error()))
			return
		}
		s.emit(Event{Kind: EventViewModeChanged, ViewMode: mode, Text: app.ViewModeToggleMessage(mode)})
		return
	}
	if line == "/skills" {
		s.emit(Event{Kind: EventSkillsSelectionRequested})
		return
	}
	if line == "/plugins" {
		s.emit(Event{Kind: EventPluginsManagerUpdated, Plugins: protocolPlugins(s.PluginsForManager()), Open: true})
		return
	}
	if line == "/config" {
		s.emitConfigManagerUpdated(true)
		return
	}
	if line == "/hooks" {
		s.emitHooksManagerUpdated()
		return
	}
	if strings.HasPrefix(line, "/hooks ") {
		s.handleHooksLocalSubmit(line)
		return
	}
	if line == "/workflows" || strings.HasPrefix(line, "/workflows ") {
		fields := strings.Fields(line)
		if len(fields) == 1 {
			s.emitWorkflowPanel("")
		} else {
			s.emit(localSubmitResultEvent("error", "usage: /workflows"))
		}
		return
	}
	if line == "/review" {
		s.emit(Event{Kind: EventReviewRequested})
		return
	}
	if s.app.IsResumeMenu(line) {
		s.emitLocalSessionChoices()
		return
	}
	if line == "/rewind" || line == "/checkpoint" {
		s.emitRewindMessages(false)
		return
	}
	if strings.HasPrefix(line, "/model ") {
		s.emit(localSubmitResultEvent("error", "usage: /model"))
		return
	}
	if question, ok := btwQuestionFromLine(line); ok {
		if question == "" {
			s.emit(localSubmitResultEvent("error", "Usage: /btw <your question>"))
			return
		}
		s.runSideQuestion(question)
		return
	}
	cmd, err := s.app.ExecuteSlash(line)
	if err != nil {
		s.emit(localSubmitResultEvent("error", err.Error()))
		return
	}
	if cmd.Handled {
		if cmd.Turn != nil {
			s.emit(localSubmitResultEvent("error", "command starts an agent turn and cannot run as a local submit"))
			return
		}
		if cmd.ClearScreen {
			s.emit(Event{Kind: EventScreenClearRequested})
		}
		if cmd.ShouldExit {
			s.requestExit()
		}
		if s.app.SessionID() != prevSessionID || cmd.HydrateSession {
			s.emitSessionHydrated()
		}
		if cmd.Text != "" {
			s.maybeWatchWorkflowRun(cmd.LocalResult)
			ev := localSubmitResultEvent("info", cmd.Text)
			ev.LocalResult = protocolLocalResult(cmd.LocalResult)
			s.emit(ev)
		}
		return
	}
	cmd, err = s.app.ExecuteLocalCommand(line)
	if err != nil {
		s.emit(localSubmitResultEvent("error", err.Error()))
		return
	}
	if cmd.Handled {
		if cmd.HydrateSession {
			s.emitSessionHydrated()
		}
		if cmd.Text != "" {
			s.maybeWatchWorkflowRun(cmd.LocalResult)
			ev := localSubmitResultEvent("info", cmd.Text)
			ev.LocalResult = protocolLocalResult(cmd.LocalResult)
			s.emit(ev)
		}
		if cmd.Turn != nil {
			s.emit(localSubmitResultEvent("error", "command starts an agent turn and cannot run as a local submit"))
		}
		return
	}
	if appcommands.LooksLikeSlashCommand(line) {
		s.emit(localSubmitResultEvent("error", fmt.Sprintf("• Unrecognized command %q. Type \"/\" for a list of supported commands.", line)))
		return
	}
}

func (s *Service) handleHooksLocalSubmit(line string) {
	cmd, err := s.app.ExecuteLocalCommand(line)
	if err != nil {
		s.emit(localSubmitResultEvent("error", err.Error()))
		return
	}
	if !cmd.Handled {
		s.emit(localSubmitResultEvent("error", fmt.Sprintf("• Unrecognized command %q. Type \"/\" for a list of supported commands.", line)))
		return
	}
	if cmd.Text != "" {
		s.emit(localSubmitResultEvent("info", cmd.Text))
	}
	if cmd.Mutated {
		s.emitHooksManagerUpdated()
	}
}

func (s *Service) emitWorkflowPanel(runID string) {
	if strings.TrimSpace(runID) == "" {
		out, err := s.app.ExecuteLocalCommand("/workflows")
		if err != nil {
			s.emit(Event{Kind: EventError, Text: err.Error()})
			return
		}
		s.emit(Event{Kind: EventWorkflowPanel, Text: out.Text, LocalResult: protocolLocalResult(out.LocalResult)})
		return
	}
	out := s.app.WorkflowPanelLocalResult(strings.TrimSpace(runID))
	s.emitWorkflowSnapshotForResult(out)
	s.emit(Event{Kind: EventWorkflowPanel, Text: out.PlainText, LocalResult: protocolLocalResult(out)})
}

func (s *Service) cancelWorkflowRun(runID string) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		s.emit(Event{Kind: EventError, Text: "workflow run id is required"})
		return
	}
	out, err := s.app.CancelWorkflowRun(runID)
	if err != nil {
		s.emit(Event{Kind: EventError, Text: err.Error()})
		return
	}
	s.emitWorkflowSnapshotForResult(out)
	s.emit(Event{Kind: EventWorkflowPanel, Text: out.PlainText, LocalResult: protocolLocalResult(out)})
}

func (s *Service) startWorkflow(name, args, resumeFromRunID string, trust bool, script, saveAs, scriptPath string) {
	var (
		out *app.LocalResult
		err error
	)
	switch {
	case strings.TrimSpace(script) != "":
		out, err = s.app.StartGeneratedWorkflowFromConfirmation(script, saveAs, args, resumeFromRunID, trust)
	case strings.TrimSpace(scriptPath) != "":
		out, err = s.app.StartScriptPathWorkflowFromConfirmation(scriptPath, args, resumeFromRunID)
	default:
		out, err = s.app.StartWorkflowFromConfirmation(name, args, resumeFromRunID, trust)
	}
	if err != nil {
		s.emit(localSubmitResultEvent("error", err.Error()))
		s.emit(localSubmitDoneEvent())
		return
	}
	s.maybeWatchWorkflowRun(out)
	s.emitWorkflowSnapshotForResult(out)
	ev := localSubmitResultEvent("info", out.PlainText)
	ev.LocalResult = protocolLocalResult(out)
	s.emit(ev)
	s.emit(localSubmitDoneEvent())
}
