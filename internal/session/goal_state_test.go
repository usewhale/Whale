package session

import (
	"testing"
	"time"
)

func TestGoalStateSaveLoadClear(t *testing.T) {
	dir := t.TempDir()
	sessionID := "sess/with/slash"
	createdAt := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)

	st := GoalState{
		ID:          "goal-1",
		Objective:   "ship goal command",
		Status:      GoalStatusActive,
		TokenBudget: 50_000,
		TokensUsed:  1_200,
		CreatedAt:   createdAt,
	}
	if err := SaveGoalState(dir, sessionID, st); err != nil {
		t.Fatalf("save goal state: %v", err)
	}

	got, ok, err := LoadGoalState(dir, sessionID)
	if err != nil {
		t.Fatalf("load goal state: %v", err)
	}
	if !ok {
		t.Fatal("expected saved goal state")
	}
	if got.Version != 1 {
		t.Fatalf("version = %d, want 1", got.Version)
	}
	if got.Objective != st.Objective {
		t.Fatalf("objective = %q, want %q", got.Objective, st.Objective)
	}
	if got.Status != GoalStatusActive {
		t.Fatalf("status = %q, want active", got.Status)
	}
	if got.TokenBudget != st.TokenBudget || got.TokensUsed != st.TokensUsed {
		t.Fatalf("tokens = %d/%d, want %d/%d", got.TokensUsed, got.TokenBudget, st.TokensUsed, st.TokenBudget)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("timestamps should be populated: %#v", got)
	}

	if err := ClearGoalState(dir, sessionID); err != nil {
		t.Fatalf("clear goal state: %v", err)
	}
	_, ok, err = LoadGoalState(dir, sessionID)
	if err != nil {
		t.Fatalf("load after clear: %v", err)
	}
	if ok {
		t.Fatal("expected goal state to be cleared")
	}
}

func TestSaveGoalStateRequiresObjective(t *testing.T) {
	err := SaveGoalState(t.TempDir(), "sess", GoalState{Objective: "   "})
	if err == nil {
		t.Fatal("expected missing objective error")
	}
}
