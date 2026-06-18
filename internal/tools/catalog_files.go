package tools

import "github.com/usewhale/whale/internal/core"

const multiEditDescription = `Apply multiple ordered SEARCH/REPLACE edits to one existing file atomically.

Use this for most partial file modifications, especially when changing several locations in the same file. Each edit runs against the result of the previous edit in memory, and Whale writes the file only if every edit succeeds. A failure leaves the file untouched.

Rules:
- file_path is relative to the workspace, or an absolute path inside the workspace root.
- edits must contain at least one step.
- search must be exact current file text; use read_file first when you need to inspect or copy the target text.
- replace may be empty to delete text.
- all=false requires search to match exactly once at that step.
- all=true replaces every occurrence at that step and requires at least one match.
- Do not re-read files just to confirm the edit applied; the tool fails when it cannot apply the edit.`

func (b *Toolset) fileDiscoveryTools() []core.Tool {
	return []core.Tool{
		toolFn{
			name:        "read_file",
			description: "Read file content. Workspace, git worktree, and discovered local skill paths are read directly; external paths request file access approval before reading. Use this before edit/write when you need to inspect or copy exact text. Files up to 32KB return full content by default; larger files return an outline with head lines and continuation hints. Use offset/limit to read bounded ranges.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string", "description": "Path relative to workspace root, or an absolute/relative external path that may require file access approval."},
					"offset":    map[string]any{"type": "integer", "minimum": 0, "description": "Start line offset (0-based)"},
					"limit":     map[string]any{"type": "integer", "minimum": 1, "maximum": 2000, "description": "Max lines to read"},
				},
				"required": []string{"file_path"},
			},
			readOnly:     true,
			capabilities: []string{"workspace.read"},
			fn:           b.readFile,
		},
		toolFn{
			name:        "load_skill",
			description: "Load a local Agent Skill by name from workspace or user skill roots. Read-only; does not execute scripts and does not accept file paths.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"name":      map[string]any{"type": "string", "description": "Skill name, e.g. code-review or playwright"},
					"arguments": map[string]any{"type": "string", "description": "Optional task-specific context or arguments to pass along with the loaded skill"},
				},
				"required": []string{"name"},
			},
			readOnly:     true,
			capabilities: []string{"workspace.read"},
			fn:           b.loadSkill,
		},
		toolFn{
			name:        "list_dir",
			description: "List directory entries. Workspace, git worktree, and discovered local skill paths are read directly; external paths request file access approval before listing. Omit path or pass an empty path to list the workspace root. Not recursive; combine with grep/read_file for targeted exploration.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"path":   map[string]any{"type": "string", "description": "Optional directory path. Omit or pass an empty string to list the workspace root. External paths may require file access approval."},
					"ignore": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Deprecated compatibility field. Accepted but ignored; list_dir always returns the full directory listing."},
				},
			},
			readOnly:     true,
			capabilities: []string{"workspace.read"},
			fn:           b.listDir,
		},
	}
}

func (b *Toolset) fileMutationTools() []core.Tool {
	return []core.Tool{
		toolFn{
			name:        "edit",
			description: "Apply one SEARCH/REPLACE edit to an existing file. Requires exact current file text; returns search_not_found when search is not found. Use read_file first when you need to inspect or copy the target text. Use for small surgical changes when the exact current text is known. Prefer multi_edit for multiple edits in the same file.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string", "description": "Target file path relative to workspace, or an absolute path inside the workspace root"},
					"search":    map[string]any{"type": "string", "description": "Exact text to replace, copied from the read_file content"},
					"replace":   map[string]any{"type": "string", "description": "Replacement text; omit or pass an empty string to delete the matched text"},
					"all":       map[string]any{"type": "boolean", "description": "Replace all occurrences"},
				},
				"required": []string{"file_path", "search"},
			},
			capabilities: []string{"workspace.write"},
			fn:           b.editFile,
			preview:      b.previewEditFile,
		},
		toolFn{
			name:        "write",
			description: "Write full file content under workspace root (create or overwrite). Use for new files or intentional full rewrites. New files are created as regular non-executable files; use shell_run with chmod if a script must be executable. For most partial modifications, prefer multi_edit.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string", "description": "Target file path relative to workspace, or an absolute path inside the workspace root"},
					"content":   map[string]any{"type": "string", "description": "Full file content to write"},
				},
				"required": []string{"file_path", "content"},
			},
			capabilities: []string{"workspace.write"},
			fn:           b.writeFile,
			preview:      b.previewWriteFile,
		},
		toolFn{
			name:        "multi_edit",
			description: multiEditDescription,
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string", "description": "Target file path relative to workspace, or an absolute path inside the workspace root"},
					"edits": map[string]any{
						"type":        "array",
						"minItems":    1,
						"description": "Ordered edits. Each step sees the file as left by the previous step.",
						"items": map[string]any{
							"type":                 "object",
							"additionalProperties": false,
							"properties": map[string]any{
								"search":  map[string]any{"type": "string", "description": "Exact text to replace. Without all=true, must match exactly once at this step."},
								"replace": map[string]any{"type": "string", "description": "Replacement text; omit or use an empty string to delete text."},
								"all":     map[string]any{"type": "boolean", "description": "Replace every occurrence at this step instead of requiring uniqueness."},
							},
							"required": []string{"search"},
						},
					},
				},
				"required": []string{"file_path", "edits"},
			},
			capabilities: []string{"workspace.write"},
			fn:           b.multiEditFile,
			preview:      b.previewMultiEditFile,
		},
	}
}
