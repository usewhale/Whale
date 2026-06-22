package render

import (
	"strings"

	"github.com/usewhale/whale/internal/runtime/protocol"
)

type MessageKind string

const (
	KindText        MessageKind = "text"
	KindNotice      MessageKind = "notice"
	KindStatus      MessageKind = "status"
	KindThinking    MessageKind = "thinking"
	KindPlan        MessageKind = "plan"
	KindPlanUpdate  MessageKind = "plan_update"
	KindLocalStatus MessageKind = "local_status"
	KindLocalMCP    MessageKind = "local_mcp"
	KindToolCall    MessageKind = "tool_call"
	KindToolResult  MessageKind = "tool_result"
	KindToolSummary MessageKind = "tool_summary"
	KindSubagent    MessageKind = "subagent"
)

type UIMessage struct {
	ID   string
	Role string
	Kind MessageKind
	Text string
	// Seq orders this message against timeline (tool) rows within a single live
	// turn so text<->tool interleaving is preserved. Assembler messages are
	// stamped lazily (see Assembler.ensureSeqs); timeline rows derive their Seq
	// from Assembler.SeqFloor at the moment the tool is created. Zero means
	// "unsequenced" (e.g. hydrated/committed rows rendered in slice order).
	Seq           int
	ToolName      string
	ToolIdentity  string
	Streaming     bool
	Local         *protocol.LocalResult
	FocusSummary  *FocusSummary
	Notice        *SystemNotice
	FullReasoning bool
	SubagentSteps []protocol.ProgressStep
}

type SystemNotice struct {
	Kind    string
	Tone    string
	Action  string
	Subject string
	Detail  string
	Command string
	Scope   string
}

func (n *SystemNotice) Text() string {
	if n == nil {
		return ""
	}
	parts := make([]string, 0, 4)
	for _, part := range []string{n.Action, n.Subject, n.Detail, n.Command} {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	text := strings.Join(parts, " ")
	if scope := strings.TrimSpace(n.Scope); scope != "" {
		if text != "" {
			text += " · "
		}
		text += scope
	}
	return text
}

type FocusSummary struct {
	Parts []FocusSummaryPart
	Hint  string
}

type FocusSummaryPart struct {
	Kind   string
	State  string
	Count  int
	Action string
	Detail string
	Status string
}

func (s *FocusSummary) Text() string {
	if s == nil {
		return ""
	}
	parts := make([]string, 0, len(s.Parts))
	for _, part := range s.Parts {
		if text := part.Text(); text != "" {
			parts = append(parts, text)
		}
	}
	text := strings.Join(parts, ", ")
	if s.Hint != "" {
		if text != "" {
			text += " "
		}
		text += s.Hint
	}
	return text
}

func (p FocusSummaryPart) Text() string {
	text := strings.TrimSpace(p.Action)
	detail := strings.TrimSpace(p.Detail)
	if detail != "" {
		if text != "" {
			text += ": "
		}
		text += detail
	}
	if status := strings.TrimSpace(p.Status); status != "" {
		if text != "" {
			text += " "
		}
		text += status
	}
	return text
}

// seqStride spaces consecutive assembler-message sequence numbers so that
// timeline (tool) rows created between two text messages have room to sort
// strictly between them (SeqFloor + a small per-turn tiebreak). It bounds the
// number of tool rows that can sit in a single text gap to seqStride-1, far
// above any real turn.
const seqStride = 1 << 20

type Assembler struct {
	messages []UIMessage
	seqOrd   int // ordinal of the last sequenced message this turn
	// breakNextCoalesce forces the next AppendDelta/AddPlanDelta to start a new
	// message instead of merging into the trailing one. The model sets it at tool
	// boundaries (BreakCoalescing) so same-role text resumed after a tool does not
	// inherit the pre-tool message's render sequence and float above the tool.
	breakNextCoalesce bool
}

func NewAssembler() *Assembler {
	return &Assembler{}
}

// Reset clears the live messages but intentionally preserves seqOrd. Some reset
// paths (EventResponseReset) clear the assembler while KEEPING the timeline's
// tool rows, so the sequence floor must stay monotonic across the reset or a
// post-reset delta would reuse a sequence below a retained tool and render above
// it. ResetSequence — called from resetTimeline, the true turn boundary that
// also clears the timeline — is what zeroes the floor.
func (a *Assembler) Reset() {
	a.messages = nil
	a.breakNextCoalesce = false
}

// ResetSequence restarts the per-turn sequence floor. Call it only when the
// timeline is also being cleared, so no retained tool row outlives the reset.
func (a *Assembler) ResetSequence() {
	if a == nil {
		return
	}
	a.seqOrd = 0
}

// BreakCoalescing marks a tool boundary: the next streamed delta starts a fresh
// message even if it shares the trailing message's role, so it is sequenced
// after the intervening tool rather than coalesced onto pre-tool text.
func (a *Assembler) BreakCoalescing() {
	if a == nil {
		return
	}
	a.breakNextCoalesce = true
}

// ensureSeqs assigns a stable, ascending Seq to any message that does not have
// one yet. New messages always append at the tail, so stamping in slice order
// keeps Seq monotonic with arrival order; coalesced deltas reuse the existing
// message's Seq.
func (a *Assembler) ensureSeqs() {
	for i := range a.messages {
		if a.messages[i].Seq == 0 {
			a.seqOrd++
			a.messages[i].Seq = a.seqOrd * seqStride
		}
	}
}

// SeqFloor returns the Seq of the most recent assembler message (forcing any
// pending messages to be sequenced first). A timeline row created now should
// sort just above this floor, so callers add a small per-turn tiebreak.
func (a *Assembler) SeqFloor() int {
	if a == nil {
		return 0
	}
	a.ensureSeqs()
	return a.seqOrd * seqStride
}

func (a *Assembler) Len() int {
	if a == nil {
		return 0
	}
	return len(a.messages)
}

func (a *Assembler) RemoveAssistantMessages() {
	if a == nil || len(a.messages) == 0 {
		return
	}
	out := a.messages[:0]
	for _, msg := range a.messages {
		if msg.Role == "assistant" && msg.Kind == KindText {
			continue
		}
		out = append(out, msg)
	}
	a.messages = out
}

func (a *Assembler) RemoveStatusMessagesWithPrefix(prefix string) bool {
	prefix = strings.TrimSpace(prefix)
	if a == nil || len(a.messages) == 0 || prefix == "" {
		return false
	}
	out := a.messages[:0]
	removed := false
	for _, msg := range a.messages {
		if msg.Kind == KindStatus && strings.HasPrefix(strings.TrimSpace(msg.Text), prefix) {
			removed = true
			continue
		}
		out = append(out, msg)
	}
	a.messages = out
	return removed
}

func (a *Assembler) ReplaceTrailingAssistantMessages(text string) bool {
	t := strings.TrimSpace(strings.TrimRight(text, "\n"))
	if a == nil || t == "" || len(a.messages) == 0 {
		return false
	}
	start := -1
	for i := len(a.messages) - 1; i >= 0; i-- {
		msg := a.messages[i]
		if msg.Role == "assistant" && msg.Kind == KindText {
			start = i
			continue
		}
		break
	}
	if start == -1 {
		return false
	}
	a.messages[start].Text = t
	a.messages = a.messages[:start+1]
	return true
}

func (a *Assembler) Snapshot() []UIMessage {
	a.ensureSeqs()
	out := make([]UIMessage, len(a.messages))
	copy(out, a.messages)
	return out
}

func (a *Assembler) AppendDelta(role, text string) {
	t := strings.ReplaceAll(text, "\r\n", "\n")
	if t == "" {
		return
	}
	if n := len(a.messages); n > 0 && !a.breakNextCoalesce && canCoalesce(role, a.messages[n-1]) {
		a.messages[n-1].Text += t
		if role == "think" {
			a.messages[n-1].Streaming = true
		}
		return
	}
	if strings.TrimSpace(t) == "" {
		return
	}
	a.breakNextCoalesce = false
	a.messages = append(a.messages, UIMessage{
		Role:      role,
		Kind:      kindForRole(role),
		Text:      t,
		Streaming: role == "think",
	})
}

func (a *Assembler) AddNotice(text string) {
	t := strings.TrimSpace(strings.TrimRight(text, "\n"))
	if t == "" {
		return
	}
	a.messages = append(a.messages, UIMessage{
		Role: "notice",
		Kind: KindNotice,
		Text: t,
	})
}

func (a *Assembler) AddAssistantMessage(text string) {
	t := strings.TrimSpace(strings.TrimRight(text, "\n"))
	if t == "" {
		return
	}
	a.messages = append(a.messages, UIMessage{
		Role: "assistant",
		Kind: KindText,
		Text: t,
	})
}

func (a *Assembler) AddSystemNotice(notice *SystemNotice) {
	if notice == nil {
		return
	}
	text := strings.TrimSpace(notice.Text())
	if text == "" {
		return
	}
	a.messages = append(a.messages, UIMessage{
		Role:   "notice",
		Kind:   KindNotice,
		Text:   text,
		Notice: notice,
	})
}

func (a *Assembler) AddStatus(text string) {
	t := strings.TrimSpace(strings.TrimRight(text, "\n"))
	if t == "" {
		return
	}
	a.messages = append(a.messages, UIMessage{
		Role: "status",
		Kind: KindStatus,
		Text: t,
	})
}

func (a *Assembler) AddLocalResult(result *protocol.LocalResult) {
	if result == nil || strings.TrimSpace(result.Kind) == "" {
		return
	}
	text := strings.TrimSpace(strings.TrimRight(result.PlainText, "\n"))
	if text == "" {
		text = strings.TrimSpace(strings.TrimRight(result.Title, "\n"))
	}
	if text == "" {
		return
	}
	kind := KindText
	role := "info"
	switch result.Kind {
	case "status":
		kind = KindLocalStatus
		role = "local_status"
	case "mcp":
		kind = KindLocalMCP
		role = "local_mcp"
	}
	a.messages = append(a.messages, UIMessage{
		Role:  role,
		Kind:  kind,
		Text:  text,
		Local: result,
	})
}

func (a *Assembler) AddPlanDelta(text string) {
	t := strings.ReplaceAll(text, "\r\n", "\n")
	if t == "" {
		return
	}
	if n := len(a.messages); n > 0 && !a.breakNextCoalesce && a.messages[n-1].Kind == KindPlan {
		a.messages[n-1].Text += t
		return
	}
	if strings.TrimSpace(t) == "" {
		return
	}
	a.breakNextCoalesce = false
	a.messages = append(a.messages, UIMessage{
		Role: "plan",
		Kind: KindPlan,
		Text: t,
	})
}

func (a *Assembler) AddPlan(text string) {
	t := strings.TrimSpace(text)
	if t == "" {
		return
	}
	a.messages = append(a.messages, UIMessage{
		Role: "plan",
		Kind: KindPlan,
		Text: t,
	})
}

func (a *Assembler) AddPlanUpdate(text string) {
	t := strings.TrimSpace(text)
	if t == "" {
		return
	}
	a.messages = append(a.messages, UIMessage{
		Role: "plan",
		Kind: KindPlanUpdate,
		Text: t,
	})
}

func (a *Assembler) SetPlan(text string) {
	t := strings.TrimSpace(text)
	if t == "" {
		return
	}
	if a.replacePlanMessages(t) {
		return
	}
	a.AddPlan(t)
}

func (a *Assembler) replacePlanMessages(text string) bool {
	if a == nil || len(a.messages) == 0 {
		return false
	}
	// Keep the LAST plan row, not the first. With coalescing broken at tool
	// boundaries a Plan-mode turn can hold several plan rows (pre-tool preamble,
	// post-tool final); the finalized card must inherit the latest row's Seq so it
	// renders after the investigation tools rather than at the pre-tool position.
	last := -1
	for i, msg := range a.messages {
		if msg.Kind == KindPlan {
			last = i
		}
	}
	if last == -1 {
		return false
	}
	out := a.messages[:0]
	for i, msg := range a.messages {
		if msg.Kind == KindPlan {
			if i == last {
				msg.Text = text
				out = append(out, msg)
			}
			continue
		}
		out = append(out, msg)
	}
	a.messages = out
	return true
}

// DemoteUncompletedPlan converts plan cards built from streamed plan deltas back
// into ordinary assistant text. It is used when a Plan-mode turn streamed plan
// content but never finalized a plan (e.g. it looped into the cap/forced-summary
// path): the streamed text was investigation, not an approvable plan, so it must
// not render as a Proposed Plan card. Returns true if anything was demoted.
func (a *Assembler) DemoteUncompletedPlan() bool {
	if a == nil {
		return false
	}
	demoted := false
	for i := range a.messages {
		if a.messages[i].Kind == KindPlan {
			a.messages[i].Kind = KindText
			a.messages[i].Role = "assistant"
			demoted = true
		}
	}
	return demoted
}

func (a *Assembler) AddToolResult(name, text string) {
	a.AddToolResultWithRole(name, text, "result")
}

func (a *Assembler) AddToolResultWithRole(name, text, role string) {
	t := strings.TrimSpace(text)
	if t == "" {
		return
	}
	if strings.TrimSpace(role) == "" {
		role = "result"
	}
	label := strings.TrimSpace(name)
	if label != "" {
		t = label + ": " + t
	}
	a.messages = append(a.messages, UIMessage{
		Role:     role,
		Kind:     KindToolResult,
		Text:     t,
		ToolName: strings.TrimSpace(name),
	})
}

func canCoalesce(role string, last UIMessage) bool {
	return last.Role == role && (last.Kind == KindText || last.Kind == KindThinking) && (role == "assistant" || role == "think")
}

func kindForRole(role string) MessageKind {
	if role == "think" {
		return KindThinking
	}
	return KindText
}
