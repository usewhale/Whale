package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

func (b *Toolset) readFile(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	var in struct {
		FilePath string `json:"file_path"`
		Offset   *int   `json:"offset"`
		Limit    *int   `json:"limit"`
	}
	if err := decodeInput(call.Input, &in); err != nil {
		return marshalToolError(call, "invalid_args", err.Error()), nil
	}
	abs, err := b.safeReadPath(in.FilePath)
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
	text, _ := normalizeTextFileBytes(data)
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	total := len(lines)
	start := 0
	if in.Offset != nil {
		start = *in.Offset
	}
	limit := 0
	if in.Limit != nil {
		limit = *in.Limit
	}
	note := ""
	if in.Offset == nil && in.Limit != nil {
		note = "offset was not provided; defaulted to 0. To read a different range, retry with both offset and limit."
	}
	if in.Offset != nil && in.Limit == nil {
		limit = 2000
		note = "limit was not provided; defaulted to 2000 lines. To read more or fewer lines, retry with both offset and limit."
	}
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := total
	if limit > 0 && start+limit < total {
		end = start + limit
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
	rel := b.displayPath(abs)
	result := map[string]any{
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
	}
	if note != "" {
		result["note"] = note
	}
	return marshalToolResult(call, result)
}
