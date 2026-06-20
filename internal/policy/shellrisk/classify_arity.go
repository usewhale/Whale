package shellrisk

import "strings"

// ArityFamily returns the "human-understandable command" prefix for a single
// simple shell command, used to group session approvals so that approving one
// invocation covers the whole command family (e.g. approving `git commit -m x`
// also covers later `git commit ...`). It mirrors opencode's BashArity model.
//
// It returns ("", false) for anything that is not a single simple command
// (pipelines, &&/||/; lists, subshells, commands with substitutions or globs),
// so compound commands keep their exact-command approval key.
func ArityFamily(command string) (string, bool) {
	for {
		base, ok := stripTrailingSafeStderrRedirect(command)
		if !ok {
			break
		}
		command = base
	}
	argv, ok := parseSimpleShellCommand(command)
	if !ok || len(argv) == 0 {
		return "", false
	}
	prefix := arityPrefixTokens(argv)
	if len(prefix) == 0 {
		return "", false
	}
	return strings.Join(prefix, " "), true
}

// arityPrefixTokens reduces argv to the tokens that define the command,
// following opencode's rule: longest matching dictionary prefix wins, flags
// never count. When the command is not in the dictionary it defaults to the
// command word plus one leading subcommand-like token when present (so
// `./bin/whale exec --help` groups as `./bin/whale exec`), otherwise the
// command word alone.
func arityPrefixTokens(argv []string) []string {
	for length := len(argv); length > 0; length-- {
		key := strings.Join(argv[:length], " ")
		arity, ok := shellArity[key]
		if !ok {
			continue
		}
		if arity > len(argv) {
			arity = len(argv)
		}
		if arity < 1 {
			arity = 1
		}
		return argv[:arity]
	}
	if len(argv) >= 2 && isSubcommandToken(argv[1]) {
		return argv[:2]
	}
	return argv[:1]
}

// isSubcommandToken reports whether tok looks like a subcommand (e.g. "exec",
// "create-cluster") rather than a flag, path, URL, or assignment. Used only for
// the default arity so that paths/URLs/flags do not widen the grant.
func isSubcommandToken(tok string) bool {
	if tok == "" || strings.HasPrefix(tok, "-") {
		return false
	}
	for _, r := range tok {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			continue
		case r == '_' || r == '-':
			continue
		default:
			return false
		}
	}
	return true
}

// shellArity maps a command prefix to the number of tokens that define the
// command. Ported from opencode's permission/arity dictionary. Flags never
// count; only subcommands do. Longest matching prefix wins.
var shellArity = map[string]int{
	"cat":     1,
	"cd":      1,
	"chmod":   1,
	"chown":   1,
	"cp":      1,
	"echo":    1,
	"env":     1,
	"export":  1,
	"grep":    1,
	"kill":    1,
	"killall": 1,
	"ln":      1,
	"ls":      1,
	"mkdir":   1,
	"mv":      1,
	"ps":      1,
	"pwd":     1,
	"rm":      1,
	"rmdir":   1,
	"sleep":   1,
	"source":  1,
	"tail":    1,
	"touch":   1,
	"unset":   1,
	"which":   1,

	"aws":                 3,
	"az":                  3,
	"bazel":               2,
	"brew":                2,
	"bun":                 2,
	"bun run":             3,
	"bun x":               3,
	"cargo":               2,
	"cargo add":           3,
	"cargo run":           3,
	"cdk":                 2,
	"cf":                  2,
	"cmake":               2,
	"composer":            2,
	"consul":              2,
	"consul kv":           3,
	"crictl":              2,
	"deno":                2,
	"deno task":           3,
	"doctl":               3,
	"docker":              2,
	"docker builder":      3,
	"docker compose":      3,
	"docker container":    3,
	"docker image":        3,
	"docker network":      3,
	"docker volume":       3,
	"eksctl":              2,
	"eksctl create":       3,
	"firebase":            2,
	"flyctl":              2,
	"gcloud":              3,
	"gh":                  3,
	"git":                 2,
	"git config":          3,
	"git remote":          3,
	"git stash":           3,
	"go":                  2,
	"gradle":              2,
	"helm":                2,
	"heroku":              2,
	"hugo":                2,
	"ip":                  2,
	"ip addr":             3,
	"ip link":             3,
	"ip netns":            3,
	"ip route":            3,
	"kind":                2,
	"kind create":         3,
	"kubectl":             2,
	"kubectl kustomize":   3,
	"kubectl rollout":     3,
	"kustomize":           2,
	"make":                2,
	"mc":                  2,
	"mc admin":            3,
	"minikube":            2,
	"mongosh":             2,
	"mysql":               2,
	"mvn":                 2,
	"ng":                  2,
	"npm":                 2,
	"npm exec":            3,
	"npm init":            3,
	"npm run":             3,
	"npm view":            3,
	"nvm":                 2,
	"nx":                  2,
	"openssl":             2,
	"openssl req":         3,
	"openssl x509":        3,
	"pip":                 2,
	"pipenv":              2,
	"pnpm":                2,
	"pnpm dlx":            3,
	"pnpm exec":           3,
	"pnpm run":            3,
	"poetry":              2,
	"podman":              2,
	"podman container":    3,
	"podman image":        3,
	"psql":                2,
	"pulumi":              2,
	"pulumi stack":        3,
	"pyenv":               2,
	"python":              2,
	"rake":                2,
	"rbenv":               2,
	"redis-cli":           2,
	"rustup":              2,
	"serverless":          2,
	"sfdx":                3,
	"skaffold":            2,
	"sls":                 2,
	"sst":                 2,
	"swift":               2,
	"systemctl":           2,
	"terraform":           2,
	"terraform workspace": 3,
	"tmux":                2,
	"turbo":               2,
	"ufw":                 2,
	"vault":               2,
	"vault auth":          3,
	"vault kv":            3,
	"vercel":              2,
	"volta":               2,
	"wp":                  2,
	"yarn":                2,
	"yarn dlx":            3,
	"yarn run":            3,
}
