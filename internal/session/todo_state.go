package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/core"
)

type TodoItem struct {
	ID          string    `json:"id"`
	Text        string    `json:"text"`
	Done        bool      `json:"done"`
	Priority    int       `json:"priority,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

type TodoState struct {
	Items []TodoItem `json:"items"`
}

func todoStatePath(sessionsDir, sessionID string) string {
	return filepath.Join(sessionsDir, core.SanitizeSessionID(sessionID)+".todo.json")
}

func LoadTodoState(sessionsDir, sessionID string) (TodoState, error) {
	path := todoStatePath(sessionsDir, sessionID)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return TodoState{Items: []TodoItem{}}, nil
		}
		return TodoState{}, fmt.Errorf("read todo state: %w", err)
	}
	var st TodoState
	if err := json.Unmarshal(b, &st); err != nil {
		return TodoState{}, fmt.Errorf("unmarshal todo state: %w", err)
	}
	if st.Items == nil {
		st.Items = []TodoItem{}
	}
	sortTodoItems(st.Items)
	return st, nil
}

func SaveTodoState(sessionsDir, sessionID string, st TodoState) error {
	sortTodoItems(st.Items)
	b, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("marshal todo state: %w", err)
	}
	path := todoStatePath(sessionsDir, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir todo dir: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write todo state: %w", err)
	}
	return nil
}

func sortTodoItems(items []TodoItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Done != items[j].Done {
			return !items[i].Done
		}
		if items[i].Priority != items[j].Priority {
			return items[i].Priority > items[j].Priority
		}
		return strings.ToLower(items[i].ID) < strings.ToLower(items[j].ID)
	})
}
