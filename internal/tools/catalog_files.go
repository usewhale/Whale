package tools

import "github.com/usewhale/whale/internal/core"

const applyPatchDescription = `Apply structured multi-file patch text.

Use Whale's apply_patch format, not unified diff. The patch must be a single string with this shape:

*** Begin Patch
*** Update File: path/to/file
@@
 context line to keep
-old line to remove
+new line to add
 context line to keep
*** End Patch

Supported file operations:
*** Add File: <path>    creates a new file; every content line must start with +
*** Delete File: <path> deletes an existing file; no hunks follow
*** Update File: <path> patches an existing file; include one or more @@ hunks

Hunk rules:
- Each hunk starts with @@ on its own line.
- Hunk lines must start with exactly one of: space for context, - for removed text, + for added text.
- Keep enough context lines for an exact match.
- Paths are relative to the workspace. Do not use absolute paths.
- Do not use unified diff headers such as diff --git, --- a/file, or +++ b/file.`

const applyPatchParamDescription = `Full patch text in Whale's *** Begin Patch format. Do not send unified diff. Use headers like *** Update File: path, then @@ hunks with space/-/+ lines.`

func (b *Toolset) fileDiscoveryTools() []core.Tool {
	return []core.Tool{
		toolFn{
			name:        "read_file",
			description: "Read file content under workspace root or discovered local skill directories. Use this before edit/write to confirm exact text. Files up to 32KB return full content by default; larger files return an outline with head lines and continuation hints. Use offset/limit to read bounded ranges.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string", "description": "Path relative to workspace root, an absolute path inside workspace root, or an absolute path inside a discovered local skill directory"},
					"offset":    map[string]any{"type": "integer", "minimum": 0, "description": "Start line offset (0-based)"},
					"limit":     map[string]any{"type": "integer", "minimum": 1, "maximum": 2000, "description": "Max lines to read"},
				},
				"required": []string{"file_path"},
			},
			readOnly: true,
			fn:       b.readFile,
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
			readOnly: true,
			fn:       b.loadSkill,
		},
		toolFn{
			name:        "list_dir",
			description: "List directory entries under workspace root or discovered local skill directories. Use for structure discovery before deeper reads. Not recursive; combine with grep/read_file for targeted exploration.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"path":   map[string]any{"type": "string", "description": "Directory path relative to workspace root, an absolute path inside workspace root, or an absolute path inside a discovered local skill directory"},
					"ignore": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
			},
			readOnly: true,
			fn:       b.listDir,
		},
	}
}

func (b *Toolset) fileMutationTools() []core.Tool {
	return []core.Tool{
		toolFn{
			name:        "edit",
			description: "Apply SEARCH/REPLACE edits to an existing file. Requires exact search text; returns error when search is not found. Prefer this for surgical changes over full-file rewrites.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string", "description": "Target file path relative to workspace, or an absolute path inside the workspace root"},
					"search":    map[string]any{"type": "string", "description": "Exact text to replace"},
					"replace":   map[string]any{"type": "string", "description": "Replacement text"},
					"all":       map[string]any{"type": "boolean", "description": "Replace all occurrences"},
				},
				"required": []string{"file_path", "search", "replace"},
			},
			fn:      b.editFile,
			preview: b.previewEditFile,
		},
		toolFn{
			name:        "write",
			description: "Write full file content under workspace root (create or overwrite). Use for new files or intentional full rewrites. For partial modifications, prefer edit.",
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string", "description": "Target file path relative to workspace, or an absolute path inside the workspace root"},
					"content":   map[string]any{"type": "string", "description": "Full file content to write"},
				},
				"required": []string{"file_path", "content"},
			},
			fn:      b.writeFile,
			preview: b.previewWriteFile,
		},
		toolFn{
			name:        "apply_patch",
			description: applyPatchDescription,
			parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"patch": map[string]any{"type": "string", "description": applyPatchParamDescription},
				},
				"required": []string{"patch"},
			},
			fn:      b.applyPatch,
			preview: b.previewApplyPatch,
		},
	}
}
