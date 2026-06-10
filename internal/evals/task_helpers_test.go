package evals

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/session"
)

func todoStateForRun(run *Run) (session.TodoState, error) {
	return session.LoadTodoState(filepath.Join(run.Root, ".sessions"), run.SessionID)
}

func todoIDFromHistory(history []core.Message) (string, error) {
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		if msg.Role != core.RoleTool {
			continue
		}
		for _, tr := range msg.ToolResults {
			if !strings.HasPrefix(tr.Name, "todo_") {
				continue
			}
			payload, ok := tr.Payload.(map[string]any)
			if !ok {
				continue
			}
			items := core.AsAnySlice(payload["items"])
			if len(items) == 0 {
				items = core.AsAnySlice(payload["data"])
			}
			if len(items) == 0 {
				continue
			}
			if item, ok := items[0].(map[string]any); ok {
				if id := strings.TrimSpace(core.AsString(item["id"])); id != "" {
					return id, nil
				}
			}
		}
	}
	return "", fmt.Errorf("todo id not found in history")
}
