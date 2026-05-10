package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

func (b *Toolset) readFile(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	var in struct {
		FilePath string `json:"file_path"`
		Offset   int    `json:"offset"`
		Limit    int    `json:"limit"`
	}
	if err := decodeInput(call.Input, &in); err != nil {
		return marshalToolError(call, "invalid_args", err.Error()), nil
	}
	abs, err := b.safePath(in.FilePath)
	if err != nil {
		return marshalToolError(call, "permission_denied", err.Error()), nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return marshalToolError(call, "not_found", err.Error()), nil
		}
		return marshalToolError(call, "read_failed", err.Error()), nil
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	total := len(lines)
	start := in.Offset
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := total
	if in.Limit > 0 && start+in.Limit < total {
		end = start + in.Limit
	}
	selected := lines[start:end]
	truncatedLines := 0
	for i := range selected {
		r := []rune(selected[i])
		if len(r) > maxViewLineChars {
			selected[i] = string(r[:maxViewLineChars]) + "...[line truncated]"
			truncatedLines++
		}
	}
	rel := filepath.ToSlash(strings.TrimPrefix(abs, b.root+string(filepath.Separator)))
	return marshalToolResult(call, map[string]any{
		"status": "ok",
		"metrics": map[string]any{
			"total_lines":          total,
			"returned_lines":       len(selected),
			"truncated_line_count": truncatedLines,
		},
		"payload": map[string]any{
			"file_path": rel,
			"range": map[string]any{
				"start": start,
				"end":   end,
			},
			"has_more_before": start > 0,
			"has_more_after":  end < total,
			"content":         strings.Join(selected, "\n"),
		},
		"summary": rel + ":" + fmt.Sprintf("%d-%d/%d", start, end, total),
	})
}
