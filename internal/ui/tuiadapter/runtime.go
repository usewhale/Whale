package tuiadapter

import (
	"context"
	"os/exec"

	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/app/service"
	"github.com/usewhale/whale/internal/runtime/protocol"
)

type Runtime struct {
	svc *service.Service
}

func NewRuntime(ctx context.Context, cfg app.Config, start app.StartOptions) (*Runtime, error) {
	svc, err := service.New(ctx, cfg, start)
	if err != nil {
		return nil, err
	}
	return &Runtime{svc: svc}, nil
}

func (r *Runtime) Events() <-chan protocol.Event { return r.svc.Events() }

func (r *Runtime) Dispatch(in protocol.Intent) { r.svc.DispatchProtocol(in) }

func (r *Runtime) Close() { r.svc.Close() }

func (r *Runtime) SessionID() string { return r.svc.SessionID() }

func (r *Runtime) Model() string { return r.svc.Model() }

func (r *Runtime) ReasoningEffort() string { return r.svc.ReasoningEffort() }

func (r *Runtime) ThinkingEnabled() bool { return r.svc.ThinkingEnabled() }

func (r *Runtime) ViewMode() string { return r.svc.ViewMode() }

func (r *Runtime) ShowReasoning() bool { return r.svc.ShowReasoning() }

func (r *Runtime) SetViewMode(mode string) error { return r.svc.SetViewMode(mode) }

func (r *Runtime) PrepareOpenCommand(line string) (string, *exec.Cmd, error) {
	return r.svc.PrepareOpenCommand(line)
}

func (r *Runtime) SkillSuggestions() []protocol.SkillView {
	views := r.svc.SkillSuggestions()
	out := make([]protocol.SkillView, 0, len(views))
	for _, view := range views {
		out = append(out, protocol.SkillView{
			Name:          view.Name,
			Description:   view.Description,
			When:          view.When,
			Path:          view.Path,
			SkillFilePath: view.SkillFilePath,
			Source:        view.Source,
			Status:        string(view.Status),
			Reason:        view.Reason,
		})
	}
	return out
}
