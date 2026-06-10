package core

import "testing"

// Moved from internal/tui when the exit-code semantics became the shared
// source of truth for the tool layer and the TUI; extended with the Windows
// commands from session 019ead56.
func TestShellExitMeansNoMatchesUsesLastCommandOutsideQuotes(t *testing.T) {
	cases := []struct {
		command string
		stdout  string
		want    bool
	}{
		{`grep -rn "^func firstLine\b" internal/ --include='*.go' | grep -v "core/"`, "", true},
		{`grep -E "foo|bar" internal/file.go`, "", true},
		{`cd internal && rg "missing"`, "", true},
		{`git grep "missing" -- '*.go'`, "", true},
		{`grep missing internal/file.go && false`, "", false},
		{`grep missing internal/file.go; false`, "", false},
		{`printf setup && false`, "", false},
		// Windows search commands (session 019ead56).
		{`findstr /i ActSlime element.csv 2>&1`, "", true},
		{`where ilspycmd 2>&1`, "INFO: Could not find files for the given pattern(s).", true},
		{`dir *.dll /b 2>&1`, "File Not Found", true},
		{`dotnet tool list -g 2>&1`, "", false},
		// cmd.exe single & chains: the final command owns the exit code,
		// while the & inside 2>&1 is a redirect, not a separator.
		{`where missing 2>&1 & go test ./...`, "", false},
		{`go build 2>&1 & findstr /i err build.log 2>&1`, "", true},
		{`dir a /b 2>&1 & dir b /b 2>&1`, "File Not Found", true},
	}
	for _, tc := range cases {
		if got := ShellExitMeansNoMatches(tc.command, 1, tc.stdout, ""); got != tc.want {
			t.Fatalf("ShellExitMeansNoMatches(%q, 1) = %v, want %v", tc.command, got, tc.want)
		}
	}
}

func TestShellExitMeansNoMatchesBoundaries(t *testing.T) {
	if ShellExitMeansNoMatches(`grep pattern missing.txt`, 2, "", "") {
		t.Fatal("exit 2 is a real error for grep-family commands")
	}
	if ShellExitMeansNoMatches(`grep pattern missing.txt`, 1, "", "grep: missing.txt: No such file or directory") {
		t.Fatal("grep stderr output means a real complaint, not a no-match")
	}

	// where reports "not found" via exit 1 with an INFO line on stderr; the
	// exit code is the documented signal, with or without 2>&1.
	if !ShellExitMeansNoMatches(`where ilspycmd`, 1, "", "INFO: Could not find files for the given pattern(s).") {
		t.Fatal("where exit 1 is a documented no-match even with the INFO line on stderr")
	}
	if ShellExitMeansNoMatches(`where /badswitch foo`, 2, "", "ERROR: Invalid pattern is specified.") {
		t.Fatal("where exit 2 is a real error")
	}

	// findstr can exit 1 for real errors too; they carry a FINDSTR: prefix
	// (on stdout when streams are merged).
	if ShellExitMeansNoMatches(`findstr pattern missing.csv 2>&1`, 1, "FINDSTR: Cannot open missing.csv", "") {
		t.Fatal("findstr cannot-open is a real error despite exit 1")
	}
	if ShellExitMeansNoMatches(`findstr pattern missing.csv`, 1, "", "FINDSTR: Cannot open missing.csv") {
		t.Fatal("findstr stderr complaint must stay a failure")
	}

	// dir exit 1 is ambiguous, so no-match needs the File Not Found marker.
	if !ShellExitMeansNoMatches(`dir *.dll /b`, 1, "", "File Not Found") {
		t.Fatal("dir reports no matching files via stderr File Not Found")
	}
	if ShellExitMeansNoMatches(`dir /badswitch 2>&1`, 1, `Invalid switch - "badswitch".`, "") {
		t.Fatal("dir real error merged into stdout must stay a failure")
	}
	if ShellExitMeansNoMatches(`dir *.dll /b 2>&1`, 1, "", "") {
		t.Fatal("dir exit 1 without the File Not Found marker is not provably a no-match")
	}
}
