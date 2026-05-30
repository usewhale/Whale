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
	ID            string
	Role          string
	Kind          MessageKind
	Text          string
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

type Assembler struct {
	messages      []UIMessage
	toolEntryByID map[string]int
}

func NewAssembler() *Assembler {
	return &Assembler{toolEntryByID: map[string]int{}}
}

func (a *Assembler) Reset() {
	a.messages = nil
	a.toolEntryByID = map[string]int{}
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
	a.rebuildToolEntryIndex()
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
	a.rebuildToolEntryIndex()
	return true
}

func (a *Assembler) Snapshot() []UIMessage {
	out := make([]UIMessage, len(a.messages))
	copy(out, a.messages)
	return out
}

func (a *Assembler) AppendDelta(role, text string) {
	t := strings.ReplaceAll(text, "\r\n", "\n")
	if t == "" {
		return
	}
	if n := len(a.messages); n > 0 && canCoalesce(role, a.messages[n-1]) {
		a.messages[n-1].Text += t
		if role == "think" {
			a.messages[n-1].Streaming = true
		}
		return
	}
	if strings.TrimSpace(t) == "" {
		return
	}
	a.messages = append(a.messages, UIMessage{
		Role:      role,
		Kind:      kindForRole(role),
		Text:      t,
		Streaming: role == "think",
	})
}

func (a *Assembler) AddToolCall(toolCallID, toolName, text string) {
	t := strings.TrimSpace(strings.TrimRight(text, "\n"))
	if t == "" {
		return
	}
	a.messages = append(a.messages, UIMessage{
		ID:       toolCallID,
		Role:     "tool",
		Kind:     KindToolCall,
		Text:     t,
		ToolName: strings.TrimSpace(toolName),
	})
	if toolCallID != "" {
		a.toolEntryByID[toolCallID] = len(a.messages) - 1
	}
}

func (a *Assembler) AddSubagent(toolCallID, text string) {
	a.AddSubagentWithSteps(toolCallID, text, nil)
}

func (a *Assembler) AddSubagentWithSteps(toolCallID, text string, steps []protocol.ProgressStep) {
	t := strings.TrimSpace(strings.TrimRight(text, "\n"))
	if t == "" {
		return
	}
	a.messages = append(a.messages, UIMessage{
		ID:            toolCallID,
		Role:          "tool",
		Kind:          KindSubagent,
		Text:          t,
		ToolName:      "spawn_subagent",
		SubagentSteps: steps,
	})
	if toolCallID != "" {
		a.toolEntryByID[toolCallID] = len(a.messages) - 1
	}
}

func (a *Assembler) UpdateSubagentProgress(toolCallID, text string, role string, steps []protocol.ProgressStep) {
	if toolCallID == "" {
		return
	}
	idx, ok := a.toolEntryByID[toolCallID]
	if !ok || idx < 0 || idx >= len(a.messages) {
		return
	}
	a.messages[idx].Text = text
	a.messages[idx].Role = role
	if len(steps) > 0 {
		a.messages[idx].SubagentSteps = steps
	}
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

func (a *Assembler) UpdateToolCall(toolCallID, text, role string) bool {
	return a.UpdateToolCallWithIdentity(toolCallID, text, role, "")
}

func (a *Assembler) UpdateToolCallWithIdentity(toolCallID, text, role, identity string) bool {
	t := strings.TrimSpace(strings.TrimRight(text, "\n"))
	if toolCallID == "" || t == "" {
		return false
	}
	idx, ok := a.toolEntryByID[toolCallID]
	if !ok || idx < 0 || idx >= len(a.messages) {
		return false
	}
	if strings.TrimSpace(role) == "" {
		role = "tool"
	}
	a.messages[idx].Role = role
	a.messages[idx].Text = t
	if strings.TrimSpace(identity) != "" {
		a.messages[idx].ToolIdentity = identity
	}
	return true
}

func (a *Assembler) ToolCallText(toolCallID string) string {
	if toolCallID == "" {
		return ""
	}
	idx, ok := a.toolEntryByID[toolCallID]
	if !ok || idx < 0 || idx >= len(a.messages) {
		return ""
	}
	return a.messages[idx].Text
}

func (a *Assembler) RemoveToolCall(toolCallID string) bool {
	if toolCallID == "" {
		return false
	}
	idx, ok := a.toolEntryByID[toolCallID]
	if !ok || idx < 0 || idx >= len(a.messages) {
		return false
	}
	a.messages = append(a.messages[:idx], a.messages[idx+1:]...)
	a.rebuildToolEntryIndex()
	return true
}

func (a *Assembler) AddPlanDelta(text string) {
	t := strings.ReplaceAll(text, "\r\n", "\n")
	if t == "" {
		return
	}
	if n := len(a.messages); n > 0 && a.messages[n-1].Kind == KindPlan {
		a.messages[n-1].Text += t
		return
	}
	if strings.TrimSpace(t) == "" {
		return
	}
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
	replaced := false
	out := a.messages[:0]
	for _, msg := range a.messages {
		if msg.Kind == KindPlan {
			if !replaced {
				msg.Text = text
				out = append(out, msg)
				replaced = true
			}
			continue
		}
		out = append(out, msg)
	}
	a.messages = out
	if replaced {
		a.rebuildToolEntryIndex()
	}
	return replaced
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

func (a *Assembler) BackfillToolCall(toolCallID, replacement string) {
	if toolCallID == "" || replacement == "" {
		return
	}
	idx, ok := a.toolEntryByID[toolCallID]
	if !ok || idx < 0 || idx >= len(a.messages) {
		return
	}
	a.messages[idx].Text = replacement
}

func (a *Assembler) rebuildToolEntryIndex() {
	a.toolEntryByID = map[string]int{}
	for i, msg := range a.messages {
		if msg.ID != "" && isIndexableToolMessage(msg.Kind) {
			a.toolEntryByID[msg.ID] = i
		}
	}
}

func isIndexableToolMessage(kind MessageKind) bool {
	return kind == KindToolCall || kind == KindSubagent
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
