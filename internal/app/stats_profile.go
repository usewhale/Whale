package app

import (
	"bufio"
	"encoding/json"
	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/session"
	"github.com/usewhale/whale/internal/telemetry"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func readProfileStats(sessionsDir, usagePath string, limit int) profileStats {
	if limit <= 0 {
		limit = statsProfileSessionLimit
	}
	stats := profileStats{
		Limit:                limit,
		PrefixFingerprints:   map[string]bool{},
		ProviderPrefixHashes: map[string]bool{},
		PrefixShapeSessions:  map[string]bool{},
		ByTool:               map[string]*profileToolStats{},
	}
	files := latestProfileSessionFiles(sessionsDir, limit)
	sessionIndex := map[string]int{}
	for _, file := range files {
		sp := readProfileSessionFile(file.Path, file.ID, file.ModTime)
		stats.Sessions = append(stats.Sessions, sp)
		sessionIndex[sp.ID] = len(stats.Sessions) - 1
		stats.ToolCalls += sp.ToolCalls
		stats.ToolResultChars += sp.ToolResultChars
		stats.ReasoningChars += sp.ReasoningChars
		stats.VisibleTextChars += sp.VisibleTextChars
		for _, tool := range sp.ByTool {
			dst := stats.ByTool[tool.Tool]
			if dst == nil {
				dst = &profileToolStats{Tool: tool.Tool}
				stats.ByTool[tool.Tool] = dst
			}
			dst.Calls += tool.Calls
			dst.ResultChars += tool.ResultChars
		}
		if sp.Trivial {
			stats.TrivialSessions++
		}
		if sp.ToolResultChars >= statsProfileToolHeavyChars {
			stats.ToolHeavySessions++
		}
	}

	childSessionIndex := profileChildSessionIndex(sessionsDir, sessionIndex)
	for _, parentIdx := range childSessionIndex {
		stats.Sessions[parentIdx].SubagentSessions++
		stats.SubagentSessions++
	}

	for _, path := range profileUsagePaths(usagePath) {
		readProfileUsage(path, sessionIndex, childSessionIndex, &stats)
	}
	readProfileApprovalEvents(sessionsDir, sessionIndex, childSessionIndex, &stats)
	for _, sp := range stats.Sessions {
		for fp := range sp.PrefixFingerprints {
			stats.PrefixFingerprints[fp] = true
		}
		for fp := range sp.ProviderPrefixHashes {
			stats.ProviderPrefixHashes[fp] = true
		}
		if len(sp.ProviderPrefixHashes) > 0 {
			stats.PrefixShapeSessions[sp.ID] = true
		}
		if sp.MaxPromptTokens > stats.MaxPromptTokens {
			stats.MaxPromptTokens = sp.MaxPromptTokens
		}
		stats.PromptTokens += sp.PromptTokens
		stats.CompletionTokens += sp.CompletionTokens
		stats.CacheHit += sp.CacheHit
		stats.CacheMiss += sp.CacheMiss
		stats.CostUSD += sp.CostUSD
		if sp.PromptTokens > 0 || sp.CompletionTokens > 0 || sp.CostUSD > 0 {
			stats.UsageMatchedSessions++
		}
		if sp.SubagentMaxPromptTokens > stats.SubagentMaxPromptTokens {
			stats.SubagentMaxPromptTokens = sp.SubagentMaxPromptTokens
		}
		stats.SubagentPromptTokens += sp.SubagentPromptTokens
		stats.SubagentCompletionTokens += sp.SubagentCompletionTokens
		stats.SubagentCacheHit += sp.SubagentCacheHit
		stats.SubagentCacheMiss += sp.SubagentCacheMiss
		stats.SubagentCostUSD += sp.SubagentCostUSD
		stats.ApprovalPrompts += sp.ApprovalPrompts
		stats.ApprovalAllowedOnce += sp.ApprovalAllowedOnce
		stats.ApprovalAllowedForSession += sp.ApprovalAllowedForSession
		stats.ApprovalDenied += sp.ApprovalDenied
		stats.ApprovalCanceled += sp.ApprovalCanceled
		stats.ApprovalReused += sp.ApprovalReused
		stats.ApprovalPolicyBlocks += sp.ApprovalPolicyBlocks
		stats.ApprovalModeBlocks += sp.ApprovalModeBlocks
		stats.ApprovalAuditEvents += sp.ApprovalAuditEvents
	}
	stats.MainWorkSessions = len(stats.Sessions) - stats.TrivialSessions
	stats.TopSessions = topProfileSessions(stats.Sessions, statsRecentLimit, false)
	stats.Insights = buildProfileInsights(stats.Sessions, statsProfileInsightLimit)
	return stats
}

type profileSessionFile struct {
	ID      string
	Path    string
	ModTime time.Time
}

func latestProfileSessionFiles(sessionsDir string, limit int) []profileSessionFile {
	entries, err := os.ReadDir(strings.TrimSpace(sessionsDir))
	if err != nil {
		return nil
	}
	files := make([]profileSessionFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !isProfileSessionJSONL(entry.Name()) {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		if isSubagentSession(sessionsDir, id) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, profileSessionFile{
			ID:      id,
			Path:    filepath.Join(sessionsDir, entry.Name()),
			ModTime: info.ModTime(),
		})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].ModTime.Equal(files[j].ModTime) {
			return files[i].ID > files[j].ID
		}
		return files[i].ModTime.After(files[j].ModTime)
	})
	return limitSlice(files, limit)
}

func isProfileSessionJSONL(name string) bool {
	if !strings.HasSuffix(name, ".jsonl") ||
		strings.HasSuffix(name, telemetry.ToolInputEventsSuffix) ||
		strings.HasSuffix(name, telemetry.ApprovalEventsSuffix) {
		return false
	}
	id := strings.TrimSuffix(name, ".jsonl")
	return !strings.Contains(id, "--subagent-") && !strings.HasPrefix(id, "e2e-") && !strings.HasPrefix(id, "rt-")
}

func isSubagentSession(sessionsDir, id string) bool {
	meta, err := session.LoadSessionMeta(sessionsDir, id)
	return err == nil && strings.EqualFold(strings.TrimSpace(meta.Kind), "subagent")
}

func profileChildSessionIndex(sessionsDir string, parentIndex map[string]int) map[string]int {
	out := map[string]int{}
	if len(parentIndex) == 0 {
		return out
	}
	entries, err := os.ReadDir(strings.TrimSpace(sessionsDir))
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if entry.IsDir() ||
			!strings.HasSuffix(entry.Name(), ".jsonl") ||
			strings.HasSuffix(entry.Name(), telemetry.ToolInputEventsSuffix) ||
			strings.HasSuffix(entry.Name(), telemetry.ApprovalEventsSuffix) {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		parentID := ""
		if meta, err := session.LoadSessionMeta(sessionsDir, id); err == nil && strings.EqualFold(strings.TrimSpace(meta.Kind), "subagent") {
			parentID = strings.TrimSpace(meta.ParentSessionID)
		}
		if parentID == "" {
			if before, _, ok := strings.Cut(id, "--subagent-"); ok {
				parentID = before
			}
		}
		if parentID == "" || parentID == id {
			continue
		}
		if idx, ok := parentIndex[parentID]; ok {
			out[id] = idx
		}
	}
	return out
}

func readProfileSessionFile(path, id string, modTime time.Time) profileSessionStats {
	stats := profileSessionStats{
		ID:                   id,
		ModTime:              modTime,
		PrefixFingerprints:   map[string]bool{},
		ProviderPrefixHashes: map[string]bool{},
		SystemHashes:         map[string]bool{},
		RuntimeHashes:        map[string]bool{},
		ToolsHashes:          map[string]bool{},
		RequestHashes:        map[string]bool{},
		ShapeSegments:        map[string]map[string]bool{},
		ByTool:               map[string]*profileToolStats{},
	}
	f, err := os.Open(path)
	if err != nil {
		return stats
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)
	var messages []core.Message
	for scanner.Scan() {
		var msg core.Message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		messages = append(messages, msg)
		stats.Messages++
		stats.VisibleTextChars += len(msg.Text)
		stats.ReasoningChars += len(msg.Reasoning)
		if msg.Role == core.RoleUser {
			stats.UserMessages++
			if msg.Hidden && strings.TrimSpace(msg.Text) != "" {
				stats.HasHiddenUserTask = true
			}
			if stats.FirstUserText == "" && !msg.Hidden {
				stats.FirstUserText = previewText(msg.Text, 80)
			}
		}
		if msg.Role == core.RoleAssistant {
			stats.AssistantMessages++
		}
		if msg.Role == core.RoleTool {
			stats.ToolMessages++
		}
		stats.ToolCalls += len(msg.ToolCalls)
		for _, tc := range msg.ToolCalls {
			addProfileToolCall(stats.ByTool, tc.Name)
		}
		for _, tr := range msg.ToolResults {
			stats.ToolResultChars += len(tr.ModelText)
			addProfileToolResult(stats.ByTool, tr.Name, len(tr.ModelText))
		}
	}
	if stats.HasHiddenUserTask && isTrivialProfileUserText(stats.FirstUserText) {
		stats.FirstUserText = "(hidden user task)"
	}
	stats.Trivial = isTrivialProfileSession(messages)
	return stats
}

func readProfileUsage(dir string, sessionIndex map[string]int, childSessionIndex map[string]int, stats *profileStats) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		f, err := os.Open(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			var rec telemetry.UsageRecord
			if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
				continue
			}
			if !isSupportedUsageModel(rec.Model) {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(rec.Kind), "subagent") && strings.TrimSpace(rec.ParentSessionID) != "" {
				if idx, ok := sessionIndex[strings.TrimSpace(rec.ParentSessionID)]; ok {
					addProfileSubagentUsage(&stats.Sessions[idx], stats, rec)
					continue
				}
			}
			if idx, ok := sessionIndex[rec.Session]; ok {
				cost := telemetry.EstimateUsageRecordUSD(rec)
				sp := &stats.Sessions[idx]
				sp.PromptTokens += rec.PromptTokens
				sp.CompletionTokens += rec.CompletionTokens
				sp.CacheHit += rec.PromptCacheHit
				sp.CacheMiss += rec.PromptCacheMiss
				sp.ReasoningReplayTokens += rec.ReasoningReplayTok
				stats.ReasoningReplayTokens += rec.ReasoningReplayTok
				sp.ToolResultRawChars += rec.ToolResultRawChars
				sp.ToolResultReplayChars += rec.ToolResultReplayChars
				sp.ToolResultRawTokens += rec.ToolResultRawTokens
				sp.ToolResultReplayTokens += rec.ToolResultReplayTokens
				sp.ToolResultTokensSaved += rec.ToolResultTokensSaved
				sp.ToolResultsCompacted += rec.ToolResultsCompacted
				stats.ToolResultRawChars += rec.ToolResultRawChars
				stats.ToolResultReplayChars += rec.ToolResultReplayChars
				stats.ToolResultRawTokens += rec.ToolResultRawTokens
				stats.ToolResultReplayTokens += rec.ToolResultReplayTokens
				stats.ToolResultTokensSaved += rec.ToolResultTokensSaved
				stats.ToolResultsCompacted += rec.ToolResultsCompacted
				sp.CostUSD += cost
				if rec.PromptTokens > sp.MaxPromptTokens {
					sp.MaxPromptTokens = rec.PromptTokens
				}
				if fp := strings.TrimSpace(rec.PrefixFingerprint); fp != "" {
					sp.PrefixFingerprints[fp] = true
				}
				addProfileCacheShape(sp, rec.CacheShape)
				continue
			}
			if idx, ok := childSessionIndex[rec.Session]; ok {
				addProfileSubagentUsage(&stats.Sessions[idx], stats, rec)
			}
		}
		_ = f.Close()
	}
}

func readProfileApprovalEvents(sessionsDir string, sessionIndex map[string]int, childSessionIndex map[string]int, stats *profileStats) {
	entries, err := os.ReadDir(strings.TrimSpace(sessionsDir))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), telemetry.ApprovalEventsSuffix) {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), telemetry.ApprovalEventsSuffix)
		idx, ok := sessionIndex[id]
		if !ok {
			idx, ok = childSessionIndex[id]
		}
		if !ok {
			continue
		}
		readProfileApprovalEventFile(filepath.Join(sessionsDir, entry.Name()), &stats.Sessions[idx])
	}
}

func readProfileApprovalEventFile(path string, stats *profileSessionStats) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var rec telemetry.ApprovalEvent
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}
		addProfileApprovalEvent(stats, rec)
	}
}

func addProfileApprovalEvent(stats *profileSessionStats, rec telemetry.ApprovalEvent) {
	if stats == nil {
		return
	}
	event := strings.TrimSpace(rec.Event)
	stats.ApprovalAuditEvents++
	switch telemetry.ClassifyApprovalEvent(event) {
	case telemetry.ApprovalEventClassPromptShown:
		if alreadyCountedApproval(&stats.approvalPromptKeys, rec, "prompt") {
			return
		}
		stats.ApprovalPrompts++
	case telemetry.ApprovalEventClassDecision:
		if alreadyCountedApproval(&stats.approvalDecisionKeys, rec, "decision") {
			return
		}
		switch strings.TrimSpace(event) {
		case "approval_allowed_once", "approval_prompt_allowed_once":
			stats.ApprovalAllowedOnce++
		case "approval_allowed_for_session", "approval_prompt_allowed_for_session":
			stats.ApprovalAllowedForSession++
		case "approval_canceled", "approval_prompt_canceled":
			stats.ApprovalCanceled++
		default:
			stats.ApprovalDenied++
		}
	case telemetry.ApprovalEventClassReused:
		stats.ApprovalReused++
	case telemetry.ApprovalEventClassPolicyBlock:
		stats.ApprovalPolicyBlocks++
	case telemetry.ApprovalEventClassModeBlock:
		stats.ApprovalModeBlocks++
	}
}

func alreadyCountedApproval(seen *map[string]bool, rec telemetry.ApprovalEvent, class string) bool {
	key := approvalCounterKey(rec, class)
	if key == "" {
		return false
	}
	if *seen == nil {
		*seen = map[string]bool{}
	}
	if (*seen)[key] {
		return true
	}
	(*seen)[key] = true
	return false
}

func approvalCounterKey(rec telemetry.ApprovalEvent, class string) string {
	toolCallID := strings.TrimSpace(rec.ToolCallID)
	if toolCallID == "" {
		return ""
	}
	return class + ":" + toolCallID
}

func addProfileSubagentUsage(sp *profileSessionStats, stats *profileStats, rec telemetry.UsageRecord) {
	cost := telemetry.EstimateUsageRecordUSD(rec)
	sp.SubagentPromptTokens += rec.PromptTokens
	sp.SubagentCompletionTokens += rec.CompletionTokens
	sp.SubagentCacheHit += rec.PromptCacheHit
	sp.SubagentCacheMiss += rec.PromptCacheMiss
	sp.SubagentReasoningReplay += rec.ReasoningReplayTok
	stats.SubagentReasoningReplay += rec.ReasoningReplayTok
	sp.SubagentToolResultRawChars += rec.ToolResultRawChars
	sp.SubagentToolResultReplayChars += rec.ToolResultReplayChars
	sp.SubagentToolResultRawTokens += rec.ToolResultRawTokens
	sp.SubagentToolResultReplayTokens += rec.ToolResultReplayTokens
	sp.SubagentToolResultTokensSaved += rec.ToolResultTokensSaved
	sp.SubagentToolResultsCompacted += rec.ToolResultsCompacted
	stats.SubagentToolResultRawChars += rec.ToolResultRawChars
	stats.SubagentToolResultReplayChars += rec.ToolResultReplayChars
	stats.SubagentToolResultRawTokens += rec.ToolResultRawTokens
	stats.SubagentToolResultReplayTokens += rec.ToolResultReplayTokens
	stats.SubagentToolResultTokensSaved += rec.ToolResultTokensSaved
	stats.SubagentToolResultsCompacted += rec.ToolResultsCompacted
	sp.SubagentCostUSD += cost
	if rec.PromptTokens > sp.SubagentMaxPromptTokens {
		sp.SubagentMaxPromptTokens = rec.PromptTokens
	}
	addProfileCacheShape(sp, rec.CacheShape)
}

func addProfileCacheShape(sp *profileSessionStats, shape *telemetry.CacheShape) {
	if sp == nil || shape == nil {
		return
	}
	ensureProfileShapeMaps(sp)
	if h := strings.TrimSpace(shape.PrefixHash); h != "" {
		sp.ProviderPrefixHashes[h] = true
	}
	if h := strings.TrimSpace(shape.SystemHash); h != "" {
		sp.SystemHashes[h] = true
	}
	if h := strings.TrimSpace(shape.RuntimeHash); h != "" {
		sp.RuntimeHashes[h] = true
	}
	if h := strings.TrimSpace(shape.ToolsHash); h != "" {
		sp.ToolsHashes[h] = true
	}
	if h := strings.TrimSpace(shape.RequestHash); h != "" {
		sp.RequestHashes[h] = true
	}
	addProfileShapeSegments(sp.ShapeSegments, "system", shape.SystemSegments)
	addProfileShapeSegments(sp.ShapeSegments, "runtime", shape.RuntimeSegments)
	addProfileShapeSegments(sp.ShapeSegments, "tool", shape.ToolSegments)
}

func ensureProfileShapeMaps(sp *profileSessionStats) {
	if sp.ProviderPrefixHashes == nil {
		sp.ProviderPrefixHashes = map[string]bool{}
	}
	if sp.SystemHashes == nil {
		sp.SystemHashes = map[string]bool{}
	}
	if sp.RuntimeHashes == nil {
		sp.RuntimeHashes = map[string]bool{}
	}
	if sp.ToolsHashes == nil {
		sp.ToolsHashes = map[string]bool{}
	}
	if sp.RequestHashes == nil {
		sp.RequestHashes = map[string]bool{}
	}
	if sp.ShapeSegments == nil {
		sp.ShapeSegments = map[string]map[string]bool{}
	}
}

func addProfileShapeSegments(dst map[string]map[string]bool, family string, segments []telemetry.CacheShapeSegment) {
	for _, segment := range segments {
		name := strings.TrimSpace(segment.Name)
		hash := strings.TrimSpace(segment.Hash)
		if name == "" || hash == "" {
			continue
		}
		key := family + ":" + name
		hashes := dst[key]
		if hashes == nil {
			hashes = map[string]bool{}
			dst[key] = hashes
		}
		hashes[hash] = true
	}
}

func profileUsagePaths(primary string) []string {
	paths := make([]string, 0, 2)
	seen := map[string]bool{}
	for _, path := range []string{primary, telemetry.DefaultUsageLogDir()} {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		key := filepath.Clean(path)
		if seen[key] {
			continue
		}
		seen[key] = true
		paths = append(paths, path)
	}
	return paths
}

func isTrivialProfileSession(messages []core.Message) bool {
	visible := make([]core.Message, 0, len(messages))
	for _, msg := range messages {
		if !msg.Hidden {
			visible = append(visible, msg)
		}
		if strings.TrimSpace(msg.Reasoning) != "" || len(msg.ToolCalls)+len(msg.ToolResults) > 0 {
			return false
		}
		if msg.Role == core.RoleTool {
			return false
		}
	}
	if len(visible) == 0 || len(visible) > 2 {
		return false
	}
	user := visible[0]
	if user.Role != core.RoleUser {
		return false
	}
	if len(visible) == 2 {
		assistant := visible[1]
		if assistant.Role != core.RoleAssistant || len([]rune(strings.TrimSpace(assistant.Text))) > 200 {
			return false
		}
	}
	return isTrivialProfileUserText(user.Text)
}

func isTrivialProfileUserText(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if strings.HasPrefix(text, "/") {
		return true
	}
	switch text {
	case "hi", "hello", "hey", "几点了", "现在几点", "现在几点了", "what time is it", "time":
		return true
	default:
		return false
	}
}

func addProfileToolCall(stats map[string]*profileToolStats, tool string) {
	tool = nonEmpty(tool, "(unknown)")
	ts := stats[tool]
	if ts == nil {
		ts = &profileToolStats{Tool: tool}
		stats[tool] = ts
	}
	ts.Calls++
}

func addProfileToolResult(stats map[string]*profileToolStats, tool string, resultChars int) {
	tool = nonEmpty(tool, "(unknown)")
	ts := stats[tool]
	if ts == nil {
		ts = &profileToolStats{Tool: tool}
		stats[tool] = ts
	}
	ts.ResultChars += resultChars
}
