package app

import (
	"context"
	"strings"

	"github.com/usewhale/whale/internal/agent"
)

func (a *App) RunSideQuestion(ctx context.Context, question string) (<-chan agent.SideQuestionEvent, error) {
	if strings.TrimSpace(question) == "" {
		return nil, agentSideQuestionUsageError()
	}
	ag, err := a.ensureAgent()
	if err != nil {
		return nil, err
	}
	return ag.RunSideQuestion(ctx, a.sessionID, question)
}

func agentSideQuestionUsageError() error {
	return sideQuestionUsageError{}
}

type sideQuestionUsageError struct{}

func (sideQuestionUsageError) Error() string { return "Usage: /btw <your question>" }
