package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/llm"
	"github.com/usewhale/whale/internal/store"
)

// Reproduces the user's report: "它调用很多 subagent 去干活，然后有 subagent
// 一直被挂起，没返回数据，它就被阻塞了" — a parallel fan-out where one worker
// hangs without returning data wedges the whole call, and a user interrupt
// (ctx cancellation) cannot free it.
//
// ParallelReason waits on its workers with a bare sync.WaitGroup.Wait()
// (parallel_reason.go:69) that has NO select on ctx.Done(). It relies entirely
// on every runOneReasoningQuery returning, which in turn relies on every
// provider stream closing its channel. If a single provider stream hangs and
// ignores ctx (exactly "没返回数据"), wg.Wait() blocks forever even after the
// caller cancels ctx — so the tool's Run never returns, the turn goroutine
// stays wedged, EventTurnDone never fires, and the TUI is stuck busy ("关不掉").
//
// This test cancels ctx and asserts ParallelReason returns. On the buggy code
// it never returns (deadlock), so the test fails by timeout.
func TestParallelReasonHangsWhenOneWorkerIgnoresCtx(t *testing.T) {
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(ctx context.Context, history []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			out := make(chan llm.ProviderEvent, 1)
			prompt := history[len(history)-1].Text
			if prompt == "hang" {
				// A wedged subagent: never sends, never closes, never looks
				// at ctx. Models a provider connection that is established but
				// returns no data and does not observe cancellation.
				return out
			}
			go func() {
				defer close(out)
				out <- llm.ProviderEvent{Type: llm.EventComplete, Response: &llm.ProviderResponse{Content: "ok:" + prompt}}
			}()
			return out
		}), nil
	}
	r := NewRunner(RunnerConfig{ProviderFactory: factory})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = r.ParallelReason(ctx, ParallelReasonRequest{Prompts: []string{"a", "hang", "c"}})
	}()

	// Let the healthy workers finish, then interrupt like the user does.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// ParallelReason returned after cancel — the wedge is escapable.
	case <-time.After(3 * time.Second):
		t.Fatal("ParallelReason did not return within 3s after ctx cancel: one hung worker that ignores ctx deadlocks wg.Wait(); the turn goroutine can never unwind (关不掉)")
	}
}

// The faithful "subagent" reproduction. A foreground subagent's child agent
// uses a provider whose stream hangs and ignores ctx ("没返回数据"). The
// parent waits for the child by ranging the child's event channel
// (subagent.go:347 drainEvents, a bare `for range events` with no ctx.Done
// escape); the child agent in turn ranges the provider stream with another
// bare range (stream_ingest.go:43). Neither unwinds until the provider closes
// its channel, which a hung provider never does. So SpawnSubagent blocks
// forever even after the caller cancels ctx — exactly "调用 subagent 干活，
// subagent 挂起没返回，主循环被阻塞，关不掉".
func TestSpawnSubagentHangsWhenChildProviderIgnoresCtx(t *testing.T) {
	factory := func(_ string, _ int) (llm.Provider, error) {
		return providerFunc(func(_ context.Context, _ []core.Message, _ []core.Tool) <-chan llm.ProviderEvent {
			// Wedged child: a channel that never sends, never closes, and
			// never consults ctx.
			return make(chan llm.ProviderEvent)
		}), nil
	}
	dir := t.TempDir()
	msgStore, err := store.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	parent := core.NewToolRegistry([]core.Tool{testTool{name: "read_file", readOnly: true, capabilities: []string{CapabilityWorkspaceRead}}})
	r := NewRunner(RunnerConfig{
		ProviderFactory: factory,
		ParentTools:     parent,
		MessageStore:    msgStore,
		SessionsDir:     dir,
		ParentSessionID: "parent-session",
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = r.SpawnSubagent(ctx, SpawnSubagentRequest{Task: "inspect", Role: "review"})
	}()

	time.Sleep(100 * time.Millisecond)
	cancel() // user interrupt

	select {
	case <-done:
		// SpawnSubagent returned after cancel — interruptible.
	case <-time.After(3 * time.Second):
		t.Fatal("SpawnSubagent did not return within 3s after ctx cancel: the child agent's hung provider stream keeps the child event channel open, so drainEvents (for range events) never unwinds; the parent turn goroutine is wedged (关不掉)")
	}
}
