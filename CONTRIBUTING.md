# Contributing to Whale

Whale is still experimental. Small, focused changes are easier to review than broad refactors mixed with behavior changes.

## Before you start

- Read [README.md](README.md) for the public command surface.
- Read [AGENTS.md](AGENTS.md) for repo layout and local development conventions.
- Open an issue first if you want to add a new top-level command, change session file formats, or widen the provider surface beyond DeepSeek.

## Getting the code

```bash
git clone https://github.com/usewhale/Whale.git
cd Whale
make build
make test
```

## Development setup

```bash
make build
make test
```

Whale uses a repo-local `.gocache` through the Makefile, so the commands above are the preferred default. On Windows or systems without `make`, use the equivalent cross-platform runner:

```bash
go run ./cmd/dev build
go run ./cmd/dev test
```

Useful focused commands:

```bash
make test-tui
make test-evals
make run
```

- `make test` runs all offline Go tests.
- `make test-tui` runs the TUI-focused subset.
- `make test-evals` runs the eval-focused subset.
- `go run ./cmd/dev test-windows` runs the supported Windows CI subset on Windows.

Live smoke scripts exist under `scripts/smoke/`, but they require a real DeepSeek key and paid API access:

```bash
DEEPSEEK_API_KEY=... ./scripts/smoke/real_stream.sh
DEEPSEEK_API_KEY=... ./scripts/smoke/real_cache.sh
```

## Opening issues

- Use the bug report template for behavior regressions, crashes, install failures, or documentation errors.
- Use the feature request template for new workflow requests or command-surface changes.
- Include the Whale version, operating system, reproduction steps, and whether local hooks or custom config were involved.
- If the change touches top-level commands, session formats, or provider scope, open an issue before writing code.

## Contribution guidelines

- Keep changes narrow and behavior-driven.
- Add or update tests with code changes whenever practical.
- Prefer offline tests, temp dirs, and fake providers over live network calls.
- Do not commit local state such as `.whale/`, session files, usage logs, or API keys.
- Treat `./.whale/settings.json` as untrusted input when reproducing another workspace, because hooks can execute shell commands.

## Pull requests

PRs should include:

- what behavior changed
- why the change is needed
- what tests you ran
- terminal output or screenshots for user-visible CLI/TUI changes

Small bug fixes and documentation updates can go straight to a PR. For larger behavior changes, especially around CLI surface, session persistence, or provider support, discuss the direction in an issue first.

If the change is intentionally breaking, say so clearly in the PR description.
