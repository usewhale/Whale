package service

import (
	"fmt"
	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/app"
	appcommands "github.com/usewhale/whale/internal/commands"
	"github.com/usewhale/whale/internal/session"
	"strings"
)

func (s *Service) handleSubmit(line string, hiddenInput bool, skillBinding *app.SkillBinding) {
	state := submitState{
		line:         strings.TrimSpace(line),
		hiddenInput:  hiddenInput,
		turnOptions:  agent.RunOptions{HiddenInput: hiddenInput},
		skillBinding: skillBinding,
	}
	if state.line == "" {
		return
	}
	state.line = appcommands.ExpandUniqueSlashPrefix(state.line, app.CommandsHelp, "/mcp")
	state.prevSessionID = s.app.SessionID()
	if s.handleSubmitMenuCommand(&state) {
		return
	}
	if s.handleSubmitModeCommand(&state) {
		return
	}
	if s.handleSubmitSlashCommand(&state) {
		return
	}
	if s.handleSubmitLocalCommand(&state) {
		return
	}
	if s.applySubmitHooks(&state) {
		return
	}
	s.startSubmitTurn(&state)
}

type submitState struct {
	line               string
	hiddenInput        bool
	prevSessionID      string
	skipHooks          bool
	skipSkillInjection bool
	turnOptions        agent.RunOptions
	skillBinding       *app.SkillBinding
}

func (s *Service) handleSubmitMenuCommand(state *submitState) bool {
	switch state.line {
	case "/model":
		s.emit(Event{
			Kind:            EventModelSelectionRequested,
			ModelChoices:    s.app.SupportedModels(),
			EffortChoices:   s.app.SupportedEfforts(),
			CurrentModel:    s.app.Model(),
			CurrentEffort:   s.app.ReasoningEffort(),
			ThinkingChoices: []string{"on", "off"},
			CurrentThinking: app.OnOff(s.app.ThinkingEnabled()),
		})
		return true
	case "/permissions":
		s.emit(Event{Kind: EventPermissionsSelectionRequested, AutoAccept: s.app.AutoAcceptPermissions(), AutoAcceptKnown: true})
		return true
	case "/focus":
		mode, err := s.app.ToggleViewMode()
		if err != nil {
			s.emit(Event{Kind: EventError, Text: err.Error()})
			s.emit(Event{Kind: EventTurnDone})
			return true
		}
		msg := app.ViewModeToggleMessage(mode)
		s.emit(Event{Kind: EventViewModeChanged, ViewMode: mode, Text: msg})
		s.emit(Event{Kind: EventTurnDone, LastResponse: msg})
		return true
	case "/skills":
		s.emit(Event{Kind: EventSkillsSelectionRequested})
		return true
	case "/plugins":
		s.emit(Event{Kind: EventPluginsManagerUpdated, Plugins: protocolPlugins(s.PluginsForManager())})
		return true
	case "/review":
		s.emit(Event{Kind: EventReviewRequested})
		return true
	default:
		return false
	}
}

func (s *Service) handleSubmitModeCommand(state *submitState) bool {
	if prompt, ok := appcommands.PlanPromptFromSlash(state.line); ok {
		out, err := s.app.SetMode(session.ModePlan)
		if err != nil {
			s.emit(Event{Kind: EventError, Text: err.Error()})
			s.emit(Event{Kind: EventTurnDone})
			return true
		}
		s.emit(Event{Kind: EventInfo, Text: out})
		state.line = prompt
		state.hiddenInput = false
	}
	if prompt, ok := appcommands.AskPromptFromSlash(state.line); ok {
		out, err := s.app.SetMode(session.ModeAsk)
		if err != nil {
			s.emit(Event{Kind: EventError, Text: err.Error()})
			s.emit(Event{Kind: EventTurnDone})
			return true
		}
		s.emit(Event{Kind: EventInfo, Text: out})
		state.line = prompt
		state.hiddenInput = false
	}
	return false
}

func (s *Service) handleSubmitSlashCommand(state *submitState) bool {
	if s.app.IsResumeMenu(state.line) {
		s.emitSessionChoices()
		s.emit(Event{Kind: EventTurnDone})
		return true
	}
	if state.line == "/rewind" || state.line == "/checkpoint" {
		s.emitRewindMessages(true)
		return true
	}
	if strings.HasPrefix(state.line, "/model ") {
		s.emit(Event{Kind: EventError, Text: "usage: /model"})
		s.emit(Event{Kind: EventTurnDone})
		return true
	}
	if question, ok := btwQuestionFromLine(state.line); ok {
		if question == "" {
			s.emit(Event{Kind: EventError, Text: "Usage: /btw <your question>"})
			s.emit(Event{Kind: EventTurnDone})
			return true
		}
		s.runSideQuestion(question)
		s.emit(Event{Kind: EventTurnDone})
		return true
	}
	cmd, err := s.app.ExecuteSlash(state.line)
	if err != nil {
		s.emit(Event{Kind: EventError, Text: err.Error()})
		s.emit(Event{Kind: EventTurnDone})
		return true
	}
	if cmd.Handled {
		if cmd.ClearScreen {
			s.emit(Event{Kind: EventScreenClearRequested})
		}
		if cmd.ShouldExit {
			s.requestExit()
		}
		if s.app.SessionID() != state.prevSessionID || cmd.HydrateSession {
			s.emitSessionHydrated()
		}
		// Emit Info after session hydration so the text isn't
		// wiped by the hydration's assembler reset.
		if cmd.Text != "" {
			s.emit(Event{Kind: EventInfo, Text: cmd.Text, LocalResult: protocolLocalResult(cmd.LocalResult)})
		}
		if cmd.Turn == nil {
			s.emit(Event{Kind: EventTurnDone, LastResponse: cmd.Text})
			return true
		}
		state.line = cmd.Turn.Input
		state.hiddenInput = cmd.Turn.Hidden
		state.turnOptions = agent.RunOptions{
			HiddenInput:        cmd.Turn.Hidden,
			ReadOnly:           cmd.Turn.ReadOnly,
			ShellAllowPrefixes: append([]string(nil), cmd.Turn.ShellAllowPrefixes...),
		}
		state.skipHooks = cmd.Turn.SkipUserPromptHooks
		state.skipSkillInjection = cmd.Turn.SkipSkillInjection
	}
	return false
}

func (s *Service) handleSubmitLocalCommand(state *submitState) bool {
	cmd, err := s.app.ExecuteLocalCommand(state.line)
	if err != nil {
		s.emit(Event{Kind: EventError, Text: err.Error()})
		s.emit(Event{Kind: EventTurnDone})
		return true
	}
	if cmd.Handled {
		if cmd.HydrateSession {
			s.emitSessionHydrated()
		}
		if cmd.Text != "" {
			s.emit(Event{Kind: EventInfo, Text: cmd.Text, LocalResult: protocolLocalResult(cmd.LocalResult)})
		}
		if cmd.Turn == nil {
			s.emit(Event{Kind: EventTurnDone, LastResponse: cmd.Text})
			return true
		}
		state.line = cmd.Turn.Input
		state.hiddenInput = cmd.Turn.Hidden
		state.turnOptions = agent.RunOptions{
			HiddenInput:        cmd.Turn.Hidden,
			ReadOnly:           cmd.Turn.ReadOnly,
			ShellAllowPrefixes: append([]string(nil), cmd.Turn.ShellAllowPrefixes...),
		}
		state.skipHooks = cmd.Turn.SkipUserPromptHooks
		state.skipSkillInjection = cmd.Turn.SkipSkillInjection
	}
	if !cmd.Handled && appcommands.LooksLikeSlashCommand(state.line) {
		s.emit(Event{Kind: EventError, Text: fmt.Sprintf("• Unrecognized command %q. Type \"/\" for a list of supported commands.", state.line)})
		s.emit(Event{Kind: EventTurnDone})
		return true
	}
	return false
}

func (s *Service) applySubmitHooks(state *submitState) bool {
	if state.skipHooks {
		return false
	}
	blocked, out, updated := s.app.RunUserPromptSubmitHook(state.line)
	state.line = updated
	if out != "" {
		s.emit(Event{Kind: EventInfo, Text: out})
	}
	if blocked {
		if out == "" {
			out = "blocked by UserPromptSubmit hook"
		}
		s.emit(Event{Kind: EventTurnDone, LastResponse: out})
		return true
	}
	return false
}

func (s *Service) startSubmitTurn(state *submitState) {
	if state.hiddenInput || state.skipSkillInjection {
		s.goTracked(func() { s.runTurnWithOptions(state.line, state.turnOptions) })
		return
	}
	skillMention, skillOut, skillSynthetic, err := s.app.BuildSkillMentionSyntheticPromptWithBinding(state.line, state.skillBinding)
	if err != nil {
		s.emit(Event{Kind: EventError, Text: err.Error()})
		s.emit(Event{Kind: EventTurnDone})
		return
	}
	if skillMention {
		if skillOut != "" {
			s.emit(Event{Kind: EventSkillLoaded, Text: skillOut})
		}
		s.goTracked(func() { s.runInjectedTurn(state.line, skillSynthetic) })
		return
	}
	s.goTracked(func() { s.runTurn(state.line, state.hiddenInput) })
}
