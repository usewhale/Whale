package tools

import "github.com/usewhale/whale/internal/core"

func (b *Toolset) searchTools() []core.Tool {
	return []core.Tool{
		toolFn{
			name:        "grep",
			description: "Search file contents recursively. Workspace, git worktree, and discovered local skill paths are read directly; external paths request file access approval before searching. Omit path or pass an empty path to search the workspace root. Use for symbol/reference discovery before read_file/edit. The pattern is a regular expression (RE2/ripgrep syntax); to match text literally, escape regex metacharacters (e.g. interface\\{\\} to find interface{}). Use include to narrow file patterns. Output is capped by limit, default 100 matches.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"pattern": map[string]any{"type": "string", "description": "Regular expression to search for (RE2/ripgrep syntax). Escape metacharacters to match literally."},
					"path":    map[string]any{"type": "string", "description": "Optional search root. Omit or pass an empty string to search the workspace root. External paths may require file access approval."},
					"include": map[string]any{"type": "string", "description": "Glob include filter, e.g. *.go"},
					"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 2000, "description": "Maximum matches to return, default 100"},
				},
				"required": []string{"pattern"},
			},
			readOnly:     true,
			capabilities: []string{"workspace.read"},
			fn:           b.searchContent,
		},
		toolFn{
			name:        "search_files",
			description: "Search file names and relative paths recursively. Workspace, git worktree, and discovered local skill paths are read directly; external paths request file access approval before searching. Omit path or pass an empty path to search the workspace root. Best for locating candidate files before read_file. To filter by file type or layout, put a glob directly in pattern (e.g. **/*.go, *.test.js, src/**/*.ts); a pattern with no glob metacharacters is matched as a case-insensitive substring of the path. This tool matches names only and does not support include; use grep (which has include) to search file contents. Output is capped by limit; if results are truncated, refine path/pattern before retrying broad searches.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"path":    map[string]any{"type": "string", "description": "Optional search root. Omit or pass an empty string to search the workspace root. External paths may require file access approval."},
					"pattern": map[string]any{"type": "string", "description": "Glob pattern (e.g. **/*.go, *.test.js) when it contains glob metacharacters, otherwise a case-insensitive substring of the file path."},
					"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 2000},
				},
				"required": []string{"pattern"},
			},
			readOnly:     true,
			capabilities: []string{"workspace.read"},
			fn:           b.searchFiles,
		},
	}
}
