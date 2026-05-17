package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/core"
	"github.com/usewhale/whale/internal/memory"
)

// buildTurnIterCost simulates what a single tool-loop iteration costs in
// terms of system-prompt rebuilding: buildImmutableSystemBlocks (which calls
// ReadProjectMemory + skills.Discover) followed by rt.Prefix.Refresh (which
// sha256s the joined system blocks). One outer iter = one tool-loop iter.
func benchTurnIter(b *testing.B, projectMemoryBytes int) {
	dir := b.TempDir()
	if projectMemoryBytes > 0 {
		body := []byte(strings.Repeat("a", projectMemoryBytes))
		if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), body, 0o600); err != nil {
			b.Fatalf("write memory: %v", err)
		}
	}
	a := NewAgentWithRegistry(nil, nil, core.NewToolRegistry(nil),
		WithProjectMemory(true, 64*1024, []string{"AGENTS.md"}, dir))
	rt := memory.HydrateRuntime(memory.NewImmutablePrefix(a.buildImmutableSystemBlocks()), nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rt.Prefix.Refresh(a.buildImmutableSystemBlocks())
	}
}

func BenchmarkBuildTurnIter_NoMemory(b *testing.B) {
	benchTurnIter(b, 0)
}

func BenchmarkBuildTurnIter_SmallMemory(b *testing.B) {
	benchTurnIter(b, 2*1024)
}

func BenchmarkBuildTurnIter_LargeMemory(b *testing.B) {
	benchTurnIter(b, 32*1024)
}

// BenchmarkReadProjectMemory_Only isolates the os.ReadFile cost when the file
// path is hot in the page cache, to show what fraction of buildTurnIter is
// project memory vs. the other work (skills.Discover, tool-spec render).
func BenchmarkReadProjectMemory_Only(b *testing.B) {
	dir := b.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"),
		[]byte(strings.Repeat("a", 4*1024)), 0o600); err != nil {
		b.Fatalf("write memory: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = memory.ReadProjectMemory(dir, []string{"AGENTS.md"}, 64*1024)
	}
}
