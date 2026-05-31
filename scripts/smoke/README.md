# Smoke Checks

Run from the repository root.

- `./scripts/smoke/local.sh` runs the offline Go suite and the mock deep-research E2E.
- `./scripts/smoke/deep_research_mock.sh` starts a local DeepSeek-compatible mock server, runs the real `./bin/whale exec` binary, launches the builtin `deep-research` workflow, and verifies that the workflow completes through `Synthesize`.
- `./scripts/smoke/real_stream.sh`, `./scripts/smoke/real_cache.sh`, and `./scripts/smoke/mcp_tools.sh` require a working `DEEPSEEK_API_KEY`.

Use the mock deep-research smoke when the provider account is unavailable but the workflow runtime path still needs to be verified.
