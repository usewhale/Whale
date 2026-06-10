package tools

import (
	"context"
	"errors"
	"fmt"
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
		return marshalToolErrorWithRecovery(call, "search_not_found", errMsg, toolRecoveryHint{
			Code:                "edit_search_not_found",
			RecommendedNextTool: "read_file",
			RecommendedInput: map[string]any{
				"file_path": toolInputPath(in.FilePath),
			},
			Retryable: false,
			Reason:    editSearchNotFoundReason(search),
		}), nil
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

// largeEditSearchLines is the search size beyond which a failed match is more
// likely content drift from rewriting code by memory than a small copy slip,
// so recovery steers toward smaller edits or apply_patch hunks.
const largeEditSearchLines = 40

func editSearchNotFoundReason(search string) string {
	reason := "edit search text must exactly match the current file content; read the file again and copy the literal text before retrying edit"
	if lines := strings.Count(search, "\n") + 1; lines > largeEditSearchLines {
		reason += fmt.Sprintf("; the failed search block is %d lines, so a single omitted or reworded line makes the whole match fail — split the change into several small edit calls or use apply_patch with small hunks instead", lines)
	}
	return reason
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
	if resolved, ok := editWhitespaceRelaxed(before, search, replace); ok {
		return resolved, true, ""
	}
	if !hasLiteralEscapedControls(search) {
		return resolvedEditSearch{}, false, "search text not found" + editSearchDivergence(before, search)
	}

	unescapedSearch := normalizeLineEndingText(unescapeLiteralControls(search))
	unescapedReplace := normalizeLineEndingText(unescapeLiteralControls(replace))
	if unescapedSearch == search {
		return resolvedEditSearch{}, false, "search text not found" + editSearchDivergence(before, search)
	}
	count := strings.Count(before, unescapedSearch)
	if count == 0 {
		return resolvedEditSearch{}, false, "search text not found; search appears JSON-escaped, but the unescaped search text was also not found" + editSearchDivergence(before, unescapedSearch)
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

// rstripLineEqual and trimLineEqual are the two relaxation levels for
// line-aligned fallback matching, mirroring codex's seek_sequence passes:
// first tolerate trailing-whitespace drift only, then drift on both edges.
func rstripLineEqual(a, b string) bool {
	return strings.TrimRight(a, " \t") == strings.TrimRight(b, " \t")
}

func trimLineEqual(a, b string) bool {
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}

// editWhitespaceRelaxed attempts a whole-line-aligned match that tolerates
// whitespace drift between search and file. The replacement always targets
// the file's actual bytes, and the result carries a repair tag like the
// json_escape_unwrapped fallback. The match must be unique at the chosen
// relaxation level; ambiguity falls back to the not-found error.
func editWhitespaceRelaxed(before, search, replace string) (resolvedEditSearch, bool) {
	searchLines := strings.Split(search, "\n")
	trailingNewline := false
	if len(searchLines) > 1 && searchLines[len(searchLines)-1] == "" {
		trailingNewline = true
		searchLines = searchLines[:len(searchLines)-1]
	}
	if len(searchLines) == 1 && strings.TrimSpace(searchLines[0]) == "" {
		return resolvedEditSearch{}, false
	}
	fileLines := strings.Split(before, "\n")
	for _, equal := range []func(a, b string) bool{rstripLineEqual, trimLineEqual} {
		starts := relaxedLineMatches(fileLines, searchLines, equal)
		if len(starts) != 1 {
			continue
		}
		start := starts[0]
		offset := 0
		for k := 0; k < start; k++ {
			offset += len(fileLines[k]) + 1
		}
		end := offset
		for k := range searchLines {
			end += len(fileLines[start+k])
			if k < len(searchLines)-1 {
				end++
			}
		}
		if trailingNewline {
			if end >= len(before) {
				continue
			}
			end++
		}
		actual := before[offset:end]
		// If the matched text also occurs earlier mid-line, a plain
		// strings.Replace would hit that occurrence instead of the aligned
		// match; only relax when this is the first byte-level occurrence.
		if strings.Index(before, actual) != offset {
			continue
		}
		return resolvedEditSearch{search: actual, replace: replace, repair: "whitespace_normalized"}, true
	}
	return resolvedEditSearch{}, false
}

func relaxedLineMatches(fileLines, searchLines []string, equal func(a, b string) bool) []int {
	if len(searchLines) == 0 || len(searchLines) > len(fileLines) {
		return nil
	}
	matches := []int{}
	for i := 0; i+len(searchLines) <= len(fileLines); i++ {
		ok := true
		for j := range searchLines {
			if !equal(fileLines[i+j], searchLines[j]) {
				ok = false
				break
			}
		}
		if ok {
			matches = append(matches, i)
			if len(matches) > 1 {
				return matches
			}
		}
	}
	return matches
}

// editDivergenceMaxLineLen caps quoted lines in divergence diagnostics so the
// error stays compact even for long source lines.
const editDivergenceMaxLineLen = 160

// editSearchDivergence locates the closest candidate region for a failed
// multi-line search, anchoring on the search's first line (whitespace-trimmed)
// and reporting the first line where file and search diverge. This lets the
// model fix the search in one round instead of re-reading the whole file; the
// match semantics stay exact, only the error message gets richer.
func editSearchDivergence(before, search string) string {
	searchLines := strings.Split(strings.TrimSuffix(search, "\n"), "\n")
	if len(searchLines) < 2 {
		return ""
	}
	anchor := strings.TrimSpace(searchLines[0])
	if anchor == "" {
		return ""
	}
	fileLines := strings.Split(before, "\n")
	bestStart, bestMatched := -1, 0
	for i := range fileLines {
		if strings.TrimSpace(fileLines[i]) != anchor {
			continue
		}
		matched := 0
		for j := 0; j < len(searchLines) && i+j < len(fileLines); j++ {
			if fileLines[i+j] != searchLines[j] {
				break
			}
			matched++
		}
		if bestStart < 0 || matched > bestMatched {
			bestStart, bestMatched = i, matched
		}
	}
	if bestStart < 0 || bestMatched >= len(searchLines) {
		return ""
	}
	divergence := bestStart + bestMatched
	fileLine := "<end of file>"
	if divergence < len(fileLines) {
		fileLine = truncateForDiagnostic(fileLines[divergence])
	}
	return fmt.Sprintf(
		"; closest match starts at line %d and matches the first %d search lines, diverging at line %d: file has %q where search has %q",
		bestStart+1, bestMatched, divergence+1, fileLine, truncateForDiagnostic(searchLines[bestMatched]))
}

func truncateForDiagnostic(s string) string {
	runes := []rune(s)
	if len(runes) <= editDivergenceMaxLineLen {
		return s
	}
	return string(runes[:editDivergenceMaxLineLen]) + "…"
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
