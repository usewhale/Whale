package policy

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/usewhale/whale/internal/core"
)

type ApprovalRequest struct {
	SessionID string
	ToolCall  core.ToolCall
	Spec      core.ToolSpec
	Reason    string
	Code      string
	Key       string
	Keys      []string
	Metadata  map[string]any
}

type ApprovalDecision int

const (
	ApprovalDeny ApprovalDecision = iota
	ApprovalAllow
	ApprovalAllowForSession
	ApprovalCancel
)

func (d ApprovalDecision) Approved() bool {
	return d == ApprovalAllow || d == ApprovalAllowForSession
}

func (d ApprovalDecision) ForSession() bool {
	return d == ApprovalAllowForSession
}

func (d ApprovalDecision) Canceled() bool {
	return d == ApprovalCancel
}

type ApprovalFunc func(req ApprovalRequest) ApprovalDecision

type SessionApprovalCache struct {
	mu     sync.RWMutex
	data   map[string]map[string]bool
	loaded map[string]bool
}

func NewSessionApprovalCache() *SessionApprovalCache {
	return &SessionApprovalCache{
		data:   make(map[string]map[string]bool),
		loaded: make(map[string]bool),
	}
}

func (c *SessionApprovalCache) Has(sessionID, key string) bool {
	return c.HasAll(sessionID, []string{key})
}

func (c *SessionApprovalCache) HasAll(sessionID string, keys []string) bool {
	if len(keys) == 0 {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	bySession, ok := c.data[sessionID]
	if !ok {
		return false
	}
	for _, key := range keys {
		if strings.TrimSpace(key) == "" || !bySession[key] {
			return false
		}
	}
	return true
}

func (c *SessionApprovalCache) Grant(sessionID, key string) {
	c.GrantAll(sessionID, []string{key})
}

func (c *SessionApprovalCache) GrantAll(sessionID string, keys []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	bySession, ok := c.data[sessionID]
	if !ok {
		bySession = make(map[string]bool)
		c.data[sessionID] = bySession
	}
	for _, key := range keys {
		if strings.TrimSpace(key) != "" {
			bySession[key] = true
		}
	}
}

func (c *SessionApprovalCache) SetLoaded(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loaded[sessionID] = true
}

func (c *SessionApprovalCache) IsLoaded(sessionID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.loaded[sessionID]
}

func (c *SessionApprovalCache) Merge(sessionID string, keys map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	bySession, ok := c.data[sessionID]
	if !ok {
		bySession = make(map[string]bool)
		c.data[sessionID] = bySession
	}
	for k, v := range keys {
		if v {
			bySession[k] = true
		}
	}
}

func ApprovalKey(call core.ToolCall) string {
	keys := ApprovalKeys(call)
	if len(keys) > 0 {
		return keys[0]
	}
	return call.Name + "|" + strings.TrimSpace(call.Input)
}

func ApprovalKeys(call core.ToolCall) []string {
	base := call.Name
	var body map[string]any
	if err := json.Unmarshal([]byte(call.Input), &body); err != nil {
		return []string{base + "|" + strings.TrimSpace(call.Input)}
	}
	switch call.Name {
	case "edit", "write":
		if v, _ := body["file_path"].(string); strings.TrimSpace(v) != "" {
			return []string{approvalFileKey(v)}
		}
	case "apply_patch":
		if files := approvalPatchFiles(body); len(files) > 0 {
			keys := make([]string, 0, len(files))
			for _, file := range files {
				keys = append(keys, approvalFileKey(file))
			}
			return keys
		}
	case "shell_run":
		if v, _ := body["command"].(string); strings.TrimSpace(v) != "" {
			return []string{base + "|cmd:" + NormalizeShellApprovalCommand(v)}
		}
	case "remember":
		scope, _ := body["scope"].(string)
		name, _ := body["name"].(string)
		scope = strings.ToLower(strings.TrimSpace(scope))
		name = strings.TrimSpace(name)
		if scope != "" && name != "" {
			return []string{fmt.Sprintf("memory:%s:%s:%s:%s", base, scope, name, memoryWritePayloadHash(body))}
		}
	case "forget":
		scope, _ := body["scope"].(string)
		name, _ := body["name"].(string)
		scope = strings.ToLower(strings.TrimSpace(scope))
		name = strings.TrimSpace(name)
		if scope != "" && name != "" {
			return []string{fmt.Sprintf("memory:%s:%s:%s", base, scope, name)}
		}
	}
	return []string{base + "|" + strings.TrimSpace(call.Input)}
}

func memoryWritePayloadHash(body map[string]any) string {
	payload := map[string]string{}
	for _, key := range []string{"type", "description", "content"} {
		if v, ok := body[key].(string); ok {
			payload[key] = strings.TrimSpace(v)
		}
	}
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return fmt.Sprintf("payload:%x", sum[:8])
}

func ApprovalRequestKeys(req ApprovalRequest) []string {
	if len(req.Keys) > 0 {
		return compactApprovalKeys(req.Keys)
	}
	if strings.TrimSpace(req.Key) != "" {
		return []string{req.Key}
	}
	return ApprovalKeys(req.ToolCall)
}

func ApprovalKeysFileScoped(keys []string) bool {
	keys = compactApprovalKeys(keys)
	if len(keys) == 0 {
		return false
	}
	for _, key := range keys {
		if !strings.HasPrefix(key, "file:") {
			return false
		}
	}
	return true
}

func ApprovalFiles(call core.ToolCall) []string {
	var body map[string]any
	if err := json.Unmarshal([]byte(call.Input), &body); err != nil {
		return nil
	}
	switch call.Name {
	case "edit", "write":
		if v, _ := body["file_path"].(string); strings.TrimSpace(v) != "" {
			return []string{approvalCleanPath(v)}
		}
	case "apply_patch":
		return approvalPatchFiles(body)
	}
	return nil
}

func ApprovalKind(call core.ToolCall) string {
	switch call.Name {
	case "edit", "write", "apply_patch":
		return "file_diff_review"
	case "shell_run":
		return "shell"
	default:
		return "tool"
	}
}

func ApprovalSessionScope(call core.ToolCall) string {
	files := ApprovalFiles(call)
	switch len(files) {
	case 0:
		if call.Name == "shell_run" {
			return "this shell command"
		}
		return "this tool request"
	case 1:
		return "this file: " + files[0]
	default:
		return "these files: " + formatApprovalFiles(files)
	}
}

func NormalizeShellApprovalCommand(command string) string {
	return strings.TrimSpace(command)
}

func ApprovalMetadata(call core.ToolCall, keys []string, metadata map[string]any) map[string]any {
	out := make(map[string]any, len(metadata)+5)
	for k, v := range metadata {
		out[k] = v
	}
	if strings.TrimSpace(stringValue(out["approval_kind"])) == "" {
		out["approval_kind"] = ApprovalKind(call)
	}
	if strings.TrimSpace(stringValue(out["approval_scope"])) == "" {
		out["approval_scope"] = ApprovalScope(call)
	}
	if strings.TrimSpace(stringValue(out["approval_session_scope"])) == "" {
		out["approval_session_scope"] = ApprovalSessionScope(call)
	}
	if compact := compactApprovalKeys(keys); len(compact) > 0 {
		out["approval_keys"] = compact
	}
	if files := ApprovalFiles(call); len(files) > 0 {
		out["approval_files"] = files
	}
	return out
}

func stringValue(v any) string {
	s, _ := v.(string)
	return s
}

func ApprovalSummary(call core.ToolCall) string {
	var body map[string]any
	if err := json.Unmarshal([]byte(call.Input), &body); err != nil {
		return call.Name
	}
	switch call.Name {
	case "shell_run":
		if v, _ := body["command"].(string); strings.TrimSpace(v) != "" {
			return fmt.Sprintf("shell_run: %s", strings.TrimSpace(v))
		}
	case "write":
		if v, _ := body["file_path"].(string); strings.TrimSpace(v) != "" {
			return fmt.Sprintf("write: %s", strings.TrimSpace(v))
		}
	case "edit":
		if v, _ := body["file_path"].(string); strings.TrimSpace(v) != "" {
			return fmt.Sprintf("edit: %s", strings.TrimSpace(v))
		}
	case "apply_patch":
		if files := approvalPatchFiles(body); len(files) > 0 {
			return fmt.Sprintf("apply_patch: %s", formatApprovalFiles(files))
		}
		return "apply_patch: patch payload"
	}
	return call.Name
}

func ApprovalScope(call core.ToolCall) string {
	var body map[string]any
	if err := json.Unmarshal([]byte(call.Input), &body); err != nil {
		return "workspace"
	}
	if v, _ := body["file_path"].(string); strings.TrimSpace(v) != "" {
		return "file:" + approvalCleanPath(v)
	}
	if v, _ := body["path"].(string); strings.TrimSpace(v) != "" {
		return "path:" + strings.TrimSpace(v)
	}
	if call.Name == "apply_patch" {
		files := approvalPatchFiles(body)
		if len(files) == 1 {
			return "file:" + files[0]
		}
		if len(files) > 1 {
			return "files:" + strings.Join(files, ",")
		}
		return "patch"
	}
	if call.Name == "shell_run" {
		return "shell"
	}
	return "workspace"
}

func approvalFileKey(path string) string {
	return "file:" + approvalCleanPath(path)
}

func approvalCleanPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(path))
}

func approvalPatchFiles(body map[string]any) []string {
	patch, _ := body["patch"].(string)
	if strings.TrimSpace(patch) == "" {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(patch, "\r\n", "\n"), "\r", "\n"), "\n")
	seen := map[string]bool{}
	for _, line := range lines {
		for _, prefix := range []string{"*** Update File: ", "*** Add File: ", "*** Delete File: "} {
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			path := approvalCleanPath(strings.TrimPrefix(line, prefix))
			if path != "" {
				seen[path] = true
			}
		}
	}
	files := make([]string, 0, len(seen))
	for file := range seen {
		files = append(files, file)
	}
	sort.Strings(files)
	return files
}

func compactApprovalKeys(keys []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out
}

func formatApprovalFiles(files []string) string {
	files = append([]string(nil), files...)
	sort.Strings(files)
	const maxVisible = 3
	if len(files) <= maxVisible {
		return strings.Join(files, ", ")
	}
	return fmt.Sprintf("%s, +%d more", strings.Join(files[:maxVisible], ", "), len(files)-maxVisible)
}
