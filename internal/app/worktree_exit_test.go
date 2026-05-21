package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/store"
	whaleworktree "github.com/usewhale/whale/internal/worktree"
)

func TestBuildWorktreeExitSummaryCountsChangesAndCommits(t *testing.T) {
	repo := newAppGitRepo(t)
	sess, err := whaleworktree.Start(repo, "exit-summary")
	if err != nil {
		t.Fatalf("Start worktree: %v", err)
	}
	app := &App{worktree: WorktreeSession{
		Name:               sess.Name,
		Path:               sess.Path,
		Branch:             sess.Branch,
		OriginalWorkspace:  sess.OriginalWorkspace,
		OriginalBranch:     sess.OriginalBranch,
		OriginalHeadCommit: sess.OriginalHeadCommit,
	}}

	if err := os.WriteFile(filepath.Join(sess.Path, "scratch.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sess.Path, "committed.txt"), []byte("committed\n"), 0o600); err != nil {
		t.Fatalf("write committed file: %v", err)
	}
	runAppGit(t, sess.Path, "add", "committed.txt")
	runAppGit(t, sess.Path, "commit", "-m", "worktree commit")

	summary, ok, err := app.BuildWorktreeExitSummary()
	if err != nil {
		t.Fatalf("BuildWorktreeExitSummary: %v", err)
	}
	if !ok {
		t.Fatal("expected active worktree summary")
	}
	if summary.ChangedFiles != 1 || summary.Commits != 1 {
		t.Fatalf("summary = %+v, want 1 changed file and 1 commit", summary)
	}
}

func TestBuildWorktreeExitSummaryCountsIgnoredFiles(t *testing.T) {
	repo := newAppGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n"), 0o600); err != nil {
		t.Fatalf("write gitignore: %v", err)
	}
	runAppGit(t, repo, "add", ".gitignore")
	runAppGit(t, repo, "commit", "-m", "ignore env")
	sess, err := whaleworktree.Start(repo, "exit-ignored")
	if err != nil {
		t.Fatalf("Start worktree: %v", err)
	}
	app := &App{worktree: WorktreeSession{
		Name:               sess.Name,
		Path:               sess.Path,
		Branch:             sess.Branch,
		OriginalWorkspace:  sess.OriginalWorkspace,
		OriginalBranch:     sess.OriginalBranch,
		OriginalHeadCommit: sess.OriginalHeadCommit,
	}}
	if err := os.WriteFile(filepath.Join(sess.Path, ".env"), []byte("secret\n"), 0o600); err != nil {
		t.Fatalf("write ignored file: %v", err)
	}

	summary, ok, err := app.BuildWorktreeExitSummary()
	if err != nil {
		t.Fatalf("BuildWorktreeExitSummary: %v", err)
	}
	if !ok {
		t.Fatal("expected active worktree summary")
	}
	if summary.ChangedFiles != 0 || summary.IgnoredFiles != 1 || summary.Commits != 0 {
		t.Fatalf("summary = %+v, want 1 ignored file only", summary)
	}
}

func TestRemoveCurrentWorktreeRemovesFromOriginalWorkspace(t *testing.T) {
	repo := newAppGitRepo(t)
	// RemoveCurrentWorktree chdir's the process out of the worktree; pin and
	// restore the working directory so the change does not leak to other tests.
	t.Chdir(repo)
	sess, err := whaleworktree.Start(repo, "exit-remove")
	if err != nil {
		t.Fatalf("Start worktree: %v", err)
	}
	app := &App{worktree: WorktreeSession{
		Name:               sess.Name,
		Path:               sess.Path,
		Branch:             sess.Branch,
		OriginalWorkspace:  sess.OriginalWorkspace,
		OriginalBranch:     sess.OriginalBranch,
		OriginalHeadCommit: sess.OriginalHeadCommit,
	}}

	res, err := app.RemoveCurrentWorktree(false)
	if err != nil {
		t.Fatalf("RemoveCurrentWorktree: %v", err)
	}
	if res.Action != "remove" {
		t.Fatalf("unexpected action: %+v", res)
	}
	if _, err := os.Stat(sess.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree should be removed, stat err=%v", err)
	}
}

func TestRemoveCurrentWorktreeChdirsOutOfWorktree(t *testing.T) {
	repo := newAppGitRepo(t)
	sess, err := whaleworktree.Start(repo, "exit-chdir")
	if err != nil {
		t.Fatalf("Start worktree: %v", err)
	}
	// Simulate an interactive --worktree session, which has chdir'd into the
	// managed worktree. Windows cannot remove a process's current directory, so
	// removal must move the cwd out first. t.Chdir restores cwd after the test.
	t.Chdir(sess.Path)
	app := &App{worktree: WorktreeSession{
		Name:               sess.Name,
		Path:               sess.Path,
		Branch:             sess.Branch,
		OriginalWorkspace:  sess.OriginalWorkspace,
		OriginalBranch:     sess.OriginalBranch,
		OriginalHeadCommit: sess.OriginalHeadCommit,
	}}

	res, err := app.RemoveCurrentWorktree(false)
	if err != nil {
		t.Fatalf("RemoveCurrentWorktree: %v", err)
	}
	if res.Action != "remove" {
		t.Fatalf("unexpected action: %+v", res)
	}
	if _, err := os.Stat(sess.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree should be removed, stat err=%v", err)
	}
	got, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd after removal: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(got); err == nil {
		got = resolved
	}
	if got != sess.OriginalWorkspace {
		t.Fatalf("process cwd after removal = %q, want original workspace %q", got, sess.OriginalWorkspace)
	}
}

func TestRemoveCurrentWorktreeRestoresCwdWhenRemovalFails(t *testing.T) {
	repo := newAppGitRepo(t)
	sess, err := whaleworktree.Start(repo, "exit-remove-fail")
	if err != nil {
		t.Fatalf("Start worktree: %v", err)
	}
	// The worktree turned dirty after the exit summary, so a non-forced
	// auto-remove will fail.
	if err := os.WriteFile(filepath.Join(sess.Path, "scratch.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}
	// Simulate the interactive session still running inside the worktree.
	t.Chdir(sess.Path)
	app := &App{worktree: WorktreeSession{
		Name:               sess.Name,
		Path:               sess.Path,
		Branch:             sess.Branch,
		OriginalWorkspace:  sess.OriginalWorkspace,
		OriginalBranch:     sess.OriginalBranch,
		OriginalHeadCommit: sess.OriginalHeadCommit,
	}}

	if _, err := app.RemoveCurrentWorktree(false); err == nil {
		t.Fatal("expected RemoveCurrentWorktree to fail for a dirty worktree")
	}
	if _, err := os.Stat(sess.Path); err != nil {
		t.Fatalf("worktree should still exist after failed removal: %v", err)
	}
	got, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd after failed removal: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(got); err == nil {
		got = resolved
	}
	wantCwd := sess.Path
	if resolved, err := filepath.EvalSymlinks(wantCwd); err == nil {
		wantCwd = resolved
	}
	if got != wantCwd {
		t.Fatalf("process cwd after failed removal = %q, want worktree %q", got, wantCwd)
	}
}

func TestWorktreeExitClearsSessionMeta(t *testing.T) {
	repo := newAppGitRepo(t)
	sess, err := whaleworktree.Start(repo, "exit-meta")
	if err != nil {
		t.Fatalf("Start worktree: %v", err)
	}
	dataDir := t.TempDir()
	sessionsDir := store.DefaultSessionsDir(dataDir)
	if err := session.SaveSessionMeta(sessionsDir, "s1", session.SessionMeta{
		Workspace:          sess.Path,
		Branch:             sess.Branch,
		WorktreeName:       sess.Name,
		WorktreePath:       sess.Path,
		WorktreeBranch:     sess.Branch,
		OriginalWorkspace:  sess.OriginalWorkspace,
		OriginalBranch:     sess.OriginalBranch,
		OriginalHeadCommit: sess.OriginalHeadCommit,
	}); err != nil {
		t.Fatalf("save meta: %v", err)
	}
	app := &App{
		sessionsDir: sessionsDir,
		sessionID:   "s1",
		worktree: WorktreeSession{
			Name:               sess.Name,
			Path:               sess.Path,
			Branch:             sess.Branch,
			OriginalWorkspace:  sess.OriginalWorkspace,
			OriginalBranch:     sess.OriginalBranch,
			OriginalHeadCommit: sess.OriginalHeadCommit,
		},
	}

	res, err := app.KeepCurrentWorktree()
	if err != nil {
		t.Fatalf("KeepCurrentWorktree: %v", err)
	}
	if res.Action != "keep" {
		t.Fatalf("unexpected action: %+v", res)
	}
	if !strings.Contains(res.Message, sess.Path) || !strings.Contains(res.Message, sess.Branch) {
		t.Fatalf("keep message lost worktree details: %q", res.Message)
	}
	meta, err := session.LoadSessionMeta(sessionsDir, "s1")
	if err != nil {
		t.Fatalf("load meta: %v", err)
	}
	if meta.Workspace != sess.OriginalWorkspace || meta.Branch != sess.OriginalBranch {
		t.Fatalf("unexpected meta workspace/branch after exit: %+v", meta)
	}
	if meta.WorktreeName != "" || meta.WorktreePath != "" || meta.WorktreeBranch != "" || meta.OriginalWorkspace != "" || meta.OriginalBranch != "" || meta.OriginalHeadCommit != "" {
		t.Fatalf("expected worktree meta to be cleared: %+v", meta)
	}
	if app.worktree.Name != "" {
		t.Fatalf("expected app worktree state to be cleared: %+v", app.worktree)
	}
}
