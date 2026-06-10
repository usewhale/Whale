package tui

// Reproductions from session 019ead56-4099-741e-ae27-f27b40a3d6ec (Windows
// user). Roughly a dozen of the session's 31 shell_run "errors" were Windows
// search commands reporting "no result" via exit code 1: findstr and where
// with no match, dir with no files matching the pattern. The grep/rg-only
// whitelist in shellCommandUsesSearchExitOne missed all of them, so the user
// saw a red "Command failed (exit 1)" wall and reported "lots of tool call
// errors". These tests pin the desired neutral rendering and FAIL until the
// whitelist understands the Windows equivalents.
//
// Session evidence: the commands below mirror m-49 (findstr), m-15 (where),
// and m-207 (dir); all ran with 2>&1 so stderr was empty.

import (
	"strings"
	"testing"
)

func assertNeutralNoMatch(t *testing.T, raw string) {
	t.Helper()
	role, got := summarizeToolResultForChat("shell_run", raw)
	if role != "result_neutral" {
		t.Errorf("expected result_neutral role, got %q (summary: %q)", role, got)
	}
	if !strings.HasPrefix(got, "No matches") {
		t.Errorf("expected summary to lead with No matches, got %q", got)
	}
	if title := completedToolTitle("shell_run", raw, ""); strings.HasPrefix(title, "Command failed") {
		t.Errorf("no-match result should not render as command failure title: %q", title)
	}
}

func TestSummarizeToolResultForChat_FindstrNoMatchesIsNeutral(t *testing.T) {
	raw := `{"success":false,"code":"exec_failed","message":"command failed","data":{"status":"error","summary":"command failed","metrics":{"exit_code":1,"duration_ms":36},"payload":{"command":"findstr /i ActSlime element.csv 2>&1","stderr":"","stdout":""}}}`
	assertNeutralNoMatch(t, raw)
}

func TestSummarizeToolResultForChat_WhereNoMatchesIsNeutral(t *testing.T) {
	// where prints an INFO line to stderr, but the session's commands all
	// appended 2>&1, so it arrives on stdout and should be kept as output.
	raw := `{"success":false,"code":"exec_failed","message":"command failed","data":{"status":"error","summary":"command failed","metrics":{"exit_code":1,"duration_ms":40},"payload":{"command":"where ilspycmd 2>&1","stderr":"","stdout":"INFO: Could not find files for the given pattern(s)."}}}`
	assertNeutralNoMatch(t, raw)
}

func TestSummarizeToolResultForChat_DirNoFilesMatchingIsNeutral(t *testing.T) {
	// dir reports a pattern with no matching files via exit 1 and a fixed
	// "File Not Found" message; with 2>&1 it lands on stdout.
	raw := `{"success":false,"code":"exec_failed","message":"command failed","data":{"status":"error","summary":"command failed","metrics":{"exit_code":1,"duration_ms":38},"payload":{"command":"dir *.dll /b 2>&1","stderr":"","stdout":"File Not Found"}}}`
	assertNeutralNoMatch(t, raw)
}

func TestSummarizeToolResultForChat_FindstrRealErrorStaysFailure(t *testing.T) {
	// Guard for the boundary: findstr exit 2 (bad arguments) is a real
	// failure and must keep failing loudly even once exit 1 is neutral.
	raw := `{"success":false,"code":"exec_failed","message":"command failed","data":{"status":"error","summary":"command failed","metrics":{"exit_code":2,"duration_ms":12},"payload":{"command":"findstr /Q bogus 2>&1","stderr":"FINDSTR: Bad command line","stdout":""}}}`
	role, _ := summarizeToolResultForChat("shell_run", raw)
	if role != "result_failed" {
		t.Errorf("findstr exit 2 must stay a failure, got role %q", role)
	}
}
