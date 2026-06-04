package memory

import (
	"testing"

	"github.com/usewhale/whale/internal/core"
)

func TestImmutablePrefixFingerprintChangesOnRefresh(t *testing.T) {
	p := NewImmutablePrefix([]string{"a"})
	first := p.Fingerprint()
	p.Refresh([]string{"a", "b"})
	if first == p.Fingerprint() {
		t.Fatal("fingerprint should change after refresh")
	}
}

func TestImmutablePrefixCopiesBlocksAndVerifiesFingerprint(t *testing.T) {
	blocks := []string{"a"}
	p := NewImmutablePrefix(blocks)
	first := p.Fingerprint()
	blocks[0] = "changed"
	if p.Fingerprint() != first {
		t.Fatal("prefix fingerprint changed after mutating source slice")
	}
	copied := p.SystemBlocks()
	copied[0] = "changed again"
	if p.Fingerprint() != first {
		t.Fatal("prefix fingerprint changed after mutating returned blocks")
	}
	if fresh, ok := p.VerifyFingerprint(); !ok || fresh != first {
		t.Fatalf("verify fingerprint = %q/%v, want %q/true", fresh, ok, first)
	}
}

func TestAppendOnlyLogRewriteWithReason(t *testing.T) {
	log := NewAppendOnlyLog()
	log.Append(core.Message{Role: core.RoleUser, Text: "u1"})
	log.Append(core.Message{Role: core.RoleAssistant, Text: "a1"})
	if ok := log.RewriteWithReason(RewriteReasonCompact, []core.Message{{Role: core.RoleSystem, Text: "s1"}}); !ok {
		t.Fatal("rewrite should succeed for compact")
	}
	got := log.Entries()
	if len(got) != 1 || got[0].Role != core.RoleSystem {
		t.Fatalf("unexpected compacted log: %+v", got)
	}
}

func TestAppendOnlyLogRejectsUnknownRewriteReason(t *testing.T) {
	log := NewAppendOnlyLog()
	log.Append(core.Message{Role: core.RoleUser, Text: "u1"})
	if ok := log.RewriteWithReason(RewriteReason("unknown"), []core.Message{}); ok {
		t.Fatal("rewrite should fail for unknown reason")
	}
	if log.Len() != 1 {
		t.Fatalf("log mutated on rejected rewrite: %d", log.Len())
	}
}

func TestVolatileScratchResets(t *testing.T) {
	s := NewVolatileScratch()
	s.Reasoning = "r"
	s.UpdateToolArgs(0, "bash", 12, 1)
	s.Warnings = append(s.Warnings, "w")
	s.ResetTurn()
	if s.Reasoning != "" || len(s.ToolArgs) != 0 || len(s.Warnings) != 0 {
		t.Fatalf("turn reset failed: %+v", s)
	}
	s.Reasoning = "x"
	s.ResetSession()
	if s.Reasoning != "" {
		t.Fatal("session reset failed")
	}
}

func TestRuntimeBuildProviderHistory(t *testing.T) {
	rt := HydrateRuntime(NewImmutablePrefix([]string{"sys"}), []core.Message{{Role: core.RoleUser, Text: "hi"}})
	got := rt.BuildProviderHistory()
	if len(got) != 2 || got[0].Role != core.RoleSystem || got[1].Role != core.RoleUser {
		t.Fatalf("unexpected history shape: %+v", got)
	}
}

func TestRuntimeBuildProviderHistoryAppendsRuntimeSystemAfterPrefix(t *testing.T) {
	rt := HydrateRuntime(NewImmutablePrefix([]string{"immutable"}), []core.Message{{Role: core.RoleUser, Text: "hi"}})
	rt.SetRuntimeBlocks([]string{"runtime-a", "runtime-b"})

	got := rt.BuildProviderHistory()
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(got), got)
	}
	if got[0].Role != core.RoleSystem || got[0].Text != "immutable" {
		t.Fatalf("unexpected immutable prefix message: %+v", got[0])
	}
	if got[1].Role != core.RoleSystem || got[1].Text != "runtime-a\n\nruntime-b" {
		t.Fatalf("unexpected runtime suffix message: %+v", got[1])
	}
	if got[2].Role != core.RoleUser || got[2].Text != "hi" {
		t.Fatalf("unexpected log message: %+v", got[2])
	}
}
