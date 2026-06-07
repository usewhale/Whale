//go:build unix

package execboundary

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/policy"
)

type Server struct {
	socketPath string
	listener   net.Listener
	policy     policy.RulePolicy
	approval   policy.ApprovalFunc
	sessionID  string
}

func StartServer(ctx context.Context, p policy.RulePolicy, approval policy.ApprovalFunc, sessionID string) (*Server, error) {
	dir, err := os.MkdirTemp("", "whale-exec-boundary-")
	if err != nil {
		return nil, err
	}
	socketPath := filepath.Join(dir, "boundary.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	s := &Server{
		socketPath: socketPath,
		listener:   ln,
		policy:     normalizePolicy(p),
		approval:   approval,
		sessionID:  strings.TrimSpace(sessionID),
	}
	go s.serve(ctx)
	return s, nil
}

func normalizePolicy(p policy.RulePolicy) policy.RulePolicy {
	p.Rules = append([]policy.PermissionRule(nil), p.Rules...)
	if p.Default == "" {
		p.Default = policy.PermissionAllow
	}
	return p
}

func (s *Server) SocketPath() string {
	if s == nil {
		return ""
	}
	return s.socketPath
}

func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	err := s.listener.Close()
	if s.socketPath != "" {
		_ = os.RemoveAll(filepath.Dir(s.socketPath))
	}
	return err
}

func (s *Server) serve(ctx context.Context) {
	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	var req DecisionRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(DecisionResponse{Allow: false, Reason: "invalid exec boundary request", Code: "invalid_exec_boundary"})
		return
	}
	decision := s.policy.DecideExecBoundary(policy.ExecBoundaryRequest{
		Program: req.Program,
		Argv:    req.Argv,
		CWD:     req.CWD,
	})
	if decision.Allow && decision.RequiresApproval {
		approval := s.requestApproval(req, decision)
		if approval.Approved() {
			decision.RequiresApproval = false
			decision.Reason = ""
			decision.Code = "permission_allow"
			decision.Phase = "allowed"
		} else {
			decision.Allow = false
			decision.RequiresApproval = false
			decision.Reason = "User denied execution"
			decision.Code = "permission_denied"
			decision.Phase = "denied"
			if approval.Canceled() {
				decision.Reason = "User cancelled execution"
			}
		}
	}
	_ = json.NewEncoder(conn).Encode(DecisionResponse{
		Allow:            decision.Allow,
		RequiresApproval: decision.RequiresApproval,
		Reason:           decision.Reason,
		Code:             decision.Code,
		Phase:            decision.Phase,
		MatchedRule:      decision.MatchedRule,
		Permission:       decision.Permission,
		Pattern:          decision.Pattern,
	})
}

func (s *Server) requestApproval(req DecisionRequest, decision policy.PolicyDecision) policy.ApprovalDecision {
	if s.approval == nil {
		return policy.ApprovalDeny
	}
	call := execBoundaryApprovalCall(req)
	keys := policy.ApprovalKeysForDecision(call, decision)
	key := policy.ApprovalKey(call)
	if len(keys) > 0 {
		key = keys[0]
	}
	return s.approval(policy.ApprovalRequest{
		SessionID: s.sessionID,
		ToolCall:  call,
		Spec: core.ToolSpec{
			Name:         "shell_run",
			Capabilities: []string{"shell.run"},
		},
		Reason:   decision.Reason,
		Code:     decision.Code,
		Key:      key,
		Keys:     keys,
		Metadata: execBoundaryApprovalMetadata(req, decision),
	})
}

func execBoundaryApprovalCall(req DecisionRequest) core.ToolCall {
	input, _ := json.Marshal(map[string]any{
		"command": shellCommandForApproval(req.Program, req.Argv),
		"cwd":     req.CWD,
	})
	return core.ToolCall{
		ID:    "exec-boundary-" + shortHashString(req.Program+"\x00"+strings.Join(req.Argv, "\x00")+"\x00"+req.CWD),
		Name:  "shell_run",
		Input: string(input),
	}
}

func execBoundaryApprovalMetadata(req DecisionRequest, decision policy.PolicyDecision) map[string]any {
	return map[string]any{
		"exec_boundary": true,
		"program":       req.Program,
		"argv":          append([]string(nil), req.Argv...),
		"cwd":           req.CWD,
		"matched_rule":  decision.MatchedRule,
		"permission":    decision.Permission,
		"pattern":       decision.Pattern,
	}
}

func shellCommandForApproval(program string, argv []string) string {
	parts := []string{program}
	if len(argv) > 1 {
		parts = append(parts, argv[1:]...)
	}
	for i := range parts {
		parts[i] = shellQuoteArg(parts[i])
	}
	return strings.Join(parts, " ")
}

func shellQuoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	if strings.IndexFunc(arg, func(r rune) bool {
		return r <= ' ' || strings.ContainsRune(`'"\\$&;()<>|*?![]{}~`, r)
	}) < 0 {
		return arg
	}
	return strconv.Quote(arg)
}

func shortHashString(v string) string {
	sum := sha256.Sum256([]byte(v))
	return fmt.Sprintf("%x", sum[:8])
}

func RequestDecision(socketPath string, req DecisionRequest) (DecisionResponse, error) {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return DecisionResponse{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return DecisionResponse{}, err
	}
	var res DecisionResponse
	if err := json.NewDecoder(conn).Decode(&res); err != nil {
		return DecisionResponse{}, err
	}
	return res, nil
}

func formatDecisionError(res DecisionResponse) string {
	if res.Reason != "" {
		return res.Reason
	}
	if res.RequiresApproval {
		return "shell command requires approval at exec boundary"
	}
	if !res.Allow {
		return "shell command denied at exec boundary"
	}
	return fmt.Sprintf("unexpected exec boundary decision: %+v", res)
}
