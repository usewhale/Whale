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
		s.emit(Event{Kind: EventPluginsManagerUpdated, Plugins: protocolPlugins(s.PluginsForManager())})
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
		if s.app.SessionID() != prevSessionID {
			s.emitSessionHydrated()
		}
		if cmd.Text != "" {
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
		if cmd.Text != "" {
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
