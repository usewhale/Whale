package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

type patchOpType string

const (
	patchOpUpdate patchOpType = "update"
	patchOpAdd    patchOpType = "add"
	patchOpDelete patchOpType = "delete"
)

type patchHunk struct {
	oldLines []string
	newLines []string
	lines    []patchHunkLine
}

type patchHunkLineKind byte

const (
	patchHunkContext patchHunkLineKind = ' '
	patchHunkRemove  patchHunkLineKind = '-'
	patchHunkAdd     patchHunkLineKind = '+'
)

type patchHunkLine struct {
	kind patchHunkLineKind
	text string
}

type patchOp struct {
	kind  patchOpType
	path  string
	hunks []patchHunk
	added []string
}

type patchFilePlan struct {
	path        string
	abs         string
	beforeBytes []byte
	exists      bool
	before      string
	after       string
	lineEndings lineEndingSnapshot
	remove      bool
}

func (b *Toolset) applyPatch(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	var in struct {
		Patch string `json:"patch"`
	}
	if err := decodeInput(call.Input, &in); err != nil {
		return marshalToolError(call, "invalid_args", err.Error()), nil
	}
	if strings.TrimSpace(in.Patch) == "" {
		return marshalToolError(call, "invalid_args", "patch is required"), nil
	}

	ops, err := parseBeginPatch(in.Patch)
	if err != nil {
		return marshalToolErrorWithRecovery(call, "patch_parse_failed", err.Error(), applyPatchParseRecovery()), nil
	}
	plans, err := b.planPatch(ops)
	if err != nil {
		return marshalToolError(call, patchApplyErrorCode(err), err.Error()), nil
	}
	changes := patchPlanChanges(plans)
	metadata := fileDiffMetadata(changes)
	commitPlans := make([]fileCommitPlan, 0, len(plans))
	for _, plan := range plans {
		commitPlans = append(commitPlans, fileCommitPlan{
			path:           plan.path,
			abs:            plan.abs,
			expectedBytes:  plan.beforeBytes,
			expectedExists: plan.exists,
			afterBytes:     restoreTextFileBytes(plan.after, plan.lineEndings),
			remove:         plan.remove,
		})
	}
	if err := b.commitFilePlans(ctx, commitPlans); err != nil {
		if isFileConflict(err) {
			return marshalToolError(call, "patch_conflict", err.Error()+": read the file again before patching"), nil
		}
		return marshalToolError(call, "patch_apply_failed", err.Error()), nil
	}
	for _, plan := range plans {
		if plan.remove {
			b.clearFileState(plan.abs)
			continue
		}
		b.storeFileState(plan.abs, plan.after)
	}

	filesChanged := make([]string, 0, len(plans))
	for _, plan := range plans {
		filesChanged = append(filesChanged, plan.path)
	}
	additions, deletions := fileDiffCounts(changes)
	return marshalToolResultWithMetadata(call, map[string]any{
		"files_changed": filesChanged,
		"additions":     additions,
		"deletions":     deletions,
	}, metadata)
}

func patchApplyErrorCode(err error) string {
	if err != nil && strings.Contains(err.Error(), "path escapes workspace") {
		return "permission_denied"
	}
	return "patch_apply_failed"
}

func applyPatchParseRecovery() toolRecoveryHint {
	return toolRecoveryHint{
		Code:                "apply_patch_parse_failed",
		RecommendedNextTool: "apply_patch",
		RecommendedInputPatch: map[string]any{
			"patch": "Rewrite the patch using *** Begin Patch / *** Update File: <path> sections; inside each update hunk, every line must start with space for context, '-' for removals, or '+' for additions.",
		},
		Retryable: true,
		Reason:    "apply_patch input must use Whale patch syntax; raw unified-diff headers or unprefixed hunk lines cannot be parsed",
	}
}

func (b *Toolset) previewApplyPatch(_ context.Context, call core.ToolCall) (map[string]any, error) {
	var in struct {
		Patch string `json:"patch"`
	}
	if err := decodeInput(call.Input, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Patch) == "" {
		return nil, fmt.Errorf("patch is required")
	}
	ops, err := parseBeginPatch(in.Patch)
	if err != nil {
		return nil, err
	}
	plans, err := b.planPatch(ops)
	if err != nil {
		return nil, err
	}
	return fileDiffMetadata(patchPlanChanges(plans)), nil
}

func patchPlanChanges(plans []patchFilePlan) []fileChangePreview {
	changes := make([]fileChangePreview, 0, len(plans))
	for _, plan := range plans {
		changes = append(changes, fileChangePreview{path: plan.path, before: plan.before, after: plan.after})
	}
	return changes
}

type patchFileState struct {
	path         string
	abs          string
	raw          []byte
	beforeExists bool
	before       string
	after        string
	lineEndings  lineEndingSnapshot
	exists       bool
	remove       bool
}

func (b *Toolset) planPatch(ops []patchOp) ([]patchFilePlan, error) {
	states := map[string]*patchFileState{}
	order := make([]string, 0, len(ops))
	getState := func(path string) (*patchFileState, error) {
		if st, ok := states[path]; ok {
			return st, nil
		}
		abs, err := b.safePath(path)
		if err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(abs)
		exists := true
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, err
			}
			exists = false
		}
		before, lineEndings := normalizeTextFileBytes(raw)
		st := &patchFileState{path: path, abs: abs, raw: raw, beforeExists: exists, before: before, after: before, lineEndings: lineEndings, exists: exists}
		states[path] = st
		order = append(order, path)
		return st, nil
	}

	for _, op := range ops {
		st, err := getState(op.path)
		if err != nil {
			return nil, err
		}
		switch op.kind {
		case patchOpAdd:
			if st.exists && !st.remove {
				return nil, fmt.Errorf("add file already exists: %s", op.path)
			}
			st.after = strings.Join(op.added, "\n")
			st.lineEndings = lineEndingSnapshot{style: lineEndingLF}
			st.exists = true
			st.remove = false
		case patchOpDelete:
			if !st.exists || st.remove {
				return nil, fmt.Errorf("delete target missing: %s", op.path)
			}
			st.after = ""
			st.exists = false
			st.remove = true
		case patchOpUpdate:
			if !st.exists || st.remove {
				return nil, fmt.Errorf("update target missing: %s", op.path)
			}
			out, err := applyPatchHunks(op.path, st.after, op.hunks)
			if err != nil {
				return nil, err
			}
			if st.lineEndings.mixed {
				lines, err := applyPatchLineEndingHunks(op.path, st.lineEndings.lines, op.hunks)
				if err != nil {
					return nil, err
				}
				st.lineEndings.lines = lines
			}
			st.after = out
		}
	}

	plans := make([]patchFilePlan, 0, len(order))
	for _, path := range order {
		st := states[path]
		if st.before == st.after && !st.remove {
			continue
		}
		plans = append(plans, patchFilePlan{path: st.path, abs: st.abs, beforeBytes: st.raw, exists: st.beforeExists, before: st.before, after: st.after, lineEndings: st.lineEndings, remove: st.remove})
	}
	return plans, nil
}

func applyPatchHunks(path, content string, hunks []patchHunk) (string, error) {
	lines, hadTrailingNewline := splitLinesKeepFlag(content)
	replacements, err := computePatchReplacements(path, lines, hunks)
	if err != nil {
		return "", err
	}
	next := applyPatchReplacements(lines, replacements)
	out := strings.Join(next, "\n")
	if hadTrailingNewline {
		out += "\n"
	}
	return out, nil
}

type patchReplacement struct {
	start    int
	oldLen   int
	newLines []string
}

func computePatchReplacements(path string, lines []string, hunks []patchHunk) ([]patchReplacement, error) {
	replacements := make([]patchReplacement, 0, len(hunks))
	lineIndex := 0
	for _, h := range hunks {
		newLines := h.newLines
		idx := findSubsliceFrom(lines, h.oldLines, lineIndex)
		if idx < 0 {
			relaxed, ambiguous := findSubsliceRelaxedFrom(lines, h.oldLines, lineIndex)
			if ambiguous {
				return nil, fmt.Errorf("hunk context matched multiple locations after whitespace normalization in %s; include more context lines to disambiguate", path)
			}
			if relaxed >= 0 {
				// The hunk matched with whitespace drift: keep the file's
				// actual text for context lines so the patch only changes
				// what its -/+ lines describe.
				idx = relaxed
				newLines = relaxedPatchNewLines(lines, relaxed, h)
			}
		}
		if idx < 0 {
			return nil, fmt.Errorf("hunk context not found in %s", path)
		}
		replacements = append(replacements, patchReplacement{
			start:    idx,
			oldLen:   len(h.oldLines),
			newLines: newLines,
		})
		lineIndex = idx + len(h.oldLines)
	}
	return replacements, nil
}

// findSubsliceRelaxedFrom retries a failed exact hunk match with decreasing
// strictness, mirroring codex's seek_sequence: ignore trailing whitespace
// first, then whitespace on both edges. Like the edit fallback, a relaxation
// level that matches more than one location is ambiguous and reported rather
// than guessed at; stricter levels are not shadowed by looser ones.
func findSubsliceRelaxedFrom(haystack, needle []string, start int) (int, bool) {
	for _, equal := range []func(a, b string) bool{rstripLineEqual, trimLineEqual} {
		idx := findSubsliceFuncFrom(haystack, needle, start, equal)
		if idx < 0 {
			continue
		}
		if findSubsliceFuncFrom(haystack, needle, idx+1, equal) >= 0 {
			return idx, true
		}
		return idx, false
	}
	return -1, false
}

func relaxedPatchNewLines(fileLines []string, start int, h patchHunk) []string {
	out := make([]string, 0, len(h.newLines))
	oldIndex := 0
	for _, line := range h.lines {
		switch line.kind {
		case patchHunkContext:
			if start+oldIndex < len(fileLines) {
				out = append(out, fileLines[start+oldIndex])
			} else {
				out = append(out, line.text)
			}
			oldIndex++
		case patchHunkRemove:
			oldIndex++
		case patchHunkAdd:
			out = append(out, line.text)
		}
	}
	return out
}

func applyPatchReplacements(lines []string, replacements []patchReplacement) []string {
	next := append([]string(nil), lines...)
	for i := len(replacements) - 1; i >= 0; i-- {
		replacement := replacements[i]
		before := append([]string{}, next[:replacement.start]...)
		after := append([]string{}, next[replacement.start+replacement.oldLen:]...)
		next = append(before, replacement.newLines...)
		next = append(next, after...)
	}
	return next
}

func applyPatchLineEndingHunks(path string, lines []lineEndingLine, hunks []patchHunk) ([]lineEndingLine, error) {
	replacements := make([]lineEndingReplacement, 0, len(hunks))
	lineIndex := 0
	for _, h := range hunks {
		idx := findLineEndingSubsliceFrom(lines, h.oldLines, lineIndex)
		if idx < 0 {
			relaxed, ambiguous := findLineEndingSubsliceRelaxedFrom(lines, h.oldLines, lineIndex)
			if ambiguous {
				return nil, fmt.Errorf("hunk context matched multiple locations after whitespace normalization in %s; include more context lines to disambiguate", path)
			}
			idx = relaxed
		}
		if idx < 0 {
			return nil, fmt.Errorf("hunk context not found in %s", path)
		}
		replacements = append(replacements, lineEndingReplacement{
			start:  idx,
			oldLen: len(h.oldLines),
			lines:  patchReplacementLineEndings(lines[idx:idx+len(h.oldLines)], h),
		})
		lineIndex = idx + len(h.oldLines)
	}
	next := append([]lineEndingLine(nil), lines...)
	for i := len(replacements) - 1; i >= 0; i-- {
		replacement := replacements[i]
		before := append([]lineEndingLine{}, next[:replacement.start]...)
		after := append([]lineEndingLine{}, next[replacement.start+replacement.oldLen:]...)
		next = append(before, replacement.lines...)
		next = append(next, after...)
	}
	return next, nil
}

type lineEndingReplacement struct {
	start  int
	oldLen int
	lines  []lineEndingLine
}

func patchReplacementLineEndings(old []lineEndingLine, h patchHunk) []lineEndingLine {
	out := make([]lineEndingLine, 0, len(h.newLines))
	oldIndex := 0
	removedSeps := make([]string, 0)
	for _, line := range h.lines {
		switch line.kind {
		case patchHunkContext:
			removedSeps = removedSeps[:0]
			if oldIndex < len(old) {
				// Use the file's actual text: identical to line.text on an
				// exact match, and the unmodified original on a relaxed
				// whitespace-drift match.
				out = append(out, lineEndingLine{text: old[oldIndex].text, sep: old[oldIndex].sep})
				oldIndex++
			}
		case patchHunkRemove:
			if oldIndex < len(old) {
				removedSeps = append(removedSeps, old[oldIndex].sep)
				oldIndex++
			}
		case patchHunkAdd:
			sep := "\n"
			if len(removedSeps) > 0 {
				sep = removedSeps[0]
				removedSeps = removedSeps[1:]
			}
			out = append(out, lineEndingLine{text: line.text, sep: sep})
		}
	}
	return out
}

func parseBeginPatch(patch string) ([]patchOp, error) {
	lines := strings.Split(normalizeLineEndingText(patch), "\n")
	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	if i >= len(lines) || strings.TrimSpace(lines[i]) != "*** Begin Patch" {
		return nil, fmt.Errorf("missing *** Begin Patch")
	}
	i++
	ops := make([]patchOp, 0)
	for i < len(lines) {
		line := lines[i]
		if strings.TrimSpace(line) == "*** End Patch" {
			return ops, nil
		}
		switch {
		case strings.HasPrefix(line, "*** Update File: "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Update File: "))
			if path == "" {
				return nil, fmt.Errorf("empty update path")
			}
			i++
			hunks := make([]patchHunk, 0)
			for i < len(lines) {
				if strings.HasPrefix(lines[i], "*** ") || strings.TrimSpace(lines[i]) == "*** End Patch" {
					break
				}
				if strings.HasPrefix(lines[i], "@@") {
					i++
					oldLines := make([]string, 0)
					newLines := make([]string, 0)
					hunkLines := make([]patchHunkLine, 0)
					for i < len(lines) {
						l := lines[i]
						if strings.HasPrefix(l, "@@") || strings.HasPrefix(l, "*** ") || strings.TrimSpace(l) == "*** End Patch" {
							break
						}
						if strings.HasPrefix(l, "-") {
							v := strings.TrimPrefix(l, "-")
							oldLines = append(oldLines, v)
							hunkLines = append(hunkLines, patchHunkLine{kind: patchHunkRemove, text: v})
						} else if strings.HasPrefix(l, "+") {
							v := strings.TrimPrefix(l, "+")
							newLines = append(newLines, v)
							hunkLines = append(hunkLines, patchHunkLine{kind: patchHunkAdd, text: v})
						} else if strings.HasPrefix(l, " ") {
							v := strings.TrimPrefix(l, " ")
							oldLines = append(oldLines, v)
							newLines = append(newLines, v)
							hunkLines = append(hunkLines, patchHunkLine{kind: patchHunkContext, text: v})
						} else if l == "" {
							// Models routinely drop the leading space on blank
							// context lines; treat a fully empty line as empty
							// context rather than failing the whole patch.
							oldLines = append(oldLines, "")
							newLines = append(newLines, "")
							hunkLines = append(hunkLines, patchHunkLine{kind: patchHunkContext, text: ""})
						} else if l == `\ No newline at end of file` {
							// ignore marker
						} else {
							return nil, fmt.Errorf("invalid hunk line: %s", l)
						}
						i++
					}
					if len(oldLines) == 0 && len(newLines) == 0 {
						return nil, fmt.Errorf("empty hunk for %s", path)
					}
					hunks = append(hunks, patchHunk{oldLines: oldLines, newLines: newLines, lines: hunkLines})
					continue
				}
				i++
			}
			if len(hunks) == 0 {
				return nil, fmt.Errorf("update file without hunks: %s", path)
			}
			ops = append(ops, patchOp{kind: patchOpUpdate, path: path, hunks: hunks})
		case strings.HasPrefix(line, "*** Add File: "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Add File: "))
			if path == "" {
				return nil, fmt.Errorf("empty add path")
			}
			i++
			added := make([]string, 0)
			for i < len(lines) {
				l := lines[i]
				if strings.HasPrefix(l, "*** ") || strings.TrimSpace(l) == "*** End Patch" {
					break
				}
				if strings.HasPrefix(l, "+") {
					added = append(added, strings.TrimPrefix(l, "+"))
				} else {
					return nil, fmt.Errorf("invalid add line: %s", l)
				}
				i++
			}
			ops = append(ops, patchOp{kind: patchOpAdd, path: path, added: added})
		case strings.HasPrefix(line, "*** Delete File: "):
			path := strings.TrimSpace(strings.TrimPrefix(line, "*** Delete File: "))
			if path == "" {
				return nil, fmt.Errorf("empty delete path")
			}
			ops = append(ops, patchOp{kind: patchOpDelete, path: path})
			i++
		default:
			if strings.TrimSpace(line) == "" {
				i++
				continue
			}
			return nil, fmt.Errorf("unknown patch line: %s\n%s", line, patchFormatHint(line))
		}
	}
	return nil, fmt.Errorf("missing *** End Patch")
}

func patchFormatHint(line string) string {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "diff --git"),
		strings.HasPrefix(trimmed, "--- "),
		strings.HasPrefix(trimmed, "+++ "),
		strings.HasPrefix(trimmed, "Index: "):
		return "This looks like unified diff syntax. Whale apply_patch expects *** Begin Patch with *** Update File/Add File/Delete File sections, not diff --git, ---, or +++ headers.\n" + minimalApplyPatchExample()
	case strings.HasPrefix(trimmed, "Update File:"),
		strings.HasPrefix(trimmed, "Update file:"),
		strings.HasPrefix(trimmed, "Update "):
		return "Patch file operations must start with the exact header *** Update File: <path>.\n" + minimalApplyPatchExample()
	default:
		return "Expected one of: *** Update File: <path>, *** Add File: <path>, *** Delete File: <path>, @@ hunk lines, or *** End Patch.\n" + minimalApplyPatchExample()
	}
}

func minimalApplyPatchExample() string {
	return strings.Join([]string{
		"Minimal valid example:",
		"*** Begin Patch",
		"*** Update File: path/to/file",
		"@@",
		" context line",
		"-old line",
		"+new line",
		"*** End Patch",
	}, "\n")
}

func splitLinesKeepFlag(s string) ([]string, bool) {
	if s == "" {
		return []string{}, false
	}
	hadTrailing := strings.HasSuffix(s, "\n")
	trimmed := strings.TrimSuffix(s, "\n")
	return strings.Split(trimmed, "\n"), hadTrailing
}

func findSubslice(haystack, needle []string) int {
	return findSubsliceFrom(haystack, needle, 0)
}

func findSubsliceFrom(haystack, needle []string, start int) int {
	return findSubsliceFuncFrom(haystack, needle, start, func(a, b string) bool { return a == b })
}

func findSubsliceFuncFrom(haystack, needle []string, start int, equal func(a, b string) bool) int {
	if len(needle) == 0 {
		if start <= len(haystack) {
			return start
		}
		return -1
	}
	if start < 0 {
		start = 0
	}
	if len(needle) > len(haystack) || start > len(haystack)-len(needle) {
		return -1
	}
outer:
	for i := start; i <= len(haystack)-len(needle); i++ {
		for j := 0; j < len(needle); j++ {
			if !equal(haystack[i+j], needle[j]) {
				continue outer
			}
		}
		return i
	}
	return -1
}

func findLineEndingSubslice(haystack []lineEndingLine, needle []string) int {
	return findLineEndingSubsliceFrom(haystack, needle, 0)
}

func findLineEndingSubsliceFrom(haystack []lineEndingLine, needle []string, start int) int {
	return findLineEndingSubsliceFuncFrom(haystack, needle, start, func(a, b string) bool { return a == b })
}

func findLineEndingSubsliceRelaxedFrom(haystack []lineEndingLine, needle []string, start int) (int, bool) {
	for _, equal := range []func(a, b string) bool{rstripLineEqual, trimLineEqual} {
		idx := findLineEndingSubsliceFuncFrom(haystack, needle, start, equal)
		if idx < 0 {
			continue
		}
		if findLineEndingSubsliceFuncFrom(haystack, needle, idx+1, equal) >= 0 {
			return idx, true
		}
		return idx, false
	}
	return -1, false
}

func findLineEndingSubsliceFuncFrom(haystack []lineEndingLine, needle []string, start int, equal func(a, b string) bool) int {
	if len(needle) == 0 {
		if start <= len(haystack) {
			return start
		}
		return -1
	}
	if start < 0 {
		start = 0
	}
	if len(needle) > len(haystack) || start > len(haystack)-len(needle) {
		return -1
	}
outer:
	for i := start; i <= len(haystack)-len(needle); i++ {
		for j := 0; j < len(needle); j++ {
			if !equal(haystack[i+j].text, needle[j]) {
				continue outer
			}
		}
		return i
	}
	return -1
}
