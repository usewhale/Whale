package core

import "testing"

func TestDisplayToolName(t *testing.T) {
	cases := map[string]string{
		"shell_run":    "Bash",
		"read_file":    "Read",
		"grep":         "Grep",
		"search_files": "Glob",
		"edit":         "Edit",
		"multi_edit":   "MultiEdit",
		"write":        "Write",
		"web_search":   "WebSearch",
		"frobnicate":   "frobnicate", // unmapped passes through
		"":             "",
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
		// other conventional names, any case
		"Grep":      "grep",
		"GREP":      "grep",
		"Glob":      "search_files",
		"Edit":      "edit",
		"MultiEdit": "multi_edit",
		"multiedit": "multi_edit",
		"Write":     "write",
		"LS":        "list_dir",
		"WebSearch": "web_search",
		"WebFetch":  "web_fetch",
		// invented CLI aliases
		"head": "read_file",
		"cat":  "read_file",
		"sh":   "shell_run",
		"rg":   "grep",
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
		"prefer search_files over web_search": "prefer Glob over WebSearch",
		"set the shell_run cwd parameter":     "set the Bash cwd parameter",
		"the read_file content":               "the Read content",
		// snake_case is matched with word boundaries
		"read_file/list_dir": "Read/LS",
		// single English-word names are NOT rewritten in prose: they collide
		// with ordinary verbs and capability literals.
		"edit the file then write output": "edit the file then write output",
		"requires workspace.write access": "requires workspace.write access",
		"use grep to search":              "use grep to search",
		"":                                "",
		"no tool names here":              "no tool names here",
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
