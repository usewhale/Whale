package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/memory"
)

func TestBuildProviderHistoryIncludesProjectMemory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("project-memory"), 0o600); err != nil {
		t.Fatalf("write memory: %v", err)
	}
	a := NewAgentWithRegistry(nil, nil, core.NewToolRegistry(nil), WithProjectMemory(true, 8000, []string{"AGENTS.md"}, dir))
	h := []core.Message{{Role: core.RoleUser, SessionID: "s1", Text: "hi"}}
	rt := memory.HydrateRuntime(memory.NewImmutablePrefix(a.buildImmutableSystemBlocks()), h)
	rt.SetRuntimeBlocks(a.buildRuntimeSystemBlocks())
	out := a.buildTurnProviderHistory("s1", rt)
	if len(out) != 3 || out[0].Role != core.RoleSystem || out[1].Role != core.RoleSystem {
		t.Fatalf("unexpected history shape: %+v", out)
	}
	if strings.Contains(out[0].Text, "Project Memory") || strings.Contains(out[0].Text, "project-memory") {
		t.Fatalf("project memory leaked into immutable prefix: %s", out[0].Text)
	}
	if !strings.Contains(out[1].Text, "Project Memory") || !strings.Contains(out[1].Text, "project-memory") {
		t.Fatalf("missing project memory runtime block: %s", out[1].Text)
	}
}
