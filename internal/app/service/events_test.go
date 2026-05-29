package service

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/core"
	llmretry "github.com/usewhale/whale/internal/llm/retry"
	"github.com/usewhale/whale/internal/plugins"
	"github.com/usewhale/whale/internal/policy"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/skills"
	"github.com/usewhale/whale/internal/store"
	"github.com/usewhale/whale/internal/telemetry"
)

func TestCriticalEventsDeliverAfterDeltaBackpressure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Service{ctx: ctx, events: make(chan Event, 1)}
	s.events <- Event{Kind: EventInfo, Text: "fill buffer"}

	deltas := newTurnDeltaCoalescers(s)
	for i := 0; i < 200; i++ {
		deltas.add(EventPlanDelta, strings.Repeat("x", 64))
	}

	done := make(chan struct{})
	go func() {
		deltas.flushReliable()
		s.emit(Event{Kind: EventPlanCompleted, Text: "final plan"})
		s.emit(Event{Kind: EventLocalSubmitResult, Status: "info", Text: "local result"})
		s.emit(Event{Kind: EventTurnDone, LastResponse: "done"})
		close(done)
	}()

	seenCompleted := false
	seenLocal := false
	seenDone := false
	deadline := time.After(2 * time.Second)
	for !seenCompleted || !seenLocal || !seenDone {
		select {
		case ev := <-s.Events():
			if ev.Kind == EventPlanCompleted && ev.Text == "final plan" {
				seenCompleted = true
			}
			if ev.Kind == EventLocalSubmitResult && ev.Text == "local result" {
				seenLocal = true
			}
			if ev.Kind == EventTurnDone {
				seenDone = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for critical events, completed=%v local=%v done=%v", seenCompleted, seenLocal, seenDone)
		}
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("critical sender remained blocked after consumer drained events")
	}
}

func TestReviewMenuEventDeliversUnderBackpressure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Service{ctx: ctx, events: make(chan Event, 1)}
	s.events <- Event{Kind: EventInfo, Text: "fill buffer"}

	done := make(chan struct{})
	go func() {
		s.emit(Event{Kind: EventReviewMenu})
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-s.Events():
			if ev.Kind == EventReviewMenu {
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("review menu emit remained blocked after event was consumed")
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for review menu event under backpressure")
		}
	}
}

func TestTurnDeltaCoalescerOverflowTrimsOnRuneBoundary(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Channel cap=1 prefilled forces every best-effort send to fail and
	// re-queue, which exercises the hardCap overflow trim.
	s := &Service{ctx: ctx, events: make(chan Event, 1)}
	s.events <- Event{Kind: EventInfo, Text: "fill buffer"}

	deltas := newTurnDeltaCoalescers(s)
	// "中" is 3 bytes (E4 B8 AD). Push enough multibyte text past hardCap so
	// the trim point is overwhelmingly likely to land mid-rune if unguarded.
	const chunkSize = 1023 // not a multiple of 3 → boundary stress
	totalBytes := deltas.hardCap*2 + chunkSize
	payload := strings.Repeat("中", chunkSize/3) + strings.Repeat("x", chunkSize%3)
	for sent := 0; sent < totalBytes; sent += len(payload) {
		deltas.add(EventAssistantDelta, payload)
	}

	<-s.Events() // drain sentinel so flushReliable can drive output

	done := make(chan struct{})
	go func() {
		deltas.flushReliable()
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	sawNotice := false
collect:
	for {
		select {
		case ev := <-s.Events():
			if ev.Kind == EventAssistantDelta && !utf8.ValidString(ev.Text) {
				t.Fatalf("delta event contains invalid UTF-8 after overflow trim: %q", ev.Text)
			}
			if ev.Kind == EventInfo && strings.Contains(ev.Text, "omitted") {
				sawNotice = true
			}
		case <-done:
			for {
				select {
				case ev := <-s.Events():
					if ev.Kind == EventAssistantDelta && !utf8.ValidString(ev.Text) {
						t.Fatalf("delta event contains invalid UTF-8 after overflow trim: %q", ev.Text)
					}
					if ev.Kind == EventInfo && strings.Contains(ev.Text, "omitted") {
						sawNotice = true
					}
				default:
					break collect
				}
			}
		case <-deadline:
			t.Fatal("timed out draining deltas after overflow")
		}
	}
	if !sawNotice {
		t.Fatal("expected overflow-drop notice; test did not exercise the trim path")
	}
}

func TestTurnDeltaCoalescerDropNoticeIsReliable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// cap=1 channel pre-filled, and we push more than hardCap bytes so the
	// re-queue path overflows into the bounded-drop branch.
	s := &Service{ctx: ctx, events: make(chan Event, 1)}
	s.events <- Event{Kind: EventInfo, Text: "fill buffer"}

	deltas := newTurnDeltaCoalescers(s)
	// hardCap defaults to 256KB; send 512KB so half is forced to drop.
	const chunkSize = 1024
	totalChunks := (deltas.hardCap * 2) / chunkSize
	for i := 0; i < totalChunks; i++ {
		deltas.add(EventAssistantDelta, strings.Repeat("x", chunkSize))
	}
	deltas.mu.Lock()
	droppedBytes := deltas.droppedBytes
	deltas.mu.Unlock()
	if droppedBytes == 0 {
		t.Fatalf("expected dropped bytes once hardCap exceeded, got 0")
	}

	done := make(chan struct{})
	go func() {
		deltas.flushReliable()
		close(done)
	}()

	// Drain events; the reliable drop-notice must arrive even though the
	// channel was full when flushReliable started.
	sawNotice := false
	deadline := time.After(2 * time.Second)
	for !sawNotice {
		select {
		case ev := <-s.Events():
			if ev.Kind == EventInfo && strings.Contains(ev.Text, "omitted") &&
				strings.Contains(ev.Text, "bytes") {
				sawNotice = true
			}
		case <-deadline:
			t.Fatalf("drop notice was not delivered reliably under backpressure")
		}
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("flushReliable did not return after notice was consumed")
	}
}

func TestTurnDeltaCoalescerAddIsRaceSafe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Service{ctx: ctx, events: make(chan Event, 1024)}
	deltas := newTurnDeltaCoalescers(s)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				deltas.add(EventAssistantDelta, strings.Repeat("x", 8))
			}
		}()
	}
	wg.Wait()
	deltas.flushReliable()
}

func TestRunTurnWithStreamResetClearsLastResponse(t *testing.T) {
	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	svc, err := New(t.Context(), cfg, app.StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	waitForServiceEvent(t, svc, EventSessionHydrated)

	ch := make(chan agent.AgentEvent, 4)
	ch <- agent.AgentEvent{Type: agent.AgentEventTypeAssistantDelta, Content: "old partial "}
	ch <- agent.AgentEvent{Type: agent.AgentEventTypeProviderRetryScheduled, ProviderRetry: &llmretry.Info{
		Attempt:     1,
		MaxAttempts: 2,
		Reason:      "API stream disconnected",
		Stage:       "stream",
		StreamReset: true,
	}}
	ch <- agent.AgentEvent{Type: agent.AgentEventTypeAssistantDelta, Content: "new final"}
	close(ch)

	svc.runTurnWith(func(context.Context) (<-chan agent.AgentEvent, error) {
		return ch, nil
	})

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-svc.Events():
			if ev.Kind != EventTurnDone {
				continue
			}
			if ev.LastResponse != "new final" {
				t.Fatalf("last response should exclude pre-reset delta, got %q", ev.LastResponse)
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for turn done")
		}
	}
}

func TestTurnDeltaCoalescerPreservesCrossKindOrder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Service{ctx: ctx, events: make(chan Event, 10)}

	deltas := newTurnDeltaCoalescers(s)
	deltas.add(EventReasoningDelta, "think-a ")
	deltas.add(EventAssistantDelta, "answer ")
	deltas.add(EventReasoningDelta, "think-b")
	deltas.flushReliable()

	want := []Event{
		{Kind: EventReasoningDelta, Text: "think-a "},
		{Kind: EventAssistantDelta, Text: "answer "},
		{Kind: EventReasoningDelta, Text: "think-b"},
	}
	for i, w := range want {
		select {
		case got := <-s.Events():
			if got.Kind != w.Kind || got.Text != w.Text {
				t.Fatalf("event %d mismatch: got kind=%s text=%q, want kind=%s text=%q", i, got.Kind, got.Text, w.Kind, w.Text)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for event %d", i)
		}
	}
}

func TestTurnDeltaCoalescerCoalescesOnlyAdjacentSameKind(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Service{ctx: ctx, events: make(chan Event, 10)}

	deltas := newTurnDeltaCoalescers(s)
	deltas.add(EventReasoningDelta, "a")
	deltas.add(EventReasoningDelta, "b")
	deltas.add(EventAssistantDelta, "c")
	deltas.add(EventAssistantDelta, "d")
	deltas.flushReliable()

	want := []Event{
		{Kind: EventReasoningDelta, Text: "ab"},
		{Kind: EventAssistantDelta, Text: "cd"},
	}
	for i, w := range want {
		select {
		case got := <-s.Events():
			if got.Kind != w.Kind || got.Text != w.Text {
				t.Fatalf("event %d mismatch: got kind=%s text=%q, want kind=%s text=%q", i, got.Kind, got.Text, w.Kind, w.Text)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for event %d", i)
		}
	}
}

func TestEmitReliableUnblocksOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{ctx: ctx, events: make(chan Event)}
	done := make(chan struct{})
	go func() {
		s.emit(Event{Kind: EventTurnDone})
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reliable emit did not unblock after context cancellation")
	}
}

func TestResumeMenuStartupOpensSessionPickerBeforeHydration(t *testing.T) {
	dir := t.TempDir()
	writeSessionFile(t, dir, "sess-1", "hello resume")
	cfg := app.DefaultConfig()
	cfg.DataDir = dir

	svc, err := New(t.Context(), cfg, app.StartOptions{ResumeMenu: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()

	for {
		ev := nextServiceEvent(t, svc)
		switch ev.Kind {
		case EventSessionHydrated:
			t.Fatal("session hydrated before resume picker was shown")
		case EventSessionsListed:
			if joined := strings.Join(ev.Choices, "\n"); !strings.Contains(joined, "hello resume") {
				t.Fatalf("expected session choice to include conversation, got:\n%s", joined)
			}
			svc.Dispatch(Intent{Kind: IntentSelectSession, SessionInput: "1"})
			assertSessionSelectedAndHydrated(t, svc)
			return
		}
	}
}

func TestResumeMenuStartupWithNoSessionsHydratesFallbackSession(t *testing.T) {
	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()

	svc, err := New(t.Context(), cfg, app.StartOptions{ResumeMenu: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()

	sawNoSaved := false
	for {
		ev := nextServiceEvent(t, svc)
		switch ev.Kind {
		case EventSessionsListed:
			t.Fatal("did not expect an empty session picker")
		case EventInfo:
			if ev.Text == "no saved sessions" {
				sawNoSaved = true
			}
		case EventSessionHydrated:
			if !sawNoSaved {
				t.Fatal("expected no saved sessions notice before hydration")
			}
			return
		}
	}
}

func TestResumeMenuCrossWorkspaceSelectionDoesNotHydrate(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	writeSessionFile(t, dir, "sess-1", "hello from elsewhere")
	if err := session.SaveSessionMeta(filepath.Join(dir, "sessions"), "sess-1", session.SessionMeta{Workspace: other}); err != nil {
		t.Fatalf("save session meta: %v", err)
	}
	cfg := app.DefaultConfig()
	cfg.DataDir = dir

	svc, err := New(t.Context(), cfg, app.StartOptions{ResumeMenu: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()

	for {
		ev := nextServiceEvent(t, svc)
		switch ev.Kind {
		case EventSessionHydrated:
			t.Fatal("session hydrated before cross-workspace selection")
		case EventSessionsListed:
			svc.Dispatch(Intent{Kind: IntentSelectSession, SessionInput: "1"})
		case EventInfo:
			if strings.Contains(ev.Text, "This conversation is from a different directory.") {
				for {
					select {
					case queued := <-svc.Events():
						if queued.Kind == EventSessionHydrated {
							t.Fatalf("did not expect hydration after cross-workspace message: %+v", queued)
						}
					default:
						return
					}
				}
			}
		}
	}
}

func TestSkillsCommandOpensMenuAndToggleUpdatesSuggestions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	work := t.TempDir()
	t.Chdir(work)
	writeServiceSkill(t, filepath.Join(work, ".whale", "skills", "test-skill"), "test-skill", "Workspace skill.")

	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	svc, err := New(t.Context(), cfg, app.StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	waitForServiceEvent(t, svc, EventSessionHydrated)

	svc.Dispatch(Intent{Kind: IntentSubmit, Input: "/skills"})
	evMenu := waitForServiceEvent(t, svc, EventSkillsMenu)
	if evMenu.Kind != EventSkillsMenu {
		t.Fatalf("expected skills menu event, got %+v", evMenu)
	}
	svc.Dispatch(Intent{Kind: IntentRequestSkillsManage})
	ev := waitForServiceEvent(t, svc, EventSkillsManager)
	if !hasServiceSkill(ev.Skills, "test-skill", "ready") {
		t.Fatalf("unexpected skills manager event: %+v", ev.Skills)
	}
	if !hasServiceSkill(svc.SkillSuggestions(), "test-skill", "ready") {
		t.Fatalf("expected skill suggestion before disabling, got %+v", svc.SkillSuggestions())
	}

	svc.Dispatch(Intent{Kind: IntentSetSkillEnabled, SkillName: "test-skill", SkillEnabled: false})
	ev = waitForServiceEvent(t, svc, EventSkillsManager)
	if !hasServiceSkill(ev.Skills, "test-skill", "disabled") {
		t.Fatalf("expected disabled skill manager event, got %+v", ev.Skills)
	}
	if got := svc.SkillSuggestions(); hasServiceSkill(got, "test-skill", "") {
		t.Fatalf("expected disabled skill to disappear from suggestions, got %+v", got)
	}

	svc.Dispatch(Intent{Kind: IntentSetSkillEnabled, SkillName: "test-skill", SkillEnabled: true})
	ev = waitForServiceEvent(t, svc, EventSkillsManager)
	if !hasServiceSkill(ev.Skills, "test-skill", "ready") {
		t.Fatalf("expected ready skill manager event, got %+v", ev.Skills)
	}
	if got := svc.SkillSuggestions(); !hasServiceSkill(got, "test-skill", "ready") {
		t.Fatalf("expected enabled skill suggestion, got %+v", got)
	}
}

func hasServiceSkill(all []skills.SkillView, name, status string) bool {
	for _, skill := range all {
		if skill.Name != name {
			continue
		}
		return status == "" || string(skill.Status) == status
	}
	return false
}

func TestPluginsCommandOpensManagerAndToggleUpdatesRuntime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	work := t.TempDir()
	t.Chdir(work)

	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	svc, err := New(t.Context(), cfg, app.StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	waitForServiceEvent(t, svc, EventSessionHydrated)

	svc.Dispatch(Intent{Kind: IntentSubmit, Input: "/plugins"})
	ev := waitForServiceEvent(t, svc, EventPluginsManager)
	if !hasServicePlugin(ev.Plugins, "memory", true) {
		t.Fatalf("expected memory plugin enabled, got %+v", ev.Plugins)
	}

	svc.Dispatch(Intent{Kind: IntentSetPluginEnabled, PluginID: "memory", PluginEnabled: false})
	ev = waitForServiceEvent(t, svc, EventPluginsManager)
	if !hasServicePlugin(ev.Plugins, "memory", false) {
		t.Fatalf("expected memory plugin disabled, got %+v", ev.Plugins)
	}
	cfgFile, loaded, err := app.LoadConfigFile(app.ProjectLocalConfigPath(work))
	if err != nil || !loaded {
		t.Fatalf("load project local config loaded=%v err=%v", loaded, err)
	}
	if len(cfgFile.Plugins.Disabled) != 1 || cfgFile.Plugins.Disabled[0] != "memory" {
		t.Fatalf("expected memory disabled in config, got %+v", cfgFile.Plugins.Disabled)
	}

	svc.Dispatch(Intent{Kind: IntentSetPluginEnabled, PluginID: "memory", PluginEnabled: true})
	ev = waitForServiceEvent(t, svc, EventPluginsManager)
	if !hasServicePlugin(ev.Plugins, "memory", true) {
		t.Fatalf("expected memory plugin enabled again, got %+v", ev.Plugins)
	}
}

func hasServicePlugin(all []plugins.PluginStatus, id string, enabled bool) bool {
	for _, plugin := range all {
		if plugin.Manifest.ID == id {
			return plugin.Enabled == enabled
		}
	}
	return false
}

func TestBtwDeltaEventDeliversUnderBackpressure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Service{ctx: ctx, events: make(chan Event, 1)}
	s.events <- Event{Kind: EventInfo, Text: "fill buffer"}

	done := make(chan struct{})
	go func() {
		s.emit(Event{Kind: EventBtwDelta, Text: "stream", Count: 7})
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-s.Events():
			if ev.Kind == EventBtwDelta {
				if ev.Text != "stream" || ev.Count != 7 {
					t.Fatalf("unexpected btw delta event: %+v", ev)
				}
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("btw delta emit remained blocked after event was consumed")
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for btw delta event")
		}
	}
}

func TestLocalSubmitDoesNotEmitTurnDone(t *testing.T) {
	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	svc, err := New(t.Context(), cfg, app.StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	waitForServiceEvent(t, svc, EventSessionHydrated)

	svc.Dispatch(Intent{Kind: IntentSubmitLocal, Input: "/stats usage"})
	for {
		ev := nextServiceEvent(t, svc)
		if ev.Kind == EventTurnDone {
			t.Fatal("local submit emitted EventTurnDone")
		}
		if ev.Kind == EventLocalSubmitResult && ev.Status == "info" && strings.Contains(ev.Text, "Stats") {
			break
		}
	}
	select {
	case ev := <-svc.Events():
		if ev.Kind == EventTurnDone {
			t.Fatal("local submit emitted delayed EventTurnDone")
		}
	case <-time.After(100 * time.Millisecond):
	}
}

func TestStatusLocalSubmitEmitsStructuredLocalResult(t *testing.T) {
	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	svc, err := New(t.Context(), cfg, app.StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	waitForServiceEvent(t, svc, EventSessionHydrated)

	svc.Dispatch(Intent{Kind: IntentSubmitLocal, Input: "/status"})
	for {
		ev := nextServiceEvent(t, svc)
		if ev.Kind != EventLocalSubmitResult {
			continue
		}
		if ev.LocalResult == nil || ev.LocalResult.Kind != "status" {
			t.Fatalf("expected structured status local result, got %+v", ev)
		}
		if ev.Text == "" || ev.Text != ev.LocalResult.PlainText {
			t.Fatalf("expected text fallback to match local result, text=%q local=%q", ev.Text, ev.LocalResult.PlainText)
		}
		return
	}
}

func TestStatsLocalSubmitEmitsStructuredLocalResult(t *testing.T) {
	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	svc, err := New(t.Context(), cfg, app.StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	waitForServiceEvent(t, svc, EventSessionHydrated)

	svc.Dispatch(Intent{Kind: IntentSubmitLocal, Input: "/stats"})
	for {
		ev := nextServiceEvent(t, svc)
		if ev.Kind != EventLocalSubmitResult {
			continue
		}
		if ev.LocalResult == nil || ev.LocalResult.Kind != "stats" {
			t.Fatalf("expected structured stats local result, got %+v", ev)
		}
		if ev.Text == "" || ev.Text != ev.LocalResult.PlainText {
			t.Fatalf("expected text fallback to match local result, text=%q local=%q", ev.Text, ev.LocalResult.PlainText)
		}
		return
	}
}

func TestMCPLocalSubmitEmitsStructuredLocalResult(t *testing.T) {
	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	svc, err := New(t.Context(), cfg, app.StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	waitForServiceEvent(t, svc, EventSessionHydrated)

	svc.Dispatch(Intent{Kind: IntentSubmitLocal, Input: "/mcp"})
	for {
		ev := nextServiceEvent(t, svc)
		if ev.Kind != EventLocalSubmitResult {
			continue
		}
		if ev.LocalResult == nil || ev.LocalResult.Kind != "mcp" {
			t.Fatalf("expected structured mcp local result, got %+v", ev)
		}
		if ev.Text == "" || ev.Text != ev.LocalResult.PlainText {
			t.Fatalf("expected text fallback to match local result, text=%q local=%q", ev.Text, ev.LocalResult.PlainText)
		}
		return
	}
}

func TestRequestExitClearsUnreadableWorktreeAndQuits(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	workspace := t.TempDir()
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(oldwd) }()

	missing := filepath.Join(t.TempDir(), "missing-worktree")
	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	svc, err := New(t.Context(), cfg, app.StartOptions{
		NewSession: true,
		Worktree: app.WorktreeSession{
			Name:              "missing",
			Path:              missing,
			Branch:            "worktree-missing",
			OriginalWorkspace: workspace,
			OriginalBranch:    "main",
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	waitForServiceEvent(t, svc, EventSessionHydrated)

	svc.Dispatch(Intent{Kind: IntentRequestExit})
	info := waitForServiceEvent(t, svc, EventInfo)
	if !strings.Contains(info.Text, "Worktree state cleared: missing") {
		t.Fatalf("unexpected info event: %+v", info)
	}
	waitForServiceEvent(t, svc, EventExitRequested)

	meta, err := session.LoadSessionMeta(store.DefaultSessionsDir(cfg.DataDir), svc.SessionID())
	if err != nil {
		t.Fatalf("LoadSessionMeta: %v", err)
	}
	if meta.WorktreeName != "" || meta.WorktreePath != "" || meta.WorktreeBranch != "" {
		t.Fatalf("expected worktree metadata cleared: %+v", meta)
	}
}

func TestLocalSubmitDispatchEnqueuesWithoutHandlingInline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := &Service{
		ctx:          ctx,
		localSubmits: make(chan string, 1),
	}

	svc.Dispatch(Intent{Kind: IntentSubmitLocal, Input: "/stats all"})

	select {
	case got := <-svc.localSubmits:
		if got != "/stats all" {
			t.Fatalf("unexpected queued local submit: %q", got)
		}
	default:
		t.Fatal("expected local submit to be queued without inline handling")
	}
}

func TestShutdownCancelsPendingInteractions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	turnCtx, turnCancel := context.WithCancel(ctx)
	approvalCh := make(chan policy.ApprovalDecision, 1)
	inputCh := make(chan userInputDecision, 1)
	svc := &Service{
		ctx:       ctx,
		events:    make(chan Event, 10),
		cancel:    turnCancel,
		approvals: map[string]chan policy.ApprovalDecision{"approval-1": approvalCh},
		inputs:    map[string]chan userInputDecision{"input-1": inputCh},
	}

	svc.Dispatch(Intent{Kind: IntentShutdown})

	select {
	case got := <-approvalCh:
		if got != policy.ApprovalCancel {
			t.Fatalf("approval decision = %v, want cancel", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not release pending approval")
	}
	select {
	case got := <-inputCh:
		if got.ok {
			t.Fatalf("user input decision ok = true, want false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not release pending user input")
	}
	select {
	case <-turnCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not cancel active turn")
	}
	if len(svc.approvals) != 0 {
		t.Fatalf("pending approvals not cleared: %+v", svc.approvals)
	}
	if len(svc.inputs) != 0 {
		t.Fatalf("pending inputs not cleared: %+v", svc.inputs)
	}
}

func TestShutdownRejectsLateInteractions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := &Service{
		ctx:           ctx,
		events:        make(chan Event, 10),
		approvals:     map[string]chan policy.ApprovalDecision{},
		sessionGrants: map[string]map[string]bool{},
		inputs:        map[string]chan userInputDecision{},
	}

	svc.Dispatch(Intent{Kind: IntentShutdown})

	approvalDone := make(chan policy.ApprovalDecision, 1)
	go func() {
		approvalDone <- svc.awaitApproval(policy.ApprovalRequest{
			SessionID: "session-1",
			Key:       "approval-key",
			ToolCall:  core.ToolCall{ID: "approval-late", Name: "shell_run", Input: `{"command":"date"}`},
		})
	}()
	select {
	case got := <-approvalDone:
		if got != policy.ApprovalCancel {
			t.Fatalf("late approval decision = %v, want cancel", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("late approval request blocked after shutdown")
	}

	inputDone := make(chan bool, 1)
	go func() {
		_, ok := svc.awaitUserInput(agent.UserInputRequest{
			SessionID: "session-1",
			ToolCall:  core.ToolCall{ID: "input-late", Name: "request_user_input"},
			Questions: []core.UserInputQuestion{{
				Header:   "Choice",
				ID:       "choice",
				Question: "Continue?",
			}},
		})
		inputDone <- ok
	}()
	select {
	case ok := <-inputDone:
		if ok {
			t.Fatal("late user input ok = true, want false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("late user input request blocked after shutdown")
	}
	if len(svc.approvals) != 0 {
		t.Fatalf("late approval should not be tracked: %+v", svc.approvals)
	}
	if len(svc.inputs) != 0 {
		t.Fatalf("late user input should not be tracked: %+v", svc.inputs)
	}
	select {
	case ev := <-svc.events:
		if ev.Kind == EventApprovalRequired || ev.Kind == EventUserInputRequired {
			t.Fatalf("late interaction emitted modal event: %+v", ev)
		}
	default:
	}
}

func TestAwaitApprovalEmitsFileReviewMetadataAndDefersFileCache(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := &Service{
		ctx:           ctx,
		events:        make(chan Event, 8),
		approvals:     map[string]chan policy.ApprovalDecision{},
		sessionGrants: map[string]map[string]bool{},
		inputs:        map[string]chan userInputDecision{},
	}
	call := core.ToolCall{
		ID:    "approval-files",
		Name:  "apply_patch",
		Input: `{"patch":"*** Begin Patch\n*** Update File: a.txt\n@@\n-old\n+new\n*** Add File: b.txt\n+created\n*** End Patch"}`,
	}
	keys := []string{"file:a.txt", "file:b.txt"}
	approvalDone := make(chan policy.ApprovalDecision, 1)
	go func() {
		approvalDone <- svc.awaitApproval(policy.ApprovalRequest{
			SessionID: "session-1",
			ToolCall:  call,
			Key:       keys[0],
			Keys:      keys,
		})
	}()

	ev := waitForServiceEvent(t, svc, EventApprovalRequired)
	if got := ev.Metadata["approval_kind"]; got != "file_diff_review" {
		t.Fatalf("approval kind = %v, want file_diff_review", got)
	}
	if got := ev.Metadata["approval_session_scope"]; got != "these files: a.txt, b.txt" {
		t.Fatalf("session scope = %v", got)
	}
	svc.resolveApproval("approval-files", policy.ApprovalAllowForSession)
	select {
	case got := <-approvalDone:
		if got != policy.ApprovalAllowForSession {
			t.Fatalf("decision = %v, want allow for session", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approval did not resolve")
	}

	svc.approveMu.Lock()
	if svc.sessionGrantAllLocked("session-1", []string{"file:a.txt"}) {
		t.Fatal("file-scoped approval should not cache before tool success")
	}
	svc.approveMu.Unlock()

	svc.syncApprovalGrant(&agent.ToolApprovalGranted{
		SessionID:  "session-1",
		ToolCallID: "approval-files",
		ToolName:   "apply_patch",
		Key:        keys[0],
		Keys:       keys,
	})

	got := svc.awaitApproval(policy.ApprovalRequest{
		SessionID: "session-1",
		ToolCall:  core.ToolCall{ID: "approval-a", Name: "write", Input: `{"file_path":"a.txt","content":"x"}`},
		Key:       "file:a.txt",
		Keys:      []string{"file:a.txt"},
	})
	if got != policy.ApprovalAllowForSession {
		t.Fatalf("cached decision = %v, want allow for session", got)
	}
	select {
	case ev := <-svc.events:
		t.Fatalf("cached approval emitted event: %+v", ev)
	default:
	}
}

func TestAwaitApprovalCachedDecisionIsAuditOnly(t *testing.T) {
	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	svc, err := New(t.Context(), cfg, app.StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	waitForServiceEvent(t, svc, EventSessionHydrated)

	svc.approveMu.Lock()
	svc.grantSessionAllLocked("session-1", []string{"shell:bounded:git:status"})
	svc.approveMu.Unlock()

	got := svc.awaitApproval(policy.ApprovalRequest{
		SessionID: "session-1",
		ToolCall:  core.ToolCall{ID: "approval-cached", Name: "shell_run", Input: `{"command":"git status"}`},
		Key:       "shell:bounded:git:status",
		Keys:      []string{"shell:bounded:git:status"},
	})
	if got != policy.ApprovalAllowForSession {
		t.Fatalf("cached approval decision = %v, want allow for session", got)
	}
	select {
	case ev := <-svc.events:
		if ev.Kind == EventApprovalRequired {
			t.Fatalf("cached approval emitted user prompt: %+v", ev)
		}
	default:
	}

	events := readApprovalEvents(t, svc.app.SessionsDir(), "session-1")
	if len(events) != 1 {
		t.Fatalf("expected one audit event, got %+v", events)
	}
	if events[0].Event != "approval_prompt_cached_allowed" || telemetry.ApprovalEventIsUserVisible(events[0].Event) {
		t.Fatalf("cached approval should be audit-only, got %+v", events[0])
	}
}

func TestLocalSubmitEmitsDone(t *testing.T) {
	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	svc, err := New(t.Context(), cfg, app.StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	waitForServiceEvent(t, svc, EventSessionHydrated)

	svc.Dispatch(Intent{Kind: IntentSubmitLocal, Input: "/model bad"})

	errEvent := waitForServiceEvent(t, svc, EventLocalSubmitResult)
	if errEvent.Status != "error" || errEvent.Text != "usage: /model" {
		t.Fatalf("unexpected local submit error: status=%q text=%q", errEvent.Status, errEvent.Text)
	}
	waitForServiceEvent(t, svc, EventLocalSubmitDone)
}

func TestDeclinePlanPersistsHiddenMarkerAndStaysInPlanMode(t *testing.T) {
	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	svc, err := New(t.Context(), cfg, app.StartOptions{NewSession: true, ModeOverride: "plan"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	waitForServiceEvent(t, svc, EventSessionHydrated)

	svc.Dispatch(Intent{Kind: IntentDeclinePlan})

	info := waitForServiceEvent(t, svc, EventInfo)
	if info.Text != "Plan not approved; staying in Plan mode" {
		t.Fatalf("unexpected decline info: %q", info.Text)
	}
	done := waitForServiceEvent(t, svc, EventTurnDone)
	if done.LastResponse != info.Text {
		t.Fatalf("unexpected decline turn response: %q", done.LastResponse)
	}
	if got := svc.app.CurrentMode(); got != session.ModePlan {
		t.Fatalf("decline should stay in plan mode, got %s", got)
	}
	msgs, err := svc.app.ListMessages()
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("expected hidden plan-not-approved marker")
	}
	got := msgs[len(msgs)-1]
	if got.Role != core.RoleUser || !got.Hidden || got.FinishReason != core.FinishReasonCanceled {
		t.Fatalf("unexpected marker message metadata: %+v", got)
	}
	if !strings.Contains(got.Text, "<plan_not_approved>") || !strings.Contains(got.Text, "specific proposal as declined") {
		t.Fatalf("unexpected marker text: %q", got.Text)
	}
	if strings.Contains(got.Text, "Stay in planning mode") {
		t.Fatalf("decline marker must not force future turns to stay in plan mode: %q", got.Text)
	}
}

func TestModeSwitchPersistsHiddenModeChangedMarker(t *testing.T) {
	tests := []struct {
		name string
		from session.Mode
		to   session.Mode
	}{
		{name: "ask to agent", from: session.ModeAsk, to: session.ModeAgent},
		{name: "plan to agent", from: session.ModePlan, to: session.ModeAgent},
		{name: "agent to ask", from: session.ModeAgent, to: session.ModeAsk},
		{name: "agent to plan", from: session.ModeAgent, to: session.ModePlan},
		{name: "ask reaffirmed", from: session.ModeAsk, to: session.ModeAsk},
		{name: "plan reaffirmed", from: session.ModePlan, to: session.ModePlan},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := app.DefaultConfig()
			cfg.DataDir = t.TempDir()
			svc, err := New(t.Context(), cfg, app.StartOptions{NewSession: true, ModeOverride: string(tt.from)})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer svc.Close()
			waitForServiceEvent(t, svc, EventSessionHydrated)

			msg, err := svc.app.SetMode(tt.to)
			if err != nil {
				t.Fatalf("SetMode: %v", err)
			}
			if !strings.Contains(msg, "mode enabled") {
				t.Fatalf("unexpected mode message: %q", msg)
			}

			msgs, err := svc.app.ListMessages()
			if err != nil {
				t.Fatalf("ListMessages: %v", err)
			}
			if len(msgs) == 0 {
				t.Fatal("expected hidden mode-changed marker")
			}
			got := msgs[len(msgs)-1]
			if got.Role != core.RoleUser || !got.Hidden || got.FinishReason != core.FinishReasonEndTurn {
				t.Fatalf("unexpected marker metadata: %+v", got)
			}
			if !strings.Contains(got.Text, "<mode_changed>") ||
				!strings.Contains(got.Text, "active session mode is now "+string(tt.to)) ||
				!strings.Contains(got.Text, "changed from "+string(tt.from)) ||
				!strings.Contains(got.Text, "anything other than "+string(tt.to)) ||
				!strings.Contains(got.Text, "stale") {
				t.Fatalf("unexpected marker text: %q", got.Text)
			}
		})
	}
}

func TestPlanDeclineThenAgentModeRecordsOverrideAfterDecline(t *testing.T) {
	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	svc, err := New(t.Context(), cfg, app.StartOptions{NewSession: true, ModeOverride: "plan"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	waitForServiceEvent(t, svc, EventSessionHydrated)

	svc.Dispatch(Intent{Kind: IntentDeclinePlan})
	waitForServiceEvent(t, svc, EventInfo)
	waitForServiceEvent(t, svc, EventTurnDone)

	if _, err := svc.app.SetMode(session.ModeAgent); err != nil {
		t.Fatalf("SetMode: %v", err)
	}

	msgs, err := svc.app.ListMessages()
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) < 2 {
		t.Fatalf("expected decline marker followed by mode marker, got %+v", msgs)
	}
	decline := msgs[len(msgs)-2]
	override := msgs[len(msgs)-1]
	if !strings.Contains(decline.Text, "<plan_not_approved>") {
		t.Fatalf("expected decline marker before mode override, got %q", decline.Text)
	}
	if strings.Contains(decline.Text, "Stay in planning mode") {
		t.Fatalf("decline marker must not keep future turns in plan mode: %q", decline.Text)
	}
	if override.Role != core.RoleUser || !strings.Contains(override.Text, "<mode_changed>") {
		t.Fatalf("expected system mode override after decline marker, got %+v", override)
	}
	if !strings.Contains(override.Text, "active session mode is now agent") ||
		!strings.Contains(override.Text, "changed from plan") ||
		!strings.Contains(override.Text, "anything other than agent") ||
		!strings.Contains(override.Text, "stale") {
		t.Fatalf("unexpected mode override text: %q", override.Text)
	}
}

func TestBuildImplementPlanPromptDoesNotEmbedStalePlan(t *testing.T) {
	prompt := buildImplementPlanPrompt("# Old Plan\n- Patch it")
	if !strings.Contains(prompt, "Implement the plan.") {
		t.Fatalf("expected generic implement prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "update_plan checklist") {
		t.Fatalf("expected update_plan guidance, got %q", prompt)
	}
	if strings.Contains(prompt, "# Old Plan") || strings.Contains(prompt, "approved plan") {
		t.Fatalf("implement prompt should not embed stale plan text: %q", prompt)
	}
}

func TestLocalSubmitBtwWithoutQuestionEmitsUsage(t *testing.T) {
	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	svc, err := New(t.Context(), cfg, app.StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	waitForServiceEvent(t, svc, EventSessionHydrated)

	svc.Dispatch(Intent{Kind: IntentSubmitLocal, Input: "/btw"})

	errEvent := waitForServiceEvent(t, svc, EventLocalSubmitResult)
	if errEvent.Status != "error" || errEvent.Text != "Usage: /btw <your question>" {
		t.Fatalf("unexpected local submit error: status=%q text=%q", errEvent.Status, errEvent.Text)
	}
	waitForServiceEvent(t, svc, EventLocalSubmitDone)
}

func TestLocalSubmitDiffEmitsDiffResult(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	svc, err := New(t.Context(), cfg, app.StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	waitForServiceEvent(t, svc, EventSessionHydrated)

	svc.Dispatch(Intent{Kind: IntentSubmitLocal, Input: "/diff"})

	ev := waitForServiceEvent(t, svc, EventDiffResult)
	if !strings.Contains(ev.Text, "not inside a git repository") {
		t.Fatalf("unexpected diff result: %q", ev.Text)
	}
	waitForServiceEvent(t, svc, EventLocalSubmitDone)
}

func TestPermissionsCommandOpensMenuAndSetsSessionAutoAccept(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	svc, err := New(t.Context(), cfg, app.StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	waitForServiceEvent(t, svc, EventSessionHydrated)

	svc.Dispatch(Intent{Kind: IntentSubmitLocal, Input: "/permissions"})

	menu := waitForServiceEvent(t, svc, EventPermissionsMenu)
	if menu.AutoAccept {
		t.Fatalf("unexpected permissions menu auto accept state: %+v", menu)
	}
	waitForServiceEvent(t, svc, EventLocalSubmitDone)

	svc.Dispatch(Intent{Kind: IntentSetApprovalMode, ApprovalMode: "auto_accept"})
	info := waitForServiceEvent(t, svc, EventInfo)
	if info.Text != "Session auto-accept enabled" {
		t.Fatalf("unexpected permissions enable info: %q", info.Text)
	}
	waitForServiceEvent(t, svc, EventTurnDone)

	svc.Dispatch(Intent{Kind: IntentSubmitLocal, Input: "/permissions"})
	menu = waitForServiceEvent(t, svc, EventPermissionsMenu)
	if !menu.AutoAccept {
		t.Fatalf("unexpected permissions menu auto accept state after enable: %+v", menu)
	}
	waitForServiceEvent(t, svc, EventLocalSubmitDone)

	svc.Dispatch(Intent{Kind: IntentSetApprovalMode, ApprovalMode: "ask"})
	info = waitForServiceEvent(t, svc, EventInfo)
	if info.Text != "Session auto-accept disabled" {
		t.Fatalf("unexpected permissions disable info: %q", info.Text)
	}
	waitForServiceEvent(t, svc, EventTurnDone)

	if _, loaded, err := app.LoadConfigFile(app.ProjectLocalConfigPath(work)); err != nil || loaded {
		t.Fatalf("session auto accept should not write project config loaded=%v err=%v", loaded, err)
	}
}

func TestReviewCommandOpensMenu(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	svc, err := New(t.Context(), cfg, app.StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	waitForServiceEvent(t, svc, EventSessionHydrated)

	svc.Dispatch(Intent{Kind: IntentSubmit, Input: "/review"})
	ev := waitForServiceEvent(t, svc, EventReviewMenu)
	if ev.Kind != EventReviewMenu {
		t.Fatalf("expected review menu event, got %+v", ev)
	}

	svc.Dispatch(Intent{Kind: IntentSubmitLocal, Input: "/review"})
	ev = waitForServiceEvent(t, svc, EventReviewMenu)
	if ev.Kind != EventReviewMenu {
		t.Fatalf("expected local review menu event, got %+v", ev)
	}
}

func TestLocalSubmitDispatchPreservesOrder(t *testing.T) {
	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	svc, err := New(t.Context(), cfg, app.StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	waitForServiceEvent(t, svc, EventSessionHydrated)

	svc.Dispatch(Intent{Kind: IntentSubmitLocal, Input: "/model bad"})
	svc.Dispatch(Intent{Kind: IntentSubmitLocal, Input: "/skills bad"})

	first := waitForServiceEvent(t, svc, EventLocalSubmitResult)
	second := waitForServiceEvent(t, svc, EventLocalSubmitResult)
	if first.Status != "error" || second.Status != "error" || first.Text != "usage: /model" || second.Text != "usage: /skills" {
		t.Fatalf("expected local submit order to be preserved, got status=%q text=%q then status=%q text=%q", first.Status, first.Text, second.Status, second.Text)
	}
}

func TestSkillMentionEmitsLoadedEventNotInfo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEEPSEEK_API_KEY", "")
	work := t.TempDir()
	t.Chdir(work)
	writeServiceSkill(t, filepath.Join(work, ".whale", "skills", "test-skill"), "test-skill", "Workspace skill.")

	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	svc, err := New(t.Context(), cfg, app.StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	waitForServiceEvent(t, svc, EventSessionHydrated)

	svc.Dispatch(Intent{Kind: IntentSubmit, Input: "$test-skill review this"})
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-svc.Events():
			if ev.Kind == EventInfo && strings.Contains(ev.Text, "loaded skill: test-skill") {
				t.Fatalf("skill load should not be emitted as info: %+v", ev)
			}
			if ev.Kind == EventSkillLoaded {
				if ev.Text != "loaded skill: test-skill" {
					t.Fatalf("unexpected skill loaded text: %q", ev.Text)
				}
				waitForServiceEvent(t, svc, EventTurnDone)
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for skill loaded event")
		}
	}
}

func TestSilentPromptRewriteAppliesBeforeSkillDetection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DEEPSEEK_API_KEY", "")
	work := t.TempDir()
	t.Chdir(work)
	writeServiceSkill(t, filepath.Join(work, ".whale", "skills", "test-skill"), "test-skill", "Workspace skill.")
	if err := os.WriteFile(filepath.Join(work, ".whale", "config.toml"), []byte(`[[hooks.UserPromptSubmit]]
command = "printf '{\"updated_input\":\"$test-skill review this\"}'"
`), 0o600); err != nil {
		t.Fatalf("write hook config: %v", err)
	}

	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	svc, err := New(t.Context(), cfg, app.StartOptions{NewSession: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer svc.Close()
	waitForServiceEvent(t, svc, EventSessionHydrated)

	svc.Dispatch(Intent{Kind: IntentSubmit, Input: "plain prompt"})
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-svc.Events():
			if ev.Kind == EventInfo && strings.Contains(ev.Text, "updated_input") {
				t.Fatalf("silent prompt rewrite hook should not emit info: %+v", ev)
			}
			if ev.Kind == EventSkillLoaded {
				if ev.Text != "loaded skill: test-skill" {
					t.Fatalf("unexpected skill loaded text: %q", ev.Text)
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for skill loaded event from rewritten prompt")
		}
	}
}

func TestShouldSuppressCancelledTurnErrorOnlyForCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	wrapped := fmt.Errorf("request failed: %w", context.Canceled)
	if shouldSuppressCancelledTurnError(ctx, wrapped) {
		t.Fatal("did not expect suppression before the turn context is cancelled")
	}
	cancel()
	if !shouldSuppressCancelledTurnError(ctx, wrapped) {
		t.Fatal("expected user-cancelled context error to be suppressed")
	}
	if shouldSuppressCancelledTurnError(ctx, fmt.Errorf("request failed: boom")) {
		t.Fatal("did not expect unrelated errors to be suppressed")
	}
}

func nextServiceEvent(t *testing.T, s *Service) Event {
	t.Helper()
	select {
	case ev := <-s.Events():
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for service event")
		return Event{}
	}
}

func waitForServiceEvent(t *testing.T, s *Service, kind EventKind) Event {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-s.Events():
			if ev.Kind == kind {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for service event %s", kind)
			return Event{}
		}
	}
}

func readApprovalEvents(t *testing.T, sessionsDir, sessionID string) []telemetry.ApprovalEvent {
	t.Helper()
	f, err := os.Open(telemetry.ApprovalEventsPath(sessionsDir, sessionID))
	if err != nil {
		t.Fatalf("open approval events: %v", err)
	}
	defer f.Close()
	var out []telemetry.ApprovalEvent
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var rec telemetry.ApprovalEvent
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatalf("unmarshal approval event: %v", err)
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan approval events: %v", err)
	}
	return out
}

func assertSessionSelectedAndHydrated(t *testing.T, s *Service) {
	t.Helper()
	sawInfo := false
	for {
		ev := nextServiceEvent(t, s)
		switch ev.Kind {
		case EventInfo:
			if strings.Contains(ev.Text, "resumed session: sess-1") {
				sawInfo = true
			}
		case EventSessionHydrated:
			if !sawInfo {
				t.Fatal("expected resumed session info before hydration")
			}
			if ev.SessionID != "sess-1" {
				t.Fatalf("hydrated session = %s, want sess-1", ev.SessionID)
			}
			return
		}
	}
}

func writeSessionFile(t *testing.T, dataDir, id, text string) {
	t.Helper()
	sessionsDir := filepath.Join(dataDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	line := fmt.Sprintf("{\"role\":\"user\",\"text\":%q}\n", text)
	if err := os.WriteFile(filepath.Join(sessionsDir, id+".jsonl"), []byte(line), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
}

func writeServiceSkill(t *testing.T, dir, name, desc string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n# %s\n", name, desc, name)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
}

func TestSummarizeToolCall_GrepShowsPatternPathAndInclude(t *testing.T) {
	got := summarizeToolCall(core.ToolCall{
		Name:  "grep",
		Input: `{"pattern":"assistant_delta","path":"internal/tui","include":"*.go"}`,
	})
	if got != "grep: assistant_delta in internal/tui (*.go)" {
		t.Fatalf("unexpected grep summary: %q", got)
	}
}

func TestSummarizeToolCall_SearchFilesShowsPatternAndPath(t *testing.T) {
	got := summarizeToolCall(core.ToolCall{
		Name:  "search_files",
		Input: `{"pattern":"markdown.go","path":"internal/tui"}`,
	})
	if got != "search_files: markdown.go in internal/tui" {
		t.Fatalf("unexpected search_files summary: %q", got)
	}
}

func TestSummarizeToolCall_WebSearchUsesNestedSearchQuery(t *testing.T) {
	got := summarizeToolCall(core.ToolCall{
		Name:  "web_search",
		Input: `{"search_query":[{"q":"F1 pit strategy tools"}]}`,
	})
	if got != "web_search: F1 pit strategy tools" {
		t.Fatalf("unexpected web_search summary: %q", got)
	}
}

func TestSummarizeToolCall_TaskTools(t *testing.T) {
	got := summarizeToolCall(core.ToolCall{
		Name:  "parallel_reason",
		Input: `{"prompts":["a","b","c"]}`,
	})
	if got != "parallel_reason: 3 prompt(s)" {
		t.Fatalf("unexpected parallel_reason summary: %q", got)
	}
	got = summarizeToolCall(core.ToolCall{
		Name:  "spawn_subagent",
		Input: `{"role":"review","task":"review internal/tasks\nignore details"}`,
	})
	if got != "spawn_subagent: review · review internal/tasks" {
		t.Fatalf("unexpected spawn_subagent summary: %q", got)
	}
}

func TestSummarizeTaskActivity(t *testing.T) {
	got := summarizeTaskActivity(EventTaskStarted, &agent.TaskActivityInfo{ToolName: "parallel_reason", Status: "started", Count: 4})
	if got != "parallel_reason started · 4 prompt(s)" {
		t.Fatalf("unexpected parallel activity: %q", got)
	}
	got = summarizeTaskActivity(EventTaskCompleted, &agent.TaskActivityInfo{ToolName: "spawn_subagent", Status: "completed", Role: "review", DurationMS: 1200})
	if got != "spawn_subagent completed · review · 1200ms" {
		t.Fatalf("unexpected subagent activity: %q", got)
	}
}
