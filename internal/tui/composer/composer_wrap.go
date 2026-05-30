package composer

import (
	"strings"
	"unicode"

	rw "github.com/mattn/go-runewidth"
)

type composerLineSegment struct {
	text       string
	start, end int
}

func wrapComposerLine(line string, width int) []composerLineSegment {
	runes := []rune(line)
	if len(runes) == 0 {
		return []composerLineSegment{{text: "", start: 0, end: 0}}
	}
	if width <= 0 {
		return []composerLineSegment{{text: line, start: 0, end: len(runes)}}
	}
	segments := []composerLineSegment{}
	start := 0
	cells := 0
	for i, r := range runes {
		w := rw.RuneWidth(r)
		if w < 0 {
			w = 0
		}
		if cells > 0 && cells+w > width {
			segments = append(segments, composerLineSegment{
				text:  string(runes[start:i]),
				start: start,
				end:   i,
			})
			start = i
			cells = 0
		}
		cells += w
	}
	segments = append(segments, composerLineSegment{
		text:  string(runes[start:]),
		start: start,
		end:   len(runes),
	})
	return segments
}

func (c Composer) visualLineCount() int {
	width := c.textarea.Width()
	lines := splitComposerLines(c.rawValue())
	if width <= 0 {
		return len(lines)
	}
	cursorLine := c.textarea.Line()
	total := 0
	for i, line := range lines {
		if i == cursorLine {
			total += wrappedLineCount([]rune(line), width)
			continue
		}
		key := wrapCacheKey{line: line, width: width}
		if c.wrapCache != nil {
			if count, ok := c.wrapCache[key]; ok {
				total += count
				continue
			}
		}
		count := wrappedLineCount([]rune(line), width)
		if c.wrapCache != nil && len(c.wrapCache) < wrapCacheMaxEntries {
			c.wrapCache[key] = count
		}
		total += count
	}
	return total
}

func wrappedLineCount(runes []rune, width int) int {
	if width <= 0 {
		return 1
	}
	var (
		lines         = 1
		rowWidth      int
		wordWidth     int
		lastCharWidth int
		spaces        int
	)

	flushWord := func() {
		if wordWidth == 0 && spaces == 0 {
			return
		}
		if spaces > 0 {
			if rowWidth+wordWidth+spaces > width {
				lines++
				rowWidth = wordWidth + spaces
			} else {
				rowWidth += wordWidth + spaces
			}
			wordWidth = 0
			lastCharWidth = 0
			spaces = 0
			return
		}
		// Handle very long words that exceed the wrap width.
		if wordWidth+lastCharWidth > width {
			if rowWidth > 0 {
				lines++
				rowWidth = 0
			}
			rowWidth = wordWidth
			wordWidth = 0
			lastCharWidth = 0
		}
	}

	for _, r := range runes {
		w := rw.RuneWidth(r)
		if w < 0 {
			w = 0
		}
		if unicode.IsSpace(r) {
			spaces++
		} else {
			wordWidth += w
			lastCharWidth = w
		}
		flushWord()
	}

	if rowWidth+wordWidth+spaces >= width {
		lines++
	}

	return lines
}

func repeatSpaces(n int) []rune {
	return []rune(strings.Repeat(" ", n))
}

func (c *Composer) reflow() {
	// Prime the rawValue cache once for this reflow; visualLineCount (and
	// any other rawValue caller below) will hit the cache instead of
	// repeatedly walking the textarea's line storage.
	c.primeRawCache()
	// Lazily seed the wrap cache here (pointer receiver) so that subsequent
	// value-receiver visualLineCount calls share the same backing map and
	// their inserts persist. Bound the size to keep memory predictable on
	// long-running sessions.
	if c.wrapCache == nil || len(c.wrapCache) >= wrapCacheMaxEntries {
		c.wrapCache = make(map[wrapCacheKey]int)
	}
	height := c.visualLineCount()
	if height < 1 {
		height = 1
	}
	if height > composerCollapseThreshold {
		height = composerCollapseThreshold
	}
	if height != c.textarea.Height() {
		c.textarea.SetHeight(height)
	}
}
