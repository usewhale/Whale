package securefs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWritePrivateFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "data.json")
	if err := WritePrivateFile(path, []byte("secret")); err != nil {
		t.Fatalf("WritePrivateFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "secret" {
		t.Fatalf("content = %q, want secret", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if got := info.Mode().Perm(); got != privateFileMode {
			t.Fatalf("mode = %o, want %o", got, privateFileMode)
		}
	}
}

func TestWritePrivateFileTightensExistingFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits are not the Windows privacy boundary")
	}
	path := filepath.Join(t.TempDir(), "private", "data.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if err := WritePrivateFile(path, []byte("secret")); err != nil {
		t.Fatalf("WritePrivateFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != privateFileMode {
		t.Fatalf("mode = %o, want %o", got, privateFileMode)
	}
}

func TestMkdirPrivateTightensExistingDirectoryMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits are not the Windows privacy boundary")
	}
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := MkdirPrivate(dir); err != nil {
		t.Fatalf("MkdirPrivate: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != privateDirMode {
		t.Fatalf("mode = %o, want %o", got, privateDirMode)
	}
}

func TestOpenPrivateAppendAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "events.jsonl")
	f, err := OpenPrivateAppend(path)
	if err != nil {
		t.Fatalf("OpenPrivateAppend: %v", err)
	}
	if _, err := f.WriteString("one\n"); err != nil {
		t.Fatalf("write first: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}
	f, err = OpenPrivateAppend(path)
	if err != nil {
		t.Fatalf("OpenPrivateAppend second: %v", err)
	}
	if _, err := f.WriteString("two\n"); err != nil {
		t.Fatalf("write second: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close second: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "one\ntwo\n" {
		t.Fatalf("content = %q", got)
	}
}
