package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/session"
)

func (a *Agent) handleTodo(call core.ToolCall, sessionID string) (core.ToolResult, error) {
	if a.sessionRuntime == nil || !a.sessionRuntime.Enabled() {
		return core.ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: `{"success":false,"error":"todo storage unavailable","code":"todo_storage_unavailable"}`, Code: "todo_storage_unavailable"}, nil
	}
	st, err := a.sessionRuntime.LoadTodo(sessionID)
	if err != nil {
		return core.ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: fmt.Sprintf(`{"success":false,"error":%q,"code":"todo_load_failed"}`, err.Error()), Code: "todo_load_failed"}, nil
	}
	switch call.Name {
	case "todo_add":
		var in struct {
			Text     string `json:"text"`
			Priority int    `json:"priority"`
		}
		if err := json.Unmarshal([]byte(call.Input), &in); err != nil || strings.TrimSpace(in.Text) == "" {
			return core.ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: `{"success":false,"error":"invalid todo_add input","code":"invalid_todo_add"}`, Code: "invalid_todo_add"}, nil
		}
		now := time.Now()
		id := fmt.Sprintf("td-%d", now.UnixNano())
		item := session.TodoItem{ID: id, Text: strings.TrimSpace(in.Text), Priority: in.Priority, CreatedAt: now, UpdatedAt: now}
		st.Items = append(st.Items, item)
	case "todo_update":
		var in struct {
			ID       string `json:"id"`
			Text     string `json:"text"`
			Done     *bool  `json:"done"`
			Priority *int   `json:"priority"`
		}
		if err := json.Unmarshal([]byte(call.Input), &in); err != nil || strings.TrimSpace(in.ID) == "" {
			return core.ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: `{"success":false,"error":"invalid todo_update input","code":"invalid_todo_update"}`, Code: "invalid_todo_update"}, nil
		}
		found := false
		for i := range st.Items {
			if st.Items[i].ID != strings.TrimSpace(in.ID) {
				continue
			}
			found = true
			if strings.TrimSpace(in.Text) != "" {
				st.Items[i].Text = strings.TrimSpace(in.Text)
			}
			if in.Priority != nil {
				st.Items[i].Priority = *in.Priority
			}
			if in.Done != nil {
				st.Items[i].Done = *in.Done
				if *in.Done {
					st.Items[i].CompletedAt = time.Now()
				} else {
					st.Items[i].CompletedAt = time.Time{}
				}
			}
			st.Items[i].UpdatedAt = time.Now()
			break
		}
		if !found {
			return core.ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: `{"success":false,"error":"todo id not found","code":"todo_not_found"}`, Code: "todo_not_found"}, nil
		}
	case "todo_remove":
		var in struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(call.Input), &in); err != nil || strings.TrimSpace(in.ID) == "" {
			return core.ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: `{"success":false,"error":"invalid todo_remove input","code":"invalid_todo_remove"}`, Code: "invalid_todo_remove"}, nil
		}
		filtered := st.Items[:0]
		for _, item := range st.Items {
			if item.ID != strings.TrimSpace(in.ID) {
				filtered = append(filtered, item)
			}
		}
		st.Items = filtered
	case "todo_clear_done":
		filtered := st.Items[:0]
		for _, item := range st.Items {
			if !item.Done {
				filtered = append(filtered, item)
			}
		}
		st.Items = filtered
	case "todo_list":
		// read-only
	}
	if err := a.sessionRuntime.SaveTodo(sessionID, st); err != nil {
		return core.ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: fmt.Sprintf(`{"success":false,"error":%q,"code":"todo_save_failed"}`, err.Error()), Code: "todo_save_failed"}, nil
	}
	var inList struct {
		IncludeDone bool `json:"include_done"`
	}
	_ = json.Unmarshal([]byte(call.Input), &inList)
	items := st.Items
	if call.Name == "todo_list" && !inList.IncludeDone {
		filtered := make([]session.TodoItem, 0, len(items))
		for _, item := range items {
			if !item.Done {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	payload, _ := core.MarshalToolJSON(map[string]any{"success": true, "data": map[string]any{"items": items, "count": len(items)}})
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, ModelText: string(payload)}, nil
}
