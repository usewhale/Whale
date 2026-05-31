#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
echo "[smoke/local] go test ./..."
go test ./...
echo "[smoke/local] scripts/smoke/deep_research_mock.sh"
scripts/smoke/deep_research_mock.sh
