package tui

import (
	"fmt"
	"runtime"
)

type RunOptions struct {
	ResumeMenu bool
}

func RunTUI(rt Runtime, opts RunOptions) error {
	defer rt.Close()
	modelName := rt.Model()
	effort := rt.ReasoningEffort()
	thinking := "on"
	if !rt.ThinkingEnabled() {
		thinking = "off"
	}
	m := newModel(rt, modelName, effort, thinking)
	m.resumeMenu = opts.ResumeMenu
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
		fmt.Printf("To resume this session, run: whale resume %s\n", rt.SessionID())
	}
	return err
}
