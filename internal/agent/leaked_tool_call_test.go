package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

// leakThenRecoverProvider emits a turn that wrote its tool call as plain text
// (no structured call), then — after the nudge — a real structured call, then
// a clean final answer.
type leakThenRecoverProvider struct {
	calls int
}

func (p *leakThenRecoverProvider) StreamResponse(_ context.Context, _ []Message, _ []Tool) <-chan ProviderEvent {
	p.calls++
	switch p.calls {
	case 1:
		return eventStream(endTurnEvent(`On it. <tool_calls> <read_file path="a.go"> </read_file> </tool_calls>`))
	case 2:
		return eventStream(toolUseEvent(toolCall("tc-1", "echo", `{"v":"hi"}`)))
	default:
		return eventStream(endTurnEvent("a.go has 10 lines."))
	}
}

func TestTurnLoopRecoversFromLeakedToolCall(t *testing.T) {
	store := NewInMemoryStore()
	prov := &leakThenRecoverProvider{}
	a := NewAgent(prov, store, []Tool{echoTool{}})

	events, err := a.RunStreamWithOptions(context.Background(), "s-leak", "read a.go", false)
	if err != nil {
		t.Fatalf("RunStreamWithOptions: %v", err)
	}
	var scrubbed, sawToolResult bool
	var done *core.Message
	for ev := range events {
		switch ev.Type {
		case AgentEventTypeError:
			t.Fatalf("unexpected error: %v", ev.Err)
		case AgentEventTypeLeakedToolCallScrubbed:
			scrubbed = true
		case AgentEventTypeToolResult:
			sawToolResult = true
		case AgentEventTypeDone:
			done = ev.Message
		}
	}

	if !scrubbed {
		t.Fatal("expected a leaked_tool_call_scrubbed event")
	}
	if !sawToolResult {
		t.Fatal("expected the recovered structured tool call to execute")
	}
	if prov.calls < 3 {
		t.Fatalf("expected the turn to continue past the leak, got %d provider calls", prov.calls)
	}
	if done == nil || !strings.Contains(done.Text, "10 lines") {
		t.Fatalf("expected clean final answer, got %#v", done)
	}

	// The leaked wrapper must be scrubbed from the persisted history so it can't
	// keep contaminating later turns.
	msgs, err := store.List(context.Background(), "s-leak")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, m := range msgs {
		if m.Role == core.RoleAssistant && strings.Contains(m.Text, "<tool_calls") {
			t.Fatalf("leaked wrapper survived in persisted assistant message: %q", m.Text)
		}
	}
}

func TestContainsLeakedToolCall(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{
			name: "observed deepseek leak",
			in:   `Let me look. <tool_calls> <read_file path="internal/tui/chat_view.go"> </read_file> </tool_calls>`,
			want: true,
		},
		{
			name: "singular tool_call wrapper",
			in:   "<tool_call>{\"name\":\"Read\"}</tool_call>",
			want: true,
		},
		{
			name: "invoke block",
			in:   `prose <invoke name="Read"><parameter name="path">x</parameter></invoke> tail`,
			want: true,
		},
		{
			name: "function_calls wrapper",
			in:   "<function_calls><invoke name=\"Bash\"></invoke></function_calls>",
			want: true,
		},
		{
			name: "plain prose mentioning the tag without a close is not a leak",
			in:   "The model emits a literal `<tool_calls>` opening tag and then stops.",
			want: false,
		},
		{
			name: "ordinary answer",
			in:   "internal/tui/chat_view.go has 316 lines.",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := containsLeakedToolCall(tc.in); got != tc.want {
				t.Fatalf("containsLeakedToolCall(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestStripLeakedToolCalls(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "strips wrapper and inner tags whole, keeps surrounding prose",
			in:   `Reading the file. <tool_calls> <read_file path="a.go"> </read_file> </tool_calls> Done.`,
			want: `Reading the file.  Done.`,
		},
		{
			name: "removes a leak-only body to empty",
			in:   `<tool_calls><read_file path="a.go"></read_file></tool_calls>`,
			want: ``,
		},
		{
			name: "leaves unbalanced opening tag untouched",
			in:   "talking about <tool_calls> as a concept",
			want: "talking about <tool_calls> as a concept",
		},
		{
			name: "non-leak text is unchanged",
			in:   "316 lines total.",
			want: "316 lines total.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripLeakedToolCalls(tc.in); got != tc.want {
				t.Fatalf("stripLeakedToolCalls(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
