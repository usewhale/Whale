# Whale Cost Benchmarks

This directory contains local benchmark harnesses for Whale prompt cost, cache
shape, and tool schema analysis. These are development/evaluation tools, not
runtime packages.

## Benchmarks

- `toolshape`: offline tool schema payload analysis, with an optional live smoke
  that compares `base` vs `base+task_tools`.
- `livecache`: prefix-cache behavior benchmark for Whale vs a benchmark-only
  volatile-prefix baseline.
- `taubenchlite`: small retail customer-service tool-calling benchmark inspired
  by tau-bench and Reasonix-style cost reports.

## Local Entry Points

Run the deterministic local suite:

```bash
make bench-cost
```

This does not require an API key. It runs harness tests, `toolshape` offline,
and dry runs for `livecache` and `taubenchlite`.

Run live DeepSeek cost checks explicitly:

```bash
DEEPSEEK_API_KEY=sk-... make bench-cost-live
```

For fixed before/after comparisons, prefer an explicit output directory:

```bash
DEEPSEEK_API_KEY=sk-... scripts/bench/cost.sh --live --out tmp/bench/cost/after-runtime-change
```

## Direct Commands

```bash
scripts/bench/tool_shape.sh --out tmp/bench/tool-shape/current
scripts/bench/tool_shape.sh --live --repeats 1 --out tmp/bench/tool-shape/live-current

scripts/bench/live_cache.sh --dry --mode both --repeats 1
scripts/bench/live_cache.sh --mode both --repeats 1

scripts/bench/tau_bench_lite.sh --dry --mode both --repeats 1
scripts/bench/tau_bench_lite.sh --mode both --repeats 1
```

Live commands require `DEEPSEEK_API_KEY`.

## Interpreting Results

Use offline results for deterministic regression checks:

- tool count and schema bytes
- `spawn_subagent` schema size and share
- tool hash changes
- report rendering and harness wiring

Use live results for local before/after evidence:

- prompt and completion tokens
- prompt cache hit/miss ratio
- estimated cost
- cache shape hashes for system, runtime, tools, and request

Live cache and cost numbers are not deterministic. They can vary with request
order, provider-side cache state, model behavior, and timing. Use them for local
comparison runs, not as exact standalone measurements.
