package tui

import (
	"fmt"
	"strings"
)

func (m model) renderReviewMenu() string {
	rows := []string{
		pickerTitle("Review"),
		pickerHint("Choose what to review"),
		"",
	}
	for i, item := range reviewMenuItems() {
		rows = append(rows, renderReviewMenuRow(item, i == m.reviewMenu.selected))
	}
	rows = append(rows, "", pickerHint("  ↑/↓ select · Enter confirm · Esc close"))
	return strings.Join(rows, "\n")
}

func (m model) renderReviewTargetPicker() string {
	rows := []string{}
	switch m.mode {
	case modeReviewBranchPicker:
		rows = append(rows, pickerTitle("Choose base branch"))
		rows = append(rows, pickerHint(branchSearchLine(m.reviewTargetPicker.query)))
	case modeReviewCommitPicker:
		rows = append(rows, pickerTitle("Choose commit"))
	case modeReviewPRPicker:
		rows = append(rows, pickerTitle("Choose pull request"))
	}
	if m.reviewTargetPicker.loading {
		rows = append(rows, pickerHint("Loading..."), "", pickerHint("  Esc back"))
		return strings.Join(rows, "\n")
	}
	if m.reviewTargetPicker.err != "" {
		rows = append(rows, pickerHint(m.reviewTargetPicker.err))
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
	rows = append(rows, "", pickerHint("  ↑/↓ select · Enter confirm · / type manually · Esc back"))
	return strings.Join(rows, "\n")
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
	return pickerRow(text, selected, false)
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
	return pickerSuggestionRow(item.Name, item.Description, selected, 36)
}
