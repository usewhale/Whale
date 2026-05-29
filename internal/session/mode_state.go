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

type Mode string

const (
	ModeAgent Mode = "agent"
	ModeAsk   Mode = "ask"
	ModePlan  Mode = "plan"
)

type ModeState struct {
	Mode      Mode      `json:"mode"`
	UpdatedAt time.Time `json:"updated_at"`
}

func ParseMode(raw string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(ModeAgent):
		return ModeAgent, nil
	case string(ModeAsk):
		return ModeAsk, nil
	case string(ModePlan):
		return ModePlan, nil
	default:
		return "", fmt.Errorf("invalid mode: %s", raw)
	}
}

func modeStatePath(sessionsDir, sessionID string) string {
	return filepath.Join(sessionsDir, core.SanitizeSessionID(sessionID)+".state.json")
}

func LoadModeState(sessionsDir, sessionID string) (ModeState, error) {
	path := modeStatePath(sessionsDir, sessionID)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ModeState{Mode: ModeAgent}, nil
		}
		return ModeState{}, fmt.Errorf("read mode state: %w", err)
	}
	var st ModeState
	if err := json.Unmarshal(b, &st); err != nil {
		return ModeState{}, fmt.Errorf("unmarshal mode state: %w", err)
	}
	if st.Mode == "" {
		st.Mode = ModeAgent
	}
	if _, err := ParseMode(string(st.Mode)); err != nil {
		return ModeState{}, err
	}
	return st, nil
}

func SaveModeState(sessionsDir, sessionID string, mode Mode) error {
	if _, err := ParseMode(string(mode)); err != nil {
		return err
	}
	st := ModeState{
		Mode:      mode,
		UpdatedAt: time.Now(),
	}
	b, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("marshal mode state: %w", err)
	}
	path := modeStatePath(sessionsDir, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir mode state dir: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write mode state: %w", err)
	}
	return nil
}
