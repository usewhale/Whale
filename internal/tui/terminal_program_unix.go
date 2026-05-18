//go:build !windows

package tui

import (
	"os"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

func newTerminalProgram(m tea.Model) (*tea.Program, func(), error) {
	input, err := openEnhancedTerminalInput()
	if err != nil {
		return nil, nil, err
	}
	restoreKeyboard := enableTerminalKeyboardEnhancements(os.Stdout)
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			restoreKeyboard()
			_ = input.Close()
		})
	}
	return tea.NewProgram(m, tea.WithInput(input)), cleanup, nil
}
