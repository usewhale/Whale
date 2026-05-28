package tui

import (
	"testing"

	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func TestFocusSummarizeToolMessageProviders(t *testing.T) {
	tests := []struct {
		name string
		msg  tuirender.UIMessage
		want focusToolSummaryItem
	}{
		{
			name: "shell command",
			msg:  tuirender.UIMessage{ToolName: "shell_run", Text: "Ran git status"},
			want: focusToolSummaryItem{Kind: "shell", Detail: "git status", Identity: "git status"},
		},
		{
			name: "shell text prefix",
			msg:  tuirender.UIMessage{Text: "Running shell: git status"},
			want: focusToolSummaryItem{Kind: "shell", Detail: "git status", Identity: "git status"},
		},
		{
			name: "shell placeholder omitted",
			msg:  tuirender.UIMessage{ToolName: "shell_run", Text: "Ran shell command"},
			want: focusToolSummaryItem{Kind: "shell"},
		},
		{
			name: "shell identity uses payload command",
			msg: tuirender.UIMessage{
				ToolName:     "shell_run",
				ToolIdentity: "make test",
				Text:         "Ran make test\n✓ · 23ms\nok",
			},
			want: focusToolSummaryItem{Kind: "shell", Detail: "make test", Identity: "make test"},
		},
		{
			name: "read action",
			msg:  tuirender.UIMessage{ToolName: "read_file", Text: "Explored\nRead internal/tui/model.go"},
			want: focusToolSummaryItem{Kind: "read", Detail: "internal/tui/model.go"},
		},
		{
			name: "search action",
			msg:  tuirender.UIMessage{ToolName: "grep", Text: "Explored\nSearch focus summary"},
			want: focusToolSummaryItem{Kind: "search", Detail: "focus summary"},
		},
		{
			name: "list action",
			msg:  tuirender.UIMessage{ToolName: "list_dir", Text: "Explored\nList internal/tui"},
			want: focusToolSummaryItem{Kind: "list", Detail: "internal/tui"},
		},
		{
			name: "mcp read placeholder omitted",
			msg:  tuirender.UIMessage{ToolName: "mcp__fs__read_text_file", Text: "Running read_text_file"},
			want: focusToolSummaryItem{Kind: "read"},
		},
		{
			name: "mcp read detail kept",
			msg:  tuirender.UIMessage{ToolName: "mcp__fs__read_text_file", Text: "Running internal/tui/focus_view.go"},
			want: focusToolSummaryItem{Kind: "read", Detail: "internal/tui/focus_view.go"},
		},
		{
			name: "edit action",
			msg:  tuirender.UIMessage{ToolName: "edit_file", Text: "Edited internal/tui/focus_view.go"},
			want: focusToolSummaryItem{Kind: "edit", Detail: "internal/tui/focus_view.go"},
		},
		{
			name: "subagent task",
			msg:  tuirender.UIMessage{Kind: tuirender.KindSubagent, ToolName: "spawn_subagent", Text: "Subagent review running\nsession: child"},
			want: focusToolSummaryItem{Kind: "task", Detail: "Subagent review running"},
		},
		{
			name: "plan update",
			msg:  tuirender.UIMessage{ToolName: "update_plan", Text: "Updating plan"},
			want: focusToolSummaryItem{Kind: "plan"},
		},
		{
			name: "todo update",
			msg:  tuirender.UIMessage{ToolName: "todo_update", Text: "Updated todos"},
			want: focusToolSummaryItem{Kind: "todo"},
		},
		{
			name: "unknown tool",
			msg:  tuirender.UIMessage{ToolName: "custom_tool", Text: "Running custom_tool"},
			want: focusToolSummaryItem{Kind: "other"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := focusSummarizeToolMessage(tt.msg)
			if got != tt.want {
				t.Fatalf("focusSummarizeToolMessage() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
