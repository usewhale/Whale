package tools

// Reproductions from session 019ead56-4099-741e-ae27-f27b40a3d6ec, a Windows
// user building a BepInEx mod for the game Elin. The user reported "lots of
// tool call errors"; analysis of the transcript found three distinct causes:
//
//  1. Every tool result containing & < > reached the model with those bytes
//     rewritten to literal \u0026 \u003c \u003e (json.Marshal HTML escaping
//     in the envelope path). The model copied the corrupted text from a
//     read_file result into an edit search (m-333, m-337) and got
//     search_not_found twice.
//  2. A GUI binary (ILSpy.exe --help) never exits, timed out, and the
//     diagnosis confidently told the model to "rerun with a longer timeout"
//     (m-13) — which can only time out again.
//  3. Windows search commands (findstr/where/dir) reporting "no matches" via
//     exit code 1 were surfaced as command failures (covered by TUI repro
//     tests in internal/tui).

import (
	"context"
	"strings"
	"testing"
)

// Verbatim content shape of the file the session's model read and then
// failed to edit (F:\ai项目\whale项目\elin\better_slime\Patch_GeneIntercept.cs).
const sessionPatchGeneIntercept = `using System;

public static class Patch_GeneIntercept
{
    public static void Proc(Chara c)
    {
        if (Act.CC.HasCondition<ConAnorexia>())
        {
            // 在原版 FoodEffect.Proc 被跳过之前，手动给捕食技能加经验
            if (c.HasElement(6608))
            {
                Element elem = c.elements.GetElement(6608);
                if (elem != null && elem.ValueWithoutLink > 0)
                {
                    c.elements.ModExp(6608, 10);
                }
            }
        }
    }
}
`

// TestReadFileResultKeepsOperatorsVerbatim pins the desired behavior at the
// tool boundary: what the model sees from read_file must equal the file
// bytes. FAILS until the model-facing envelope stops HTML-escaping payloads.
func TestReadFileResultKeepsOperatorsVerbatim(t *testing.T) {
	dir := t.TempDir()
	ts := newToolsetWithFile(t, dir, "Patch_GeneIntercept.cs", sessionPatchGeneIntercept)

	res, err := ts.readFile(context.Background(), tc("read_file", map[string]any{
		"file_path": "Patch_GeneIntercept.cs",
	}))
	if err != nil || res.IsError {
		t.Fatalf("read_file failed: err=%v res=%+v", err, res)
	}
	for _, esc := range []string{`\u0026`, `\u003c`, `\u003e`} {
		if strings.Contains(res.Content, esc) {
			t.Errorf("read_file result shows the model literal %s instead of the file's bytes", esc)
		}
	}
	if !strings.Contains(res.Content, "elem != null && elem.ValueWithoutLink > 0") {
		t.Errorf("expected C# operators verbatim in read_file result, got:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "HasCondition<ConAnorexia>()") {
		t.Errorf("expected generic type syntax verbatim in read_file result, got:\n%s", res.Content)
	}
}

// TestEditSearchCopiedFromEscapedReadResultCannotMatch documents the failure
// mechanism (not desired behavior): the session's model copied the escaped
// text it was shown into an edit search, which can never match the file.
// This test passes before and after the fix; once read_file output is
// verbatim, this input simply stops being produced by the model.
func TestEditSearchCopiedFromEscapedReadResultCannotMatch(t *testing.T) {
	dir := t.TempDir()
	ts := newToolsetWithFile(t, dir, "Patch_GeneIntercept.cs", sessionPatchGeneIntercept)

	// Search text as the model wrote it in m-333: copied from the escaped
	// read_file rendering, so it contains literal \u0026\u0026 and \u003e.
	search := `                Element elem = c.elements.GetElement(6608);
                if (elem != null \u0026\u0026 elem.ValueWithoutLink \u003e 0)`
	res, err := ts.editFile(context.Background(), tc("edit", map[string]any{
		"file_path": "Patch_GeneIntercept.cs",
		"search":    search,
		"replace":   "// replaced\n",
	}))
	if err != nil {
		t.Fatalf("editFile returned transport error: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "search_not_found") {
		t.Fatalf("expected search_not_found for escaped search text, got: %s", res.Content)
	}
}

// TestShellTimeoutWithNoOutputDoesNotClaimTimeoutTooShort pins the desired
// diagnosis for m-13: ILSpy.exe is a GUI program, produced zero output, and
// timed out. Telling the model the timeout "was too short" and to rerun with
// a longer one is provably wrong advice here — the process never exits.
// FAILS until the diagnosis distinguishes silent never-exiting processes.
func TestShellTimeoutWithNoOutputDoesNotClaimTimeoutTooShort(t *testing.T) {
	snap := shellTaskSnapshot{
		Command: `"C:\Users\jiran\AppData\Local\Programs\ILSpy\ILSpy.exe" --help 2>&1 || "C:\Users\jiran\AppData\Local\Programs\ILSpy\ILSpy.exe" /? 2>&1`,
		Status:  "timeout",
		Stdout:  "",
		Stderr:  "",
	}
	diag := shellTimeoutDiagnosis(snap, shellTimeoutContext{
		RequestedTimeoutMS: 10000,
		EffectiveTimeoutMS: 10000,
		DefaultWaitMS:      15000,
	})
	if diag.SuggestedNextAction == "rerun_with_longer_timeout" {
		t.Errorf("timeout with zero output should not unconditionally advise a longer rerun, got reason=%q hint=%q", diag.Reason, diag.Hint)
	}
}
