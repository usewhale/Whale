package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/core"
)

const (
	defaultSearchFilesLimit = 200
	maxSearchFilesLimit     = 2000
	defaultSearchFilesTime  = 20 * time.Second
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

type fileSearchMeta struct {
	MatchLimitReached bool
	Cancelled         bool
	TimedOut          bool
	Fallback          string
	Elapsed           time.Duration
}

func normalizeSearchFilesLimit(limit int) int {
	if limit <= 0 {
		return defaultSearchFilesLimit
	}
	return min(limit, maxSearchFilesLimit)
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
	pattern := strings.TrimSpace(in.Pattern)
	if pattern == "" {
		return marshalToolError(call, "invalid_args", "pattern is required"), nil
	}
	limit := normalizeSearchFilesLimit(in.Limit)
	abs, err := b.safeReadPath(ctx, in.Path)
	if err != nil {
		return b.marshalReadPathError(call, in.Path, err), nil
	}

	searchCtx, cancel := context.WithTimeout(ctx, defaultSearchFilesTime)
	defer cancel()

	start := time.Now()
	matches, meta, err := searchFileNamesWithRipgrep(searchCtx, abs, pattern, limit, b.displayPath)
	if err != nil && !isContextStopped(err) {
		matches, meta, err = searchFileNamesWithGo(searchCtx, abs, pattern, limit, b.displayPath)
	}
	meta.Elapsed = time.Since(start)
	if err != nil && !isContextStopped(err) {
		return marshalToolError(call, "exec_failed", err.Error()), nil
	}
	if errors.Is(err, context.Canceled) {
		meta.Cancelled = true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		meta.TimedOut = true
	}
	sort.Strings(matches)
	return marshalToolResult(call, buildSearchFilesResult(matches, meta, limit))
}

func isContextStopped(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func searchFileNamesWithRipgrep(ctx context.Context, abs string, pattern string, limit int, displayPath func(string) string) ([]string, fileSearchMeta, error) {
	if _, err := exec.LookPath("rg"); err != nil {
		return nil, fileSearchMeta{}, err
	}
	args := []string{"--files", "--hidden"}
	for dir := range defaultIgnoredDirs {
		args = append(args, "--glob", "!**/"+dir+"/**")
	}
	args = append(args, abs)
	cmd := exec.CommandContext(ctx, "rg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fileSearchMeta{}, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fileSearchMeta{}, err
	}

	matches := make([]string, 0, min(limit, 128))
	pat := strings.ToLower(pattern)
	meta := fileSearchMeta{}
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		if ctx.Err() != nil {
			_ = cmd.Process.Kill()
			break
		}
		rawPath := strings.TrimSpace(sc.Text())
		if rawPath == "" {
			continue
		}
		if !filepath.IsAbs(rawPath) {
			rawPath = filepath.Join(abs, rawPath)
		}
		if searchFilePathMatches(rawPath, pat) {
			matches = append(matches, displayPath(rawPath))
			if len(matches) >= limit {
				meta.MatchLimitReached = true
				_ = cmd.Process.Kill()
				break
			}
		}
	}
	if err := sc.Err(); err != nil && !meta.MatchLimitReached && ctx.Err() == nil {
		_ = cmd.Wait()
		return matches, meta, err
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		meta.applyContextStop(ctx.Err())
		return matches, meta, ctx.Err()
	}
	if waitErr != nil && !meta.MatchLimitReached {
		return matches, meta, waitErr
	}
	return matches, meta, nil
}

func searchFileNamesWithGo(ctx context.Context, abs string, pattern string, limit int, displayPath func(string) string) ([]string, fileSearchMeta, error) {
	matches := make([]string, 0, min(limit, 128))
	pat := strings.ToLower(pattern)
	meta := fileSearchMeta{Fallback: "go_walk"}
	err := filepath.WalkDir(abs, func(path string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			meta.applyContextStop(ctx.Err())
			return ctx.Err()
		}
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if defaultIgnoredDirs[d.Name()] && path != abs {
				return filepath.SkipDir
			}
			return nil
		}
		if searchFilePathMatches(path, pat) {
			matches = append(matches, displayPath(path))
			if len(matches) >= limit {
				meta.MatchLimitReached = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	if errors.Is(err, context.Canceled) {
		return matches, meta, err
	}
	if err != nil {
		return matches, meta, err
	}
	return matches, meta, nil
}

func (m *fileSearchMeta) applyContextStop(err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		m.TimedOut = true
		return
	}
	if errors.Is(err, context.Canceled) {
		m.Cancelled = true
	}
}

func searchFilePathMatches(path string, lowerPattern string) bool {
	return strings.Contains(strings.ToLower(filepath.ToSlash(path)), lowerPattern) ||
		strings.Contains(strings.ToLower(filepath.Base(path)), lowerPattern)
}

func buildSearchFilesResult(matches []string, meta fileSearchMeta, limit int) map[string]any {
	truncated := meta.MatchLimitReached || meta.Cancelled || meta.TimedOut
	summaryParts := make([]string, 0, 3)
	if meta.MatchLimitReached {
		hintLimit := min(limit*2, maxSearchFilesLimit)
		summaryParts = append(summaryParts, fmt.Sprintf("%d file matches limit reached; use limit=%d or refine path/pattern", limit, hintLimit))
	}
	if meta.Cancelled {
		summaryParts = append(summaryParts, "search cancelled; refine path/pattern before retrying broad searches")
	}
	if meta.TimedOut {
		summaryParts = append(summaryParts, "search timed out; refine path/pattern before retrying broad searches")
	}
	if meta.Fallback != "" {
		summaryParts = append(summaryParts, "ripgrep unavailable; used Go filesystem walk fallback")
	}
	truncatedBy := ""
	switch {
	case meta.Cancelled:
		truncatedBy = "cancelled"
	case meta.TimedOut:
		truncatedBy = "timeout"
	case meta.MatchLimitReached:
		truncatedBy = "match_limit"
	}
	return map[string]any{
		"status": "ok",
		"metrics": map[string]any{
			"total_matches":       len(matches),
			"returned":            len(matches),
			"match_limit":         limit,
			"match_limit_reached": meta.MatchLimitReached,
			"truncated":           truncated,
			"truncated_by":        truncatedBy,
			"cancelled":           meta.Cancelled,
			"timed_out":           meta.TimedOut,
			"elapsed_ms":          meta.Elapsed.Milliseconds(),
		},
		"payload": map[string]any{
			"items": matches,
		},
		"summary": strings.Join(summaryParts, " | "),
	}
}
