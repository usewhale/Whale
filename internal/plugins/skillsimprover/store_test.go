package skillsimprover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/usewhale/whale/internal/agent"
	"github.com/usewhale/whale/internal/core"
)

func TestStoreEvidenceAndProposalApply(t *testing.T) {
	workspace := t.TempDir()
	skillPath := writeTestSkill(t, workspace, "demo", "old body")
	store := NewStore(filepath.Join(t.TempDir(), "skills-improver"), workspace)

	ev, err := store.AppendEvidence(Evidence{Kind: "user-feedback", SessionID: "s1", Skill: "demo", Prompt: "下次 $demo 要更具体"})
	if err != nil {
		t.Fatalf("append evidence: %v", err)
	}
	if ev.ID == "" {
		t.Fatal("expected evidence id")
	}
	evs, err := store.ListEvidence("demo", 10)
	if err != nil || len(evs) != 1 {
		t.Fatalf("list evidence len=%d err=%v", len(evs), err)
	}

	orig, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	proposed := skillMarkdown("demo", "new body")
	p, err := store.SaveProposal(Proposal{
		Skill:           "demo",
		SkillFilePath:   skillPath,
		OriginalSHA256:  sha256Hex(orig),
		Summary:         "make demo more specific",
		ProposedSkillMD: proposed,
		EvidenceIDs:     []string{ev.ID},
	})
	if err != nil {
		t.Fatalf("save proposal: %v", err)
	}
	applied, err := store.ApplyProposal(p.ID)
	if err != nil {
		t.Fatalf("apply proposal: %v", err)
	}
	if applied.AppliedAt.IsZero() {
		t.Fatal("expected applied timestamp")
	}
	got, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "new body") {
		t.Fatalf("proposal not applied:\n%s", string(got))
	}
}

func TestApplyProposalRejectsChangedSkill(t *testing.T) {
	workspace := t.TempDir()
	skillPath := writeTestSkill(t, workspace, "demo", "old body")
	store := NewStore(filepath.Join(t.TempDir(), "skills-improver"), workspace)
	orig, _ := os.ReadFile(skillPath)
	p, err := store.SaveProposal(Proposal{
		Skill:           "demo",
		SkillFilePath:   skillPath,
		OriginalSHA256:  sha256Hex(orig),
		Summary:         "change",
		ProposedSkillMD: skillMarkdown("demo", "new body"),
	})
	if err != nil {
		t.Fatalf("save proposal: %v", err)
	}
	if err := os.WriteFile(skillPath, []byte(skillMarkdown("demo", "external change")), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyProposal(p.ID); err == nil || !strings.Contains(err.Error(), "sha mismatch") {
		t.Fatalf("expected sha mismatch, got %v", err)
	}
}

func TestSaveProposalIgnoresCallerSuppliedID(t *testing.T) {
	workspace := t.TempDir()
	skillPath := writeTestSkill(t, workspace, "demo", "old body")
	dataDir := filepath.Join(t.TempDir(), "skills-improver")
	store := NewStore(dataDir, workspace)
	orig, _ := os.ReadFile(skillPath)
	p, err := store.SaveProposal(Proposal{
		ID:              "../escape",
		Skill:           "demo",
		SkillFilePath:   skillPath,
		OriginalSHA256:  sha256Hex(orig),
		Summary:         "change",
		ProposedSkillMD: skillMarkdown("demo", "new body"),
	})
	if err != nil {
		t.Fatalf("save proposal: %v", err)
	}
	if strings.Contains(p.ID, "..") || strings.ContainsAny(p.ID, `/\`) || p.ID == "../escape" {
		t.Fatalf("caller supplied id was not regenerated: %q", p.ID)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "escape.json")); !os.IsNotExist(err) {
		t.Fatalf("proposal escaped proposals dir, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "proposals", p.ID+".json")); err != nil {
		t.Fatalf("expected sanitized proposal file: %v", err)
	}
}

func TestSaveProposalPreservesUppercaseSkillName(t *testing.T) {
	workspace := t.TempDir()
	skillPath := writeTestSkill(t, workspace, "MySkill", "old body")
	store := NewStore(filepath.Join(t.TempDir(), "skills-improver"), workspace)
	orig, _ := os.ReadFile(skillPath)
	p, err := store.SaveProposal(Proposal{
		Skill:           "MySkill",
		SkillFilePath:   skillPath,
		OriginalSHA256:  sha256Hex(orig),
		Summary:         "change",
		ProposedSkillMD: skillMarkdown("MySkill", "new body"),
	})
	if err != nil {
		t.Fatalf("save uppercase proposal: %v", err)
	}
	if p.Skill != "MySkill" {
		t.Fatalf("skill name lowercased: %q", p.Skill)
	}
	if _, err := store.ApplyProposal(p.ID); err != nil {
		t.Fatalf("apply uppercase proposal: %v", err)
	}
}

func TestSaveProposalRequiresOriginalSHA(t *testing.T) {
	workspace := t.TempDir()
	skillPath := writeTestSkill(t, workspace, "demo", "old body")
	store := NewStore(filepath.Join(t.TempDir(), "skills-improver"), workspace)
	_, err := store.SaveProposal(Proposal{
		Skill:           "demo",
		SkillFilePath:   skillPath,
		Summary:         "change",
		ProposedSkillMD: skillMarkdown("demo", "new body"),
	})
	if err == nil || !strings.Contains(err.Error(), "original_sha256 is required") {
		t.Fatalf("expected missing original_sha256 error, got %v", err)
	}
}

func TestApplyProposalAcceptsSymlinkEquivalentPath(t *testing.T) {
	workspace := t.TempDir()
	skillPath := writeTestSkill(t, workspace, "demo", "old body")
	linkRoot := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(workspace, linkRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	linkSkillPath := filepath.Join(linkRoot, ".whale", "skills", "demo", "SKILL.md")
	store := NewStore(filepath.Join(t.TempDir(), "skills-improver"), workspace)
	orig, _ := os.ReadFile(skillPath)
	p, err := store.SaveProposal(Proposal{
		Skill:           "demo",
		SkillFilePath:   linkSkillPath,
		OriginalSHA256:  sha256Hex(orig),
		Summary:         "change",
		ProposedSkillMD: skillMarkdown("demo", "new body"),
	})
	if err != nil {
		t.Fatalf("save proposal with symlink-equivalent path: %v", err)
	}
	if _, err := store.ApplyProposal(p.ID); err != nil {
		t.Fatalf("apply proposal with symlink-equivalent path: %v", err)
	}
}

func TestHooksCaptureFeedbackAndFailuresOnly(t *testing.T) {
	workspace := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "skills-improver")
	handlers := Hooks(Context{DataDir: dataDir, WorkspaceRoot: workspace})
	runner := agent.NewHookRunner(nil, workspace)
	runner.AddHandlers(handlers...)

	pass := runner.RunHook(t.Context(), agent.NewUserPromptSubmitPayload("s1", workspace, "普通请求"))
	if len(pass.Outcomes) != 1 || pass.Outcomes[0].Decision != agent.HookDecisionPass {
		t.Fatalf("unexpected pass hook report: %+v", pass)
	}
	store := NewStore(dataDir, workspace)
	evs, err := store.ListEvidence("", 10)
	if err != nil || len(evs) != 0 {
		t.Fatalf("ordinary prompt should not record evidence: len=%d err=%v", len(evs), err)
	}

	_ = runner.RunHook(t.Context(), agent.NewUserPromptSubmitPayload("s1", workspace, "what should I do next?"))
	evs, err = store.ListEvidence("", 10)
	if err != nil || len(evs) != 0 {
		t.Fatalf("broad feedback keyword without skill should not record evidence: len=%d err=%v", len(evs), err)
	}

	_ = runner.RunHook(t.Context(), agent.NewUserPromptSubmitPayload("s1", workspace, "$demo run the tests"))
	evs, err = store.ListEvidence("", 10)
	if err != nil || len(evs) != 0 {
		t.Fatalf("ordinary skill invocation should not record evidence: len=%d err=%v", len(evs), err)
	}

	_ = runner.RunHook(t.Context(), agent.NewUserPromptSubmitPayload("s1", workspace, "以后 $demo 要检查测试"))
	evs, err = store.ListEvidence("demo", 10)
	if err != nil || len(evs) != 1 || evs[0].Kind != "user-feedback" {
		t.Fatalf("expected feedback evidence, got %+v err=%v", evs, err)
	}

	okRes := core.NewToolResultSuccess(core.ToolCall{ID: "c1", Name: "shell_run"}, map[string]any{"status": "ok"}, nil)
	_ = runner.RunHook(t.Context(), agent.NewPostToolUsePayload("s1", core.ToolCall{Name: "shell_run", Input: `{"command":"go test"}`}, nil, okRes))
	evs, _ = store.ListEvidence("", 10)
	if len(evs) != 1 {
		t.Fatalf("successful tool should not add evidence: %+v", evs)
	}

	failRes := core.NewToolResultError(core.ToolCall{ID: "c2", Name: "shell_run"}, "exec_failed", "tests failed", nil)
	_ = runner.RunHook(t.Context(), agent.NewPostToolUsePayload("s1", core.ToolCall{Name: "shell_run", Input: `{"command":"go test"}`}, map[string]any{"command": "go test"}, failRes))
	evs, _ = store.ListEvidence("", 10)
	if len(evs) != 2 || evs[0].Kind == "" {
		t.Fatalf("expected tool failure evidence: %+v", evs)
	}
}

func TestStopHookSummarizesOnlyUnsummarizedEvidence(t *testing.T) {
	workspace := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "skills-improver")
	handlers := Hooks(Context{DataDir: dataDir, WorkspaceRoot: workspace})
	runner := agent.NewHookRunner(nil, workspace)
	runner.AddHandlers(handlers...)
	store := NewStore(dataDir, workspace)

	_ = runner.RunHook(t.Context(), agent.NewUserPromptSubmitPayload("s1", workspace, "以后 $demo 要检查测试"))
	_ = runner.RunHook(t.Context(), agent.NewStopPayload("s1", workspace, "first answer", 1))
	evs, err := store.ListEvidence("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if countEvidenceKind(evs, "turn-summary") != 1 {
		t.Fatalf("expected one turn summary after evidence, got %+v", evs)
	}
	summaries, err := store.ListEvidence("demo", 10)
	if err != nil {
		t.Fatal(err)
	}
	var linkedSummary Evidence
	for _, ev := range summaries {
		if ev.Kind == "turn-summary" {
			linkedSummary = ev
			break
		}
	}
	if linkedSummary.Skill != "demo" || !strings.Contains(linkedSummary.Prompt, "$demo") || linkedSummary.Metadata["source_evidence_id"] == "" {
		t.Fatalf("turn summary should preserve skill linkage, got %+v", linkedSummary)
	}

	_ = runner.RunHook(t.Context(), agent.NewStopPayload("s1", workspace, "unrelated later answer", 2))
	evs, err = store.ListEvidence("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if countEvidenceKind(evs, "turn-summary") != 1 {
		t.Fatalf("unrelated later stop should not append summary, got %+v", evs)
	}

	_ = runner.RunHook(t.Context(), agent.NewUserPromptSubmitPayload("s1", workspace, "下次 $demo 要更短"))
	_ = runner.RunHook(t.Context(), agent.NewStopPayload("s1", workspace, "second answer", 3))
	evs, err = store.ListEvidence("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if countEvidenceKind(evs, "turn-summary") != 2 {
		t.Fatalf("new evidence should allow a new summary, got %+v", evs)
	}
}

func writeTestSkill(t *testing.T, workspace, name, body string) string {
	t.Helper()
	dir := filepath.Join(workspace, ".whale", "skills", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(skillMarkdown(name, body)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func skillMarkdown(name, body string) string {
	return "---\nname: " + name + "\ndescription: Demo skill\n---\n\n# Demo\n\n" + body + "\n"
}

func countEvidenceKind(evs []Evidence, kind string) int {
	count := 0
	for _, ev := range evs {
		if ev.Kind == kind {
			count++
		}
	}
	return count
}
