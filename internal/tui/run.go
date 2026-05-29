package tui

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/app/service"
)

func RunTUI(cfg app.Config, start app.StartOptions) error {
	ctx := context.Background()
	if !cfg.ConfigLoaded {
		workspaceRoot, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get workspace: %w", err)
		}
		resolved, err := app.LoadAndApplyConfig(cfg, workspaceRoot)
		if err != nil {
			return err
		}
		cfg = resolved
	}
	outcome, action, err := runUpdatePromptIfNeeded(ctx, cfg)
	if err != nil {
		return err
	}
	if outcome == updatePromptRun {
		return runUpdateAction(action)
	}
	if outcome == updatePromptInterrupt {
		return nil
	}
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
	m.resumeMenu = start.ResumeMenu
	if runtime.GOOS == "windows" {
		m.windowsPaste.enabled = true
	}
	p, cleanup, err := newTerminalProgram(m)
	if err != nil {
		return err
	}
	defer cleanup()
	_, err = p.Run()
	cleanup()
	if err == nil {
		fmt.Printf("To resume this session, run: whale resume %s\n", svc.SessionID())
	}
	return err
}
