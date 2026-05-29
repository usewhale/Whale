package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/usewhale/whale/internal/core"
)

type UserInputState struct {
	Pending    bool                     `json:"pending"`
	ToolCallID string                   `json:"tool_call_id,omitempty"`
	Questions  []core.UserInputQuestion `json:"questions,omitempty"`
	CreatedAt  time.Time                `json:"created_at,omitempty"`
	UpdatedAt  time.Time                `json:"updated_at,omitempty"`
}

func userInputStatePath(sessionsDir, sessionID string) string {
	return filepath.Join(sessionsDir, core.SanitizeSessionID(sessionID)+".user_input.json")
}

func LoadUserInputState(sessionsDir, sessionID string) (UserInputState, error) {
	path := userInputStatePath(sessionsDir, sessionID)
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return UserInputState{}, nil
		}
		return UserInputState{}, fmt.Errorf("read user input state: %w", err)
	}
	var st UserInputState
	if err := json.Unmarshal(b, &st); err != nil {
		return UserInputState{}, fmt.Errorf("unmarshal user input state: %w", err)
	}
	return st, nil
}

func SaveUserInputState(sessionsDir, sessionID string, st UserInputState) error {
	now := time.Now().UTC()
	if st.CreatedAt.IsZero() {
		st.CreatedAt = now
	}
	st.UpdatedAt = now
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal user input state: %w", err)
	}
	path := userInputStatePath(sessionsDir, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir user input state dir: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write user input state: %w", err)
	}
	return nil
}

func ClearUserInputState(sessionsDir, sessionID string) error {
	path := userInputStatePath(sessionsDir, sessionID)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove user input state: %w", err)
	}
	return nil
}
