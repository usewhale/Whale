#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."

: "${DEEPSEEK_API_KEY:?DEEPSEEK_API_KEY is required}"

ROOT="$(pwd)"
WHALE_BIN="${WHALE_BIN:-$ROOT/bin/whale}"
TIMEOUT_SEC="${WHALE_MCP_SMOKE_TIMEOUT_SEC:-90}"
SECRET_TOKEN="whale-secret-smoke-token"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/whale-mcp-smoke.XXXXXX")"
FAILED=0

cleanup() {
  local status=$?
  if [[ "$status" -ne 0 ]]; then
    FAILED=1
  fi
  if [[ "${KEEP_WHALE_MCP_SMOKE:-}" == "1" || "$FAILED" == "1" ]]; then
    echo "[smoke/mcp_tools] artifacts preserved: $TMP_ROOT" >&2
  else
    rm -rf "$TMP_ROOT"
  fi
}
trap cleanup EXIT

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "[smoke/mcp_tools] missing required command: $1" >&2
    exit 1
  fi
}

require_cmd python3
require_cmd expect
require_cmd npx

if [[ ! -x "$WHALE_BIN" ]]; then
  echo "[smoke/mcp_tools] make build"
  make build
fi

WHALE_HOME="$TMP_ROOT/home"
WORKSPACE="$TMP_ROOT/workspace"
FS_DIR="$TMP_ROOT/fs"
mkdir -p "$WHALE_HOME" "$WORKSPACE" "$FS_DIR"
printf 'hello from whale mcp smoke\n' >"$FS_DIR/smoke.txt"

cat >"$WHALE_HOME/config.toml" <<'TOML'
[permissions]
default = "allow"
auto_accept = true

[ui]
check_for_update_on_startup = false
TOML

cat >"$WHALE_HOME/mcp.json" <<JSON
{
  "mcpServers": {
    "slow": {
      "command": "sh",
      "args": ["-c", "sleep 30"],
      "timeout": 30
    },
    "secret": {
      "command": "sh",
      "args": ["-c", "exit 1", "--header=Authorization: Bearer ${SECRET_TOKEN}"],
      "timeout": 1
    },
    "fs": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "${FS_DIR}"],
      "timeout": 20
    }
  }
}
JSON

MCP_OUT="$TMP_ROOT/mcp.out"
MCP_TEXT="$TMP_ROOT/mcp.txt"
echo "[smoke/mcp_tools] probing /mcp in the TUI"
(
  cd "$WORKSPACE"
  export WHALE_HOME
  export DEEPSEEK_API_KEY
  export WHALE_BIN
  expect <<'EXPECT'
set timeout 35
log_user 1
spawn $env(WHALE_BIN)
after 1000
send -- "/mcp\r"
expect {
  -re {MCP Tools} {}
  timeout {}
}
after 8000
send -- "/mcp\r"
after 1000
send -- "/exit\r"
expect eof
EXPECT
) >"$MCP_OUT" 2>&1 || {
  echo "[smoke/mcp_tools] /mcp TUI probe failed" >&2
  exit 1
}

python3 - "$MCP_OUT" "$MCP_TEXT" <<'PY'
import re
import sys

raw_path, text_path = sys.argv[1:3]
data = open(raw_path, "rb").read().decode("utf-8", "replace")
data = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", data)
data = data.replace("\r", "\n")
open(text_path, "w", encoding="utf-8").write(data)

missing = [s for s in ["MCP Tools", "fs", "slow", "secret"] if s not in data]
if missing:
    print(f"missing expected /mcp text: {', '.join(missing)}", file=sys.stderr)
    sys.exit(1)
PY

if grep -Fq "$SECRET_TOKEN" "$MCP_TEXT"; then
  echo "[smoke/mcp_tools] secret token leaked in /mcp output" >&2
  exit 1
fi

EXEC_OUT="$TMP_ROOT/exec.json"
EXEC_ERR="$TMP_ROOT/exec.err"
PROMPT="Use the MCP filesystem server to list this exact directory: ${FS_DIR}. You must call mcp__fs__list_directory with path ${FS_DIR}. Then answer with MCP_SMOKE_DONE."

echo "[smoke/mcp_tools] running whale exec with MCP fs"
(
  cd "$WORKSPACE"
  WHALE_HOME="$WHALE_HOME" DEEPSEEK_API_KEY="$DEEPSEEK_API_KEY" "$WHALE_BIN" exec --json --timeout-sec "$TIMEOUT_SEC" "$PROMPT"
) >"$EXEC_OUT" 2>"$EXEC_ERR"

python3 - "$EXEC_OUT" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as f:
    data = json.load(f)

if data.get("status") != "completed":
    print(f"exec status was {data.get('status')!r}: {data.get('error', '')}", file=sys.stderr)
    sys.exit(1)

tools = data.get("tools") or []
if not any(t.get("name") == "mcp__fs__list_directory" and t.get("success") for t in tools):
    print("mcp__fs__list_directory was not called successfully", file=sys.stderr)
    print(json.dumps(tools, indent=2), file=sys.stderr)
    sys.exit(1)

if "MCP_SMOKE_DONE" not in (data.get("output") or ""):
    print("final output did not contain MCP_SMOKE_DONE", file=sys.stderr)
    sys.exit(1)
PY

if grep -Fq "$SECRET_TOKEN" "$EXEC_OUT" "$EXEC_ERR"; then
  echo "[smoke/mcp_tools] secret token leaked in exec output" >&2
  exit 1
fi

echo "[smoke/mcp_tools] ok"
