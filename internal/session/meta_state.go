package session

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	metaLocksMu sync.Mutex
	metaLocks   = map[string]*sync.Mutex{}
)

func metaLock(path string) *sync.Mutex {
	metaLocksMu.Lock()
	defer metaLocksMu.Unlock()
	if m, ok := metaLocks[path]; ok {
		return m
	}
	m := &sync.Mutex{}
	metaLocks[path] = m
	return m
}

type SessionMeta struct {
	Branch             string    `json:"branch,omitempty"`
	Title              string    `json:"title,omitempty"`
	Summary            string    `json:"summary,omitempty"`
	TotalCostUSD       float64   `json:"total_cost_usd,omitempty"`
	TurnCount          int       `json:"turn_count,omitempty"`
	Workspace          string    `json:"workspace,omitempty"`
	WorktreeName       string    `json:"worktree_name,omitempty"`
	WorktreePath       string    `json:"worktree_path,omitempty"`
	WorktreeBranch     string    `json:"worktree_branch,omitempty"`
	OriginalWorkspace  string    `json:"original_workspace,omitempty"`
	OriginalBranch     string    `json:"original_branch,omitempty"`
	OriginalHeadCommit string    `json:"original_head_commit,omitempty"`
	Kind               string    `json:"kind,omitempty"`
	ParentSessionID    string    `json:"parent_session_id,omitempty"`
	Role               string    `json:"role,omitempty"`
	Model              string    `json:"model,omitempty"`
	Task               string    `json:"task,omitempty"`
	Status             string    `json:"status,omitempty"`
	Error              string    `json:"error,omitempty"`
	StartedAt          time.Time `json:"started_at,omitempty"`
	CompletedAt        time.Time `json:"completed_at,omitempty"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func metaStatePath(sessionsDir, sessionID string) string {
	return filepath.Join(sessionsDir, sanitizeSessionID(sessionID)+".meta.json")
}

func LoadSessionMeta(sessionsDir, sessionID string) (SessionMeta, error) {
	path := metaStatePath(sessionsDir, sessionID)
	lock := metaLock(path)
	lock.Lock()
	defer lock.Unlock()
	return loadSessionMetaAt(path)
}

func loadSessionMetaAt(path string) (SessionMeta, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return SessionMeta{}, nil
		}
		return SessionMeta{}, fmt.Errorf("read session meta: %w", err)
	}
	var st SessionMeta
	if err := json.Unmarshal(b, &st); err != nil {
		return SessionMeta{}, fmt.Errorf("unmarshal session meta: %w", err)
	}
	return st, nil
}

func SaveSessionMeta(sessionsDir, sessionID string, st SessionMeta) error {
	path := metaStatePath(sessionsDir, sessionID)
	lock := metaLock(path)
	lock.Lock()
	defer lock.Unlock()
	return saveSessionMetaAt(path, st)
}

func saveSessionMetaAt(path string, st SessionMeta) error {
	st.UpdatedAt = time.Now()
	b, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("marshal session meta: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir session meta dir: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write session meta: %w", err)
	}
	return nil
}

// UpdateSessionMeta atomically loads, mutates, and saves session meta under
// a per-file lock so concurrent writers cannot lose each other's updates.
func UpdateSessionMeta(sessionsDir, sessionID string, mutate func(*SessionMeta)) (SessionMeta, error) {
	path := metaStatePath(sessionsDir, sessionID)
	lock := metaLock(path)
	lock.Lock()
	defer lock.Unlock()
	cur, err := loadSessionMetaAt(path)
	if err != nil {
		return SessionMeta{}, err
	}
	mutate(&cur)
	if err := saveSessionMetaAt(path, cur); err != nil {
		return SessionMeta{}, err
	}
	return cur, nil
}

func PatchSessionMeta(sessionsDir, sessionID string, patch SessionMeta) (SessionMeta, error) {
	path := metaStatePath(sessionsDir, sessionID)
	lock := metaLock(path)
	lock.Lock()
	defer lock.Unlock()
	cur, err := loadSessionMetaAt(path)
	if err != nil {
		return SessionMeta{}, err
	}
	if strings.TrimSpace(patch.Branch) != "" {
		cur.Branch = strings.TrimSpace(patch.Branch)
	}
	if strings.TrimSpace(cur.Title) == "" && strings.TrimSpace(patch.Title) != "" {
		cur.Title = strings.TrimSpace(patch.Title)
	}
	if strings.TrimSpace(patch.Summary) != "" {
		cur.Summary = strings.TrimSpace(patch.Summary)
	}
	if patch.TotalCostUSD != 0 {
		cur.TotalCostUSD = patch.TotalCostUSD
	}
	if patch.TurnCount != 0 {
		cur.TurnCount = patch.TurnCount
	}
	if strings.TrimSpace(patch.Workspace) != "" {
		cur.Workspace = strings.TrimSpace(patch.Workspace)
	}
	if strings.TrimSpace(patch.WorktreeName) != "" {
		cur.WorktreeName = strings.TrimSpace(patch.WorktreeName)
	}
	if strings.TrimSpace(patch.WorktreePath) != "" {
		cur.WorktreePath = strings.TrimSpace(patch.WorktreePath)
	}
	if strings.TrimSpace(patch.WorktreeBranch) != "" {
		cur.WorktreeBranch = strings.TrimSpace(patch.WorktreeBranch)
	}
	if strings.TrimSpace(patch.OriginalWorkspace) != "" {
		cur.OriginalWorkspace = strings.TrimSpace(patch.OriginalWorkspace)
	}
	if strings.TrimSpace(patch.OriginalBranch) != "" {
		cur.OriginalBranch = strings.TrimSpace(patch.OriginalBranch)
	}
	if strings.TrimSpace(patch.OriginalHeadCommit) != "" {
		cur.OriginalHeadCommit = strings.TrimSpace(patch.OriginalHeadCommit)
	}
	if strings.TrimSpace(patch.Kind) != "" {
		cur.Kind = strings.TrimSpace(patch.Kind)
	}
	if strings.TrimSpace(patch.ParentSessionID) != "" {
		cur.ParentSessionID = strings.TrimSpace(patch.ParentSessionID)
	}
	if strings.TrimSpace(patch.Role) != "" {
		cur.Role = strings.TrimSpace(patch.Role)
	}
	if strings.TrimSpace(patch.Model) != "" {
		cur.Model = strings.TrimSpace(patch.Model)
	}
	if strings.TrimSpace(patch.Task) != "" {
		cur.Task = strings.TrimSpace(patch.Task)
	}
	if strings.TrimSpace(patch.Status) != "" {
		cur.Status = strings.TrimSpace(patch.Status)
	}
	if strings.TrimSpace(patch.Error) != "" {
		cur.Error = strings.TrimSpace(patch.Error)
	}
	if !patch.StartedAt.IsZero() {
		cur.StartedAt = patch.StartedAt
	}
	if !patch.CompletedAt.IsZero() {
		cur.CompletedAt = patch.CompletedAt
	}
	if err := saveSessionMetaAt(path, cur); err != nil {
		return SessionMeta{}, err
	}
	return cur, nil
}

func DetectGitBranch(cwd string) string {
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
