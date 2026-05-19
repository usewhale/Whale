package cli

import (
	"bytes"
	"strings"
	"testing"
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
