package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	diffCommandTimeout    = 5 * time.Second
	diffMaxOutputBytes    = 2 * 1024 * 1024
	diffMaxUntrackedSize  = 256 * 1024
	diffMaxUntrackedFiles = 200
)

func (a *App) BuildDiffText(ctx context.Context) string {
	if ctx == nil {
		ctx = context.Background()
	}
	out, err := buildDiffText(ctx, a.workspaceRoot)
	if err != nil {
		return "Failed to compute diff: " + err.Error()
	}
	return out
}

func buildDiffText(ctx context.Context, workspaceRoot string) (string, error) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		workspaceRoot = "."
	}
	if ok, err := insideGitRepo(ctx, workspaceRoot); err != nil {
		return "", err
	} else if !ok {
		return "`/diff` - not inside a git repository", nil
	}
	if transient, err := inTransientGitState(ctx, workspaceRoot); err != nil {
		return "", err
	} else if transient {
		return "`/diff` is unavailable during merge, rebase, cherry-pick, or revert operations.", nil
	}

	tracked, err := trackedDiff(ctx, workspaceRoot)
	if err != nil {
		return "", err
	}
	untracked, err := untrackedDiff(ctx, workspaceRoot, diffMaxOutputBytes-len(tracked))
	if err != nil {
		return "", err
	}
	text := tracked + untracked
	if strings.TrimSpace(stripANSI(text)) == "" {
		return "No changes detected.", nil
	}
	return truncateDiffOutput(text), nil
}

func trackedDiff(ctx context.Context, cwd string) (string, error) {
	hasHead, err := hasGitHead(ctx, cwd)
	if err != nil {
		return "", err
	}
	if hasHead {
		return runGitDiff(ctx, cwd, "diff", "--no-ext-diff", "--no-textconv", "--color=always", "HEAD")
	}
	cached, err := runGitDiff(ctx, cwd, "diff", "--no-ext-diff", "--no-textconv", "--color=always", "--cached")
	if err != nil {
		return "", err
	}
	remaining := diffMaxOutputBytes - len(cached)
	if remaining <= 0 {
		return truncateDiffOutput(cached), nil
	}
	worktree, err := runGitDiffLimited(ctx, cwd, remaining, "diff", "--no-ext-diff", "--no-textconv", "--color=always")
	if err != nil {
		return "", err
	}
	return cached + worktree, nil
}

func insideGitRepo(ctx context.Context, cwd string) (bool, error) {
	_, code, err := runGit(ctx, cwd, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return false, nil
	}
	return code == 0, nil
}

func gitTopLevel(ctx context.Context, cwd string) (string, error) {
	out, code, err := runGit(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("git rev-parse --show-toplevel failed with status %d", code)
	}
	return strings.TrimSpace(out), nil
}

func hasGitHead(ctx context.Context, cwd string) (bool, error) {
	_, code, err := runGit(ctx, cwd, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return false, err
	}
	return code == 0, nil
}

func inTransientGitState(ctx context.Context, cwd string) (bool, error) {
	gitDir, code, err := runGit(ctx, cwd, "rev-parse", "--git-dir")
	if err != nil || code != 0 {
		return false, err
	}
	gitDir = strings.TrimSpace(gitDir)
	if gitDir == "" {
		return false, nil
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(cwd, gitDir)
	}
	for _, name := range []string{"MERGE_HEAD", "REBASE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD"} {
		if _, err := os.Stat(filepath.Join(gitDir, name)); err == nil {
			return true, nil
		}
	}
	return false, nil
}

func untrackedDiff(ctx context.Context, cwd string, budget int) (string, error) {
	// git ls-files only reports paths under its working directory, so running it
	// from a launch subdirectory would omit untracked files elsewhere in the
	// worktree even though the tracked `git diff HEAD` above covers the whole
	// repo. Resolve the worktree top-level so both halves stay consistent.
	root, err := gitTopLevel(ctx, cwd)
	if err != nil || root == "" {
		root = cwd
	}
	out, code, err := runGit(ctx, root, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil || code != 0 {
		return "", err
	}
	files := splitGitPaths(out)
	if len(files) == 0 {
		return "", nil
	}
	if budget <= 0 {
		return fmt.Sprintf("\n... %d untracked file(s) omitted: diff size limit reached ...\n", len(files)), nil
	}
	nullPath := "/dev/null"
	if runtime.GOOS == "windows" {
		nullPath = "NUL"
	}
	var b strings.Builder
	for i, file := range files {
		if b.Len() >= budget {
			fmt.Fprintf(&b, "\n... %d untracked file(s) omitted: diff size limit reached ...\n", len(files)-i)
			break
		}
		if i >= diffMaxUntrackedFiles {
			fmt.Fprintf(&b, "\n... %d untracked file(s) omitted: file count limit reached ...\n", len(files)-i)
			break
		}
		path := filepath.Join(root, filepath.FromSlash(file))
		info, err := os.Lstat(path)
		if err != nil {
			fmt.Fprintf(&b, "\ndiff --git a/%s b/%s\nuntracked file unavailable: %v\n", file, file, err)
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			diff, err := untrackedSymlinkDiff(file, path)
			if err != nil {
				fmt.Fprintf(&b, "\ndiff --git a/%s b/%s\nuntracked symlink diff unavailable: %v\n", file, file, err)
				continue
			}
			b.WriteString(diff)
			continue
		}
		if info.IsDir() {
			fmt.Fprintf(&b, "\ndiff --git a/%s b/%s\nuntracked directory omitted: directory diff preview is not supported\n", file, file)
			continue
		}
		if info.Size() > diffMaxUntrackedSize {
			fmt.Fprintf(&b, "\ndiff --git a/%s b/%s\nuntracked file omitted: exceeds %d byte preview limit\n", file, file, diffMaxUntrackedSize)
			continue
		}
		remaining := budget - b.Len()
		diff, err := runGitDiffLimited(ctx, root, remaining, "diff", "--no-ext-diff", "--no-textconv", "--color=always", "--no-index", "--", nullPath, file)
		if err != nil {
			fmt.Fprintf(&b, "\ndiff --git a/%s b/%s\nuntracked file diff unavailable: %v\n", file, file, err)
			continue
		}
		b.WriteString(diff)
		if len(diff) >= remaining {
			fmt.Fprintf(&b, "\n... %d untracked file(s) omitted: diff size limit reached ...\n", len(files)-i-1)
			break
		}
	}
	return b.String(), nil
}

func untrackedSymlinkDiff(file, path string) (string, error) {
	target, err := os.Readlink(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("diff --git a/%s b/%s\nnew file mode 120000\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1 @@\n+%s\n\\ No newline at end of file\n", file, file, file, target), nil
}

func runGitDiff(ctx context.Context, cwd string, args ...string) (string, error) {
	return runGitDiffLimited(ctx, cwd, diffMaxOutputBytes, args...)
}

func runGitDiffLimited(ctx context.Context, cwd string, limit int, args ...string) (string, error) {
	out, code, truncated, err := runGitLimited(ctx, cwd, limit, args...)
	if err != nil {
		return "", err
	}
	if truncated {
		return withDiffTruncatedNotice(out), nil
	}
	if code == 0 || code == 1 {
		return out, nil
	}
	return "", fmt.Errorf("git %s failed with status %d", strings.Join(args, " "), code)
}

func runGit(ctx context.Context, cwd string, args ...string) (string, int, error) {
	out, code, _, err := runGitLimited(ctx, cwd, diffMaxOutputBytes, args...)
	return out, code, err
}

func runGitLimited(ctx context.Context, cwd string, limit int, args ...string) (string, int, bool, error) {
	if limit <= 0 {
		limit = 1
	}
	cmdCtx, cancel := context.WithTimeout(ctx, diffCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "git", append([]string{"--no-optional-locks"}, args...)...)
	cmd.Dir = cwd
	cmd.Env = gitDiffCommandEnv(os.Environ())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", -1, false, err
	}
	if err := cmd.Start(); err != nil {
		return "", -1, false, err
	}
	var out bytes.Buffer
	truncated := false
	buf := make([]byte, 32*1024)
	for {
		n, readErr := stdout.Read(buf)
		if n > 0 {
			remaining := limit - out.Len()
			if remaining > 0 {
				if n > remaining {
					out.Write(buf[:remaining])
					truncated = true
				} else {
					out.Write(buf[:n])
				}
			} else {
				truncated = true
			}
			if truncated && cmd.Process != nil {
				_ = cmd.Process.Kill()
				break
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				return "", -1, false, readErr
			}
			break
		}
	}
	err = cmd.Wait()
	if cmdCtx.Err() == context.DeadlineExceeded {
		return "", -1, truncated, fmt.Errorf("git %s timed out", strings.Join(args, " "))
	}
	if truncated {
		return out.String(), 1, true, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return out.String(), exitErr.ExitCode(), false, nil
	}
	if err != nil {
		return "", -1, false, err
	}
	return out.String(), 0, false, nil
}

func gitDiffCommandEnv(base []string) []string {
	env := make([]string, 0, len(base)+2)
	for _, item := range base {
		if strings.HasPrefix(item, "GIT_EXTERNAL_DIFF=") {
			continue
		}
		env = append(env, item)
	}
	env = append(env, "GIT_PAGER=cat", "PAGER=cat")
	return env
}

func truncateDiffOutput(s string) string {
	if len(s) <= diffMaxOutputBytes {
		return s
	}
	cut := s[:diffMaxOutputBytes]
	return withDiffTruncatedNotice(cut)
}

func withDiffTruncatedNotice(cut string) string {
	if idx := strings.LastIndex(cut, "\n"); idx > 0 {
		cut = cut[:idx]
	}
	return cut + "\n... diff truncated ...\n"
}

func stripANSI(s string) string {
	var b strings.Builder
	state := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch state {
		case 1:
			if c == '[' {
				state = 2
			} else {
				state = 0
			}
			continue
		case 2:
			if c >= '@' && c <= '~' {
				state = 0
			}
			continue
		}
		if c == 0x1b {
			state = 1
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func splitGitPaths(s string) []string {
	raw := strings.Split(strings.TrimRight(s, "\x00"), "\x00")
	out := make([]string, 0, len(raw))
	for _, path := range raw {
		if path != "" {
			out = append(out, path)
		}
	}
	return out
}
