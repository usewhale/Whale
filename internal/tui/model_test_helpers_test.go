package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
	"github.com/usewhale/whale/internal/runtime/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type testRuntime struct {
	events chan protocol.Event
}

func (r *testRuntime) Events() <-chan protocol.Event {
	if r.events == nil {
		r.events = make(chan protocol.Event)
	}
	return r.events
}

func (r *testRuntime) Dispatch(protocol.Intent) {}
func (r *testRuntime) Close()                   {}
func (r *testRuntime) SessionID() string        { return "" }
func (r *testRuntime) Model() string            { return "" }
func (r *testRuntime) ReasoningEffort() string  { return "" }
func (r *testRuntime) ThinkingEnabled() bool    { return true }
func (r *testRuntime) ViewMode() string         { return "" }
func (r *testRuntime) ShowReasoning() bool      { return false }
func (r *testRuntime) SetViewMode(string) error { return nil }
func (r *testRuntime) SkillSuggestions() []protocol.SkillView {
	return nil
}
func (r *testRuntime) PrepareOpenCommand(string) (string, *exec.Cmd, error) {
	return "", nil, os.ErrInvalid
}

type fakeClock struct{ t time.Time }

func newFakeClock() *fakeClock               { return &fakeClock{t: time.Unix(1700000000, 0)} }
func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}
func writeFileSuggestionFixture(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}
func runFileSuggestionSearchForTest(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected file suggestion search command")
	}
	msg := cmd()
	updated, _ := updateTestModel(t, m, msg)
	return updated
}
func newModelWithDispatchSpy() (model, *[]protocol.Intent) {
	m := newModel(nil, "", "", "")
	intents := []protocol.Intent{}
	m.dispatch = func(in protocol.Intent) {
		intents = append(intents, in)
	}
	return m, &intents
}
func updateTestModel(t *testing.T, m model, msg tea.Msg) (model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	updated, ok := next.(model)
	if !ok {
		t.Fatalf("expected model update, got %T", next)
	}
	return updated, cmd
}
func flushWindowsPasteBurstForTest(t *testing.T, m model) model {
	t.Helper()
	if !m.hasWindowsPasteBuffer() {
		t.Fatal("expected active Windows paste burst")
	}
	m.windowsPaste.activeUntil = time.Now().Add(-time.Millisecond)
	next, _ := updateTestModel(t, m, windowsPasteBurstFlushMsg{id: m.windowsPaste.burstID})
	return next
}
func typeRunesForTest(t *testing.T, m model, value string) model {
	t.Helper()
	for _, r := range value {
		m, _ = updateTestModel(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		if m.hasWindowsPasteBuffer() {
			m = flushWindowsPasteBurstForTest(t, m)
		}
	}
	return m
}
func assertStyledPickerContains(t *testing.T, rendered string, wants ...string) {
	t.Helper()
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("expected styled picker, got:\n%s", rendered)
	}
	plain := xansi.Strip(rendered)
	for _, want := range wants {
		if !strings.Contains(plain, want) {
			t.Fatalf("expected plain picker to contain %q, got:\n%s", want, plain)
		}
	}
}
func assertFooterLastLine(t *testing.T, view, want string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	if len(lines) == 0 {
		t.Fatal("empty view")
	}
	if got := lines[len(lines)-1]; !strings.Contains(got, want) {
		t.Fatalf("expected footer %q on last line, got %q in view:\n%s", want, got, view)
	}
}
func assertFooterLastLineNotContains(t *testing.T, view, unwanted string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	if len(lines) == 0 {
		t.Fatal("empty view")
	}
	if got := lines[len(lines)-1]; strings.Contains(got, unwanted) {
		t.Fatalf("expected footer not to contain %q, got %q in view:\n%s", unwanted, got, view)
	}
}
