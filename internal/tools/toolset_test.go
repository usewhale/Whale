package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/skills"
)

func tc(name string, in any) core.ToolCall {
	b, _ := json.Marshal(in)
	return core.ToolCall{ID: "tc-1", Name: name, Input: string(b)}
}

func TestViewWriteEdit(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	writeRes, err := ts.writeFile(context.Background(), tc("write", map[string]any{
		"file_path": "a.txt",
		"content":   "hello\nworld\n",
	}))
	if err != nil || writeRes.IsError {
		t.Fatalf("write failed: err=%v res=%+v", err, writeRes)
	}

	viewRes, err := ts.readFile(context.Background(), tc("read_file", map[string]any{
		"file_path": "a.txt",
		"offset":    1,
		"limit":     1,
	}))
	if err != nil || viewRes.IsError {
		t.Fatalf("view failed: err=%v res=%+v", err, viewRes)
	}
	if !strings.Contains(viewRes.Content, "world") {
		t.Fatalf("unexpected view content: %s", viewRes.Content)
	}

	editRes, err := ts.editFile(context.Background(), tc("edit", map[string]any{
		"file_path": "a.txt",
		"search":    "world",
		"replace":   "whale",
	}))
	if err != nil || editRes.IsError {
		t.Fatalf("edit failed: err=%v res=%+v", err, editRes)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if !strings.Contains(string(got), "whale") {
		t.Fatalf("edit not applied: %s", string(got))
	}
}

func TestReadFileNormalizesCRLFContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("zero\r\none\r\ntwo\r\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.readFile(context.Background(), tc("read_file", map[string]any{
		"file_path": "a.txt",
	}))
	if err != nil || res.IsError {
		t.Fatalf("read_file failed: err=%v res=%+v", err, res)
	}
	data := readFileData(t, res)
	if content := readFileContent(t, data); content != "zero\none\ntwo" {
		t.Fatalf("content = %q, want normalized LF content", content)
	}
	if strings.Contains(readFileContent(t, data), "\r") {
		t.Fatalf("content still contains CR: %q", readFileContent(t, data))
	}
}

func TestReadFileStripsUTF8BOMFromVisibleContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), append([]byte{0xEF, 0xBB, 0xBF}, []byte("zero\none\n")...), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.readFile(context.Background(), tc("read_file", map[string]any{
		"file_path": "a.txt",
	}))
	if err != nil || res.IsError {
		t.Fatalf("read_file failed: err=%v res=%+v", err, res)
	}
	data := readFileData(t, res)
	content := readFileContent(t, data)
	if content != "zero\none" {
		t.Fatalf("content = %q, want BOM stripped from visible content", content)
	}
	if strings.Contains(content, "\ufeff") {
		t.Fatalf("content still contains BOM: %q", content)
	}
}

func TestWriteFilePreservesCRLFWhenOverwritingExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("alpha\r\nbeta\r\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.writeFile(context.Background(), tc("write", map[string]any{
		"file_path": "a.txt",
		"content":   "alpha\nwhale\n",
	}))
	if err != nil || res.IsError {
		t.Fatalf("write failed: err=%v res=%+v", err, res)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(got) != "alpha\r\nwhale\r\n" {
		t.Fatalf("content = %q, want CRLF preserved", string(got))
	}
}

func TestWriteFilePreservesLFWhenOverwritingExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.writeFile(context.Background(), tc("write", map[string]any{
		"file_path": "a.txt",
		"content":   "alpha\r\nwhale\r\n",
	}))
	if err != nil || res.IsError {
		t.Fatalf("write failed: err=%v res=%+v", err, res)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(got) != "alpha\nwhale\n" {
		t.Fatalf("content = %q, want LF preserved", string(got))
	}
}

func TestWriteFilePreservesUTF8BOMWhenOverwritingExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, append([]byte{0xEF, 0xBB, 0xBF}, []byte("alpha\r\nbeta\r\n")...), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.writeFile(context.Background(), tc("write", map[string]any{
		"file_path": "a.txt",
		"content":   "alpha\nwhale\n",
	}))
	if err != nil || res.IsError {
		t.Fatalf("write failed: err=%v res=%+v", err, res)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	want := append([]byte{0xEF, 0xBB, 0xBF}, []byte("alpha\r\nwhale\r\n")...)
	if string(got) != string(want) {
		t.Fatalf("content bytes = % x, want % x", got, want)
	}
}

func TestWriteFileKeepsNewFileContentExact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.writeFile(context.Background(), tc("write", map[string]any{
		"file_path": "a.txt",
		"content":   "alpha\r\nwhale\r\n",
	}))
	if err != nil || res.IsError {
		t.Fatalf("write failed: err=%v res=%+v", err, res)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(got) != "alpha\r\nwhale\r\n" {
		t.Fatalf("content = %q, want new file content unchanged", string(got))
	}
}

func TestWriteFileKeepsExistingFileContentExactWhenNoLineEndingStyle(t *testing.T) {
	for _, tt := range []struct {
		name    string
		before  string
		content string
	}{
		{name: "empty placeholder", before: "", content: "alpha\r\nwhale\r\n"},
		{name: "single line placeholder", before: "placeholder", content: "alpha\rwhale\r"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "a.txt")
			if err := os.WriteFile(path, []byte(tt.before), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			ts, err := NewToolset(dir)
			if err != nil {
				t.Fatalf("new toolset: %v", err)
			}

			res, err := ts.writeFile(context.Background(), tc("write", map[string]any{
				"file_path": "a.txt",
				"content":   tt.content,
			}))
			if err != nil || res.IsError {
				t.Fatalf("write failed: err=%v res=%+v", err, res)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read result: %v", err)
			}
			if string(got) != tt.content {
				t.Fatalf("content = %q, want exact requested content %q", string(got), tt.content)
			}
		})
	}
}

func TestEditFileMatchesLFSearchAndPreservesCRLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("alpha\r\nbeta\r\ngamma\r\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.editFile(context.Background(), tc("edit", map[string]any{
		"file_path": "a.txt",
		"search":    "alpha\nbeta",
		"replace":   "alpha\nwhale",
	}))
	if err != nil || res.IsError {
		t.Fatalf("edit failed: err=%v res=%+v", err, res)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(got) != "alpha\r\nwhale\r\ngamma\r\n" {
		t.Fatalf("content = %q, want CRLF preserved", string(got))
	}
}

func TestEditFileMatchesFirstLineAfterUTF8BOMAndPreservesBOM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, append([]byte{0xEF, 0xBB, 0xBF}, []byte("SIF LOCAL:1 > 0\r\n中文┃全角\r\n")...), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.editFile(context.Background(), tc("edit", map[string]any{
		"file_path": "a.txt",
		"search":    "SIF LOCAL:1 > 0",
		"replace":   "SIF LOCAL:1 > 1",
	}))
	if err != nil || res.IsError {
		t.Fatalf("edit failed: err=%v res=%+v", err, res)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	want := append([]byte{0xEF, 0xBB, 0xBF}, []byte("SIF LOCAL:1 > 1\r\n中文┃全角\r\n")...)
	if string(got) != string(want) {
		t.Fatalf("content bytes = % x, want % x", got, want)
	}
}

func TestEditFilePreservesMixedLineEndings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("alpha\r\nbeta\ngamma\r\ndelta\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.editFile(context.Background(), tc("edit", map[string]any{
		"file_path": "a.txt",
		"search":    "beta\ngamma",
		"replace":   "whale\ngamma",
	}))
	if err != nil || res.IsError {
		t.Fatalf("edit failed: err=%v res=%+v", err, res)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(got) != "alpha\r\nwhale\ngamma\r\ndelta\n" {
		t.Fatalf("content = %q, want mixed line endings preserved", string(got))
	}
}

func TestEditFileDuplicateReplacementKeepsMixedLineEndings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("a\r\nb\nc\r\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.editFile(context.Background(), tc("edit", map[string]any{
		"file_path": "a.txt",
		"search":    "b",
		"replace":   "c",
	}))
	if err != nil || res.IsError {
		t.Fatalf("edit failed: err=%v res=%+v", err, res)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(got) != "a\r\nc\nc\r\n" {
		t.Fatalf("content = %q, want duplicate replacement to preserve mixed endings", string(got))
	}
}

func TestApplyPatchMatchesLFHunksAndPreservesCRLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("alpha\r\nbeta\r\ngamma\r\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: a.txt",
		"@@",
		" alpha",
		"-beta",
		"+whale",
		" gamma",
		"*** End Patch",
	}, "\n")

	preview, err := ts.previewApplyPatch(context.Background(), tc("apply_patch", map[string]any{"patch": patch}))
	if err != nil {
		t.Fatalf("preview patch: %v", err)
	}
	if diff := firstMetadataDiff(t, preview); !strings.Contains(diff, "-beta") || !strings.Contains(diff, "+whale") {
		t.Fatalf("unexpected preview diff:\n%s", diff)
	}
	res, err := ts.applyPatch(context.Background(), tc("apply_patch", map[string]any{"patch": patch}))
	if err != nil || res.IsError {
		t.Fatalf("apply patch failed: err=%v res=%+v", err, res)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(got) != "alpha\r\nwhale\r\ngamma\r\n" {
		t.Fatalf("content = %q, want CRLF preserved", string(got))
	}
}

func TestApplyPatchMatchesFirstLineAfterUTF8BOMAndPreservesBOM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, append([]byte{0xEF, 0xBB, 0xBF}, []byte("SIF LOCAL:1 > 0\r\n中文┃全角\r\n")...), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: a.txt",
		"@@",
		"-SIF LOCAL:1 > 0",
		"+SIF LOCAL:1 > 1",
		" 中文┃全角",
		"*** End Patch",
	}, "\n")

	res, err := ts.applyPatch(context.Background(), tc("apply_patch", map[string]any{"patch": patch}))
	if err != nil || res.IsError {
		t.Fatalf("apply patch failed: err=%v res=%+v", err, res)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	want := append([]byte{0xEF, 0xBB, 0xBF}, []byte("SIF LOCAL:1 > 1\r\n中文┃全角\r\n")...)
	if string(got) != string(want) {
		t.Fatalf("content bytes = % x, want % x", got, want)
	}
}

func TestApplyPatchPreservesMixedLineEndings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("alpha\r\nbeta\ngamma\r\ndelta\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: a.txt",
		"@@",
		" alpha",
		"-beta",
		"+whale",
		"+inserted",
		" gamma",
		"*** End Patch",
	}, "\n")

	res, err := ts.applyPatch(context.Background(), tc("apply_patch", map[string]any{"patch": patch}))
	if err != nil || res.IsError {
		t.Fatalf("apply patch failed: err=%v res=%+v", err, res)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(got) != "alpha\r\nwhale\ninserted\ngamma\r\ndelta\n" {
		t.Fatalf("content = %q, want mixed line endings preserved", string(got))
	}
}

func TestApplyPatchDuplicateInsertionBeforeContextKeepsMixedLineEndings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("a\nc\r\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: a.txt",
		"@@",
		" a",
		"+c",
		" c",
		"*** End Patch",
	}, "\n")

	res, err := ts.applyPatch(context.Background(), tc("apply_patch", map[string]any{"patch": patch}))
	if err != nil || res.IsError {
		t.Fatalf("apply patch failed: err=%v res=%+v", err, res)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(got) != "a\nc\nc\r\n" {
		t.Fatalf("content = %q, want inserted duplicate to keep context separator", string(got))
	}
}

func TestApplyPatchDoesNotCarryDeletedSeparatorAcrossContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("a\r\nb\nc\r\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: a.txt",
		"@@",
		"-a",
		" b",
		"+x",
		" c",
		"*** End Patch",
	}, "\n")

	res, err := ts.applyPatch(context.Background(), tc("apply_patch", map[string]any{"patch": patch}))
	if err != nil || res.IsError {
		t.Fatalf("apply patch failed: err=%v res=%+v", err, res)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(got) != "b\nx\nc\r\n" {
		t.Fatalf("content = %q, want deletion separator not to cross context", string(got))
	}
}

func TestApplyPatchToolDescriptionDocumentsFormat(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	var spec core.ToolSpec
	for _, tool := range ts.Tools() {
		if tool.Name() == "apply_patch" {
			spec = core.DescribeTool(tool)
			break
		}
	}
	if spec.Name == "" {
		t.Fatal("apply_patch tool not found")
	}
	for _, want := range []string{
		"*** Begin Patch",
		"*** Update File: path/to/file",
		"*** Add File: <path>",
		"*** Delete File: <path>",
		"Do not use unified diff headers",
	} {
		if !strings.Contains(spec.Description, want) {
			t.Fatalf("apply_patch description missing %q:\n%s", want, spec.Description)
		}
	}
	props, _ := spec.Parameters["properties"].(map[string]any)
	patchProp, _ := props["patch"].(map[string]any)
	desc, _ := patchProp["description"].(string)
	if !strings.Contains(desc, "*** Begin Patch") || !strings.Contains(desc, "Do not send unified diff") {
		t.Fatalf("patch parameter description does not document patch format: %q", desc)
	}
}

func TestApplyPatchParseErrorsGuideAwayFromUnifiedDiff(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	patch := strings.Join([]string{
		"*** Begin Patch",
		"--- a/a.txt",
		"+++ b/a.txt",
		"@@",
		"-world",
		"+whale",
		"*** End Patch",
	}, "\n")

	res, err := ts.applyPatch(context.Background(), tc("apply_patch", map[string]any{"patch": patch}))
	if err != nil {
		t.Fatalf("apply patch returned dispatch error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected parse error, got %+v", res)
	}
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok {
		t.Fatalf("parse envelope: %s", res.Content)
	}
	if env.Code != "patch_parse_failed" {
		t.Fatalf("code = %q, want patch_parse_failed", env.Code)
	}
	for _, want := range []string{
		"unified diff syntax",
		"*** Update File/Add File/Delete File",
		"Minimal valid example:",
	} {
		if !strings.Contains(env.Error, want) {
			t.Fatalf("parse error missing %q:\n%s", want, env.Error)
		}
	}
}

func TestApplyPatchParseErrorsGuideBadUpdateHeader(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	patch := strings.Join([]string{
		"*** Begin Patch",
		"Update File: a.txt",
		"@@",
		"-old",
		"+new",
		"*** End Patch",
	}, "\n")

	res, err := ts.applyPatch(context.Background(), tc("apply_patch", map[string]any{"patch": patch}))
	if err != nil {
		t.Fatalf("apply patch returned dispatch error: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected parse error, got %+v", res)
	}
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok {
		t.Fatalf("parse envelope: %s", res.Content)
	}
	if !strings.Contains(env.Error, "must start with the exact header *** Update File: <path>") {
		t.Fatalf("parse error did not explain update header:\n%s", env.Error)
	}
}

func TestReadFileRangeDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("zero\none\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	limitOnly, err := ts.readFile(context.Background(), tc("read_file", map[string]any{
		"file_path": "a.txt",
		"limit":     1,
	}))
	if err != nil || limitOnly.IsError {
		t.Fatalf("limit-only read failed: err=%v res=%+v", err, limitOnly)
	}
	limitData := readFileData(t, limitOnly)
	if got := rangeNumber(t, limitData, "start"); got != 0 {
		t.Fatalf("limit-only start = %d, want 0", got)
	}
	if got := rangeNumber(t, limitData, "end"); got != 1 {
		t.Fatalf("limit-only end = %d, want 1", got)
	}
	if content := readFileContent(t, limitData); content != "zero" {
		t.Fatalf("limit-only content = %q, want zero", content)
	}
	if note, _ := limitData["note"].(string); !strings.Contains(note, "offset was not provided; defaulted to 0") {
		t.Fatalf("missing offset default note in %#v", limitData["note"])
	}

	offsetOnly, err := ts.readFile(context.Background(), tc("read_file", map[string]any{
		"file_path": "a.txt",
		"offset":    1,
	}))
	if err != nil || offsetOnly.IsError {
		t.Fatalf("offset-only read failed: err=%v res=%+v", err, offsetOnly)
	}
	offsetData := readFileData(t, offsetOnly)
	if got := rangeNumber(t, offsetData, "start"); got != 1 {
		t.Fatalf("offset-only start = %d, want 1", got)
	}
	if got := rangeNumber(t, offsetData, "end"); got != 4 {
		t.Fatalf("offset-only end = %d, want 4", got)
	}
	if content := readFileContent(t, offsetData); content != "one\ntwo\nthree" {
		t.Fatalf("offset-only content = %q, want remaining lines", content)
	}
	if note, _ := offsetData["note"].(string); !strings.Contains(note, "limit was not provided; defaulted to 2000 lines") {
		t.Fatalf("missing limit default note in %#v", offsetData["note"])
	}

	fullRead, err := ts.readFile(context.Background(), tc("read_file", map[string]any{
		"file_path": "a.txt",
	}))
	if err != nil || fullRead.IsError {
		t.Fatalf("full read failed: err=%v res=%+v", err, fullRead)
	}
	fullData := readFileData(t, fullRead)
	if _, ok := fullData["note"]; ok {
		t.Fatalf("unexpected note for full read: %#v", fullData["note"])
	}

	scopedRead, err := ts.readFile(context.Background(), tc("read_file", map[string]any{
		"file_path": "a.txt",
		"offset":    1,
		"limit":     1,
	}))
	if err != nil || scopedRead.IsError {
		t.Fatalf("scoped read failed: err=%v res=%+v", err, scopedRead)
	}
	scopedData := readFileData(t, scopedRead)
	if _, ok := scopedData["note"]; ok {
		t.Fatalf("unexpected note for scoped read: %#v", scopedData["note"])
	}
}

func TestWriteAndEditReturnDiffMetadata(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	res, err := ts.editFile(context.Background(), tc("edit", map[string]any{
		"file_path": "a.txt",
		"search":    "world",
		"replace":   "whale",
	}))
	if err != nil || res.IsError {
		t.Fatalf("edit failed: err=%v res=%+v", err, res)
	}
	diff := firstMetadataDiff(t, res.Metadata)
	if !strings.Contains(diff, "-world") || !strings.Contains(diff, "+whale") {
		t.Fatalf("expected edit diff metadata, got:\n%s", diff)
	}

	res, err = ts.writeFile(context.Background(), tc("write", map[string]any{
		"file_path": "b.txt",
		"content":   "new file\n",
	}))
	if err != nil || res.IsError {
		t.Fatalf("write failed: err=%v res=%+v", err, res)
	}
	diff = firstMetadataDiff(t, res.Metadata)
	if !strings.Contains(diff, "+++ b/b.txt") || !strings.Contains(diff, "+new file") {
		t.Fatalf("expected write diff metadata, got:\n%s", diff)
	}
}

func TestApplyPatchPreviewAndResultMetadataMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	call := tc("apply_patch", map[string]any{"patch": strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: a.txt",
		"@@",
		" hello",
		"-world",
		"+whale",
		"*** End Patch",
	}, "\n")})

	preview, err := ts.previewApplyPatch(context.Background(), call)
	if err != nil {
		t.Fatalf("preview patch: %v", err)
	}
	res, err := ts.applyPatch(context.Background(), call)
	if err != nil || res.IsError {
		t.Fatalf("apply patch failed: err=%v res=%+v", err, res)
	}
	if got, want := firstMetadataDiff(t, res.Metadata), firstMetadataDiff(t, preview); got != want {
		t.Fatalf("preview/result diff mismatch:\npreview:\n%s\nresult:\n%s", want, got)
	}
}

func firstMetadataDiff(t *testing.T, metadata map[string]any) string {
	t.Helper()
	if metadata["kind"] != fileDiffMetadataKind {
		t.Fatalf("expected file diff metadata, got %+v", metadata)
	}
	files, ok := metadata["files"].([]map[string]any)
	if !ok || len(files) == 0 {
		t.Fatalf("expected metadata files, got %+v", metadata["files"])
	}
	diff, _ := files[0]["unified_diff"].(string)
	if diff == "" {
		t.Fatalf("expected unified diff, got %+v", files[0])
	}
	return diff
}

func TestPathEscapeDenied(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "inside.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	insideRes, err := ts.readFile(context.Background(), tc("read_file", map[string]any{
		"file_path": filepath.Join(dir, "inside.txt"),
	}))
	if err != nil || insideRes.IsError {
		t.Fatalf("expected absolute path inside workspace to be allowed: err=%v res=%+v", err, insideRes)
	}
	if !strings.Contains(insideRes.Content, "ok") {
		t.Fatalf("expected inside file content, got: %s", insideRes.Content)
	}
	res, err := ts.readFile(context.Background(), tc("read_file", map[string]any{
		"file_path": "../x",
	}))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "permission_denied") {
		t.Fatalf("expected permission_denied, got: %+v", res)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}
	res, err = ts.readFile(context.Background(), tc("read_file", map[string]any{
		"file_path": outside,
	}))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "permission_denied") {
		t.Fatalf("expected absolute outside path to be denied, got: %+v", res)
	}
}

func TestReadOnlyToolsCanReadDiscoveredGlobalSkillReferences(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	skillDir := filepath.Join(home, ".whale", "skills", "global-skill")
	refDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("---\nname: global-skill\ndescription: Global test skill.\n---\n\n# Global Skill\n\nUse global instructions.\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	refPath := filepath.Join(refDir, "guide.md")
	if err := os.WriteFile(refPath, []byte("reference marker\n"), 0o644); err != nil {
		t.Fatalf("write reference: %v", err)
	}
	ts, err := NewToolset(workspace)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	loadRes, err := ts.loadSkill(context.Background(), tc("load_skill", map[string]any{
		"name":      "global-skill",
		"arguments": "arg text",
	}))
	if err != nil || loadRes.IsError {
		t.Fatalf("load_skill failed: err=%v res=%+v", err, loadRes)
	}
	if !strings.Contains(loadRes.Content, "Use global instructions") || !strings.Contains(loadRes.Content, "arg text") {
		t.Fatalf("unexpected load_skill content: %s", loadRes.Content)
	}
	readRes, err := ts.readFile(context.Background(), tc("read_file", map[string]any{
		"file_path": refPath,
	}))
	if err != nil {
		t.Fatalf("read_file err: %v", err)
	}
	if readRes.IsError || !strings.Contains(readRes.Content, "reference marker") || !strings.Contains(readRes.Content, "$global-skill/references/guide.md") {
		t.Fatalf("expected read_file to read global skill reference, got: %+v", readRes)
	}

	lsRes, err := ts.listDir(context.Background(), tc("list_dir", map[string]any{
		"path": refDir,
	}))
	if err != nil || lsRes.IsError || !strings.Contains(lsRes.Content, "guide.md") {
		t.Fatalf("expected list_dir to list global skill reference dir: err=%v res=%+v", err, lsRes)
	}

	filesRes, err := ts.searchFiles(context.Background(), tc("search_files", map[string]any{
		"path":    skillDir,
		"pattern": "guide",
	}))
	if err != nil || filesRes.IsError || !strings.Contains(filesRes.Content, "$global-skill/references/guide.md") {
		t.Fatalf("expected search_files to search global skill dir: err=%v res=%+v", err, filesRes)
	}

	grepRes, err := ts.searchContent(context.Background(), tc("grep", map[string]any{
		"path":         skillDir,
		"pattern":      "reference marker",
		"literal_text": true,
	}))
	if err != nil || grepRes.IsError || !strings.Contains(grepRes.Content, "$global-skill/references/guide.md") {
		t.Fatalf("expected grep to search global skill dir: err=%v res=%+v", err, grepRes)
	}

	writeRes, err := ts.writeFile(context.Background(), tc("write", map[string]any{
		"file_path": refPath,
		"content":   "changed\n",
	}))
	if err != nil {
		t.Fatalf("write_file err: %v", err)
	}
	if !writeRes.IsError || !strings.Contains(writeRes.Content, "permission_denied") {
		t.Fatalf("expected write_file to deny global skill path, got: %+v", writeRes)
	}

	editRes, err := ts.editFile(context.Background(), tc("edit", map[string]any{
		"file_path": refPath,
		"search":    "reference",
		"replace":   "changed",
	}))
	if err != nil {
		t.Fatalf("edit_file err: %v", err)
	}
	if !editRes.IsError || !strings.Contains(editRes.Content, "permission_denied") {
		t.Fatalf("expected edit_file to deny global skill path, got: %+v", editRes)
	}
}

func TestSkillReadPathDoesNotFollowSymlinkOutsideSkillDir(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	skillDir := filepath.Join(home, ".whale", "skills", "global-skill")
	refDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: global-skill\ndescription: Global test skill.\n---\n\n# Global Skill\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside secret"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	link := filepath.Join(refDir, "outside.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	ts, err := NewToolset(workspace)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	res, err := ts.readFile(context.Background(), tc("read_file", map[string]any{
		"file_path": link,
	}))
	if err != nil {
		t.Fatalf("read_file err: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "permission_denied") {
		t.Fatalf("expected symlink escape to be denied, got: %+v", res)
	}
}

func TestDisabledSkillDoesNotExpandReadBoundary(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	skillDir := filepath.Join(home, ".whale", "skills", "disabled-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: disabled-skill\ndescription: Disabled test skill.\n---\n\n# Disabled\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	refPath := filepath.Join(skillDir, "notes.md")
	if err := os.WriteFile(refPath, []byte("disabled reference"), 0o644); err != nil {
		t.Fatalf("write reference: %v", err)
	}
	ts, err := NewToolset(workspace)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	ts.SetSkillDisabled([]string{"disabled-skill"})
	res, err := ts.readFile(context.Background(), tc("read_file", map[string]any{
		"file_path": refPath,
	}))
	if err != nil {
		t.Fatalf("read_file err: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "permission_denied") {
		t.Fatalf("expected disabled skill reference to be denied, got: %+v", res)
	}
}

func TestInvalidSkillDoesNotExpandReadBoundary(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	skillDir := filepath.Join(home, ".whale", "skills", "bad-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: bad-skill\n---\n\n# Missing description\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	refPath := filepath.Join(skillDir, "notes.md")
	if err := os.WriteFile(refPath, []byte("invalid reference"), 0o644); err != nil {
		t.Fatalf("write reference: %v", err)
	}
	ts, err := NewToolset(workspace)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	res, err := ts.readFile(context.Background(), tc("read_file", map[string]any{
		"file_path": refPath,
	}))
	if err != nil {
		t.Fatalf("read_file err: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "permission_denied") {
		t.Fatalf("expected invalid skill reference to be denied, got: %+v", res)
	}
}

func TestLoadSkillUnknownListsAvailableAndRegistryReadOnly(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	skillDir := filepath.Join(workspace, ".whale", "skills", "known-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: known-skill\ndescription: Known skill.\n---\n\n# Known\n"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	ts, err := NewToolset(workspace)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	res, err := ts.loadSkill(context.Background(), tc("load_skill", map[string]any{
		"name": "missing-skill",
	}))
	if err != nil {
		t.Fatalf("load_skill err: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "known-skill") {
		t.Fatalf("expected available skill in error, got: %s", res.Content)
	}
	var found bool
	for _, tool := range ts.Tools() {
		if tool.Name() == "load_skill" {
			found = true
			if !core.DescribeTool(tool).ReadOnly {
				t.Fatal("load_skill should be read-only")
			}
		}
	}
	if !found {
		t.Fatal("load_skill not registered")
	}
}

func TestLoadSkillFiltersDisabledPluginSkills(t *testing.T) {
	workspace := t.TempDir()
	ts, err := NewToolset(workspace)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	ts.SetExtraSkills([]*skills.Skill{{
		Name:          "plugin-skill",
		Description:   "Plugin skill.",
		Instructions:  "secret plugin instructions",
		SkillFilePath: "plugin://test/SKILL.md",
	}})
	ts.SetSkillDisabled([]string{"plugin-skill"})
	res, err := ts.loadSkill(context.Background(), tc("load_skill", map[string]any{
		"name": "plugin-skill",
	}))
	if err != nil {
		t.Fatalf("load_skill err: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "skill disabled") {
		t.Fatalf("expected disabled plugin skill to be rejected, got: %+v", res)
	}

	missing, err := ts.loadSkill(context.Background(), tc("load_skill", map[string]any{
		"name": "missing-skill",
	}))
	if err != nil {
		t.Fatalf("missing load_skill err: %v", err)
	}
	if strings.Contains(missing.Content, "plugin-skill") {
		t.Fatalf("disabled plugin skill leaked into available list: %s", missing.Content)
	}
}

func TestListDirAndShellRun(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	lsRes, err := ts.listDir(context.Background(), tc("list_dir", map[string]any{}))
	if err != nil || lsRes.IsError {
		t.Fatalf("ls failed: err=%v res=%+v", err, lsRes)
	}
	if !strings.Contains(lsRes.Content, "x.txt") {
		t.Fatalf("ls missing file: %s", lsRes.Content)
	}
	shellRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command": "echo hi",
	}))
	if err != nil || shellRes.IsError {
		t.Fatalf("shell_run failed: err=%v res=%+v", err, shellRes)
	}
	if !strings.Contains(shellRes.Content, "hi") {
		t.Fatalf("unexpected shell output: %s", shellRes.Content)
	}
}

func TestApplyPatchUpdateAddDelete(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	patch := strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: a.txt",
		"@@",
		" hello",
		"-world",
		"+whale",
		"*** Add File: b.txt",
		"+new file",
		"*** Delete File: a.txt",
		"*** End Patch",
	}, "\n")
	res, err := ts.applyPatch(context.Background(), tc("apply_patch", map[string]any{"patch": patch}))
	if err != nil {
		t.Fatalf("apply patch err: %v", err)
	}
	if res.IsError {
		t.Fatalf("apply patch result error: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected a.txt deleted, stat err=%v", err)
	}
	gotB, err := os.ReadFile(filepath.Join(dir, "b.txt"))
	if err != nil {
		t.Fatalf("read b.txt: %v", err)
	}
	if string(gotB) != "new file" {
		t.Fatalf("unexpected b.txt content: %q", string(gotB))
	}
}

func TestApplyPatchInvalidPatch(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	res, err := ts.applyPatch(context.Background(), tc("apply_patch", map[string]any{"patch": "bad patch"}))
	if err != nil {
		t.Fatalf("apply patch err: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "patch_parse_failed") {
		t.Fatalf("expected patch_parse_failed, got: %+v", res)
	}
}

func TestSearchFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "alpha.go"), []byte("package sub"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	res, err := ts.searchFiles(context.Background(), tc("search_files", map[string]any{
		"pattern": "alpha",
	}))
	if err != nil || res.IsError {
		t.Fatalf("search_files failed: err=%v res=%+v", err, res)
	}
	if !strings.Contains(res.Content, "alpha.go") {
		t.Fatalf("expected alpha.go in result: %s", res.Content)
	}
}

func TestShellRunBackgroundAndWait(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "echo hello",
		"background": true,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run background failed: err=%v res=%+v", err, startRes)
	}
	var envelope struct {
		Success bool `json:"success"`
		Data    struct {
			Payload struct {
				TaskID string `json:"task_id"`
			} `json:"payload"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(startRes.Content), &envelope); err != nil {
		t.Fatalf("unmarshal start result: %v", err)
	}
	if envelope.Data.Payload.TaskID == "" {
		t.Fatalf("expected task_id, got: %s", startRes.Content)
	}
	waitRes, err := ts.shellWait(context.Background(), tc("shell_wait", map[string]any{
		"task_id":    envelope.Data.Payload.TaskID,
		"timeout_ms": 5000,
	}))
	if err != nil || waitRes.IsError {
		t.Fatalf("shell_wait failed: err=%v res=%+v", err, waitRes)
	}
	if !strings.Contains(waitRes.Content, "hello") {
		t.Fatalf("expected output in wait result: %s", waitRes.Content)
	}
}

func TestShellRunBackgroundFinalWaitReleasesTask(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "echo cleanup",
		"background": true,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run background failed: err=%v res=%+v", err, startRes)
	}
	taskID := toolsetBackgroundTaskID(t, startRes.Content)

	waitRes, err := ts.shellWait(context.Background(), tc("shell_wait", map[string]any{
		"task_id":    taskID,
		"timeout_ms": 5000,
	}))
	if err != nil || waitRes.IsError {
		t.Fatalf("shell_wait failed: err=%v res=%+v", err, waitRes)
	}
	if !strings.Contains(waitRes.Content, `"done":true`) {
		t.Fatalf("expected final wait result, got: %s", waitRes.Content)
	}

	ts.tasks.mu.RLock()
	taskCount := len(ts.tasks.tasks)
	_, stillTracked := ts.tasks.tasks[taskID]
	ts.tasks.mu.RUnlock()
	if stillTracked || taskCount != 0 {
		t.Fatalf("expected final shell_wait to release task %s, registry has %d tasks", taskID, taskCount)
	}
}

func TestShellRunBackgroundCompletionSchedulesRegistryCleanup(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	type scheduledCleanup struct {
		delay time.Duration
		fn    func()
	}
	scheduled := make(chan scheduledCleanup, 1)
	ts.tasks.scheduleCleanup = func(delay time.Duration, fn func()) {
		select {
		case scheduled <- scheduledCleanup{delay: delay, fn: fn}:
		default:
		}
	}

	startRes, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command":    "echo cleanup",
		"background": true,
	}))
	if err != nil || startRes.IsError {
		t.Fatalf("shell_run background failed: err=%v res=%+v", err, startRes)
	}
	taskID := toolsetBackgroundTaskID(t, startRes.Content)

	var cleanup scheduledCleanup
	select {
	case cleanup = <-scheduled:
	case <-time.After(5 * time.Second):
		t.Fatal("expected background completion to schedule registry cleanup")
	}
	if cleanup.delay != shellTaskCompletedTTL {
		t.Fatalf("expected cleanup delay %s, got %s", shellTaskCompletedTTL, cleanup.delay)
	}

	ts.tasks.mu.RLock()
	task, stillTracked := ts.tasks.tasks[taskID]
	ts.tasks.mu.RUnlock()
	if !stillTracked {
		t.Fatalf("expected completed task %s to remain available before TTL cleanup", taskID)
	}

	expiredFinished := time.Now().Add(-shellTaskCompletedTTL - time.Second)
	task.mu.Lock()
	task.finishedAt = &expiredFinished
	task.mu.Unlock()

	cleanup.fn()

	ts.tasks.mu.RLock()
	_, stillTracked = ts.tasks.tasks[taskID]
	ts.tasks.mu.RUnlock()
	if stillTracked {
		t.Fatalf("expected scheduled cleanup to release expired task %s", taskID)
	}
}

func toolsetBackgroundTaskID(t *testing.T, content string) string {
	t.Helper()
	var envelope struct {
		Data struct {
			Payload struct {
				TaskID string `json:"task_id"`
			} `json:"payload"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(content), &envelope); err != nil {
		t.Fatalf("unmarshal background result: %v", err)
	}
	if envelope.Data.Payload.TaskID == "" {
		t.Fatalf("missing task_id in background result: %s", content)
	}
	return envelope.Data.Payload.TaskID
}

func TestShellRunCWDStaysInsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}
	res, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command": "pwd",
		"cwd":     "sub",
	}))
	if err != nil || res.IsError {
		t.Fatalf("shell_run cwd failed: err=%v res=%+v", err, res)
	}
	if !strings.Contains(res.Content, filepath.Join(dir, "sub")) || !strings.Contains(res.Content, `"cwd":"sub"`) {
		t.Fatalf("expected command to run in subdir with cwd metadata: %s", res.Content)
	}
	escaped, err := ts.shellRun(context.Background(), tc("shell_run", map[string]any{
		"command": "pwd",
		"cwd":     "../outside",
	}))
	if err != nil {
		t.Fatalf("shell_run escaped cwd returned dispatch error: %v", err)
	}
	if !escaped.IsError || !strings.Contains(escaped.Content, "path escapes workspace") {
		t.Fatalf("expected escaped cwd to be rejected: %+v", escaped)
	}
}

func readFileData(t *testing.T, res core.ToolResult) map[string]any {
	t.Helper()
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok {
		t.Fatalf("parse read_file envelope: %s", res.Content)
	}
	return env.Data
}

func rangeNumber(t *testing.T, data map[string]any, key string) int {
	t.Helper()
	payload, ok := data["payload"].(map[string]any)
	if !ok {
		t.Fatalf("missing payload in %#v", data)
	}
	rangeData, ok := payload["range"].(map[string]any)
	if !ok {
		t.Fatalf("missing range in %#v", payload)
	}
	v, ok := rangeData[key].(float64)
	if !ok {
		t.Fatalf("missing range %s in %#v", key, rangeData)
	}
	return int(v)
}

func readFileContent(t *testing.T, data map[string]any) string {
	t.Helper()
	payload, ok := data["payload"].(map[string]any)
	if !ok {
		t.Fatalf("missing payload in %#v", data)
	}
	content, ok := payload["content"].(string)
	if !ok {
		t.Fatalf("missing content in %#v", payload)
	}
	return content
}
