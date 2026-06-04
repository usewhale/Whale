package tui

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	appcommands "github.com/usewhale/whale/internal/runtime/commands"
	"github.com/usewhale/whale/internal/runtime/protocol"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

var errTestClipboardImage = errors.New("clipboard test error")

var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
	0x42, 0x60, 0x82,
}

func TestSubmitPromptSendsAndClearsPendingAttachments(t *testing.T) {
	var intents []protocol.Intent
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		width:     80,
		height:    24,
		dispatch:  func(in protocol.Intent) { intents = append(intents, in) },
		composerAttachments: []composerAttachment{
			{Placeholder: "[Attachment #1: note.txt]", Input: protocol.AttachmentInput{Path: "note.txt", DisplayName: "note.txt"}},
		},
	}
	_ = m.submitPrompt("[Attachment #1: note.txt]\ninspect this")
	if len(intents) != 1 {
		t.Fatalf("intents = %+v", intents)
	}
	got := intents[0]
	if got.Kind != protocol.IntentSubmit || got.Input != "[Attachment #1: note.txt]\ninspect this" {
		t.Fatalf("intent = %+v", got)
	}
	if len(got.Attachments) != 1 || got.Attachments[0].Path != "note.txt" {
		t.Fatalf("attachments = %+v", got.Attachments)
	}
	if len(m.composerAttachments) != 0 {
		t.Fatalf("composer attachments not cleared: %+v", m.composerAttachments)
	}
	if len(m.transcript) == 0 || !strings.Contains(m.transcript[0].Text, "[Attachment #1: note.txt]") {
		t.Fatalf("transcript = %+v", m.transcript)
	}
}

func TestDeletedComposerPlaceholderDoesNotSendAttachment(t *testing.T) {
	var intents []protocol.Intent
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		width:     80,
		height:    24,
		dispatch:  func(in protocol.Intent) { intents = append(intents, in) },
		composerAttachments: []composerAttachment{
			{Placeholder: "[Attachment #1: note.txt]", Input: protocol.AttachmentInput{Path: "note.txt", DisplayName: "note.txt"}},
		},
	}
	_ = m.submitPrompt("inspect this")
	if len(intents) != 1 {
		t.Fatalf("intents = %+v", intents)
	}
	if len(intents[0].Attachments) != 0 {
		t.Fatalf("attachments = %+v", intents[0].Attachments)
	}
	if len(m.composerAttachments) != 0 {
		t.Fatalf("composer attachments not cleared: %+v", m.composerAttachments)
	}
}

func TestLocalCommandDoesNotConsumeComposerAttachments(t *testing.T) {
	var intents []protocol.Intent
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		width:     80,
		height:    24,
		dispatch:  func(in protocol.Intent) { intents = append(intents, in) },
		composerAttachments: []composerAttachment{
			{Placeholder: "[Attachment #1: note.txt]", Input: protocol.AttachmentInput{Path: "note.txt", DisplayName: "note.txt"}},
		},
	}
	_ = m.submitPrompt("/status")
	if len(intents) != 1 || intents[0].Kind != protocol.IntentSubmitLocal {
		t.Fatalf("intents = %+v", intents)
	}
	if len(m.composerAttachments) != 1 {
		t.Fatalf("composer attachments consumed: %+v", m.composerAttachments)
	}
}

func TestClipboardImagePasteInsertsComposerPlaceholder(t *testing.T) {
	old := pasteClipboardImageToTempPNG
	oldSupported := clipboardImagePasteSupported
	pasteClipboardImageToTempPNG = func() (string, error) {
		return "/tmp/whale-clipboard-test.png", nil
	}
	clipboardImagePasteSupported = true
	defer func() {
		pasteClipboardImageToTempPNG = old
		clipboardImagePasteSupported = oldSupported
	}()

	var intents []protocol.Intent
	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		width:     80,
		height:    24,
		dispatch:  func(in protocol.Intent) { intents = append(intents, in) },
	}
	cmd, _, handled := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlV})
	if !handled {
		t.Fatal("expected ctrl+v to be handled")
	}
	if cmd != nil {
		t.Fatal("clipboard image paste should not return async command")
	}
	if len(intents) != 0 {
		t.Fatalf("intents = %+v", intents)
	}
	if len(m.composerAttachments) != 1 {
		t.Fatalf("composer attachments = %+v", m.composerAttachments)
	}
	got := m.composerAttachments[0]
	if got.Input.Path != "/tmp/whale-clipboard-test.png" || got.Input.DisplayName != "clipboard.png" {
		t.Fatalf("attachment = %+v", got.Input)
	}
	if input := m.input.Value(); !strings.Contains(input, "[Attachment #1: clipboard.png]") {
		t.Fatalf("input = %q", input)
	}
}

func TestClipboardImagePasteFailureDoesNotModifyComposer(t *testing.T) {
	old := pasteClipboardImageToTempPNG
	oldSupported := clipboardImagePasteSupported
	pasteClipboardImageToTempPNG = func() (string, error) {
		return "", errTestClipboardImage
	}
	clipboardImagePasteSupported = true
	defer func() {
		pasteClipboardImageToTempPNG = old
		clipboardImagePasteSupported = oldSupported
	}()

	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		width:     80,
		height:    24,
	}
	_, _, handled := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}, Alt: true})
	if !handled {
		t.Fatal("expected alt+v to be handled")
	}
	if len(m.composerAttachments) != 0 {
		t.Fatalf("composer attachments = %+v", m.composerAttachments)
	}
	if input := m.input.Value(); input != "" {
		t.Fatalf("input = %q", input)
	}
	if m.status != "image paste failed" {
		t.Fatalf("status = %q", m.status)
	}
	if len(m.transcript) == 0 || !strings.Contains(m.transcript[0].Text, "clipboard test error") {
		t.Fatalf("transcript = %+v", m.transcript)
	}
}

func TestClipboardImagePasteKeyIgnoredWhenUnsupported(t *testing.T) {
	oldSupported := clipboardImagePasteSupported
	clipboardImagePasteSupported = false
	defer func() { clipboardImagePasteSupported = oldSupported }()

	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		width:     80,
		height:    24,
	}
	_, _, handled := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyCtrlV})
	if handled {
		t.Fatal("expected unsupported clipboard image paste key to fall through")
	}
	if len(m.composerAttachments) != 0 {
		t.Fatalf("composer attachments = %+v", m.composerAttachments)
	}
	if m.status == "image paste failed" {
		t.Fatalf("status = %q", m.status)
	}
}

func TestPastedEscapedLocalImagePathInsertsComposerPlaceholder(t *testing.T) {
	path := writeTestPNG(t, filepath.Join(t.TempDir(), "Application Support", "screen shot.png"))
	escaped := strings.ReplaceAll(path, " ", `\ `)

	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		width:     80,
		height:    24,
	}
	cmd, _, handled := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(escaped), Paste: true})
	if !handled {
		t.Fatal("expected paste to be handled")
	}
	if cmd != nil {
		t.Fatal("pasted image path should not return async command")
	}
	if len(m.composerAttachments) != 1 {
		t.Fatalf("composer attachments = %+v", m.composerAttachments)
	}
	got := m.composerAttachments[0]
	if got.Input.Path != path || got.Input.DisplayName != "screen shot.png" {
		t.Fatalf("attachment = %+v", got.Input)
	}
	if input := m.input.Value(); !strings.Contains(input, "[Attachment #1: screen shot.png]") || strings.Contains(input, escaped) {
		t.Fatalf("input = %q", input)
	}
}

func TestPastedWrappedLocalImagePathInsertsComposerPlaceholder(t *testing.T) {
	path := writeTestPNG(t, filepath.Join(t.TempDir(), "qq", "nt_data", "Pic", "2026-06", "Ori", "e64cfa42660e2e4e51ba6e5ab3c60ea0.png"))
	breakAt := strings.Index(path, "3c60ea0")
	if breakAt < 0 {
		t.Fatalf("test path missing split marker: %s", path)
	}
	wrapped := path[:breakAt] + "\n" + path[breakAt:]

	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		width:     80,
		height:    24,
	}
	_, _, handled := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(wrapped), Paste: true})
	if !handled {
		t.Fatal("expected paste to be handled")
	}
	if len(m.composerAttachments) != 1 || m.composerAttachments[0].Input.Path != path {
		t.Fatalf("composer attachments = %+v", m.composerAttachments)
	}
	if input := m.input.Value(); !strings.Contains(input, "[Attachment #1: e64cfa42660e2e4e51ba6e5ab3c60ea0.png]") {
		t.Fatalf("input = %q", input)
	}
}

func TestPastedFileURLImagePathInsertsComposerPlaceholder(t *testing.T) {
	path := writeTestPNG(t, filepath.Join(t.TempDir(), "screen shot.png"))
	fileURL := (&url.URL{Scheme: "file", Path: path}).String()

	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		width:     80,
		height:    24,
	}
	_, _, handled := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(fileURL), Paste: true})
	if !handled {
		t.Fatal("expected paste to be handled")
	}
	if len(m.composerAttachments) != 1 || m.composerAttachments[0].Input.Path != path {
		t.Fatalf("composer attachments = %+v", m.composerAttachments)
	}
	if input := m.input.Value(); !strings.Contains(input, "[Attachment #1: screen shot.png]") {
		t.Fatalf("input = %q", input)
	}
}

func TestNormalizePastedFileURLForWindowsDrivePath(t *testing.T) {
	parsedCases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "drive path in URL path",
			raw:  "file:///C:/Users/goranka/screen%20shot.png",
			want: `C:\Users\goranka\screen shot.png`,
		},
	}
	for _, tc := range parsedCases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := url.Parse(tc.raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			got, ok := normalizePastedFileURLForGOOS(parsed, "windows")
			if !ok {
				t.Fatal("expected file URL to normalize")
			}
			if got != tc.want {
				t.Fatalf("path = %q, want %q", got, tc.want)
			}
		})
	}

	rawCases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "drive path in URL host",
			raw:  "file://C:%5CUsers%5Cgoranka%5Cscreen%20shot.png",
			want: `C:\Users\goranka\screen shot.png`,
		},
	}
	for _, tc := range rawCases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := normalizeRawPastedFileURLForGOOS(tc.raw, "windows")
			if !ok {
				t.Fatal("expected file URL to normalize")
			}
			if got != tc.want {
				t.Fatalf("path = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPastedNonImagePathFallsBackToTextPaste(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Application Support")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	escaped := strings.ReplaceAll(path, " ", `\ `)

	m := model{
		assembler: tuirender.NewAssembler(),
		mode:      modeChat,
		width:     80,
		height:    24,
	}
	_, _, handled := m.handleKeyMsg(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(escaped), Paste: true})
	if !handled {
		t.Fatal("expected paste to be handled")
	}
	if len(m.composerAttachments) != 0 {
		t.Fatalf("composer attachments = %+v", m.composerAttachments)
	}
	if input := m.input.Value(); input != escaped {
		t.Fatalf("input = %q, want %q", input, escaped)
	}
}

func TestUnescapeShellPathPreservesWindowsSeparators(t *testing.T) {
	raw := `C:\Users\goranka\Application\ Support\screen\ shot.png`
	want := `C:\Users\goranka\Application Support\screen shot.png`
	if got := unescapeShellPath(raw); got != want {
		t.Fatalf("unescaped path = %q, want %q", got, want)
	}
}

func TestAttachSlashCommandIsNotAdvertised(t *testing.T) {
	help := appcommands.CommandsHelp()
	if strings.Contains(help, "/attach") {
		t.Fatalf("commands help still advertises /attach: %s", help)
	}
	for _, name := range appcommands.SlashCommandNames() {
		if name == "/attach" {
			t.Fatalf("slash command names include /attach: %+v", appcommands.SlashCommandNames())
		}
	}
}

func writeTestPNG(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, tinyPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
