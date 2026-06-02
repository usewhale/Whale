package service

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

func (s *Service) emit(ev Event) {
	ev = s.prepareLifecycleEvent(ev)
	if isCriticalEvent(ev.Kind) {
		s.emitReliable(ev)
		return
	}
	s.emitBestEffort(ev)
}

func (s *Service) emitReliable(ev Event) {
	select {
	case s.events <- ev:
	case <-s.ctx.Done():
	}
}

func (s *Service) emitBestEffort(ev Event) {
	select {
	case s.events <- ev:
	default:
	}
}

func isCriticalEvent(kind EventKind) bool {
	switch kind {
	case EventError, EventPlanCompleted, EventPlanUpdate, EventProviderRetry, EventResponseReset, EventToolCall, EventToolResult, EventHookStarted, EventHookCompleted, EventTaskStarted, EventTaskCompleted, EventMCPComplete, EventApprovalRequired, EventApprovalDecision, EventUserInputRequired, EventUserInputDone, EventSessionsListed, EventRewindMessagesListed, EventLocalSubmitResult, EventLocalSubmitDone, EventDiffResult, EventBtwStarted, EventBtwDelta, EventBtwDone, EventBtwError, EventPendingInputAccepted, EventPendingInputRejected, EventTurnDone, EventModelSelectionRequested, EventPermissionsSelectionRequested, EventSkillsSelectionRequested, EventSkillsManagerUpdated, EventPluginsManagerUpdated, EventConfigManagerUpdated, EventHooksManagerUpdated, EventHooksStartupReviewRequested, EventReviewRequested, EventSkillLoaded, EventWorktreeExitPrompt, EventExitRequested, EventScreenClearRequested, EventSessionHydrated, EventWorkflowPanel, EventWorkflowSnapshot, EventWorkflowTerminal:
		return true
	default:
		return false
	}
}

func (s *Service) prepareLifecycleEvent(ev Event) Event {
	if !isLifecycleEvent(ev.Kind) {
		return ev
	}
	if ev.Sequence == 0 {
		ev.Sequence = s.nextEventSequence.Add(1)
	}
	if ev.StartedAt.IsZero() {
		ev.StartedAt = time.Now()
	}
	if ev.ItemID == "" {
		ev.ItemID = lifecycleItemID(ev)
	}
	if ev.ApprovalID == "" && ev.Approval != nil {
		ev.ApprovalID = ev.Approval.Key
	}
	if ev.WorkflowRunID == "" {
		ev.WorkflowRunID = workflowRunIDFromEvent(ev)
	}
	return ev
}

func isLifecycleEvent(kind EventKind) bool {
	switch kind {
	case EventToolCall, EventToolResult, EventApprovalRequired, EventApprovalDecision, EventTaskStarted, EventTaskProgress, EventTaskCompleted, EventHookStarted, EventHookCompleted, EventUserInputRequired, EventUserInputDone, EventWorkflowSnapshot, EventWorkflowTerminal:
		return true
	default:
		return false
	}
}

func lifecycleItemID(ev Event) string {
	if ev.Kind == EventWorkflowSnapshot || ev.Kind == EventWorkflowTerminal {
		if runID := workflowRunIDFromEvent(ev); runID != "" {
			return "workflow:" + runID
		}
	}
	if ev.ToolCallID != "" {
		if ev.Kind == EventUserInputRequired || ev.Kind == EventUserInputDone {
			return "user_input:" + ev.ToolCallID
		}
		return "tool:" + ev.ToolCallID
	}
	if ev.Approval != nil && ev.Approval.ToolCall.ID != "" {
		return "tool:" + ev.Approval.ToolCall.ID
	}
	if ev.Hook != nil && ev.Hook.ID != "" {
		return "hook:" + ev.Hook.ID
	}
	return ""
}

func workflowRunIDFromEvent(ev Event) string {
	if ev.WorkflowRunID != "" {
		return strings.TrimSpace(ev.WorkflowRunID)
	}
	for _, key := range []string{"workflow_run_id", "run_id", "runId"} {
		if ev.Metadata == nil {
			continue
		}
		raw, ok := ev.Metadata[key]
		if !ok || raw == nil {
			continue
		}
		if s := strings.TrimSpace(fmt.Sprint(raw)); s != "" {
			return s
		}
	}
	if ev.LocalResult != nil && ev.LocalResult.WorkflowPanelSnapshot != nil {
		return strings.TrimSpace(ev.LocalResult.WorkflowPanelSnapshot.RunID)
	}
	return ""
}

type deltaChunk struct {
	kind EventKind
	text string
}

type turnDeltaCoalescers struct {
	mu            sync.Mutex
	svc           *Service
	chunks        []deltaChunk
	queuedChars   int
	droppedBytes  int
	lastFlush     time.Time
	flushChars    int
	flushInterval time.Duration
	hardCap       int
}

func newTurnDeltaCoalescers(s *Service) turnDeltaCoalescers {
	return turnDeltaCoalescers{
		svc:           s,
		lastFlush:     time.Now(),
		flushChars:    2048,
		flushInterval: 50 * time.Millisecond,
		hardCap:       256 * 1024,
	}
}

func (c *turnDeltaCoalescers) add(kind EventKind, text string) {
	if text == "" {
		return
	}
	c.mu.Lock()
	if n := len(c.chunks); n > 0 && c.chunks[n-1].kind == kind {
		c.chunks[n-1].text += text
	} else {
		c.chunks = append(c.chunks, deltaChunk{kind: kind, text: text})
	}
	c.queuedChars += len(text)
	now := time.Now()
	if c.queuedChars >= c.flushChars || c.lastFlush.IsZero() || now.Sub(c.lastFlush) >= c.flushInterval {
		chunks := c.drainLocked(now)
		c.mu.Unlock()
		c.flushChunksBestEffort(chunks)
		return
	}
	c.mu.Unlock()
}

func (c *turnDeltaCoalescers) flushBestEffort() {
	if c == nil {
		return
	}
	c.mu.Lock()
	chunks := c.drainLocked(time.Now())
	c.mu.Unlock()
	c.flushChunksBestEffort(chunks)
}

func (c *turnDeltaCoalescers) drainLocked(now time.Time) []deltaChunk {
	if len(c.chunks) == 0 {
		return nil
	}
	chunks := append([]deltaChunk(nil), c.chunks...)
	c.chunks = nil
	c.queuedChars = 0
	c.lastFlush = now
	return chunks
}

func (c *turnDeltaCoalescers) flushChunksBestEffort(chunks []deltaChunk) {
	sent := 0
SendLoop:
	for i, chunk := range chunks {
		if chunk.text == "" {
			sent = i + 1
			continue
		}
		select {
		case c.svc.events <- Event{Kind: chunk.kind, Text: chunk.text}:
			sent = i + 1
		default:
			// Stop at the first failure so we preserve cross-kind order on
			// re-queue: a later same-kind chunk slipping ahead of an earlier
			// different-kind one would corrupt the visible stream.
			break SendLoop
		}
	}
	remainder := chunks[sent:]
	if len(remainder) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Prepend unsent chunks ahead of anything add() has accumulated since
	// drainLocked. If the seam is the same kind, coalesce to avoid emitting
	// adjacent fragments of the same stream.
	if n := len(c.chunks); n > 0 && remainder[len(remainder)-1].kind == c.chunks[0].kind {
		remainder[len(remainder)-1].text += c.chunks[0].text
		c.chunks = c.chunks[1:]
	}
	c.chunks = append(remainder, c.chunks...)
	c.queuedChars = 0
	for _, ch := range c.chunks {
		c.queuedChars += len(ch.text)
	}
	// Soft cap: if the queue has grown past the hard limit (UI truly wedged),
	// drop the OLDEST bytes to keep memory bounded while still showing the
	// latest progress. Account the dropped bytes for the end-of-turn notice.
	for c.queuedChars > c.hardCap && len(c.chunks) > 0 {
		head := &c.chunks[0]
		overflow := c.queuedChars - c.hardCap
		if len(head.text) <= overflow {
			c.droppedBytes += len(head.text)
			c.queuedChars -= len(head.text)
			c.chunks = c.chunks[1:]
			continue
		}
		// Advance to the next rune boundary so we never hand the UI a
		// fragment that starts mid-multibyte sequence (would render as a
		// replacement char).
		for overflow < len(head.text) && !utf8.RuneStart(head.text[overflow]) {
			overflow++
		}
		head.text = head.text[overflow:]
		c.droppedBytes += overflow
		c.queuedChars -= overflow
	}
}

func (c *turnDeltaCoalescers) flushReliable() {
	if c == nil {
		return
	}
	c.mu.Lock()
	chunks := c.drainLocked(time.Now())
	droppedBytes := c.droppedBytes
	c.droppedBytes = 0
	c.mu.Unlock()

	for _, chunk := range chunks {
		if chunk.text != "" {
			c.svc.emitReliable(Event{Kind: chunk.kind, Text: chunk.text})
		}
	}
	if droppedBytes > 0 {
		// The notice itself must be reliable: emitting it best-effort means
		// that under sustained UI backpressure the user never learns content
		// was dropped, defeating the point of the notice.
		c.svc.emitReliable(Event{
			Kind: EventInfo,
			Text: fmt.Sprintf("[stream] UI backpressure exceeded buffer; omitted ~%d bytes of streamed text", droppedBytes),
		})
	}
}
