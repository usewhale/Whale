package tools

import "github.com/usewhale/whale/internal/core"

func (b *Toolset) searchTools() []core.Tool {
	return []core.Tool{
		toolFn{
			name:        "grep",
			description: "Search file contents recursively under workspace root or discovered local skill directories. Use for symbol/reference discovery before read_file/edit. For literal matching set literal_text=true; use include to narrow file patterns. Output is capped by limit, default 100 matches.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"pattern":      map[string]any{"type": "string", "description": "Pattern or literal query"},
					"path":         map[string]any{"type": "string", "description": "Search root relative to workspace, an absolute path inside workspace root, or an absolute path inside a discovered local skill directory"},
					"include":      map[string]any{"type": "string", "description": "Glob include filter, e.g. *.go"},
					"literal_text": map[string]any{"type": "boolean", "description": "When true, treat pattern as plain text"},
					"limit":        map[string]any{"type": "integer", "minimum": 1, "maximum": 2000, "description": "Maximum matches to return, default 100"},
				},
				"required": []string{"pattern"},
			},
			readOnly: true,
			fn:       b.searchContent,
		},
		toolFn{
			name:        "search_files",
			description: "Search file names and relative paths recursively under workspace root or discovered local skill directories. Best for locating candidate files before read_file.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"path":    map[string]any{"type": "string", "description": "Search root relative to workspace, or an absolute path inside workspace or a discovered local skill directory"},
					"pattern": map[string]any{"type": "string", "description": "Case-insensitive file/path pattern"},
					"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 2000},
				},
				"required": []string{"pattern"},
			},
			readOnly: true,
			fn:       b.searchFiles,
		},
	}
}
