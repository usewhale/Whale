package policy

import (
	"strconv"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/policy/shellrisk"
)

func TestDefaultToolPolicyAllowsOrdinaryShellCommands(t *testing.T) {
	p := DefaultToolPolicy{}
	spec := core.ToolSpec{Name: "shell_run"}
	for _, command := range []string{
		"git tag --list 'v0.1.*' --sort=-v:refname",
		"git worktree list",
		"git config --get remote.origin.url && git remote -v",
		`ls -la .worktrees 2>/dev/null; echo "---"; ls -la worktrees 2>/dev/null; echo "---"; git worktree list 2>/dev/null; echo "---"`,
		"make test",
	} {
		decision := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if !decision.Allow || decision.RequiresApproval {
			t.Fatalf("expected no approval for %q: %+v", command, decision)
		}
	}
}

func TestScopedAllowPolicyAllowsOnlyConfiguredShellPrefixes(t *testing.T) {
	p := ScopedAllowPolicy{
		Base:               DefaultToolPolicy{},
		ShellAllowPrefixes: []string{"gh pr list", "gh pr view", "gh pr diff"},
	}
	spec := core.ToolSpec{Name: "shell_run"}

	allow := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"gh pr diff 123"}`})
	if !allow.Allow || allow.RequiresApproval || allow.Code != "scoped_allow_prefix" || allow.MatchedRule != "gh pr diff" {
		t.Fatalf("expected scoped allow for gh pr diff: %+v", allow)
	}
	view := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"gh pr view 123 --json title,body"}`})
	if !view.Allow || view.RequiresApproval || view.Code != "scoped_allow_prefix" || view.MatchedRule != "gh pr view" {
		t.Fatalf("expected scoped allow for gh pr view json: %+v", view)
	}
	list := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"gh pr list --limit 30 --json number,title"}`})
	if !list.Allow || list.RequiresApproval || list.Code != "scoped_allow_prefix" || list.MatchedRule != "gh pr list" {
		t.Fatalf("expected scoped allow for gh pr list json: %+v", list)
	}

	curl := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"curl https://example.com"}`})
	if !curl.Allow || !curl.RequiresApproval {
		t.Fatalf("expected curl to keep default approval requirement: %+v", curl)
	}

	wideGh := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":"gh repo clone usewhale/whale"}`})
	if !wideGh.Allow || wideGh.RequiresApproval || wideGh.Code == "scoped_allow_prefix" {
		t.Fatalf("expected unrelated gh command to use default shell allow: %+v", wideGh)
	}

	for _, command := range []string{
		"gh pr view 123 --web",
		"gh pr view 123 -w",
		"gh pr list --web",
		"gh pr diff 123 --web",
		"gh pr diff 123 | head -c 1000",
		"gh pr diff 123 2>/dev/null",
		"gh pr diff 123 $(echo x)",
		"gh pr diff 123 && echo done",
		"gh pr diff 123 --output file",
		"gh pr view 123 --jq .body",
		"gh pr list --repo other/repo",
	} {
		decision := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if decision.Code == "scoped_allow_prefix" {
			t.Fatalf("expected scoped allow to reject shell compound %q: %+v", command, decision)
		}
	}

	for _, command := range []string{
		"gh pr view 123",
		"gh pr view 123 --comments",
		"gh pr view 123 --json title,body",
		"gh pr view 123 --comments --json title,body",
		"gh pr list",
		"gh pr list --limit 30",
		"gh pr list --state open --limit 30 --json number,title",
		"gh pr diff 123 --patch",
		"gh pr diff 123 --name-only",
		"gh pr diff 123 --color=never",
	} {
		decision := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if !decision.Allow || decision.RequiresApproval || decision.Code != "scoped_allow_prefix" {
			t.Fatalf("expected scoped allow without approval for %q: %+v", command, decision)
		}
	}
}

func TestScopedAllowPolicyDoesNotOverrideBaseDeny(t *testing.T) {
	p := ScopedAllowPolicy{
		Base: RulePolicy{
			Default: PermissionAllow,
			Rules: []PermissionRule{
				{Permission: "shell", Pattern: "*", Action: PermissionAllow},
				{Permission: "shell", Pattern: "gh pr diff*", Action: PermissionDeny},
			},
		},
		ShellAllowPrefixes: []string{"gh pr diff"},
	}
	decision := p.Decide(
		core.ToolSpec{Name: "shell_run"},
		core.ToolCall{Name: "shell_run", Input: `{"command":"gh pr diff 123"}`},
	)
	if decision.Allow || decision.Code != "permission_denied" {
		t.Fatalf("expected base deny to win over scoped allow: %+v", decision)
	}
}

func TestReadOnlyTurnPolicyDeniesMutatingToolsWithoutApproval(t *testing.T) {
	p := ReadOnlyTurnPolicy{Base: DefaultToolPolicy{}}
	decision := p.Decide(
		core.ToolSpec{Name: "edit"},
		core.ToolCall{Name: "edit", Input: `{"file_path":"a.txt","search":"a","replace":"b"}`},
	)
	if decision.Allow || decision.RequiresApproval || decision.Code != "read_only_turn_denied" {
		t.Fatalf("expected read-only turn denial for edit: %+v", decision)
	}
}

func TestReadOnlyTurnPolicyAllowsOnlyClassifiedReadOnlyShell(t *testing.T) {
	p := ReadOnlyTurnPolicy{Base: ScopedAllowPolicy{
		Base:               DefaultToolPolicy{},
		ShellAllowPrefixes: []string{"gh pr view", "gh pr diff"},
	}}
	spec := productionShellRunSpec(t)

	for _, command := range []string{
		"git status --short",
		"git log --oneline | grep feature | sort -u",
	} {
		decision := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if !decision.Allow || decision.RequiresApproval {
			t.Fatalf("expected read-only turn to allow %q: %+v", command, decision)
		}
	}

	for _, command := range []string{
		"gh pr view 123 --json title,body",
		"make test",
	} {
		decision := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if decision.Allow || decision.RequiresApproval || decision.Code != "read_only_turn_denied" {
			t.Fatalf("expected read-only turn to deny %q without approval: %+v", command, decision)
		}
	}
}

func TestDefaultToolPolicyAutoAllowsCommonShellChecksInOnRequest(t *testing.T) {
	p := DefaultToolPolicy{}
	spec := productionShellRunSpec(t)
	for _, command := range []string{
		"git status --short",
		"git status --short 2>&1",
		"git -C internal status --short",
		"git diff",
		"git diff --cached",
		"git diff --cached 2>&1",
		"git diff -- internal/policy/policy_test.go | tail -80",
		"git diff -- internal/tools/catalog_shell.go | head -40",
		"git show feature/worktree-command:internal/worktree/worktree.go | sed -n '300,459p'",
		"git branch --list 'feature/worktree-command' && git rev-parse --abbrev-ref HEAD",
		"rg whale internal | wc -l",
		"git diff --stat",
		"git diff main...HEAD",
		"git diff --no-index /dev/null internal/commands/review.go",
		"git show --stat --patch HEAD",
		"git log --oneline -5",
		"git branch --show-current",
		"git branch -a",
		"git remote -v",
		"git remote get-url origin",
		"git rev-parse --abbrev-ref HEAD",
		"git symbolic-ref --short refs/remotes/origin/HEAD",
		"git symbolic-ref -q --short refs/remotes/origin/HEAD",
		"git config --get remote.origin.url",
		"rg whale internal",
		"ls -u",
		"uptime",
		"cal",
		"id -u",
		"uname -a",
		"whoami",
		"df -h",
		"du -sh .",
		"locale",
		"groups",
		"nproc",
		"stat internal/policy/policy.go",
		"strings bin/whale",
		"hexdump -C internal/policy/policy.go",
		"od -c internal/policy/policy.go",
		"nl -ba internal/policy/policy.go",
		"basename internal/policy/policy.go",
		"dirname internal/policy/policy.go",
		"realpath internal/policy/policy.go",
		"readlink bin/whale",
		"cut -d : -f 1 internal/policy/policy.go",
		"paste internal/policy/policy.go internal/policy/policy_test.go",
		"tr a-z A-Z",
		"column -t internal/policy/policy.go",
		"tac internal/policy/policy.go",
		"rev internal/policy/policy.go",
		"fold -w 80 internal/policy/policy.go",
		"expand internal/policy/policy.go",
		"unexpand internal/policy/policy.go",
		"comm internal/policy/policy.go internal/policy/policy_test.go",
		"cmp internal/policy/policy.go internal/policy/policy_test.go",
		"numfmt --to=iec 1024",
		"true",
		"false",
		"which whale",
		"type whale",
		"expr 1 + 1",
		"test -f internal/policy/policy.go",
		"getconf ARG_MAX",
		"seq 1 3",
		"tsort internal/policy/policy.go",
		"pr internal/policy/policy.go",
		"date",
		"date -u",
		"date +%Y-%m-%d",
		"which go",
		"command -v go",
		"python -m pytest tests",
	} {
		decision := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if !decision.Allow || decision.RequiresApproval {
			t.Fatalf("expected no approval for %q: %+v", command, decision)
		}
	}
}

func TestDefaultToolPolicyAppliesExplicitShellAskAndDenyRules(t *testing.T) {
	p := DefaultToolPolicy{}
	spec := productionShellRunSpec(t)
	for _, command := range []string{"rm file", "git push origin main", "curl https://example.com"} {
		decision := p.Decide(spec, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
		if !decision.Allow || !decision.RequiresApproval || decision.Code != "permission_required" {
			t.Fatalf("expected approval for %q: %+v", command, decision)
		}
	}
	for _, command := range []string{
		"rm -rf /tmp/x",
		"rm -fr /tmp/x",
		"rm -r -f /tmp/x",
		"rm -R dir",
		"git status\nrm -rf /tmp/x",
		"echo before\nrm -rf /tmp/x",
	} {
		decision := p.Decide(
			core.ToolSpec{Name: "shell_run"},
			core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`},
		)
		if decision.Allow || decision.Code != "permission_denied" {
			t.Fatalf("expected deny for %q, got %+v", command, decision)
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
	decision := DefaultToolPolicy{}.Decide(
		core.ToolSpec{Name: "remember", Capabilities: []string{"mutates_state"}},
		core.ToolCall{Name: "remember", Input: `{}`},
	)
	if !decision.Allow || !decision.RequiresApproval || decision.Code != "permission_required" {
		t.Fatalf("decision: %+v", decision)
	}
}

func TestDefaultToolPolicyAllowsWebTools(t *testing.T) {
	p := DefaultToolPolicy{}
	cases := []struct {
		spec core.ToolSpec
		call core.ToolCall
	}{
		{
			spec: core.ToolSpec{Name: "web_search", ReadOnly: true},
			call: core.ToolCall{Name: "web_search", Input: `{"query":"Node.js permission model"}`},
		},
		{
			spec: core.ToolSpec{Name: "fetch", ReadOnly: true},
			call: core.ToolCall{Name: "fetch", Input: `{"url":"https://nodejs.org/api/permissions.html","prompt":"extract"}`},
		},
		{
			spec: core.ToolSpec{Name: "web_fetch", ReadOnly: true},
			call: core.ToolCall{Name: "web_fetch", Input: `{"url":"https://nodejs.org/api/permissions.html","prompt":"extract"}`},
		},
	}
	for _, tc := range cases {
		decision := p.Decide(tc.spec, tc.call)
		if !decision.Allow || decision.RequiresApproval || decision.Code != "permission_allow" {
			t.Fatalf("%s decision: %+v", tc.call.Name, decision)
		}
	}
}

func TestDefaultToolPolicyRequiresApprovalForCancelSubagent(t *testing.T) {
	decision := DefaultToolPolicy{}.Decide(
		core.ToolSpec{Name: "cancel_subagent", ReadOnly: false},
		core.ToolCall{Name: "cancel_subagent", Input: `{"session_id":"child-1"}`},
	)
	if !decision.Allow || !decision.RequiresApproval || decision.Permission != "mutating_tool" || decision.Code != "permission_required" {
		t.Fatalf("decision: %+v", decision)
	}
}

func TestRulePolicyAllowDefaultAllowsMutatingCapability(t *testing.T) {
	decision := RulePolicy{Default: PermissionAllow}.Decide(
		core.ToolSpec{Name: "remember", Capabilities: []string{"mutates_state"}},
		core.ToolCall{Name: "remember", Input: `{}`},
	)
	if !decision.Allow || decision.RequiresApproval || decision.Code != "permission_allow" {
		t.Fatalf("decision: %+v", decision)
	}
}

func TestRulePolicyMatchesWebFetchRulesByHost(t *testing.T) {
	decision := RulePolicy{
		Default: PermissionAsk,
		Rules: []PermissionRule{
			{Permission: "web_fetch", Pattern: "host:nodejs.org", Action: PermissionAllow},
		},
	}.Decide(
		core.ToolSpec{Name: "web_fetch", ReadOnly: true},
		core.ToolCall{Name: "web_fetch", Input: `{"url":"https://www.nodejs.org/api/permissions.html","prompt":"extract"}`},
	)
	if !decision.Allow || decision.RequiresApproval || decision.Code != "permission_allow" {
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

func productionShellRunSpec(t *testing.T) core.ToolSpec {
	t.Helper()
	return core.ToolSpec{
		Name:         "shell_run",
		Capabilities: []string{"shell.read", "shell.run"},
		ReadOnlyCheck: func(args map[string]any) bool {
			cmd, _ := args["command"].(string)
			decision := shellrisk.Classify(strings.TrimSpace(cmd))
			return decision.Allow && decision.Level == shellrisk.LevelSafeRead
		},
	}
}
