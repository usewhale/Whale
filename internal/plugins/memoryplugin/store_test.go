package memoryplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/core"
)

func TestStoreWriteReadDeleteAndIndex(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "memory"), t.TempDir())
	store.now = func() time.Time { return time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC) }

	entry, err := store.Write(WriteInput{
		Scope:       "global",
		Type:        "user",
		Name:        "response-style",
		Description: "prefers concise Chinese answers",
		Content:     "Use concise Chinese answers with repo evidence.",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if entry.Created != "2026-05-18" || entry.Updated != "2026-05-18" {
		t.Fatalf("dates not set: %+v", entry)
	}
	if !strings.HasSuffix(entry.Path, filepath.Join("global", "response-style.md")) {
		t.Fatalf("unexpected path: %s", entry.Path)
	}

	_, index, err := store.Index("global")
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if !strings.Contains(index, "[response-style](response-style.md) - prefers concise Chinese answers") {
		t.Fatalf("unexpected index:\n%s", index)
	}

	got, err := store.Read("global", "response-style")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Content != "Use concise Chinese answers with repo evidence." || got.Type != "user" {
		t.Fatalf("unexpected entry: %+v", got)
	}

	deleted, err := store.Delete("global", "response-style")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !deleted {
		t.Fatal("expected delete to report true")
	}
	_, index, err = store.Index("global")
	if err != nil {
		t.Fatalf("Index after delete: %v", err)
	}
	if strings.TrimSpace(index) != "" {
		t.Fatalf("expected empty index after delete, got %q", index)
	}
}

func TestStoreRejectsUnsafeNames(t *testing.T) {
	store := NewStore(t.TempDir(), t.TempDir())
	for _, name := range []string{"../secret", ".hidden", "a", "bad/name", "bad\\name"} {
		_, err := store.Write(WriteInput{
			Scope:       "global",
			Type:        "user",
			Name:        name,
			Description: "desc",
			Content:     "body",
		})
		if err == nil {
			t.Fatalf("expected invalid name %q to fail", name)
		}
	}
}

func TestStartupContextInjectsOnlyIndex(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "memory"), t.TempDir())
	_, err := store.Write(WriteInput{
		Scope:       "global",
		Type:        "user",
		Name:        "response-style",
		Description: "prefers concise Chinese answers",
		Content:     "full body should not be in startup context",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	block, err := StartupContext(context.Background(), store)
	if err != nil {
		t.Fatalf("StartupContext: %v", err)
	}
	if !strings.Contains(block, "# Whale memory") || !strings.Contains(block, "[response-style](response-style.md)") {
		t.Fatalf("missing memory index:\n%s", block)
	}
	if strings.Contains(block, "full body should not be") {
		t.Fatalf("startup context should not include topic body:\n%s", block)
	}
}

func TestMemoryToolsRememberAndRecall(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "memory"), t.TempDir())
	remember := rememberTool{store: store}
	rememberSpec := core.DescribeTool(remember)
	if !containsCapability(rememberSpec.Capabilities, "mutates_state") || strings.TrimSpace(rememberSpec.ApprovalHint) == "" {
		t.Fatalf("remember should describe mutating approval metadata: %+v", rememberSpec)
	}
	input := `{"scope":"global","type":"user","name":"style","description":"concise Chinese","content":"Answer in concise Chinese."}`
	res, err := remember.Run(context.Background(), core.ToolCall{ID: "c1", Name: "remember", Input: input})
	if err != nil || res.IsError {
		t.Fatalf("remember res=%+v err=%v", res, err)
	}
	if !strings.Contains(res.Content, "Treat this as established fact") {
		t.Fatalf("remember result missing session hint: %s", res.Content)
	}
	if !strings.Contains(res.Content, `"write_status":"created"`) {
		t.Fatalf("remember result should report created: %s", res.Content)
	}

	res, err = remember.Run(context.Background(), core.ToolCall{ID: "c1b", Name: "remember", Input: `{"scope":"global","type":"user","name":"style","description":"updated concise Chinese","content":"Answer in very concise Chinese."}`})
	if err != nil || res.IsError {
		t.Fatalf("remember update res=%+v err=%v", res, err)
	}
	if !strings.Contains(res.Content, `"write_status":"updated"`) {
		t.Fatalf("remember update should report updated: %s", res.Content)
	}

	recall := recallTool{store: store}
	recallSpec := core.DescribeTool(recall)
	if !recallSpec.ReadOnly {
		t.Fatalf("recall_memory should be read-only: %+v", recallSpec)
	}
	res, err = recall.Run(context.Background(), core.ToolCall{ID: "c2", Name: "recall_memory", Input: `{"scope":"global","name":"style"}`})
	if err != nil || res.IsError {
		t.Fatalf("recall res=%+v err=%v", res, err)
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.Content), &env); err != nil {
		t.Fatalf("parse recall envelope: %v\n%s", err, res.Content)
	}
	if env.Data["content"] != "Answer in very concise Chinese." {
		t.Fatalf("unexpected recall content: %+v", env.Data)
	}
}

func TestRememberToolPreviewMetadata(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "memory"), t.TempDir())
	remember := rememberTool{store: store}
	call := core.ToolCall{
		ID:    "c1",
		Name:  "remember",
		Input: `{"scope":"global","type":"user","name":"style","description":" concise Chinese answers ","content":"Answer in concise Chinese."}`,
	}

	metadata, err := remember.Preview(context.Background(), call)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	for k, want := range map[string]string{
		"approval_kind":          "memory_write",
		"approval_session_scope": "global memory: style",
		"memory_scope":           "global",
		"memory_type":            "user",
		"memory_name":            "style",
		"memory_description":     "concise Chinese answers",
		"memory_content_preview": "Answer in concise Chinese.",
		"memory_write_status":    "created",
	} {
		if got := metadata[k]; got != want {
			t.Fatalf("metadata[%s]=%q, want %q; all=%+v", k, got, want, metadata)
		}
	}

	if _, err := store.Write(WriteInput{Scope: "global", Type: "user", Name: "style", Description: "old desc", Content: "old content"}); err != nil {
		t.Fatalf("Write existing: %v", err)
	}
	metadata, err = remember.Preview(context.Background(), call)
	if err != nil {
		t.Fatalf("Preview existing: %v", err)
	}
	if got := metadata["memory_write_status"]; got != "updated" {
		t.Fatalf("memory_write_status=%q, want updated; all=%+v", got, metadata)
	}
}

func TestForgetToolPreviewMetadata(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "memory"), t.TempDir())
	if _, err := store.Write(WriteInput{Scope: "project", Type: "project", Name: "roadmap", Description: "plugin-first memory", Content: "Memory is the first official plugin."}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	forget := forgetTool{store: store}

	metadata, err := forget.Preview(context.Background(), core.ToolCall{ID: "c1", Name: "forget", Input: `{"scope":"project","name":"roadmap"}`})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	for k, want := range map[string]string{
		"approval_kind":          "memory_delete",
		"approval_session_scope": "project memory: roadmap",
		"memory_scope":           "project",
		"memory_type":            "project",
		"memory_name":            "roadmap",
		"memory_description":     "plugin-first memory",
		"memory_content_preview": "Memory is the first official plugin.",
	} {
		if got := metadata[k]; got != want {
			t.Fatalf("metadata[%s]=%q, want %q; all=%+v", k, got, want, metadata)
		}
	}
}

func TestForgetToolDescribesMutatingApprovalMetadata(t *testing.T) {
	spec := core.DescribeTool(forgetTool{store: NewStore(filepath.Join(t.TempDir(), "memory"), t.TempDir())})
	if !containsCapability(spec.Capabilities, "mutates_state") || strings.TrimSpace(spec.ApprovalHint) == "" {
		t.Fatalf("forget should describe mutating approval metadata: %+v", spec)
	}
}

func TestForgetToolReturnsSessionHintToIgnoreDeletedMemory(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "memory"), t.TempDir())
	if _, err := store.Write(WriteInput{Scope: "project", Type: "project", Name: "roadmap", Description: "old fact", Content: "Use the old fact."}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	forget := forgetTool{store: store}
	res, err := forget.Run(context.Background(), core.ToolCall{ID: "c1", Name: "forget", Input: `{"scope":"project","name":"roadmap"}`})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	env, ok := core.ParseToolEnvelope(res.Content)
	if !ok {
		t.Fatalf("result is not a tool envelope: %s", res.Content)
	}
	hint, _ := env.Data["session_hint"].(string)
	if !strings.Contains(hint, "Immediately stop using memory project/roadmap") || !strings.Contains(hint, "revoked") {
		t.Fatalf("missing revoke session hint: %+v", env.Data)
	}
}

func containsCapability(caps []string, want string) bool {
	for _, cap := range caps {
		if strings.EqualFold(strings.TrimSpace(cap), want) {
			return true
		}
	}
	return false
}

func TestHandleCommandShowAndForget(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "memory"), t.TempDir())
	if _, err := store.Write(WriteInput{Scope: "project", Type: "project", Name: "roadmap", Description: "plugin-first memory", Content: "Memory is the first official plugin."}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out, ok, err := HandleCommand(store, "/memory show project/roadmap")
	if err != nil || !ok {
		t.Fatalf("show ok=%v err=%v", ok, err)
	}
	if !strings.Contains(out, "Memory is the first official plugin.") {
		t.Fatalf("show output missing body:\n%s", out)
	}
	out, ok, err = HandleCommand(store, "/memory forget project/roadmap")
	if err != nil || !ok {
		t.Fatalf("forget ok=%v err=%v", ok, err)
	}
	if !strings.Contains(out, "forgot memory") {
		t.Fatalf("unexpected forget output: %s", out)
	}
	if _, err := os.Stat(filepath.Join(store.root, "projects", WorkspaceHash(store.workspaceRoot), "roadmap.md")); !os.IsNotExist(err) {
		t.Fatalf("memory file should be deleted, err=%v", err)
	}
}

func TestFormatListShowsCountsAndHighCountWarning(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "memory"), t.TempDir())
	for i := 0; i < 50; i++ {
		if _, err := store.Write(WriteInput{
			Scope:       "global",
			Type:        "reference",
			Name:        fmt.Sprintf("memory-%02d", i),
			Description: fmt.Sprintf("memory %02d", i),
			Content:     "body",
		}); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	out := FormatList(store)
	for _, want := range []string{
		"Manage durable memories.",
		"Scopes",
		"- global: available across sessions and workspaces",
		"- project: available for this workspace",
		"Memory indexes load automatically at startup.",
		"Common actions",
		"- /memory show <global|project>/<name>",
		"Global (50 memories)",
		"Project (0 memories)",
		"Memory count is high",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected memory list to contain %q:\n%s", want, out)
		}
	}
}
