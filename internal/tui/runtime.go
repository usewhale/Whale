package tui

import (
	"os/exec"

	"github.com/usewhale/whale/internal/runtime/protocol"
)

type Runtime interface {
	Events() <-chan protocol.Event
	Dispatch(protocol.Intent)
	Close()
	SessionID() string
	Model() string
	ReasoningEffort() string
	ThinkingEnabled() bool
	ViewMode() string
	ShowReasoning() bool
	SetViewMode(string) error
	SkillSuggestions() []protocol.SkillView
	PrepareOpenCommand(string) (string, *exec.Cmd, error)
}
