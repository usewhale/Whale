package tools

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

var defaultIgnoredDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	".next":        true,
	"target":       true,
}

func (b *Toolset) searchFiles(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	var in struct {
		Path    string `json:"path"`
		Pattern string `json:"pattern"`
		Limit   int    `json:"limit"`
	}
	if err := decodeInput(call.Input, &in); err != nil {
		return marshalToolError(call, "invalid_args", err.Error()), nil
	}
	if strings.TrimSpace(in.Pattern) == "" {
		return marshalToolError(call, "invalid_args", "pattern is required"), nil
	}
	if in.Limit <= 0 {
		in.Limit = 200
	}
	if in.Limit > 2000 {
		in.Limit = 2000
	}
	abs, err := b.safeReadPath(ctx, in.Path)
	if err != nil {
		return b.marshalReadPathError(call, in.Path, err), nil
	}

	matches := make([]string, 0, min(in.Limit, 128))
	total := 0
	pat := strings.ToLower(strings.TrimSpace(in.Pattern))
	_ = filepath.WalkDir(abs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if defaultIgnoredDirs[d.Name()] && path != abs {
				return filepath.SkipDir
			}
			return nil
		}
		rel := b.displayPath(path)
		if strings.Contains(strings.ToLower(rel), pat) || strings.Contains(strings.ToLower(d.Name()), pat) {
			total++
			if len(matches) < in.Limit {
				matches = append(matches, rel)
			}
		}
		return nil
	})
	sort.Strings(matches)
	return marshalToolResult(call, map[string]any{
		"status": "ok",
		"metrics": map[string]any{
			"total_matches": total,
			"returned":      len(matches),
			"truncated":     total > len(matches),
		},
		"payload": map[string]any{
			"items": matches,
		},
	})
}
