package appserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/usewhale/whale/internal/app"
	"github.com/usewhale/whale/internal/app/service"
	"github.com/usewhale/whale/internal/runtime/protocol"
)

const maxClientMessageBytes = 16 * 1024 * 1024

func Run(ctx context.Context, cfg app.Config, start app.StartOptions, in io.Reader, out io.Writer) error {
	svc, err := service.New(ctx, cfg, start)
	if err != nil {
		return err
	}
	defer svc.Close()

	enc := json.NewEncoder(out)
	write := func(msg protocol.ServerMessage) error {
		if err := enc.Encode(msg); err != nil {
			return fmt.Errorf("write app-server message: %w", err)
		}
		return nil
	}
	if err := write(protocol.ServerMessage{
		Type:          protocol.ServerMessageReady,
		SessionID:     svc.SessionID(),
		WorkspaceRoot: svc.WorkspaceRoot(),
	}); err != nil {
		return err
	}

	inputs := make(chan clientInput)
	go scanClientMessages(in, inputs)
	for {
		select {
		case ev := <-svc.Events():
			if err := write(protocol.ServerMessage{Type: protocol.ServerMessageEvent, Event: &ev}); err != nil {
				return err
			}
		case input := <-inputs:
			if input.done {
				return closeAfterClientDisconnect(svc, write)
			}
			if input.err != nil {
				if err := write(protocol.ServerMessage{Type: protocol.ServerMessageError, Error: input.err.Error()}); err != nil {
					return err
				}
				continue
			}
			if input.msg.Type != protocol.ClientMessageIntent || input.msg.Intent == nil {
				if err := write(protocol.ServerMessage{Type: protocol.ServerMessageError, Error: "expected client message type intent with intent payload"}); err != nil {
					return err
				}
				continue
			}
			svc.DispatchProtocol(*input.msg.Intent)
			if input.msg.Intent.Kind == protocol.IntentShutdown {
				if err := write(protocol.ServerMessage{Type: protocol.ServerMessageClosed}); err != nil {
					return err
				}
				return nil
			}
		case <-ctx.Done():
			if err := write(protocol.ServerMessage{Type: protocol.ServerMessageClosed}); err != nil {
				return err
			}
			return ctx.Err()
		}
	}
}

type protocolDispatcher interface {
	DispatchProtocol(protocol.Intent)
}

func closeAfterClientDisconnect(dispatcher protocolDispatcher, write func(protocol.ServerMessage) error) error {
	dispatcher.DispatchProtocol(protocol.Intent{Kind: protocol.IntentShutdown})
	if err := write(protocol.ServerMessage{Type: protocol.ServerMessageClosed}); err != nil {
		return err
	}
	return nil
}

type clientInput struct {
	msg  protocol.ClientMessage
	err  error
	done bool
}

func scanClientMessages(in io.Reader, out chan<- clientInput) {
	defer close(out)
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), maxClientMessageBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg protocol.ClientMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			out <- clientInput{err: fmt.Errorf("decode client message: %w", err)}
			continue
		}
		out <- clientInput{msg: msg}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		out <- clientInput{err: fmt.Errorf("read client message: %w", err)}
	}
	out <- clientInput{done: true}
}
