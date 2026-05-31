package clipboard

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const defaultTimeout = 2 * time.Second

type Result struct {
	Chars       int
	Lines       int
	FilePath    string
	Native      bool
	Tmux        bool
	OSC52       bool
	NativeError string
	FileError   string
}

type Options struct {
	FallbackDir      string
	FallbackFilename string
	TermWriter       io.Writer
	LookupEnv        func(string) (string, bool)
	LookPath         func(string) (string, error)
	RunCommand       func(context.Context, string, []string, string) error
	GOOS             string
}

func CopyText(ctx context.Context, text string, opts Options) (Result, error) {
	opts = normalizeOptions(opts)
	res := Result{Chars: len(text), Lines: countLines(text)}

	if shouldUseNative(opts) {
		if err := copyNative(ctx, text, opts); err == nil {
			res.Native = true
		} else {
			res.NativeError = err.Error()
		}
	}

	osc := osc52(text)
	if _, ok := opts.LookupEnv("TMUX"); ok {
		if err := opts.RunCommand(ctx, "tmux", tmuxLoadBufferArgs(opts), text); err == nil {
			res.Tmux = true
			writeTerm(opts.TermWriter, tmuxPassthrough(osc))
		} else {
			writeTerm(opts.TermWriter, osc)
		}
	} else {
		writeTerm(opts.TermWriter, osc)
	}
	res.OSC52 = true

	filePath, err := writeFallback(text, opts.FallbackDir, opts.FallbackFilename)
	if err != nil {
		res.FileError = err.Error()
		return res, nil
	}
	res.FilePath = filePath
	return res, nil
}

func Summary(result Result) string {
	lineWord := "lines"
	if result.Lines == 1 {
		lineWord = "line"
	}
	text := fmt.Sprintf("Copied to clipboard (%d characters, %d %s)", result.Chars, result.Lines, lineWord)
	if strings.TrimSpace(result.FilePath) != "" {
		text += "\nAlso written to " + filepath.ToSlash(result.FilePath)
	}
	return text
}

func normalizeOptions(opts Options) Options {
	if opts.FallbackDir == "" {
		opts.FallbackDir = filepath.Join(os.TempDir(), "whale")
	}
	if opts.FallbackFilename == "" {
		opts.FallbackFilename = "response.md"
	}
	if opts.TermWriter == nil {
		opts.TermWriter = io.Discard
	}
	if opts.LookupEnv == nil {
		opts.LookupEnv = os.LookupEnv
	}
	if opts.LookPath == nil {
		opts.LookPath = exec.LookPath
	}
	if opts.RunCommand == nil {
		opts.RunCommand = runCommand
	}
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	return opts
}

func shouldUseNative(opts Options) bool {
	if _, disabled := opts.LookupEnv("WHALE_COPY_DISABLE_NATIVE"); disabled {
		return false
	}
	_, ssh := opts.LookupEnv("SSH_CONNECTION")
	return !ssh
}

func copyNative(ctx context.Context, text string, opts Options) error {
	switch opts.GOOS {
	case "darwin":
		return opts.RunCommand(ctx, "pbcopy", nil, text)
	case "windows":
		return opts.RunCommand(ctx, "clip", nil, text)
	case "linux":
		for _, candidate := range []struct {
			name string
			args []string
		}{
			{name: "wl-copy"},
			{name: "xclip", args: []string{"-selection", "clipboard"}},
			{name: "xsel", args: []string{"--clipboard", "--input"}},
		} {
			if _, err := opts.LookPath(candidate.name); err != nil {
				continue
			}
			if err := opts.RunCommand(ctx, candidate.name, candidate.args, text); err == nil {
				return nil
			}
		}
		return fmt.Errorf("no clipboard command found")
	default:
		return fmt.Errorf("native clipboard unsupported on %s", opts.GOOS)
	}
}

func runCommand(ctx context.Context, name string, args []string, stdin string) error {
	cmdCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("%s: %w: %s", name, err, msg)
		}
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func tmuxLoadBufferArgs(opts Options) []string {
	if lcTerminal, ok := opts.LookupEnv("LC_TERMINAL"); ok && strings.EqualFold(lcTerminal, "iTerm2") {
		return []string{"load-buffer", "-"}
	}
	return []string{"load-buffer", "-w", "-"}
}

func osc52(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
}

func tmuxPassthrough(seq string) string {
	escaped := strings.ReplaceAll(seq, "\x1b", "\x1b\x1b")
	return "\x1bPtmux;" + escaped + "\x1b\\"
}

func writeTerm(w io.Writer, seq string) {
	if strings.TrimSpace(seq) == "" {
		return
	}
	_, _ = io.WriteString(w, seq)
}

func writeFallback(text, dir, filename string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("write clipboard fallback: %w", err)
	}
	path := filepath.Join(dir, filepath.Clean(filename))
	if filepath.Dir(path) != filepath.Clean(dir) {
		path = filepath.Join(dir, "response.md")
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return "", fmt.Errorf("write clipboard fallback: %w", err)
	}
	return path, nil
}

func countLines(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}
