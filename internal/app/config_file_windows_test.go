//go:build windows

package app

import "testing"

func TestExpandUserPathExpandsWindowsPercentEnv(t *testing.T) {
	t.Setenv("USERPROFILE", `C:\Users\tester`)
	t.Setenv("APPDATA", `C:\Users\tester\AppData\Roaming`)

	tests := map[string]string{
		`%USERPROFILE%\whale\mcp.json`: `C:\Users\tester\whale\mcp.json`,
		`%APPDATA%\whale\mcp.json`:     `C:\Users\tester\AppData\Roaming\whale\mcp.json`,
		`%MISSING_VAR%\whale\mcp.json`: `%MISSING_VAR%\whale\mcp.json`,
	}
	for in, want := range tests {
		if got := expandUserPath(in); got != want {
			t.Fatalf("expandUserPath(%q) = %q, want %q", in, got, want)
		}
	}
}
