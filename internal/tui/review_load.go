package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

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
