package tui

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	tuitheme "github.com/usewhale/whale/internal/tui/theme"
)

const (
	maxFileSuggestionRows       = 8
	maxFileSuggestionCandidates = 512
)

var fileSuggestionIgnoredDirs = map[string]bool{
	".git":          true,
	".gocache":      true,
	".cache":        true,
	".venv":         true,
	"venv":          true,
	"__pycache__":   true,
	".mypy_cache":   true,
	".pytest_cache": true,
	".tox":          true,
	".idea":         true,
	".vscode":       true,
	"node_modules":  true,
	"vendor":        true,
	"dist":          true,
	"build":         true,
	".next":         true,
	".nuxt":         true,
	".svelte-kit":   true,
	".turbo":        true,
	"target":        true,
}

type fileSuggestion struct {
	Path  string
	IsDir bool
}

type fileSuggestionsLoadedMsg struct {
	token   int
	root    string
	query   string
	matches []fileSuggestion
}

func (m *model) updateFileMatches() tea.Cmd {
	if m.mode != modeChat || m.busy || m.hasSlashPanel() {
		clearFileSuggestions(m)
		return nil
	}
	raw := m.input.Value()
	if strings.Contains(raw, "\n") || (m.inHistoryNav && raw == m.lastHistoryText) {
		clearFileSuggestions(m)
		return nil
	}
	query, ok := m.input.CurrentPrefixedToken('@')
	if !ok {
		clearFileSuggestions(m)
		return nil
	}
	query = normalizeFileSuggestionQuery(query)
	root := cleanFileSuggestionRoot(m.cwdPath)
	if m.files.active && m.files.query == query && m.files.root == root {
		return nil
	}
	m.cancelFileSuggestionSearch()
	m.files.matches = nil
	m.files.selected = 0
	m.files.searching = false
	m.files.active = true
	m.files.query = query
	m.files.root = root
	if query == "" {
		return nil
	}
	m.files.token++
	token := m.files.token
	ctx, cancel := context.WithCancel(context.Background())
	m.files.cancel = cancel
	m.files.searching = true
	return fileSuggestionSearchCmd(ctx, token, root, query)
}

func (m *model) applyFileSuggestionsLoaded(msg fileSuggestionsLoadedMsg) {
	// Canceled searches may still emit an empty message after a newer query
	// starts. The token/root/query guards below keep those stale results from
	// mutating the active panel; the current search owns the searching state.
	if msg.token != m.files.token || msg.root != m.files.root || msg.query != m.files.query || !m.files.active {
		return
	}
	query, ok := m.input.CurrentPrefixedToken('@')
	if !ok || normalizeFileSuggestionQuery(query) != msg.query {
		return
	}
	m.files.matches = msg.matches
	m.files.searching = false
	m.files.cancel = nil
	if m.files.selected >= len(m.files.matches) {
		m.files.selected = max(0, len(m.files.matches)-1)
	}
}

func fileSuggestionSearchCmd(ctx context.Context, token int, root, query string) tea.Cmd {
	return func() tea.Msg {
		return fileSuggestionsLoadedMsg{
			token:   token,
			root:    root,
			query:   query,
			matches: findFileSuggestionsWithCancel(ctx, root, query),
		}
	}
}

func (m *model) cancelFileSuggestionSearch() {
	if m.files.cancel == nil {
		return
	}
	m.files.cancel()
	m.files.cancel = nil
}

func cleanFileSuggestionRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "."
	}
	return filepath.Clean(root)
}

func normalizeFileSuggestionQuery(query string) string {
	return strings.ToLower(filepath.ToSlash(strings.TrimSpace(query)))
}

func findFileSuggestions(root, query string) []fileSuggestion {
	return findFileSuggestionsWithCancel(context.Background(), root, query)
}

func findFileSuggestionsWithCancel(ctx context.Context, root, query string) []fileSuggestion {
	root = cleanFileSuggestionRoot(root)
	query = normalizeFileSuggestionQuery(query)
	if query == "" || ctx.Err() != nil {
		return nil
	}
	queryParts := strings.FieldsFunc(query, func(r rune) bool {
		return r == '/' || r == '\\'
	})

	var out []fileSuggestion
	scanned := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
		if err != nil {
			return nil
		}
		if path == root {
			return nil
		}
		name := d.Name()
		if d.IsDir() && fileSuggestionIgnoredDirs[name] {
			return filepath.SkipDir
		}
		scanned++
		if scanned%256 == 0 && ctx.Err() != nil {
			return filepath.SkipAll
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if fileSuggestionMatches(rel, query, queryParts) {
			out = append(out, fileSuggestion{Path: rel, IsDir: d.IsDir()})
			if len(out) > maxFileSuggestionCandidates {
				sortFileSuggestions(out, query)
				out = out[:maxFileSuggestionCandidates]
			}
		}
		return nil
	})
	if ctx.Err() != nil {
		return nil
	}
	sortFileSuggestions(out, query)
	if len(out) > maxFileSuggestionRows {
		out = out[:maxFileSuggestionRows]
	}
	return out
}

func sortFileSuggestions(out []fileSuggestion, query string) {
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		aq, bq := fileSuggestionRank(a.Path, query), fileSuggestionRank(b.Path, query)
		if aq != bq {
			return aq < bq
		}
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		if len(a.Path) != len(b.Path) {
			return len(a.Path) < len(b.Path)
		}
		return a.Path < b.Path
	})
}

func fileSuggestionMatches(path, query string, queryParts []string) bool {
	if query == "" {
		return true
	}
	lower := strings.ToLower(path)
	if strings.Contains(lower, query) || strings.Contains(strings.ToLower(filepath.Base(path)), query) {
		return true
	}
	pos := 0
	for _, part := range queryParts {
		if part == "" {
			continue
		}
		idx := strings.Index(lower[pos:], part)
		if idx < 0 {
			return false
		}
		pos += idx + len(part)
	}
	return true
}

func fileSuggestionRank(path, query string) int {
	if query == "" {
		return 20
	}
	lower := strings.ToLower(path)
	base := strings.ToLower(filepath.Base(path))
	switch {
	case lower == query:
		return 0
	case base == query:
		return 1
	case strings.HasPrefix(lower, query):
		return 2
	case strings.HasPrefix(base, query):
		return 3
	case strings.Contains(base, query):
		return 4
	case strings.Contains(lower, query):
		return 5
	default:
		return 10
	}
}

func (m model) hasFileSuggestions() bool {
	return len(m.files.matches) > 0
}

func (m model) hasFilePanel() bool {
	return m.files.active || m.hasFileSuggestions()
}

func (m model) renderFileSuggestions() string {
	rows := []string{"Files"}
	if len(m.files.matches) == 0 {
		if m.files.searching {
			rows = append(rows, "  Searching workspace files...")
		} else if strings.TrimSpace(m.files.query) != "" {
			rows = append(rows, "  No file matches")
		} else {
			rows = append(rows, "  Type to search workspace files")
		}
		return lipgloss.NewStyle().Foreground(tuitheme.Default.Info).Render(strings.Join(rows, "\n"))
	}
	start := 0
	if len(m.files.matches) > maxFileSuggestionRows {
		start = m.files.selected - maxFileSuggestionRows/2
		if start < 0 {
			start = 0
		}
		if start > len(m.files.matches)-maxFileSuggestionRows {
			start = len(m.files.matches) - maxFileSuggestionRows
		}
	}
	end := len(m.files.matches)
	if end > start+maxFileSuggestionRows {
		end = start + maxFileSuggestionRows
	}
	for i := start; i < end; i++ {
		item := m.files.matches[i]
		prefix := "  "
		if i == m.files.selected {
			prefix = "> "
		}
		kind := "file"
		display := item.Path
		if item.IsDir {
			kind = "dir"
			display += "/"
		}
		rows = append(rows, fmt.Sprintf("%s%-48s %s", prefix, display, kind))
	}
	rows = append(rows, "  ↑/↓ navigate · Tab/Enter insert · Esc cancel")
	return lipgloss.NewStyle().Foreground(tuitheme.Default.Info).Render(strings.Join(rows, "\n"))
}

func (m model) selectedFileSuggestion() (fileSuggestion, bool) {
	if m.files.selected < 0 || m.files.selected >= len(m.files.matches) {
		return fileSuggestion{}, false
	}
	return m.files.matches[m.files.selected], true
}

func (m *model) insertSelectedFileSuggestion() bool {
	if !m.hasFileSuggestions() {
		return false
	}
	selected, ok := m.selectedFileSuggestion()
	if !ok || strings.TrimSpace(selected.Path) == "" {
		return false
	}
	insert := quoteFileSuggestionPath(selected.Path) + " "
	if !m.input.ReplaceCurrentPrefixedToken('@', insert) {
		return false
	}
	clearFileSuggestions(m)
	m.skillBinding = nil
	m.resetHistoryNavigation()
	m.refreshViewportContent()
	return true
}

func quoteFileSuggestionPath(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		path = "./" + path
	}
	if !strings.ContainsAny(path, " \t\"\\") {
		return path
	}
	var b strings.Builder
	b.Grow(len(path) + 2)
	b.WriteByte('"')
	for i := 0; i < len(path); i++ {
		c := path[i]
		if c == '"' || c == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteByte('"')
	return b.String()
}

func clearFileSuggestions(m *model) {
	m.cancelFileSuggestionSearch()
	m.files.active = false
	m.files.matches = nil
	m.files.selected = 0
	m.files.query = ""
	m.files.root = ""
	m.files.searching = false
}
