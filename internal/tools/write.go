package tools

import (
	"context"
	"os"
	"path/filepath"

	"github.com/usewhale/whale/internal/core"
)

func (b *Toolset) writeFile(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	var in struct {
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
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
	beforeBytes, readErr := os.ReadFile(abs)
	if readErr != nil && !os.IsNotExist(readErr) {
		return marshalToolError(call, "read_failed", readErr.Error()), nil
	}
	existing := readErr == nil
	before, after, content := prepareWriteFileContent(beforeBytes, in.Content, existing)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return marshalToolError(call, "write_failed", err.Error()), nil
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return marshalToolError(call, "write_failed", err.Error()), nil
	}
	metadata := fileDiffMetadata([]fileChangePreview{{path: in.FilePath, before: before, after: after}})
	return marshalToolResultWithMetadata(call, map[string]any{"file_path": in.FilePath, "bytes": len(content)}, metadata)
}

func (b *Toolset) previewWriteFile(_ context.Context, call core.ToolCall) (map[string]any, error) {
	var in struct {
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
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
	beforeBytes, err := os.ReadFile(abs)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	before, after, _ := prepareWriteFileContent(beforeBytes, in.Content, err == nil)
	return fileDiffMetadata([]fileChangePreview{{path: in.FilePath, before: before, after: after}}), nil
}

func prepareWriteFileContent(beforeBytes []byte, content string, existing bool) (string, string, string) {
	if !existing {
		return "", content, content
	}
	before, lineEndings := normalizeTextFileBytes(beforeBytes)
	if !hasLineEndingBytes(beforeBytes) {
		return before, content, string(restoreTextFileBytes(content, lineEndings))
	}
	after := normalizeLineEndingText(content)
	return before, after, string(restoreTextFileBytes(after, lineEndings))
}

func hasLineEndingBytes(b []byte) bool {
	for _, c := range b {
		if c == '\n' || c == '\r' {
			return true
		}
	}
	return false
}
