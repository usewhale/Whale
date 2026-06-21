package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/usewhale/whale/internal/core"
)

type multiEditStep struct {
	Search  string `json:"search"`
	Replace string `json:"replace"`
	All     bool   `json:"all"`
}

type multiEditInput struct {
	FilePath string          `json:"file_path"`
	Edits    []multiEditStep `json:"edits"`
}

type multiEditApplyError struct {
	code     string
	message  string
	recovery *toolRecoveryHint
}

func (e multiEditApplyError) Error() string { return e.message }

func (b *Toolset) multiEditFile(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	plan, err := b.planMultiEdit(call)
	if err != nil {
		return multiEditToolError(call, err), nil
	}
	if err := b.commitFilePlans(ctx, []fileCommitPlan{{
		path:           plan.input.FilePath,
		abs:            plan.abs,
		expectedBytes:  plan.beforeBytes,
		expectedExists: true,
		afterBytes:     plan.afterBytes,
	}}); err != nil {
		if isFileConflict(err) {
			return marshalToolError(call, "write_conflict", err.Error()+": read the file again before editing"), nil
		}
		return marshalToolError(call, "write_failed", err.Error()), nil
	}
	b.storeFileState(plan.abs, plan.after)
	metadata := fileDiffMetadata([]fileChangePreview{{path: plan.input.FilePath, before: plan.before, after: plan.after}})
	return marshalToolResultWithMetadata(call, map[string]any{
		"file_path":    plan.input.FilePath,
		"edits":        len(plan.input.Edits),
		"replacements": plan.replacements,
		"repair_count": len(plan.repairs),
		"repairs":      plan.repairs,
	}, metadata)
}

func (b *Toolset) previewMultiEditFile(_ context.Context, call core.ToolCall) (map[string]any, error) {
	plan, err := b.planMultiEdit(call)
	if err != nil {
		return nil, err
	}
	return fileDiffMetadata([]fileChangePreview{{path: plan.input.FilePath, before: plan.before, after: plan.after}}), nil
}

type multiEditPlan struct {
	input        multiEditInput
	abs          string
	beforeBytes  []byte
	before       string
	after        string
	afterBytes   []byte
	replacements int
	repairs      []string
}

func (b *Toolset) planMultiEdit(call core.ToolCall) (multiEditPlan, error) {
	var in multiEditInput
	if err := decodeInput(call.Input, &in); err != nil {
		return multiEditPlan{}, multiEditApplyError{code: "invalid_args", message: err.Error()}
	}
	if in.FilePath == "" {
		return multiEditPlan{}, multiEditApplyError{code: "invalid_args", message: "file_path is required"}
	}
	if len(in.Edits) == 0 {
		return multiEditPlan{}, multiEditApplyError{code: "invalid_args", message: "edits must not be empty"}
	}
	abs, err := b.safePath(in.FilePath)
	if err != nil {
		return multiEditPlan{}, multiEditApplyError{code: "permission_denied", message: err.Error()}
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return multiEditPlan{}, multiEditApplyError{code: "not_found", message: err.Error()}
		}
		return multiEditPlan{}, multiEditApplyError{code: "read_failed", message: err.Error()}
	}
	before, lineEndings := normalizeTextFileBytes(data)
	if b.afterFileRead != nil {
		b.afterFileRead(abs)
	}
	after, replacements, repairs, err := applyMultiEditSteps(before, in.Edits)
	if err != nil {
		return multiEditPlan{}, err
	}
	return multiEditPlan{
		input:        in,
		abs:          abs,
		beforeBytes:  data,
		before:       before,
		after:        after,
		afterBytes:   restoreTextFileBytes(after, lineEndings),
		replacements: replacements,
		repairs:      repairs,
	}, nil
}

func applyMultiEditSteps(before string, edits []multiEditStep) (string, int, []string, error) {
	after := before
	replacements := 0
	repairs := []string{}
	for i, step := range edits {
		stepIndex := i + 1
		if step.Search == "" {
			return "", 0, nil, multiEditApplyError{
				code:    "invalid_args",
				message: fmt.Sprintf("edit %d: search is required", stepIndex),
			}
		}
		search := normalizeLineEndingText(step.Search)
		replace := normalizeLineEndingText(step.Replace)
		resolved, ok, errMsg := resolveEditSearch(after, search, replace, step.All)
		if !ok {
			return "", 0, nil, multiEditSearchError(stepIndex, search, errMsg)
		}
		count := strings.Count(after, resolved.search)
		if count == 0 {
			return "", 0, nil, multiEditSearchError(stepIndex, search, "search text not found")
		}
		if !step.All && count != 1 {
			locations := describeSearchMatches(after, resolved.search)
			return "", 0, nil, multiEditApplyError{
				code: "search_not_unique",
				message: fmt.Sprintf(
					"edit %d: search text matched %d locations:\n%s\nadd a surrounding line to disambiguate one of them, or set all to true to replace every occurrence",
					stepIndex,
					count,
					locations,
				),
				recovery: &toolRecoveryHint{
					Code:                "multi_edit_search_not_unique",
					RecommendedNextTool: "multi_edit",
					Retryable:           true,
					Reason: fmt.Sprintf(
						"edit %d search matched %d locations (listed in the error); retry multi_edit with a surrounding line added to the search so it matches exactly one, or set all to true",
						stepIndex,
						count,
					),
				},
			}
		}
		if step.All {
			after = strings.ReplaceAll(after, resolved.search, resolved.replace)
			replacements += count
		} else {
			after = strings.Replace(after, resolved.search, resolved.replace, 1)
			replacements++
		}
		if resolved.repair != "" {
			repairs = append(repairs, fmt.Sprintf("edit %d: %s", stepIndex, resolved.repair))
		}
	}
	return after, replacements, repairs, nil
}

// describeSearchMatches renders the file locations where search occurs, one per
// line as "  line N: <line text>", so a search_not_unique error tells the model
// exactly where the ambiguity is and it can add a disambiguating line without
// re-reading the file. Output is capped so a pathological match count can't
// flood the error; the cap is reported when hit. Each rendered line is also
// length-capped so a match on a minified or generated long line cannot embed
// megabytes of text on this diagnostic path.
func describeSearchMatches(content, search string) string {
	const maxShown = 10
	const maxLineLen = 200
	var b strings.Builder
	shown := 0
	for offset := 0; ; {
		idx := strings.Index(content[offset:], search)
		if idx < 0 {
			break
		}
		matchAt := offset + idx
		if shown < maxShown {
			line := 1 + strings.Count(content[:matchAt], "\n")
			lineStart := strings.LastIndexByte(content[:matchAt], '\n') + 1
			lineEnd := strings.IndexByte(content[matchAt:], '\n')
			if lineEnd < 0 {
				lineEnd = len(content)
			} else {
				lineEnd += matchAt
			}
			text := strings.TrimSpace(content[lineStart:lineEnd])
			if len(text) > maxLineLen {
				cut := maxLineLen
				for cut > 0 && !utf8.RuneStart(text[cut]) {
					cut--
				}
				text = text[:cut] + "… (line truncated)"
			}
			fmt.Fprintf(&b, "  line %d: %s\n", line, text)
			shown++
		} else {
			shown++
		}
		// Advance past the whole match, mirroring strings.Count/ReplaceAll's
		// non-overlapping semantics so the listed locations match the reported
		// count (e.g. "aa" in "aaaa" is 2 matches, not 3).
		offset = matchAt + len(search)
	}
	if shown > maxShown {
		fmt.Fprintf(&b, "  ... and %d more\n", shown-maxShown)
	}
	return strings.TrimRight(b.String(), "\n")
}

func multiEditSearchError(stepIndex int, search, message string) multiEditApplyError {
	return multiEditApplyError{
		code:    "search_not_found",
		message: fmt.Sprintf("edit %d: %s", stepIndex, message),
		recovery: &toolRecoveryHint{
			Code:                "multi_edit_search_not_found",
			RecommendedNextTool: "read_file",
			Retryable:           false,
			Reason:              fmt.Sprintf("edit %d search failed; %s", stepIndex, editSearchNotFoundReason(search)),
		},
	}
}

func multiEditToolError(call core.ToolCall, err error) core.ToolResult {
	if applyErr, ok := err.(multiEditApplyError); ok {
		if applyErr.recovery != nil {
			return marshalToolErrorWithRecovery(call, applyErr.code, applyErr.message, *applyErr.recovery)
		}
		return marshalToolError(call, applyErr.code, applyErr.message)
	}
	return marshalToolError(call, "multi_edit_failed", err.Error())
}
