package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuildDiffTextNonGitRepo(t *testing.T) {
	out, err := buildDiffText(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("buildDiffText: %v", err)
	}
	if !strings.Contains(out, "not inside a git repository") {
		t.Fatalf("expected non-git message, got:\n%s", out)
	}
}

func TestBuildDiffTextIncludesTrackedAndUntrackedChanges(t *testing.T) {
	repo := newAppGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new file.txt"), []byte("new content\n"), 0o600); err != nil {
		t.Fatalf("write untracked: %v", err)
	}

	out, err := buildDiffText(t.Context(), repo)
	if err != nil {
		t.Fatalf("buildDiffText: %v", err)
	}
	plain := stripANSI(out)
	for _, want := range []string{"README.md", "-test", "+changed", "new file.txt", "+new content"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected diff to contain %q:\n%s", want, plain)
		}
	}
}

func TestBuildDiffTextDisablesExternalDiff(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script as the external diff driver")
	}
	repo := newAppGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "external-diff-ran")
	script := filepath.Join(dir, "external-diff.sh")
	body := "#!/bin/sh\nprintf ran > " + shellQuote(marker) + "\nexit 2\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("write external diff script: %v", err)
	}
	t.Setenv("GIT_EXTERNAL_DIFF", script)

	out, err := buildDiffText(t.Context(), repo)
	if err != nil {
		t.Fatalf("buildDiffText: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("external diff driver should not run, stat err=%v", err)
	}
	if plain := stripANSI(out); !strings.Contains(plain, "+changed") {
		t.Fatalf("expected normal git diff output, got:\n%s", plain)
	}
}

func TestBuildDiffTextDisablesTextconv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell script as the textconv driver")
	}
	repo := newAppGitRepo(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "textconv-ran")
	script := filepath.Join(dir, "textconv.sh")
	body := "#!/bin/sh\nprintf ran > " + shellQuote(marker) + "\ncat \"$1\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("write textconv script: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("README.md diff=whaleconv\n"), 0o600); err != nil {
		t.Fatalf("write gitattributes: %v", err)
	}
	runAppGit(t, repo, "add", ".gitattributes")
	runAppGit(t, repo, "commit", "-m", "attributes")
	runAppGit(t, repo, "config", "diff.whaleconv.textconv", script)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}

	out, err := buildDiffText(t.Context(), repo)
	if err != nil {
		t.Fatalf("buildDiffText: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("textconv driver should not run, stat err=%v", err)
	}
	if plain := stripANSI(out); !strings.Contains(plain, "+changed") {
		t.Fatalf("expected raw git diff output, got:\n%s", plain)
	}
}

func TestBuildDiffTextCapsLargeTrackedOutput(t *testing.T) {
	repo := newAppGitRepo(t)
	body := strings.Repeat("large payload line\n", diffMaxOutputBytes/len("large payload line\n")+1024)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte(body), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}

	out, err := buildDiffText(t.Context(), repo)
	if err != nil {
		t.Fatalf("buildDiffText: %v", err)
	}
	if len(out) > diffMaxOutputBytes+len("\n... diff truncated ...\n") {
		t.Fatalf("diff output exceeded cap: got %d", len(out))
	}
	if !strings.Contains(out, "diff truncated") {
		t.Fatalf("expected truncation notice, got length %d", len(out))
	}
}

func TestBuildDiffTextCleanRepo(t *testing.T) {
	repo := newAppGitRepo(t)
	out, err := buildDiffText(t.Context(), repo)
	if err != nil {
		t.Fatalf("buildDiffText: %v", err)
	}
	if out != "No changes detected." {
		t.Fatalf("expected clean message, got %q", out)
	}
}

func TestBuildDiffTextRepoWithoutHeadShowsUntracked(t *testing.T) {
	repo := t.TempDir()
	runAppGit(t, repo, "init")
	if err := os.WriteFile(filepath.Join(repo, "first.txt"), []byte("first\n"), 0o600); err != nil {
		t.Fatalf("write untracked: %v", err)
	}

	out, err := buildDiffText(t.Context(), repo)
	if err != nil {
		t.Fatalf("buildDiffText: %v", err)
	}
	plain := stripANSI(out)
	for _, want := range []string{"first.txt", "+first"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected unborn repo diff to contain %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "Failed to compute diff") || strings.Contains(plain, "ambiguous") {
		t.Fatalf("unborn repo should not fail against HEAD:\n%s", plain)
	}
}

func TestBuildDiffTextRepoWithoutHeadShowsStaged(t *testing.T) {
	repo := t.TempDir()
	runAppGit(t, repo, "init")
	if err := os.WriteFile(filepath.Join(repo, "staged.txt"), []byte("staged\n"), 0o600); err != nil {
		t.Fatalf("write staged: %v", err)
	}
	runAppGit(t, repo, "add", "staged.txt")

	out, err := buildDiffText(t.Context(), repo)
	if err != nil {
		t.Fatalf("buildDiffText: %v", err)
	}
	plain := stripANSI(out)
	for _, want := range []string{"staged.txt", "+staged"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected unborn repo staged diff to contain %q:\n%s", want, plain)
		}
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func TestBuildDiffTextTransientGitState(t *testing.T) {
	repo := newAppGitRepo(t)
	gitDir, _, err := runGit(t.Context(), repo, "rev-parse", "--git-dir")
	if err != nil {
		t.Fatalf("git dir: %v", err)
	}
	gitDir = strings.TrimSpace(gitDir)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(repo, gitDir)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "MERGE_HEAD"), []byte("deadbeef\n"), 0o600); err != nil {
		t.Fatalf("write MERGE_HEAD: %v", err)
	}

	out, err := buildDiffText(t.Context(), repo)
	if err != nil {
		t.Fatalf("buildDiffText: %v", err)
	}
	if !strings.Contains(out, "unavailable during merge") {
		t.Fatalf("expected transient state message, got:\n%s", out)
	}
}

func TestUntrackedDiffCapsFileCount(t *testing.T) {
	repo := newAppGitRepo(t)
	total := diffMaxUntrackedFiles + 25
	for i := 0; i < total; i++ {
		name := fmt.Sprintf("untracked-%04d.txt", i)
		if err := os.WriteFile(filepath.Join(repo, name), []byte("x\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	out, err := untrackedDiff(t.Context(), repo, diffMaxOutputBytes)
	if err != nil {
		t.Fatalf("untrackedDiff: %v", err)
	}
	plain := stripANSI(out)
	if !strings.Contains(plain, "file count limit reached") {
		t.Fatalf("expected file count cap notice, got:\n%s", plain)
	}
	want := fmt.Sprintf("%d untracked file(s) omitted", total-diffMaxUntrackedFiles)
	if !strings.Contains(plain, want) {
		t.Fatalf("expected omitted count %q, got:\n%s", want, plain)
	}
}

func TestBuildDiffTextFromSubdirectoryIncludesUntrackedElsewhere(t *testing.T) {
	repo := newAppGitRepo(t)
	subdir := filepath.Join(repo, "packages", "api")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "root-untracked.txt"), []byte("root scope\n"), 0o600); err != nil {
		t.Fatalf("write root untracked: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "sub-untracked.txt"), []byte("sub scope\n"), 0o600); err != nil {
		t.Fatalf("write sub untracked: %v", err)
	}

	out, err := buildDiffText(t.Context(), subdir)
	if err != nil {
		t.Fatalf("buildDiffText: %v", err)
	}
	plain := stripANSI(out)
	for _, want := range []string{"+changed", "root-untracked.txt", "+root scope", "sub-untracked.txt", "+sub scope"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected diff from subdirectory to contain %q:\n%s", want, plain)
		}
	}
}

func TestUntrackedDiffReportsUntrackedGitDirectory(t *testing.T) {
	repo := newAppGitRepo(t)
	nested := filepath.Join(repo, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	runAppGit(t, nested, "init")

	out, err := buildDiffText(t.Context(), repo)
	if err != nil {
		t.Fatalf("buildDiffText: %v", err)
	}
	plain := stripANSI(out)
	for _, want := range []string{"nested", "untracked directory omitted"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected untracked directory summary to contain %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "No changes detected") {
		t.Fatalf("untracked directory should not be reported as clean:\n%s", plain)
	}
}

func TestUntrackedDiffIncludesSymlinkToDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on many Windows setups")
	}
	repo := newAppGitRepo(t)
	targetDir := filepath.Join(repo, "target-dir")
	if err := os.Mkdir(targetDir, 0o700); err != nil {
		t.Fatalf("mkdir target-dir: %v", err)
	}
	if err := os.Symlink("target-dir", filepath.Join(repo, "dir-link")); err != nil {
		t.Fatalf("symlink dir-link: %v", err)
	}

	out, err := untrackedDiff(t.Context(), repo, diffMaxOutputBytes)
	if err != nil {
		t.Fatalf("untrackedDiff: %v", err)
	}
	plain := stripANSI(out)
	for _, want := range []string{"dir-link", "new file mode 120000", "+target-dir"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected symlink diff to contain %q:\n%s", want, plain)
		}
	}
}

func TestUntrackedDiffIncludesSymlinkToLargeTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on many Windows setups")
	}
	repo := newAppGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "large-target.bin"), []byte(strings.Repeat("x", diffMaxUntrackedSize+1)), 0o600); err != nil {
		t.Fatalf("write large target: %v", err)
	}
	if err := os.Symlink("large-target.bin", filepath.Join(repo, "large-link")); err != nil {
		t.Fatalf("symlink large-link: %v", err)
	}

	out, err := untrackedDiff(t.Context(), repo, diffMaxOutputBytes)
	if err != nil {
		t.Fatalf("untrackedDiff: %v", err)
	}
	plain := stripANSI(out)
	for _, want := range []string{"large-link", "new file mode 120000", "+large-target.bin"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected symlink diff to contain %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "large-link\nuntracked file omitted") {
		t.Fatalf("symlink should not be omitted because its target is large:\n%s", plain)
	}
}

func TestUntrackedDiffStopsAtBudget(t *testing.T) {
	repo := newAppGitRepo(t)
	for i := 0; i < 20; i++ {
		name := fmt.Sprintf("untracked-%02d.txt", i)
		body := strings.Repeat("payload line\n", 64)
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	out, err := untrackedDiff(t.Context(), repo, 4096)
	if err != nil {
		t.Fatalf("untrackedDiff: %v", err)
	}
	if !strings.Contains(stripANSI(out), "diff size limit reached") {
		t.Fatalf("expected size cap notice, got:\n%s", stripANSI(out))
	}
}

func TestUntrackedDiffSkipsWhenBudgetExhausted(t *testing.T) {
	repo := newAppGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "leftover.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatalf("write leftover: %v", err)
	}

	out, err := untrackedDiff(t.Context(), repo, 0)
	if err != nil {
		t.Fatalf("untrackedDiff: %v", err)
	}
	if !strings.Contains(out, "diff size limit reached") {
		t.Fatalf("expected exhausted-budget notice, got:\n%s", out)
	}
	if strings.Contains(out, "+x") {
		t.Fatalf("exhausted budget should not produce file diffs:\n%s", out)
	}
}
