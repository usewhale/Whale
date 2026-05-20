package commands

import (
	"fmt"
	"strings"
)

func ReviewPromptFromArgs(args string) (string, error) {
	target, err := parseReviewArgs(args)
	if err != nil {
		return "", err
	}
	return buildReviewPrompt(target.kind, target.target, target.custom), nil
}

func ReviewShellAllowPrefixesFromArgs(args string) ([]string, error) {
	target, err := parseReviewArgs(args)
	if err != nil {
		return nil, err
	}
	if target.kind != "pr" {
		return nil, nil
	}
	return []string{"gh pr list", "gh pr view", "gh pr diff"}, nil
}

type reviewTarget struct {
	kind   string
	target string
	custom string
}

func parseReviewArgs(args string) (reviewTarget, error) {
	args = strings.TrimSpace(args)
	out := reviewTarget{kind: "local"}

	if args != "" {
		fields := strings.Fields(args)
		switch fields[0] {
		case "local", "changes":
			if len(fields) > 1 {
				return reviewTarget{}, fmt.Errorf("usage: /review local")
			}
			out.kind = "local"
		case "branch":
			if len(fields) > 2 {
				return reviewTarget{}, fmt.Errorf("usage: /review branch [base]")
			}
			out.kind = "branch"
			if len(fields) == 2 {
				out.target = fields[1]
			}
		case "pr":
			if len(fields) != 2 {
				return reviewTarget{}, fmt.Errorf("usage: /review pr <number-or-url>")
			}
			out.kind = "pr"
			out.target = fields[1]
		case "commit":
			if len(fields) != 2 {
				return reviewTarget{}, fmt.Errorf("usage: /review commit <sha>")
			}
			out.kind = "commit"
			out.target = fields[1]
		default:
			out.kind = "custom"
			out.custom = args
		}
	}

	return out, nil
}

func buildReviewPrompt(kind, target, custom string) string {
	var targetBlock string
	switch kind {
	case "branch":
		if strings.TrimSpace(target) == "" {
			targetBlock = `Target: current branch vs the repository default branch.

Determine the default branch using simple read-only git commands run one at a time. Prefer:
- git symbolic-ref --short refs/remotes/origin/HEAD
- git branch -r

Use the remote ref directly if available, for example git diff origin/main...HEAD. Avoid shell pipelines, redirects, command substitutions, and || fallbacks. Then review:
- git status --short
- git diff <base>...HEAD`
		} else {
			quotedRange := shellQuoteArg(target + "...HEAD")
			targetBlock = fmt.Sprintf(`Target: current branch vs %s.

Review:
- git status --short
- git diff %s`, target, quotedRange)
		}
	case "pr":
		quotedTarget := shellQuoteArg(target)
		targetBlock = fmt.Sprintf(`Target: pull request %s.

Review:
- gh pr view %s --json title,body,state,author,files,additions,deletions,reviews,comments,createdAt,mergedAt,closedAt,baseRefName,headRefName,isDraft
- gh pr diff %s`, target, quotedTarget, quotedTarget)
	case "commit":
		quotedTarget := shellQuoteArg(target)
		targetBlock = fmt.Sprintf(`Target: commit %s.

Review:
- git show --stat --patch %s`, target, quotedTarget)
	case "custom":
		targetBlock = fmt.Sprintf(`Target: custom review request.

User request:
%s

Determine the relevant files, diff, or pull request before reviewing.`, custom)
	default:
		targetBlock = `Target: local changes.

Review:
- git status --short
- git diff --cached
- git diff

If git status shows untracked files with ??, inspect the contents of each relevant untracked file before reporting findings. Use read-only file inspection or git diff --no-index /dev/null <file> for those files.

If both staged and unstaged diffs are empty but the branch has committed changes, determine the default branch with simple read-only git commands run one at a time. Prefer git symbolic-ref --short refs/remotes/origin/HEAD, then use the returned remote ref directly, for example git diff origin/main...HEAD. Avoid shell pipelines, redirects, command substitutions, and || fallbacks.`
	}

	return strings.TrimSpace(fmt.Sprintf(`You are an expert code reviewer. Perform a read-only review and do not modify files.

%s

Review rules:
- Focus on correctness, security, hidden behavior changes, missing tests, and meaningful maintainability issues.
- Prefer project conventions over generic style rules.
- Do not report speculative issues. If evidence is weak, omit the finding.
- Do not include praise sections by default.
- Shell commands already run from the workspace root. Do not prefix commands with cd; use the shell_run cwd parameter for subdirectories.
- For review shell commands, run one plain read-only command at a time. Do not add redirects, pipes, command substitutions, semicolon chains, && chains, || fallbacks, or temp/workspace diff capture files.
- If a PR diff is truncated, use the PR file list to identify relevant files. If the PR head branch is present locally, inspect large files with git diff <base>...<head> -- <path>. Do not assume gh pr diff supports path filtering.
- Do not use read_file for PR-added files unless you have confirmed the PR head is checked out locally. For PR files that are not present in the workspace, rely on the PR diff output you have and clearly state any remaining visibility limits.

Output format:
- Start with findings, ordered by severity.
- Each finding must include file/line, the problem, impact, and concrete fix direction.
- If you find no actionable issues, say that clearly and mention exactly what you reviewed.
- Keep the review concise.`, targetBlock))
}

func shellQuoteArg(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
}
