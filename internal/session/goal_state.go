package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/core"
)

type GoalStatus string

const (
	GoalStatusActive        GoalStatus = "active"
	GoalStatusPaused        GoalStatus = "paused"
	GoalStatusBudgetLimited GoalStatus = "budget_limited"
	GoalStatusCompleted     GoalStatus = "completed"
)

type GoalState struct {
	Version         int        `json:"version"`
	ID              string     `json:"id"`
	Objective       string     `json:"objective"`
	Status          GoalStatus `json:"status"`
	TokenBudget     int        `json:"token_budget,omitempty"`
	TokensUsed      int        `json:"tokens_used,omitempty"`
	TokenBaseline   int        `json:"token_baseline,omitempty"`
	TimeUsedSeconds int        `json:"time_used_seconds,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	CompletedAt     time.Time  `json:"completed_at,omitempty"`
}

func goalStatePath(sessionsDir, sessionID string) string {
	return filepath.Join(sessionsDir, core.SanitizeSessionID(sessionID)+".goal.json")
}

func LoadGoalState(sessionsDir, sessionID string) (GoalState, bool, error) {
	path := goalStatePath(sessionsDir, sessionID)
	lock := metaLock(sessionsDir)
	lock.Lock()
	defer lock.Unlock()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return GoalState{}, false, nil
		}
		return GoalState{}, false, fmt.Errorf("read goal state: %w", err)
	}
	var st GoalState
	if err := json.Unmarshal(b, &st); err != nil {
		return GoalState{}, false, fmt.Errorf("unmarshal goal state: %w", err)
	}
	return st, true, nil
}

func SaveGoalState(sessionsDir, sessionID string, st GoalState) error {
	st.Objective = strings.TrimSpace(st.Objective)
	if st.Objective == "" {
		return fmt.Errorf("goal objective is required")
	}
	if st.Version == 0 {
		st.Version = 1
	}
	if st.Status == "" {
		st.Status = GoalStatusActive
	}
	now := time.Now()
	if st.CreatedAt.IsZero() {
		st.CreatedAt = now
	}
	st.UpdatedAt = now
	path := goalStatePath(sessionsDir, sessionID)
	lock := metaLock(sessionsDir)
	lock.Lock()
	defer lock.Unlock()
	b, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("marshal goal state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir goal state dir: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write goal state: %w", err)
	}
	return nil
}

func ClearGoalState(sessionsDir, sessionID string) error {
	path := goalStatePath(sessionsDir, sessionID)
	lock := metaLock(sessionsDir)
	lock.Lock()
	defer lock.Unlock()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove goal state: %w", err)
	}
	return nil
}
