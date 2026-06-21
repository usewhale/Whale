package agent

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/usewhale/whale/internal/core"
)

// Layer-1 progress guard. The storm breaker (tool_repair.go) only suppresses
// byte-identical repeated calls; it is blind to "same target, varying args"
// spinning — e.g. re-reading one file line by line with a stepping offset, where
// every call's input differs so nothing ever matches. The progress tracker
// catches no-progress spinning by content coverage, not arg shape:
//
//   - File reads are judged by how many NEW lines they cover. Paging forward
//     through a large file covers a fresh chunk every round, so it is always
//     progress no matter how many rounds it takes. Re-reading a file already
//     seen while adding almost nothing new (the #271 limit-1 stepping loop, or
//     re-reading the same region) is a stall.
//   - Other read-only tools (grep, list_dir, ...) have no line range, so they
//     fall back to target-revisitation frequency within a sliding window.
//
// A round counts as redundant only if it has at least one call and every call is
// a stall. Mutating calls and fresh content are progress and reset the streak.
const (
	// progressWindowSize bounds how many recent non-ranged read-only targets we
	// remember for the frequency fallback.
	progressWindowSize = 24
	// targetRevisitThreshold: a non-ranged read-only call whose target has been
	// seen at least this many times in the window is a stall.
	targetRevisitThreshold = 8
	// minProgressLines: a re-read of an already-seen file that adds fewer than
	// this many new lines is a stall. Set low so genuine paging (which advances
	// by a full page) is never a stall, while the limit-1 stepping loop (1 new
	// line per round) always is.
	minProgressLines = 4
	// defaultReadLines models the read tool's whole-file read when no explicit
	// limit is given, so two whole-file reads of the same file register as
	// overlapping (the second adds no new coverage).
	defaultReadLines = 2000
	// maxConsecutiveRedundantRounds is how many back-to-back redundant rounds
	// end the turn via forced summary.
	maxConsecutiveRedundantRounds = 6
)

// toolTargetKeys are the argument keys that identify what a tool call operates
// on, for the non-ranged frequency fallback. Every present key is joined into
// the identity, so anything that changes the actual scope of a search — path,
// pattern, and the include/glob/name filters — keeps distinct searches distinct.
var toolTargetKeys = []string{"file_path", "path", "notebook_path", "command", "pattern", "url", "query", "search_query", "include", "name"}

// isPollingTool reports whether a tool is a read-only poll of a running task,
// where repeated identical calls are expected progress rather than a loop.
func isPollingTool(name string) bool {
	switch name {
	case "shell_wait":
		return true
	default:
		return false
	}
}

// toolCallTarget derives a stable, name-scoped identity for what a call targets,
// for the frequency fallback. It joins every present identifying arg (path AND
// pattern/query/command/...), so two greps under the same path with different
// patterns are distinct targets — each fresh search is progress, not a revisit.
// Incidental paging args (offset/limit) are deliberately excluded. Falls back to
// the full input when no known key is present (then it behaves like exact-args).
func toolCallTarget(c core.ToolCall) string {
	args := map[string]any{}
	if err := json.Unmarshal([]byte(c.Input), &args); err == nil {
		parts := make([]string, 0, len(toolTargetKeys))
		for _, k := range toolTargetKeys {
			if s, ok := args[k].(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, k+"="+strings.TrimSpace(s))
			}
		}
		if len(parts) > 0 {
			return c.Name + "::" + strings.Join(parts, "&")
		}
	}
	return c.Name + "::" + strings.TrimSpace(c.Input)
}

// readFileRange extracts the file path and [offset, offset+limit) line range of
// a ranged file read. It deliberately matches only read_file-style calls: a
// file_path/notebook_path AND an explicit offset or limit. ok is false for
// anything else — whole-file reads (no range) and search tools like grep that
// merely take a generic path — so those go through the frequency fallback and
// distinct queries are not mistaken for re-reads of the same [0,2000) range.
func readFileRange(c core.ToolCall) (key string, start, end int, ok bool) {
	args := map[string]any{}
	if err := json.Unmarshal([]byte(c.Input), &args); err != nil {
		return "", 0, 0, false
	}
	var path string
	for _, k := range []string{"file_path", "notebook_path"} {
		if v, ok := args[k].(string); ok && strings.TrimSpace(v) != "" {
			path = strings.TrimSpace(v)
			break
		}
	}
	if path == "" {
		return "", 0, 0, false
	}
	_, hasOffset := args["offset"]
	_, hasLimit := args["limit"]
	if !hasOffset && !hasLimit {
		return "", 0, 0, false
	}
	offset := jsonInt(args["offset"], 0)
	if offset < 0 {
		offset = 0
	}
	limit := jsonInt(args["limit"], defaultReadLines)
	if limit <= 0 {
		limit = defaultReadLines
	}
	return c.Name + "::" + path, offset, offset + limit, true
}

func jsonInt(v any, def int) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	return def
}

// resultReturnedLines reads the read tool's own returned_lines metric from a
// tool result so coverage reflects what the tool actually produced, not what the
// model requested. ok is false when the metric is absent (then the caller falls
// back to the requested range).
func resultReturnedLines(tr core.ToolResult) (int, bool) {
	pl, ok := tr.Payload.(map[string]any)
	if !ok {
		return 0, false
	}
	metrics, ok := pl["metrics"].(map[string]any)
	if !ok {
		return 0, false
	}
	v, ok := metrics["returned_lines"]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	default:
		return 0, false
	}
}

// lineCoverage tracks the union of line ranges read for one file across a turn
// as sorted, non-overlapping half-open intervals.
type lineCoverage struct {
	ivs []interval
}

type interval struct{ start, end int }

// addAndCountNew records [start, end) and returns how many of its lines were not
// already covered.
func (lc *lineCoverage) addAndCountNew(start, end int) int {
	if end <= start {
		return 0
	}
	newCount := end - start
	for _, iv := range lc.ivs {
		lo, hi := max(start, iv.start), min(end, iv.end)
		if hi > lo {
			newCount -= hi - lo
		}
	}
	if newCount < 0 {
		newCount = 0
	}
	lc.ivs = append(lc.ivs, interval{start, end})
	lc.merge()
	return newCount
}

func (lc *lineCoverage) merge() {
	if len(lc.ivs) < 2 {
		return
	}
	sort.Slice(lc.ivs, func(i, j int) bool { return lc.ivs[i].start < lc.ivs[j].start })
	merged := lc.ivs[:1]
	for _, iv := range lc.ivs[1:] {
		last := &merged[len(merged)-1]
		if iv.start <= last.end {
			if iv.end > last.end {
				last.end = iv.end
			}
			continue
		}
		merged = append(merged, iv)
	}
	lc.ivs = merged
}

// progressTracker watches tool-call progress across a turn to detect no-progress
// spinning.
type progressTracker struct {
	recentTargets []string
	covered       map[string]*lineCoverage
}

func (p *progressTracker) reset() {
	if p == nil {
		return
	}
	p.recentTargets = p.recentTargets[:0]
	p.covered = nil
}

// observe records one round of tool calls (with their results) and reports
// whether the round was redundant: it had at least one call and every call was
// a stall.
func (p *progressTracker) observe(calls []core.ToolCall, results []core.ToolResult, readOnly func(core.ToolCall) bool) bool {
	if p == nil || len(calls) == 0 {
		return false
	}
	resByID := make(map[string]core.ToolResult, len(results))
	for _, r := range results {
		resByID[r.ToolCallID] = r
	}
	redundant := true
	for _, c := range calls {
		res, hasRes := resByID[c.ID]
		if !p.stalled(c, res, hasRes, readOnly) {
			redundant = false
		}
	}
	return redundant
}

// stalled reports whether a single call made no real progress. A mutating call
// is never a stall. A ranged file read is a stall only when it re-reads an
// already-seen file and adds fewer than minProgressLines new lines — measured by
// the lines the tool actually RETURNED, not the requested range, so a read past
// EOF (fresh offset, empty result) counts as no progress. The first read of any
// file, and every forward page that returns a full chunk, count as progress.
// Every other read-only call (whole-file reads, grep, list_dir, ...) falls back
// to target-revisitation frequency.
func (p *progressTracker) stalled(c core.ToolCall, res core.ToolResult, hasRes bool, readOnly func(core.ToolCall) bool) bool {
	if readOnly == nil || !readOnly(c) {
		return false
	}
	// Polling tools (e.g. shell_wait) are read-only and legitimately called
	// many times with identical args while a background command runs — each
	// poll may surface new output or eventual completion. Frequency-based stall
	// detection would mistake that progress for a loop, so exempt them.
	if isPollingTool(c.Name) {
		return false
	}
	if key, start, end, ok := readFileRange(c); ok {
		if p.covered == nil {
			p.covered = map[string]*lineCoverage{}
		}
		cov, seen := p.covered[key]
		if !seen {
			cov = &lineCoverage{}
			p.covered[key] = cov
		}
		// Clamp the covered range to what the tool actually returned: a read
		// past EOF requests a fresh range but yields nothing, so it advances
		// coverage by zero and must register as a stall.
		covEnd := end
		if hasRes {
			if got, ok := resultReturnedLines(res); ok {
				covEnd = start + got
			} else if res.IsError() {
				covEnd = start
			}
		}
		if covEnd > end {
			covEnd = end
		}
		newLines := cov.addAndCountNew(start, covEnd)
		if !seen {
			return false
		}
		return newLines < minProgressLines
	}
	target := toolCallTarget(c)
	count := 0
	for _, t := range p.recentTargets {
		if t == target {
			count++
		}
	}
	p.recentTargets = append(p.recentTargets, target)
	if len(p.recentTargets) > progressWindowSize {
		p.recentTargets = p.recentTargets[len(p.recentTargets)-progressWindowSize:]
	}
	return count >= targetRevisitThreshold
}
