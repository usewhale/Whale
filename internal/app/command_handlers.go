package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/plugins"
	"github.com/usewhale/whale/internal/session"
)

type CommandExecution struct {
	Handled     bool
	Text        string
	Turn        *plugins.CommandTurn
	ShouldExit  bool
	ClearScreen bool
	Mutated     bool
}

func (a *App) HandleSlash(line string) (handled bool, output string, synthetic string, shouldExit bool, clearScreen bool, err error) {
	res, err := a.ExecuteSlash(line)
	if res.Turn != nil {
		synthetic = res.Turn.Input
	}
	return res.Handled, res.Text, synthetic, res.ShouldExit, res.ClearScreen, err
}

func (a *App) ExecuteSlash(line string) (CommandExecution, error) {
	cmdResult, cmdErr := handleCommand(line, a.sessionID, time.Now())
	if cmdErr != nil {
		return CommandExecution{Handled: true}, cmdErr
	}
	if !cmdResult.Handled {
		return CommandExecution{}, nil
	}
	if cmdResult.ClearScreen {
		return CommandExecution{Handled: true, ClearScreen: true}, nil
	}
	if cmdResult.ShowStatus {
		return CommandExecution{Handled: true, Text: a.buildStatus()}, nil
	}
	if cmdResult.InitMemory {
		line, err := a.initMemory()
		if err != nil || line != "" {
			return CommandExecution{Handled: true, Text: line}, err
		}
		return CommandExecution{Handled: true, Text: "Initializing AGENTS.md from repository context...", Turn: &plugins.CommandTurn{
			Input:               buildInitSyntheticPrompt(),
			Hidden:              true,
			SkipUserPromptHooks: true,
			SkipSkillInjection:  true,
		}}, nil
	}
	if cmdResult.ShowSkills {
		return CommandExecution{Handled: true, Text: a.buildSkillsList()}, nil
	}
	if cmdResult.BtwQuestion != "" {
		return CommandExecution{Handled: true}, errors.New("/btw is only available in the interactive TUI")
	}
	if cmdResult.ReviewPrompt != "" {
		return CommandExecution{Handled: true, Turn: &plugins.CommandTurn{
			Input:               cmdResult.ReviewPrompt,
			Hidden:              true,
			ReadOnly:            true,
			SkipUserPromptHooks: true,
			SkipSkillInjection:  true,
			ShellAllowPrefixes:  append([]string(nil), cmdResult.AllowShellPrefixes...),
		}}, nil
	}
	if strings.TrimSpace(cmdResult.ForkName) != "" || trimmedCommandHead(line) == "/fork" {
		msg, err := a.forkCurrentSession(cmdResult.ForkName)
		return CommandExecution{Handled: true, Text: msg}, err
	}
	out := CommandExecution{Handled: true, ShouldExit: cmdResult.ShouldExit}
	if cmdResult.Mode != "" {
		mode, err := session.ParseMode(cmdResult.Mode)
		if err != nil {
			return out, err
		}
		msg, err := a.SetMode(mode)
		if err != nil {
			return out, err
		}
		if cmdResult.Output == "" {
			cmdResult.Output = msg
		}
	}
	if cmdResult.Output != "" {
		out.Text = cmdResult.Output
	}
	if cmdResult.AskPrompt != "" {
		out.Turn = &plugins.CommandTurn{Input: cmdResult.AskPrompt}
	}
	if cmdResult.PlanPrompt != "" {
		out.Turn = &plugins.CommandTurn{Input: cmdResult.PlanPrompt}
	}
	// For /new: capture old session info before switching.
	oldID := a.sessionID
	oldMsgCount := 0
	trimmed := strings.TrimSpace(line)
	fields := strings.Fields(trimmed)
	isNewCommand := len(fields) > 0 && fields[0] == "/new"
	if isNewCommand {
		if msgs, err := a.msgStore.List(a.ctx, a.sessionID); err == nil {
			oldMsgCount = len(msgs)
		}
	}
	a.sessionID = cmdResult.SessionID
	if isNewCommand {
		modeState, err := session.LoadModeState(a.sessionsDir, a.sessionID)
		if err != nil {
			return out, err
		}
		a.currentMode = modeState.Mode
		a.a = nil
		out.Text = fmt.Sprintf("New session\n\nsession:  %s", cmdResult.SessionID)
		if oldMsgCount > 0 {
			out.Text += fmt.Sprintf("\ndropped:  %d message(s) from %s", oldMsgCount, oldID)
		} else {
			out.Text += fmt.Sprintf("\nprevious: %s", oldID)
		}
		out.Text += fmt.Sprintf("\nresume:   whale resume %s", oldID)
		out.Text += fmt.Sprintf("\nmode:     %s", a.currentMode)
		if _, err := session.PatchSessionMeta(a.sessionsDir, a.sessionID, a.baseSessionMeta()); err != nil {
			return out, err
		}
	}
	return out, nil
}

func trimmedCommandHead(line string) string {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func (a *App) HandleLocalCommand(line string) (handled bool, output string, synthetic string, err error) {
	res, err := a.ExecuteLocalCommand(line)
	if res.Turn != nil {
		synthetic = res.Turn.Input
	}
	return res.Handled, res.Text, synthetic, err
}

func (a *App) ExecuteLocalCommand(line string) (CommandExecution, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "/mcp" {
		return CommandExecution{Handled: true, Text: a.buildMCPStatus()}, nil
	}
	if trimmed == "/help" {
		return CommandExecution{Handled: true, Text: BuildHelpText()}, nil
	}
	if strings.HasPrefix(trimmed, "/help ") {
		return CommandExecution{Handled: true}, errors.New("usage: /help")
	}
	if trimmed == "/feedback" {
		return CommandExecution{Handled: true, Text: openFeedbackIssues()}, nil
	}
	if trimmed == "/worktree" || strings.HasPrefix(trimmed, "/worktree ") {
		fields := strings.Fields(trimmed)
		args := fields[1:]
		if len(args) > 0 && args[0] == "remove" {
			text, err := a.removeWorktree(args)
			return CommandExecution{Handled: true, Text: text, Mutated: err == nil}, err
		}
		text, err := a.buildWorktreeStatus(args)
		return CommandExecution{Handled: true, Text: text}, err
	}
	if trimmed == "/focus" {
		mode, err := a.ToggleViewMode()
		if err != nil {
			return CommandExecution{Handled: true}, err
		}
		return CommandExecution{Handled: true, Text: ViewModeToggleMessage(mode)}, nil
	}
	if strings.HasPrefix(trimmed, "/plugins ") {
		return CommandExecution{Handled: true}, errors.New("usage: /plugins")
	}
	if trimmed == "/stats" {
		return CommandExecution{Handled: true, Text: a.buildStats()}, nil
	}
	if a.pluginManager != nil {
		res, handled, err := a.pluginManager.HandleCommand(a.ctx, trimmed)
		if handled || err != nil {
			if res.Mutated {
				a.a = nil
			}
			return CommandExecution{Handled: handled, Text: res.Text, Turn: res.Turn, Mutated: res.Mutated}, err
		}
		if pluginID := pluginCommandID(trimmed); pluginID != "" {
			if st, ok := a.pluginManager.Status(pluginID); ok && !st.Enabled {
				return CommandExecution{Handled: true, Text: pluginID + " plugin is disabled"}, nil
			}
		}
	}
	if strings.HasPrefix(trimmed, "/stats ") {
		fields := strings.Fields(trimmed)
		if len(fields) == 2 {
			switch fields[1] {
			case "usage", "tools", "repair", "recent", "all":
				return CommandExecution{Handled: true, Text: a.buildStatsView(fields[1])}, nil
			}
		}
		return CommandExecution{Handled: true}, errors.New("usage: /stats [usage|tools|repair|recent|all]")
	}
	if strings.HasPrefix(line, "/compact") {
		fields := strings.Fields(line)
		if len(fields) != 1 || fields[0] != "/compact" {
			return CommandExecution{Handled: true}, errors.New("usage: /compact")
		}
		ag, err := a.ensureAgent()
		if err != nil {
			return CommandExecution{Handled: true}, err
		}
		info, err := ag.CompactSession(a.ctx, a.sessionID)
		if err != nil {
			return CommandExecution{Handled: true}, err
		}
		a.a = nil
		if !info.Compacted {
			return CommandExecution{Handled: true, Text: "nothing to compact"}, nil
		}
		return CommandExecution{Handled: true, Text: fmt.Sprintf("compacted conversation: %d -> %d messages; ~%d -> ~%d tokens", info.MessagesBefore, info.MessagesAfter, info.BeforeEstimate, info.AfterEstimate)}, nil
	}
	return CommandExecution{}, nil
}

func pluginCommandID(line string) string {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return ""
	}
	switch fields[0] {
	case "/memory":
		return "memory"
	default:
		return ""
	}
}
