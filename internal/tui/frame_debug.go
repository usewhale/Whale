package tui

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	xansi "github.com/charmbracelet/x/ansi"
)

// Frame instrumentation gated on WHALE_FRAME_DEBUG. Value is a path to append
// per-frame stats to (one CSV row per View()). The special value "stderr"
// writes to standard error, which is only visible after the TUI exits since
// Bubble Tea owns the alt-screen.
//
// Columns: t_ms,dur_us,bytes,visible_bytes,lines,page,width,height,goos
//
// Disabled by default; the fast path is a single atomic-ish nil check.
var (
	frameDebugOnce sync.Once
	frameDebugMu   sync.Mutex
	frameDebugW    *os.File
	frameDebugT0   time.Time
)

func frameDebugSink() *os.File {
	frameDebugOnce.Do(func() {
		path := strings.TrimSpace(os.Getenv("WHALE_FRAME_DEBUG"))
		if path == "" {
			return
		}
		var f *os.File
		var err error
		if strings.EqualFold(path, "stderr") {
			f = os.Stderr
		} else {
			f, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				return
			}
		}
		frameDebugW = f
		frameDebugT0 = time.Now()
		fmt.Fprintf(f, "# whale frame debug start %s goos=%s\n", frameDebugT0.Format(time.RFC3339Nano), runtime.GOOS)
		fmt.Fprintln(f, "t_ms,dur_us,bytes,visible_bytes,lines,page,width,height")
	})
	return frameDebugW
}

func recordFrame(start time.Time, frame string, p page, width, height int) {
	sink := frameDebugSink()
	if sink == nil {
		return
	}
	dur := time.Since(start)
	bytesTotal := len(frame)
	visible := len(xansi.Strip(frame))
	lines := 1 + strings.Count(frame, "\n")
	pageName := "chat"
	switch p {
	case pageLogs:
		pageName = "logs"
	case pageDiff:
		pageName = "diff"
	}
	frameDebugMu.Lock()
	fmt.Fprintf(sink, "%d,%d,%d,%d,%d,%s,%d,%d\n",
		time.Since(frameDebugT0).Milliseconds(),
		dur.Microseconds(),
		bytesTotal,
		visible,
		lines,
		pageName,
		width,
		height,
	)
	frameDebugMu.Unlock()
}
