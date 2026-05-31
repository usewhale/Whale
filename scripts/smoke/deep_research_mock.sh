#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../.."

ROOT="$(pwd)"
WHALE_BIN="${WHALE_BIN:-$ROOT/bin/whale}"
TIMEOUT_SEC="${WHALE_DEEP_RESEARCH_MOCK_TIMEOUT_SEC:-90}"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/whale-deep-research-smoke.XXXXXX")"
FAILED=0

cleanup() {
  local status=$?
  if [[ "$status" -ne 0 ]]; then
    FAILED=1
  fi
  if [[ -n "${MOCK_PID:-}" ]]; then
    kill "$MOCK_PID" >/dev/null 2>&1 || true
  fi
  if [[ "${KEEP_WHALE_DEEP_RESEARCH_SMOKE:-}" == "1" || "$FAILED" == "1" ]]; then
    echo "[smoke/deep_research_mock] artifacts preserved: $TMP_ROOT" >&2
  else
    rm -rf "$TMP_ROOT"
  fi
}
trap cleanup EXIT

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "[smoke/deep_research_mock] missing required command: $1" >&2
    exit 1
  fi
}

require_cmd python3
if [[ ! -x "$WHALE_BIN" ]]; then
  echo "[smoke/deep_research_mock] make build"
  make build
fi

PORT_FILE="$TMP_ROOT/port"
cat >"$TMP_ROOT/mock_deepseek.py" <<'PY'
import json
import os
import socket
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

def pick_free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port

def response_for(messages):
    text = "\n".join(str(m.get("content", "")) for m in messages)
    if "Decompose this research question" in text:
        return {
            "question": "What changed in the Node.js permission model between v20 and v22?",
            "summary": "Compare official docs, release notes, and security hardening.",
            "angles": [
                {"label": "Official docs", "query": "Node.js v20 v22 permission model docs", "rationale": "API surface"},
                {"label": "Release notes", "query": "Node.js permission model v22 release notes", "rationale": "Release deltas"},
                {"label": "Security", "query": "Node.js permission model CVE v20 v22", "rationale": "Hardening"},
            ],
        }
    if "## Web Searcher:" in text:
        return {
            "results": [
                {
                    "url": "https://nodejs.org/mock/" + str(abs(hash(text)) % 100000),
                    "title": "Mock Node.js permission model source",
                    "snippet": "Mock source relevant to the permission model comparison.",
                    "relevance": "high",
                }
            ]
        }
    if "## Source Extractor" in text:
        return {
            "sourceQuality": "primary",
            "publishDate": "2026-01-01",
            "claims": [
                {
                    "claim": "The Node.js permission model changed between v20 and v22 in this mock source.",
                    "quote": "Permission model change evidence.",
                    "importance": "central",
                }
            ],
        }
    if "## Adversarial Claim Verifier" in text:
        return {"refuted": False, "evidence": "Mock primary source supports the claim.", "confidence": "high"}
    if "## Synthesis: research report" in text:
        return {
            "summary": "Mock synthesis completed for the Node.js permission model comparison.",
            "findings": [
                {
                    "claim": "Mock finding: the permission model changed across versioned surfaces.",
                    "confidence": "high",
                    "sources": ["https://nodejs.org/mock/source"],
                    "evidence": "Verified mock claims survived adversarial review.",
                    "vote": "3-0",
                }
            ],
            "caveats": "Mock provider smoke test; not factual research.",
            "openQuestions": ["Which exact patch releases carried each real change?"],
        }
    return {"summary": "mock structured output", "findings": [], "caveats": "mock"}

def sse(w, frame):
    w.write(("data: " + json.dumps(frame, separators=(",", ":")) + "\n\n").encode())

class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        return

    def do_POST(self):
        if self.path != "/chat/completions":
            self.send_error(404)
            return
        length = int(self.headers.get("content-length", "0"))
        payload = json.loads(self.rfile.read(length) or b"{}")
        messages = payload.get("messages") or []
        tools = payload.get("tools") or []
        tool_names = [((t.get("function") or {}).get("name") or "") for t in tools]
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.end_headers()
        if "workflow" in tool_names and not any(m.get("role") == "tool" for m in messages):
            args = json.dumps({
                "name": "deep-research",
                "args": {"question": "What changed in the Node.js permission model between v20 and v22?"},
            }, separators=(",", ":"))
            sse(self.wfile, {"choices": [{"delta": {"tool_calls": [{"index": 0, "id": "call_workflow", "function": {"name": "workflow", "arguments": args}}]}, "finish_reason": "tool_calls"}], "usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}})
        elif "structured_output" in tool_names and not any(m.get("role") == "tool" for m in messages):
            args = json.dumps(response_for(messages), separators=(",", ":"))
            sse(self.wfile, {"choices": [{"delta": {"tool_calls": [{"index": 0, "id": "call_structured", "function": {"name": "structured_output", "arguments": args}}]}, "finish_reason": "tool_calls"}], "usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}})
        else:
            if any(m.get("role") == "tool" and '"runId"' in str(m.get("content", "")) for m in messages):
                time.sleep(15)
            sse(self.wfile, {"choices": [{"delta": {"content": "ok"}, "finish_reason": "stop"}], "usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}})
        self.wfile.write(b"data: [DONE]\n\n")

port = pick_free_port()
with open(os.environ["PORT_FILE"], "w", encoding="utf-8") as f:
    f.write(str(port))
ThreadingHTTPServer(("127.0.0.1", port), Handler).serve_forever()
PY

PORT_FILE="$PORT_FILE" python3 "$TMP_ROOT/mock_deepseek.py" >"$TMP_ROOT/mock.log" 2>&1 &
MOCK_PID=$!
for _ in $(seq 1 50); do
  [[ -s "$PORT_FILE" ]] && break
  sleep 0.1
done
if [[ ! -s "$PORT_FILE" ]]; then
  echo "[smoke/deep_research_mock] mock server did not start" >&2
  exit 1
fi
MOCK_PORT="$(cat "$PORT_FILE")"

WHALE_HOME="$TMP_ROOT/home"
WORKSPACE="$TMP_ROOT/workspace"
mkdir -p "$WHALE_HOME" "$WORKSPACE"
cat >"$WHALE_HOME/config.toml" <<'TOML'
[permissions]
default = "allow"
auto_accept = true

[ui]
check_for_update_on_startup = false
TOML

echo "[smoke/deep_research_mock] running ./bin/whale deep-research against mock DeepSeek"
(
  cd "$WORKSPACE"
  export WHALE_HOME
  export DEEPSEEK_API_KEY="mock-key"
  export DEEPSEEK_BASE_URL="http://127.0.0.1:${MOCK_PORT}"
  "$WHALE_BIN" exec --json --timeout-sec 30 --dangerously-skip-permissions \
    "Launch the builtin deep-research workflow for: What changed in the Node.js permission model between v20 and v22?"
) >"$TMP_ROOT/exec.json" 2>"$TMP_ROOT/exec.err"

python3 - "$TMP_ROOT/exec.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as f:
    data = json.load(f)
if data.get("status") != "completed":
    print(f"exec status={data.get('status')!r} error={data.get('error')!r}", file=sys.stderr)
    sys.exit(1)
if not any((t.get("name") == "workflow" and t.get("success")) for t in (data.get("tools") or [])):
    print("workflow tool was not called successfully", file=sys.stderr)
    sys.exit(1)
PY

python3 - "$WHALE_HOME/runs" "$TIMEOUT_SEC" <<'PY'
import json
import os
import sys
import time

runs_dir, timeout = sys.argv[1], float(sys.argv[2])
deadline = time.time() + timeout
last = ""
while time.time() < deadline:
    paths = []
    if os.path.isdir(runs_dir):
        for name in os.listdir(runs_dir):
            p = os.path.join(runs_dir, name, "events.jsonl")
            if os.path.exists(p):
                paths.append(p)
    for path in sorted(paths, key=os.path.getmtime, reverse=True):
        events = []
        with open(path, encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if line:
                    events.append(json.loads(line))
        terminal = next((e for e in reversed(events) if e.get("type") in ("run_completed", "run_failed", "run_cancelled")), None)
        if terminal:
            rid = os.path.basename(os.path.dirname(path))
            last = f"{rid}: {terminal.get('type')} {terminal.get('message', '')}"
            if terminal.get("type") != "run_completed":
                print(last, file=sys.stderr)
                sys.exit(1)
            phases = {e.get("phase") or e.get("message") for e in events if e.get("type") == "phase_started"}
            if "Synthesize" not in phases:
                print(f"{rid}: completed without Synthesize phase", file=sys.stderr)
                sys.exit(1)
            result = ((terminal.get("data") or {}).get("result") or {})
            stats = result.get("stats") or {}
            if stats.get("afterSynthesis") != 1:
                print(f"{rid}: afterSynthesis={stats.get('afterSynthesis')!r}", file=sys.stderr)
                sys.exit(1)
            print(f"[smoke/deep_research_mock] ok {rid}: completed through Synthesize")
            sys.exit(0)
    time.sleep(0.5)
print("[smoke/deep_research_mock] no terminal workflow event; last=" + last, file=sys.stderr)
sys.exit(1)
PY
