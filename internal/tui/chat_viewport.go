package tui

import (
	"strings"

	tuirender "github.com/usewhale/whale/internal/tui/render"
)

const (
	chatTailRenderMessageLimit = 80
	chatTailRenderLineFloor    = 80
	chatTailElision            = "...\n"
)

func (m *model) refreshViewportContent() {
	mainWidth, _ := m.layoutDims()
	bodyHeight := m.viewportBodyHeight(mainWidth)
	m.refreshViewportContentForSize(mainWidth, m.chatViewportBodyHeight(mainWidth, bodyHeight), false)
}

func (m *model) refreshViewportContentFollow(forceBottom bool) {
	mainWidth, _ := m.layoutDims()
	bodyHeight := m.viewportBodyHeight(mainWidth)
	m.refreshViewportContentForSize(mainWidth, m.chatViewportBodyHeight(mainWidth, bodyHeight), forceBottom)
}

func (m *model) refreshLiveViewportContent() {
	if m.viewportFrozen {
		return
	}
	m.refreshViewportContentFollow(false)
}

func (m *model) refreshViewportContentIfBodyHeightChanged(prevMainWidth, prevBodyHeight int) {
	mainWidth, _ := m.layoutDims()
	bodyHeight := m.viewportBodyHeight(mainWidth)
	contentHeight := m.chatViewportBodyHeight(mainWidth, bodyHeight)
	prevContentHeight := m.chatViewportBodyHeight(prevMainWidth, prevBodyHeight)
	if !m.viewportLayoutReady || m.viewportLayoutPage != m.page || mainWidth != prevMainWidth {
		m.refreshViewportContentForSize(mainWidth, contentHeight, false)
		return
	}
	if contentHeight != prevContentHeight {
		m.syncViewportLayoutForBodyHeight(mainWidth, contentHeight)
	}
}

func (m *model) ensureViewportContentForSize(mainWidth, bodyHeight int) {
	if m.viewportLayoutReady &&
		m.viewportLayoutPage == m.page &&
		m.viewportLayoutWidth == mainWidth &&
		m.viewportLayoutHeight == bodyHeight {
		return
	}
	m.refreshViewportContentForSize(mainWidth, bodyHeight, false)
}

func (m *model) syncViewportLayoutForBodyHeight(mainWidth, bodyHeight int) {
	if m.page == pageChat {
		m.chat.SetSize(max(10, mainWidth), max(1, bodyHeight))
		if !m.viewportFrozen && m.followTail {
			m.chat.ScrollToBottom()
		}
		m.syncViewportFromChat()
	} else {
		m.viewport.Height = max(1, bodyHeight-2)
	}
	m.viewportLayoutReady = true
	m.viewportLayoutPage = m.page
	m.viewportLayoutWidth = mainWidth
	m.viewportLayoutHeight = bodyHeight
}

func (m *model) refreshViewportContentForSize(mainWidth, bodyHeight int, forceBottom bool) {
	content := ""
	if m.page == pageChat {
		if forceBottom {
			m.unfreezeChatViewport()
			m.followTail = true
		}
		m.chat.SetSize(max(10, mainWidth), max(1, bodyHeight))
		renderWidth := max(20, mainWidth-2)
		messages := m.chatMessages()
		if m.viewportFrozen {
			messages = m.frozenChatMessages
		} else if m.shouldRenderChatTailOnly(forceBottom) {
			messages = m.chatTailMessagesForView(messages, renderWidth, bodyHeight)
		}
		m.chat.SetMessages(messages, renderWidth)
		if forceBottom || m.followTail {
			m.chat.ScrollToBottom()
		}
		m.syncViewportFromChat()
		m.viewportLayoutReady = true
		m.viewportLayoutPage = m.page
		m.viewportLayoutWidth = mainWidth
		m.viewportLayoutHeight = bodyHeight
		return
	}
	m.viewport.Width = max(10, mainWidth-2)
	m.viewport.Height = max(1, bodyHeight-2)
	if m.page == pageLogs {
		content = strings.Join(m.filteredLogs(), "\n")
	}
	if m.page == pageDiff {
		content = strings.Join(m.renderDiffs(), "\n")
	}
	m.viewport.SetContent(content)
	m.viewportLayoutReady = true
	m.viewportLayoutPage = m.page
	m.viewportLayoutWidth = mainWidth
	m.viewportLayoutHeight = bodyHeight
}

func (m *model) shouldRenderChatTailOnly(forceBottom bool) bool {
	return m.page == pageChat && m.followTail && !m.viewportFrozen && !forceBottom
}

func (m *model) freezeChatViewport() {
	if m.page != pageChat || m.viewportFrozen {
		return
	}
	mainWidth, _ := m.layoutDims()
	bodyHeight := m.viewportBodyHeight(mainWidth)
	chatHeight := m.chatViewportBodyHeight(mainWidth, bodyHeight)
	m.chat.SetSize(max(10, mainWidth), max(1, chatHeight))
	m.frozenChatMessages = append([]tuirender.UIMessage(nil), m.chatMessages()...)
	m.chat.SetMessages(m.frozenChatMessages, max(20, mainWidth-2))
	if m.followTail {
		m.chat.ScrollToBottom()
	}
	m.syncViewportFromChat()
	m.viewportFrozen = true
}

func (m *model) unfreezeChatViewport() {
	m.viewportFrozen = false
	m.frozenChatMessages = nil
}

func (m *model) syncViewportFromChat() {
	m.viewport.Width = max(10, m.chat.width)
	m.viewport.Height = max(1, m.chat.height)
	m.viewport.SetContent(m.chat.FullContent())
	m.viewport.YOffset = m.chat.HiddenLineCount()
}

func (m model) renderChatLines(width int) []string {
	messages := m.chatMessages()
	if len(messages) == 0 {
		return nil
	}
	return tuirender.ChatLines(messages, width)
}

func (m model) scrollbackText(messages []tuirender.UIMessage) string {
	lines := tuirender.ChatLines(m.focusMessages(messages), m.chatRenderWidth())
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func (m model) chatMessages() []tuirender.UIMessage {
	live := []tuirender.UIMessage(nil)
	if m.assembler != nil {
		live = m.assembler.Snapshot()
	}
	if len(m.transcript) == 0 && len(live) == 0 && len(m.ephemeralMessages) == 0 {
		return nil
	}
	out := make([]tuirender.UIMessage, 0, len(m.transcript)+len(live)+len(m.ephemeralMessages))
	out = append(out, m.transcript...)
	out = append(out, live...)
	out = append(out, m.ephemeralMessages...)
	return m.focusMessages(out)
}

func (m model) chatContent(width int) string {
	lines := tuirender.ChatLines(m.chatMessages(), max(20, width-2))
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func (m model) chatTailContent(width, height int) string {
	messages := m.chatMessages()
	renderWidth := max(20, width-2)
	messages = m.chatTailMessagesForView(messages, renderWidth, height)
	lines := tuirender.ChatLines(messages, renderWidth)
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func (m model) chatTailMessagesForView(messages []tuirender.UIMessage, renderWidth, height int) []tuirender.UIMessage {
	if len(messages) > chatTailRenderMessageLimit {
		messages = messages[len(messages)-chatTailRenderMessageLimit:]
	}
	lineLimit := max(chatTailRenderLineFloor, max(1, height)*4)
	for len(messages) > 1 {
		if len(tuirender.ChatLines(messages, renderWidth)) <= lineLimit {
			return messages
		}
		messages = messages[1:]
	}
	if len(messages) == 1 {
		return []tuirender.UIMessage{tailMessageForLineLimit(messages[0], renderWidth, lineLimit)}
	}
	return messages
}

func tailMessageForLineLimit(msg tuirender.UIMessage, renderWidth, lineLimit int) tuirender.UIMessage {
	if lineLimit <= 0 || strings.TrimSpace(msg.Text) == "" {
		return msg
	}
	msg.Text = tailTextForRender(msg.Text, renderWidth, lineLimit)
	for {
		lines := renderChatItemLines(msg, renderWidth)
		if len(lines) <= lineLimit {
			break
		}
		next := trimLeadingTailText(msg.Text, renderWidth, len(lines)-lineLimit+1)
		if next == msg.Text {
			break
		}
		msg.Text = next
	}
	return msg
}

func tailTextForRender(text string, renderWidth, lineLimit int) string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return text
	}
	runes := []rune(text)
	contentWidth := max(16, renderWidth-6)
	maxRunes := max(256, contentWidth*lineLimit)
	if len(runes) <= maxRunes {
		return text
	}
	return chatTailElision + strings.TrimLeft(string(runes[len(runes)-maxRunes:]), "\n")
}

func trimLeadingTailText(text string, renderWidth, linesToDrop int) string {
	base := strings.TrimPrefix(text, chatTailElision)
	runes := []rune(base)
	if len(runes) == 0 {
		return text
	}
	contentWidth := max(16, renderWidth-6)
	drop := max(1, contentWidth*max(1, linesToDrop))
	if drop >= len(runes) {
		drop = max(1, len(runes)/2)
	}
	return chatTailElision + strings.TrimLeft(string(runes[drop:]), "\n")
}
