package service

import (
	"fmt"
	"time"
)

func (s *Service) emit(ev Event) {
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
	case EventError, EventPlanCompleted, EventToolCall, EventToolResult, EventTaskStarted, EventTaskCompleted, EventMCPComplete, EventApprovalRequired, EventUserInputRequired, EventUserInputDone, EventSessionsListed, EventLocalSubmitResult, EventLocalSubmitDone, EventTurnDone, EventModelPicker, EventPermissionsPicker, EventSkillLoaded, EventExitRequested, EventClearScreen, EventSessionHydrated:
		return true
	default:
		return false
	}
}

type deltaChunk struct {
	kind EventKind
	text string
}

type turnDeltaCoalescers struct {
	svc            *Service
	chunks         []deltaChunk
	queuedChars    int
	droppedFlushes int
	lastFlush      time.Time
	flushChars     int
	flushInterval  time.Duration
}

func newTurnDeltaCoalescers(s *Service) turnDeltaCoalescers {
	return turnDeltaCoalescers{
		svc:           s,
		lastFlush:     time.Now(),
		flushChars:    2048,
		flushInterval: 50 * time.Millisecond,
	}
}

func (c *turnDeltaCoalescers) add(kind EventKind, text string) {
	if text == "" {
		return
	}
	if n := len(c.chunks); n > 0 && c.chunks[n-1].kind == kind {
		c.chunks[n-1].text += text
	} else {
		c.chunks = append(c.chunks, deltaChunk{kind: kind, text: text})
	}
	c.queuedChars += len(text)
	now := time.Now()
	if c.queuedChars >= c.flushChars || c.lastFlush.IsZero() || now.Sub(c.lastFlush) >= c.flushInterval {
		c.flushBestEffort()
	}
}

func (c *turnDeltaCoalescers) flushBestEffort() {
	if c == nil || len(c.chunks) == 0 {
		return
	}
	chunks := c.chunks
	c.chunks = nil
	c.queuedChars = 0
	c.lastFlush = time.Now()
	for _, chunk := range chunks {
		if chunk.text == "" {
			continue
		}
		select {
		case c.svc.events <- Event{Kind: chunk.kind, Text: chunk.text}:
		default:
			c.droppedFlushes++
		}
	}
}

func (c *turnDeltaCoalescers) flushReliable() {
	if c == nil {
		return
	}
	if len(c.chunks) > 0 {
		chunks := c.chunks
		c.chunks = nil
		c.queuedChars = 0
		c.lastFlush = time.Now()
		for _, chunk := range chunks {
			if chunk.text != "" {
				c.svc.emitReliable(Event{Kind: chunk.kind, Text: chunk.text})
			}
		}
	}
	if c.droppedFlushes > 0 {
		// The notice itself must be reliable: emitting it best-effort means
		// that under sustained UI backpressure the user never learns chunks
		// were dropped, defeating the point of the notice.
		c.svc.emitReliable(Event{
			Kind: EventInfo,
			Text: fmt.Sprintf("[stream] coalesced output under UI backpressure; omitted %d intermediate chunks", c.droppedFlushes),
		})
		c.droppedFlushes = 0
	}
}
