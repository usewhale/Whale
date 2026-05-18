package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/session"
)

func (a *App) HandleSlash(line string) (handled bool, output string, synthetic string, shouldExit bool, clearScreen bool, err error) {
	cmdResult, cmdErr := handleCommand(line, a.sessionID, time.Now())
	if cmdErr != nil {
		return true, "", "", false, false, cmdErr
	}
	if !cmdResult.Handled {
		return false, "", "", false, false, nil
	}
	if cmdResult.ClearScreen {
		return true, "▸ terminal cleared — context is intact — use /new to start fresh", "", false, true, nil
	}
	if cmdResult.ShowStatus {
		return true, a.buildStatus(), "", false, false, nil
	}
	if cmdResult.InitMemory {
		line, err := a.initMemory()
		if err != nil || line != "" {
			return true, line, "", false, false, err
		}
		return true, "Initializing AGENTS.md from repository context...", buildInitSyntheticPrompt(), false, false, nil
	}
	if cmdResult.ShowSkills {
		return true, a.buildSkillsList(), "", false, false, nil
	}
	if cmdResult.Mode != "" {
		mode, err := session.ParseMode(cmdResult.Mode)
		if err != nil {
			return true, "", "", false, false, err
		}
		msg, err := a.SetMode(mode)
		if err != nil {
			return true, "", "", false, false, err
		}
		if cmdResult.Output == "" {
			cmdResult.Output = msg
		}
	}
	if cmdResult.Output != "" {
		output = cmdResult.Output
	}
	if cmdResult.AskPrompt != "" {
		synthetic = cmdResult.AskPrompt
	}
	if cmdResult.PlanPrompt != "" {
		synthetic = cmdResult.PlanPrompt
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
			return true, "", "", false, false, err
		}
		a.currentMode = modeState.Mode
		a.a = nil
		output = fmt.Sprintf("new session: %s", cmdResult.SessionID)
		if oldMsgCount > 0 {
			output += fmt.Sprintf("\n▸ dropped %d message(s) from session %s", oldMsgCount, oldID)
		} else {
			output += fmt.Sprintf("\n▸ previous session: %s", oldID)
		}
		output += fmt.Sprintf("\n▸ to resume the previous session, run: whale resume %s", oldID)
		output += fmt.Sprintf("\nmode: %s", a.currentMode)
		if _, err := session.PatchSessionMeta(a.sessionsDir, a.sessionID, session.SessionMeta{Workspace: a.workspaceRoot, Branch: a.branch}); err != nil {
			return true, "", "", false, false, err
		}
	}
	return true, output, synthetic, cmdResult.ShouldExit, false, nil
}

func (a *App) HandleLocalCommand(line string) (handled bool, output string, err error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "/mcp" {
		return true, a.buildMCPStatus(), nil
	}
	if trimmed == "/plugins" || strings.HasPrefix(trimmed, "/plugins ") {
		out, err := a.handlePluginsCommand(trimmed)
		return true, out, err
	}
	if trimmed == "/stats" {
		return true, a.buildStats(), nil
	}
	if a.pluginManager != nil {
		res, handled, err := a.pluginManager.HandleCommand(a.ctx, trimmed)
		if handled || err != nil {
			if res.Mutated {
				a.a = nil
			}
			return handled, res.Text, err
		}
		if pluginID := pluginCommandID(trimmed); pluginID != "" {
			if st, ok := a.pluginManager.Status(pluginID); ok && !st.Enabled {
				return true, pluginID + " plugin is disabled", nil
			}
		}
	}
	if strings.HasPrefix(trimmed, "/stats ") {
		fields := strings.Fields(trimmed)
		if len(fields) == 2 {
			switch fields[1] {
			case "usage", "tools", "repair", "recent", "all":
				return true, a.buildStatsView(fields[1]), nil
			}
		}
		return true, "", errors.New("usage: /stats [usage|tools|repair|recent|all]")
	}
	if strings.HasPrefix(line, "/compact") {
		fields := strings.Fields(line)
		if len(fields) != 1 || fields[0] != "/compact" {
			return true, "", errors.New("usage: /compact")
		}
		ag, err := a.ensureAgent()
		if err != nil {
			return true, "", err
		}
		info, err := ag.CompactSession(a.ctx, a.sessionID)
		if err != nil {
			return true, "", err
		}
		a.a = nil
		if !info.Compacted {
			return true, "nothing to compact", nil
		}
		return true, fmt.Sprintf("compacted conversation: %d -> %d messages; ~%d -> ~%d tokens", info.MessagesBefore, info.MessagesAfter, info.BeforeEstimate, info.AfterEstimate), nil
	}
	return false, "", nil
}

func pluginCommandID(line string) string {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return ""
	}
	switch fields[0] {
	case "/memory":
		return "memory"
	case "/skills-improver":
		return "skills-improver"
	case "/local-indexer":
		return "local-indexer"
	default:
		return ""
	}
}
