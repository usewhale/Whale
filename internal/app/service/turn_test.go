package service

import (
	"context"
	"testing"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/app"
)

func TestTUIRunOptionsDefaultToCurrentViewMode(t *testing.T) {
	a := newServiceTestApp(t, app.ViewModeFocus)
	s := &Service{app: a}

	got := s.tuiRunOptions(agent.RunOptions{})
	if got.ViewMode != app.ViewModeFocus {
		t.Fatalf("view mode: want %q, got %q", app.ViewModeFocus, got.ViewMode)
	}
}

func TestTUIRunOptionsKeepExplicitViewMode(t *testing.T) {
	a := newServiceTestApp(t, app.ViewModeFocus)
	s := &Service{app: a}

	got := s.tuiRunOptions(agent.RunOptions{ViewMode: app.ViewModeDefault})
	if got.ViewMode != app.ViewModeDefault {
		t.Fatalf("view mode: want explicit %q, got %q", app.ViewModeDefault, got.ViewMode)
	}
}

func newServiceTestApp(t *testing.T, viewMode string) *app.App {
	t.Helper()
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.ViewMode = viewMode
	a, err := app.New(context.Background(), cfg, app.StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}
