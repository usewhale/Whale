package policy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func TestApprovalKeysUseSharedFileKeyForEditAndWrite(t *testing.T) {
	edit := core.ToolCall{ID: "edit-1", Name: "edit", Input: `{"file_path":"./a.txt","search":"old","replace":"new"}`}
	write := core.ToolCall{ID: "write-1", Name: "write", Input: `{"file_path":"a.txt","content":"new"}`}

	if got, want := ApprovalKeys(edit), []string{"file:a.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("edit keys = %v, want %v", got, want)
	}
	if got, want := ApprovalKeys(write), []string{"file:a.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("write keys = %v, want %v", got, want)
	}
}

func TestApprovalKeysExtractApplyPatchFiles(t *testing.T) {
	call := core.ToolCall{ID: "patch-1", Name: "apply_patch", Input: `{"patch":"*** Begin Patch\n*** Update File: b.txt\n@@\n-old\n+new\n*** Add File: a.txt\n+created\n*** Update File: b.txt\n@@\n-new\n+newer\n*** End Patch"}`}

	if got, want := ApprovalKeys(call), []string{"file:a.txt", "file:b.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("apply_patch keys = %v, want %v", got, want)
	}
	if got, want := ApprovalFiles(call), []string{"a.txt", "b.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("apply_patch files = %v, want %v", got, want)
	}
	if got := ApprovalSessionScope(call); got != "these files: a.txt, b.txt" {
		t.Fatalf("session scope = %q", got)
	}
	if got := ApprovalScope(call); got != "files:a.txt,b.txt" {
		t.Fatalf("approval scope = %q", got)
	}
}

func TestApprovalKeysKeepShellCommandScope(t *testing.T) {
	call := core.ToolCall{ID: "shell-1", Name: "shell_run", Input: `{"command":"go test ./..."}`}

	if got, want := ApprovalKeys(call), []string{"shell_run|cmd:go test ./..."}; !reflect.DeepEqual(got, want) {
		t.Fatalf("shell keys = %v, want %v", got, want)
	}
	if got := ApprovalSessionScope(call); got != "this shell command" {
		t.Fatalf("session scope = %q", got)
	}
}

func TestApprovalKeysUseMemoryPayloadForWrites(t *testing.T) {
	remember := core.ToolCall{ID: "memory-1", Name: "remember", Input: `{"scope":"global","type":"user","name":"style","description":"old","content":"old"}`}
	rememberUpdate := core.ToolCall{ID: "memory-2", Name: "remember", Input: `{"scope":"global","type":"user","name":"style","description":"new","content":"new"}`}
	forget := core.ToolCall{ID: "memory-3", Name: "forget", Input: `{"scope":"global","name":"style"}`}

	rememberKeys := ApprovalKeys(remember)
	rememberUpdateKeys := ApprovalKeys(rememberUpdate)
	if len(rememberKeys) != 1 || !strings.HasPrefix(rememberKeys[0], "memory:remember:global:style:payload:") {
		t.Fatalf("remember keys = %v", rememberKeys)
	}
	if len(rememberUpdateKeys) != 1 || !strings.HasPrefix(rememberUpdateKeys[0], "memory:remember:global:style:payload:") {
		t.Fatalf("remember update keys = %v", rememberUpdateKeys)
	}
	if reflect.DeepEqual(rememberKeys, rememberUpdateKeys) {
		t.Fatalf("changed memory payload should not reuse approval key: %v", rememberKeys)
	}
	if got, want := ApprovalKeys(forget), []string{"memory:forget:global:style"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("forget keys = %v, want %v", got, want)
	}
}
