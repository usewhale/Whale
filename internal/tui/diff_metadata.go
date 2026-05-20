package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"

	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

var diffHunkHeaderRe = regexp.MustCompile(`^@@ -([0-9]+)(?:,[0-9]+)? \+([0-9]+)(?:,[0-9]+)? @@`)

const (
	fileDiffPreviewMaxLines         = 400
	approvalFileDiffPreviewMaxLines = 80
)

func renderFileDiffMetadataMarkdown(metadata map[string]any, maxLines int) string {
	plain := renderFileDiffMetadataPlain(metadata, maxLines)
	if strings.TrimSpace(plain) == "" {
		return ""
	}
	if strings.HasPrefix(strings.TrimSpace(plain), "diff preview unavailable:") {
		return plain
	}
	return "```diff\n" + plain + "\n```"
}

func renderFileDiffMetadataPlain(metadata map[string]any, maxLines int) string {
	if len(metadata) == 0 || strings.TrimSpace(asString(metadata["kind"])) != "file_diff" {
		return ""
	}
	if msg := strings.TrimSpace(asString(metadata["preview_error"])); msg != "" {
		return "diff preview unavailable: " + msg
	}
	files := fileDiffMetadataFiles(metadata["files"])
	if len(files) == 0 {
		return ""
	}
	lines := make([]string, 0, len(files)*8)
	for _, file := range files {
		if strings.TrimSpace(file.diff) == "" {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, strings.Split(strings.TrimRight(file.diff, "\n"), "\n")...)
		if file.truncated {
			lines = append(lines, "... diff truncated ...")
		}
	}
	if len(lines) == 0 {
		return ""
	}
	if maxLines > 0 && len(lines) > maxLines {
		hidden := len(lines) - maxLines
		lines = append(lines[:maxLines], fmt.Sprintf("... diff truncated (%d lines hidden) ...", hidden))
	}
	return strings.Join(lines, "\n")
}

func renderApprovalDiffMetadata(metadata map[string]any, maxLines int) string {
	if len(metadata) == 0 || strings.TrimSpace(asString(metadata["kind"])) != "file_diff" {
		return ""
	}
	if msg := strings.TrimSpace(asString(metadata["preview_error"])); msg != "" {
		return "diff preview unavailable: " + msg
	}
	files := fileDiffMetadataFiles(metadata["files"])
	if len(files) == 0 {
		return ""
	}

	headerStyle := lipgloss.NewStyle().Bold(true)
	addStyle := lipgloss.NewStyle().Foreground(tuitheme.Default.Success)
	delStyle := lipgloss.NewStyle().Foreground(tuitheme.Default.Error)
	mutedStyle := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)

	lines := make([]string, 0, len(files)*8)
	for _, file := range files {
		if strings.TrimSpace(file.diff) == "" {
			continue
		}
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		path := strings.TrimSpace(file.path)
		if path == "" {
			path = "file"
		}
		lines = append(lines, headerStyle.Render(fmt.Sprintf("%s (+%d -%d)", path, file.additions, file.deletions)))
		rows, ok := renderApprovalDiffRows(file.diff, addStyle, delStyle, mutedStyle)
		if !ok {
			rows = strings.Split(strings.TrimRight(file.diff, "\n"), "\n")
		}
		lines = append(lines, rows...)
		if file.truncated {
			lines = append(lines, mutedStyle.Render("... diff truncated ..."))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	if maxLines > 0 && len(lines) > maxLines {
		hidden := len(lines) - maxLines
		lines = append(lines[:maxLines], mutedStyle.Render(fmt.Sprintf("... diff truncated (%d lines hidden) ...", hidden)))
	}
	return strings.Join(lines, "\n")
}

func isReadableApprovalDiff(diff string) bool {
	trimmed := strings.TrimSpace(diff)
	return trimmed != "" &&
		!strings.HasPrefix(trimmed, "diff preview unavailable:") &&
		!strings.HasPrefix(trimmed, "--- ") &&
		!strings.HasPrefix(trimmed, "+++ ") &&
		!strings.HasPrefix(trimmed, "@@ ")
}

func renderApprovalDiffRows(diff string, addStyle, delStyle, mutedStyle lipgloss.Style) ([]string, bool) {
	rawLines := strings.Split(strings.TrimRight(diff, "\n"), "\n")
	rows := make([]string, 0, len(rawLines))
	oldLine, newLine := 0, 0
	sawHunk := false
	for _, line := range rawLines {
		switch {
		case strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ "):
			continue
		case strings.HasPrefix(line, "@@ "):
			match := diffHunkHeaderRe.FindStringSubmatch(line)
			if len(match) != 3 {
				return nil, false
			}
			oldLine = parsePositiveInt(match[1])
			newLine = parsePositiveInt(match[2])
			sawHunk = true
		case strings.HasPrefix(line, "... diff truncated"):
			rows = append(rows, mutedStyle.Render(line))
		case sawHunk && strings.HasPrefix(line, "-"):
			rows = append(rows, delStyle.Render(formatApprovalDiffLine(oldLine, "-", strings.TrimPrefix(line, "-"))))
			oldLine++
		case sawHunk && strings.HasPrefix(line, "+"):
			rows = append(rows, addStyle.Render(formatApprovalDiffLine(newLine, "+", strings.TrimPrefix(line, "+"))))
			newLine++
		case sawHunk && strings.HasPrefix(line, " "):
			rows = append(rows, mutedStyle.Render(formatApprovalDiffLine(newLine, " ", strings.TrimPrefix(line, " "))))
			oldLine++
			newLine++
		case strings.TrimSpace(line) == "":
			rows = append(rows, "")
		default:
			return nil, false
		}
	}
	return rows, sawHunk
}

func formatApprovalDiffLine(lineNo int, marker, text string) string {
	return fmt.Sprintf("%4d %s%s", lineNo, marker, text)
}

func parsePositiveInt(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

type fileDiffView struct {
	path      string
	diff      string
	additions int
	deletions int
	truncated bool
}

func fileDiffMetadataFiles(value any) []fileDiffView {
	switch files := value.(type) {
	case []map[string]any:
		out := make([]fileDiffView, 0, len(files))
		for _, file := range files {
			out = append(out, fileDiffView{
				path:      asString(file["path"]),
				diff:      asString(file["unified_diff"]),
				additions: asInt(file["additions"]),
				deletions: asInt(file["deletions"]),
				truncated: asBool(file["truncated"]),
			})
		}
		return out
	case []any:
		out := make([]fileDiffView, 0, len(files))
		for _, item := range files {
			file, ok := item.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, fileDiffView{
				path:      asString(file["path"]),
				diff:      asString(file["unified_diff"]),
				additions: asInt(file["additions"]),
				deletions: asInt(file["deletions"]),
				truncated: asBool(file["truncated"]),
			})
		}
		return out
	default:
		return nil
	}
}

func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(strings.TrimSpace(t), "true")
	default:
		return false
	}
}
