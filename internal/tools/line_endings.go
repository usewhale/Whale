package tools

import "strings"

type lineEndingStyle string

const (
	lineEndingLF   lineEndingStyle = "\n"
	lineEndingCRLF lineEndingStyle = "\r\n"
	lineEndingCR   lineEndingStyle = "\r"
)

type lineEndingSnapshot struct {
	style    lineEndingStyle
	mixed    bool
	lines    []lineEndingLine
	bom      bool
	encoding textEncoding
}

var utf8BOMBytes = []byte{0xEF, 0xBB, 0xBF}

func normalizeTextFileBytes(data []byte) (string, lineEndingSnapshot) {
	bom := len(data) >= len(utf8BOMBytes) &&
		data[0] == utf8BOMBytes[0] &&
		data[1] == utf8BOMBytes[1] &&
		data[2] == utf8BOMBytes[2]
	if bom {
		data = data[len(utf8BOMBytes):]
	}
	decoded, encoding := decodeTextFileBytes(data)
	text, snapshot := normalizeLineEndings(decoded)
	snapshot.bom = bom
	snapshot.encoding = encoding
	return text, snapshot
}

func restoreTextFileBytes(s string, snapshot lineEndingSnapshot) []byte {
	text := restoreLineEndings(s, snapshot)
	encoded := encodeTextFileString(text, snapshot.encoding)
	if !snapshot.bom {
		return encoded
	}
	out := make([]byte, 0, len(utf8BOMBytes)+len(encoded))
	out = append(out, utf8BOMBytes...)
	out = append(out, encoded...)
	return out
}

type lineEndingLine struct {
	text string
	sep  string
}

type lineEndingMatch struct {
	before int
	after  int
}

const maxLineEndingDiffCells = 4_000_000
const maxLineEndingMyersEdits = 1_024

func normalizeLineEndings(s string) (string, lineEndingSnapshot) {
	style, mixed := detectLineEndingStyle(s)
	if !mixed {
		switch style {
		case lineEndingCRLF:
			return strings.ReplaceAll(s, "\r\n", "\n"), lineEndingSnapshot{style: style}
		case lineEndingCR:
			return strings.ReplaceAll(s, "\r", "\n"), lineEndingSnapshot{style: style}
		default:
			return s, lineEndingSnapshot{style: lineEndingLF}
		}
	}
	lines, normalized := scanLineEndings(s)
	return normalized, lineEndingSnapshot{style: lineEndingLF, mixed: true, lines: lines}
}

func detectLineEndingStyle(s string) (lineEndingStyle, bool) {
	first := lineEndingStyle("")
	for i := 0; i < len(s); i++ {
		sep := lineEndingStyle("")
		switch s[i] {
		case '\r':
			if i+1 < len(s) && s[i+1] == '\n' {
				sep = lineEndingCRLF
				i++
			} else {
				sep = lineEndingCR
			}
		case '\n':
			sep = lineEndingLF
		}
		if sep == "" {
			continue
		}
		if first == "" {
			first = sep
			continue
		}
		if sep != first {
			return lineEndingLF, true
		}
	}
	if first == "" {
		return lineEndingLF, false
	}
	return first, false
}

func scanLineEndings(s string) ([]lineEndingLine, string) {
	lines := make([]lineEndingLine, 0)
	var line strings.Builder
	var normalized strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\r':
			sep := "\r"
			if i+1 < len(s) && s[i+1] == '\n' {
				sep = "\r\n"
				i++
			}
			text := line.String()
			lines = append(lines, lineEndingLine{text: text, sep: sep})
			normalized.WriteString(text)
			normalized.WriteByte('\n')
			line.Reset()
		case '\n':
			text := line.String()
			lines = append(lines, lineEndingLine{text: text, sep: "\n"})
			normalized.WriteString(text)
			normalized.WriteByte('\n')
			line.Reset()
		default:
			line.WriteByte(s[i])
		}
	}
	if line.Len() > 0 {
		text := line.String()
		lines = append(lines, lineEndingLine{text: text})
		normalized.WriteString(text)
	}
	return lines, normalized.String()
}

func splitNormalizedLineText(s string) ([]string, bool) {
	if s == "" {
		return nil, false
	}
	hadTrailing := strings.HasSuffix(s, "\n")
	if hadTrailing {
		return strings.Split(strings.TrimSuffix(s, "\n"), "\n"), true
	}
	return strings.Split(s, "\n"), false
}

func normalizeLineEndingText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

func restoreLineEndings(s string, snapshot lineEndingSnapshot) string {
	if snapshot.mixed {
		return restoreMixedLineEndings(s, snapshot)
	}
	switch snapshot.style {
	case lineEndingCRLF:
		return strings.ReplaceAll(s, "\n", "\r\n")
	case lineEndingCR:
		return strings.ReplaceAll(s, "\n", "\r")
	default:
		return s
	}
}

func restoreMixedLineEndings(s string, snapshot lineEndingSnapshot) string {
	afterLines, hadTrailing := splitNormalizedLineText(s)
	if len(afterLines) == 0 {
		return ""
	}
	seps := make([]string, len(afterLines))
	for i := range seps {
		seps[i] = "\n"
	}
	if !hadTrailing {
		seps[len(seps)-1] = ""
	}
	applyLineEndingMatches(snapshot.lines, afterLines, seps)
	var out strings.Builder
	for i, line := range afterLines {
		out.WriteString(line)
		sep := seps[i]
		if i == len(afterLines)-1 && !hadTrailing {
			sep = ""
		} else if sep == "" {
			sep = "\n"
		}
		out.WriteString(sep)
	}
	return out.String()
}

func applyLineEndingMatches(before []lineEndingLine, after []string, seps []string) {
	matches := lineEndingMatches(before, after)
	beforeStart, afterStart := 0, 0
	for _, match := range append(matches, lineEndingMatch{before: len(before), after: len(after)}) {
		copyReplacementSeparators(before[beforeStart:match.before], seps[afterStart:match.after])
		if match.before < len(before) && match.after < len(after) {
			seps[match.after] = before[match.before].sep
		}
		beforeStart = match.before + 1
		afterStart = match.after + 1
	}
}

func copyReplacementSeparators(before []lineEndingLine, afterSeps []string) {
	for i := 0; i < len(before) && i < len(afterSeps); i++ {
		afterSeps[i] = before[i].sep
	}
}

func lineEndingMatches(before []lineEndingLine, after []string) []lineEndingMatch {
	if len(before) == 0 || len(after) == 0 {
		return nil
	}
	if len(before)*len(after) > maxLineEndingDiffCells {
		if matches, ok := myersLineEndingMatches(before, after, maxLineEndingMyersEdits); ok {
			return matches
		}
		return expandedUniqueLineEndingMatches(before, after)
	}
	return lcsLineEndingMatches(before, after)
}

func myersLineEndingMatches(before []lineEndingLine, after []string, maxEdits int) ([]lineEndingMatch, bool) {
	prefix := commonLineEndingPrefix(before, after)
	beforeTail := before[prefix:]
	afterTail := after[prefix:]
	suffix := commonLineEndingSuffix(beforeTail, afterTail)

	matches := make([]lineEndingMatch, 0, prefix+suffix)
	for i := 0; i < prefix; i++ {
		matches = append(matches, lineEndingMatch{before: i, after: i})
	}
	coreBeforeEnd := len(before) - suffix
	coreAfterEnd := len(after) - suffix
	core, ok := myersLineEndingMatchesCore(before[prefix:coreBeforeEnd], after[prefix:coreAfterEnd], maxEdits)
	if !ok {
		return nil, false
	}
	for _, match := range core {
		matches = append(matches, lineEndingMatch{before: prefix + match.before, after: prefix + match.after})
	}
	for i := suffix; i > 0; i-- {
		matches = append(matches, lineEndingMatch{before: len(before) - i, after: len(after) - i})
	}
	return matches, true
}

func myersLineEndingMatchesCore(before []lineEndingLine, after []string, maxEdits int) ([]lineEndingMatch, bool) {
	n, m := len(before), len(after)
	if n == 0 || m == 0 {
		return nil, n == m
	}
	max := n + m
	if maxEdits > max {
		maxEdits = max
	}
	offset := maxEdits + 1
	width := 2*maxEdits + 3
	v := make([]int, width)
	trace := make([][]int, 0, maxEdits+1)
	for d := 0; d <= maxEdits; d++ {
		next := make([]int, width)
		for k := -d; k <= d; k += 2 {
			idx := offset + k
			x := 0
			if k == -d || (k != d && v[idx-1] < v[idx+1]) {
				x = v[idx+1]
			} else {
				x = v[idx-1] + 1
			}
			y := x - k
			for x < n && y < m && before[x].text == after[y] {
				x++
				y++
			}
			next[idx] = x
			if x >= n && y >= m {
				trace = append(trace, next)
				return backtrackMyersLineEndingMatches(trace, offset, n, m), true
			}
		}
		trace = append(trace, next)
		v = next
	}
	return nil, false
}

func backtrackMyersLineEndingMatches(trace [][]int, offset, x, y int) []lineEndingMatch {
	matches := make([]lineEndingMatch, 0)
	for d := len(trace) - 1; d > 0; d-- {
		prev := trace[d-1]
		k := x - y
		var prevK int
		if k == -d || (k != d && prev[offset+k-1] < prev[offset+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := prev[offset+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			matches = append(matches, lineEndingMatch{before: x, after: y})
		}
		x = prevX
		y = prevY
	}
	for x > 0 && y > 0 {
		x--
		y--
		matches = append(matches, lineEndingMatch{before: x, after: y})
	}
	for i, j := 0, len(matches)-1; i < j; i, j = i+1, j-1 {
		matches[i], matches[j] = matches[j], matches[i]
	}
	return matches
}

func lcsLineEndingMatches(before []lineEndingLine, after []string) []lineEndingMatch {
	prefix := commonLineEndingPrefix(before, after)
	beforeTail := before[prefix:]
	afterTail := after[prefix:]
	suffix := commonLineEndingSuffix(beforeTail, afterTail)

	matches := make([]lineEndingMatch, 0, prefix+suffix)
	for i := 0; i < prefix; i++ {
		matches = append(matches, lineEndingMatch{before: i, after: i})
	}
	coreBeforeEnd := len(before) - suffix
	coreAfterEnd := len(after) - suffix
	for _, match := range lcsLineEndingMatchesCore(before[prefix:coreBeforeEnd], after[prefix:coreAfterEnd]) {
		matches = append(matches, lineEndingMatch{before: prefix + match.before, after: prefix + match.after})
	}
	for i := suffix; i > 0; i-- {
		matches = append(matches, lineEndingMatch{before: len(before) - i, after: len(after) - i})
	}
	return matches
}

func lcsLineEndingMatchesCore(before []lineEndingLine, after []string) []lineEndingMatch {
	if len(before) == 0 || len(after) == 0 {
		return nil
	}
	width := len(after) + 1
	cells := make([]int, (len(before)+1)*width)
	for i := len(before) - 1; i >= 0; i-- {
		for j := len(after) - 1; j >= 0; j-- {
			idx := i*width + j
			if before[i].text == after[j] {
				cells[idx] = cells[(i+1)*width+j+1] + 1
				continue
			}
			if cells[(i+1)*width+j] >= cells[i*width+j+1] {
				cells[idx] = cells[(i+1)*width+j]
			} else {
				cells[idx] = cells[i*width+j+1]
			}
		}
	}
	matches := make([]lineEndingMatch, 0, cells[0])
	for i, j := 0, 0; i < len(before) && j < len(after); {
		switch {
		case before[i].text == after[j]:
			matches = append(matches, lineEndingMatch{before: i, after: j})
			i++
			j++
		case cells[(i+1)*width+j] >= cells[i*width+j+1]:
			i++
		default:
			j++
		}
	}
	return matches
}

func uniqueLineEndingMatches(before []lineEndingLine, after []string) []lineEndingMatch {
	beforeCounts := map[string]int{}
	afterCounts := map[string]int{}
	afterIndex := map[string]int{}
	for _, line := range before {
		beforeCounts[line.text]++
	}
	for i, line := range after {
		afterCounts[line]++
		afterIndex[line] = i
	}
	matches := make([]lineEndingMatch, 0)
	lastAfter := -1
	for i, line := range before {
		if beforeCounts[line.text] != 1 || afterCounts[line.text] != 1 {
			continue
		}
		j := afterIndex[line.text]
		if j <= lastAfter {
			continue
		}
		matches = append(matches, lineEndingMatch{before: i, after: j})
		lastAfter = j
	}
	return matches
}

func expandedUniqueLineEndingMatches(before []lineEndingLine, after []string) []lineEndingMatch {
	anchors := uniqueLineEndingMatches(before, after)
	out := make([]lineEndingMatch, 0, len(anchors))
	beforeStart, afterStart := 0, 0
	for _, anchor := range append(anchors, lineEndingMatch{before: len(before), after: len(after)}) {
		beforeEnd, afterEnd := anchor.before, anchor.after
		prefix := commonLineEndingPrefix(before[beforeStart:beforeEnd], after[afterStart:afterEnd])
		for i := 0; i < prefix; i++ {
			out = append(out, lineEndingMatch{before: beforeStart + i, after: afterStart + i})
		}
		beforeSuffixStart := beforeStart + prefix
		afterSuffixStart := afterStart + prefix
		suffix := commonLineEndingSuffix(before[beforeSuffixStart:beforeEnd], after[afterSuffixStart:afterEnd])
		for i := suffix; i > 0; i-- {
			out = append(out, lineEndingMatch{before: beforeEnd - i, after: afterEnd - i})
		}
		if anchor.before < len(before) && anchor.after < len(after) {
			out = append(out, anchor)
		}
		beforeStart = anchor.before + 1
		afterStart = anchor.after + 1
	}
	return out
}

func commonLineEndingPrefix(before []lineEndingLine, after []string) int {
	n := min(len(before), len(after))
	for i := 0; i < n; i++ {
		if before[i].text != after[i] {
			return i
		}
	}
	return n
}

func commonLineEndingSuffix(before []lineEndingLine, after []string) int {
	n := min(len(before), len(after))
	for i := 0; i < n; i++ {
		if before[len(before)-1-i].text != after[len(after)-1-i] {
			return i
		}
	}
	return n
}
