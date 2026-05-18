//go:build windows

package tui

import (
	"fmt"
	"io"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/erikgeiser/coninput"
	"golang.org/x/sys/windows"
)

func newTerminalProgram(m tea.Model) (*tea.Program, func(), error) {
	input, err := newWindowsConsoleInput()
	if err != nil {
		// Fall back to Bubble Tea's native Windows input path if direct console
		// setup fails. That preserves normal input even though Shift+Enter will
		// be indistinguishable from Enter.
		return tea.NewProgram(m), func() {}, nil
	}
	p := tea.NewProgram(m, tea.WithInput(eofInput{}))
	input.start(p)
	return p, input.close, nil
}

type eofInput struct{}

func (eofInput) Read([]byte) (int, error) {
	return 0, io.EOF
}

type windowsConsoleInput struct {
	conin        windows.Handle
	originalMode uint32
	stop         chan struct{}
	done         chan struct{}
	closeOnce    sync.Once
}

func newWindowsConsoleInput() (*windowsConsoleInput, error) {
	conin, err := coninput.NewStdinHandle()
	if err != nil {
		return nil, err
	}
	var original uint32
	if err := windows.GetConsoleMode(conin, &original); err != nil {
		return nil, err
	}
	mode := coninput.AddInputModes(0, windows.ENABLE_WINDOW_INPUT, windows.ENABLE_EXTENDED_FLAGS)
	if err := windows.SetConsoleMode(conin, mode); err != nil {
		return nil, err
	}
	return &windowsConsoleInput{
		conin:        conin,
		originalMode: original,
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
	}, nil
}

func (i *windowsConsoleInput) start(p *tea.Program) {
	go func() {
		defer close(i.done)
		var windowSize coninput.WindowBufferSizeEventRecord
		for {
			select {
			case <-i.stop:
				return
			default:
			}

			events, err := i.readAvailableEvents()
			if err != nil {
				return
			}
			for _, event := range events {
				switch e := event.Unwrap().(type) {
				case coninput.KeyEventRecord:
					for _, msg := range windowsKeyMessages(e) {
						p.Send(msg)
					}
				case coninput.WindowBufferSizeEventRecord:
					if e != windowSize {
						windowSize = e
						p.Send(tea.WindowSizeMsg{
							Width:  int(e.Size.X),
							Height: int(e.Size.Y),
						})
					}
				}
			}
		}
	}()
}

func (i *windowsConsoleInput) close() {
	i.closeOnce.Do(func() {
		close(i.stop)
		_ = windows.SetConsoleMode(i.conin, i.originalMode)
		select {
		case <-i.done:
		case <-time.After(500 * time.Millisecond):
		}
	})
}

func (i *windowsConsoleInput) readAvailableEvents() ([]coninput.InputRecord, error) {
	for {
		select {
		case <-i.stop:
			return nil, fmt.Errorf("windows console input stopped")
		default:
		}

		events, err := coninput.PeekNConsoleInputs(i.conin, 16)
		if err != nil {
			return nil, err
		}
		if len(events) > 0 {
			return coninput.ReadNConsoleInputs(i.conin, uint32(len(events)))
		}
		time.Sleep(16 * time.Millisecond)
	}
}

func windowsKeyMessages(e coninput.KeyEventRecord) []tea.Msg {
	if !e.KeyDown || e.VirtualKeyCode == coninput.VK_SHIFT {
		return nil
	}
	repeat := int(e.RepeatCount)
	if repeat < 1 {
		repeat = 1
	}
	msg, ok := windowsKeyMessage(e)
	if !ok {
		return nil
	}
	msgs := make([]tea.Msg, repeat)
	for idx := range msgs {
		msgs[idx] = msg
	}
	return msgs
}

func windowsKeyMessage(e coninput.KeyEventRecord) (tea.KeyMsg, bool) {
	shift := e.ControlKeyState.Contains(coninput.SHIFT_PRESSED)
	leftCtrl := e.ControlKeyState.Contains(coninput.LEFT_CTRL_PRESSED)
	rightCtrl := e.ControlKeyState.Contains(coninput.RIGHT_CTRL_PRESSED)
	leftAlt := e.ControlKeyState.Contains(coninput.LEFT_ALT_PRESSED)
	rightAlt := e.ControlKeyState.Contains(coninput.RIGHT_ALT_PRESSED)
	altGr := leftCtrl && rightAlt
	ctrl := (leftCtrl || rightCtrl) && !altGr
	alt := (leftAlt || rightAlt) && !altGr

	switch e.VirtualKeyCode {
	case coninput.VK_RETURN:
		if shift && !ctrl && !alt {
			return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("shift+enter")}, true
		}
		return tea.KeyMsg{Type: tea.KeyEnter, Alt: alt}, true
	case coninput.VK_BACK:
		return tea.KeyMsg{Type: tea.KeyBackspace, Alt: alt}, true
	case coninput.VK_TAB:
		if shift {
			return tea.KeyMsg{Type: tea.KeyShiftTab, Alt: alt}, true
		}
		return tea.KeyMsg{Type: tea.KeyTab, Alt: alt}, true
	case coninput.VK_ESCAPE:
		return tea.KeyMsg{Type: tea.KeyEsc, Alt: alt}, true
	case coninput.VK_UP:
		return tea.KeyMsg{Type: windowsArrowKey(tea.KeyUp, tea.KeyCtrlUp, tea.KeyShiftUp, tea.KeyCtrlShiftUp, ctrl, shift), Alt: alt}, true
	case coninput.VK_DOWN:
		return tea.KeyMsg{Type: windowsArrowKey(tea.KeyDown, tea.KeyCtrlDown, tea.KeyShiftDown, tea.KeyCtrlShiftDown, ctrl, shift), Alt: alt}, true
	case coninput.VK_RIGHT:
		return tea.KeyMsg{Type: windowsArrowKey(tea.KeyRight, tea.KeyCtrlRight, tea.KeyShiftRight, tea.KeyCtrlShiftRight, ctrl, shift), Alt: alt}, true
	case coninput.VK_LEFT:
		return tea.KeyMsg{Type: windowsArrowKey(tea.KeyLeft, tea.KeyCtrlLeft, tea.KeyShiftLeft, tea.KeyCtrlShiftLeft, ctrl, shift), Alt: alt}, true
	case coninput.VK_HOME:
		return tea.KeyMsg{Type: windowsArrowKey(tea.KeyHome, tea.KeyCtrlHome, tea.KeyShiftHome, tea.KeyCtrlShiftHome, ctrl, shift), Alt: alt}, true
	case coninput.VK_END:
		return tea.KeyMsg{Type: windowsArrowKey(tea.KeyEnd, tea.KeyCtrlEnd, tea.KeyShiftEnd, tea.KeyCtrlShiftEnd, ctrl, shift), Alt: alt}, true
	case coninput.VK_PRIOR:
		return tea.KeyMsg{Type: tea.KeyPgUp, Alt: alt}, true
	case coninput.VK_NEXT:
		return tea.KeyMsg{Type: tea.KeyPgDown, Alt: alt}, true
	case coninput.VK_DELETE:
		return tea.KeyMsg{Type: tea.KeyDelete, Alt: alt}, true
	case coninput.VK_F1, coninput.VK_F2, coninput.VK_F3, coninput.VK_F4,
		coninput.VK_F5, coninput.VK_F6, coninput.VK_F7, coninput.VK_F8,
		coninput.VK_F9, coninput.VK_F10, coninput.VK_F11, coninput.VK_F12:
		return tea.KeyMsg{Type: tea.KeyType(int(tea.KeyF1) + int(e.VirtualKeyCode-coninput.VK_F1)), Alt: alt}, true
	}

	if ctrl {
		if typ, ok := windowsControlKeyType(e); ok {
			return tea.KeyMsg{Type: typ, Alt: alt}, true
		}
	}
	if e.Char != 0 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{e.Char}, Alt: alt}, true
	}
	return tea.KeyMsg{}, false
}

func windowsArrowKey(plain, ctrlKey, shiftKey, ctrlShiftKey tea.KeyType, ctrl, shift bool) tea.KeyType {
	switch {
	case ctrl && shift:
		return ctrlShiftKey
	case ctrl:
		return ctrlKey
	case shift:
		return shiftKey
	default:
		return plain
	}
}

func windowsControlKeyType(e coninput.KeyEventRecord) (tea.KeyType, bool) {
	switch e.Char {
	case '@', 0:
		return tea.KeyCtrlAt, true
	case '\x01':
		return tea.KeyCtrlA, true
	case '\x02':
		return tea.KeyCtrlB, true
	case '\x03':
		return tea.KeyCtrlC, true
	case '\x04':
		return tea.KeyCtrlD, true
	case '\x05':
		return tea.KeyCtrlE, true
	case '\x06':
		return tea.KeyCtrlF, true
	case '\a':
		return tea.KeyCtrlG, true
	case '\b':
		return tea.KeyCtrlH, true
	case '\t':
		return tea.KeyCtrlI, true
	case '\n':
		return tea.KeyCtrlJ, true
	case '\v':
		return tea.KeyCtrlK, true
	case '\f':
		return tea.KeyCtrlL, true
	case '\r':
		return tea.KeyCtrlM, true
	case '\x0e':
		return tea.KeyCtrlN, true
	case '\x0f':
		return tea.KeyCtrlO, true
	case '\x10':
		return tea.KeyCtrlP, true
	case '\x11':
		return tea.KeyCtrlQ, true
	case '\x12':
		return tea.KeyCtrlR, true
	case '\x13':
		return tea.KeyCtrlS, true
	case '\x14':
		return tea.KeyCtrlT, true
	case '\x15':
		return tea.KeyCtrlU, true
	case '\x16':
		return tea.KeyCtrlV, true
	case '\x17':
		return tea.KeyCtrlW, true
	case '\x18':
		return tea.KeyCtrlX, true
	case '\x19':
		return tea.KeyCtrlY, true
	case '\x1a':
		return tea.KeyCtrlZ, true
	case '\x1b':
		return tea.KeyCtrlOpenBracket, true
	case '\x1c':
		return tea.KeyCtrlBackslash, true
	case '\x1f':
		return tea.KeyCtrlUnderscore, true
	}

	switch e.VirtualKeyCode {
	case coninput.VK_OEM_4:
		return tea.KeyCtrlOpenBracket, true
	case coninput.VK_OEM_6:
		return tea.KeyCtrlCloseBracket, true
	default:
		return 0, false
	}
}
