package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/skills"
)

type Toolset struct {
	root              string
	worktreeRoot      string
	originalWorkspace string
	httpClient        *http.Client
	ddgSearchURL      string
	bingSearchURL     string
	tasks             *shellTaskRegistry
	fileLocks         *fileMutationLocks
	// Test hooks for deterministic mutation-race coverage.
	afterFileRead    func(string)
	beforeFileCommit func(string)
	skillDisabled    []string
	extraSkills      []*skills.Skill
}

type externalReadRootsKey struct{}

func WithApprovedExternalReadRoots(ctx context.Context, roots []string) context.Context {
	cleaned := make([]string, 0, len(roots))
	for _, root := range roots {
		root = cleanOptionalAbsPath(root)
		if root != "" {
			cleaned = append(cleaned, root)
		}
	}
	if len(cleaned) == 0 {
		return ctx
	}
	return context.WithValue(ctx, externalReadRootsKey{}, cleaned)
}

func NewToolset(root string) (*Toolset, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	return &Toolset{
		root:          abs,
		httpClient:    &http.Client{Timeout: 15 * time.Second},
		ddgSearchURL:  "https://html.duckduckgo.com/html/?q=%s",
		bingSearchURL: "https://www.bing.com/search?q=%s",
		tasks:         newShellTaskRegistry(),
		fileLocks:     newFileMutationLocks(),
	}, nil
}

func (b *Toolset) SetSkillDisabled(names []string) {
	b.skillDisabled = append([]string(nil), names...)
}

func (b *Toolset) SetExtraSkills(extra []*skills.Skill) {
	b.extraSkills = append([]*skills.Skill(nil), extra...)
}

func (b *Toolset) SetWorktreeContext(worktreeRoot, originalWorkspace string) {
	b.worktreeRoot = cleanOptionalAbsPath(worktreeRoot)
	b.originalWorkspace = cleanOptionalAbsPath(originalWorkspace)
}

func cleanOptionalAbsPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

func marshalToolResult(call core.ToolCall, data any) (core.ToolResult, error) {
	return marshalToolResultWithMetadata(call, data, nil)
}

func marshalToolResultWithMetadata(call core.ToolCall, data any, metadata map[string]any) (core.ToolResult, error) {
	dataMap, ok := data.(map[string]any)
	if !ok {
		dataMap = map[string]any{"payload": data}
	}
	content, err := core.MarshalToolEnvelope(core.NewToolSuccessEnvelope(dataMap))
	if err != nil {
		return core.ToolResult{}, err
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content, Metadata: metadata}, nil
}

func marshalToolError(call core.ToolCall, code, msg string) core.ToolResult {
	content, err := core.MarshalToolEnvelope(core.NewToolErrorEnvelope(code, msg))
	if err != nil {
		content = fmt.Sprintf(`{"success":false,"code":%q,"message":%q}`, code, msg)
	}
	return core.ToolResult{ToolCallID: call.ID, Name: call.Name, Content: content, IsError: true}
}

func (b *Toolset) marshalReadPathError(call core.ToolCall, raw string, err error) core.ToolResult {
	return marshalToolError(call, "permission_denied", b.pathDiagnosticMessage(raw, "", err.Error()))
}

func (b *Toolset) marshalPathNotFound(call core.ToolCall, raw, resolved, msg string) core.ToolResult {
	return marshalToolError(call, "not_found", b.pathDiagnosticMessage(raw, resolved, msg))
}

func (b *Toolset) pathDiagnosticMessage(raw, resolved, reason string) string {
	requested := strings.TrimSpace(raw)
	if requested == "" {
		requested = "."
	}
	if strings.TrimSpace(resolved) == "" {
		resolved = cleanTargetPath(requested, b.root)
	}
	var parts []string
	if strings.TrimSpace(reason) != "" {
		parts = append(parts, strings.TrimSpace(reason))
	}
	parts = append(parts,
		"Current workspace root: "+b.root,
		"Requested path: "+requested,
		"Resolved path: "+resolved,
		"Filesystem tools resolve relative paths inside the current workspace. External read paths require file access approval.",
	)
	return strings.Join(parts, "\n")
}

func (b *Toolset) safePath(raw string) (string, error) {
	return b.safeWorkspacePath(raw)
}

func (b *Toolset) safeWorkspacePath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "."
	}
	var target string
	if filepath.IsAbs(raw) {
		target = filepath.Clean(raw)
	} else {
		for strings.HasPrefix(raw, "\\") {
			raw = raw[1:]
		}
		target = filepath.Clean(filepath.Join(b.root, raw))
	}
	rel, err := filepath.Rel(b.root, target)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path escapes workspace: %s", raw)
	}
	return target, nil
}

func (b *Toolset) safeReadPath(ctx context.Context, raw string) (string, error) {
	if expanded := expandHomePath(raw); expanded != strings.TrimSpace(raw) {
		target := cleanTargetPath(expanded, b.root)
		if target == "" {
			return "", fmt.Errorf("path escapes workspace: %s", raw)
		}
		if b.isProjectReadPath(target) || b.isApprovedExternalReadPath(ctx, target) || b.isDiscoveredSkillReadPath(target) {
			return target, nil
		}
		return "", fmt.Errorf("path escapes workspace: %s", strings.TrimSpace(raw))
	}
	if abs, err := b.safeWorkspacePath(raw); err == nil {
		return abs, nil
	}
	target := cleanTargetPath(raw, b.root)
	if target == "" {
		return "", fmt.Errorf("path escapes workspace: %s", raw)
	}
	if b.isProjectReadPath(target) || b.isApprovedExternalReadPath(ctx, target) {
		return target, nil
	}
	if b.isDiscoveredSkillReadPath(target) {
		return target, nil
	}
	return "", fmt.Errorf("path escapes workspace: %s", strings.TrimSpace(raw))
}

func expandHomePath(path string) string {
	path = strings.TrimSpace(path)
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	switch {
	case path == "~", path == "$HOME":
		return home
	case strings.HasPrefix(path, "~/"):
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	case strings.HasPrefix(path, "$HOME/"):
		return filepath.Join(home, strings.TrimPrefix(path, "$HOME/"))
	default:
		return path
	}
}

func cleanTargetPath(raw, root string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "."
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	for strings.HasPrefix(raw, "\\") {
		raw = raw[1:]
	}
	return filepath.Clean(filepath.Join(root, raw))
}

func (b *Toolset) isDiscoveredSkillReadPath(target string) bool {
	targetReal, err := existingRealPath(target)
	if err != nil {
		return false
	}
	for _, skill := range skills.Filter(skills.Discover(skills.DefaultRoots(b.root)), b.skillDisabled) {
		if skill == nil || strings.TrimSpace(skill.Path) == "" {
			continue
		}
		dirReal, err := existingRealPath(skill.Path)
		if err != nil {
			continue
		}
		if pathWithin(targetReal, dirReal) {
			return true
		}
	}
	return false
}

func (b *Toolset) isProjectReadPath(target string) bool {
	if b.worktreeRoot == "" {
		return false
	}
	targetReal, err := existingRealPath(target)
	if err != nil {
		return false
	}
	rootReal, err := existingRealPath(b.worktreeRoot)
	if err != nil {
		return false
	}
	return pathWithin(targetReal, rootReal)
}

func (b *Toolset) isApprovedExternalReadPath(ctx context.Context, target string) bool {
	roots, _ := ctx.Value(externalReadRootsKey{}).([]string)
	if len(roots) == 0 {
		return false
	}
	targetReal := existingOrCleanPath(target)
	if targetReal == "" {
		return false
	}
	for _, root := range roots {
		rootReal := existingOrCleanPath(root)
		if rootReal == "" {
			continue
		}
		if pathWithin(targetReal, rootReal) {
			return true
		}
	}
	return false
}

func existingOrCleanPath(path string) string {
	real, err := existingRealPath(path)
	if err == nil {
		return real
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	return filepath.Clean(abs)
}

func existingRealPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(real), nil
	}
	current := abs
	var missing []string
	for {
		if current == "" || current == string(filepath.Separator) || current == "." {
			return "", os.ErrNotExist
		}
		if real, err := filepath.EvalSymlinks(current); err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				real = filepath.Join(real, missing[i])
			}
			return filepath.Clean(real), nil
		}
		missing = append(missing, filepath.Base(current))
		parent := filepath.Dir(current)
		if parent == current {
			return "", os.ErrNotExist
		}
		current = parent
	}
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func (b *Toolset) displayPath(abs string) string {
	if rel, err := filepath.Rel(b.root, abs); err == nil && rel != "." && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
		return filepath.ToSlash(rel)
	}
	for _, skill := range skills.Filter(skills.Discover(skills.DefaultRoots(b.root)), b.skillDisabled) {
		if skill == nil || strings.TrimSpace(skill.Path) == "" {
			continue
		}
		if rel, err := filepath.Rel(skill.Path, abs); err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			prefix := "$" + skill.Name
			if rel == "." {
				return prefix
			}
			return filepath.ToSlash(filepath.Join(prefix, rel))
		}
	}
	return filepath.ToSlash(abs)
}

func decodeInput(raw string, out any) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return json.Unmarshal([]byte(raw), out)
}
