package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/usewhale/whale/internal/shell"
)

const (
	stdioCloseGrace = 500 * time.Millisecond
	stdioCloseLimit = 5 * time.Second
)

type stdioProcessTransport struct {
	base *sdk.CommandTransport
	cmd  *exec.Cmd
}

func (t *stdioProcessTransport) Connect(ctx context.Context) (sdk.Connection, error) {
	conn, err := t.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &stdioProcessConnection{
		Connection: conn,
		cleanup:    shell.AttachCommandCleanup(t.cmd),
	}, nil
}

type stdioProcessConnection struct {
	sdk.Connection
	cleanup *shell.CommandCleanup
}

func (c *stdioProcessConnection) Close() error {
	if c == nil {
		return nil
	}
	if c.Connection == nil {
		return ignoreProcessDone(c.cleanup.Cleanup())
	}

	done := make(chan error, 1)
	go func() {
		done <- c.Connection.Close()
	}()

	var cleanupErr error
	select {
	case connErr := <-done:
		cleanupErr = ignoreProcessDone(c.cleanup.Cleanup())
		return closeErr(connErr, cleanupErr)
	case <-time.After(stdioCloseGrace):
		cleanupErr = ignoreProcessDone(c.cleanup.Cleanup())
	}

	select {
	case connErr := <-done:
		return closeErr(connErr, cleanupErr)
	case <-time.After(stdioCloseLimit):
		return errors.Join(cleanupErr, fmt.Errorf("mcp stdio close timed out"))
	}
}

func closeErr(connErr, cleanupErr error) error {
	if cleanupErr == nil && isIntentionalProcessExit(connErr) {
		return nil
	}
	return errors.Join(connErr, cleanupErr)
}

func ignoreProcessDone(err error) error {
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func isIntentionalProcessExit(err error) bool {
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		return true
	}
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}
