package tools

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

func (b *Toolset) editFile(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
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
	if code, msg := b.validateFileState(abs, before); code != "" {
		return marshalToolError(call, code, msg), nil
	}
	if b.afterFileRead != nil {
		b.afterFileRead(abs)
	}
	if in.Search == "" {
		return marshalToolError(call, "invalid_args", "search is required"), nil
	}
	search := normalizeLineEndingText(in.Search)
	replace := normalizeLineEndingText(in.Replace)
	resolved, ok, errMsg := resolveEditSearch(before, search, replace, in.All)
	if !ok {
		return marshalToolError(call, "search_not_found", errMsg), nil
	}
	after := ""
	replacements := 1
	if in.All {
		replacements = strings.Count(before, resolved.search)
		after = strings.ReplaceAll(before, resolved.search, resolved.replace)
	} else {
		after = strings.Replace(before, resolved.search, resolved.replace, 1)
	}
	afterBytes := restoreTextFileBytes(after, lineEndings)
	if err := b.commitFilePlans(ctx, []fileCommitPlan{{
		path:           in.FilePath,
		abs:            abs,
		expectedBytes:  data,
		expectedExists: true,
		afterBytes:     afterBytes,
	}}); err != nil {
		if isFileConflict(err) {
			return marshalToolError(call, "write_conflict", err.Error()+": read the file again before editing"), nil
		}
		return marshalToolError(call, "write_failed", err.Error()), nil
	}
	b.storeFileState(abs, after)
	metadata := fileDiffMetadata([]fileChangePreview{{path: in.FilePath, before: before, after: after}})
	dataMap := map[string]any{
		"file_path":    in.FilePath,
		"replacements": replacements,
	}
	if resolved.repair != "" {
		dataMap["repair"] = resolved.repair
	}
	return marshalToolResultWithMetadata(call, dataMap, metadata)
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
	if code, msg := b.validateFileState(abs, before); code != "" {
		return nil, errors.New(code + ": " + msg)
	}
	if in.Search == "" {
		return nil, os.ErrInvalid
	}
	search := normalizeLineEndingText(in.Search)
	replace := normalizeLineEndingText(in.Replace)
	resolved, ok, _ := resolveEditSearch(before, search, replace, in.All)
	if !ok {
		return nil, os.ErrNotExist
	}
	after := strings.Replace(before, resolved.search, resolved.replace, 1)
	if in.All {
		after = strings.ReplaceAll(before, resolved.search, resolved.replace)
	}
	return fileDiffMetadata([]fileChangePreview{{path: in.FilePath, before: before, after: after}}), nil
}

type resolvedEditSearch struct {
	search  string
	replace string
	repair  string
}

func resolveEditSearch(before, search, replace string, all bool) (resolvedEditSearch, bool, string) {
	if strings.Contains(before, search) {
		return resolvedEditSearch{search: search, replace: replace}, true, ""
	}
	if !hasLiteralEscapedControls(search) {
		return resolvedEditSearch{}, false, "search text not found"
	}

	unescapedSearch := normalizeLineEndingText(unescapeLiteralControls(search))
	unescapedReplace := normalizeLineEndingText(unescapeLiteralControls(replace))
	if unescapedSearch == search {
		return resolvedEditSearch{}, false, "search text not found"
	}
	count := strings.Count(before, unescapedSearch)
	if count == 0 {
		return resolvedEditSearch{}, false, "search text not found; search appears JSON-escaped, but the unescaped search text was also not found"
	}
	if !all && count > 1 {
		return resolvedEditSearch{}, false, "search text not found; search appears JSON-escaped, but the unescaped search text matched multiple locations"
	}
	return resolvedEditSearch{
		search:  unescapedSearch,
		replace: unescapedReplace,
		repair:  "json_escape_unwrapped",
	}, true, ""
}

func hasLiteralEscapedControls(s string) bool {
	return strings.Contains(s, `\n`) || strings.Contains(s, `\t`) || strings.Contains(s, `\r`)
}

func unescapeLiteralControls(s string) string {
	replacer := strings.NewReplacer(
		`\r\n`, "\n",
		`\n`, "\n",
		`\r`, "\r",
		`\t`, "\t",
	)
	return replacer.Replace(s)
}
