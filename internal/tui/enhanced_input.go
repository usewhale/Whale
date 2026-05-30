package tui

import (
	"bytes"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/term"
)

const (
	// xterm modifyOtherKeys level 1: only keys without a traditional
	// encoding (e.g. Shift+Enter, Ctrl+J disambiguation) get reported as
	// CSI 27 / CSI u sequences. Keys with a traditional form keep it —
	// crucially Alt+letter stays as `\x1b<letter>` so bubbletea decodes
	// it as KeyMsg{Alt: true, ...}. Level 2 would re-encode Alt+letter
	// as `\x1b[27;3;<code>~`, which bubbletea does not parse, causing
	// Alt+B / Alt+F / Alt+D / Alt+Backspace to silently fail in
	// modifyOtherKeys-aware terminals such as Ghostty (issue #118).
	// tmux sidesteps the problem by intercepting the enable sequence,
	// which is why those keys "work in tmux" but not in raw Ghostty.
	keyboardEnhancementEnable  = "\x1b[>4;1m"
	keyboardEnhancementDisable = "\x1b[>4m"
	shiftEnterPartialTimeout   = 100 * time.Millisecond
)

var enhancedInputSequences = []enhancedInputSequence{
	{input: []byte("\x1b[13;2u"), output: []byte{'\n'}},
	{input: []byte("\x1b[27;2;13~"), output: []byte{'\n'}},
	{input: []byte("\x1b[106;5u"), output: []byte{'\n'}},
	{input: []byte("\x1b[74;5u"), output: []byte{'\n'}},
	{input: []byte("\x1b[27;5;106~"), output: []byte{'\n'}},
	{input: []byte("\x1b[27;5;74~"), output: []byte{'\n'}},
	{input: []byte("\x1b[27;5;10~"), output: []byte{'\n'}},
}

type enhancedInputSequence struct {
	input  []byte
	output []byte
}

type enhancedTerminalInput struct {
	file      *os.File
	reader    *shiftEnterReader
	closeFile bool
}

var _ term.File = (*enhancedTerminalInput)(nil)

func newEnhancedTerminalInput(file *os.File, closeFile bool) *enhancedTerminalInput {
	return &enhancedTerminalInput{
		file:      file,
		reader:    newShiftEnterReader(file),
		closeFile: closeFile,
	}
}

func (i *enhancedTerminalInput) Read(p []byte) (int, error) {
	return i.reader.Read(p)
}

func (i *enhancedTerminalInput) Write(p []byte) (int, error) {
	return i.file.Write(p)
}

func (i *enhancedTerminalInput) Fd() uintptr {
	return i.file.Fd()
}

func (i *enhancedTerminalInput) Close() error {
	if !i.closeFile {
		return nil
	}
	return i.file.Close()
}

type shiftEnterReader struct {
	chunks  <-chan inputChunk
	pending []byte
	out     []byte
	err     error
}

type inputChunk struct {
	data []byte
	err  error
}

func newShiftEnterReader(src io.Reader) *shiftEnterReader {
	return &shiftEnterReader{chunks: readInputChunks(src)}
}

func (r *shiftEnterReader) Read(p []byte) (int, error) {
	for {
		if len(r.out) > 0 {
			n := copy(p, r.out)
			r.out = r.out[n:]
			return n, nil
		}

		final := r.err != nil
		r.drainPending(final)
		if len(r.out) > 0 {
			continue
		}
		if final {
			err := r.err
			r.err = nil
			return 0, err
		}

		if isPartialShiftEnterSequence(r.pending) {
			if r.readNextChunk(shiftEnterPartialTimeout) {
				continue
			}
			r.out = append(r.out, r.pending[0])
			r.pending = r.pending[1:]
			continue
		}

		r.readNextChunk(0)
	}
}

func readInputChunks(src io.Reader) <-chan inputChunk {
	chunks := make(chan inputChunk, 1)
	go func() {
		defer close(chunks)
		var buf [64]byte
		for {
			n, err := src.Read(buf[:])
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				chunks <- inputChunk{data: data}
			}
			if err != nil {
				chunks <- inputChunk{err: err}
				return
			}
			if n == 0 {
				time.Sleep(time.Millisecond)
			}
		}
	}()
	return chunks
}

func (r *shiftEnterReader) readNextChunk(timeout time.Duration) bool {
	if timeout <= 0 {
		chunk, ok := <-r.chunks
		r.appendChunk(chunk, ok)
		return ok
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case chunk, ok := <-r.chunks:
		r.appendChunk(chunk, ok)
		return ok
	case <-timer.C:
		return false
	}
}

func (r *shiftEnterReader) appendChunk(chunk inputChunk, ok bool) {
	if !ok {
		if r.err == nil {
			r.err = io.EOF
		}
		return
	}
	if len(chunk.data) > 0 {
		r.pending = append(r.pending, chunk.data...)
	}
	if chunk.err != nil {
		r.err = chunk.err
	}
}

func (r *shiftEnterReader) drainPending(final bool) {
	for len(r.pending) > 0 {
		matched := false
		for _, seq := range enhancedInputSequences {
			if bytes.HasPrefix(r.pending, seq.input) {
				r.out = append(r.out, seq.output...)
				r.pending = r.pending[len(seq.input):]
				matched = true
				break
			}
		}
		if matched {
			continue
		}

		if consumed, ok := discardTerminalResponseSequence(r.pending); ok {
			r.pending = r.pending[consumed:]
			continue
		}

		if out, consumed, ok := translateEnhancedPrintableSequence(r.pending); ok {
			r.out = append(r.out, out...)
			r.pending = r.pending[consumed:]
			continue
		}

		if !final && isPartialShiftEnterSequence(r.pending) {
			return
		}

		r.out = append(r.out, r.pending[0])
		r.pending = r.pending[1:]
	}
}

func isPartialShiftEnterSequence(input []byte) bool {
	if len(input) == 0 {
		return false
	}
	for _, seq := range enhancedInputSequences {
		if len(input) < len(seq.input) && bytes.HasPrefix(seq.input, input) {
			return true
		}
	}
	if isPartialEnhancedPrintableSequence(input) {
		return true
	}
	if isPartialTerminalResponseSequence(input) {
		return true
	}
	return false
}

func discardTerminalResponseSequence(input []byte) (int, bool) {
	if !bytes.HasPrefix(input, []byte("\x1b]")) {
		return 0, false
	}
	if end := bytes.IndexByte(input, '\a'); end >= 0 {
		return end + 1, true
	}
	if end := bytes.Index(input, []byte("\x1b\\")); end >= 0 {
		return end + 2, true
	}
	return 0, false
}

func isPartialTerminalResponseSequence(input []byte) bool {
	if len(input) == 0 || !bytes.HasPrefix([]byte("\x1b]"), input[:min(len(input), 2)]) {
		return false
	}
	return !bytes.Contains(input, []byte{'\a'}) && !bytes.Contains(input, []byte("\x1b\\"))
}

func translateEnhancedPrintableSequence(input []byte) ([]byte, int, bool) {
	codepoint, modifier, consumed, ok := parseCSIUPrintableSequence(input)
	if !ok {
		codepoint, modifier, consumed, ok = parseModifyOtherKeysPrintableSequence(input)
	}
	if !ok || !isTextInputModifier(modifier) {
		return nil, 0, false
	}
	if !isPrintableCodepoint(codepoint) {
		return translateCSIUFunctionKey(codepoint, modifier, consumed)
	}
	return []byte(string(rune(codepoint))), consumed, true
}

// translateCSIUFunctionKey translates a CSI u / modifyOtherKeys sequence
// for a non-printable function key (Home, End, etc.) into the traditional
// CSI sequence that Bubble Tea can parse. Ghostty, Kitty, and other
// terminals that implement the Kitty keyboard protocol encode function
// keys like Home as "\x1b[1;2u" instead of the standard "\x1b[1;2H".
func translateCSIUFunctionKey(codepoint, modifier, consumed int) ([]byte, int, bool) {
	// Map CSI u codepoint to the traditional CSI terminator character.
	var terminator string
	switch codepoint {
	case 1:
		terminator = "H" // Home
	case 4:
		terminator = "F" // End
	default:
		return nil, 0, false
	}

	switch modifier {
	case 1:
		return []byte("\x1b[" + terminator), consumed, true
	case 2:
		return []byte("\x1b[1;2" + terminator), consumed, true
	case 3:
		return []byte("\x1b[1;3" + terminator), consumed, true
	case 4:
		return []byte("\x1b[1;4" + terminator), consumed, true
	default:
		return nil, 0, false
	}
}

func parseCSIUPrintableSequence(input []byte) (int, int, int, bool) {
	if !bytes.HasPrefix(input, []byte("\x1b[")) {
		return 0, 0, 0, false
	}
	end := bytes.IndexByte(input, 'u')
	if end < 0 {
		return 0, 0, 0, false
	}
	parts := strings.Split(string(input[2:end]), ";")
	if len(parts) != 2 {
		return 0, 0, 0, false
	}
	codepoint, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, false
	}
	modifier, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, false
	}
	return codepoint, modifier, end + 1, true
}

func parseModifyOtherKeysPrintableSequence(input []byte) (int, int, int, bool) {
	if !bytes.HasPrefix(input, []byte("\x1b[")) {
		return 0, 0, 0, false
	}
	end := bytes.IndexByte(input, '~')
	if end < 0 {
		return 0, 0, 0, false
	}
	parts := strings.Split(string(input[2:end]), ";")
	if len(parts) != 3 || parts[0] != "27" {
		return 0, 0, 0, false
	}
	modifier, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, false
	}
	codepoint, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, 0, false
	}
	return codepoint, modifier, end + 1, true
}

func isTextInputModifier(modifier int) bool {
	return modifier == 1 || modifier == 2
}

func isPrintableCodepoint(codepoint int) bool {
	r := rune(codepoint)
	return codepoint >= 0x20 && codepoint != 0x7f && utf8.ValidRune(r)
}

func isPartialEnhancedPrintableSequence(input []byte) bool {
	if len(input) == 0 || !bytes.HasPrefix([]byte("\x1b["), input[:min(len(input), 2)]) {
		return false
	}
	if len(input) <= 2 {
		return true
	}
	body := string(input[2:])
	if strings.ContainsAny(body, "u~") {
		return false
	}
	parts := strings.Split(body, ";")
	if len(parts) > 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			continue
		}
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return len(parts) <= 2 || parts[0] == "27"
}

func enableTerminalKeyboardEnhancements(output *os.File) func() {
	if output == nil || !term.IsTerminal(output.Fd()) {
		return func() {}
	}
	_, _ = output.WriteString(keyboardEnhancementEnable)
	var once sync.Once
	return func() {
		once.Do(func() {
			_, _ = output.WriteString(keyboardEnhancementDisable)
		})
	}
}
