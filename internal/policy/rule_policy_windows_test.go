//go:build windows

package policy

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func TestRulePolicyExternalDirectoryAllowsWindowsTemp(t *testing.T) {
	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules(), WorkspaceRoot: `C:\Users\tester\repo`}
	command := `cat ` + filepath.Join(os.TempDir(), "whale.txt")

	got := p.Decide(core.ToolSpec{Name: "shell_run"}, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
	if got.RequiresApproval || !got.Allow {
		t.Fatalf("windows temp path should not require external-directory approval: %+v", got)
	}
}

func TestRulePolicyExternalDirectoryStillFlagsNonTempWindowsPath(t *testing.T) {
	p := RulePolicy{Default: PermissionAllow, Rules: DefaultRules(), WorkspaceRoot: `C:\Users\tester\repo`}
	command := `cat C:\Users\tester\Documents\secret.txt`

	got := p.Decide(core.ToolSpec{Name: "shell_run"}, core.ToolCall{Name: "shell_run", Input: `{"command":` + strconv.Quote(command) + `}`})
	if !got.RequiresApproval || got.MatchedRule != "external_directory:*=ask" {
		t.Fatalf("non-temp external windows path should require external-directory approval: %+v", got)
	}
}
