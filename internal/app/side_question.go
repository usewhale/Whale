package app

import (
	"context"
	"strings"

	"github.com/usewhale/whale/internal/agent"
)

func (a *App) RunSideQuestion(ctx context.Context, question string) (<-chan agent.SideQuestionEvent, error) {
	return a.RunSideQuestionWithOptions(ctx, question, agent.RunOptions{})
}

func (a *App) RunSideQuestionWithOptions(ctx context.Context, question string, opts agent.RunOptions) (<-chan agent.SideQuestionEvent, error) {
	if strings.TrimSpace(question) == "" {
		return nil, agentSideQuestionUsageError()
	}
	ag, err := a.ensureAgent()
	if err != nil {
		return nil, err
	}
	return ag.RunSideQuestionWithOptions(ctx, a.sessionID, question, a.applyRunOptionsDefaults(opts))
}

func agentSideQuestionUsageError() error {
	return sideQuestionUsageError{}
}

type sideQuestionUsageError struct{}

func (sideQuestionUsageError) Error() string { return "Usage: /btw <your question>" }
