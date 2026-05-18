package memoryplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

type Plugin struct{}

func New() *Plugin { return &Plugin{} }

func (p *Plugin) ID() string   { return PluginID }
func (p *Plugin) Name() string { return "Memory" }

func Tools(store *Store) []core.Tool {
	return []core.Tool{
		rememberTool{store: store},
		forgetTool{store: store},
		recallTool{store: store},
	}
}

func StartupContext(_ context.Context, store *Store) (string, error) {
	globalPath, global, _, err := store.CappedIndex(globalScope, indexMaxBytes)
	if err != nil {
		return "", err
	}
	projectPath, project := "", ""
	if strings.TrimSpace(store.workspaceRoot) != "" {
		projectPath, project, _, err = store.CappedIndex(projectScope, indexMaxBytes)
		if err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(global) == "" && strings.TrimSpace(project) == "" {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("# Whale memory\n\n")
	b.WriteString("Use these remembered facts when relevant. If the user says not to use memory, ignore this block. Use `recall_memory` for details.\n")
	if strings.TrimSpace(global) != "" {
		b.WriteString("\n## Global\n")
		b.WriteString("Path: ")
		b.WriteString(globalPath)
		b.WriteString("\n\n```markdown\n")
		b.WriteString(global)
		b.WriteString("\n```\n")
	}
	if strings.TrimSpace(project) != "" {
		b.WriteString("\n## Project\n")
		b.WriteString("Path: ")
		b.WriteString(projectPath)
		b.WriteString("\n\n```markdown\n")
		b.WriteString(project)
		b.WriteString("\n```\n")
	}
	return strings.TrimSpace(b.String()), nil
}

func HandleCommand(store *Store, line string) (string, bool, error) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 || fields[0] != "/memory" {
		return "", false, nil
	}
	if len(fields) == 1 || (len(fields) == 2 && fields[1] == "list") {
		return FormatList(store), true, nil
	}
	if len(fields) == 2 && fields[1] == "path" {
		return FormatPaths(store), true, nil
	}
	if len(fields) == 3 && fields[1] == "show" {
		scope, name, err := ParseScopedName(fields[2])
		if err != nil {
			return "", true, err
		}
		entry, err := store.Read(scope, name)
		if err != nil {
			return "", true, err
		}
		return DisplayEntry(entry), true, nil
	}
	if len(fields) == 3 && fields[1] == "forget" {
		scope, name, err := ParseScopedName(fields[2])
		if err != nil {
			return "", true, err
		}
		deleted, err := store.Delete(scope, name)
		if err != nil {
			return "", true, err
		}
		if deleted {
			return fmt.Sprintf("forgot memory: %s/%s", scope, name), true, nil
		}
		return fmt.Sprintf("no such memory: %s/%s", scope, name), true, nil
	}
	return "", true, fmt.Errorf("usage: /memory [list|path|show <scope>/<name>|forget <scope>/<name>]")
}

type memoryToolBase struct {
	store *Store
}

func (t memoryToolBase) storeForWorkspace(workspace string) *Store {
	if t.store == nil {
		return NewStore("", workspace)
	}
	cp := *t.store
	cp.workspaceRoot = workspace
	return &cp
}

type rememberTool struct{ store *Store }

func (t rememberTool) Name() string { return "remember" }
func (t rememberTool) Description() string {
	return "Save or update a durable memory for future sessions. Use when the user states a lasting preference, corrects Whale's behavior, shares a non-obvious project fact, or explicitly asks Whale to remember something. Before creating a new memory, check the startup MEMORY.md index and prefer reusing an existing related name so the old file is updated instead of creating duplicates. Do not save transient task state."
}
func (t rememberTool) Capabilities() []string { return []string{"mutates_state"} }
func (t rememberTool) ApprovalHint() string {
	return "Writes long-term Whale memory."
}
func (t rememberTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"scope":       map[string]any{"type": "string", "enum": []string{globalScope, projectScope}},
			"type":        map[string]any{"type": "string", "enum": []string{"user", "feedback", "project", "reference"}},
			"name":        map[string]any{"type": "string", "description": "filename-safe identifier, 3-40 chars, alnum plus _ . -"},
			"description": map[string]any{"type": "string", "description": "one-line summary for MEMORY.md"},
			"content":     map[string]any{"type": "string", "description": "full memory body in markdown"},
		},
		"required": []string{"scope", "type", "name", "description", "content"},
	}
}
func (t rememberTool) Run(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	var in WriteInput
	if err := json.Unmarshal([]byte(call.Input), &in); err != nil {
		return toolError(call, "invalid_args", err.Error()), nil
	}
	entry, err := t.store.Write(in)
	if err != nil {
		return toolError(call, "remember_failed", err.Error()), nil
	}
	status := "created"
	if entry.UpdatedExisting {
		status = "updated"
	}
	return toolOK(call, map[string]any{
		"status":       "ok",
		"write_status": status,
		"scope":        entry.Scope,
		"name":         entry.Name,
		"description":  entry.Description,
		"path":         entry.Path,
		"summary":      fmt.Sprintf("%s memory (%s/%s): %s", status, entry.Scope, entry.Name, entry.Description),
		"session_hint": "Treat this as established fact for the rest of this session. It will be pinned into startup context on the next /new or launch.",
	})
}

func (t rememberTool) Preview(_ context.Context, call core.ToolCall) (map[string]any, error) {
	var in WriteInput
	if err := json.Unmarshal([]byte(call.Input), &in); err != nil {
		return nil, err
	}
	name, err := sanitizeName(in.Name)
	if err != nil {
		return nil, err
	}
	scope, err := normalizeScope(in.Scope)
	if err != nil {
		return nil, err
	}
	typ, err := normalizeType(in.Type)
	if err != nil {
		return nil, err
	}
	status := "created"
	if t.store != nil {
		if _, err := t.store.Read(scope, name); err == nil {
			status = "updated"
		}
	}
	return map[string]any{
		"approval_kind":          "memory_write",
		"approval_session_scope": fmt.Sprintf("%s memory: %s", scope, name),
		"memory_scope":           scope,
		"memory_type":            typ,
		"memory_name":            name,
		"memory_description":     oneLine(in.Description),
		"memory_content_preview": previewText(in.Content, 800),
		"memory_write_status":    status,
	}, nil
}

type forgetTool struct{ store *Store }

func (t forgetTool) Name() string { return "forget" }
func (t forgetTool) Description() string {
	return "Delete a memory and remove it from MEMORY.md. Use only when the user explicitly asks to forget something or a remembered fact is wrong."
}
func (t forgetTool) Capabilities() []string { return []string{"mutates_state"} }
func (t forgetTool) ApprovalHint() string {
	return "Deletes long-term Whale memory."
}
func (t forgetTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"scope": map[string]any{"type": "string", "enum": []string{globalScope, projectScope}},
			"name":  map[string]any{"type": "string"},
		},
		"required": []string{"scope", "name"},
	}
}
func (t forgetTool) Run(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	var in struct {
		Scope string `json:"scope"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal([]byte(call.Input), &in); err != nil {
		return toolError(call, "invalid_args", err.Error()), nil
	}
	existed, err := t.store.Delete(in.Scope, in.Name)
	if err != nil {
		return toolError(call, "forget_failed", err.Error()), nil
	}
	return toolOK(call, map[string]any{
		"status":       "ok",
		"scope":        in.Scope,
		"name":         in.Name,
		"deleted":      existed,
		"summary":      fmt.Sprintf("forgot (%s/%s): %t", in.Scope, in.Name, existed),
		"session_hint": fmt.Sprintf("Immediately stop using memory %s/%s for the rest of this session. If it appeared in startup context, treat that remembered fact as revoked.", in.Scope, in.Name),
	})
}

func (t forgetTool) Preview(_ context.Context, call core.ToolCall) (map[string]any, error) {
	var in struct {
		Scope string `json:"scope"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal([]byte(call.Input), &in); err != nil {
		return nil, err
	}
	name, err := sanitizeName(in.Name)
	if err != nil {
		return nil, err
	}
	scope, err := normalizeScope(in.Scope)
	if err != nil {
		return nil, err
	}
	metadata := map[string]any{
		"approval_kind":          "memory_delete",
		"approval_session_scope": fmt.Sprintf("%s memory: %s", scope, name),
		"memory_scope":           scope,
		"memory_name":            name,
	}
	if t.store != nil {
		if entry, err := t.store.Read(scope, name); err == nil {
			metadata["memory_type"] = entry.Type
			metadata["memory_description"] = entry.Description
			metadata["memory_content_preview"] = previewText(entry.Content, 800)
		}
	}
	return metadata, nil
}

type recallTool struct{ store *Store }

func (t recallTool) Name() string { return "recall_memory" }
func (t recallTool) Description() string {
	return "Read the full body of a memory file when the MEMORY.md one-liner is not enough detail."
}
func (t recallTool) ReadOnly() bool { return true }
func (t recallTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"scope": map[string]any{"type": "string", "enum": []string{globalScope, projectScope}},
			"name":  map[string]any{"type": "string"},
		},
		"required": []string{"scope", "name"},
	}
}
func (t recallTool) Run(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
	var in struct {
		Scope string `json:"scope"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal([]byte(call.Input), &in); err != nil {
		return toolError(call, "invalid_args", err.Error()), nil
	}
	entry, err := t.store.Read(in.Scope, in.Name)
	if err != nil {
		return toolError(call, "recall_failed", err.Error()), nil
	}
	return toolOK(call, map[string]any{
		"status":      "ok",
		"scope":       entry.Scope,
		"type":        entry.Type,
		"name":        entry.Name,
		"description": entry.Description,
		"path":        entry.Path,
		"content":     entry.Content,
	})
}

func toolOK(call core.ToolCall, data map[string]any) (core.ToolResult, error) {
	content, err := core.MarshalToolEnvelope(core.NewToolSuccessEnvelope(data))
	if err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content}, nil
}

func toolError(call core.ToolCall, code, msg string) core.ToolResult {
	content, err := core.MarshalToolEnvelope(core.NewToolErrorEnvelope(code, msg))
	if err != nil {
		content = fmt.Sprintf(`{"ok":false,"success":false,"code":%q,"error":%q}`, code, msg)
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content, IsError: true}
}

func previewText(raw string, max int) string {
	text := strings.TrimSpace(raw)
	if max <= 0 || len(text) <= max {
		return text
	}
	cut := text[:max]
	if i := strings.LastIndex(cut, "\n"); i > 0 && i >= max/2 {
		cut = cut[:i]
	}
	return strings.TrimSpace(cut) + "..."
}

func FormatList(store *Store) string {
	var lines []string
	lines = append(lines, "Memory", "")
	for _, scope := range []string{globalScope, projectScope} {
		path, index, err := store.Index(scope)
		count := countIndexEntries(index)
		title := strings.Title(scope)
		lines = append(lines, fmt.Sprintf("%s (%d %s)", title, count, memoryNoun(count)))
		lines = append(lines, "path: "+path)
		if err != nil {
			lines = append(lines, "error: "+err.Error(), "")
			continue
		}
		if strings.TrimSpace(index) == "" {
			lines = append(lines, "(empty)", "")
			continue
		}
		lines = append(lines, index, "")
		if count >= 50 {
			lines = append(lines, "Memory count is high; consider forgetting or consolidating related memories.", "")
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func countIndexEntries(index string) int {
	count := 0
	for _, line := range strings.Split(index, "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func memoryNoun(count int) string {
	if count == 1 {
		return "memory"
	}
	return "memories"
}

func FormatPaths(store *Store) string {
	global, _ := store.ScopeDir(globalScope)
	project, _ := store.ScopeDir(projectScope)
	return strings.TrimSpace(strings.Join([]string{
		"Memory paths",
		"",
		"global: " + global,
		"project: " + project,
	}, "\n"))
}

func ParseScopedName(raw string) (string, string, error) {
	scope, name, ok := strings.Cut(strings.TrimSpace(raw), "/")
	if !ok {
		return "", "", fmt.Errorf("expected <global|project>/<name>")
	}
	if _, err := normalizeScope(scope); err != nil {
		return "", "", err
	}
	name, err := sanitizeName(name)
	if err != nil {
		return "", "", err
	}
	return scope, name, nil
}

func DisplayEntry(e Entry) string {
	return strings.TrimSpace(fmt.Sprintf(`# %s (%s/%s)

> %s

path: %s

%s`, e.Name, e.Scope, e.Type, e.Description, filepath.ToSlash(e.Path), e.Content))
}
