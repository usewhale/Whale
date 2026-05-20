package memory

import (
	"strings"

	"github.com/usewhale/whale/internal/session"
)

type SessionRuntime struct {
	sessionsDir string
}

func NewSessionRuntime(sessionsDir string) *SessionRuntime {
	return &SessionRuntime{sessionsDir: strings.TrimSpace(sessionsDir)}
}

func (r *SessionRuntime) Enabled() bool {
	return r != nil && r.sessionsDir != ""
}

func (r *SessionRuntime) RefreshScratch(sessionID string, scratch *VolatileScratch) {
	if scratch == nil {
		return
	}
	if !r.Enabled() || strings.TrimSpace(sessionID) == "" {
		return
	}
	if ust, err := r.LoadUserInput(sessionID); err != nil {
		scratch.UserInput = UserInputGateState{Err: err.Error()}
	} else {
		scratch.UserInput = UserInputGateState{State: ust}
	}
}

func (r *SessionRuntime) LoadUserInput(sessionID string) (session.UserInputState, error) {
	if !r.Enabled() {
		return session.UserInputState{}, nil
	}
	return session.LoadUserInputState(r.sessionsDir, sessionID)
}

func (r *SessionRuntime) SaveUserInput(sessionID string, st session.UserInputState) error {
	if !r.Enabled() {
		return nil
	}
	return session.SaveUserInputState(r.sessionsDir, sessionID, st)
}

func (r *SessionRuntime) ClearUserInput(sessionID string) error {
	if !r.Enabled() {
		return nil
	}
	return session.ClearUserInputState(r.sessionsDir, sessionID)
}

func (r *SessionRuntime) LoadTodo(sessionID string) (session.TodoState, error) {
	if !r.Enabled() {
		return session.TodoState{}, nil
	}
	return session.LoadTodoState(r.sessionsDir, sessionID)
}

func (r *SessionRuntime) SaveTodo(sessionID string, st session.TodoState) error {
	if !r.Enabled() {
		return nil
	}
	return session.SaveTodoState(r.sessionsDir, sessionID, st)
}

func (r *SessionRuntime) LoadMeta(sessionID string) (session.SessionMeta, error) {
	if !r.Enabled() {
		return session.SessionMeta{}, nil
	}
	return session.LoadSessionMeta(r.sessionsDir, sessionID)
}

func (r *SessionRuntime) SaveMeta(sessionID string, st session.SessionMeta) error {
	if !r.Enabled() {
		return nil
	}
	return session.SaveSessionMeta(r.sessionsDir, sessionID, st)
}

func (r *SessionRuntime) UpdateMeta(sessionID string, mutate func(*session.SessionMeta)) (session.SessionMeta, error) {
	if !r.Enabled() {
		return session.SessionMeta{}, nil
	}
	return session.UpdateSessionMeta(r.sessionsDir, sessionID, mutate)
}
