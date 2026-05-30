package app

import (
	"fmt"
	appcommands "github.com/usewhale/whale/internal/commands"
	"github.com/usewhale/whale/internal/session"
	"strings"
	"time"
)

func resolveInitialSessionID(sessionsDir string) (string, error) {
	sessions, err := session.ListSessions(sessionsDir, 1)
	if err != nil {
		return "", err
	}
	if len(sessions) > 0 && strings.TrimSpace(sessions[0].ID) != "" {
		return sessions[0].ID, nil
	}
	return "default", nil
}

func newSessionID(now time.Time) string {
	return appcommands.NewSessionID(now)
}

func resolveCLIResumeID(args []string) (string, bool, error) {
	if len(args) == 0 {
		return "", false, nil
	}
	if args[0] != "resume" {
		return "", false, nil
	}
	if len(args) != 2 || strings.TrimSpace(args[1]) == "" {
		return "", true, fmt.Errorf("usage: whale resume <id>")
	}
	return strings.TrimSpace(args[1]), true, nil
}

func handleCommand(line, currentSessionID string, now time.Time) (appcommands.Result, error) {
	return appcommands.Parse(line, currentSessionID, now)
}

func planPromptFromSlash(line string) (string, bool) {
	return appcommands.PlanPromptFromSlash(line)
}
