package core

import "testing"

func TestDisplayToolName(t *testing.T) {
	cases := map[string]string{
		"shell_run":  "Bash",
		"read_file":  "Read",
		"grep":       "grep", // unmapped passes through
		"web_search": "web_search",
		"":           "",
	}
	for in, want := range cases {
		if got := DisplayToolName(in); got != want {
			t.Errorf("DisplayToolName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCanonicalToolName(t *testing.T) {
	cases := map[string]string{
		// conventional display names (any case)
		"Bash": "shell_run",
		"bash": "shell_run",
		"BASH": "shell_run",
		"Read": "read_file",
		"read": "read_file",
		// invented CLI aliases
		"head": "read_file",
		"cat":  "read_file",
		"sh":   "shell_run",
		// whitespace tolerated
		" Bash ": "shell_run",
		// unknown passes through (still surfaces as tool-not-found later)
		"frobnicate": "frobnicate",
	}
	for in, want := range cases {
		if got := CanonicalToolName(in); got != want {
			t.Errorf("CanonicalToolName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestApplyDisplayToolNames(t *testing.T) {
	cases := map[string]string{
		"use read_file first, then shell_run": "use Read first, then Bash",
		"prefer grep and search_files":        "prefer grep and search_files", // unmapped untouched
		"set the shell_run cwd parameter":     "set the Bash cwd parameter",
		"the read_file content":               "the Read content",
		"":                                    "",
		"no tool names here":                  "no tool names here",
	}
	for in, want := range cases {
		if got := ApplyDisplayToolNames(in); got != want {
			t.Errorf("ApplyDisplayToolNames(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestToolNameRoundTrip guards the core invariant: a renamed tool survives a
// full round trip — internal name -> display (outbound) -> canonical (inbound)
// -> internal — unchanged.
func TestToolNameRoundTrip(t *testing.T) {
	for internal := range displayToolNames {
		display := DisplayToolName(internal)
		if back := CanonicalToolName(display); back != internal {
			t.Errorf("round trip %q -> %q -> %q, want %q", internal, display, back, internal)
		}
	}
}
