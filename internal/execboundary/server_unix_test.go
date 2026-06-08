//go:build unix

package execboundary

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/policy"
)

func TestServerDecidesExecBoundaryRequests(t *testing.T) {
	server, err := StartServer(context.Background(), policy.RulePolicy{
		Default: policy.PermissionAllow,
		Rules:   policy.DefaultRules(),
	}, nil, "")
	if err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	defer server.Close()

	denied, err := RequestDecision(server.SocketPath(), DecisionRequest{
		Program: "/bin/rm",
		Argv:    []string{"rm", "-rf", "/tmp/target"},
		CWD:     "/repo",
	})
	if err != nil {
		t.Fatalf("RequestDecision denied: %v", err)
	}
	if denied.Allow || denied.MatchedRule != "shell:rm -rf*=deny" {
		t.Fatalf("rm -rf should be denied by parent server, got %+v", denied)
	}

	ask, err := RequestDecision(server.SocketPath(), DecisionRequest{
		Program: "/usr/bin/npm",
		Argv:    []string{"npm", "install", "left-pad"},
		CWD:     "/repo",
	})
	if err != nil {
		t.Fatalf("RequestDecision ask: %v", err)
	}
	if ask.Allow || ask.RequiresApproval || ask.MatchedRule != "shell:npm install*=ask" {
		t.Fatalf("npm install should fail closed without approval callback, got %+v", ask)
	}

	allowed, err := RequestDecision(server.SocketPath(), DecisionRequest{
		Program: "/usr/bin/git",
		Argv:    []string{"git", "status"},
		CWD:     "/repo",
	})
	if err != nil {
		t.Fatalf("RequestDecision allow: %v", err)
	}
	if !allowed.Allow || allowed.RequiresApproval {
		t.Fatalf("git status should be allowed by parent server, got %+v", allowed)
	}
}

func TestServerApprovalAllowsExecBoundaryPrompt(t *testing.T) {
	var seen policy.ApprovalRequest
	server, err := StartServer(context.Background(), policy.RulePolicy{
		Default: policy.PermissionAllow,
		Rules: []policy.PermissionRule{
			{Permission: "shell", Pattern: "npm install*", Action: policy.PermissionAsk},
		},
	}, func(req policy.ApprovalRequest) policy.ApprovalDecision {
		seen = req
		return policy.ApprovalAllow
	}, "session-1")
	if err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	defer server.Close()

	got, err := RequestDecision(server.SocketPath(), DecisionRequest{
		Program: "/usr/bin/npm",
		Argv:    []string{"npm", "install", "left-pad"},
		CWD:     "/repo",
	})
	if err != nil {
		t.Fatalf("RequestDecision: %v", err)
	}
	if !got.Allow || got.RequiresApproval {
		t.Fatalf("approved exec boundary prompt should allow execution, got %+v", got)
	}
	if seen.SessionID != "session-1" || seen.ToolCall.Name != "shell_run" || seen.Spec.Name != "shell_run" {
		t.Fatalf("unexpected approval request: %+v", seen)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(seen.ToolCall.Input), &body); err != nil {
		t.Fatalf("approval input JSON: %v", err)
	}
	if body["command"] != "/usr/bin/npm install left-pad" || body["cwd"] != "/repo" {
		t.Fatalf("approval should show intercepted command and cwd, got %#v", body)
	}
}

func TestServerApprovalDeniesExecBoundaryPrompt(t *testing.T) {
	server, err := StartServer(context.Background(), policy.RulePolicy{
		Default: policy.PermissionAllow,
		Rules: []policy.PermissionRule{
			{Permission: "shell", Pattern: "npm install*", Action: policy.PermissionAsk},
		},
	}, func(policy.ApprovalRequest) policy.ApprovalDecision {
		return policy.ApprovalDeny
	}, "session-1")
	if err != nil {
		t.Fatalf("StartServer: %v", err)
	}
	defer server.Close()

	got, err := RequestDecision(server.SocketPath(), DecisionRequest{
		Program: "/usr/bin/npm",
		Argv:    []string{"npm", "install", "left-pad"},
		CWD:     "/repo",
	})
	if err != nil {
		t.Fatalf("RequestDecision: %v", err)
	}
	if got.Allow || got.RequiresApproval || got.Code != "permission_denied" || !strings.Contains(got.Reason, "denied") {
		t.Fatalf("denied exec boundary prompt should deny execution, got %+v", got)
	}
}

func TestDecisionForRequestFailsClosedWhenServerUnavailable(t *testing.T) {
	t.Setenv("WHALE_EXEC_BOUNDARY_SOCKET", "/tmp/whale-missing-boundary.sock")
	got := decisionForRequest(DecisionRequest{Program: "/usr/bin/git", Argv: []string{"git", "status"}, CWD: "/repo"})
	if got.Allow || !strings.Contains(got.Reason, "server unavailable") {
		t.Fatalf("missing parent server should fail closed, got %+v", got)
	}
}

func TestExecProgramEnvPreservesBoundaryForChildProcesses(t *testing.T) {
	got := execProgramEnv([]string{
		"WHALE_EXEC_BOUNDARY_WRAPPER=1",
		"WHALE_EXEC_BOUNDARY_RULES=[]",
		"WHALE_EXEC_BOUNDARY_SOCKET=/tmp/whale-boundary.sock",
		"WHALE_EXEC_BOUNDARY_SHELL=/opt/whale/runtime/zsh",
		"WHALE_EXEC_BOUNDARY_WRAPPER_PATH=/opt/whale/whale",
		"EXEC_WRAPPER=/opt/whale/whale",
		"PATH=/usr/bin:/bin",
	})
	joined := "\n" + strings.Join(got, "\n") + "\n"
	if strings.Contains(joined, "\nWHALE_EXEC_BOUNDARY_WRAPPER=1\n") {
		t.Fatalf("wrapper mode marker should not be inherited: %q", got)
	}
	for _, want := range []string{
		"WHALE_EXEC_BOUNDARY_RULES=[]",
		"WHALE_EXEC_BOUNDARY_SOCKET=/tmp/whale-boundary.sock",
		"WHALE_EXEC_BOUNDARY_SHELL=/opt/whale/runtime/zsh",
		"WHALE_EXEC_BOUNDARY_WRAPPER_PATH=/opt/whale/whale",
		"EXEC_WRAPPER=/opt/whale/whale",
		"PATH=/usr/bin:/bin",
	} {
		if !strings.Contains(joined, "\n"+want+"\n") {
			t.Fatalf("expected %s to be preserved, got %q", want, got)
		}
	}
}
