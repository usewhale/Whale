package shellrisk

import "testing"

func TestArityFamily(t *testing.T) {
	cases := []struct {
		command string
		want    string
		ok      bool
	}{
		// Dictionary arities: flags and trailing args drop out.
		{"git commit -m \"msg\"", "git commit", true},
		{"git log --oneline -20", "git log", true},
		{"npm install lodash", "npm install", true},
		{"npm run dev --watch", "npm run dev", true},
		{"go test -exec ./wrapper ./pkg", "go test", true},
		{"docker compose up -d", "docker compose up", true},
		// Default: command word plus a leading subcommand-like token.
		{"./bin/whale exec --help", "./bin/whale exec", true},
		{"mytool sub-cmd --flag value", "mytool sub-cmd", true},
		// Default: no subcommand-like second token -> command word only.
		{"curl https://example.com", "curl", true},
		{"mytool --version", "mytool", true},
		{"mytool ./relative/path", "mytool", true},
		{"./script.sh", "./script.sh", true},
		// Trailing safe stderr redirect is stripped before reducing.
		{"./bin/whale exec --help 2>&1", "./bin/whale exec", true},
		// Compound commands are not a single family.
		{"npm install && curl https://x", "", false},
		{"cat a | grep b", "", false},
		{"echo $(whoami)", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := ArityFamily(tc.command)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ArityFamily(%q) = (%q, %v), want (%q, %v)", tc.command, got, ok, tc.want, tc.ok)
		}
	}
}
