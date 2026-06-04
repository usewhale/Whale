package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/usewhale/whale/internal/telemetry"
)

const (
	cacheBreakMinTokenDrop = 2000
	cacheBreakMaxReadRatio = 0.95
)

type cacheBreakDetector struct {
	previous map[string]telemetry.UsageRecord
	out      cacheDiagnostics
}

func newCacheBreakDetector() *cacheBreakDetector {
	return &cacheBreakDetector{
		previous: map[string]telemetry.UsageRecord{},
		out:      cacheDiagnostics{Counts: map[string]int{}},
	}
}

func (d *cacheBreakDetector) Add(rec telemetry.UsageRecord) {
	if d == nil {
		return
	}
	key := cacheBreakKey(rec)
	if key == "" {
		return
	}
	if prev, ok := d.previous[key]; ok {
		if br, ok := detectCacheBreak(prev, rec, key); ok {
			d.out.Breaks = appendRecentCacheBreak(d.out.Breaks, br)
			d.out.Counts[br.Cause]++
		}
	}
	d.previous[key] = rec
}

func (d *cacheBreakDetector) Diagnostics() cacheDiagnostics {
	if d == nil {
		return cacheDiagnostics{}
	}
	return d.out
}

func cacheBreakKey(rec telemetry.UsageRecord) string {
	sessionID := strings.TrimSpace(rec.Session)
	kind := strings.TrimSpace(rec.Kind)
	if strings.EqualFold(kind, "subagent") {
		parent := strings.TrimSpace(rec.ParentSessionID)
		if parent == "" {
			parent = legacySubagentParentID(sessionID)
		}
		role := strings.TrimSpace(rec.SubagentRole)
		if role == "" {
			role = "subagent"
		}
		if parent != "" {
			return "subagent:" + parent + ":" + role
		}
	}
	if sessionID == "" {
		return ""
	}
	requestKind := ""
	if rec.CacheShape != nil {
		requestKind = strings.TrimSpace(rec.CacheShape.RequestKind)
	}
	if requestKind == "" {
		requestKind = "agent"
	}
	return "main:" + sessionID + ":" + requestKind
}

func legacySubagentParentID(sessionID string) string {
	if before, _, ok := strings.Cut(strings.TrimSpace(sessionID), "--subagent-"); ok {
		return before
	}
	return ""
}

func detectCacheBreak(prev, cur telemetry.UsageRecord, key string) (cacheBreak, bool) {
	drop := prev.PromptCacheHit - cur.PromptCacheHit
	if prev.PromptCacheHit <= 0 || drop < cacheBreakMinTokenDrop {
		return cacheBreak{}, false
	}
	if float64(cur.PromptCacheHit) >= float64(prev.PromptCacheHit)*cacheBreakMaxReadRatio {
		return cacheBreak{}, false
	}
	cause, details, added, removed, changed := cacheBreakCause(prev, cur)
	return cacheBreak{
		TS:              cur.TS,
		Session:         cur.Session,
		Key:             key,
		Model:           cur.Model,
		RequestKind:     cacheShapeRequestKind(cur.CacheShape),
		PreviousHit:     prev.PromptCacheHit,
		CurrentHit:      cur.PromptCacheHit,
		CurrentMiss:     cur.PromptCacheMiss,
		PromptTokens:    cur.PromptTokens,
		Cause:           cause,
		Details:         details,
		AddedTools:      added,
		RemovedTools:    removed,
		ChangedTools:    changed,
		PreviousRequest: cacheShapeRequestHash(prev.CacheShape),
		CurrentRequest:  cacheShapeRequestHash(cur.CacheShape),
	}, true
}

func cacheBreakCause(prev, cur telemetry.UsageRecord) (string, string, []string, []string, []string) {
	if strings.TrimSpace(prev.Model) != strings.TrimSpace(cur.Model) {
		return "model changed", fmt.Sprintf("%s -> %s", nonEmpty(prev.Model, "(unknown)"), nonEmpty(cur.Model, "(unknown)")), nil, nil, nil
	}
	prevShape := prev.CacheShape
	curShape := cur.CacheShape
	if prevShape == nil || curShape == nil {
		return "shape unavailable", "cache_shape missing on one or both records", nil, nil, nil
	}
	if prevShape.SystemHash != curShape.SystemHash {
		return "system changed", segmentDriftDetail("system", prevShape.SystemSegments, curShape.SystemSegments), nil, nil, nil
	}
	if prevShape.RuntimeHash != curShape.RuntimeHash {
		return "runtime changed", segmentDriftDetail("runtime", prevShape.RuntimeSegments, curShape.RuntimeSegments), nil, nil, nil
	}
	if prevShape.ToolsHash != curShape.ToolsHash {
		added, removed, changed := toolSegmentDiff(prevShape.ToolSegments, curShape.ToolSegments)
		return "tools changed", toolDiffDetail(added, removed, changed), added, removed, changed
	}
	if prevShape.AssistantPrefixHash != curShape.AssistantPrefixHash {
		return "assistant prefix changed", "assistant prefix hash changed", nil, nil, nil
	}
	if prevShape.LogTailHash != curShape.LogTailHash {
		return "history tail changed", fmt.Sprintf("tail messages %d -> %d", prevShape.TailMessages, curShape.TailMessages), nil, nil, nil
	}
	if prevShape.RequestHash != curShape.RequestHash {
		return "request shape changed", fmt.Sprintf("%s -> %s", shortHash(prevShape.RequestHash), shortHash(curShape.RequestHash)), nil, nil, nil
	}
	return "likely server-side or TTL", "prompt shape unchanged", nil, nil, nil
}

func cacheShapeRequestKind(shape *telemetry.CacheShape) string {
	if shape == nil || strings.TrimSpace(shape.RequestKind) == "" {
		return "agent"
	}
	return strings.TrimSpace(shape.RequestKind)
}

func cacheShapeRequestHash(shape *telemetry.CacheShape) string {
	if shape == nil {
		return ""
	}
	return shape.RequestHash
}

func segmentDriftDetail(label string, prev, cur []telemetry.CacheShapeSegment) string {
	_, _, changed := segmentDiff(prev, cur)
	if len(changed) == 0 {
		return label + " hash changed"
	}
	return label + " changed: " + strings.Join(changed, ", ")
}

func toolSegmentDiff(prev, cur []telemetry.CacheShapeSegment) ([]string, []string, []string) {
	added, removed, changed := segmentDiff(prev, cur)
	return sanitizeToolNames(added), sanitizeToolNames(removed), sanitizeToolNames(changed)
}

func segmentDiff(prev, cur []telemetry.CacheShapeSegment) ([]string, []string, []string) {
	prevByName := segmentHashByName(prev)
	curByName := segmentHashByName(cur)
	var added, removed, changed []string
	for name, hash := range curByName {
		if prevHash, ok := prevByName[name]; !ok {
			added = append(added, name)
		} else if prevHash != hash {
			changed = append(changed, name)
		}
	}
	for name := range prevByName {
		if _, ok := curByName[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)
	return added, removed, changed
}

func segmentHashByName(segments []telemetry.CacheShapeSegment) map[string]string {
	out := map[string]string{}
	for _, seg := range segments {
		name := strings.TrimSpace(seg.Name)
		if name == "" {
			name = fmt.Sprintf("segment_%02d", seg.Index)
		}
		out[name] = seg.Hash
	}
	return out
}

func sanitizeToolNames(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, name := range in {
		name = sanitizeToolName(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func sanitizeToolName(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, "mcp__") {
		return "mcp"
	}
	return name
}

func toolDiffDetail(added, removed, changed []string) string {
	var parts []string
	if len(added) > 0 {
		parts = append(parts, "added "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		parts = append(parts, "removed "+strings.Join(removed, ", "))
	}
	if len(changed) > 0 {
		parts = append(parts, "changed "+strings.Join(changed, ", "))
	}
	if len(parts) == 0 {
		return "tool schema/order changed"
	}
	return strings.Join(parts, "; ")
}

func shortHash(hash string) string {
	hash = strings.TrimSpace(hash)
	if len(hash) <= 8 {
		return hash
	}
	return hash[:8]
}

func appendRecentCacheBreak(recent []cacheBreak, br cacheBreak) []cacheBreak {
	recent = append(recent, br)
	sort.Slice(recent, func(i, j int) bool { return recent[i].TS > recent[j].TS })
	return limitSlice(recent, statsRecentLimit)
}

func cacheDiagnosticsSummary(diag cacheDiagnostics) string {
	if len(diag.Breaks) == 0 {
		return ""
	}
	parts := []string{fmt.Sprintf("%d breaks", totalCacheBreaks(diag))}
	for _, kv := range topCounts(diag.Counts, 3) {
		parts = append(parts, fmt.Sprintf("%d %s", kv.Value, kv.Key))
	}
	return strings.Join(parts, " · ")
}

func totalCacheBreaks(diag cacheDiagnostics) int {
	if len(diag.Counts) == 0 {
		return len(diag.Breaks)
	}
	total := 0
	for _, count := range diag.Counts {
		total += count
	}
	return total
}
