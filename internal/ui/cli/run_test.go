package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/plugins"
)

func TestClearCLIOutputClearsAndConfirms(t *testing.T) {
	var out bytes.Buffer
	clearCLIOutput(&out)
	got := out.String()
	if !strings.Contains(got, "\033[H\033[2J\033[3J") {
		t.Fatalf("expected clear screen sequence, got %q", got)
	}
	if !strings.Contains(got, "terminal cleared") {
		t.Fatalf("expected visible clear confirmation, got %q", got)
	}
}

func TestCLIRunOptionsFromCommandTurnPreservesReviewOptions(t *testing.T) {
	turn := &plugins.CommandTurn{
		Hidden:             true,
		ReadOnly:           true,
		ShellAllowPrefixes: []string{"gh pr view", "gh pr diff"},
	}
	opts := cliRunOptionsFromCommandTurn(turn, false)
	if !opts.HiddenInput {
		t.Fatal("expected hidden input option")
	}
	if !opts.ReadOnly {
		t.Fatal("expected read-only option")
	}
	if got := strings.Join(opts.ShellAllowPrefixes, ","); got != "gh pr view,gh pr diff" {
		t.Fatalf("unexpected shell allow prefixes: %q", got)
	}

	turn.ShellAllowPrefixes[0] = "mutated"
	if opts.ShellAllowPrefixes[0] != "gh pr view" {
		t.Fatalf("expected allow prefixes to be copied, got %+v", opts.ShellAllowPrefixes)
	}
}
