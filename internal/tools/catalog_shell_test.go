package tools

import (
	"strconv"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/shell"
)

func TestShellRunDescriptionIncludesPowerShellRuntimeGuidance(t *testing.T) {
	desc := shellRunDescriptionFor(shell.RuntimeDescription{
		GOOS: "windows",
		Spec: shell.Spec{Kind: shell.KindPowerShell, DisplayName: "PowerShell"},
	})
	for _, want := range []string{
		"Run a shell command from the current Whale workspace.",
		"Runtime shell: PowerShell",
		"Use PowerShell syntax",
		"Get-ChildItem",
		"Select-String",
		"$env:TEMP",
		"$env:FOO",
		"read_file",
		"list_dir",
		"Avoid POSIX-only assumptions",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q:\n%s", want, desc)
		}
	}
}

func TestShellRunDescriptionIncludesCmdRuntimeGuidance(t *testing.T) {
	desc := shellRunDescriptionFor(shell.RuntimeDescription{
		GOOS: "windows",
		Spec: shell.Spec{Kind: shell.KindCmd, DisplayName: "cmd.exe"},
	})
	for _, want := range []string{
		"Runtime shell: cmd.exe",
		"Use cmd.exe syntax",
		"dir",
		"findstr",
		"%TEMP%",
		"%FOO%",
		"read_file",
		"list_dir",
		"Avoid PowerShell-only syntax",
	} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q:\n%s", want, desc)
		}
	}
}

func TestShellReadOnlyCheckRejectsWindowsShellCommands(t *testing.T) {
	powerShellSpec := shellRunReadOnlySpecFor(shell.KindPowerShell)
	cmdSpec := shellRunReadOnlySpecFor(shell.KindCmd)

	for _, tc := range []struct {
		name    string
		spec    core.ToolSpec
		command string
	}{
		{name: "powershell list", spec: powerShellSpec, command: "Get-ChildItem internal"},
		{name: "powershell search", spec: powerShellSpec, command: "Select-String -Path internal/*.go -Pattern Runtime"},
		{name: "powershell double-quoted pattern", spec: powerShellSpec, command: `Select-String -Path internal/*.go -Pattern "Runtime|ToolGuidance"`},
		{name: "powershell single-quoted pattern", spec: powerShellSpec, command: `Select-String -Path internal/*.go -Pattern 'Runtime|ToolGuidance'`},
		{name: "cmd list", spec: cmdSpec, command: "dir internal"},
		{name: "cmd quoted path", spec: cmdSpec, command: `dir "x & y"`},
		{name: "cmd search", spec: cmdSpec, command: "findstr /s Runtime internal\\*.go"},
		{name: "cmd quoted pattern", spec: cmdSpec, command: `findstr /c:"Runtime|ToolGuidance" internal\*.go`},
		{name: "posix command on powershell runtime", spec: powerShellSpec, command: "ls internal"},
		{name: "posix command on cmd runtime", spec: cmdSpec, command: "ls internal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(tc.command) + `}`}
			if core.IsReadOnlyToolCall(tc.spec, call) {
				t.Fatalf("expected %q to require approval", tc.command)
			}
		})
	}
}

func TestShellReadOnlyCheckRejectsChainedOrRedirectedCommands(t *testing.T) {
	powerShellSpec := shellRunReadOnlySpecFor(shell.KindPowerShell)
	cmdSpec := shellRunReadOnlySpecFor(shell.KindCmd)
	posixSpec := shellRunReadOnlySpecFor(shell.KindPOSIX)

	for _, tc := range []struct {
		name    string
		spec    core.ToolSpec
		command string
	}{
		{name: "powershell semicolon", spec: powerShellSpec, command: "Get-ChildItem .; Remove-Item foo"},
		{name: "powershell pipe", spec: powerShellSpec, command: "Select-String -Path internal/*.go -Pattern Runtime | Remove-Item foo"},
		{name: "powershell redirection", spec: powerShellSpec, command: "Get-ChildItem . > out.txt"},
		{name: "powershell subexpression", spec: powerShellSpec, command: "Get-ChildItem $(Remove-Item foo)"},
		{name: "powershell double-quoted subexpression", spec: powerShellSpec, command: `Get-ChildItem "$(Remove-Item foo)"`},
		{name: "powershell parenthesized command", spec: powerShellSpec, command: "Get-ChildItem (Remove-Item foo)"},
		{name: "cmd redirection", spec: cmdSpec, command: "dir . > out.txt"},
		{name: "cmd ampersand", spec: cmdSpec, command: "dir . & del foo"},
		{name: "cmd single quotes do not quote", spec: cmdSpec, command: "dir 'x & del foo & rem '"},
		{name: "cmd env expansion", spec: cmdSpec, command: "dir %TEMP%"},
		{name: "cmd caret escape", spec: cmdSpec, command: "dir ^& del foo"},
		{name: "newline separator", spec: cmdSpec, command: "dir .\ndel foo"},
		{name: "posix command substitution", spec: posixSpec, command: "ls $(rm foo)"},
		{name: "posix backtick substitution", spec: posixSpec, command: "ls `rm foo`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(tc.command) + `}`}
			if core.IsReadOnlyToolCall(tc.spec, call) {
				t.Fatalf("expected %q to require approval", tc.command)
			}
		})
	}
}

func TestShellReadOnlyCheckRejectsUnsafeReadCommandArgs(t *testing.T) {
	spec := shellRunReadOnlySpecFor(shell.KindPOSIX)
	for _, command := range []string{
		"find . -delete",
		"find . -exec rm {} +",
		"find . -fprint out",
		"find . -fprint0 out",
		"find . -fprintf out %p",
		`find . "-exec" rm {} +`,
		`find . '-delete'`,
		`find . "-fprint0" out`,
		`find . -\exec rm {} +`,
		"git diff --output=out.patch",
		"git show --ext-diff HEAD",
		`git show "--output=out.patch" HEAD`,
		"git log --textconv",
		"rg --pre ./danger pattern",
		`rg "--pre=./danger" pattern`,
		`rg "--pre" ./danger pattern`,
	} {
		call := core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`}
		if core.IsReadOnlyToolCall(spec, call) {
			t.Fatalf("expected unsafe read-command args in %q to require approval", command)
		}
	}
}

func TestShellReadOnlyCheckRejectsTestAndBuildCommands(t *testing.T) {
	spec := shellRunReadOnlySpecFor(shell.KindPOSIX)
	for _, command := range []string{
		"make test",
		"make test-tui",
		"make test-evals",
		"make fmt-check",
		"make vet",
		"make build",
		"go test ./...",
		"go vet ./...",
		"npm run test -- --runInBand",
		"npm run typecheck",
		"python -m pytest tests",
		"cargo check --workspace",
	} {
		call := core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`}
		if core.IsReadOnlyToolCall(spec, call) {
			t.Fatalf("expected test/build command %q not to be strict read-only", command)
		}
	}
}

func TestShellReadOnlyCheckRejectsMutatingNearMisses(t *testing.T) {
	spec := shellRunReadOnlySpecFor(shell.KindPOSIX)
	for _, command := range []string{
		"make clean",
		"npm install lodash",
		"npm run lint -- --fix",
		"npx jest --updateSnapshot",
		"npx jest -u",
		"npx vitest run --update",
		"go testfoo ./...",
		"make test clean",
		"make test build",
		"make build clean",
		"make test GOCACHE_DIR=.gocache",
		"make test && rm -rf bin",
		"make test > out.txt",
	} {
		call := core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`}
		if core.IsReadOnlyToolCall(spec, call) {
			t.Fatalf("expected mutating or unsafe command %q to require approval", command)
		}
	}
}

func TestShellReadOnlyCheckAllowsQuotedSearchPatterns(t *testing.T) {
	spec := shellRunReadOnlySpecFor(shell.KindPOSIX)
	for _, command := range []string{
		`grep "Runtime|ToolGuidance" internal/tools/catalog_shell.go`,
		`grep 'Runtime;ToolGuidance' internal/tools/catalog_shell.go`,
		"git status -u",
		"ls -u",
	} {
		call := core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`}
		if !core.IsReadOnlyToolCall(spec, call) {
			t.Fatalf("expected quoted search pattern in %q to be read-only", command)
		}
	}
}

func TestShellReadOnlyCheckRejectsPOSIXExpansionSyntax(t *testing.T) {
	spec := shellRunReadOnlySpecFor(shell.KindPOSIX)
	for _, command := range []string{
		`grep Runtime internal/*.go`,
		`grep Runtime $HOME/file`,
		`ls \; rm foo`,
		`ls "unterminated`,
		`echo hi # comment`,
	} {
		call := core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`}
		if core.IsReadOnlyToolCall(spec, call) {
			t.Fatalf("expected POSIX shell expansion syntax in %q to require approval", command)
		}
	}
}

func TestShellReadOnlyCheckIsRuntimeSpecific(t *testing.T) {
	posixSpec := shellRunReadOnlySpecFor(shell.KindPOSIX)
	for _, command := range []string{
		"Get-ChildItem internal",
		"Select-String -Path internal/*.go -Pattern Runtime",
		"dir internal",
		"findstr /s Runtime internal\\*.go",
	} {
		call := core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`}
		if core.IsReadOnlyToolCall(posixSpec, call) {
			t.Fatalf("expected POSIX runtime to reject Windows command %q", command)
		}
	}
}

func shellRunReadOnlySpecFor(kind shell.Kind) core.ToolSpec {
	return core.DescribeTool(toolFn{
		name: "shell_run",
		readOnlyCheck: shellReadOnlyCheckFor(shell.RuntimeDescription{
			Spec: shell.Spec{Kind: kind},
		}),
	})
}
