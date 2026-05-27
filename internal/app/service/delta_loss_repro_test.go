package service

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestDeltaContentPreservedUnderBackpressure is the regression test for the
// fix where flushChunksBestEffort re-queues unsent delta chunks instead of
// dropping them. As long as the total streamed bytes stay below the
// coalescer's hard cap, a slow consumer must still receive every byte.
func TestDeltaContentPreservedUnderBackpressure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Small channel + one prefilled slot to simulate a slow UI consumer.
	s := &Service{ctx: ctx, events: make(chan Event, 4)}
	s.events <- Event{Kind: EventInfo, Text: "fill buffer"}

	deltas := newTurnDeltaCoalescers(s)

	const chunks = 500
	const chunkSize = 64
	payload := strings.Repeat("x", chunkSize)
	sent := chunks * chunkSize
	if sent >= deltas.hardCap {
		t.Fatalf("test payload (%d) must stay below hardCap (%d) to assert lossless delivery", sent, deltas.hardCap)
	}

	for i := 0; i < chunks; i++ {
		deltas.add(EventAssistantDelta, payload)
	}

	// Drain the prefilled sentinel so flushReliable can make progress.
	<-s.Events()

	done := make(chan struct{})
	go func() {
		deltas.flushReliable()
		close(done)
	}()

	var (
		receivedBytes int
		notice        string
	)
	deadline := time.After(2 * time.Second)
collect:
	for {
		select {
		case ev := <-s.Events():
			if ev.Kind == EventAssistantDelta {
				receivedBytes += len(ev.Text)
			}
			if ev.Kind == EventInfo && strings.Contains(ev.Text, "omitted") {
				notice = ev.Text
			}
		case <-done:
			for {
				select {
				case ev := <-s.Events():
					if ev.Kind == EventAssistantDelta {
						receivedBytes += len(ev.Text)
					}
					if ev.Kind == EventInfo && strings.Contains(ev.Text, "omitted") {
						notice = ev.Text
					}
				default:
					break collect
				}
			}
		case <-deadline:
			t.Fatalf("timed out; received=%d/%d notice=%q", receivedBytes, sent, notice)
		}
	}

	if receivedBytes != sent {
		t.Fatalf("expected lossless delivery, got %d/%d bytes (notice=%q)", receivedBytes, sent, notice)
	}
	if notice != "" {
		t.Fatalf("did not expect a drop notice below hardCap, got %q", notice)
	}
}
