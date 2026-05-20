# Whale Roadmap

This roadmap is for contributors and users interested in where Whale is heading. It is not a release commitment — it breaks down the most worthwhile directions into **discussable**, claimable, and verifiable todos.

**If you think anything on this roadmap should be improved, speak up. We are still learning.**

Whale's core positioning stays the same: DeepSeek-native, terminal-first, affordable for long coding sessions.

## Overview

- [ ] TUI stability and local telemetry
- [ ] Windows support
- [ ] Chinese-first documentation system
- [ ] Test system improvements
- [ ] Subagent capability optimization
- [ ] Token usage and cache hit rate comparisons
- [ ] Common slash command workflows
- [ ] Image recognition (doesn't have to be a DeepSeek model)

## TUI Stability and Local Telemetry

Whale needs to become a stable, smooth terminal tool where failures are diagnosable. The biggest UX issues today are not missing features — they are TUI interactions, information flow, and failure diagnostics.

- [ ] Identify and fix TUI lag issues
- [ ] Optimize streaming output refresh rhythm to reduce interference between input box, status bar, and chat area
- [ ] Improve information layering: user messages, model responses, thinking, tool calls, tool results, errors, and status hints should be easier to distinguish
- [ ] Optimize theme colors for dark terminals, light terminals, and low-contrast environments
- [ ] Improve display of approvals, diffs, shell output, MCP errors, and subagent progress
- [ ] Add local telemetry to observe tool call success rate, failure reasons, latency, retry count, token usage, and cache hits
- [ ] Improve error classification and hints after tool call failures
- [ ] Improve retry strategy: distinguish between retriable errors, parameter errors, permission denials, user cancellation, and cases where the model needs to replan
- [ ] Add debug entry points so users and contributors can quickly collect issue context

Splittable issues:

- [ ] TUI lag reproduction and profiling
- [ ] Redesign tool call/result display hierarchy
- [ ] Add tool call telemetry recording
- [ ] Add failure reason classification stats
- [ ] Improve MCP/tool error readability in TUI

## Windows Support

Windows support should not stop at "it compiles." Real support means installation, shell, paths, terminal, CI, releases, and documentation.

- [x] Add Windows CI covering full repo test compilation and basic shell runtime tests
- [x] Verify Windows shell command execution behavior, including `cmd`, PowerShell, and Git Bash trade-offs (shared shell resolver and basic runtime tests added)
- [ ] Fix Windows-specific differences: path handling, line endings, encoding, terminal size, key events
- [x] Add Windows release assets and verify required assets in the release workflow
- [x] Add PowerShell install script or clearly recommend an install method
- [x] Document current Windows support scope and known limitations
- [ ] Add Windows-specific `whale doctor` checks
- [ ] Verify that when Windows users ask Whale to compile a local project, stderr/stdout are returned to the model correctly

Splittable issues:

- [x] Windows CI baseline
- [x] Windows shell resolver
- [x] Windows install documentation
- [x] Windows release assets
- [ ] Windows terminal/TUI smoke test

## Chinese-First Documentation System

Whale's primary users and contributors are likely to read Chinese first. Documentation should get Chinese solid first, then add English.

- [ ] Add architecture docs explaining the relationship between CLI, TUI, App, Agent, Tools, MCP, and Skills
- [ ] Add quick-start docs covering install, setup, doctor, TUI, exec, resume, ask, plan
- [ ] Add provider configuration docs for DeepSeek official, Alibaba Cloud Bailian, Volcano Engine, SiliconFlow, and OpenAI-compatible endpoints
- [ ] Add MCP setup tutorial and common server examples
- [ ] Add Skills usage, installation, creation, and disable guides
- [ ] Add contribution guide: how to set up the environment, run tests, debug TUI bugs, write evals, and submit PRs
- [ ] Add debugging guide: how to read logs, telemetry, usage, session, and MCP status
- [ ] Fill in FAQ: API keys, model selection, caching, cost, Windows, terminal compatibility, common errors

Splittable issues:

- [ ] `docs/architecture.md`
- [ ] `docs/getting-started.md`
- [ ] `docs/providers.md`
- [ ] `docs/debugging.md`
- [ ] `docs/contributing.zh-CN.md`

## Test System Improvements

Whale already has Go unit tests and offline evals, but TUI interactions, end-to-end behavior, and benchmarks need systematizing. The test system should serve real regression, not just chase quantity.

- [ ] Fill in core package tests: config, session, policy, tools, agent, mcp, skills, telemetry
- [ ] Add TUI behavior tests covering input, queuing, approval, slash picker, skills picker, session picker, interrupt
- [ ] Add TUI render/golden tests to freeze key UI output under narrow, wide, and mixed-language layouts
- [ ] Improve eval harness docs so contributors can add new deterministic evals
- [ ] Add more regression evals: tool parameter repair, failure recovery, ask/plan modes, MCP errors, subagent result aggregation
- [ ] Organize SWE-bench usage docs, clarifying it is an external benchmark, not a substitute for local regression tests
- [ ] Add boundary notes for live smoke tests: only for real API verification, not a mandatory CI run
- [ ] Clarify in the PR template which tests different change types should run

Splittable issues:

- [ ] TUI golden test helper
- [ ] Compact (auto and manual) tests
- [ ] Eval task writing guide
- [ ] Ask/plan mode regression evals
- [ ] MCP failure recovery evals
- [ ] SWE-bench usage guide

## Subagent Capability Optimization

Subagents are still relatively weak. Focus on reliability and observability first, then consider more complex multi-agent orchestration.

- [ ] Clarify subagent usage boundaries: read-only exploration, review, research, or can it be extended to stronger tasks
- [ ] Improve subagent prompts to return structured, useful, and actionable conclusions
- [ ] Improve subagent progress display so users know what it is doing
- [ ] Add subagent token, latency, and failure rate stats
- [ ] Add subagent cancellation and timeout handling
- [ ] Support clearer roles such as `explore`, `review`, `research`
- [ ] Investigate whether to fork sessions or use independent context instead of one-shot tool calls
- [ ] Add subagent-related evals to verify it is actually more useful than the main agent searching alone

Splittable issues:

- [ ] Subagent prompt optimization
- [ ] Subagent telemetry
- [ ] Subagent TUI progress
- [ ] Subagent timeout/cancel
- [ ] Subagent eval cases

## Token Usage and Cache Hit Rate Comparisons

One of Whale's differentiators is DeepSeek's cost and prefix cache. Credible data is needed to show how it differs from other agents.

- [ ] Design a comparison task set: read code, fix bugs, run tests, refactor, small multi-turn tasks, large repo exploration
- [ ] Record Whale's token usage, cache hits, latency, tool call count, and success rate
- [ ] Compare with pi, Codex CLI, Claude Code, DeepSeek-TUI, Aider, and other common agents
- [ ] Separate the impact of model pricing, cache hits, context strategy, and tool call strategy on cost
- [ ] Output reproducible benchmark scripts
- [ ] Output a report in Chinese explaining which tasks Whale is cheaper for and which tasks it is not good enough at yet
- [ ] Avoid treating one-shot results as permanent conclusions — note the date, version, model, and config in reports

Splittable issues:

- [ ] Benchmark task set design
- [ ] Token/cache collection format
- [ ] Whale vs pi cost comparison
- [ ] Whale vs Codex/Claude Code cost comparison
- [ ] Chinese benchmark report

## Common Slash Commands and @-based Workflows

New slash commands should serve real workflows, not expand the command surface for its own sake. Each command needs to answer: what problem does it solve, is it just an alias, does it need persistent state, and can it be tested?

- [ ] `/review`: review the current git diff or a specified range, output a priority-ordered review
- [ ] `/fork`: fork the current session to explore an alternative path
- [ ] `/cwd`: view or change the current working directory; also evaluate whether `/status` or the status bar is a better fit
- [ ] `/btw`: define the semantics clearly before implementing, avoid becoming a vague catch-all
- [ ] Support `@`-based scoping operations
- [ ] Support rules configuration

Splittable issues:

- [ ] `/review` design and implementation
- [ ] `/fork` session semantics design
- [ ] `/cwd` — should it be a slash command?
- [ ] `/btw` design and implementation
- [ ] Support `@`-based operations
- [ ] Support rules configuration

## Image Recognition

Whale does not yet support image input, but this is useful in development scenarios.

- [ ] Investigate vision models that support image input via API
- [ ] Support pasting or dragging images into the TUI, converting to base64 for prompt context
- [ ] Integrate once DeepSeek's model supports image recognition via API
- [ ] Add corresponding TUI render display and regression tests after integration

Splittable issues:

- [ ] Vision model research
- [ ] TUI image paste/drag interaction
- [ ] Image to base64 prompt injection flow
- [ ] Image recognition integration tests

## Not Doing (for now)

- [ ] No rush to build a large dashboard
- [ ] No hooks for now (some code exists, but it is premature)
- [ ] No overly novel or complex agent designs
