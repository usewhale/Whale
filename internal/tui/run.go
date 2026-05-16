package tui

import (
	"context"
	"fmt"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/app/service"
)

func Run(cfg app.Config, start app.StartOptions) error {
	ctx := context.Background()
	svc, err := service.New(ctx, cfg, start)
	if err != nil {
		if app.IsCrossWorkspaceResumeError(err) {
			fmt.Println(err.Error())
			return nil
		}
		return err
	}
	defer svc.Close()
	modelName := svc.Model()
	effort := svc.ReasoningEffort()
	thinking := "on"
	if !svc.ThinkingEnabled() {
		thinking = "off"
	}
	m := newModel(svc, modelName, effort, thinking)
	if runtime.GOOS == "windows" {
		m.windowsPaste.enabled = true
	}
	p := tea.NewProgram(m)
	_, err = p.Run()
	if err == nil {
		fmt.Printf("To resume this session, run: whale resume %s\n", svc.SessionID())
	}
	return err
}
