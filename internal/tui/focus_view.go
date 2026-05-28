package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/usewhale/whale/internal/app"
	tuirender "github.com/usewhale/whale/internal/tui/render"
)

func (m model) focusEnabled() bool {
	return strings.TrimSpace(m.viewMode) == app.ViewModeFocus
}

func (m model) focusMessages(messages []tuirender.UIMessage) []tuirender.UIMessage {
	if len(messages) == 0 {
		return messages
	}
	if m.focusEnabled() {
		return projectFocusMessages(messages)
	}
	return projectExpandedFocusMessages(messages)
}

func (m *model) toggleFocusView() bool {
	next := app.ViewModeFocus
	if m.focusEnabled() {
		next = app.ViewModeDefault
	}
	m.viewMode = next
	m.persistViewMode(next)
	m.setEphemeralInfo(app.ViewModeToggleMessage(next))
	if !m.busy {
		m.status = "ready"
	}
	m.refreshViewportContentFollow(true)
	return true
}

func (m *model) persistViewMode(mode string) {
	if m.svc == nil {
		return
	}
	if err := m.svc.SetViewMode(mode); err != nil {
		m.append("error", err.Error())
	}
}

func (m *model) redrawTranscriptForFocusToggleCmd() tea.Cmd {
	if m.page != pageChat || len(m.transcript) == 0 {
		m.refreshViewportContentFollow(true)
		return nil
	}
	m.nativeScrollbackPrinted = 0
	printCmd := m.flushNativeScrollbackCmd()
	m.refreshViewportContentFollow(true)
	if printCmd == nil {
		return nil
	}
	return tea.Sequence(clearScreenCmd(), printCmd)
}

func projectFocusMessages(messages []tuirender.UIMessage) []tuirender.UIMessage {
	out := make([]tuirender.UIMessage, 0, len(messages))
	var tools focusToolSummary
	flushTools := func() {
		if !tools.used {
			return
		}
		summary := tools.summary()
		text := summary.Text()
		if text != "" {
			out = append(out, tuirender.UIMessage{
				Role:         "tool_summary",
				Kind:         tuirender.KindToolSummary,
				Text:         text,
				FocusSummary: summary,
			})
		}
		tools = focusToolSummary{}
	}
	for _, msg := range messages {
		if isFocusHiddenToolMessage(msg) {
			tools.add(msg)
			continue
		}
		if isFocusHiddenMessage(msg) {
			continue
		}
		flushTools()
		out = append(out, msg)
	}
	flushTools()
	return out
}

func projectExpandedFocusMessages(messages []tuirender.UIMessage) []tuirender.UIMessage {
	out := make([]tuirender.UIMessage, 0, len(messages))
	for _, msg := range messages {
		if isFocusHiddenMessage(msg) || isFocusHiddenToolMessage(msg) {
			msg.Text = appendFocusToggleHint(msg.Text, "collapse")
		}
		out = append(out, msg)
	}
	return out
}

func appendFocusToggleHint(text, action string) string {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return text
	}
	hint := " (ctrl+o to " + action + ")"
	lines := strings.Split(text, "\n")
	lines[0] = strings.TrimRight(lines[0], " ") + hint
	return strings.Join(lines, "\n")
}

func isFocusHiddenMessage(msg tuirender.UIMessage) bool {
	return msg.Kind == tuirender.KindThinking || msg.Role == "think"
}

func isFocusHiddenToolMessage(msg tuirender.UIMessage) bool {
	return msg.Kind == tuirender.KindToolCall || msg.Kind == tuirender.KindToolResult || msg.Kind == tuirender.KindSubagent
}

type focusToolSummary struct {
	used   bool
	shell  focusSummaryBucket
	search focusSummaryBucket
	read   focusSummaryBucket
	list   focusSummaryBucket
	edit   focusSummaryBucket
	task   focusSummaryBucket
	plan   focusSummaryBucket
	todo   focusSummaryBucket
	mcp    focusSummaryBucket
	other  focusSummaryBucket
}

type focusSummaryBucket struct {
	count   int
	running int
	failed  int
	denied  int
	details []string
	entries []focusSummaryEntry
}

type focusSummaryEntry struct {
	state    string
	detail   string
	identity string
}

func (s *focusToolSummary) add(msg tuirender.UIMessage) {
	s.used = true
	state := focusToolState(msg)
	item := focusSummarizeToolMessage(msg)
	switch item.Kind {
	case "shell":
		s.shell.add(state, item.Detail, item.Identity)
	case "search":
		s.search.add(state, item.Detail, "")
	case "read":
		s.read.add(state, item.Detail, "")
	case "list":
		s.list.add(state, item.Detail, "")
	case "edit":
		s.edit.add(state, item.Detail, "")
	case "task":
		s.task.add(state, item.Detail, "")
	case "plan":
		s.plan.add(state, "", "")
	case "todo":
		s.todo.add(state, "", "")
	case "mcp":
		s.mcp.add(state, item.Detail, "")
	default:
		s.other.add(state, "", "")
	}
}

func (s focusToolSummary) summary() *tuirender.FocusSummary {
	parts := make([]tuirender.FocusSummaryPart, 0, 8)
	add := func(part tuirender.FocusSummaryPart) {
		if part.Text() != "" {
			parts = append(parts, part)
		}
	}
	recoveredShell, remainingShell := s.shell.splitRecovered()
	recoveredShell = recoveredShell.withDisambiguatedShellCWD()
	remainingShell = remainingShell.withDisambiguatedShellCWD()
	for _, state := range []string{"denied", "failed", "running", "done"} {
		add(focusStateHintSummaryPart("search", s.search, state, "Searching for", "Searched for", "Denied", "Failed", "pattern", "patterns", focusQuoteHint))
		add(focusStateHintSummaryPart("read", s.read, state, "Reading", "Read", "Denied", "Failed", "file", "files", focusPlainHint))
		add(focusStateHintSummaryPart("list", s.list, state, "Listing", "Listed", "Denied", "Failed", "directory", "directories", focusPlainHint))
		if state == "done" {
			add(focusRecoveredShellSummaryPart(recoveredShell))
		}
		add(focusStateShellSummaryPart(remainingShell, state))
		add(focusStateHintSummaryPart("edit", s.edit, state, "Editing", "Edited", "Denied", "Failed", "file", "files", focusPlainHint))
		add(focusStateTaskSummaryPart(s.task, state))
		add(focusStateSimpleSummaryPart("plan", s.plan, state, "Updating plan", "Updated plan", "Denied", "Failed", "plan update", "plan updates"))
		add(focusStateSimpleSummaryPart("todo", s.todo, state, "Updating todos", "Updated todos", "Denied", "Failed", "todo update", "todo updates"))
		add(focusStateHintSummaryPart("mcp", s.mcp, state, "Calling", "Called", "Denied", "Failed", "MCP tool", "MCP tools", focusPlainHint))
		add(focusStateCountSummaryPart("other", s.other, state, "Running", "Ran", "Denied", "Failed", "tool", "tools"))
	}
	return &tuirender.FocusSummary{Parts: parts, Hint: "(ctrl+o to expand)"}
}

func (b *focusSummaryBucket) add(state, detail, identity string) {
	b.count++
	switch state {
	case "running":
		b.running++
	case "failed":
		b.failed++
	case "denied":
		b.denied++
	}
	if detail != "" {
		b.details = append(b.details, detail)
	}
	b.entries = append(b.entries, focusSummaryEntry{state: state, detail: detail, identity: identity})
}

func (b focusSummaryBucket) activeVerb(running, done string) string {
	if b.running > 0 {
		return running
	}
	return done
}

func (b focusSummaryBucket) allDenied() bool {
	return b.count > 0 && b.denied == b.count
}

func (b focusSummaryBucket) succeeded() int {
	return b.count - b.running - b.failed - b.denied
}

func (b focusSummaryBucket) splitRecovered() (focusSummaryBucket, focusSummaryBucket) {
	pendingFailures := map[string][]int{}
	recoveredEntries := map[int]struct{}{}
	for i, entry := range b.entries {
		identity := strings.TrimSpace(entry.identity)
		if identity == "" {
			continue
		}
		switch entry.state {
		case "failed":
			pendingFailures[identity] = append(pendingFailures[identity], i)
		case "done":
			pending := pendingFailures[identity]
			if len(pending) > 0 {
				for _, failedIndex := range pending {
					recoveredEntries[failedIndex] = struct{}{}
				}
				pendingFailures[identity] = nil
				recoveredEntries[i] = struct{}{}
			}
		}
	}
	var recovered, remaining focusSummaryBucket
	for i, entry := range b.entries {
		if _, ok := recoveredEntries[i]; ok {
			recovered.add(entry.state, entry.detail, entry.identity)
			continue
		}
		remaining.add(entry.state, entry.detail, entry.identity)
	}
	return recovered, remaining
}

func (b focusSummaryBucket) withDisambiguatedShellCWD() focusSummaryBucket {
	identitiesByDetail := map[string]map[string]struct{}{}
	for _, entry := range b.entries {
		if entry.detail == "" || shellIdentityCWD(entry.identity) == "" {
			continue
		}
		if identitiesByDetail[entry.detail] == nil {
			identitiesByDetail[entry.detail] = map[string]struct{}{}
		}
		identitiesByDetail[entry.detail][entry.identity] = struct{}{}
	}
	var out focusSummaryBucket
	for _, entry := range b.entries {
		detail := entry.detail
		if len(identitiesByDetail[entry.detail]) > 1 {
			if cwd := shellIdentityCWD(entry.identity); cwd != "" {
				detail = detail + " (cwd: " + cwd + ")"
			}
		}
		out.add(entry.state, detail, entry.identity)
	}
	return out
}

func shellIdentityCWD(identity string) string {
	_, cwd, ok := strings.Cut(identity, "\x00cwd=")
	if !ok {
		return ""
	}
	return strings.TrimSpace(cwd)
}

func (b focusSummaryBucket) forState(state string) focusSummaryBucket {
	var out focusSummaryBucket
	for _, entry := range b.entries {
		if entry.state == state {
			out.add(entry.state, entry.detail, entry.identity)
		}
	}
	return out
}

func (b focusSummaryBucket) statusSuffix() string {
	status := make([]string, 0, 4)
	if b.running > 0 {
		status = append(status, fmt.Sprintf("%d running", b.running))
	}
	if b.failed > 0 {
		status = append(status, fmt.Sprintf("%d failed", b.failed))
	}
	if succeeded := b.succeeded(); succeeded > 0 && (b.running > 0 || b.failed > 0 || b.denied > 0) {
		status = append(status, fmt.Sprintf("%d succeeded", succeeded))
	}
	if b.denied > 0 {
		status = append(status, fmt.Sprintf("%d denied/canceled", b.denied))
	}
	if len(status) == 0 {
		return ""
	}
	return " (" + strings.Join(status, ", ") + ")"
}

func focusBucketState(b focusSummaryBucket) string {
	switch {
	case b.denied > 0:
		return "denied"
	case b.failed > 0:
		return "failed"
	case b.running > 0:
		return "running"
	default:
		return "done"
	}
}

func focusSummaryPart(kind string, b focusSummaryBucket, action, detail string) tuirender.FocusSummaryPart {
	if b.count == 0 {
		return tuirender.FocusSummaryPart{}
	}
	return tuirender.FocusSummaryPart{
		Kind:   kind,
		State:  focusBucketState(b),
		Count:  b.count,
		Action: action,
		Detail: detail,
		Status: strings.TrimSpace(b.statusSuffix()),
	}
}

func focusStateShellSummaryPart(b focusSummaryBucket, state string) tuirender.FocusSummaryPart {
	b = b.forState(state)
	if state == "failed" {
		return focusFailedShellSummaryPart(b)
	}
	return focusShellSummaryPart(b)
}

func focusRecoveredShellSummaryPart(b focusSummaryBucket) tuirender.FocusSummaryPart {
	if b.count == 0 {
		return tuirender.FocusSummaryPart{}
	}
	done := b.forState("done")
	detail := latestFocusHint(done.details, focusPlainHint)
	if detail == "" {
		detail = focusSampleDetails(b.details, b.count, focusPlainHint)
	}
	action := "Retried shell"
	if succeeded := b.succeeded(); succeeded > 1 {
		action = fmt.Sprintf("Retried %d shell commands", succeeded)
	}
	part := focusSummaryPart("shell", b, action, detail)
	part.State = "done"
	return part
}

func focusShellSummaryPart(b focusSummaryBucket) tuirender.FocusSummaryPart {
	if b.count == 0 {
		return tuirender.FocusSummaryPart{}
	}
	if b.allDenied() {
		return focusSummaryPart("shell", b, fmt.Sprintf("Denied %d %s", b.count, pluralize(b.count, "shell command", "shell commands")), "")
	}
	verb := b.activeVerb("Running", "Ran")
	if len(b.details) == 1 {
		return focusSummaryPart("shell", b, verb+" shell", b.details[0])
	}
	if detail := focusSampleDetails(b.details, b.count, focusPlainHint); detail != "" {
		return focusSummaryPart("shell", b, fmt.Sprintf("%s %d shell commands", verb, b.count), detail)
	}
	return focusSummaryPart("shell", b, fmt.Sprintf("%s %d %s", verb, b.count, pluralize(b.count, "shell command", "shell commands")), "")
}

func focusFailedShellSummaryPart(b focusSummaryBucket) tuirender.FocusSummaryPart {
	if b.count == 0 {
		return tuirender.FocusSummaryPart{}
	}
	if len(b.details) == 1 {
		return focusSummaryPart("shell", b, "Failed shell", b.details[0])
	}
	if detail := focusSampleDetails(b.details, b.count, focusPlainHint); detail != "" {
		return focusSummaryPart("shell", b, fmt.Sprintf("Failed %d shell commands", b.count), detail)
	}
	return focusSummaryPart("shell", b, fmt.Sprintf("Failed %d %s", b.count, pluralize(b.count, "shell command", "shell commands")), "")
}

func focusStateCountSummaryPart(kind string, b focusSummaryBucket, state, runningVerb, doneVerb, deniedVerb, failedVerb, singular, plural string) tuirender.FocusSummaryPart {
	b = b.forState(state)
	if state == "failed" {
		return focusCountSummaryPartWithVerb(kind, b, failedVerb, singular, plural)
	}
	if state == "denied" {
		return focusCountSummaryPartWithVerb(kind, b, deniedVerb, singular, plural)
	}
	return focusCountSummaryPart(kind, b, runningVerb, doneVerb, singular, plural)
}

func focusCountSummaryPart(kind string, b focusSummaryBucket, runningVerb, doneVerb, singular, plural string) tuirender.FocusSummaryPart {
	if b.count == 0 {
		return tuirender.FocusSummaryPart{}
	}
	return focusSummaryPart(kind, b, fmt.Sprintf("%s %d %s", b.activeVerb(runningVerb, doneVerb), b.count, pluralize(b.count, singular, plural)), "")
}

func focusCountSummaryPartWithVerb(kind string, b focusSummaryBucket, verb, singular, plural string) tuirender.FocusSummaryPart {
	if b.count == 0 {
		return tuirender.FocusSummaryPart{}
	}
	return focusSummaryPart(kind, b, fmt.Sprintf("%s %d %s", verb, b.count, pluralize(b.count, singular, plural)), "")
}

func focusStateHintSummaryPart(kind string, b focusSummaryBucket, state, runningVerb, doneVerb, deniedVerb, failedVerb, singular, plural string, formatHint func(string) string) tuirender.FocusSummaryPart {
	b = b.forState(state)
	if state == "failed" {
		return focusFailedHintSummaryPart(kind, b, failedVerb, singular, plural, formatHint)
	}
	return focusHintSummaryPart(kind, b, runningVerb, doneVerb, deniedVerb, singular, plural, formatHint)
}

func focusHintSummaryPart(kind string, b focusSummaryBucket, runningVerb, doneVerb, deniedVerb, singular, plural string, formatHint func(string) string) tuirender.FocusSummaryPart {
	if b.count == 0 {
		return tuirender.FocusSummaryPart{}
	}
	verb := b.activeVerb(runningVerb, doneVerb)
	if b.allDenied() {
		verb = deniedVerb
	}
	part := focusSummaryPart(kind, b, fmt.Sprintf("%s %d %s", verb, b.count, pluralize(b.count, singular, plural)), "")
	if b.running > 0 && !b.allDenied() {
		if hint := latestFocusHint(b.details, formatHint); hint != "" {
			part.Detail = hint
		}
	} else if !b.allDenied() {
		part.Detail = focusSampleDetails(b.details, b.count, formatHint)
	}
	return part
}

func focusFailedHintSummaryPart(kind string, b focusSummaryBucket, failedVerb, singular, plural string, formatHint func(string) string) tuirender.FocusSummaryPart {
	if b.count == 0 {
		return tuirender.FocusSummaryPart{}
	}
	part := focusSummaryPart(kind, b, fmt.Sprintf("%s %d %s", failedVerb, b.count, pluralize(b.count, singular, plural)), "")
	part.Detail = focusSampleDetails(b.details, b.count, formatHint)
	return part
}

func focusSampleDetails(details []string, total int, format func(string) string) string {
	const maxDetails = 2
	samples := make([]string, 0, maxDetails)
	seen := map[string]struct{}{}
	unique := 0
	for _, detail := range details {
		if sample := format(strings.TrimSpace(detail)); sample != "" {
			if _, ok := seen[sample]; ok {
				continue
			}
			seen[sample] = struct{}{}
			unique++
			if len(samples) < maxDetails {
				samples = append(samples, sample)
			}
		}
	}
	if len(samples) == 0 {
		return ""
	}
	text := strings.Join(samples, "; ")
	remaining := total - len(samples)
	if hiddenUnique := unique - len(samples); hiddenUnique > remaining {
		remaining = hiddenUnique
	}
	if remaining > 0 {
		text += fmt.Sprintf(" (+%d)", remaining)
	}
	return text
}

func latestFocusHint(details []string, format func(string) string) string {
	for i := len(details) - 1; i >= 0; i-- {
		if detail := format(strings.TrimSpace(details[i])); detail != "" {
			return detail
		}
	}
	return ""
}

func focusPlainHint(detail string) string {
	return truncateFocusToolDetail(detail)
}

func focusQuoteHint(detail string) string {
	detail = strings.Trim(detail, `"`)
	if detail == "" {
		return ""
	}
	return `"` + truncateFocusToolDetail(detail) + `"`
}

func focusSimpleSummaryPart(kind string, b focusSummaryBucket, runningText, doneText, singular, plural string) tuirender.FocusSummaryPart {
	if b.count == 0 {
		return tuirender.FocusSummaryPart{}
	}
	action := ""
	if b.count == 1 {
		action = b.activeVerb(runningText, doneText)
	} else {
		action = fmt.Sprintf("%s: %d %s", b.activeVerb(runningText, doneText), b.count, pluralize(b.count, singular, plural))
	}
	return focusSummaryPart(kind, b, action, "")
}

func focusStateSimpleSummaryPart(kind string, b focusSummaryBucket, state, runningText, doneText, deniedVerb, failedVerb, singular, plural string) tuirender.FocusSummaryPart {
	b = b.forState(state)
	switch state {
	case "denied":
		return focusCountSummaryPartWithVerb(kind, b, deniedVerb, singular, plural)
	case "failed":
		return focusCountSummaryPartWithVerb(kind, b, failedVerb, singular, plural)
	default:
		return focusSimpleSummaryPart(kind, b, runningText, doneText, singular, plural)
	}
}

func focusStateTaskSummaryPart(b focusSummaryBucket, state string) tuirender.FocusSummaryPart {
	b = b.forState(state)
	switch state {
	case "denied":
		return focusCountSummaryPartWithVerb("task", b, "Denied", "subagent task", "subagent tasks")
	case "failed":
		return focusCountSummaryPartWithVerb("task", b, "Failed", "subagent task", "subagent tasks")
	default:
		return focusTaskSummaryPart(b)
	}
}

func focusTaskSummaryPart(b focusSummaryBucket) tuirender.FocusSummaryPart {
	if b.count == 0 {
		return tuirender.FocusSummaryPart{}
	}
	if b.count == 1 && len(b.details) == 1 {
		return focusSummaryPart("task", b, b.details[0], "")
	}
	return focusCountSummaryPart("task", b, "Running", "Ran", "subagent task", "subagent tasks")
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

func focusToolState(msg tuirender.UIMessage) string {
	switch strings.TrimSpace(msg.Role) {
	case "tool", "result_running", "shell_result_running":
		return "running"
	case "result_failed", "result_error", "result_timeout", "shell_result_failed", "shell_result_error", "shell_result_timeout":
		return "failed"
	case "result_denied", "result_canceled", "shell_result_denied", "shell_result_canceled":
		return "denied"
	default:
		return "done"
	}
}
