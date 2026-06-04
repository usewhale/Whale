package tui

import (
	"strings"

	xansi "github.com/charmbracelet/x/ansi"

	tuirender "github.com/usewhale/whale/internal/tui/render"
)

const chatListGap = 1

type chatList struct {
	width       int
	height      int
	items       []chatItem
	leadingGap  int
	offsetIdx   int
	offsetLine  int
	generation  uint64
	renderCache map[chatItemRenderKey][]string
}

type chatItem struct {
	msg   tuirender.UIMessage
	lines []string
}

type chatItemRenderKey struct {
	msgID       string
	msgKind     tuirender.MessageKind
	msgRole     string
	msgText     string
	msgToolName string
	streaming   bool
	fullReason  bool
	renderWidth int
}

func makeRenderKey(msg tuirender.UIMessage, renderWidth int) chatItemRenderKey {
	return chatItemRenderKey{
		msgID:       msg.ID,
		msgKind:     msg.Kind,
		msgRole:     msg.Role,
		msgText:     msg.Text,
		msgToolName: msg.ToolName,
		streaming:   msg.Streaming,
		fullReason:  msg.FullReasoning,
		renderWidth: renderWidth,
	}
}

func newChatList() chatList {
	return chatList{}
}

func (l *chatList) SetSize(width, height int) {
	l.width = max(20, width)
	l.height = max(0, height)
	l.clampOffset()
}

// SetMessages resets leadingGap to 0. Callers that need a leading gap must
// call SetLeadingGap after SetMessages.
func (l *chatList) SetMessages(messages []tuirender.UIMessage, renderWidth int) {
	items := make([]chatItem, 0, len(messages))
	nextCache := make(map[chatItemRenderKey][]string, len(messages))
	pendingWorkSeparator := false
	for _, msg := range messages {
		key := makeRenderKey(msg, renderWidth)
		baseLines, ok := l.renderCache[key]
		if !ok {
			baseLines = renderChatItemLines(msg, renderWidth)
		}
		if len(baseLines) == 0 {
			continue
		}
		nextCache[key] = baseLines
		lines := baseLines
		if pendingWorkSeparator && tuirender.NeedsWorkSeparatorBefore(msg) {
			lines = append([]string{tuirender.WorkSeparator(max(20, renderWidth)), ""}, lines...)
			pendingWorkSeparator = false
		}
		items = append(items, chatItem{msg: msg, lines: lines})
		switch {
		case tuirender.IsWorkEvent(msg):
			pendingWorkSeparator = true
		case msg.Role == "you" || tuirender.NeedsWorkSeparatorBefore(msg):
			pendingWorkSeparator = false
		}
	}
	l.items = items
	l.leadingGap = 0
	l.renderCache = nextCache
	l.generation++
	if len(l.items) == 0 {
		l.offsetIdx = 0
		l.offsetLine = 0
		return
	}
	l.clampOffset()
}

func (l *chatList) SetLeadingGap(gap int) {
	l.leadingGap = max(0, gap)
	l.clampOffset()
}

// measureLines returns the rendered line count for msg at renderWidth,
// reusing renderCache so a subsequent SetMessages call for the same
// (msg, renderWidth) skips re-rendering.
func (l *chatList) measureLines(msg tuirender.UIMessage, renderWidth int) int {
	key := makeRenderKey(msg, renderWidth)
	if lines, ok := l.renderCache[key]; ok {
		return len(lines)
	}
	lines := renderChatItemLines(msg, renderWidth)
	if l.renderCache == nil {
		l.renderCache = make(map[chatItemRenderKey][]string)
	}
	l.renderCache[key] = lines
	return len(lines)
}

func renderChatItemLines(msg tuirender.UIMessage, width int) []string {
	lines := tuirender.ChatLines([]tuirender.UIMessage{msg}, max(20, width))
	for len(lines) > 0 && isPlainBlankLine(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func isPlainBlankLine(line string) bool {
	if strings.TrimSpace(xansi.Strip(line)) != "" {
		return false
	}
	return line == xansi.Strip(line)
}

func chatListGapAfter(prev, next tuirender.UIMessage) int {
	if prev.Role == "assistant" && prev.Kind == tuirender.KindText && next.Role == "you" {
		return 2
	}
	return chatListGap
}

func chatListGapAfterItems(items []chatItem, idx int) int {
	if idx < 0 || idx >= len(items)-1 {
		return 0
	}
	return chatListGapAfter(items[idx].msg, items[idx+1].msg)
}

func (l chatList) itemHeight(idx int) int {
	if idx < 0 || idx >= len(l.items) {
		return 0
	}
	return l.leadingGapBeforeItem(idx) + len(l.items[idx].lines) + chatListGapAfterItems(l.items, idx)
}

func (l chatList) leadingGapBeforeItem(idx int) int {
	if idx == 0 {
		return l.leadingGap
	}
	return 0
}

func chatListRenderedLineCount(messages []tuirender.UIMessage, renderWidth int) int {
	var l chatList
	l.SetMessages(messages, renderWidth)
	return l.TotalLineCount()
}

func chatListRenderedLineCountWithLeadingGap(messages []tuirender.UIMessage, renderWidth, leadingGap int) int {
	var l chatList
	l.SetMessages(messages, renderWidth)
	l.SetLeadingGap(leadingGap)
	return l.TotalLineCount()
}

func (l *chatList) View() string {
	if len(l.items) == 0 || l.height <= 0 {
		return ""
	}
	lines := make([]string, 0, l.height)
	idx := l.offsetIdx
	offset := l.offsetLine
	for len(lines) < l.height && idx < len(l.items) {
		itemLines := l.items[idx].lines
		leadingGap := l.leadingGapBeforeItem(idx)
		if offset < leadingGap {
			for i := offset; i < leadingGap; i++ {
				lines = append(lines, "")
			}
			lines = append(lines, itemLines...)
			for range chatListGapAfterItems(l.items, idx) {
				lines = append(lines, "")
			}
		} else if itemOffset := offset - leadingGap; itemOffset < len(itemLines) {
			lines = append(lines, itemLines[itemOffset:]...)
			for range chatListGapAfterItems(l.items, idx) {
				lines = append(lines, "")
			}
		} else {
			gapOffset := offset - leadingGap - len(itemLines)
			for i := gapOffset; i < chatListGapAfterItems(l.items, idx); i++ {
				lines = append(lines, "")
			}
		}
		idx++
		offset = 0
	}
	if len(lines) > l.height {
		lines = lines[:l.height]
	}
	return strings.Join(lines, "\n")
}

func (l *chatList) FullContent() string {
	if len(l.items) == 0 {
		return ""
	}
	out := make([]string, 0, l.TotalLineCount())
	for range l.leadingGap {
		out = append(out, "")
	}
	for i, item := range l.items {
		out = append(out, item.lines...)
		for range chatListGapAfterItems(l.items, i) {
			out = append(out, "")
		}
	}
	return strings.Join(out, "\n")
}

func (l *chatList) TotalLineCount() int {
	total := 0
	for i, item := range l.items {
		total += l.leadingGapBeforeItem(i)
		total += len(item.lines)
		total += chatListGapAfterItems(l.items, i)
	}
	return total
}

func (l *chatList) HiddenLineCount() int {
	total := 0
	for i := 0; i < l.offsetIdx && i < len(l.items); i++ {
		total += l.itemHeight(i)
	}
	return total + l.offsetLine
}

func (l *chatList) AtBottom() bool {
	if len(l.items) == 0 {
		return true
	}
	return l.HiddenLineCount()+l.height >= l.TotalLineCount()
}

func (l *chatList) ScrollToBottom() {
	if len(l.items) == 0 {
		return
	}
	total := 0
	for i := len(l.items) - 1; i >= 0; i-- {
		itemHeight := l.itemHeight(i)
		total += itemHeight
		if total > l.height {
			l.offsetIdx = i
			l.offsetLine = max(0, total-l.height)
			l.clampOffset()
			return
		}
	}
	l.offsetIdx = 0
	l.offsetLine = 0
}

func (l *chatList) ScrollToTop() {
	l.offsetIdx = 0
	l.offsetLine = 0
}

func (l *chatList) ScrollBy(lines int) {
	if len(l.items) == 0 || lines == 0 {
		return
	}
	if lines > 0 {
		if l.AtBottom() {
			return
		}
		l.offsetLine += lines
		for l.offsetIdx < len(l.items) {
			currentHeight := l.itemHeight(l.offsetIdx)
			if l.offsetLine < currentHeight {
				break
			}
			l.offsetLine -= currentHeight
			l.offsetIdx++
		}
		l.clampOffset()
		return
	}
	l.offsetLine += lines
	for l.offsetLine < 0 {
		l.offsetIdx--
		if l.offsetIdx < 0 {
			l.ScrollToTop()
			return
		}
		prevHeight := l.itemHeight(l.offsetIdx)
		l.offsetLine += prevHeight
	}
}

func (l *chatList) PageUp() {
	l.ScrollBy(-max(1, l.height))
}

func (l *chatList) PageDown() {
	l.ScrollBy(max(1, l.height))
}

func (l *chatList) HalfPageUp() {
	l.ScrollBy(-max(1, l.height/2))
}

func (l *chatList) HalfPageDown() {
	l.ScrollBy(max(1, l.height/2))
}

func (l *chatList) clampOffset() {
	if len(l.items) == 0 {
		l.offsetIdx = 0
		l.offsetLine = 0
		return
	}
	if l.offsetIdx < 0 {
		l.offsetIdx = 0
	}
	if l.offsetIdx >= len(l.items) {
		l.ScrollToBottom()
		return
	}
	maxHidden := max(0, l.TotalLineCount()-l.height)
	if l.HiddenLineCount() > maxHidden {
		l.setHiddenLineCount(maxHidden)
	}
}

func (l *chatList) setHiddenLineCount(hidden int) {
	if hidden <= 0 || len(l.items) == 0 {
		l.offsetIdx = 0
		l.offsetLine = 0
		return
	}
	idx := 0
	for idx < len(l.items) {
		itemHeight := l.itemHeight(idx)
		if hidden < itemHeight {
			l.offsetIdx = idx
			l.offsetLine = hidden
			return
		}
		hidden -= itemHeight
		idx++
	}
	l.offsetIdx = len(l.items) - 1
	l.offsetLine = max(0, len(l.items[l.offsetIdx].lines)-1)
}
