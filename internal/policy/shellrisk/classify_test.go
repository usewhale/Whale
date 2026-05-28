package shellrisk

import "testing"

func TestClassifyAllowsSafeReadOnlyCommands(t *testing.T) {
	for _, command := range []string{
		"date",
		"date -u",
		"date +%Y-%m-%d",
		"date --date tomorrow",
		"date --iso-8601",
		"date -R",
		"date --rfc-3339 seconds",
		"uname -a",
		"whoami",
		"id -u",
		"id -un",
		"which go",
		"command -v go",
		"git status --short",
		"git shortlog -sne HEAD",
		"rg whale internal",
		"uptime",
		"printf '%s\\n' ---",
		"sort go.mod",
		"uniq names.txt",
		"uniq -c names.txt",
		"uniq --count names.txt",
		"uniq -f 1 -s2 names.txt",
		"sort -u names.txt",
		"sort -rn counts.txt",
		"sort --numeric-sort --reverse counts.txt",
		"sort --key=2,2 --field-separator=: passwd.txt",
	} {
		got := Classify(command)
		if !got.Allow || got.Code != CodeSafeRead {
			t.Fatalf("Classify(%q) = %+v, want safe read allow", command, got)
		}
	}
}

func TestClassifyIdentifiesBoundedWriteCommands(t *testing.T) {
	tests := map[string]string{
		"go test ./...":       "shell:bounded:go:test",
		"go vet ./...":        "shell:bounded:go:vet",
		"make test":           "shell:bounded:make:test",
		"make build":          "shell:bounded:make:build",
		"cargo build":         "shell:bounded:cargo:build",
		"cargo test --all":    "shell:bounded:cargo:test",
		"npm test -- --watch": "shell:bounded:npm:test",
		"npm run build":       "shell:bounded:npm:run-build",
		"pnpm run typecheck":  "shell:bounded:pnpm:run-typecheck",
		"python -m pytest":    "shell:bounded:python:-m-pytest",
		"pytest tests":        "shell:bounded:pytest",
		"deno test":           "shell:bounded:deno:test",
		"bun test":            "shell:bounded:bun:test",
		"npx jest":            "shell:bounded:npx:jest",
		"npx vitest run":      "shell:bounded:npx:vitest",
		"npx tsc --noEmit":    "shell:bounded:npx:tsc-noemit",
	}
	for command, wantKey := range tests {
		got := Classify(command)
		if got.Allow || got.Code != CodeBoundedWrite || got.Level != LevelBoundedWrite {
			t.Fatalf("Classify(%q) = %+v, want bounded write", command, got)
		}
		if len(got.ApprovalKeys) != 1 || got.ApprovalKeys[0] != wantKey {
			t.Fatalf("Classify(%q) keys = %v, want %s", command, got.ApprovalKeys, wantKey)
		}
		if len(got.WriteScopes) == 0 {
			t.Fatalf("Classify(%q) missing write scopes: %+v", command, got)
		}
	}
}

func TestClassifyRequiresExactApprovalForGoBinaryOutputs(t *testing.T) {
	for _, command := range []string{
		"go test -c ./pkg",
		"go test -c=true ./pkg",
		"go build ./cmd/app",
		"go build .",
	} {
		got := Classify(command)
		if got.Allow || got.Code != CodeNeedsApproval || got.Level != LevelNeedsApproval || len(got.ApprovalKeys) != 0 {
			t.Fatalf("Classify(%q) = %+v, want exact approval", command, got)
		}
	}
}

func TestClassifyRequiresExactApprovalForGoTestExecWrappers(t *testing.T) {
	for _, command := range []string{
		"go test -exec ./wrapper ./pkg",
		"go test -exec=./wrapper ./pkg",
		"go test -toolexec ./wrapper ./pkg",
		"go test -toolexec=./wrapper ./pkg",
	} {
		got := Classify(command)
		if got.Allow || got.Code != CodeNeedsApproval || got.Level != LevelNeedsApproval || len(got.ApprovalKeys) != 0 {
			t.Fatalf("Classify(%q) = %+v, want exact approval", command, got)
		}
	}
}

func TestClassifyRejectsBoundedWriteCommandsWithExplicitOutputPaths(t *testing.T) {
	for _, command := range []string{
		"go test -coverprofile coverage.out ./...",
		"go test -o testbin ./...",
		"go build -o ./bin/app ./cmd/app",
		"cargo build --target-dir ../target",
		"npx jest --outputFile=/tmp/jest.json --json",
		"npx vitest --outputFile=/tmp/v.json",
		"pytest --junitxml=/tmp/x.xml",
		"python -m pytest --junitxml /tmp/x.xml",
		"pytest --cov-report=xml:/tmp/cov.xml",
	} {
		got := Classify(command)
		if got.Allow || got.Code != CodeUnsafeArgs {
			t.Fatalf("Classify(%q) = %+v, want unsafe args", command, got)
		}
	}
}

func TestClassifyRejectsUnsafeDateVariants(t *testing.T) {
	for _, command := range []string{
		"date -s now",
		"date --set now",
		"date --set=now",
		"date -f dates.txt",
		"date --file dates.txt",
		"date 052016002026",
		"date -i",
	} {
		got := Classify(command)
		if got.Allow || got.Code != CodeUnsafeArgs {
			t.Fatalf("Classify(%q) = %+v, want unsafe args", command, got)
		}
	}
}

func TestClassifyRejectsCompoundOrRedirectedCommands(t *testing.T) {
	got := Classify("command -v go > out")
	if got.Allow || got.Code != CodeParseFailed {
		t.Fatalf("Classify(redirected command lookup) = %+v, want parse failure", got)
	}

	for _, command := range []string{
		"date; rm -rf /tmp/x",
		"git status --short; make build",
	} {
		got := Classify(command)
		if got.Allow || got.Code != CodeNeedsApproval {
			t.Fatalf("Classify(%q) = %+v, want needs approval", command, got)
		}
	}
	got = Classify("which go && rm -rf /tmp/x")
	if got.Allow || got.Code != CodeNeedsApproval {
		t.Fatalf("Classify(unsafe && list) = %+v, want needs approval", got)
	}
}

func TestClassifyAllowsSafeReadOnlyPipelines(t *testing.T) {
	got := Classify("uname -a | cat")
	if !got.Allow || got.Code != CodeSafeRead {
		t.Fatalf("Classify(read-only pipeline) = %+v, want safe read allow", got)
	}

	got = Classify("git show HEAD:internal/app/config_file.go | sed -n '300,459p'")
	if !got.Allow || got.Code != CodeSafeRead {
		t.Fatalf("Classify(git show sed pipeline) = %+v, want safe read allow", got)
	}

	got = Classify("git branch --list 'feature/worktree-command' && git rev-parse --abbrev-ref HEAD")
	if !got.Allow || got.Code != CodeSafeRead {
		t.Fatalf("Classify(read-only && list) = %+v, want safe read allow", got)
	}

	got = Classify("git diff --stat HEAD 2>/dev/null; printf '%s\\n' ---; git ls-files | sort")
	if !got.Allow || got.Code != CodeSafeRead {
		t.Fatalf("Classify(read-only ; list) = %+v, want safe read allow", got)
	}

	got = Classify("git log --oneline | grep feature | sort -u")
	if !got.Allow || got.Code != CodeSafeRead {
		t.Fatalf("Classify(read-only filtered git log) = %+v, want safe read allow", got)
	}

	got = Classify(`echo "=== GIT STATUS ===" && git status && echo "" && echo "=== CURRENT BRANCH ===" && git branch --show-current && echo "" && echo "=== RECENT 5 COMMITS ===" && git log --oneline -5 && echo "" && echo "=== GO FILES UNDER internal/ ===" && find internal -name '*.go' -type f | wc -l`)
	if !got.Allow || got.Code != CodeSafeRead {
		t.Fatalf("Classify(read-only status/log/count command) = %+v, want safe read allow", got)
	}

	got = Classify(`echo "=== git status ===" && git status && echo "" && echo "=== 最近5条提交 ===" && git log --oneline -5 && echo "" && echo "=== 当前分支 ===" && git branch --show-current && echo "" && echo "=== internal 下 Go 文件数量 ===" && find internal -name '*.go' -type f | wc -l && echo "" && echo "=== internal 下 Go 文件（含测试文件）明细 ===" && find internal -name '*.go' -type f | sed 's/.*\\.//' | sort | uniq -c | sort -rn`)
	if !got.Allow || got.Code != CodeSafeRead {
		t.Fatalf("Classify(read-only status/log/count/details command) = %+v, want safe read allow", got)
	}
}

func TestClassifyRejectsUnsafeExistingAutoAllowVariants(t *testing.T) {
	for _, command := range []string{
		"find . -delete",
		"git diff --output=out.patch",
		"git show --ext-diff HEAD",
		"git log --textconv",
		"rg --pre ./danger pattern",
		"npx jest -u",
		"sort -o out.txt input.txt",
		"sort --output=out.txt input.txt",
		"sort --compress-program=touch bigfile",
		"sort --compress-program touch bigfile",
		"sort -T /tmp input.txt",
		"sort --temporary-directory=/tmp input.txt",
		"sort --random-source=/dev/zero input.txt",
		"uniq input.txt output.txt",
		"uniq --count input.txt output.txt",
		"uniq --output=out.txt input.txt",
	} {
		got := Classify(command)
		if got.Allow || got.Code != CodeUnsafeArgs {
			t.Fatalf("Classify(%q) = %+v, want unsafe args", command, got)
		}
	}

	for _, command := range []string{
		"sed 's/foo/bar/w out.txt' go.mod",
		"sed 's/foo/bar/e' go.mod",
	} {
		got := Classify(command)
		if got.Allow || got.Code != CodeNeedsApproval {
			t.Fatalf("Classify(%q) = %+v, want needs approval", command, got)
		}
	}
}

func TestClassifyRequiresApprovalForUnsafeReadOnlyPipelinesAndLists(t *testing.T) {
	for _, command := range []string{
		"git show HEAD:internal/app/config_file.go | sed -i '300,459p'",
		"git show HEAD:internal/app/config_file.go | sed -n '300,459w out.txt'",
		"git status --short && git branch -D feature",
	} {
		got := Classify(command)
		if got.Allow || got.Code != CodeNeedsApproval {
			t.Fatalf("Classify(%q) = %+v, want needs approval", command, got)
		}
	}
}

func TestClassifyRejectsUnclassifiedCommands(t *testing.T) {
	for _, command := range []string{
		"env",
		"printenv",
		"npm install",
		"curl https://example.com",
	} {
		got := Classify(command)
		if got.Allow || got.Code != CodeNeedsApproval {
			t.Fatalf("Classify(%q) = %+v, want needs approval", command, got)
		}
	}
}

func TestClassifyRejectsUnparseableUnsafeCommands(t *testing.T) {
	got := Classify("find . -exec rm {} +")
	if got.Allow || got.Code != CodeParseFailed {
		t.Fatalf("Classify(find -exec) = %+v, want parse failure", got)
	}
}
