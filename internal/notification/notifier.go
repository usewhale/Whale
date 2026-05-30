// Package notification sends macOS desktop notifications via terminal OSC
// escape sequences. It relies on the terminal emulator (Ghostty, Kitty,
// iTerm2, WezTerm, foot) to convert the sequences into native macOS
// notifications.
//
// Supported terminals (auto-detected):
//   - Ghostty:  OSC 777 notify
//   - Kitty:    OSC 99 (three-part protocol)
//   - iTerm2:   OSC 9 (legacy)
//   - WezTerm:  OSC 9
//   - foot:     OSC 9
//
// Unsupported terminals fall back to BEL (\x07).
package notification

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Notifier handles desktop notifications for the TUI.
type Notifier struct {
	mu                 sync.Mutex
	enabled            bool
	lastNotifyTurn     time.Time
	lastNotifyApproval time.Time
	minInterval        time.Duration // minimum gap between notifications

	writer io.Writer
	term   terminalKind
}

// Notification carries the content for a desktop notification.
type Notification struct {
	Title   string
	Message string
	Kind    string // "turn_complete" or "approval_request"
}

type terminalKind int

const (
	termUnknown terminalKind = iota
	termGhostty
	termKitty
	termOSC9 // iTerm2, WezTerm, foot — all speak OSC 9
	termBEL  // fallback: just terminal bell
)

const defaultMinInterval = 10 * time.Second

// New creates a Notifier, auto-detecting the terminal.
func New() *Notifier {
	return &Notifier{
		enabled:     true,
		writer:      os.Stderr,
		term:        detectTerminal(),
		minInterval: defaultMinInterval,
	}
}

// SetEnabled controls whether notifications are sent.
func (n *Notifier) SetEnabled(v bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.enabled = v
}

// SetMinInterval sets the minimum gap between notifications.
func (n *Notifier) SetMinInterval(d time.Duration) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.minInterval = d
}

// SendTurnDone fires a desktop notification when a turn completes.
// It respects the minimum interval and will no-op if called too soon.
func (n *Notifier) SendTurnDone(summary string) {
	n.mu.Lock()
	enabled := n.enabled
	if !enabled {
		n.mu.Unlock()
		return
	}
	now := time.Now()
	if !n.lastNotifyTurn.IsZero() && now.Before(n.lastNotifyTurn.Add(n.minInterval)) {
		n.mu.Unlock()
		return
	}
	n.lastNotifyTurn = now
	term := n.term
	w := n.writer
	n.mu.Unlock()

	if summary == "" {
		summary = "Task completed"
	}
	notif := Notification{
		Title:   "Whale",
		Message: truncate(summary, 200),
		Kind:    "turn_complete",
	}
	writeSeq(w, term, notif)
}

// SendApprovalRequired fires a desktop notification when the user's
// approval is needed for a tool call.
func (n *Notifier) SendApprovalRequired(toolName, reason string) {
	n.mu.Lock()
	enabled := n.enabled
	if !enabled {
		n.mu.Unlock()
		return
	}
	now := time.Now()
	if !n.lastNotifyApproval.IsZero() && now.Before(n.lastNotifyApproval.Add(n.minInterval)) {
		n.mu.Unlock()
		return
	}
	n.lastNotifyApproval = now
	term := n.term
	w := n.writer
	n.mu.Unlock()

	msg := toolName
	if reason != "" {
		msg = toolName + ": " + truncate(reason, 100)
	}
	notif := Notification{
		Title:   "Whale — Approval Required",
		Message: msg,
		Kind:    "approval_request",
	}
	writeSeq(w, term, notif)
}

// writeSeq writes the appropriate OSC escape sequence for the terminal.
func writeSeq(w io.Writer, term terminalKind, n Notification) {
	switch term {
	case termGhostty:
		// ESC ] 777 ; notify ; title ; message BEL
		fmt.Fprintf(w, "\x1b]777;notify;%s;%s\x07",
			escapeOSC(n.Title), escapeOSC(n.Message))

	case termKitty:
		// Kitty uses a three-part OSC 99 protocol:
		//   1. Set title  (d=0)
		//   2. Set body   (p=body)
		//   3. Fire      (d=1, a=focus)
		id := time.Now().UnixMilli() % 10000
		fmt.Fprintf(w, "\x1b]99;i=%d:d=0:p=title;%s\x07", id, escapeOSC(n.Title))
		fmt.Fprintf(w, "\x1b]99;i=%d:p=body;%s\x07", id, escapeOSC(n.Message))
		fmt.Fprintf(w, "\x1b]99;i=%d:d=1:a=focus;\x07", id)

	case termOSC9:
		// ESC ] 9 ; message BEL  — iTerm2/WezTerm/foot
		text := n.Message
		if n.Title != "" {
			text = n.Title + ": " + n.Message
		}
		fmt.Fprintf(w, "\x1b]9;%s\x07", escapeOSC(text))

	default:
		// BEL — audible beep
		fmt.Fprint(w, "\x07")
	}
}

// detectTerminal identifies the terminal emulator from environment variables.
func detectTerminal() terminalKind {
	if v := os.Getenv("TERM_PROGRAM"); v != "" {
		switch v {
		case "ghostty":
			return termGhostty
		case "kitty":
			return termKitty
		case "iTerm.app":
			return termOSC9
		case "WezTerm":
			return termOSC9
		}
	}
	if os.Getenv("ITERM_SESSION_ID") != "" {
		return termOSC9
	}
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return termKitty
	}
	switch os.Getenv("TERM") {
	case "xterm-kitty":
		return termKitty
	case "wezterm", "foot":
		return termOSC9
	}
	if strings.Contains(os.Getenv("TERM"), "kitty") {
		return termKitty
	}
	return termBEL
}

// TerminalDescription returns a human-readable name of the detected terminal.
func TerminalDescription() string {
	switch detectTerminal() {
	case termGhostty:
		return "Ghostty"
	case termKitty:
		return "Kitty"
	case termOSC9:
		return "OSC 9 compatible (iTerm2/WezTerm/foot)"
	case termBEL:
		return "terminal bell (no desktop notification support)"
	default:
		return "unknown"
	}
}

// escapeOSC escapes backslashes and semicolons and strips control characters
// for safe embedding in terminal OSC sequences. Control characters such as
// BEL (\x07) or ESC (\x1b) would otherwise terminate the sequence early and
// allow injection of arbitrary terminal control sequences.
func escapeOSC(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ";", "\\;")
	// Strip control characters (0x00-0x08, 0x0B-0x0C, 0x0E-0x1F, 0x7F) that
	// could prematurely terminate the OSC sequence or inject escape sequences.
	// Keep \t (0x09), \n (0x0A), \r (0x0D) since they are harmless in
	// notification text and may appear in model output.
	return strings.Map(func(r rune) rune {
		if r == 0x09 || r == 0x0a || r == 0x0d {
			return r
		}
		if r <= 0x1f || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// truncate shortens a string to at most n runes.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	// Find the last space within the first n runes (in rune-space, not byte-space)
	// so that multibyte text like CJK or emoji doesn't skew the comparison.
	spaceAt := -1
	for i := 0; i < n && i < len(runes); i++ {
		if runes[i] == ' ' {
			spaceAt = i
		}
	}
	if spaceAt > n/2 {
		return string(runes[:spaceAt]) + "…"
	}
	return string(runes[:n]) + "…"
}
