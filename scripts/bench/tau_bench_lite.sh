#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" != "--dry" && -z "${DEEPSEEK_API_KEY:-}" ]]; then
  echo "DEEPSEEK_API_KEY is required unless --dry is passed" >&2
  exit 1
fi

go run ./bench/taubenchlite "$@"
