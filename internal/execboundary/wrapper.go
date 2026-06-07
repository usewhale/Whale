package execboundary

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/usewhale/whale/internal/execenv"
	"github.com/usewhale/whale/internal/policy"
)

func RunWrapper(args []string, stdout, stderr io.Writer) int {
	if stderr == nil {
		stderr = io.Discard
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		_, _ = fmt.Fprintln(stderr, "whale exec boundary: missing program")
		return 1
	}
	program := args[0]
	argv := append([]string(nil), args[1:]...)
	if len(argv) == 0 {
		argv = []string{filepath.Base(program)}
	}
	req := DecisionRequest{
		Program: program,
		Argv:    argv,
		CWD:     currentWorkingDirectory(),
	}
	decision := decisionForRequest(req)
	if !decision.Allow || decision.RequiresApproval {
		reason := formatDecisionError(decision)
		_, _ = fmt.Fprintf(stderr, "Whale policy denied shell command: %s", strings.Join(argv, " "))
		if decision.MatchedRule != "" {
			_, _ = fmt.Fprintf(stderr, " (%s)", decision.MatchedRule)
		}
		_, _ = fmt.Fprintf(stderr, ": %s\n", reason)
		return 1
	}
	return execProgram(program, argv, stdout, stderr)
}

func decisionForRequest(req DecisionRequest) DecisionResponse {
	if socketPath := strings.TrimSpace(os.Getenv(execenv.SocketEnv)); socketPath != "" {
		if res, err := RequestDecision(socketPath, req); err == nil {
			return res
		}
		return DecisionResponse{Allow: false, Reason: "exec boundary decision server unavailable", Code: "exec_boundary_server_unavailable"}
	}
	decision := policyFromEnv().DecideExecBoundary(policy.ExecBoundaryRequest{
		Program: req.Program,
		Argv:    req.Argv,
		CWD:     req.CWD,
	})
	return DecisionResponse{
		Allow:            decision.Allow,
		RequiresApproval: decision.RequiresApproval,
		Reason:           decision.Reason,
		Code:             decision.Code,
		Phase:            decision.Phase,
		MatchedRule:      decision.MatchedRule,
		Permission:       decision.Permission,
		Pattern:          decision.Pattern,
	}
}

func policyFromEnv() policy.RulePolicy {
	raw := strings.TrimSpace(os.Getenv(execenv.RulesEnv))
	if raw == "" {
		return policy.RulePolicy{Default: policy.PermissionAllow, Rules: policy.DefaultRules()}
	}
	var rules []policy.PermissionRule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return policy.RulePolicy{Default: policy.PermissionDeny}
	}
	return policy.RulePolicy{Default: policy.PermissionAllow, Rules: rules}
}

func currentWorkingDirectory() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}
