//go:build !windows

package tui

import (
	"fmt"
	"os"

	"github.com/charmbracelet/x/term"
)

func openEnhancedTerminalInput() (*enhancedTerminalInput, error) {
	if term.IsTerminal(os.Stdin.Fd()) {
		return newEnhancedTerminalInput(os.Stdin, false), nil
	}

	file, err := os.Open("/dev/tty")
	if err != nil {
		return nil, fmt.Errorf("could not open a new TTY: %w", err)
	}
	return newEnhancedTerminalInput(file, true), nil
}
