package tools

// Reproductions from session 019e9c00-f884-7e53-88e8-d49c106b51cc, where the
// model failed to edit internal/tasks/subagent.go and escaped to a python
// heredoc. The failure chain was:
//
//  1. edit rejected because the model rewrote a 356-line search block from
//     memory and omitted a 3-line comment present in the file (m-1288).
//
// These tests cover the fixes derived from that session: large-search recovery
// guidance and divergence diagnostics for failed edit searches.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newToolsetWithFile writes the fixture, builds a toolset rooted at dir, and
// performs a full read so edit's observed-read-state requirement is met.
func newToolsetWithFile(t *testing.T, dir, name, content string) *Toolset {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	readFileFull(t, ts, name)
	return ts
}

// sessionFileContent mirrors the shape of the real divergence: the file
// contains a comment block that the model's search text omitted.
const sessionFileContent = `func (r *Runner) spawn() error {
	opts := []agent.Option{
		agent.WithToolPolicy(r.parentPolicy),
		// The child registry is capability-restricted, but policy decisions
		// still flow through the parent approval path so workspace/user
		// permission rules remain effective inside subagents.
		agent.WithApprovalFunc(r.approvalFunc),
	}
	return run(opts)
}
`

// Search block as the model wrote it: identical except the comment lines are
// missing. Exact matching cannot find it (m-1288: search_not_found).
const sessionSearchMissingComment = `func (r *Runner) spawn() error {
	opts := []agent.Option{
		agent.WithToolPolicy(r.parentPolicy),
		agent.WithApprovalFunc(r.approvalFunc),
	}
	return run(opts)
}
`

func TestEditSearchBlockOmittingCommentLinesReportsDivergence(t *testing.T) {
	dir := t.TempDir()
	ts := newToolsetWithFile(t, dir, "subagent.go", sessionFileContent)

	res, err := ts.editFile(context.Background(), tc("edit", map[string]any{
		"file_path": "subagent.go",
		"search":    sessionSearchMissingComment,
		"replace":   "// replaced\n",
	}))
	if err != nil {
		t.Fatalf("editFile returned transport error: %v", err)
	}
	if !res.IsError() {
		t.Fatalf("expected search_not_found error, got success: %s", res.ModelText)
	}
	if !strings.Contains(res.ModelText, "search_not_found") {
		t.Fatalf("expected search_not_found code, got: %s", res.ModelText)
	}
	// The diagnostics anchor on the search's first line, find the closest
	// candidate block, and quote the first divergent line from the file —
	// here the comment the model omitted — so it can fix the search without
	// re-reading the file.
	if !strings.Contains(res.ModelText, "capability-restricted") {
		t.Fatalf("expected divergence diagnostics quoting the omitted comment line, got: %s", res.ModelText)
	}
	if !strings.Contains(res.ModelText, "diverging at line 4") {
		t.Fatalf("expected divergence at line 4, got: %s", res.ModelText)
	}
}

func TestEditSearchDivergenceSilentForUnanchoredSearch(t *testing.T) {
	dir := t.TempDir()
	ts := newToolsetWithFile(t, dir, "subagent.go", sessionFileContent)

	// First line of the search does not exist anywhere in the file: no
	// candidate region, so the error stays the plain search_not_found.
	res, err := ts.editFile(context.Background(), tc("edit", map[string]any{
		"file_path": "subagent.go",
		"search":    "no such anchor line\nno such second line\n",
		"replace":   "x",
	}))
	if err != nil || !res.IsError() {
		t.Fatalf("expected error result: err=%v res=%+v", err, res)
	}
	if strings.Contains(res.ModelText, "closest match") {
		t.Fatalf("expected no divergence diagnostics without an anchor, got: %s", res.ModelText)
	}
}

func TestEditWhitespaceRelaxedMatchesTrailingSpaceDrift(t *testing.T) {
	dir := t.TempDir()
	// File lines carry trailing spaces the model's search omits.
	ts := newToolsetWithFile(t, dir, "a.go", "func f() { \n\tcall() \n}\n")

	res, err := ts.editFile(context.Background(), tc("edit", map[string]any{
		"file_path": "a.go",
		"search":    "func f() {\n\tcall()\n}\n",
		"replace":   "func f() {\n\tother()\n}\n",
	}))
	if err != nil || res.IsError() {
		t.Fatalf("edit failed: err=%v res=%+v", err, res)
	}
	if !strings.Contains(res.ModelText, "whitespace_normalized") {
		t.Fatalf("expected whitespace_normalized repair tag, got: %s", res.ModelText)
	}
	got, err := os.ReadFile(filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(got) != "func f() {\n\tother()\n}\n" {
		t.Fatalf("content = %q, want relaxed edit applied", string(got))
	}
}

func TestEditWhitespaceRelaxedMatchesIndentationDrift(t *testing.T) {
	dir := t.TempDir()
	// File uses tabs, search uses spaces: only the trim-both pass matches.
	ts := newToolsetWithFile(t, dir, "a.go", "if ok {\n\tcall()\n}\n")

	res, err := ts.editFile(context.Background(), tc("edit", map[string]any{
		"file_path": "a.go",
		"search":    "if ok {\n    call()\n}\n",
		"replace":   "if ok {\n\tother()\n}\n",
	}))
	if err != nil || res.IsError() {
		t.Fatalf("edit failed: err=%v res=%+v", err, res)
	}
	got, err := os.ReadFile(filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(got) != "if ok {\n\tother()\n}\n" {
		t.Fatalf("content = %q, want relaxed edit applied", string(got))
	}
}

func TestEditWhitespaceRelaxedRejectsAmbiguousMatch(t *testing.T) {
	dir := t.TempDir()
	// Two regions match after trimming; relaxation must not guess.
	ts := newToolsetWithFile(t, dir, "a.go", "call() \nend\ncall()\t\nend\n")

	res, err := ts.editFile(context.Background(), tc("edit", map[string]any{
		"file_path": "a.go",
		"search":    "call()\nend\n",
		"replace":   "other()\nend\n",
	}))
	if err != nil {
		t.Fatalf("editFile returned transport error: %v", err)
	}
	if !res.IsError() || !strings.Contains(res.ModelText, "search_not_found") {
		t.Fatalf("expected search_not_found for ambiguous relaxed match, got: %s", res.ModelText)
	}
}

func TestEditNotFoundRecoverySuggestsSplittingLargeSearchBlocks(t *testing.T) {
	dir := t.TempDir()
	ts := newToolsetWithFile(t, dir, "subagent.go", sessionFileContent)

	// A large search block that cannot match, like the 356-line block from
	// the session. The recovery hint should steer toward splitting the edit
	// or using multi_edit.
	large := strings.Repeat("\tthis line does not exist in the file\n", largeEditSearchLines+1)
	res, err := ts.editFile(context.Background(), tc("edit", map[string]any{
		"file_path": "subagent.go",
		"search":    large,
		"replace":   "x",
	}))
	if err != nil || !res.IsError() {
		t.Fatalf("expected error result: err=%v res=%+v", err, res)
	}
	if !strings.Contains(res.ModelText, "multi_edit") {
		t.Fatalf("expected large-search guidance mentioning multi_edit, got: %s", res.ModelText)
	}

	// Small failed searches keep the focused message without the guidance.
	res, err = ts.editFile(context.Background(), tc("edit", map[string]any{
		"file_path": "subagent.go",
		"search":    "does not exist",
		"replace":   "x",
	}))
	if err != nil || !res.IsError() {
		t.Fatalf("expected error result: err=%v res=%+v", err, res)
	}
	if strings.Contains(res.ModelText, "multi_edit") {
		t.Fatalf("small search should not trigger large-block guidance, got: %s", res.ModelText)
	}
}
