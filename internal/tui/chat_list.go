package tui

import (
	"strings"

	xansi "github.com/charmbracelet/x/ansi"

	tuirender "github.com/usewhale/whale/internal/tui/render"
)

const chatListGap = 1

type chatList struct {
	width      int
	height     int
	items      []chatItem
	offsetIdx  int
	offsetLine int
	generation uint64
}

type chatItem struct {
	msg   tuirender.UIMessage
	lines []string
}

func newChatList() chatList {
	return chatList{}
}

func (l *chatList) SetSize(width, height int) {
	l.width = max(20, width)
	l.height = max(0, height)
	l.clampOffset()
}

func (l *chatList) SetMessages(messages []tuirender.UIMessage, renderWidth int) {
	items := make([]chatItem, 0, len(messages))
	pendingWorkSeparator := false
	for _, msg := range messages {
		lines := renderChatItemLines(msg, renderWidth)
		if len(lines) == 0 {
			continue
		}
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
	l.generation++
	if len(l.items) == 0 {
		l.offsetIdx = 0
		l.offsetLine = 0
		return
	}
	l.clampOffset()
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

func (l *chatList) View() string {
	if len(l.items) == 0 || l.height <= 0 {
		return ""
	}
	lines := make([]string, 0, l.height)
	idx := l.offsetIdx
	offset := l.offsetLine
	for len(lines) < l.height && idx < len(l.items) {
		itemLines := l.items[idx].lines
		if offset < len(itemLines) {
			lines = append(lines, itemLines[offset:]...)
			if idx < len(l.items)-1 {
				for range chatListGap {
					lines = append(lines, "")
				}
			}
		} else {
			gapOffset := offset - len(itemLines)
			for i := gapOffset; i < chatListGap; i++ {
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
	for i, item := range l.items {
		out = append(out, item.lines...)
		if i < len(l.items)-1 {
			for range chatListGap {
				out = append(out, "")
			}
		}
	}
	return strings.Join(out, "\n")
}

func (l *chatList) TotalLineCount() int {
	total := 0
	for i, item := range l.items {
		total += len(item.lines)
		if i < len(l.items)-1 {
			total += chatListGap
		}
	}
	return total
}

func (l *chatList) HiddenLineCount() int {
	total := 0
	for i := 0; i < l.offsetIdx && i < len(l.items); i++ {
		total += len(l.items[i].lines)
		if i < len(l.items)-1 {
			total += chatListGap
		}
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
		itemHeight := len(l.items[i].lines)
		if i < len(l.items)-1 {
			itemHeight += chatListGap
		}
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
			currentHeight := len(l.items[l.offsetIdx].lines)
			if l.offsetIdx < len(l.items)-1 {
				currentHeight += chatListGap
			}
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
		prevHeight := len(l.items[l.offsetIdx].lines)
		if l.offsetIdx < len(l.items)-1 {
			prevHeight += chatListGap
		}
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
		itemHeight := len(l.items[idx].lines)
		if idx < len(l.items)-1 {
			itemHeight += chatListGap
		}
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
