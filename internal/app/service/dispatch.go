package service

import (
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/app"
	appcommands "github.com/usewhale/whale/internal/app/commands"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
)

func (s *Service) Dispatch(in Intent) {
	switch in.Kind {
	case IntentSubmit:
		s.goTracked(func() { s.handleSubmit(in.Input, in.HiddenInput, in.SkillBinding) })
	case IntentSubmitLocal:
		s.enqueueLocalSubmit(in.Input)
	case IntentAllowTool:
		s.resolveApproval(in.ToolCallID, policy.ApprovalAllow)
	case IntentAllowToolForSession:
		s.resolveApproval(in.ToolCallID, policy.ApprovalAllowForSession)
	case IntentDenyTool:
		s.resolveApproval(in.ToolCallID, policy.ApprovalDeny)
	case IntentCancelToolApproval:
		s.resolveApproval(in.ToolCallID, policy.ApprovalCancel)
	case IntentSubmitUserInput:
		if in.UserInput != nil {
			s.resolveUserInput(in.ToolCallID, *in.UserInput, true)
		}
	case IntentCancelUserInput:
		s.resolveUserInput(in.ToolCallID, core.UserInputResponse{}, false)
	case IntentRequestSessions:
		s.emitSessionChoices()
	case IntentRequestExit:
		s.requestExit()
	case IntentSelectSession:
		res, err := s.app.ApplyResumeChoice(in.SessionInput)
		if err != nil {
			s.emit(Event{Kind: EventError, Text: err.Error()})
			return
		}
		s.emit(Event{Kind: EventInfo, Text: res.Message})
		if res.Resumed {
			s.emitSessionHydrated()
		} else {
			s.emitSessionChoices()
		}
	case IntentShutdown:
		s.cancelMu.Lock()
		if s.cancel != nil {
			s.cancel()
		}
		s.cancelMu.Unlock()
		s.cancelPendingInteractions()
	case IntentWorktreeExitChoice:
		s.handleWorktreeExitChoice(in.WorktreeAction)
	case IntentSetModelAndEffort:
		if err := s.app.SetModelAndEffort(in.Model, in.Effort); err != nil {
			s.emit(Event{Kind: EventError, Text: err.Error()})
			return
		}
		if strings.EqualFold(strings.TrimSpace(in.Thinking), "on") {
			s.app.SetThinkingEnabled(true)
		}
		if strings.EqualFold(strings.TrimSpace(in.Thinking), "off") {
			s.app.SetThinkingEnabled(false)
		}
		s.emit(Event{Kind: EventInfo, Text: fmt.Sprintf("model set: %s  effort: %s  thinking: %s", s.app.Model(), s.app.ReasoningEffort(), app.OnOff(s.app.ThinkingEnabled()))})
		s.emit(Event{Kind: EventTurnDone})
	case IntentSetApprovalMode:
		enabled := in.ApprovalMode == "auto_accept"
		s.app.SetAutoAcceptPermissions(enabled)
		s.emit(Event{Kind: EventInfo, Text: autoAcceptMessage(enabled), AutoAccept: enabled, AutoAcceptKnown: true})
		s.emit(Event{Kind: EventTurnDone})
	case IntentSetViewMode:
		if err := s.app.SetViewMode(in.ViewMode); err != nil {
			s.emit(Event{Kind: EventError, Text: err.Error()})
			return
		}
		s.emit(Event{Kind: EventViewModeChanged, ViewMode: s.app.ViewMode(), Text: app.ViewModeToggleMessage(s.app.ViewMode())})
		s.emit(Event{Kind: EventTurnDone})
	case IntentToggleMode:
		msg, err := s.app.ToggleMode()
		if err != nil {
			s.emit(Event{Kind: EventError, Text: err.Error()})
			return
		}
		s.emit(Event{Kind: EventInfo, Text: msg})
		s.emit(Event{Kind: EventTurnDone, LastResponse: msg})
	case IntentImplementPlan:
		out, err := s.app.SetMode(session.ModeAgent)
		if err != nil {
			s.emit(Event{Kind: EventError, Text: err.Error()})
			s.emit(Event{Kind: EventTurnDone})
			return
		}
		s.emit(Event{Kind: EventInfo, Text: out})
		s.goTracked(func() { s.runInjectedTurn("Implement the plan.", buildImplementPlanPrompt(in.Input)) })
	case IntentDeclinePlan:
		s.app.RecordPlanNotApproved()
		const msg = "Plan not approved; staying in Plan mode"
		s.emit(Event{Kind: EventInfo, Text: msg})
		s.emit(Event{Kind: EventTurnDone, LastResponse: msg})
	case IntentRequestSkillsManage:
		s.emit(Event{Kind: EventSkillsManager, Skills: s.SkillsForManager()})
	case IntentSetSkillEnabled:
		if _, err := s.app.SetSkillEnabled(in.SkillName, in.SkillEnabled); err != nil {
			s.emit(Event{Kind: EventError, Text: err.Error()})
			return
		}
		s.emit(Event{Kind: EventSkillsManager, Skills: s.SkillsForManager()})
	case IntentSetPluginEnabled:
		if _, err := s.app.SetPluginEnabled(in.PluginID, in.PluginEnabled); err != nil {
			s.emit(Event{Kind: EventError, Text: err.Error()})
			return
		}
		s.emit(Event{Kind: EventPluginsManager, Plugins: s.PluginsForManager()})
	}
}

func (s *Service) requestExit() {
	summary, ok, err := s.app.BuildWorktreeExitSummary()
	if !ok {
		s.emit(Event{Kind: EventExitRequested})
		return
	}
	if err != nil {
		res, clearErr := s.app.ForgetCurrentWorktree()
		if clearErr != nil {
			s.emit(Event{Kind: EventError, Text: err.Error() + "\n" + clearErr.Error()})
			s.emit(Event{Kind: EventExitRequested})
			return
		}
		s.emit(Event{Kind: EventInfo, Text: res.Message})
		s.emit(Event{Kind: EventExitRequested})
		return
	}
	if summary.ChangedFiles == 0 && summary.IgnoredFiles == 0 && summary.Commits == 0 {
		res, err := s.app.RemoveCurrentWorktree(false)
		if err != nil {
			s.emit(Event{Kind: EventError, Text: err.Error()})
			return
		}
		s.emit(Event{Kind: EventInfo, Text: res.Message})
		s.emit(Event{Kind: EventExitRequested})
		return
	}
	s.emit(Event{Kind: EventWorktreeExitPrompt, WorktreeExit: &summary})
}

func (s *Service) handleWorktreeExitChoice(action string) {
	switch strings.TrimSpace(action) {
	case "keep":
		res, err := s.app.KeepCurrentWorktree()
		if err != nil {
			s.emit(Event{Kind: EventError, Text: err.Error()})
			return
		}
		s.emit(Event{Kind: EventInfo, Text: res.Message})
		s.emit(Event{Kind: EventExitRequested})
	case "remove":
		res, err := s.app.RemoveCurrentWorktree(true)
		if err != nil {
			s.emit(Event{Kind: EventError, Text: err.Error()})
			return
		}
		s.emit(Event{Kind: EventInfo, Text: res.Message})
		s.emit(Event{Kind: EventExitRequested})
	case "cancel":
		s.emit(Event{Kind: EventInfo, Text: "Exit canceled"})
	default:
		s.emit(Event{Kind: EventError, Text: "unknown worktree exit action"})
	}
}

func buildImplementPlanPrompt(_ string) string {
	return strings.TrimSpace(`Implement the plan.

Before editing, initialize and maintain an update_plan checklist for the implementation work. Keep exactly one item in_progress while working and mark items completed as soon as they are done.`)
}

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
			Kind:            EventModelPicker,
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
		s.emit(Event{Kind: EventPermissionsMenu, AutoAccept: s.app.AutoAcceptPermissions(), AutoAcceptKnown: true})
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
		s.emit(Event{Kind: EventSkillsMenu})
		return
	}
	if line == "/plugins" {
		s.emit(Event{Kind: EventPluginsManager, Plugins: s.PluginsForManager()})
		return
	}
	if line == "/review" {
		s.emit(Event{Kind: EventReviewMenu})
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
			s.emit(Event{Kind: EventClearScreen})
		}
		if cmd.ShouldExit {
			s.requestExit()
		}
		if s.app.SessionID() != prevSessionID {
			s.emitSessionHydrated()
		}
		if cmd.Text != "" {
			ev := localSubmitResultEvent("info", cmd.Text)
			ev.LocalResult = cmd.LocalResult
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
			ev.LocalResult = cmd.LocalResult
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
			Kind:            EventModelPicker,
			ModelChoices:    s.app.SupportedModels(),
			EffortChoices:   s.app.SupportedEfforts(),
			CurrentModel:    s.app.Model(),
			CurrentEffort:   s.app.ReasoningEffort(),
			ThinkingChoices: []string{"on", "off"},
			CurrentThinking: app.OnOff(s.app.ThinkingEnabled()),
		})
		return true
	case "/permissions":
		s.emit(Event{Kind: EventPermissionsMenu, AutoAccept: s.app.AutoAcceptPermissions(), AutoAcceptKnown: true})
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
		s.emit(Event{Kind: EventSkillsMenu})
		return true
	case "/plugins":
		s.emit(Event{Kind: EventPluginsManager, Plugins: s.PluginsForManager()})
		return true
	case "/review":
		s.emit(Event{Kind: EventReviewMenu})
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
			s.emit(Event{Kind: EventClearScreen})
		}
		if cmd.ShouldExit {
			s.requestExit()
		}
		if s.app.SessionID() != state.prevSessionID {
			s.emitSessionHydrated()
		}
		// Emit Info after session hydration so the text isn't
		// wiped by the hydration's assembler reset.
		if cmd.Text != "" {
			s.emit(Event{Kind: EventInfo, Text: cmd.Text, LocalResult: cmd.LocalResult})
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
		if cmd.Text != "" {
			s.emit(Event{Kind: EventInfo, Text: cmd.Text, LocalResult: cmd.LocalResult})
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

func (s *Service) emitSessionHydrated() {
	msgs, err := s.app.ListMessages()
	if err != nil {
		s.emit(Event{Kind: EventError, Text: err.Error()})
		return
	}
	s.emit(Event{Kind: EventSessionHydrated, SessionID: s.app.SessionID(), Messages: msgs, AutoAccept: s.app.AutoAcceptPermissions(), AutoAcceptKnown: true})
}

func (s *Service) emitSessionChoices() bool {
	choices, err := s.app.ListResumeChoices(20)
	if err != nil {
		s.emit(Event{Kind: EventError, Text: err.Error()})
		return false
	}
	if len(choices) == 0 {
		s.emit(Event{Kind: EventInfo, Text: "no saved sessions"})
		return false
	}
	s.emit(Event{Kind: EventSessionsListed, Choices: choices})
	return true
}

func (s *Service) emitLocalSessionChoices() bool {
	choices, err := s.app.ListResumeChoices(20)
	if err != nil {
		s.emit(localSubmitResultEvent("error", err.Error()))
		return false
	}
	if len(choices) == 0 {
		s.emit(localSubmitResultEvent("info", "no saved sessions"))
		return false
	}
	s.emit(Event{Kind: EventSessionsListed, Choices: choices})
	return true
}

func localSubmitResultEvent(status, text string) Event {
	return Event{Kind: EventLocalSubmitResult, Text: text, Status: status, Metadata: map[string]any{EventMetadataLocalSubmit: true}}
}

func localSubmitDoneEvent() Event {
	return Event{Kind: EventLocalSubmitDone, Metadata: map[string]any{EventMetadataLocalSubmit: true}}
}

func btwQuestionFromLine(line string) (string, bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 || fields[0] != "/btw" {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "/btw")), true
}

func autoAcceptMessage(enabled bool) string {
	if enabled {
		return "Session auto-accept enabled"
	}
	return "Session auto-accept disabled"
}
