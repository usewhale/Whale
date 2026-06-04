package tui

import (
	"fmt"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"

	tuirender "github.com/usewhale/whale/internal/tui/render"
)

var pasteClipboardImageToTempPNG = pasteClipboardImageToTempPNGImpl
var clipboardImagePasteSupported = runtime.GOOS == "darwin"

func isClipboardImagePasteKey(msg tea.KeyMsg) bool {
	return clipboardImagePasteSupported && (msg.String() == "ctrl+v" || msg.String() == "alt+v")
}

func (m *model) handleClipboardImagePaste() tea.Cmd {
	path, err := pasteClipboardImageToTempPNG()
	if err != nil {
		m.appendTranscript("local_status", tuirender.KindLocalStatus, fmt.Sprintf("Failed to paste image: %v", err))
		m.status = "image paste failed"
		m.refreshViewportContentFollow(true)
		return nil
	}
	return m.attachComposerFile(path, "clipboard.png")
}
