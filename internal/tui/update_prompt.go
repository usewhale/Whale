package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/build"
	"github.com/usewhale/whale/internal/updatecheck"
)

type updatePromptOutcome int

const (
	updatePromptContinue updatePromptOutcome = iota
	updatePromptRun
	updatePromptInterrupt
)

type updateSelection int

const (
	updateSelectionNow updateSelection = iota
	updateSelectionSkip
	updateSelectionDismiss
	updateSelectionInterrupt
)

type updatePromptModel struct {
	result      updatecheck.Result
	highlighted updateSelection
	selection   updateSelection
	done        bool
	width       int
}

func runUpdatePromptIfNeeded(ctx context.Context, cfg app.Config) (updatePromptOutcome, updatecheck.Action, error) {
	checker := updateChecker(cfg)
	// Read the cached result first so an in-flight background refresh can't
	// rewrite version.json mid-read and cause this launch to silently skip the
	// prompt. Then kick off the refresh so the next launch sees fresh data —
	// keeping a slow/blocked api.github.com off the startup path.
	result, ok := checker.CachedUpgradeVersion()
	checker.RefreshIfStaleAsync()
	if !ok {
		return updatePromptContinue, updatecheck.Action{}, nil
	}
	model := newUpdatePromptModel(result)
	finalModel, err := tea.NewProgram(model).Run()
	if err != nil {
		return updatePromptContinue, updatecheck.Action{}, err
	}
	prompt, ok := finalModel.(updatePromptModel)
	if !ok {
		return updatePromptContinue, updatecheck.Action{}, nil
	}
	switch prompt.selection {
	case updateSelectionNow:
		return updatePromptRun, result.UpdateAction, nil
	case updateSelectionDismiss:
		_ = checker.Dismiss(result.LatestVersion)
		return updatePromptContinue, updatecheck.Action{}, nil
	case updateSelectionInterrupt:
		return updatePromptInterrupt, updatecheck.Action{}, nil
	default:
		return updatePromptContinue, updatecheck.Action{}, nil
	}
}

func updateChecker(cfg app.Config) updatecheck.Checker {
	exe, _ := os.Executable()
	return updatecheck.Checker{
		DataDir:        cfg.DataDir,
		CurrentVersion: build.CurrentVersion(),
		Enabled:        cfg.CheckForUpdateOnStartup,
		Goos:           runtime.GOOS,
		ExecutablePath: exe,
	}
}

func runUpdateAction(action updatecheck.Action) error {
	return runUpdateActionWithIO(action, os.Stdin, os.Stdout, os.Stderr)
}

func runUpdateActionWithIO(action updatecheck.Action, stdin io.Reader, stdout, stderr io.Writer) error {
	if action.ManualOnly {
		fmt.Fprintf(stdout, "\nWhale update command (run after Whale exits):\n  %s\n", action.String())
		return nil
	}
	fmt.Fprintf(stdout, "\nUpdating Whale via `%s`...\n", action.String())
	cmd := exec.Command(action.Cmd, action.Args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func newUpdatePromptModel(result updatecheck.Result) updatePromptModel {
	return updatePromptModel{
		result:      result,
		highlighted: updateSelectionSkip,
		selection:   updateSelectionSkip,
		width:       80,
	}
}

func (m updatePromptModel) Init() tea.Cmd {
	return nil
}

func (m updatePromptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.selection = updateSelectionInterrupt
			m.done = true
			return m, tea.Quit
		case "esc":
			m.selection = updateSelectionSkip
			m.done = true
			return m, tea.Quit
		case "up", "k":
			m.highlighted = m.highlighted.prev()
		case "down", "j":
			m.highlighted = m.highlighted.next()
		case "1":
			m.selection = updateSelectionNow
			m.done = true
			return m, tea.Quit
		case "2":
			m.selection = updateSelectionSkip
			m.done = true
			return m, tea.Quit
		case "3":
			m.selection = updateSelectionDismiss
			m.done = true
			return m, tea.Quit
		case "enter":
			m.selection = m.highlighted
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m updatePromptModel) View() string {
	if m.done {
		return ""
	}
	width := m.width
	if width < 44 {
		width = 44
	}
	if width > 88 {
		width = 88
	}
	contentWidth := width - 6
	if contentWidth < 32 {
		contentWidth = 32
	}
	title := lipgloss.NewStyle().Bold(true).Render(
		fmt.Sprintf("Update available! %s -> %s", m.result.CurrentVersion, m.result.LatestVersion),
	)
	lines := []string{
		title,
		"",
		lipgloss.NewStyle().Faint(true).Render("Release notes: " + m.result.ReleaseNotesURL),
		"",
		m.option(updateSelectionNow, m.updateNowLabel()),
		m.option(updateSelectionSkip, "2. Skip"),
		m.option(updateSelectionDismiss, "3. Skip until next version"),
		"",
		lipgloss.NewStyle().Faint(true).Render("Use ↑/↓ to choose, Enter to confirm the highlighted option"),
	}
	body := strings.Join(lines, "\n")
	return "\n" + lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("86")).
		Padding(1, 2).
		Width(contentWidth).
		Render(body) + "\n"
}

func (m updatePromptModel) updateNowLabel() string {
	if m.result.UpdateAction.ManualOnly {
		return "1. Show update command (run after Whale exits)"
	}
	return "1. Update now (runs `" + m.result.UpdateAction.String() + "`)"
}

func (m updatePromptModel) option(selection updateSelection, text string) string {
	if m.highlighted == selection {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("86")).Bold(true).Render("› " + text)
	}
	return "  " + text
}

func (s updateSelection) next() updateSelection {
	switch s {
	case updateSelectionNow:
		return updateSelectionSkip
	case updateSelectionSkip:
		return updateSelectionDismiss
	default:
		return updateSelectionNow
	}
}

func (s updateSelection) prev() updateSelection {
	switch s {
	case updateSelectionNow:
		return updateSelectionDismiss
	case updateSelectionSkip:
		return updateSelectionNow
	default:
		return updateSelectionSkip
	}
}
