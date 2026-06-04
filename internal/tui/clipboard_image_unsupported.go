//go:build !darwin

package tui

import "fmt"

func pasteClipboardImageToTempPNGImpl() (string, error) {
	return "", fmt.Errorf("clipboard image paste is not supported on this platform")
}
