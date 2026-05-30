package app

import (
	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/policy"
)

func defaultApprovalFunc(fn policy.ApprovalFunc) policy.ApprovalFunc {
	if fn != nil {
		return fn
	}
	return func(policy.ApprovalRequest) policy.ApprovalDecision { return policy.ApprovalDeny }
}

func defaultUserInputFunc(fn agent.UserInputFunc) agent.UserInputFunc {
	if fn != nil {
		return fn
	}
	return func(agent.UserInputRequest) (core.UserInputResponse, bool) {
		return core.UserInputResponse{}, false
	}
}

func (a *App) SetApprovalFunc(fn policy.ApprovalFunc) {
	if fn == nil {
		return
	}
	a.approvalFn = fn
	a.a = nil
}

func (a *App) SetUserInputFunc(fn agent.UserInputFunc) {
	if fn == nil {
		return
	}
	a.userInput = fn
	a.a = nil
}
