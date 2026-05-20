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
		"rg whale internal",
		"uptime",
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
	for _, command := range []string{
		"date; rm -rf /tmp/x",
		"command -v go > out",
	} {
		got := Classify(command)
		if got.Allow || got.Code != CodeParseFailed {
			t.Fatalf("Classify(%q) = %+v, want parse failure", command, got)
		}
	}
	got := Classify("which go && rm -rf /tmp/x")
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
}

func TestClassifyRejectsUnsafeExistingAutoAllowVariants(t *testing.T) {
	for _, command := range []string{
		"find . -delete",
		"git diff --output=out.patch",
		"git show --ext-diff HEAD",
		"git log --textconv",
		"rg --pre ./danger pattern",
		"npx jest -u",
	} {
		got := Classify(command)
		if got.Allow || got.Code != CodeUnsafeArgs {
			t.Fatalf("Classify(%q) = %+v, want unsafe args", command, got)
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
