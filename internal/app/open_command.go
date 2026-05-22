package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type OpenCommand struct {
	Path string
	Cmd  *exec.Cmd
}

func IsOpenCommandLine(line string) bool {
	fields := strings.Fields(strings.TrimSpace(line))
	return len(fields) > 0 && fields[0] == "/open"
}

func (a *App) PrepareOpenCommand(line string) (OpenCommand, error) {
	target, err := ResolveOpenPath(a.workspaceRoot, openCommandArg(line))
	if err != nil {
		return OpenCommand{}, err
	}
	parts, err := ResolveOpenCommand(os.LookupEnv, exec.LookPath, runtime.GOOS, target)
	if err != nil {
		return OpenCommand{}, err
	}
	args := append(append([]string{}, parts[1:]...), target)
	cmd := exec.Command(parts[0], args...)
	if a.workspaceRoot != "" {
		cmd.Dir = a.workspaceRoot
	}
	return OpenCommand{Path: target, Cmd: cmd}, nil
}

func (a *App) ExecuteOpenCommand(line string) (string, error) {
	openCmd, err := a.PrepareOpenCommand(line)
	if err != nil {
		return "", err
	}
	openCmd.Cmd.Stdin = os.Stdin
	openCmd.Cmd.Stdout = os.Stdout
	openCmd.Cmd.Stderr = os.Stderr
	if err := openCmd.Cmd.Run(); err != nil {
		return "", fmt.Errorf("open editor: %w", err)
	}
	return OpenCommandSuccessText(openCmd.Path), nil
}

func OpenCommandSuccessText(path string) string {
	return "Opened " + path
}

func openCommandArg(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "/open" {
		return ""
	}
	if strings.HasPrefix(trimmed, "/open ") || strings.HasPrefix(trimmed, "/open\t") {
		return strings.TrimSpace(trimmed[len("/open"):])
	}
	return ""
}

func ResolveOpenPath(workspaceRoot, raw string) (string, error) {
	base := strings.TrimSpace(workspaceRoot)
	if base == "" {
		base = "."
	}
	target := strings.TrimSpace(raw)
	if target == "" {
		target = base
	} else if !filepath.IsAbs(target) {
		target = filepath.Join(base, target)
	}
	abs, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("open target does not exist: %s", abs)
		}
		return "", err
	}
	return abs, nil
}

func ResolveEditorCommand(lookupEnv func(string) (string, bool), goos string) ([]string, error) {
	return ResolveOpenCommand(lookupEnv, exec.LookPath, goos, "")
}

func ResolveOpenCommand(lookupEnv func(string) (string, bool), lookupPath func(string) (string, error), goos, target string) ([]string, error) {
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if value, ok := lookupEnv(name); ok && strings.TrimSpace(value) != "" {
			parts, err := splitEditorCommand(value)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			if len(parts) == 0 {
				continue
			}
			return parts, nil
		}
	}
	if goos == "windows" {
		if target != "" {
			if info, err := os.Stat(target); err == nil && info.IsDir() {
				return []string{"explorer"}, nil
			}
		}
		return []string{"notepad"}, nil
	}
	for _, editor := range []string{"vim", "vi"} {
		path, err := lookupPath(editor)
		if err == nil && strings.TrimSpace(path) != "" {
			return []string{path}, nil
		}
	}
	switch goos {
	case "darwin", "linux":
		return nil, errors.New("no editor found: set VISUAL or EDITOR, or install vim/vi")
	default:
		return []string{"vi"}, nil
	}
}

func splitEditorCommand(input string) ([]string, error) {
	var out []string
	var b strings.Builder
	var quote rune
	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '\\' && quote != '\'' && i+1 < len(runes) && isEditorEscapable(runes[i+1]) {
			i++
			b.WriteRune(runes[i])
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			b.WriteRune(r)
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case ' ', '\t', '\n', '\r':
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	if len(out) == 0 {
		return nil, errors.New("empty editor command")
	}
	return out, nil
}

func isEditorEscapable(r rune) bool {
	switch r {
	case '\\', '\'', '"', ' ', '\t', '\n', '\r':
		return true
	default:
		return false
	}
}
