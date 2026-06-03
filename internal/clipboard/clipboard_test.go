package clipboard

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCopyTextUsesNativeOSC52AndFallback(t *testing.T) {
	dir := t.TempDir()
	var term bytes.Buffer
	var calls []string
	res, err := CopyText(context.Background(), "hello\nworld", Options{
		FallbackDir:      dir,
		FallbackFilename: "response.md",
		TermWriter:       &term,
		GOOS:             "darwin",
		LookupEnv: func(string) (string, bool) {
			return "", false
		},
		RunCommand: func(_ context.Context, name string, args []string, stdin string) error {
			calls = append(calls, name+":"+stdin)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("CopyText: %v", err)
	}
	if !res.Native || !res.OSC52 || res.Tmux {
		t.Fatalf("unexpected result: %+v", res)
	}
	if want := []string{"pbcopy:hello\nworld"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("commands = %#v, want %#v", calls, want)
	}
	if !strings.Contains(term.String(), "\x1b]52;c;") {
		t.Fatalf("expected OSC52 sequence, got %q", term.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "response.md"))
	if err != nil {
		t.Fatalf("read fallback: %v", err)
	}
	if string(got) != "hello\nworld" {
		t.Fatalf("fallback = %q", got)
	}
}

func TestCopyTextUsesTmuxBufferWhenAvailable(t *testing.T) {
	var term bytes.Buffer
	var calls []string
	env := map[string]string{"TMUX": "/tmp/tmux"}
	res, err := CopyText(context.Background(), "text", Options{
		FallbackDir:      t.TempDir(),
		FallbackFilename: "response.md",
		TermWriter:       &term,
		GOOS:             "linux",
		LookupEnv: func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		},
		LookPath: func(name string) (string, error) {
			return "", os.ErrNotExist
		},
		RunCommand: func(_ context.Context, name string, args []string, stdin string) error {
			calls = append(calls, name+" "+strings.Join(args, " "))
			return nil
		},
	})
	if err != nil {
		t.Fatalf("CopyText: %v", err)
	}
	if !res.Tmux || !res.OSC52 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if len(calls) != 1 || calls[0] != "tmux load-buffer -w -" {
		t.Fatalf("commands = %#v", calls)
	}
	if !strings.HasPrefix(term.String(), "\x1bPtmux;") {
		t.Fatalf("expected tmux passthrough, got %q", term.String())
	}
}

func TestCopyTextNativeCommandsByPlatform(t *testing.T) {
	tests := []struct {
		name      string
		goos      string
		lookPath  func(string) (string, error)
		wantCalls []string
	}{
		{
			name:      "macOS",
			goos:      "darwin",
			wantCalls: []string{"pbcopy"},
		},
		{
			name:      "Windows",
			goos:      "windows",
			wantCalls: []string{"clip"},
		},
		{
			name: "Linux wl-copy",
			goos: "linux",
			lookPath: func(name string) (string, error) {
				if name == "wl-copy" {
					return "/usr/bin/wl-copy", nil
				}
				return "", os.ErrNotExist
			},
			wantCalls: []string{"wl-copy"},
		},
		{
			name: "Linux xclip fallback",
			goos: "linux",
			lookPath: func(name string) (string, error) {
				if name == "xclip" {
					return "/usr/bin/xclip", nil
				}
				return "", os.ErrNotExist
			},
			wantCalls: []string{"xclip -selection clipboard"},
		},
		{
			name: "Linux xsel fallback",
			goos: "linux",
			lookPath: func(name string) (string, error) {
				if name == "xsel" {
					return "/usr/bin/xsel", nil
				}
				return "", os.ErrNotExist
			},
			wantCalls: []string{"xsel --clipboard --input"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls []string
			_, err := CopyText(context.Background(), "text", Options{
				FallbackDir:      t.TempDir(),
				FallbackFilename: "response.md",
				TermWriter:       ioDiscard{},
				GOOS:             tt.goos,
				LookupEnv: func(string) (string, bool) {
					return "", false
				},
				LookPath: tt.lookPath,
				RunCommand: func(_ context.Context, name string, args []string, stdin string) error {
					if stdin != "text" {
						t.Fatalf("stdin = %q, want text", stdin)
					}
					calls = append(calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
					return nil
				},
			})
			if err != nil {
				t.Fatalf("CopyText: %v", err)
			}
			if !reflect.DeepEqual(calls, tt.wantCalls) {
				t.Fatalf("commands = %#v, want %#v", calls, tt.wantCalls)
			}
		})
	}
}

func TestCopyTextSkipsNativeOverSSH(t *testing.T) {
	var calls []string
	env := map[string]string{"SSH_CONNECTION": "host"}
	_, err := CopyText(context.Background(), "text", Options{
		FallbackDir:      t.TempDir(),
		FallbackFilename: "response.md",
		TermWriter:       ioDiscard{},
		GOOS:             "darwin",
		LookupEnv: func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		},
		RunCommand: func(_ context.Context, name string, args []string, stdin string) error {
			calls = append(calls, name)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("CopyText: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("expected no native command over SSH, got %#v", calls)
	}
}

func TestTmuxLoadBufferDropsClipboardFlagForITerm2(t *testing.T) {
	env := map[string]string{
		"TMUX":        "/tmp/tmux",
		"LC_TERMINAL": "iTerm2",
	}
	var calls []string
	_, err := CopyText(context.Background(), "text", Options{
		FallbackDir:      t.TempDir(),
		FallbackFilename: "response.md",
		TermWriter:       ioDiscard{},
		GOOS:             "linux",
		LookupEnv: func(key string) (string, bool) {
			v, ok := env[key]
			return v, ok
		},
		LookPath: func(name string) (string, error) {
			return "", os.ErrNotExist
		},
		RunCommand: func(_ context.Context, name string, args []string, stdin string) error {
			calls = append(calls, fmt.Sprintf("%s %s", name, strings.Join(args, " ")))
			return nil
		},
	})
	if err != nil {
		t.Fatalf("CopyText: %v", err)
	}
	if len(calls) != 1 || calls[0] != "tmux load-buffer -" {
		t.Fatalf("commands = %#v", calls)
	}
}

func TestSummaryIncludesCountsAndFallback(t *testing.T) {
	got := Summary(Result{Chars: 5, Lines: 1, FilePath: "/tmp/whale/response.md"})
	for _, want := range []string{"Copied to clipboard (5 characters, 1 line)", "Also written to /tmp/whale/response.md"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Summary() = %q, missing %q", got, want)
		}
	}
}

func TestCopyTextTreatsFallbackWriteAsBestEffort(t *testing.T) {
	res, err := CopyText(context.Background(), "text", Options{
		FallbackDir: "/dev/null",
		TermWriter:  ioDiscard{},
		GOOS:        "darwin",
		RunCommand: func(_ context.Context, name string, args []string, stdin string) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("CopyText should not fail when fallback write fails: %v", err)
	}
	if res.FileError == "" {
		t.Fatalf("expected fallback write error, got %+v", res)
	}
	if !res.Native || !res.OSC52 {
		t.Fatalf("expected clipboard paths to still be attempted, got %+v", res)
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
