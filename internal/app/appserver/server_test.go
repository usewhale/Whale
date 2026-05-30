package appserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/runtime/protocol"
)

func TestRunEmitsReadyAndClosedOnEOF(t *testing.T) {
	out := runAppServerTest(t, "")
	msgs := decodeServerMessages(t, out)
	if len(msgs) < 2 {
		t.Fatalf("expected ready and closed messages, got %+v", msgs)
	}
	if msgs[0].Type != protocol.ServerMessageReady {
		t.Fatalf("first message = %+v, want ready", msgs[0])
	}
	if msgs[len(msgs)-1].Type != protocol.ServerMessageClosed {
		t.Fatalf("last message = %+v, want closed", msgs[len(msgs)-1])
	}
}

func TestRunReportsInvalidJSONAndContinues(t *testing.T) {
	input := "{bad}\n" + encodeClientMessage(t, protocol.ClientMessage{
		Type:   protocol.ClientMessageIntent,
		Intent: &protocol.Intent{Kind: protocol.IntentShutdown},
	})
	msgs := decodeServerMessages(t, runAppServerTest(t, input))
	if !hasServerError(msgs, "decode client message") {
		t.Fatalf("expected decode error message, got %+v", msgs)
	}
	if msgs[len(msgs)-1].Type != protocol.ServerMessageClosed {
		t.Fatalf("server did not continue to shutdown, got %+v", msgs)
	}
}

func TestRunDispatchesProtocolIntent(t *testing.T) {
	input := encodeClientMessage(t, protocol.ClientMessage{
		Type: protocol.ClientMessageIntent,
		Intent: &protocol.Intent{
			Kind:         protocol.IntentSetApprovalMode,
			ApprovalMode: "auto_accept",
		},
	}) + encodeClientMessage(t, protocol.ClientMessage{
		Type:   protocol.ClientMessageIntent,
		Intent: &protocol.Intent{Kind: protocol.IntentShutdown},
	})
	msgs := decodeServerMessages(t, runAppServerTest(t, input))
	if !hasEventKind(msgs, protocol.EventInfo) {
		t.Fatalf("expected info event from approval mode intent, got %+v", msgs)
	}
}

func TestRunAcceptsLargeProtocolFrame(t *testing.T) {
	input := encodeClientMessage(t, protocol.ClientMessage{
		Type: protocol.ClientMessageIntent,
		Intent: &protocol.Intent{
			Kind:  protocol.IntentShutdown,
			Input: strings.Repeat("x", 70*1024),
		},
	})
	msgs := decodeServerMessages(t, runAppServerTest(t, input))
	if hasServerError(msgs, "read client message") {
		t.Fatalf("large valid client frame should not produce read error, got %+v", msgs)
	}
	if msgs[len(msgs)-1].Type != protocol.ServerMessageClosed {
		t.Fatalf("server did not close cleanly after large shutdown intent, got %+v", msgs)
	}
}

func TestClientEOFDispatchesShutdownBeforeClosed(t *testing.T) {
	var dispatcher recordingDispatcher
	var written []protocol.ServerMessage
	err := closeAfterClientDisconnect(&dispatcher, func(msg protocol.ServerMessage) error {
		written = append(written, msg)
		return nil
	})
	if err != nil {
		t.Fatalf("closeAfterClientDisconnect: %v", err)
	}
	if len(dispatcher.intents) != 1 || dispatcher.intents[0].Kind != protocol.IntentShutdown {
		t.Fatalf("EOF did not dispatch shutdown intent: %+v", dispatcher.intents)
	}
	if len(written) != 1 || written[0].Type != protocol.ServerMessageClosed {
		t.Fatalf("EOF did not write closed message: %+v", written)
	}
}

type recordingDispatcher struct {
	intents []protocol.Intent
}

func (d *recordingDispatcher) DispatchProtocol(intent protocol.Intent) {
	d.intents = append(d.intents, intent)
}

func runAppServerTest(t *testing.T, input string) string {
	t.Helper()
	t.Setenv("DEEPSEEK_API_KEY", "sk-test")
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	t.Chdir(workspace)

	cfg := app.DefaultConfig()
	cfg.DataDir = t.TempDir()
	var out bytes.Buffer
	if err := Run(context.Background(), cfg, app.StartOptions{NewSession: true}, strings.NewReader(input), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out.String()
}

func encodeClientMessage(t *testing.T, msg protocol.ClientMessage) string {
	t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal client message: %v", err)
	}
	return string(data) + "\n"
}

func decodeServerMessages(t *testing.T, raw string) []protocol.ServerMessage {
	t.Helper()
	var msgs []protocol.ServerMessage
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		var msg protocol.ServerMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("unmarshal server message %q: %v\nall output:\n%s", line, err, raw)
		}
		msgs = append(msgs, msg)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan server messages: %v", err)
	}
	return msgs
}

func hasServerError(msgs []protocol.ServerMessage, contains string) bool {
	for _, msg := range msgs {
		if msg.Type == protocol.ServerMessageError && strings.Contains(msg.Error, contains) {
			return true
		}
	}
	return false
}

func hasEventKind(msgs []protocol.ServerMessage, kind protocol.EventKind) bool {
	for _, msg := range msgs {
		if msg.Type == protocol.ServerMessageEvent && msg.Event != nil && msg.Event.Kind == kind {
			return true
		}
	}
	return false
}
