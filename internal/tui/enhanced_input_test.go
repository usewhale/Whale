package tui

import (
	"io"
	"strings"
	"testing"
)

func TestKeyboardEnhancementModeDoesNotEnableKittyProtocol(t *testing.T) {
	if strings.Contains(keyboardEnhancementEnable, "\x1b[>1u") {
		t.Fatalf("keyboard enhancement enable sequence should not enable kitty CSI-u: %q", keyboardEnhancementEnable)
	}
	if strings.Contains(keyboardEnhancementDisable, "\x1b[<u") {
		t.Fatalf("keyboard enhancement disable sequence should not pop kitty CSI-u: %q", keyboardEnhancementDisable)
	}
	if keyboardEnhancementEnable != "\x1b[>4;2m" {
		t.Fatalf("unexpected keyboard enhancement enable sequence: %q", keyboardEnhancementEnable)
	}
	if keyboardEnhancementDisable != "\x1b[>4m" {
		t.Fatalf("unexpected keyboard enhancement disable sequence: %q", keyboardEnhancementDisable)
	}
}

func TestShiftEnterReaderTranslatesEnhancedEnterSequences(t *testing.T) {
	input := "alpha\x1b[13;2ubeta\x1b[27;2;13~gamma"
	got := readShiftEnterInput(t, strings.NewReader(input))
	if want := "alpha\nbeta\ngamma"; got != want {
		t.Fatalf("unexpected translated input: got %q want %q", got, want)
	}
}

func TestShiftEnterReaderTranslatesEnhancedCtrlJSequences(t *testing.T) {
	input := "alpha\x1b[106;5ubeta\x1b[74;5ugamma\x1b[27;5;106~delta\x1b[27;5;74~epsilon\x1b[27;5;10~zeta"
	got := readShiftEnterInput(t, strings.NewReader(input))
	if want := "alpha\nbeta\ngamma\ndelta\nepsilon\nzeta"; got != want {
		t.Fatalf("unexpected translated Ctrl+J input: got %q want %q", got, want)
	}
}

func TestShiftEnterReaderPreservesPlainEnterAndUnknownEscapeSequences(t *testing.T) {
	input := "alpha\rbeta\x1b[15~gamma\x1b"
	got := readShiftEnterInput(t, strings.NewReader(input))
	if got != input {
		t.Fatalf("unexpected preserved input: got %q want %q", got, input)
	}
}

func TestShiftEnterReaderHandlesChunkedSequences(t *testing.T) {
	reader := &chunkReader{chunks: []string{
		"alpha\x1b[",
		"13;",
		"2ubeta\x1b[27;",
		"2;",
		"13~gamma",
	}}
	got := readShiftEnterInput(t, reader)
	if want := "alpha\nbeta\ngamma"; got != want {
		t.Fatalf("unexpected translated chunked input: got %q want %q", got, want)
	}
}

func TestShiftEnterReaderDoesNotHoldBareEscape(t *testing.T) {
	reader := &chunkReader{chunks: []string{"\x1b", "x"}}
	got := readShiftEnterInput(t, reader)
	if want := "\x1bx"; got != want {
		t.Fatalf("unexpected bare escape input: got %q want %q", got, want)
	}
}

func readShiftEnterInput(t *testing.T, src io.Reader) string {
	t.Helper()
	got, err := io.ReadAll(newShiftEnterReader(src))
	if err != nil {
		t.Fatalf("read shift enter input: %v", err)
	}
	return string(got)
}

type chunkReader struct {
	chunks []string
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	chunk := r.chunks[0]
	r.chunks = r.chunks[1:]
	return copy(p, chunk), nil
}
