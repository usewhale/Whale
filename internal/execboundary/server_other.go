//go:build !unix

package execboundary

import (
	"context"
	"errors"

	"github.com/usewhale/whale/internal/policy"
)

type Server struct{}

func StartServer(_ context.Context, _ policy.RulePolicy, _ policy.ApprovalFunc, _ string) (*Server, error) {
	return nil, errors.New("exec boundary server is not supported on this platform")
}

func (s *Server) SocketPath() string { return "" }
func (s *Server) Close() error       { return nil }

func RequestDecision(_ string, _ DecisionRequest) (DecisionResponse, error) {
	return DecisionResponse{}, errors.New("exec boundary server is not supported on this platform")
}

func formatDecisionError(res DecisionResponse) string {
	if res.Reason != "" {
		return res.Reason
	}
	return "shell command denied at exec boundary"
}
