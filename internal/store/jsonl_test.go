package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultDataDirIgnoresHomeOnWindows(t *testing.T) {
	got := defaultDataDir("windows", getenv(map[string]string{
		"HOME": `C:\msys64\home\goranka`,
	}), func() (string, error) {
		return `C:\Users\goranka`, nil
	})
	want := filepath.Join(`C:\Users\goranka`, ".whale")
	if got != want {
		t.Fatalf("defaultDataDir windows = %q, want %q", got, want)
	}
}

func TestDefaultDataDirUsesWhaleHomeOverride(t *testing.T) {
	got := defaultDataDir("windows", getenv(map[string]string{
		DataDirEnv: `D:\WhaleData`,
		"HOME":     `C:\msys64\home\goranka`,
	}), func() (string, error) {
		return `C:\Users\goranka`, nil
	})
	if got != `D:\WhaleData` {
		t.Fatalf("defaultDataDir with %s = %q, want D:\\WhaleData", DataDirEnv, got)
	}
}

func TestDefaultDataDirIgnoresBlankWhaleHomeOverride(t *testing.T) {
	got := defaultDataDir("linux", getenv(map[string]string{
		DataDirEnv: "  ",
		"HOME":     "/home/dev",
	}), func() (string, error) {
		return "/ignored", nil
	})
	want := filepath.Join("/home/dev", ".whale")
	if got != want {
		t.Fatalf("defaultDataDir with blank %s = %q, want %q", DataDirEnv, got, want)
	}
}

func TestDefaultDataDirDoesNotFallbackToHomeOnWindows(t *testing.T) {
	got := defaultDataDir("windows", getenv(map[string]string{
		"HOME": `C:\msys64\home\goranka`,
	}), func() (string, error) {
		return "", errors.New("user home unavailable")
	})
	if got != ".whale" {
		t.Fatalf("defaultDataDir windows without user home = %q, want .whale", got)
	}
}

func TestDefaultDataDirUsesHomeOnNonWindows(t *testing.T) {
	got := defaultDataDir("linux", getenv(map[string]string{
		"HOME": "/home/dev",
	}), func() (string, error) {
		return "/ignored", nil
	})
	want := filepath.Join("/home/dev", ".whale")
	if got != want {
		t.Fatalf("defaultDataDir linux = %q, want %q", got, want)
	}
}

func TestDefaultDataDirFallsBackToUserHomeOnNonWindows(t *testing.T) {
	got := defaultDataDir("darwin", getenv(nil), func() (string, error) {
		return "/Users/dev", nil
	})
	want := filepath.Join("/Users/dev", ".whale")
	if got != want {
		t.Fatalf("defaultDataDir darwin = %q, want %q", got, want)
	}
}

func TestMostRecentSessionIDIgnoresToolInputEventSidecars(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "s1.jsonl")
	sidecarPath := filepath.Join(dir, "s1.tool_input_events.jsonl")
	if err := os.WriteFile(sessionPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	if err := os.WriteFile(sidecarPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	now := time.Now()
	_ = os.Chtimes(sessionPath, now.Add(-time.Hour), now.Add(-time.Hour))
	_ = os.Chtimes(sidecarPath, now, now)

	got, err := MostRecentSessionID(dir)
	if err != nil {
		t.Fatalf("most recent session: %v", err)
	}
	if got != "s1" {
		t.Fatalf("expected s1, got %q", got)
	}
}

func getenv(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}
