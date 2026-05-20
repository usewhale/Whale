package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

type reviewMenuItem struct {
	Name        string
	Description string
	Action      string
	Prefill     string
	Picker      string
}

func reviewMenuItems() []reviewMenuItem {
	return []reviewMenuItem{
		{Name: "Local changes", Description: "Review staged, unstaged, and relevant untracked files.", Action: "/review local"},
		{Name: "Branch...", Description: "Choose a base branch; first option is vs default branch.", Picker: "branch"},
		{Name: "Pull request...", Description: "Choose an open GitHub PR or enter a number/URL.", Picker: "pr"},
		{Name: "Commit...", Description: "Choose a recent commit or enter a SHA.", Picker: "commit"},
		{Name: "Custom instructions...", Description: "Describe exactly what to review.", Prefill: "/review "},
	}
}

type reviewTargetPickerState struct {
	kind          string
	selected      int
	loading       bool
	err           string
	query         string
	defaultBranch string
	branches      []reviewBranchItem
	commits       []reviewCommitItem
	prs           []reviewPRItem
}

type reviewBranchItem struct {
	Name    string
	Current bool
}

type reviewCommitItem struct {
	SHA     string
	Subject string
	Author  string
	When    string
}

type reviewPRItem struct {
	Number int
	Title  string
	Head   string
	Author string
}

type reviewCommitsLoadedMsg struct {
	items []reviewCommitItem
	err   string
}

type reviewBranchesLoadedMsg struct {
	items         []reviewBranchItem
	defaultBranch string
	err           string
}

type reviewPRsLoadedMsg struct {
	items []reviewPRItem
	err   string
}

func (m *model) handleReviewMenuKey(msg tea.KeyMsg) tea.Cmd {
	items := reviewMenuItems()
	switch msg.String() {
	case "esc", "ctrl+c":
		m.closeReviewMenu()
	case "up", "k":
		if m.reviewMenu.selected > 0 {
			m.reviewMenu.selected--
		}
	case "down", "j":
		if m.reviewMenu.selected < len(items)-1 {
			m.reviewMenu.selected++
		}
	case "enter":
		if m.reviewMenu.selected < 0 || m.reviewMenu.selected >= len(items) {
			return nil
		}
		item := items[m.reviewMenu.selected]
		if item.Action != "" {
			m.closeReviewMenu()
			return m.submitPrompt(item.Action)
		}
		if item.Picker != "" {
			return m.openReviewTargetPicker(item.Picker)
		}
		if item.Prefill != "" {
			m.closeReviewMenu()
			m.input.SetValue(item.Prefill)
			m.skillBinding = nil
			m.resetHistoryNavigation()
			m.updateSlashMatches()
			m.refreshViewportContent()
		}
	}
	return nil
}

func (m *model) openReviewTargetPicker(kind string) tea.Cmd {
	m.reviewTargetPicker.kind = kind
	m.reviewTargetPicker.selected = 0
	m.reviewTargetPicker.loading = true
	m.reviewTargetPicker.err = ""
	m.reviewTargetPicker.query = ""
	m.reviewTargetPicker.defaultBranch = ""
	m.reviewTargetPicker.branches = nil
	m.reviewTargetPicker.commits = nil
	m.reviewTargetPicker.prs = nil
	switch kind {
	case "branch":
		m.mode = modeReviewBranchPicker
		m.status = "review branch"
		return loadReviewBranchesCmd(m.cwdPath)
	case "commit":
		m.mode = modeReviewCommitPicker
		m.status = "review commit"
		return loadReviewCommitsCmd(m.cwdPath)
	case "pr":
		m.mode = modeReviewPRPicker
		m.status = "review pr"
		return loadReviewPRsCmd(m.cwdPath)
	default:
		m.closeReviewMenu()
		return nil
	}
}

func (m *model) handleReviewTargetPickerKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.mode = modeReviewMenu
		m.status = "review"
		return nil
	case "backspace", "ctrl+h":
		if m.mode == modeReviewBranchPicker && m.reviewTargetPicker.query != "" {
			runes := []rune(m.reviewTargetPicker.query)
			m.reviewTargetPicker.query = string(runes[:len(runes)-1])
			m.reviewTargetPicker.selected = min(m.reviewTargetPicker.selected, max(0, m.reviewTargetPickerChoiceCount()-1))
			return nil
		}
	case "up", "k":
		if m.reviewTargetPicker.selected > 0 {
			m.reviewTargetPicker.selected--
		}
	case "down", "j":
		if m.reviewTargetPicker.selected < m.reviewTargetPickerChoiceCount()-1 {
			m.reviewTargetPicker.selected++
		}
	case "enter":
		return m.submitSelectedReviewTarget()
	case "/":
		return m.prefillReviewTarget("")
	default:
		if msg.Type == tea.KeyRunes {
			if m.mode == modeReviewBranchPicker {
				m.reviewTargetPicker.query += string(msg.Runes)
				m.reviewTargetPicker.selected = 0
				return nil
			}
			return m.prefillReviewTarget(string(msg.Runes))
		}
	}
	return nil
}

func (m *model) handleReviewCommitsLoaded(msg reviewCommitsLoadedMsg) {
	if m.mode != modeReviewCommitPicker {
		return
	}
	m.reviewTargetPicker.loading = false
	m.reviewTargetPicker.err = strings.TrimSpace(msg.err)
	m.reviewTargetPicker.commits = msg.items
	if m.reviewTargetPicker.selected >= m.reviewTargetPickerChoiceCount() {
		m.reviewTargetPicker.selected = max(0, m.reviewTargetPickerChoiceCount()-1)
	}
	m.refreshViewportContent()
}

func (m *model) handleReviewPRsLoaded(msg reviewPRsLoadedMsg) {
	if m.mode != modeReviewPRPicker {
		return
	}
	m.reviewTargetPicker.loading = false
	m.reviewTargetPicker.err = strings.TrimSpace(msg.err)
	m.reviewTargetPicker.prs = msg.items
	if m.reviewTargetPicker.selected >= m.reviewTargetPickerChoiceCount() {
		m.reviewTargetPicker.selected = max(0, m.reviewTargetPickerChoiceCount()-1)
	}
	m.refreshViewportContent()
}

func (m *model) handleReviewBranchesLoaded(msg reviewBranchesLoadedMsg) {
	if m.mode != modeReviewBranchPicker {
		return
	}
	m.reviewTargetPicker.loading = false
	m.reviewTargetPicker.err = strings.TrimSpace(msg.err)
	m.reviewTargetPicker.defaultBranch = strings.TrimSpace(msg.defaultBranch)
	m.reviewTargetPicker.branches = msg.items
	if m.reviewTargetPicker.selected >= m.reviewTargetPickerChoiceCount() {
		m.reviewTargetPicker.selected = max(0, m.reviewTargetPickerChoiceCount()-1)
	}
	m.refreshViewportContent()
}

func (m model) reviewTargetPickerChoiceCount() int {
	switch m.mode {
	case modeReviewBranchPicker:
		return len(m.filteredReviewBranches()) + 1
	case modeReviewCommitPicker:
		return len(m.reviewTargetPicker.commits) + 1
	case modeReviewPRPicker:
		return len(m.reviewTargetPicker.prs) + 1
	default:
		return 0
	}
}

func (m *model) submitSelectedReviewTarget() tea.Cmd {
	switch m.mode {
	case modeReviewBranchPicker:
		branches := m.filteredReviewBranches()
		if m.reviewTargetPicker.selected < len(branches) {
			item := branches[m.reviewTargetPicker.selected]
			if strings.TrimSpace(item.Name) != "" {
				m.closeReviewMenu()
				return m.submitPrompt("/review branch " + item.Name)
			}
		}
		return m.prefillReviewTarget("")
	case modeReviewCommitPicker:
		if m.reviewTargetPicker.selected < len(m.reviewTargetPicker.commits) {
			item := m.reviewTargetPicker.commits[m.reviewTargetPicker.selected]
			if strings.TrimSpace(item.SHA) != "" {
				m.closeReviewMenu()
				return m.submitPrompt("/review commit " + item.SHA)
			}
		}
		return m.prefillReviewTarget("")
	case modeReviewPRPicker:
		if m.reviewTargetPicker.selected < len(m.reviewTargetPicker.prs) {
			item := m.reviewTargetPicker.prs[m.reviewTargetPicker.selected]
			if item.Number > 0 {
				m.closeReviewMenu()
				return m.submitPrompt("/review pr " + strconv.Itoa(item.Number))
			}
		}
		return m.prefillReviewTarget("")
	default:
		return nil
	}
}

func (m *model) prefillReviewTarget(suffix string) tea.Cmd {
	prefix := "/review "
	switch m.mode {
	case modeReviewBranchPicker:
		prefix = "/review branch "
	case modeReviewCommitPicker:
		prefix = "/review commit "
	case modeReviewPRPicker:
		prefix = "/review pr "
	}
	m.closeReviewMenu()
	m.input.SetValue(prefix + suffix)
	m.skillBinding = nil
	m.resetHistoryNavigation()
	m.updateSlashMatches()
	m.refreshViewportContent()
	return nil
}

func (m *model) closeReviewMenu() {
	m.mode = modeChat
	m.reviewMenu.selected = 0
	m.status = "ready"
}

func (m model) renderReviewMenu() string {
	title := lipgloss.NewStyle().Foreground(tuitheme.Default.InfoSoft).Bold(true)
	muted := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)
	rows := []string{
		title.Render("Review"),
		muted.Render("Choose what to review"),
		"",
	}
	for i, item := range reviewMenuItems() {
		rows = append(rows, renderReviewMenuRow(item, i == m.reviewMenu.selected))
	}
	rows = append(rows, "", muted.Render("  ↑/↓ select · Enter confirm · Esc close"))
	return strings.Join(rows, "\n")
}

func (m model) renderReviewTargetPicker() string {
	title := lipgloss.NewStyle().Foreground(tuitheme.Default.InfoSoft).Bold(true)
	muted := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)
	rows := []string{}
	switch m.mode {
	case modeReviewBranchPicker:
		rows = append(rows, title.Render("Choose base branch"))
		rows = append(rows, muted.Render(branchSearchLine(m.reviewTargetPicker.query)))
	case modeReviewCommitPicker:
		rows = append(rows, title.Render("Choose commit"))
	case modeReviewPRPicker:
		rows = append(rows, title.Render("Choose pull request"))
	}
	if m.reviewTargetPicker.loading {
		rows = append(rows, muted.Render("Loading..."), "", muted.Render("  Esc back"))
		return strings.Join(rows, "\n")
	}
	if m.reviewTargetPicker.err != "" {
		rows = append(rows, muted.Render(m.reviewTargetPicker.err))
	}
	switch m.mode {
	case modeReviewBranchPicker:
		branches := m.filteredReviewBranches()
		total := len(branches) + 1
		start, end := visibleReviewTargetRange(total, m.reviewTargetPicker.selected, 6)
		for i := start; i < end; i++ {
			if i < len(branches) {
				rows = append(rows, renderReviewTargetRow(formatReviewBranch(m.currentReviewBranch(), branches[i]), i == m.reviewTargetPicker.selected))
				continue
			}
			rows = append(rows, renderReviewTargetRow("Type branch manually...", m.reviewTargetPicker.selected == i))
		}
	case modeReviewCommitPicker:
		total := len(m.reviewTargetPicker.commits) + 1
		start, end := visibleReviewTargetRange(total, m.reviewTargetPicker.selected, 6)
		for i := start; i < end; i++ {
			if i < len(m.reviewTargetPicker.commits) {
				rows = append(rows, renderReviewTargetRow(formatReviewCommit(m.reviewTargetPicker.commits[i]), i == m.reviewTargetPicker.selected))
				continue
			}
			rows = append(rows, renderReviewTargetRow("Type SHA manually...", m.reviewTargetPicker.selected == i))
		}
	case modeReviewPRPicker:
		total := len(m.reviewTargetPicker.prs) + 1
		start, end := visibleReviewTargetRange(total, m.reviewTargetPicker.selected, 6)
		for i := start; i < end; i++ {
			if i < len(m.reviewTargetPicker.prs) {
				rows = append(rows, renderReviewTargetRow(formatReviewPR(m.reviewTargetPicker.prs[i]), i == m.reviewTargetPicker.selected))
				continue
			}
			rows = append(rows, renderReviewTargetRow("Type number or URL manually...", m.reviewTargetPicker.selected == i))
		}
	}
	rows = append(rows, "", muted.Render("  ↑/↓ select · Enter confirm · / type manually · Esc back"))
	return strings.Join(rows, "\n")
}

func (m model) filteredReviewBranches() []reviewBranchItem {
	query := strings.ToLower(strings.TrimSpace(m.reviewTargetPicker.query))
	out := make([]reviewBranchItem, 0, len(m.reviewTargetPicker.branches))
	seen := map[string]bool{}
	if query == "" {
		if def := strings.TrimSpace(m.reviewTargetPicker.defaultBranch); def != "" && def != m.currentReviewBranch() {
			out = append(out, reviewBranchItem{Name: def})
			seen[def] = true
		}
	}
	for _, item := range m.reviewTargetPicker.branches {
		if item.Current {
			continue
		}
		if seen[item.Name] {
			continue
		}
		if query == "" || strings.Contains(strings.ToLower(item.Name), query) {
			out = append(out, item)
			seen[item.Name] = true
		}
	}
	return out
}

func (m model) currentReviewBranch() string {
	for _, item := range m.reviewTargetPicker.branches {
		if item.Current {
			return item.Name
		}
	}
	if strings.TrimSpace(m.gitBranch) != "" {
		return strings.TrimSpace(m.gitBranch)
	}
	return "HEAD"
}

func branchSearchLine(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return "Type to search branches"
	}
	return "Type to search branches: " + query
}

func visibleReviewTargetRange(total, selected, limit int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if limit <= 0 || total <= limit {
		return 0, total
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= total {
		selected = total - 1
	}
	start := selected - limit + 1
	if start < 0 {
		start = 0
	}
	if start+limit > total {
		start = total - limit
	}
	return start, start + limit
}

func renderReviewTargetRow(text string, selected bool) string {
	muted := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)
	style := lipgloss.NewStyle()
	prefix := muted.Render("  ")
	if selected {
		prefix = lipgloss.NewStyle().Foreground(tuitheme.Default.InfoSoft).Bold(true).Render("> ")
		style = style.Foreground(tuitheme.Default.InfoSoft).Bold(true)
	}
	return prefix + style.Render(text)
}

func formatReviewCommit(item reviewCommitItem) string {
	parts := []string{item.SHA, item.Subject}
	meta := strings.TrimSpace(strings.Join(trimEmpty([]string{item.When, item.Author}), " · "))
	if meta != "" {
		parts = append(parts, "("+meta+")")
	}
	return strings.Join(trimEmpty(parts), " ")
}

func formatReviewBranch(current string, item reviewBranchItem) string {
	current = strings.TrimSpace(current)
	if current == "" {
		current = "HEAD"
	}
	text := current + " -> " + item.Name
	if item.Current {
		text += " (current)"
	}
	return text
}

func formatReviewPR(item reviewPRItem) string {
	head := strings.TrimSpace(item.Head)
	author := strings.TrimSpace(item.Author)
	meta := strings.Join(trimEmpty([]string{head, author}), " · ")
	if meta != "" {
		meta = " (" + meta + ")"
	}
	return fmt.Sprintf("#%d %s%s", item.Number, item.Title, meta)
}

func renderReviewMenuRow(item reviewMenuItem, selected bool) string {
	muted := lipgloss.NewStyle().Foreground(tuitheme.Default.Muted)
	nameStyle := lipgloss.NewStyle()
	prefix := muted.Render("  ")
	if selected {
		prefix = lipgloss.NewStyle().Foreground(tuitheme.Default.InfoSoft).Bold(true).Render("> ")
		nameStyle = nameStyle.Foreground(tuitheme.Default.InfoSoft).Bold(true)
	}
	head := prefix + nameStyle.Render(item.Name)
	desc := strings.TrimSpace(item.Description)
	if desc == "" {
		return head
	}
	const descCol = 38
	gap := descCol - lipgloss.Width(head)
	if gap < 1 {
		gap = 1
	}
	return head + strings.Repeat(" ", gap) + muted.Render(desc)
}

func loadReviewCommitsCmd(cwd string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", "log", "--date=relative", "--pretty=format:%h%x1f%s%x1f%an%x1f%cr", "-n", "30")
		if strings.TrimSpace(cwd) != "" {
			cmd.Dir = cwd
		}
		out, err := cmd.Output()
		if err != nil {
			return reviewCommitsLoadedMsg{err: commandError("git log", err)}
		}
		items := parseReviewCommits(string(out))
		return reviewCommitsLoadedMsg{items: items}
	}
}

func loadReviewBranchesCmd(cwd string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", "branch", "--format=%(refname:short)\t%(HEAD)", "--sort=-committerdate")
		if strings.TrimSpace(cwd) != "" {
			cmd.Dir = cwd
		}
		out, err := cmd.Output()
		if err != nil {
			return reviewBranchesLoadedMsg{err: commandError("git branch", err)}
		}
		defaultBranch := loadReviewDefaultBranch(ctx, cwd)
		items := parseReviewBranches(string(out))
		return reviewBranchesLoadedMsg{items: items, defaultBranch: defaultBranch}
	}
}

func loadReviewDefaultBranch(ctx context.Context, cwd string) string {
	cmd := exec.CommandContext(ctx, "git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if strings.TrimSpace(cwd) != "" {
		cmd.Dir = cwd
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func loadReviewPRsCmd(cwd string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "gh", "pr", "list", "--limit", "30", "--json", "number,title,headRefName,author")
		if strings.TrimSpace(cwd) != "" {
			cmd.Dir = cwd
		}
		out, err := cmd.Output()
		if err != nil {
			return reviewPRsLoadedMsg{err: commandError("gh pr list", err)}
		}
		items, parseErr := parseReviewPRs(out)
		if parseErr != nil {
			return reviewPRsLoadedMsg{err: parseErr.Error()}
		}
		return reviewPRsLoadedMsg{items: items}
	}
}

func parseReviewBranches(raw string) []reviewBranchItem {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	items := make([]reviewBranchItem, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		name := strings.TrimSpace(fields[0])
		if name == "" {
			continue
		}
		current := len(fields) > 1 && strings.TrimSpace(fields[1]) == "*"
		items = append(items, reviewBranchItem{Name: name, Current: current})
	}
	return items
}

func parseReviewCommits(raw string) []reviewCommitItem {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	items := make([]reviewCommitItem, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\x1f")
		if len(fields) < 2 {
			continue
		}
		item := reviewCommitItem{SHA: strings.TrimSpace(fields[0]), Subject: strings.TrimSpace(fields[1])}
		if len(fields) > 2 {
			item.Author = strings.TrimSpace(fields[2])
		}
		if len(fields) > 3 {
			item.When = strings.TrimSpace(fields[3])
		}
		if item.SHA != "" {
			items = append(items, item)
		}
	}
	return items
}

func parseReviewPRs(raw []byte) ([]reviewPRItem, error) {
	var payload []struct {
		Number      int    `json:"number"`
		Title       string `json:"title"`
		HeadRefName string `json:"headRefName"`
		Author      struct {
			Login string `json:"login"`
		} `json:"author"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("parse gh pr list: %w", err)
	}
	items := make([]reviewPRItem, 0, len(payload))
	for _, pr := range payload {
		if pr.Number <= 0 {
			continue
		}
		items = append(items, reviewPRItem{
			Number: pr.Number,
			Title:  strings.TrimSpace(pr.Title),
			Head:   strings.TrimSpace(pr.HeadRefName),
			Author: strings.TrimSpace(pr.Author.Login),
		})
	}
	return items, nil
}

func commandError(name string, err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, exec.ErrNotFound) {
		if strings.HasPrefix(name, "gh ") {
			return "gh CLI not found. Install GitHub CLI or enter the PR number/URL manually."
		}
		return name + " not found on PATH"
	}
	if exit, ok := err.(*exec.ExitError); ok {
		msg := strings.TrimSpace(string(exit.Stderr))
		if msg != "" {
			return name + " failed: " + msg
		}
	}
	return name + " failed: " + err.Error()
}

func trimEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
