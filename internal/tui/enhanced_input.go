package tui

import (
	"bytes"
	"io"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/x/term"
)

const (
	keyboardEnhancementEnable  = "\x1b[>4;2m"
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
	return false
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
