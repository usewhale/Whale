package policy

import (
	"strconv"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func TestDefaultToolPolicyPrefixRulesApplyToShellRunCommand(t *testing.T) {
	p := DefaultToolPolicy{
		Mode:          ApprovalModeOnRequest,
		AllowPrefixes: []string{"git status"},
		DenyPrefixes:  []string{"rm -rf"},
	}
	spec := core.ToolSpec{Name: "shell_run"}

	allow := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"git status --short"}`})
	if !allow.Allow || allow.RequiresApproval || allow.Code != "allow_prefix" || allow.MatchedRule != "git status" {
		t.Fatalf("expected allow-prefix decision for shell_run.command: %+v", allow)
	}

	deny := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"rm -rf /tmp/x"}`})
	if deny.Allow || deny.Code != "policy_denied" || deny.MatchedRule != "rm -rf" {
		t.Fatalf("expected deny-prefix decision for shell_run.command: %+v", deny)
	}
}

func TestDefaultToolPolicyPrefixRulesRequireTokenBoundary(t *testing.T) {
	p := DefaultToolPolicy{
		Mode:          ApprovalModeOnRequest,
		AllowPrefixes: []string{"git status"},
		DenyPrefixes:  []string{"rm -rf"},
	}
	spec := core.ToolSpec{Name: "shell_run"}

	allow := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"git   status   --short"}`})
	if !allow.Allow || allow.RequiresApproval || allow.Code != "allow_prefix" {
		t.Fatalf("expected whitespace-normalized allow-prefix decision: %+v", allow)
	}
	notAllow := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"git statusfoo"}`})
	if !notAllow.Allow || !notAllow.RequiresApproval || notAllow.Code != "approval_required" {
		t.Fatalf("expected statusfoo not to match git status prefix: %+v", notAllow)
	}
	newline := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"git\nstatus --short"}`})
	if !newline.Allow || !newline.RequiresApproval || newline.Code != "approval_required" {
		t.Fatalf("expected newline-separated command not to match git status prefix: %+v", newline)
	}
	notDeny := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"rm -rfoo /tmp/x"}`})
	if !notDeny.Allow || !notDeny.RequiresApproval || notDeny.Code != "approval_required" {
		t.Fatalf("expected rm -rfoo not to match rm -rf deny prefix: %+v", notDeny)
	}
}

func TestDefaultToolPolicyAutoAllowsCommonShellChecksInOnRequest(t *testing.T) {
	p := DefaultToolPolicy{Mode: ApprovalModeOnRequest}
	spec := core.ToolSpec{Name: "shell_run"}
	for _, command := range []string{
		"git status --short",
		"git diff --stat",
		"rg whale internal",
		"ls -u",
		"make test",
		"make test-tui",
		"make build",
		"go test ./...",
		"go vet ./...",
		"npm run test -- --runInBand",
		"npm run typecheck",
		"python -m pytest tests",
		"cargo check --workspace",
	} {
		decision := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if !decision.Allow || decision.RequiresApproval || decision.Code != "shell_auto_allow" {
			t.Fatalf("expected shell_auto_allow for %q: %+v", command, decision)
		}
	}
}

func TestDefaultToolPolicyDoesNotAutoAllowUnsafeShellVariants(t *testing.T) {
	p := DefaultToolPolicy{Mode: ApprovalModeOnRequest}
	spec := core.ToolSpec{Name: "shell_run"}
	for _, command := range []string{
		"make test clean",
		"make build clean",
		"npm run lint -- --fix",
		"npx jest --updateSnapshot",
		"npx jest -u",
		"npx vitest run --update",
		"find . -delete",
		"find . -exec rm {} +",
		"find . -fprint out",
		"git diff --output=out.patch",
		"git show --ext-diff HEAD",
		"git log --textconv",
		"rg --pre ./danger pattern",
		"go test ./... > out.txt",
		"go test ./...\nrm -rf /tmp/x",
	} {
		decision := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if !decision.Allow || !decision.RequiresApproval || decision.Code != "approval_required" {
			t.Fatalf("expected approval_required for %q: %+v", command, decision)
		}
	}
}

func TestDefaultToolPolicyNeverSkipsApprovalForMutatingTools(t *testing.T) {
	p := DefaultToolPolicy{Mode: ApprovalModeNever}
	tests := []struct {
		name string
		spec core.ToolSpec
		call core.ToolCall
	}{
		{
			name: "write",
			spec: core.ToolSpec{Name: "write"},
			call: core.ToolCall{Name: "write", Input: `{"file_path":"a.txt","content":"x"}`},
		},
		{
			name: "apply_patch",
			spec: core.ToolSpec{Name: "apply_patch"},
			call: core.ToolCall{Name: "apply_patch", Input: `{"patch":"*** Begin Patch\n*** End Patch\n"}`},
		},
		{
			name: "shell_run",
			spec: core.ToolSpec{Name: "shell_run"},
			call: core.ToolCall{Name: "shell_run", Input: `{"command":"go test ./..."}`},
		},
		{
			name: "mcp",
			spec: core.ToolSpec{Name: "mcp__github__create_issue"},
			call: core.ToolCall{Name: "mcp__github__create_issue", Input: `{}`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			decision := p.Decide(tc.spec, tc.call)
			if !decision.Allow || decision.RequiresApproval || decision.Code != "auto_allow" {
				t.Fatalf("decision: %+v", decision)
			}
		})
	}
}

func TestDefaultToolPolicyNeverStillHonorsDenyPrefixes(t *testing.T) {
	p := DefaultToolPolicy{
		Mode:         ApprovalModeNever,
		DenyPrefixes: []string{"rm -rf"},
	}
	for _, command := range []string{
		"rm -rf /tmp/x",
		"rm -rf /tmp/x\necho done",
		"echo before\nrm -rf /tmp/x",
	} {
		decision := p.Decide(
			core.ToolSpec{Name: "shell_run"},
			core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`},
		)
		if decision.Allow || decision.Code != "policy_denied" || decision.MatchedRule != "rm -rf" {
			t.Fatalf("expected deny prefix for %q, got %+v", command, decision)
		}
	}
}

func TestShellCommandFromInput(t *testing.T) {
	if got := shellCommandFromInput(`{"command":" echo hi "}`); got != "echo hi" {
		t.Fatalf("shellCommandFromInput = %q, want %q", got, "echo hi")
	}
	if got := shellCommandFromInput(`{`); got != "" {
		t.Fatalf("shellCommandFromInput malformed = %q, want empty", got)
	}
}

func TestDefaultToolPolicyRequiresApprovalForMutatingCapability(t *testing.T) {
	decision := DefaultToolPolicy{Mode: ApprovalModeOnRequest}.Decide(
		core.ToolSpec{Name: "remember", Capabilities: []string{"mutates_state"}},
		core.ToolCall{Name: "remember", Input: `{}`},
	)
	if !decision.Allow || !decision.RequiresApproval || decision.Code != "approval_required" {
		t.Fatalf("decision: %+v", decision)
	}
}

func TestDefaultToolPolicyNeverAllowsMutatingCapability(t *testing.T) {
	decision := DefaultToolPolicy{Mode: ApprovalModeNever}.Decide(
		core.ToolSpec{Name: "remember", Capabilities: []string{"mutates_state"}},
		core.ToolCall{Name: "remember", Input: `{}`},
	)
	if !decision.Allow || decision.RequiresApproval || decision.Code != "auto_allow" {
		t.Fatalf("decision: %+v", decision)
	}
}

func TestApprovalMetadataPreservesToolPreviewValues(t *testing.T) {
	got := ApprovalMetadata(
		core.ToolCall{Name: "remember", Input: `{"scope":"global","name":"style"}`},
		[]string{"remember|x"},
		map[string]any{
			"approval_kind":          "memory_write",
			"approval_session_scope": "global memory: style",
			"memory_name":            "style",
		},
	)
	if got["approval_kind"] != "memory_write" {
		t.Fatalf("approval_kind overwritten: %+v", got)
	}
	if got["approval_session_scope"] != "global memory: style" {
		t.Fatalf("approval_session_scope overwritten: %+v", got)
	}
	if got["approval_scope"] != "workspace" {
		t.Fatalf("approval_scope default not set: %+v", got)
	}
	if got["memory_name"] != "style" {
		t.Fatalf("preview metadata lost: %+v", got)
	}
}
