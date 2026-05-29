package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	windowsPasteEnterDelay = 300 * time.Millisecond
	// Time-until-flush after the last paste chunk arrives. Has to be
	// long enough to bridge intra-paste gaps in slow conhost delivery
	// but short enough that the user does not perceive the buffered
	// portion as a separate, late-arriving second insert. Sits one tier
	// above the 60ms cadence window so a single paste cannot be split,
	// and close to the ~100ms human "instant" perception threshold.
	// Codex / DeepSeek-TUI ship 60ms on Windows; this is the safer
	// neighbor that still feels snappy.
	windowsPasteQuietDelay         = 80 * time.Millisecond
	windowsPasteContinuationWindow = 30 * time.Millisecond
)

type windowsDeferredEnterMsg struct {
	id          int
	wasBusy     bool
	wasStopping bool
}

type windowsPasteBurstFlushMsg struct {
	id int
}

type windowsPendingEnterTailMsg struct {
	id int
}

type windowsPasteFallbackState struct {
	enabled             bool
	pendingEnterID      int
	pendingEnter        bool
	pendingEnterBusy    bool
	pendingEnterStop    bool
	pendingEnterTailID  int
	pendingEnterTail    string
	burstID             int
	burstFlushScheduled bool
	bufferChunks        []string
	bufferLen           int
	activeUntil         time.Time
	busyInput           bool
	busyInputStop       bool
	bracketedThisInput  bool
	suppressNextCtrlJ   bool
	classifier          windowsPasteBurstClassifier
	// nowFunc lets tests inject a deterministic clock so cadence-based
	// classification can be exercised without real time delays.
	nowFunc func() time.Time
}

func (s *windowsPasteFallbackState) now() time.Time {
	if s.nowFunc != nil {
		return s.nowFunc()
	}
	return time.Now()
}

func (m *model) handleWindowsDeferredEnter(msg windowsDeferredEnterMsg) tea.Cmd {
	if !m.windowsPasteFallbackEnabled() || !m.windowsPaste.pendingEnter || msg.id != m.windowsPaste.pendingEnterID {
		return nil
	}
	tail := m.consumePendingEnterTail()
	submitCmd := m.resolvePendingEnterAsSubmit()
	if tail != "" {
		m.input.HandlePaste(tail)
		m.resetHistoryNavigation()
		suggestionCmd := m.updateSlashMatches()
		m.refreshViewportContent()
		return tea.Sequence(submitCmd, suggestionCmd)
	}
	return submitCmd
}

func (m *model) resolvePendingEnterAsSubmit() tea.Cmd {
	if !m.windowsPaste.pendingEnter {
		return nil
	}
	wasBusy := m.windowsPaste.pendingEnterBusy
	wasStopping := m.windowsPaste.pendingEnterStop
	m.clearWindowsDeferredEnter()
	value := strings.TrimSpace(m.input.Value())
	if value == "" {
		return nil
	}
	if m.busy {
		return tea.Sequence(m.flushNativeScrollbackCmd(), m.submitPromptFromDeferredBusyEnter(value, wasStopping))
	}
	if wasBusy && wasStopping {
		return nil
	}
	if m.localSubmitPending > 0 {
		m.status = "wait for command to finish"
		m.refreshViewportContent()
		return m.flushNativeScrollbackCmd()
	}
	return tea.Sequence(m.flushNativeScrollbackCmd(), m.submitPrompt(value))
}

func (m *model) handleWindowsPasteFallbackKey(msg tea.KeyMsg) (cmd tea.Cmd, handled bool) {
	if !m.windowsPasteFallbackEnabled() {
		return nil, false
	}
	now := m.windowsPaste.now()
	// Editing keys (Enter, Tab, arrows, backspace, …) arrive with no
	// rune payload. They must segment paste-cadence detection so a slow
	// typist who hits Enter mid-edit doesn't have their next keystroke
	// folded into a phantom burst.
	if len(msg.Runes) == 0 {
		m.windowsPaste.classifier.reset()
	}
	var pendingFlushCmd tea.Cmd
	if m.hasWindowsPasteBuffer() && !m.windowsPaste.activeUntil.IsZero() && now.After(m.windowsPaste.activeUntil) {
		pendingFlushCmd = m.flushWindowsPasteBurstToComposer()
	}
	defer func() {
		if pendingFlushCmd != nil {
			cmd = tea.Batch(pendingFlushCmd, cmd)
		}
	}()
	if msg.String() == "enter" {
		switch {
		case m.hasWindowsPasteBuffer():
			if !m.windowsPasteBufferHasLineBreak() {
				return m.deferWindowsSingleLinePasteSubmit(), true
			}
			return m.appendWindowsPasteBurst(now, "\n", true), true
		case m.windowsPaste.pendingEnter:
			suffix := "\n" + m.consumePendingEnterTail() + "\n"
			return m.startWindowsPasteBurstFromComposer(now, suffix, true), true
		case m.shouldDeferWindowsEnterSubmit():
			return m.deferWindowsEnterSubmit(), true
		}
	}
	if msg.String() == "ctrl+j" && (m.windowsPaste.pendingEnter || m.hasWindowsPasteBuffer()) {
		if m.windowsPaste.suppressNextCtrlJ {
			m.windowsPaste.suppressNextCtrlJ = false
			if m.hasWindowsPasteBuffer() {
				return m.scheduleWindowsPasteBurstFlush(now), true
			}
			return nil, true
		}
		if m.hasWindowsPasteBuffer() {
			return m.appendWindowsPasteBurst(now, "\n", false), true
		}
		suffix := "\n"
		if tail := m.consumePendingEnterTail(); tail != "" {
			suffix = "\n" + tail + "\n"
		}
		return m.startWindowsPasteBurstFromComposer(now, suffix, false), true
	}
	if msg.String() == "tab" && !m.hasSlashSuggestions() && !m.hasFilePanel() && !m.hasSkillSuggestions() {
		if m.windowsPaste.pendingEnter || m.hasWindowsPasteBuffer() {
			return m.appendWindowsPasteFallbackText(now, "    "), true
		}
		cmd := m.insertWindowsPasteFallbackInactiveText("    ")
		m.refreshViewportContent()
		return cmd, true
	}
	if len(msg.Runes) > 0 {
		text := string(msg.Runes)
		// classify() is stateful: it records arrival time so the next chunk
		// can detect terminal-streamed paste cadence even when both chunks
		// look like ordinary typing in isolation.
		decision := m.windowsPaste.classifier.classify(now, text)
		if m.hasWindowsPasteBuffer() {
			return m.appendWindowsPasteBurst(now, text, false), true
		}
		if m.windowsPaste.pendingEnter {
			tailHeld := m.windowsPaste.pendingEnterTail != ""
			if tailHeld || decision == windowsPasteChunkBurst || isASCIIMultiRune(text) {
				suffix := "\n" + m.consumePendingEnterTail() + text
				return m.startWindowsPasteBurstFromComposer(now, suffix, false), true
			}
			return m.deferWindowsPendingEnterTail(text), true
		}
		if decision == windowsPasteChunkBurst {
			return m.startWindowsPasteBurst(now, text, false), true
		}
		m.markWindowsBusyInput(m.busy, m.stopping)
		return nil, false
	}
	if m.windowsPaste.pendingEnter && m.shouldCancelWindowsDeferredEnterForKey(msg) {
		m.cancelWindowsDeferredEnter()
	}
	if m.hasWindowsPasteBuffer() {
		return m.flushWindowsPasteBurstToComposer(), false
	}
	return nil, false
}

func (m *model) shouldDeferWindowsEnterSubmit() bool {
	if !m.windowsPasteFallbackEnabled() || m.mode != modeChat || m.windowsPaste.bracketedThisInput {
		return false
	}
	if m.hasSlashSuggestions() || m.hasFilePanel() || m.hasSkillSuggestions() {
		return false
	}
	if m.localSubmitPending > 0 {
		return false
	}
	if m.page == pageLogs && m.logFilterInput.Focused() {
		return false
	}
	raw := m.input.Value()
	if strings.HasSuffix(raw, "\\") {
		return false
	}
	return strings.TrimSpace(raw) != ""
}

func (m *model) shouldCancelWindowsDeferredEnterForKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "esc":
		return !m.busy
	case "enter", "ctrl+j", "tab":
		return false
	}
	return true
}

func (m *model) deferWindowsEnterSubmit() tea.Cmd {
	return m.deferWindowsEnterSubmitAfter(windowsPasteEnterDelay)
}

func (m *model) deferWindowsEnterSubmitAfter(delay time.Duration) tea.Cmd {
	m.windowsPaste.pendingEnterID++
	id := m.windowsPaste.pendingEnterID
	m.windowsPaste.pendingEnter = true
	m.windowsPaste.pendingEnterBusy = m.busy
	m.windowsPaste.pendingEnterStop = m.stopping
	m.windowsPaste.pendingEnterTail = ""
	m.markWindowsBusyInput(m.busy, m.stopping)
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return windowsDeferredEnterMsg{
			id:          id,
			wasBusy:     m.windowsPaste.pendingEnterBusy,
			wasStopping: m.windowsPaste.pendingEnterStop,
		}
	})
}

func (m *model) appendWindowsPasteFallbackText(now time.Time, text string) tea.Cmd {
	if m.windowsPaste.pendingEnter {
		suffix := "\n" + m.consumePendingEnterTail() + text
		return m.startWindowsPasteBurstFromComposer(now, suffix, false)
	}
	return m.appendWindowsPasteBurst(now, text, false)
}

func (m *model) insertWindowsPasteFallbackInactiveText(text string) tea.Cmd {
	m.input.HandlePaste(text)
	m.resetHistoryNavigation()
	return m.updateSlashMatches()
}

func (m *model) cancelWindowsDeferredEnter() {
	m.clearWindowsDeferredEnter()
}

func (m *model) clearWindowsDeferredEnter() {
	m.windowsPaste.pendingEnter = false
	m.windowsPaste.pendingEnterBusy = false
	m.windowsPaste.pendingEnterStop = false
	m.windowsPaste.pendingEnterTail = ""
	m.windowsPaste.suppressNextCtrlJ = false
}

func (m *model) deferWindowsPendingEnterTail(text string) tea.Cmd {
	m.windowsPaste.pendingEnterTailID++
	id := m.windowsPaste.pendingEnterTailID
	m.windowsPaste.pendingEnterTail = text
	return tea.Tick(windowsPasteContinuationWindow, func(time.Time) tea.Msg {
		return windowsPendingEnterTailMsg{id: id}
	})
}

// consumePendingEnterTail returns and clears any rune parked in the 30 ms
// tail window, invalidating its in-flight tick so it becomes a no-op.
func (m *model) consumePendingEnterTail() string {
	tail := m.windowsPaste.pendingEnterTail
	if tail == "" {
		return ""
	}
	m.windowsPaste.pendingEnterTail = ""
	m.windowsPaste.pendingEnterTailID++
	return tail
}

func (m *model) handleWindowsPendingEnterTail(msg windowsPendingEnterTailMsg) tea.Cmd {
	if !m.windowsPasteFallbackEnabled() || !m.windowsPaste.pendingEnter || msg.id != m.windowsPaste.pendingEnterTailID || m.windowsPaste.pendingEnterTail == "" {
		return nil
	}
	tail := m.consumePendingEnterTail()
	submitCmd := m.resolvePendingEnterAsSubmit()
	m.input.HandlePaste(tail)
	m.resetHistoryNavigation()
	suggestionCmd := m.updateSlashMatches()
	m.refreshViewportContent()
	return tea.Sequence(submitCmd, suggestionCmd)
}

func (m model) hasWindowsPasteBuffer() bool {
	return m.windowsPaste.bufferLen > 0
}

func (m model) windowsPasteBufferHasLineBreak() bool {
	for _, chunk := range m.windowsPaste.bufferChunks {
		if strings.Contains(chunk, "\n") {
			return true
		}
	}
	return false
}

func (m model) windowsPasteBuffer() string {
	if m.windowsPaste.bufferLen == 0 {
		return ""
	}
	if len(m.windowsPaste.bufferChunks) == 1 {
		return m.windowsPaste.bufferChunks[0]
	}
	var b strings.Builder
	b.Grow(m.windowsPaste.bufferLen)
	for _, chunk := range m.windowsPaste.bufferChunks {
		b.WriteString(chunk)
	}
	return b.String()
}

func (m *model) setWindowsPasteBuffer(text string) {
	m.clearWindowsPasteBuffer()
	m.appendWindowsPasteBuffer(text)
}

func (m *model) appendWindowsPasteBuffer(text string) {
	if text == "" {
		return
	}
	m.windowsPaste.bufferChunks = append(m.windowsPaste.bufferChunks, text)
	m.windowsPaste.bufferLen += len(text)
}

func (m *model) clearWindowsPasteBuffer() {
	m.windowsPaste.bufferChunks = nil
	m.windowsPaste.bufferLen = 0
}

func (m *model) deferWindowsSingleLinePasteSubmit() tea.Cmd {
	flushCmd := m.flushWindowsPasteBurstToComposer()
	if m.localSubmitPending > 0 {
		m.status = "wait for command to finish"
		m.refreshViewportContent()
		return tea.Batch(flushCmd, m.flushNativeScrollbackCmd())
	}
	return tea.Batch(flushCmd, m.deferWindowsEnterSubmit())
}

func (m *model) startWindowsPasteBurstFromComposer(now time.Time, suffix string, suppressNextCtrlJ bool) tea.Cmd {
	prefix := m.input.Value()
	m.input.SetValue("")
	return m.startWindowsPasteBurst(now, prefix+suffix, suppressNextCtrlJ)
}

func (m *model) startWindowsPasteBurst(now time.Time, text string, suppressNextCtrlJ bool) tea.Cmd {
	m.setWindowsPasteBuffer(text)
	return m.afterWindowsPasteBurstChanged(now, suppressNextCtrlJ)
}

func (m *model) appendWindowsPasteBurst(now time.Time, text string, suppressNextCtrlJ bool) tea.Cmd {
	m.appendWindowsPasteBuffer(text)
	return m.afterWindowsPasteBurstChanged(now, suppressNextCtrlJ)
}

func (m *model) afterWindowsPasteBurstChanged(now time.Time, suppressNextCtrlJ bool) tea.Cmd {
	wasBusy := m.windowsPaste.pendingEnterBusy || m.windowsPaste.busyInput || m.busy
	wasStopping := m.windowsPaste.pendingEnterStop || m.windowsPaste.busyInputStop || m.stopping
	m.clearWindowsDeferredEnter()
	m.windowsPaste.bracketedThisInput = false
	m.windowsPaste.suppressNextCtrlJ = suppressNextCtrlJ
	m.markWindowsBusyInput(wasBusy, wasStopping)
	return m.scheduleWindowsPasteBurstFlush(now)
}

func (m *model) scheduleWindowsPasteBurstFlush(now time.Time) tea.Cmd {
	return m.scheduleWindowsPasteBurstFlushAfter(now, windowsPasteQuietDelay)
}

func (m *model) scheduleWindowsPasteBurstFlushAfter(now time.Time, delay time.Duration) tea.Cmd {
	m.windowsPaste.activeUntil = now.Add(delay)
	if m.windowsPaste.burstFlushScheduled {
		return nil
	}
	m.windowsPaste.burstID++
	id := m.windowsPaste.burstID
	m.windowsPaste.burstFlushScheduled = true
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return windowsPasteBurstFlushMsg{id: id}
	})
}

func (m *model) handleWindowsPasteBurstFlush(msg windowsPasteBurstFlushMsg) tea.Cmd {
	if !m.windowsPasteFallbackEnabled() || msg.id != m.windowsPaste.burstID {
		return nil
	}
	if !m.hasWindowsPasteBuffer() {
		m.windowsPaste.burstFlushScheduled = false
		return nil
	}
	if !m.windowsPaste.activeUntil.IsZero() {
		if remaining := time.Until(m.windowsPaste.activeUntil); remaining > 0 {
			return tea.Tick(remaining, func(time.Time) tea.Msg {
				return windowsPasteBurstFlushMsg{id: msg.id}
			})
		}
	}
	return m.flushWindowsPasteBurstToComposer()
}

func (m *model) flushWindowsPasteBurstToComposer() tea.Cmd {
	text := m.windowsPasteBuffer()
	if text == "" {
		return nil
	}
	m.clearWindowsPasteBuffer()
	m.windowsPaste.activeUntil = time.Time{}
	m.windowsPaste.burstFlushScheduled = false
	m.windowsPaste.suppressNextCtrlJ = false
	m.input.HandlePaste(text)
	m.resetHistoryNavigation()
	cmd := m.updateSlashMatches()
	m.refreshViewportContent()
	return cmd
}

func (m model) hasPendingWindowsBusyInput() bool {
	if !m.windowsPasteFallbackEnabled() {
		return false
	}
	if strings.TrimSpace(m.input.Value()) == "" && strings.TrimSpace(m.windowsPasteBuffer()) == "" {
		return false
	}
	return (m.windowsPaste.pendingEnter && m.windowsPaste.pendingEnterBusy) || m.windowsPaste.busyInput || m.hasWindowsPasteBuffer()
}

func (m *model) markWindowsBusyInput(wasBusy, wasStopping bool) {
	if !m.windowsPasteFallbackEnabled() {
		return
	}
	if wasBusy {
		m.windowsPaste.busyInput = true
	}
	if wasStopping {
		m.windowsPaste.busyInputStop = true
	}
}

func (m *model) markWindowsBusyInputStopped() {
	if !m.windowsPasteFallbackEnabled() {
		return
	}
	if m.windowsPaste.pendingEnter {
		m.windowsPaste.pendingEnterBusy = m.windowsPaste.pendingEnterBusy || m.busy
		m.windowsPaste.pendingEnterStop = true
	}
	if m.windowsPaste.busyInput || !m.windowsPaste.activeUntil.IsZero() {
		m.windowsPaste.busyInput = true
		m.windowsPaste.busyInputStop = true
	}
}

func (m *model) markWindowsPastedInput() {
	m.clearWindowsPasteBuffer()
	m.windowsPaste.bracketedThisInput = true
	m.windowsPaste.activeUntil = time.Time{}
	m.windowsPaste.burstFlushScheduled = false
	m.windowsPaste.suppressNextCtrlJ = false
	m.markWindowsBusyInput(m.busy, m.stopping)
}

func (m *model) resetWindowsPasteFallbackInputState() {
	m.clearWindowsDeferredEnter()
	m.clearWindowsPasteBuffer()
	m.windowsPaste.bracketedThisInput = false
	m.windowsPaste.activeUntil = time.Time{}
	m.windowsPaste.burstFlushScheduled = false
	m.windowsPaste.busyInput = false
	m.windowsPaste.busyInputStop = false
	m.windowsPaste.suppressNextCtrlJ = false
}

func (m *model) resetWindowsPasteFallbackIfInputEmpty() {
	if m.hasWindowsPasteBuffer() {
		return
	}
	if strings.TrimSpace(m.input.Value()) == "" {
		m.resetWindowsPasteFallbackInputState()
	}
}

func (m model) windowsPasteFallbackEnabled() bool {
	return m.windowsPaste.enabled
}
