package tui

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/runtime/protocol"
)

type composerAttachment struct {
	Placeholder string
	Input       protocol.AttachmentInput
}

func (m *model) attachComposerFile(path, displayName string) tea.Cmd {
	name := attachmentDisplayName(protocol.AttachmentInput{Path: path, DisplayName: displayName})
	placeholder := m.nextAttachmentPlaceholder(name)
	m.composerAttachments = append(m.composerAttachments, composerAttachment{
		Placeholder: placeholder,
		Input:       protocol.AttachmentInput{Path: path, DisplayName: name},
	})
	m.input.HandlePaste(placeholder + "\n")
	m.status = fmt.Sprintf("attached %s", name)
	m.refreshViewportContentFollow(true)
	return nil
}

func (m *model) handlePastedImagePath(raw string) (tea.Cmd, bool) {
	path, ok := pastedImagePath(raw)
	if !ok {
		return nil, false
	}
	m.cancelWindowsDeferredEnter()
	m.markWindowsPastedInput()
	m.resetHistoryNavigation()
	return m.attachComposerFile(path, filepath.Base(path)), true
}

func unquotePastedPath(raw string) string {
	if len(raw) < 2 {
		return raw
	}
	quote := raw[0]
	if (quote != '"' && quote != '\'') || raw[len(raw)-1] != quote {
		return raw
	}
	inner := raw[1 : len(raw)-1]
	var b strings.Builder
	b.Grow(len(inner))
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if c == '\\' && i+1 < len(inner) {
			next := inner[i+1]
			if next == quote || next == '\\' {
				b.WriteByte(next)
				i++
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

func pastedImagePath(raw string) (string, bool) {
	for _, candidate := range pastedPathCandidates(raw) {
		if isSupportedPastedImage(candidate) {
			return candidate, true
		}
	}
	return "", false
}

func pastedPathCandidates(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	normalized := strings.ReplaceAll(trimmed, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")

	var out []string
	add := func(value string) {
		path, ok := normalizePastedPath(value)
		if !ok {
			return
		}
		for _, existing := range out {
			if existing == path {
				return
			}
		}
		out = append(out, path)
	}

	add(normalized)
	if strings.Contains(normalized, "\n") {
		add(strings.Join(strings.Split(normalized, "\n"), ""))
		add(strings.Join(strings.Fields(normalized), " "))
	}
	return out
}

func normalizePastedPath(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", false
	}
	value = unquotePastedPath(value)
	if strings.HasPrefix(strings.ToLower(value), "file://") {
		if parsed, err := url.Parse(value); err == nil && parsed.Scheme == "file" {
			path, ok := normalizePastedFileURL(parsed)
			if !ok {
				return "", false
			}
			return path, true
		}
		if path, ok := normalizeRawPastedFileURL(value); ok {
			return path, true
		}
		return "", false
	}
	value = unescapeShellPath(value)
	if strings.TrimSpace(value) == "" {
		return "", false
	}
	return value, true
}

func unescapeShellPath(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\\' && i+1 < len(raw) {
			next := raw[i+1]
			if next == ' ' || next == '\t' || next == '\n' || next == '\r' || next == '"' || next == '\'' {
				b.WriteByte(next)
				i++
				continue
			}
		}
		b.WriteByte(raw[i])
	}
	return b.String()
}

func normalizePastedFileURL(parsed *url.URL) (string, bool) {
	return normalizePastedFileURLForGOOS(parsed, runtime.GOOS)
}

func normalizeRawPastedFileURL(raw string) (string, bool) {
	return normalizeRawPastedFileURLForGOOS(raw, runtime.GOOS)
}

func normalizeRawPastedFileURLForGOOS(raw, goos string) (string, bool) {
	if goos != "windows" {
		return "", false
	}
	if len(raw) < len("file://") || !strings.EqualFold(raw[:len("file://")], "file://") {
		return "", false
	}
	rest := raw[len("file://"):]
	path, err := url.PathUnescape(rest)
	if err != nil || !hasWindowsDrivePrefix(path) {
		return "", false
	}
	return normalizeFileURLPathForGOOS(path, goos), true
}

func normalizePastedFileURLForGOOS(parsed *url.URL, goos string) (string, bool) {
	if parsed.Host != "" && parsed.Host != "localhost" {
		if goos != "windows" {
			return "", false
		}
		hostPath, err := url.PathUnescape(parsed.Host)
		if err != nil || !hasWindowsDrivePrefix(hostPath) {
			return "", false
		}
		path, err := url.PathUnescape(parsed.Path)
		if err != nil {
			return "", false
		}
		combined := hostPath
		if path != "" {
			combined += path
		}
		return normalizeFileURLPathForGOOS(combined, goos), true
	}
	path, err := url.PathUnescape(parsed.Path)
	if err != nil || path == "" {
		return "", false
	}
	return normalizeFileURLPathForGOOS(path, goos), true
}

func normalizeFileURLPathForGOOS(path, goos string) string {
	if goos == "windows" {
		if len(path) >= 4 && path[0] == '/' && isASCIIAlpha(path[1]) && path[2] == ':' {
			path = path[1:]
		}
		return strings.ReplaceAll(path, "/", `\`)
	}
	return path
}

func hasWindowsDrivePrefix(path string) bool {
	return len(path) >= 2 && isASCIIAlpha(path[0]) && path[1] == ':'
}

func isASCIIAlpha(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func isSupportedPastedImage(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()

	var header [512]byte
	n, err := file.Read(header[:])
	if err != nil && n == 0 {
		return false
	}
	switch http.DetectContentType(header[:n]) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func (m model) nextAttachmentPlaceholder(name string) string {
	return fmt.Sprintf("[Attachment #%d: %s]", len(m.composerAttachments)+1, name)
}

func (m *model) consumeVisibleComposerAttachments(text string) []composerAttachment {
	if len(m.composerAttachments) == 0 {
		return nil
	}
	out := make([]composerAttachment, 0, len(m.composerAttachments))
	for _, item := range m.composerAttachments {
		if strings.Contains(text, item.Placeholder) {
			out = append(out, item)
		}
	}
	m.composerAttachments = nil
	return out
}

func cloneAttachmentInputs(in []protocol.AttachmentInput) []protocol.AttachmentInput {
	if len(in) == 0 {
		return nil
	}
	out := make([]protocol.AttachmentInput, len(in))
	copy(out, in)
	return out
}

func cloneComposerAttachments(in []composerAttachment) []composerAttachment {
	if len(in) == 0 {
		return nil
	}
	out := make([]composerAttachment, len(in))
	copy(out, in)
	return out
}

func attachmentInputsFromComposerAttachments(in []composerAttachment) []protocol.AttachmentInput {
	if len(in) == 0 {
		return nil
	}
	out := make([]protocol.AttachmentInput, 0, len(in))
	for _, item := range in {
		out = append(out, item.Input)
	}
	return out
}

func attachmentDisplayName(item protocol.AttachmentInput) string {
	name := strings.TrimSpace(item.DisplayName)
	if name == "" {
		name = filepath.Base(strings.TrimSpace(item.Path))
	}
	if name == "" || name == "." {
		name = strings.TrimSpace(item.Path)
	}
	return name
}
