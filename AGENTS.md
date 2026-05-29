# Repository Guidelines

## Project Structure & Module Organization

Whale is a Go CLI/TUI agent. The entrypoint is `cmd/whale`. Most application code lives under `internal/`:

- `internal/ui/cli` and `internal/tui` for Cobra commands and terminal UI
- `internal/agent` for the turn loop, approvals, hooks, and tool orchestration
- `internal/tools` for built-in tools such as shell, patch, file, and web actions
- `internal/app`, `internal/session`, `internal/memory`, and `internal/store` for runtime state and persistence
- `internal/evals` for replay and harness-based eval coverage
- `docs/` for user-facing configuration docs
- `scripts/smoke/` for live API smoke checks that require a real DeepSeek key

Keep new packages focused and place tests next to the code they cover.

## Build, Test, and Development Commands

- `make build` (or `go build -o bin/whale .` on Windows without Make) builds `bin/whale` with the repo-local `.gocache`
- `make run` (or `go build -o bin/whale . && ./bin/whale`) builds and launches the interactive TUI
- `make test` (or `go test ./...`) runs the full offline Go test suite
- `make test-tui` (or `go test ./internal/tui/...`) runs the TUI-focused subset
- `make test-evals` (or `go test ./internal/evals/...`) runs eval and replay tests
- `make clean` removes `bin/` and `.gocache`

Prefer Makefile targets on macOS/Linux. On Windows (where `make` is not available by default), use the equivalent `go` commands listed above. The `.gocache` directory is managed automatically so cache paths stay consistent regardless of which method you use.

## Coding Style & Naming Conventions

Follow standard Go style: tabs for indentation, `gofmt` formatting, concise package names, and exported identifiers only when cross-package use is required. Prefer table-driven tests for behavior matrices. Name files by responsibility, such as `tool_repair.go`, `model_test.go`, or `preferences_test.go`.

## Testing Guidelines

Tests use Go's `testing` package. Name tests `TestXxx` and keep them adjacent to the implementation. Prefer offline tests with temp dirs, fake providers, and deterministic fixtures; live network checks belong in `scripts/smoke/`. For user-visible CLI/TUI changes, add or update behavior-level tests, not just helper-unit coverage.

## Commit & Pull Request Guidelines

Recent history mixes short imperative subjects with scoped prefixes such as `feat(tui): ...`, `agent: ...`, and `chore: ...`. Keep commits small and behavior-focused. PRs should state what changed, why it changed, and what you tested; include terminal output or screenshots when CLI/TUI behavior changes. Open an issue before broad command-surface, session-format, or provider-scope changes.

## Security & Configuration Tips

Do not commit `~/.whale/` data, `./.whale/settings.json`, session logs, or API keys. Treat project hook config as untrusted input because hooks can execute shell commands.
