package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/runtime/protocol"
)

type worktreeExitOption struct {
	label       string
	action      string
	description string
}

func (m model) worktreeExitOptions() []worktreeExitOption {
	summary := m.worktreeExit.summary
	removeDescription := "Clean up the worktree directory."
	if summary.ChangedFiles > 0 || summary.IgnoredFiles > 0 || summary.Commits > 0 {
		removeDescription = "Discard this checkout and its changes. The conversation can still be resumed."
	}
	return []worktreeExitOption{
		{label: "Keep worktree", action: "keep", description: "Leave it on disk so work can continue there."},
		{label: "Remove worktree", action: "remove", description: removeDescription},
		{label: "Cancel exit", action: "cancel", description: "Return to this Whale session."},
	}
}

func (m *model) handleWorktreeExitKey(msg tea.KeyMsg) tea.Cmd {
	options := m.worktreeExitOptions()
	switch msg.String() {
	case "up", "k":
		if m.worktreeExit.selected > 0 {
			m.worktreeExit.selected--
		} else {
			m.worktreeExit.selected = len(options) - 1
		}
	case "down", "j", "tab":
		m.worktreeExit.selected = (m.worktreeExit.selected + 1) % len(options)
	case "esc":
		m.mode = modeChat
		m.status = "exit canceled"
		m.dispatchIntent(protocol.Intent{Kind: protocol.IntentWorktreeExitChoice, WorktreeAction: "cancel"})
	case "enter":
		action := options[m.worktreeExit.selected].action
		m.mode = modeChat
		switch action {
		case "keep":
			m.status = "keeping worktree"
		case "remove":
			m.status = "removing worktree"
		default:
			m.status = "exit canceled"
		}
		m.dispatchIntent(protocol.Intent{Kind: protocol.IntentWorktreeExitChoice, WorktreeAction: action})
	}
	return nil
}

func (m model) renderWorktreeExit() string {
	summary := m.worktreeExit.summary
	lines := []string{
		pickerTitle("Exiting worktree session"),
		"",
		pickerStateLine("worktree", summary.Session.Name, "text"),
		pickerStateLine("branch", valueOrDash(summary.Session.Branch), "text"),
		pickerStateLine("path", valueOrDash(summary.Session.Path), "text"),
		"",
		pickerHint(worktreeExitSummaryText(summary.ChangedFiles, summary.IgnoredFiles, summary.Commits)),
		"",
	}
	for i, option := range m.worktreeExitOptions() {
		lines = append(lines, pickerRow(option.label, i == m.worktreeExit.selected, false))
		lines = append(lines, pickerHint("    "+option.description))
	}
	lines = append(lines, "", pickerHint("(up/down choose, enter confirm, esc cancel)"))
	return strings.Join(lines, "\n")
}

func worktreeExitSummaryText(changedFiles, ignoredFiles, commits int) string {
	parts := []string{}
	if changedFiles > 0 {
		parts = append(parts, fmt.Sprintf("%d uncommitted %s", changedFiles, plural(changedFiles, "file", "files")))
	}
	if ignoredFiles > 0 {
		parts = append(parts, fmt.Sprintf("%d ignored %s", ignoredFiles, plural(ignoredFiles, "file", "files")))
	}
	if commits > 0 {
		parts = append(parts, fmt.Sprintf("%d %s on the worktree branch", commits, plural(commits, "commit", "commits")))
	}
	if len(parts) == 0 {
		return "No worktree changes were detected."
	}
	return "Removing will discard " + strings.Join(parts, " and ") + "."
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func valueOrDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}
