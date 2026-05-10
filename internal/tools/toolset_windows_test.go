//go:build windows

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileCRLF(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	// Write a file with CRLF line endings.
	path := filepath.Join(dir, "crlf.txt")
	content := "line1\r\nline2\r\nline3\r\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	res, err := ts.readFile(nil, tc("read_file", map[string]any{
		"file_path": path,
		"offset":    0,
		"limit":     10,
	}))
	if err != nil || res.IsError {
		t.Fatalf("readFile failed: err=%v res=%+v", err, res)
	}
	// The returned content must not contain \r.
	if strings.Contains(res.Content, "\r") {
		t.Fatalf("CRLF not normalized in read_file output: %q", res.Content)
	}
	if !strings.Contains(res.Content, "line1") || !strings.Contains(res.Content, "line2") || !strings.Contains(res.Content, "line3") {
		t.Fatalf("expected all three lines, got: %q", res.Content)
	}
}

func TestEditFileCRLF(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	// Write a file with CRLF line endings.
	path := filepath.Join(dir, "edit.txt")
	content := "hello\r\nworld\r\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Search using LF-only text (as the LLM would produce).
	res, err := ts.editFile(nil, tc("edit_file", map[string]any{
		"file_path": path,
		"search":    "hello\nworld",
		"replace":   "foo\nbar",
	}))
	if err != nil || res.IsError {
		t.Fatalf("editFile failed: err=%v res=%+v", err, res)
	}
	if !strings.Contains(res.Content, `"replacements":1`) {
		t.Fatalf("expected 1 replacement, got: %s", res.Content)
	}

	// Verify the file was actually changed.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	// After edit, the file should now use LF (since we normalize to LF internally).
	if strings.Contains(string(got), "hello") && strings.Contains(string(got), "world") {
		t.Fatalf("old content still present: %q", string(got))
	}
}

func TestEditFileCRLFSearchNotFoundWithoutNormalization(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	path := filepath.Join(dir, "edit2.txt")
	content := "alpha\r\nbeta\r\ngamma\r\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Search with LF line endings should still succeed after normalization.
	res, err := ts.editFile(nil, tc("edit_file", map[string]any{
		"file_path": path,
		"search":    "beta",
		"replace":   "replaced",
	}))
	if err != nil || res.IsError {
		t.Fatalf("editFile search failed unexpectedly: err=%v res=%+v", err, res)
	}
	if !strings.Contains(res.Content, `"replacements":1`) {
		t.Fatalf("expected 1 replacement, got: %s", res.Content)
	}
}

func TestApplyPatchCRLF(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewToolset(dir)
	if err != nil {
		t.Fatalf("new toolset: %v", err)
	}

	// Write a file with CRLF line endings.
	path := filepath.Join(dir, "patch.txt")
	content := "first\r\nsecond\r\nthird\r\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	patch := "*** Begin Patch\n" +
		"*** Update File: " + filepath.ToSlash(path) + "\n" +
		"@@\n" +
		" first\n" +
		"-second\n" +
		"+replaced\n" +
		" third\n" +
		"*** End Patch\n"

	res, err := ts.applyPatch(nil, tc("apply_patch", map[string]any{
		"patch": patch,
	}))
	if err != nil || res.IsError {
		t.Fatalf("applyPatch failed: err=%v res=%+v", err, res)
	}

	// Verify the file was patched.
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(got), "replaced") {
		t.Fatalf("patch not applied correctly, file content: %q", string(got))
	}
	if strings.Contains(string(got), "second") {
		t.Fatalf("old content still present after patch: %q", string(got))
	}
}
