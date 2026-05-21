package app

import (
	"fmt"
	"strings"

	"github.com/usewhale/whale/internal/session"
	whaleworktree "github.com/usewhale/whale/internal/worktree"
)

func (a *App) CurrentWorktree() WorktreeSession {
	return a.worktree
}

func (a *App) BuildWorktreeExitSummary() (WorktreeExitSummary, bool, error) {
	if strings.TrimSpace(a.worktree.Name) == "" {
		return WorktreeExitSummary{}, false, nil
	}
	changes, err := whaleworktree.CountChanges(a.worktree.Path, a.worktree.OriginalWorkspace, a.worktree.OriginalHeadCommit)
	if err != nil {
		return WorktreeExitSummary{}, true, err
	}
	return WorktreeExitSummary{
		Session:      a.worktree,
		ChangedFiles: changes.ChangedFiles,
		IgnoredFiles: changes.IgnoredFiles,
		Commits:      changes.Commits,
	}, true, nil
}

func (a *App) KeepCurrentWorktree() (WorktreeExitResult, error) {
	if strings.TrimSpace(a.worktree.Name) == "" {
		return WorktreeExitResult{Action: "none", Message: "No active worktree session found"}, nil
	}
	path := a.worktree.Path
	branch := a.worktree.Branch
	if err := a.markWorktreeExited(); err != nil {
		return WorktreeExitResult{}, err
	}
	msg := fmt.Sprintf("Worktree kept at %s on branch %s", path, valueOrDash(branch))
	return WorktreeExitResult{Action: "keep", Message: msg}, nil
}

func (a *App) ForgetCurrentWorktree() (WorktreeExitResult, error) {
	if strings.TrimSpace(a.worktree.Name) == "" {
		return WorktreeExitResult{Action: "none", Message: "No active worktree session found"}, nil
	}
	name := a.worktree.Name
	path := a.worktree.Path
	if err := a.markWorktreeExited(); err != nil {
		return WorktreeExitResult{}, err
	}
	msg := fmt.Sprintf("Worktree state cleared: %s", name)
	if strings.TrimSpace(path) != "" {
		msg += "\nPath was not inspected: " + path
	}
	return WorktreeExitResult{Action: "forget", Message: msg}, nil
}

func (a *App) RemoveCurrentWorktree(force bool) (WorktreeExitResult, error) {
	if strings.TrimSpace(a.worktree.Name) == "" {
		return WorktreeExitResult{Action: "none", Message: "No active worktree session found"}, nil
	}
	cwd := strings.TrimSpace(a.worktree.OriginalWorkspace)
	if cwd == "" {
		root, err := whaleworktree.CanonicalRepoRoot(a.worktree.Path)
		if err != nil {
			return WorktreeExitResult{}, fmt.Errorf("resolve worktree repository: %w", err)
		}
		cwd = root
	}
	name := a.worktree.Name
	res, err := whaleworktree.Remove(cwd, name, force)
	if err != nil {
		return WorktreeExitResult{}, err
	}
	if err := a.markWorktreeExited(); err != nil {
		return WorktreeExitResult{}, err
	}
	msg := fmt.Sprintf("Worktree removed: %s", res.Entry.Name)
	if res.BranchDeleted {
		msg += "\nDeleted branch: " + whaleworktree.BranchName(name)
	}
	if res.BranchWarning != "" {
		msg += "\nBranch warning: " + res.BranchWarning
	}
	return WorktreeExitResult{
		Action:        "remove",
		Message:       msg,
		BranchWarning: res.BranchWarning,
	}, nil
}

func (a *App) markWorktreeExited() error {
	if a == nil || strings.TrimSpace(a.sessionsDir) == "" || strings.TrimSpace(a.sessionID) == "" {
		a.worktree = WorktreeSession{}
		return nil
	}
	workspace := firstNonEmpty(strings.TrimSpace(a.worktree.OriginalWorkspace), strings.TrimSpace(a.workspaceRoot))
	branch := strings.TrimSpace(a.worktree.OriginalBranch)
	if _, err := session.UpdateSessionMeta(a.sessionsDir, a.sessionID, func(meta *session.SessionMeta) {
		if workspace != "" {
			meta.Workspace = workspace
		}
		if branch != "" {
			meta.Branch = branch
		}
		clearSessionMetaWorktree(meta)
	}); err != nil {
		return fmt.Errorf("record worktree exit: %w", err)
	}
	a.worktree = WorktreeSession{}
	return nil
}

func clearSessionMetaWorktree(meta *session.SessionMeta) {
	meta.WorktreeName = ""
	meta.WorktreePath = ""
	meta.WorktreeBranch = ""
	meta.OriginalWorkspace = ""
	meta.OriginalBranch = ""
	meta.OriginalHeadCommit = ""
}
