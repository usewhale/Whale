package tools

import (
	"context"
	"os"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

func (b *Toolset) editFile(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	var in struct {
		FilePath string `json:"file_path"`
		Search   string `json:"search"`
		Replace  string `json:"replace"`
		All      bool   `json:"all"`
	}
	if err := decodeInput(call.Input, &in); err != nil {
		return marshalToolError(call, "invalid_args", err.Error()), nil
	}
	if in.FilePath == "" {
		return marshalToolError(call, "invalid_args", "file_path is required"), nil
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
	before, lineEndings := normalizeTextFileBytes(data)
	if in.Search == "" {
		return marshalToolError(call, "invalid_args", "search is required"), nil
	}
	search := normalizeLineEndingText(in.Search)
	replace := normalizeLineEndingText(in.Replace)
	if !strings.Contains(before, search) {
		return marshalToolError(call, "search_not_found", "search text not found"), nil
	}
	after := ""
	replacements := 1
	if in.All {
		replacements = strings.Count(before, search)
		after = strings.ReplaceAll(before, search, replace)
	} else {
		after = strings.Replace(before, search, replace, 1)
	}
	if err := os.WriteFile(abs, restoreTextFileBytes(after, lineEndings), 0o644); err != nil {
		return marshalToolError(call, "write_failed", err.Error()), nil
	}
	metadata := fileDiffMetadata([]fileChangePreview{{path: in.FilePath, before: before, after: after}})
	return marshalToolResultWithMetadata(call, map[string]any{
		"file_path":    in.FilePath,
		"replacements": replacements,
	}, metadata)
}

func (b *Toolset) previewEditFile(_ context.Context, call core.ToolCall) (map[string]any, error) {
	var in struct {
		FilePath string `json:"file_path"`
		Search   string `json:"search"`
		Replace  string `json:"replace"`
		All      bool   `json:"all"`
	}
	if err := decodeInput(call.Input, &in); err != nil {
		return nil, err
	}
	if in.FilePath == "" {
		return nil, os.ErrInvalid
	}
	abs, err := b.safePath(in.FilePath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	before, _ := normalizeTextFileBytes(data)
	if in.Search == "" {
		return nil, os.ErrInvalid
	}
	search := normalizeLineEndingText(in.Search)
	replace := normalizeLineEndingText(in.Replace)
	if !strings.Contains(before, search) {
		return nil, os.ErrNotExist
	}
	after := strings.Replace(before, search, replace, 1)
	if in.All {
		after = strings.ReplaceAll(before, search, replace)
	}
	return fileDiffMetadata([]fileChangePreview{{path: in.FilePath, before: before, after: after}}), nil
}
