//go:build windows

package tui

import (
	"os"
)

func openEnhancedTerminalInput() (*enhancedTerminalInput, error) {
	// Bubble Tea v1 uses its Windows coninput reader only when the input fd is
	// exactly os.Stdin. Duplicating stdin or opening CONIN$ makes it fall back
	// to byte-oriented ANSI input, which does not receive normal console keys.
	return newEnhancedTerminalInput(os.Stdin, false), nil
}
