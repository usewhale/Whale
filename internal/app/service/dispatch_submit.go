package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/attachments"
	appcommands "github.com/usewhale/whale/internal/commands"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/session"
)

func (s *Service) handleSubmit(line string, hiddenInput bool, skillBinding *app.SkillBinding, clientInputID string, attachmentInputs []AttachmentInput) {
	state := submitState{
		line:             strings.TrimSpace(line),
		clientInputID:    clientInputID,
		hiddenInput:      hiddenInput,
		turnOptions:      agent.RunOptions{HiddenInput: hiddenInput},
		skillBinding:     skillBinding,
		attachmentInputs: append([]AttachmentInput(nil), attachmentInputs...),
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
	clientInputID      string
	hiddenInput        bool
	prevSessionID      string
	skipHooks          bool
	skipSkillInjection bool
	turnOptions        agent.RunOptions
	skillBinding       *app.SkillBinding
	attachmentInputs   []AttachmentInput
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
		s.emit(Event{Kind: EventPluginsManagerUpdated, Plugins: protocolPlugins(s.PluginsForManager()), Open: true})
		return true
	case "/config":
		s.emitConfigManagerUpdated(true)
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
			GoalContinuation:   cmd.Turn.GoalContinuation,
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
			GoalContinuation:   cmd.Turn.GoalContinuation,
			ShellAllowPrefixes: append([]string(nil), cmd.Turn.ShellAllowPrefixes...),
		}
		state.skipHooks = cmd.Turn.SkipUserPromptHooks
		state.skipSkillInjection = cmd.Turn.SkipSkillInjection
	}
	if !cmd.Handled && appcommands.LooksLikeSlashCommand(state.line) {
		s.emit(Event{Kind: EventError, Text: fmt.Sprintf("• Unrecognized command %s. Type \"/\" for a list of supported commands.", firstCommandWord(state.line))})
		s.emit(Event{Kind: EventTurnDone})
		return true
	}
	return false
}

func (s *Service) applySubmitHooks(state *submitState) bool {
	if state.skipHooks {
		return false
	}
	blocked, out, updated := s.app.RunUserPromptSubmitHookWithObserver(state.line, s.hookObserver())
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
	if len(state.attachmentInputs) > 0 {
		parts, err := s.prepareAttachmentParts(state.line, state.attachmentInputs)
		if err != nil {
			s.emit(Event{Kind: EventError, Text: err.Error()})
			s.emit(Event{Kind: EventTurnDone})
			return
		}
		if state.hiddenInput || state.skipSkillInjection {
			s.goTracked(func() { s.runTurnWithContentOptions(parts, state.turnOptions) })
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
			s.goTracked(func() { s.runInjectedTurnWithContentOptions(parts, skillSynthetic, state.turnOptions) })
			return
		}
		s.goTracked(func() { s.runTurnWithContentOptions(parts, state.turnOptions) })
		return
	}
	if state.hiddenInput || state.skipSkillInjection {
		if s.injectActiveTurn(state.line, state.turnOptions, state.clientInputID) {
			return
		}
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
		if s.injectActiveTurnWithHidden(state.line, skillSynthetic, agent.RunOptions{}, state.clientInputID) {
			return
		}
		s.goTracked(func() { s.runInjectedTurn(state.line, skillSynthetic) })
		return
	}
	if s.injectActiveTurn(state.line, state.turnOptions, state.clientInputID) {
		return
	}
	s.goTracked(func() { s.runTurn(state.line, state.hiddenInput) })
}

func (s *Service) prepareAttachmentParts(line string, inputs []AttachmentInput) ([]core.MessagePart, error) {
	sources := make([]attachments.Source, 0, len(inputs))
	for _, input := range inputs {
		if strings.TrimSpace(input.Path) == "" {
			continue
		}
		sources = append(sources, attachments.Source{Path: input.Path, DisplayName: input.DisplayName})
	}
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	parts, _, err := attachments.PrepareMessageParts(ctx, line, sources, attachments.Options{
		SessionsDir:   s.app.SessionsDir(),
		SessionID:     s.app.SessionID(),
		WorkspaceRoot: s.app.WorkspaceRoot(),
	})
	return parts, err
}

func firstCommandWord(line string) string {
	line = strings.TrimSpace(line)
	if idx := strings.IndexAny(line, " \t\n"); idx > 0 {
		return line[:idx]
	}
	return line
}
