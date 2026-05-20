package tui

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const gitBranchLookupTimeout = 5 * time.Second

type gitBranchUpdatedMsg struct {
	cwd    string
	branch string
}

func detectGitBranchCmd(cwd string) tea.Cmd {
	if strings.TrimSpace(cwd) == "" {
		return nil
	}
	return func() tea.Msg {
		return gitBranchUpdatedMsg{
			cwd:    cwd,
			branch: detectGitBranch(cwd),
		}
	}
}

func toolResultMayChangeGitBranch(toolName string) bool {
	switch toolName {
	case "shell_run", "shell_wait":
		return true
	default:
		return false
	}
}

func detectGitBranch(cwd string) string {
	ctx, cancel := context.WithTimeout(context.Background(), gitBranchLookupTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "branch", "--show-current")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
