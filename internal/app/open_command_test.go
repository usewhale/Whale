package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestResolveOpenPathDefaultsToWorkspaceRoot(t *testing.T) {
	dir := t.TempDir()
	got, err := ResolveOpenPath(dir, "")
	if err != nil {
		t.Fatalf("ResolveOpenPath: %v", err)
	}
	if got != dir {
		t.Fatalf("path = %q, want %q", got, dir)
	}
}

func TestResolveOpenPathHandlesRelativeAbsoluteAndSpaces(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "My Folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "My Folder", "file.txt")
	if err := os.WriteFile(file, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveOpenPath(dir, "My Folder/file.txt")
	if err != nil {
		t.Fatalf("relative ResolveOpenPath: %v", err)
	}
	if got != file {
		t.Fatalf("relative path = %q, want %q", got, file)
	}

	got, err = ResolveOpenPath(dir, file)
	if err != nil {
		t.Fatalf("absolute ResolveOpenPath: %v", err)
	}
	if got != file {
		t.Fatalf("absolute path = %q, want %q", got, file)
	}
}

func TestResolveOpenPathMissingTarget(t *testing.T) {
	dir := t.TempDir()
	_, err := ResolveOpenPath(dir, "missing.txt")
	if err == nil || !strings.Contains(err.Error(), "open target does not exist") {
		t.Fatalf("expected missing target error, got %v", err)
	}
}

func TestResolveEditorCommandPriorityAndFallback(t *testing.T) {
	env := map[string]string{
		"EDITOR": "nano --line 3",
		"VISUAL": `"Code - Insiders" --wait`,
	}
	got, err := ResolveEditorCommand(func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}, "darwin")
	if err != nil {
		t.Fatalf("ResolveEditorCommand: %v", err)
	}
	want := []string{"Code - Insiders", "--wait"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("editor = %#v, want %#v", got, want)
	}

	got, err = ResolveOpenCommand(func(string) (string, bool) { return "", false }, fakeLookPath(nil), "windows", "")
	if err != nil {
		t.Fatalf("windows fallback: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"notepad"}) {
		t.Fatalf("windows fallback = %#v", got)
	}

	got, err = ResolveOpenCommand(func(string) (string, bool) { return "", false }, fakeLookPath(map[string]string{"vim": "/usr/bin/vim"}), "linux", "")
	if err != nil {
		t.Fatalf("linux fallback: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"/usr/bin/vim"}) {
		t.Fatalf("linux fallback = %#v", got)
	}
}

func TestResolveOpenCommandFallsBackToViAndReportsMissingUnixEditor(t *testing.T) {
	got, err := ResolveOpenCommand(func(string) (string, bool) { return "", false }, fakeLookPath(map[string]string{"vi": "/bin/vi"}), "linux", "")
	if err != nil {
		t.Fatalf("vi fallback: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"/bin/vi"}) {
		t.Fatalf("vi fallback = %#v", got)
	}

	_, err = ResolveOpenCommand(func(string) (string, bool) { return "", false }, fakeLookPath(nil), "darwin", "")
	if err == nil || !strings.Contains(err.Error(), "no editor found") {
		t.Fatalf("expected missing editor error, got %v", err)
	}
}

func TestResolveOpenCommandWindowsUsesExplorerForDirectories(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveOpenCommand(func(string) (string, bool) { return "", false }, fakeLookPath(nil), "windows", dir)
	if err != nil {
		t.Fatalf("windows directory fallback: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"explorer"}) {
		t.Fatalf("windows directory fallback = %#v", got)
	}

	got, err = ResolveOpenCommand(func(string) (string, bool) { return "", false }, fakeLookPath(nil), "windows", file)
	if err != nil {
		t.Fatalf("windows file fallback: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"notepad"}) {
		t.Fatalf("windows file fallback = %#v", got)
	}
}

func TestResolveEditorCommandRejectsUnterminatedQuote(t *testing.T) {
	_, err := ResolveEditorCommand(func(k string) (string, bool) {
		return `"unterminated`, true
	}, "darwin")
	if err == nil || !strings.Contains(err.Error(), "unterminated quote") {
		t.Fatalf("expected unterminated quote error, got %v", err)
	}
}

func TestExecuteLocalCommandOpenRunsEditor(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(target, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "fake-editor.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' \"$2\" > \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	outFile := filepath.Join(dir, "opened.txt")
	t.Setenv("VISUAL", script+" "+outFile)
	t.Setenv("EDITOR", "should-not-run")

	a := &App{workspaceRoot: dir}
	handled, out, synthetic, err := a.HandleLocalCommand("/open file.txt")
	if err != nil {
		t.Fatalf("HandleLocalCommand: %v", err)
	}
	if !handled {
		t.Fatal("expected /open handled")
	}
	if synthetic != "" {
		t.Fatalf("synthetic = %q, want empty", synthetic)
	}
	if out != OpenCommandSuccessText(target) {
		t.Fatalf("out = %q, want %q", out, OpenCommandSuccessText(target))
	}
	opened, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(opened) != target {
		t.Fatalf("opened target = %q, want %q", opened, target)
	}
}

func TestResolveEditorCommandKeepsWindowsBackslashes(t *testing.T) {
	got, err := ResolveEditorCommand(func(k string) (string, bool) {
		return `"C:\Program Files\Vim\vim.exe" --clean`, true
	}, "windows")
	if err != nil {
		t.Fatalf("ResolveEditorCommand: %v", err)
	}
	want := []string{`C:\Program Files\Vim\vim.exe`, "--clean"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("editor = %#v, want %#v", got, want)
	}
}

func fakeLookPath(paths map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if path, ok := paths[name]; ok {
			return path, nil
		}
		return "", os.ErrNotExist
	}
}
