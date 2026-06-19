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

func TestApprovalKeysUseSharedFileKeyForMultiEdit(t *testing.T) {
	call := core.ToolCall{ID: "multi-1", Name: "multi_edit", Input: `{"file_path":"./b.txt","edits":[{"search":"old","replace":"new"}]}`}

	if got, want := ApprovalKeys(call), []string{"file:b.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("multi_edit keys = %v, want %v", got, want)
	}
	if got, want := ApprovalFiles(call), []string{"b.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("multi_edit files = %v, want %v", got, want)
	}
	if got := ApprovalSessionScope(call); got != "this file: b.txt" {
		t.Fatalf("session scope = %q", got)
	}
	if got := ApprovalScope(call); got != "file:b.txt" {
		t.Fatalf("approval scope = %q", got)
	}
}

func TestApprovalKeysKeepShellCommandScope(t *testing.T) {
	call := core.ToolCall{ID: "shell-1", Name: "shell_run", Input: `{"command":"go test ./..."}`}

	if got, want := ApprovalKeys(call), []string{"shell:bounded:go:test"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("shell keys = %v, want %v", got, want)
	}
	if got := ApprovalSessionScope(call); got != "this bounded shell command family" {
		t.Fatalf("session scope = %q", got)
	}
}

func TestApprovalKeysTreatWriteStdinAsTerminalWrite(t *testing.T) {
	call := core.ToolCall{ID: "stdin-1", Name: "write_stdin", Input: `{"task_id":"task-1","keys":["enter"]}`}

	wantKey := WriteStdinApprovalKey(map[string]any{"task_id": "task-1", "keys": []any{"enter"}})
	if got, want := ApprovalKeys(call), []string{wantKey}; !reflect.DeepEqual(got, want) {
		t.Fatalf("write_stdin keys = %v, want %v", got, want)
	}
	if !strings.HasPrefix(wantKey, "terminal:write_stdin:task-1:payload:") {
		t.Fatalf("write_stdin key should include task and payload hash, got %q", wantKey)
	}
	same := core.ToolCall{ID: "stdin-2", Name: "write_stdin", Input: `{"keys":["enter"],"task_id":"task-1"}`}
	if got := ApprovalKeys(same); !reflect.DeepEqual(got, []string{wantKey}) {
		t.Fatalf("same write_stdin payload should keep same key, got %v want %v", got, []string{wantKey})
	}
	otherTask := core.ToolCall{ID: "stdin-3", Name: "write_stdin", Input: `{"task_id":"task-2","keys":["enter"]}`}
	if got := ApprovalKeys(otherTask); reflect.DeepEqual(got, []string{wantKey}) {
		t.Fatalf("different task should not reuse write_stdin key: %v", got)
	}
	otherInput := core.ToolCall{ID: "stdin-4", Name: "write_stdin", Input: `{"task_id":"task-1","chars":"rm -rf /","keys":["enter"]}`}
	if got := ApprovalKeys(otherInput); reflect.DeepEqual(got, []string{wantKey}) {
		t.Fatalf("different stdin payload should not reuse write_stdin key: %v", got)
	}
	if got := ApprovalKind(call); got != "terminal" {
		t.Fatalf("approval kind = %q, want terminal", got)
	}
	if got := ApprovalScope(call); got != "terminal" {
		t.Fatalf("approval scope = %q, want terminal", got)
	}
	if got := ApprovalSessionScope(call); got != "this stdin write" {
		t.Fatalf("session scope = %q", got)
	}
}

func TestApprovalSummaryShowsWriteStdinPayload(t *testing.T) {
	call := core.ToolCall{Name: "write_stdin", Input: `{"task_id":"task-1","chars":"npm install left-pad\n","keys":["enter"]}`}
	got := ApprovalSummary(call)
	for _, want := range []string{
		"write_stdin: task-1",
		`chars="npm install left-pad\n"`,
		"keys=[enter]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("ApprovalSummary missing %q in %q", want, got)
		}
	}

	long := core.ToolCall{Name: "write_stdin", Input: `{"task_id":"task-2","chars":"` + strings.Repeat("x", 200) + `"}`}
	got = ApprovalSummary(long)
	if !strings.Contains(got, "task-2") || !strings.Contains(got, "[truncated]") || strings.Contains(got, strings.Repeat("x", 200)) {
		t.Fatalf("ApprovalSummary should show truncated write_stdin payload, got %q", got)
	}
}

func TestApprovalKeysUseWebCommandAndHostScopes(t *testing.T) {
	search := core.ToolCall{Name: "web_search", Input: `{"query":"Node.js permission model"}`}
	if got, want := ApprovalKeys(search), []string{"web_search:*"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("web search keys = %v, want %v", got, want)
	}
	fetch := core.ToolCall{Name: "fetch", Input: `{"url":"https://www.nodejs.org/api/permissions.html","prompt":"extract"}`}
	if got, want := ApprovalKeys(fetch), []string{"web_fetch:host:nodejs.org"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fetch keys = %v, want %v", got, want)
	}
	if got := ApprovalSessionScope(fetch); got != "nodejs.org" {
		t.Fatalf("fetch session scope = %q", got)
	}
}

func TestApprovalKeysKeepExactScopeForUnclassifiedShellCommand(t *testing.T) {
	call := core.ToolCall{ID: "shell-1", Name: "shell_run", Input: `{"command":"npm install lodash"}`}

	if got, want := ApprovalKeys(call), []string{"shell_run|cmd:npm install lodash"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("shell keys = %v, want %v", got, want)
	}
	if got := ApprovalSessionScope(call); got != "this shell command" {
		t.Fatalf("session scope = %q", got)
	}
}

func TestApprovalKeysKeepExactScopeForGoBinaryOutputs(t *testing.T) {
	tests := map[string][]string{
		"go test -c ./pkg":               {"shell_run|cmd:go test -c ./pkg"},
		"go test -exec ./wrapper ./pkg":  {"shell_run|cmd:go test -exec ./wrapper ./pkg"},
		"go test -toolexec ./wrap ./pkg": {"shell_run|cmd:go test -toolexec ./wrap ./pkg"},
		"go build ./cmd/app":             {"shell_run|cmd:go build ./cmd/app"},
	}
	for command, want := range tests {
		call := core.ToolCall{ID: "shell-1", Name: "shell_run", Input: `{"command":"` + command + `"}`}
		if got := ApprovalKeys(call); !reflect.DeepEqual(got, want) {
			t.Fatalf("ApprovalKeys(%q) = %v, want %v", command, got, want)
		}
	}
}

func TestApprovalMetadataIncludesShellRisk(t *testing.T) {
	call := core.ToolCall{ID: "shell-1", Name: "shell_run", Input: `{"command":"go test ./..."}`}

	got := ApprovalMetadata(call, ApprovalKeys(call), nil)
	if got["shell_risk_code"] != "bounded_write" {
		t.Fatalf("shell_risk_code = %v, metadata=%+v", got["shell_risk_code"], got)
	}
	if got["shell_risk_level"] != "bounded_write" {
		t.Fatalf("shell_risk_level = %v, metadata=%+v", got["shell_risk_level"], got)
	}
	if got["shell_approval_family"] != true {
		t.Fatalf("shell_approval_family = %v, metadata=%+v", got["shell_approval_family"], got)
	}
	scopes, ok := got["shell_write_scopes"].([]string)
	if !ok || len(scopes) == 0 {
		t.Fatalf("shell_write_scopes missing: %+v", got)
	}
}

func TestApprovalMetadataIncludesGrantEffect(t *testing.T) {
	call := core.ToolCall{ID: "read-1", Name: "read_file", Input: `{"file_path":"/outside/a.txt"}`}

	got := ApprovalMetadata(call, []string{"grant:external_directory:/outside"}, nil)
	if got["effect_kind"] != "external_directory" {
		t.Fatalf("effect_kind = %v, metadata=%+v", got["effect_kind"], got)
	}
	if got["effect_scope"] != "/outside" || got["grant_pattern"] != "/outside" {
		t.Fatalf("grant metadata = %+v, want /outside scope and pattern", got)
	}
}

func TestApprovalKeysForDecisionUseExternalReadDirectoryScope(t *testing.T) {
	decision := PolicyDecision{Permission: "external_directory", Pattern: "/repo/external", RequiresApproval: true}
	calls := []core.ToolCall{
		{Name: "read_file", Input: `{"file_path":"../external/a.go"}`},
		{Name: "list_dir", Input: `{"path":"../external"}`},
		{Name: "grep", Input: `{"path":"../external","pattern":"needle"}`},
		{Name: "search_files", Input: `{"path":"../external","pattern":"a"}`},
	}
	for _, call := range calls {
		if got, want := ApprovalKeysForDecision(call, decision), []string{"grant:external_directory:/repo/external"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s external read keys = %v, want %v", call.Name, got, want)
		}
	}

	roots := ExternalReadApprovalRootsFromKeys([]string{"grant:external_directory:/repo/external"})
	if !reflect.DeepEqual(roots, []string{"/repo/external"}) {
		t.Fatalf("external read roots = %v", roots)
	}
}

func TestApprovalKeysForDecisionKeepCommandKeyWhenShellRuleAlsoAsks(t *testing.T) {
	// A shell command into an external dir is path-eligible, so its
	// external_directory requirement is satisfied by a directory grant. But when
	// the command independently requires approval via a shell rule (the rm ask
	// rule here), ApprovalKeysForDecision must also keep the command-scoped key,
	// so the directory grant alone cannot approve it. Mutation protection lives
	// in the shell rules, not in external-directory eligibility.
	command := "rm /tmp/a"
	call := core.ToolCall{Name: "shell_run", Input: `{"command":"` + command + `"}`}
	decision := PolicyDecision{
		Permission:       "external_directory",
		Pattern:          "/tmp",
		RequiresApproval: true,
		ApprovalRequirements: []ApprovalRequirement{
			{Permission: "shell", Pattern: command},
			{Permission: "external_directory", Pattern: "/tmp"},
		},
	}

	keys := ApprovalKeysForDecision(call, decision)
	cache := NewSessionApprovalCache()
	cache.Grant("s", "grant:external_directory:/tmp")
	if cache.HasAll("s", keys) {
		t.Fatalf("directory grant alone must not approve a command that also has a shell ask rule; keys=%v", keys)
	}
	// Granting the command key too lets it through.
	cache.GrantAll("s", ShellApprovalKeys(command))
	if !cache.HasAll("s", keys) {
		t.Fatalf("expected approval once both the directory grant and the command key are granted; keys=%v", keys)
	}
}

func TestApprovalKeysForDecisionDoNotReuseExternalDirectoryGrantForMutatingFileTools(t *testing.T) {
	decision := PolicyDecision{Permission: "external_directory", Pattern: "/tmp", RequiresApproval: true}
	calls := []core.ToolCall{
		{Name: "edit", Input: `{"file_path":"/tmp/a.txt","search":"old","replace":"new"}`},
		{Name: "write", Input: `{"file_path":"/tmp/a.txt","content":"new"}`},
		{Name: "multi_edit", Input: `{"file_path":"/tmp/a.txt","edits":[{"search":"old","replace":"new"}]}`},
	}

	cache := NewSessionApprovalCache()
	cache.Grant("s", "grant:external_directory:/tmp")
	for _, call := range calls {
		keys := ApprovalKeysForDecision(call, decision)
		want := ApprovalKeys(call)
		if !reflect.DeepEqual(keys, want) {
			t.Fatalf("%s external directory keys = %v, want file-specific keys %v", call.Name, keys, want)
		}
		if cache.HasAll("s", keys) {
			t.Fatalf("external directory grant must not approve %s", call.Name)
		}
	}
}

func TestApprovalKeysForDecisionDoNotPersistExternalDirectoryGrantForMCPTools(t *testing.T) {
	call := core.ToolCall{Name: "mcp__fs__read_file", Input: `{"path":"/tmp/a.txt"}`}
	decision := PolicyDecision{Permission: "external_directory", Pattern: "/tmp", RequiresApproval: true}

	keys := ApprovalKeysForDecision(call, decision)
	want := ApprovalKeys(call)
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("MCP external directory keys = %v, want exact MCP approval keys %v", keys, want)
	}

	cache := NewSessionApprovalCache()
	cache.Grant("s", "grant:external_directory:/tmp")
	if cache.HasAll("s", keys) {
		t.Fatal("external directory grant must not approve MCP tool keys")
	}
}

func TestApprovalKeysForDecisionPreserveReadRuleApprovalForExternalReads(t *testing.T) {
	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules(), WorkspaceRoot: "/repo"}
	call := core.ToolCall{Name: "read_file", Input: `{"file_path":"/outside/.env"}`}
	decision := p.Decide(core.ToolSpec{Name: "read_file"}, call)
	if !decision.RequiresApproval || decision.Permission != "external_directory" {
		t.Fatalf("external .env decision = %+v, want external_directory approval with read requirement", decision)
	}

	keys := ApprovalKeysForDecision(call, decision)
	want := []string{"grant:external_directory:/outside", `read_file|{"file_path":"/outside/.env"}`}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("external .env keys = %v, want %v", keys, want)
	}

	cache := NewSessionApprovalCache()
	cache.Grant("s", "grant:external_directory:/outside")
	if cache.HasAll("s", keys) {
		t.Fatal("external directory grant must not bypass the sensitive read approval key")
	}
	cache.Grant("s", `read_file|{"file_path":"/outside/.env"}`)
	if !cache.HasAll("s", keys) {
		t.Fatal("expected cache hit after both external directory and sensitive read keys are granted")
	}
}

func TestExternalReadRootsForDecisionPreserveConfiguredAllowWithReadApproval(t *testing.T) {
	rules := append(DefaultRules(), PermissionRule{Permission: "external_directory", Pattern: "/outside", Action: PermissionAllow})
	p := RulePolicy{Default: PermissionAllow, Rules: rules, WorkspaceRoot: "/repo"}
	call := core.ToolCall{Name: "read_file", Input: `{"file_path":"/outside/.env"}`}
	decision := p.Decide(core.ToolSpec{Name: "read_file"}, call)
	if !decision.RequiresApproval || decision.Permission != "read" {
		t.Fatalf("external .env decision = %+v, want read approval with configured external allow", decision)
	}

	keys := ApprovalKeysForDecision(call, decision)
	wantKeys := []string{`read_file|{"file_path":"/outside/.env"}`}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Fatalf("mixed approval keys = %v, want only read approval key %v", keys, wantKeys)
	}

	roots := ExternalReadApprovalRootsForDecision(call, decision)
	if !reflect.DeepEqual(roots, []string{"/outside"}) {
		t.Fatalf("external read roots = %v, want configured /outside root", roots)
	}
}

func TestSessionApprovalCacheReusesExternalReadParentRoots(t *testing.T) {
	cache := NewSessionApprovalCache()
	cache.Grant("s", "grant:external_directory:/outside")

	if !cache.Has("s", "grant:external_directory:/outside/sub/file.go") {
		t.Fatal("expected parent external_directory approval to cover descendant")
	}
	if cache.Has("s", "grant:external_directory:/outside-other/file.go") {
		t.Fatal("external_directory approval must not cover sibling paths")
	}
	if cache.Has("s", "grant:external_directory:/out") {
		t.Fatal("external_directory child approval must not imply parent approval")
	}
}

func TestSessionApprovalCachePreservesLegacyExternalReadGrants(t *testing.T) {
	cache := NewSessionApprovalCache()
	cache.Grant("s", "external_read:/outside")

	if !cache.Has("s", "grant:external_directory:/outside/sub/file.go") {
		t.Fatal("expected legacy external_read approval to cover descendant external directory grant")
	}
	if cache.Has("s", "grant:external_directory:/outside-other/file.go") {
		t.Fatal("legacy external_read approval must not cover sibling paths")
	}
	if cache.Has("s", "shell_run|cmd:cat /outside/sub/file.go") {
		t.Fatal("legacy external_read approval must not approve shell command keys")
	}
}

func TestExternalReadRootsFromKeysPreservesLegacyExternalReadKeys(t *testing.T) {
	roots := ExternalReadApprovalRootsFromKeys([]string{"external_read:/outside"})
	if !reflect.DeepEqual(roots, []string{"/outside"}) {
		t.Fatalf("legacy external read roots = %v, want /outside", roots)
	}
}

func TestNormalizeShellApprovalCommand(t *testing.T) {
	tests := map[string]string{
		"go test ./...":                  "go test ./...",
		"make test-tui":                  "make test-tui",
		"npm install lodash":             "npm install lodash",
		"python3 -m pytest tests":        "python3 -m pytest tests",
		"node scripts/check.js --strict": "node scripts/check.js --strict",
		"bash ./safe.sh":                 "bash ./safe.sh",
		"   git   status  --short  ":     "git   status  --short",
		"echo foo\nrm -rf /tmp/x":        "echo foo\nrm -rf /tmp/x",
	}
	for command, want := range tests {
		if got := NormalizeShellApprovalCommand(command); got != want {
			t.Fatalf("NormalizeShellApprovalCommand(%q) = %q, want %q", command, got, want)
		}
	}
}

func TestShellApprovalKeysPreserveSemanticWhitespace(t *testing.T) {
	oneCommand := core.ToolCall{ID: "shell-1", Name: "shell_run", Input: `{"command":"echo foo rm -rf /tmp/x"}`}
	twoCommands := core.ToolCall{ID: "shell-2", Name: "shell_run", Input: `{"command":"echo foo\nrm -rf /tmp/x"}`}

	if reflect.DeepEqual(ApprovalKeys(oneCommand), ApprovalKeys(twoCommands)) {
		t.Fatalf("shell approval keys should preserve semantic whitespace: %v", ApprovalKeys(oneCommand))
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
