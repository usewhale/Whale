#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."

LIVE=0
RUN_TESTS=1
OUT=""
MODEL="deepseek-v4-flash"
USER_MODEL="deepseek-chat"
MODE="both"
REPEATS=1
TAU_REPEATS=1
LIVE_CACHE_REPEATS=1
TOOL_SHAPE_REPEATS=1

usage() {
  cat <<'USAGE'
Usage: scripts/bench/cost.sh [options]

Runs the local Whale cost/cache benchmark suite.

Default mode is offline and deterministic: no API key, no model calls.
Pass --live to run the DeepSeek-backed smoke/cost checks.

Options:
  --live                         Run live DeepSeek checks. Requires DEEPSEEK_API_KEY.
  --out <dir>                    Output root. Default: tmp/bench/cost/<timestamp>.
  --model <name>                 Agent model for live runs. Default: deepseek-v4-flash.
  --user-model <name>            User simulator model for tau-bench-lite. Default: deepseek-chat.
  --mode <baseline|whale|both>   Modes for live-cache and tau-bench-lite. Default: both.
  --repeats <n>                  Set all live repeats to n. Default: 1.
  --tau-repeats <n>              Repeats for tau-bench-lite. Default: 1.
  --live-cache-repeats <n>       Repeats for live-cache. Default: 1.
  --tool-shape-repeats <n>       Repeats for tool-shape live smoke. Default: 1.
  --no-tests                     Skip Go bench harness tests.
  -h, --help                     Show this help.

Examples:
  scripts/bench/cost.sh
  scripts/bench/cost.sh --live --repeats 1
  scripts/bench/cost.sh --live --out tmp/bench/cost/after-runtime-change
USAGE
}

require_value() {
  local flag="$1"
  local value="${2:-}"
  if [[ -z "$value" ]]; then
    echo "$flag requires a value" >&2
    exit 2
  fi
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --live)
      LIVE=1
      shift
      ;;
    --out)
      require_value "$1" "${2:-}"
      OUT="$2"
      shift 2
      ;;
    --model)
      require_value "$1" "${2:-}"
      MODEL="$2"
      shift 2
      ;;
    --user-model)
      require_value "$1" "${2:-}"
      USER_MODEL="$2"
      shift 2
      ;;
    --mode)
      require_value "$1" "${2:-}"
      MODE="$2"
      shift 2
      ;;
    --repeats)
      require_value "$1" "${2:-}"
      REPEATS="$2"
      TAU_REPEATS="$2"
      LIVE_CACHE_REPEATS="$2"
      TOOL_SHAPE_REPEATS="$2"
      shift 2
      ;;
    --tau-repeats)
      require_value "$1" "${2:-}"
      TAU_REPEATS="$2"
      shift 2
      ;;
    --live-cache-repeats)
      require_value "$1" "${2:-}"
      LIVE_CACHE_REPEATS="$2"
      shift 2
      ;;
    --tool-shape-repeats)
      require_value "$1" "${2:-}"
      TOOL_SHAPE_REPEATS="$2"
      shift 2
      ;;
    --no-tests)
      RUN_TESTS=0
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ "$OUT" == "" ]]; then
  OUT="tmp/bench/cost/$(date -u +%Y%m%dT%H%M%SZ)"
fi

if [[ "$LIVE" == "1" && -z "${DEEPSEEK_API_KEY:-}" ]]; then
  echo "DEEPSEEK_API_KEY is required for --live" >&2
  exit 1
fi

mkdir -p "$OUT"
mkdir -p .gocache

echo "[bench-cost] output: $OUT"
echo "[bench-cost] live: $LIVE"

if [[ "$RUN_TESTS" == "1" ]]; then
  echo "[bench-cost] testing bench harness"
  GOCACHE="${GOCACHE:-$PWD/.gocache}" go test ./bench/livecache ./bench/taubenchlite ./bench/toolshape
fi

echo "[bench-cost] tool-shape offline"
scripts/bench/tool_shape.sh --out "$OUT/tool-shape"

echo "[bench-cost] live-cache dry"
scripts/bench/live_cache.sh --dry --mode "$MODE" --repeats 1 --out "$OUT/live-cache-dry" --model "$MODEL"

echo "[bench-cost] tau-bench-lite dry"
scripts/bench/tau_bench_lite.sh --dry --mode "$MODE" --repeats 1 --out "$OUT/tau-bench-lite-dry" --model "$MODEL" --user-model "$USER_MODEL"

if [[ "$LIVE" == "1" ]]; then
  echo "[bench-cost] tool-shape live"
  scripts/bench/tool_shape.sh --live --repeats "$TOOL_SHAPE_REPEATS" --model "$MODEL" --out "$OUT/tool-shape-live"

  echo "[bench-cost] live-cache live"
  scripts/bench/live_cache.sh --mode "$MODE" --repeats "$LIVE_CACHE_REPEATS" --out "$OUT/live-cache" --model "$MODEL"

  echo "[bench-cost] tau-bench-lite live"
  scripts/bench/tau_bench_lite.sh --mode "$MODE" --repeats "$TAU_REPEATS" --out "$OUT/tau-bench-lite" --model "$MODEL" --user-model "$USER_MODEL"
fi

SUMMARY="$OUT/summary.md"
{
  printf "# Whale Cost Bench Local Run\n\n"
  printf "**Live:** %s\n" "$LIVE"
  printf "**Model:** \`%s\`\n" "$MODEL"
  printf "**Mode:** \`%s\`\n" "$MODE"
  printf "**Output:** \`%s\`\n\n" "$OUT"
  printf "## Reports\n\n"
  printf -- "- tool-shape offline: \`%s/tool-shape/report.md\`\n" "$OUT"
  printf -- "- live-cache dry: \`%s/live-cache-dry/report.md\`\n" "$OUT"
  printf -- "- tau-bench-lite dry: \`%s/tau-bench-lite-dry/report.md\`\n" "$OUT"
  if [[ "$LIVE" == "1" ]]; then
    printf -- "- tool-shape live: \`%s/tool-shape-live/report.md\`\n" "$OUT"
    printf -- "- live-cache live: \`%s/live-cache/report.md\`\n" "$OUT"
    printf -- "- tau-bench-lite live: \`%s/tau-bench-lite/report.md\`\n" "$OUT"
  fi
} > "$SUMMARY"

echo "[bench-cost] wrote $SUMMARY"
