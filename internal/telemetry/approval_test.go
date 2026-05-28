package telemetry

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAppendApprovalEventWritesSessionSidecar(t *testing.T) {
	dir := t.TempDir()
	now := time.UnixMilli(1234567890)
	err := AppendApprovalEvent(dir, ApprovalEvent{
		Session:    "s/1",
		ToolCallID: "tc-1",
		Tool:       "shell_run",
		Event:      "approval_required",
		Code:       "permission_required",
		Keys:       []string{"shell:bounded:git:status"},
		Scope:      "this bounded shell command family",
	}, now)
	if err != nil {
		t.Fatalf("append approval event: %v", err)
	}

	path := ApprovalEventsPath(dir, "s/1")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open sidecar: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		t.Fatal("expected one event line")
	}
	var got ApprovalEvent
	if err := json.Unmarshal(scanner.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if got.TS != now.UnixMilli() || got.Session != "s/1" || got.ToolCallID != "tc-1" || got.Event != "approval_required" || got.Keys[0] != "shell:bounded:git:status" {
		t.Fatalf("unexpected event: %+v", got)
	}
	var raw map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal raw event: %v", err)
	}
	if _, ok := raw["input"]; ok {
		t.Fatalf("raw input must not be logged: %v", raw)
	}
}

func TestAppendApprovalEventNoopsWithoutSessionOrEvent(t *testing.T) {
	dir := t.TempDir()
	if err := AppendApprovalEvent(dir, ApprovalEvent{Event: "approval_required"}, time.Now()); err != nil {
		t.Fatalf("empty session should be a no-op: %v", err)
	}
	if err := AppendApprovalEvent(dir, ApprovalEvent{Session: "s1"}, time.Now()); err != nil {
		t.Fatalf("empty event should be a no-op: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no files, got %d", len(entries))
	}
}

func TestAppendApprovalEventRedactsShellExactCommandKeys(t *testing.T) {
	dir := t.TempDir()
	secretCommand := `curl -H "Authorization: Bearer sk-secret" "https://example.test?token=abc"`
	err := AppendApprovalEvent(dir, ApprovalEvent{
		Session: "s1",
		Tool:    "shell_run",
		Event:   "approval_required",
		Key:     "shell_run|cmd:" + secretCommand,
		Keys: []string{
			"shell_run|cmd:" + secretCommand,
			"shell:bounded:go:test",
		},
		Scope: "shell_run|cmd:" + secretCommand,
	}, time.UnixMilli(1))
	if err != nil {
		t.Fatalf("append approval event: %v", err)
	}

	raw, err := os.ReadFile(ApprovalEventsPath(dir, "s1"))
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	for _, leak := range []string{"sk-secret", "token=abc", secretCommand} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("approval sidecar leaked %q: %s", leak, raw)
		}
	}

	var got ApprovalEvent
	if err := json.Unmarshal(bytes.TrimSpace(raw), &got); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if !strings.HasPrefix(got.Key, "shell_run|cmd_sha256:") {
		t.Fatalf("key was not redacted: %q", got.Key)
	}
	if len(got.Keys) != 2 || !strings.HasPrefix(got.Keys[0], "shell_run|cmd_sha256:") || got.Keys[1] != "shell:bounded:go:test" {
		t.Fatalf("keys were not redacted conservatively: %+v", got.Keys)
	}
	if !strings.HasPrefix(got.Scope, "shell_run|cmd_sha256:") {
		t.Fatalf("scope was not redacted: %q", got.Scope)
	}
}

func TestAppendApprovalEventRedactsFallbackRawInputKeys(t *testing.T) {
	dir := t.TempDir()
	rawInput := `{"url":"https://example.test","authorization":"Bearer sk-custom-secret"}`
	err := AppendApprovalEvent(dir, ApprovalEvent{
		Session: "s1",
		Tool:    "mcp_custom_tool",
		Event:   "approval_required",
		Key:     "mcp_custom_tool|" + rawInput,
		Keys: []string{
			"mcp_custom_tool|" + rawInput,
			"file:README.md",
		},
	}, time.UnixMilli(1))
	if err != nil {
		t.Fatalf("append approval event: %v", err)
	}

	raw, err := os.ReadFile(ApprovalEventsPath(dir, "s1"))
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	for _, leak := range []string{"sk-custom-secret", "authorization", rawInput} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("approval sidecar leaked %q: %s", leak, raw)
		}
	}

	var got ApprovalEvent
	if err := json.Unmarshal(bytes.TrimSpace(raw), &got); err != nil {
		t.Fatalf("unmarshal event: %v", err)
	}
	if !strings.HasPrefix(got.Key, "mcp_custom_tool|input_sha256:") {
		t.Fatalf("fallback key was not redacted: %q", got.Key)
	}
	if len(got.Keys) != 2 || !strings.HasPrefix(got.Keys[0], "mcp_custom_tool|input_sha256:") || got.Keys[1] != "file:README.md" {
		t.Fatalf("keys were not redacted conservatively: %+v", got.Keys)
	}
}
