#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
if [[ " $* " != *" --dry "* ]]; then
  : "${DEEPSEEK_API_KEY:?DEEPSEEK_API_KEY is required}"
fi
mkdir -p .gocache
GOCACHE="${GOCACHE:-$PWD/.gocache}" go run ./bench/livecache "$@"
