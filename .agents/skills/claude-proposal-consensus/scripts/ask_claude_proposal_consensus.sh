#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage:
  ask_claude_proposal_consensus.sh [options]

Round 1 required:
  -w, --workspace <path>       Workspace directory to inspect
  -t, --task <text>            Original user problem
      --round <n>              Proposal round number

Resumed rounds required:
  -w, --workspace <path>       Workspace directory to inspect
      --feedback-file <path>   OpenAI agent review feedback markdown
      --round <n>              Proposal round number
      --session <id>           Resume the Claude session for this same requirement

Options:
      --model <name>           Claude model (default: use Claude CLI default)
      --effort <level>         Effort: low, medium, high, max (default: max)
      --permission-mode <mode> Claude permission mode for new sessions (default: auto);
                               Claude mutation tools are disabled
  -o, --output <path>          Output markdown path (default: .runtime/<timestamp>.md)
  -h, --help                   Show this help

Output (on success):
  session_id=<session_id>      Keep only inside this consensus subagent/request
  output_path=<file>           Path to Claude proposal markdown
USAGE
}

fail() {
  echo "[ERROR] $*" >&2
  exit 1
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    fail "Missing required command: $1"
  fi
}

take_value() {
  local option="$1"
  if [[ $# -lt 2 || -z "${2:-}" ]]; then
    fail "Missing value for $option."
  fi
  printf '%s' "$2"
}

trim_whitespace() {
  local value="$1"
  value="${value#"${value%%[![:space:]]*}"}"
  value="${value%"${value##*[![:space:]]}"}"
  printf '%s' "$value"
}

read_text_file() {
  local option="$1" path="$2"
  [[ -f "$path" ]] || fail "$option file does not exist: $path"
  cat -- "$path"
}

workspace="${PWD}"
task_text=""
feedback_file=""
feedback_text=""
round="1"
session_id=""
model=""
effort="max"
permission_mode="auto"
output_path=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    -w|--workspace) workspace="$(take_value "$1" "${2:-}")"; shift 2 ;;
    -t|--task) task_text="$(take_value "$1" "${2:-}")"; shift 2 ;;
    --feedback-file) feedback_file="$(take_value "$1" "${2:-}")"; shift 2 ;;
    --round) round="$(take_value "$1" "${2:-}")"; shift 2 ;;
    --session) session_id="$(take_value "$1" "${2:-}")"; shift 2 ;;
    --model) model="$(take_value "$1" "${2:-}")"; shift 2 ;;
    --effort|--reasoning) effort="$(take_value "$1" "${2:-}")"; shift 2 ;;
    --permission-mode) permission_mode="$(take_value "$1" "${2:-}")"; shift 2 ;;
    -o|--output) output_path="$(take_value "$1" "${2:-}")"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "[ERROR] Unknown option: $1" >&2; usage >&2; exit 1 ;;
    *) echo "[ERROR] Unexpected argument: $1" >&2; usage >&2; exit 1 ;;
  esac
done

[[ -n "$workspace" ]] || fail "Workspace path is empty."
[[ -d "$workspace" ]] || fail "Workspace does not exist: $workspace"
workspace="$(cd "$workspace" && pwd)"

task_text="$(trim_whitespace "$task_text")"
round="$(trim_whitespace "$round")"
session_id="$(trim_whitespace "$session_id")"
feedback_file="$(trim_whitespace "$feedback_file")"

[[ "$round" =~ ^[0-9]+$ ]] || fail "--round must be a positive integer."
(( round >= 1 )) || fail "--round must be a positive integer."

if [[ -z "$session_id" ]]; then
  [[ -n "$task_text" ]] || fail "Missing required --task for round 1."
  [[ -z "$feedback_file" ]] || fail "--feedback-file requires --session."
else
  [[ -n "$feedback_file" ]] || fail "Missing required --feedback-file for resumed rounds."
  feedback_text="$(read_text_file "--feedback-file" "$feedback_file")"
fi

require_cmd claude
require_cmd jq

if [[ -z "$output_path" ]]; then
  timestamp="$(date -u +"%Y%m%d-%H%M%S")"
  skill_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  output_path="$skill_dir/.runtime/${timestamp}.md"
fi
mkdir -p "$(dirname "$output_path")"

proposal_contract="$(cat <<'CONTRACT'
You are Claude. In this workflow, you are the proposal owner and the OpenAI agent is the reviewer.

Rules:
- You are read-only. Use only Read, Grep, Glob, and LS. Do not edit files.
- Produce a complete plan for the user's task. Do not execute the plan.
- If the OpenAI agent provides feedback, return a full revised plan, not a diff, patch, or partial update.
- Do not invent missing facts. State blockers or assumptions clearly.
- Keep the proposal scoped to the user's request and the current workspace.

Your proposal should include:
- Restated goal and assumptions.
- Relevant workspace findings.
- Recommended approach and architecture rationale.
- Files or modules expected to change if the plan is later executed.
- Step-by-step implementation plan.
- Verification plan.
- Database changes (if applicable): a dedicated, clearly labeled section listing all database-related changes, including schema migrations, new or altered tables/columns/indexes, data migrations, stored procedure changes, ORM model changes that imply DDL, and connection or pool configuration changes. Each item should state what changes and why. Database-related work must not appear only inside implementation steps; it must be surfaced in this section for at-a-glance visibility.
- Risks, blockers, and decisions needed from the user.
CONTRACT
)"

if [[ -n "$session_id" ]]; then
  prompt="$(cat <<PROMPT
$proposal_contract

Consensus round:
$round

OpenAI agent review feedback:
$feedback_text

Revise your proposal to address the feedback. Output the complete revised plan.
PROMPT
)"
else
  prompt="$(cat <<PROMPT
$proposal_contract

Workspace:
$workspace

Consensus round:
$round

Original user problem:
$task_text

Inspect the workspace as needed and output the complete initial proposal.
PROMPT
)"
fi

cmd=(claude -p --verbose --output-format stream-json --effort "$effort" --tools "Read,Grep,Glob,LS")
if [[ -n "$session_id" ]]; then
  cmd+=(--resume "$session_id")
else
  cmd+=(--permission-mode "$permission_mode")
fi
[[ -n "$model" ]] && cmd+=(--model "$model")

stderr_file="$(mktemp)"
json_file="$(mktemp)"
prompt_file="$(mktemp)"
trap 'rm -f "$stderr_file" "$json_file" "$prompt_file"' EXIT

printf "%s" "$prompt" > "$prompt_file"

run_claude() {
  (cd "$workspace" && "${cmd[@]}" < "$prompt_file" 2>"$stderr_file")
}

print_progress() {
  local line="$1" text tool
  case "$line" in
    *'"type":"system"'*'"session_id"'*)
      text="$(printf '%s' "$line" | jq -r '.session_id // empty' 2>/dev/null | cut -c1-80)"
      [[ -n "$text" ]] && echo "[claude] session $text" >&2
      ;;
    *'"type":"assistant"'*)
      text="$(printf '%s' "$line" | jq -r '
        .message.content?
        | if type == "array" then .[]?
          elif type == "object" then .
          elif type == "string" and . != "" then {type: "text", text: .}
          else empty end
        | select(.type == "text")
        | .text
      ' 2>/dev/null | sed -n '1p' | cut -c1-120)"
      [[ -n "$text" ]] && echo "[claude] $text" >&2
      ;;
    *'"type":"tool_use"'*|*'"tool_use"'*)
      tool="$(printf '%s' "$line" | jq -r '
        first(
          .. | objects | select(.type? == "tool_use")
          | (.name? // "") as $name
          | select($name != "" and (["Read", "Grep", "Glob", "LS"] | index($name) | not))
          | $name
        ) // empty
      ' 2>/dev/null | cut -c1-80)"
      [[ -n "$tool" ]] && echo "[claude] non-read-only tool requested: $tool" >&2
      ;;
  esac
}

set +e
run_claude | while IFS= read -r line; do
  cleaned="${line//$'\r'/}"
  cleaned="${cleaned//$'\004'/}"
  [[ -z "$cleaned" || "$cleaned" != \{* ]] && continue
  printf '%s\n' "$cleaned" >> "$json_file"
  print_progress "$cleaned"
done
exit_code=${PIPESTATUS[0]}
set -e

if [[ -s "$stderr_file" ]]; then
  cat "$stderr_file" >&2
fi

if [[ "$exit_code" -ne 0 && ! -s "$json_file" ]]; then
  fail "Claude exited with code $exit_code"
fi

thread_id="$(jq -sr '[.[] | .session_id? // empty] | .[0] // empty' < "$json_file" 2>/dev/null || true)"

jq -sr '
  def assistant_chunks:
    .[]
    | select(.type == "assistant")
    | .message.content?
    | if type == "array" then .[]?
      elif type == "object" then .
      elif type == "string" and . != "" then {type: "text", text: .}
      else empty end
    | if .type == "text" and (.text // "") != "" then
        .text
      elif .type == "tool_use" and (.name // "") != "" then
        .name as $name
        | if ["Read", "Grep", "Glob", "LS"] | index($name) then
          empty
        else
          "### Non-read-only tool requested: `" + $name + "`"
        end
      else empty end;

  def result_chunks:
    .[]
    | select(.type == "result" and (.result // "") != "")
    | .result;

  [assistant_chunks, result_chunks]
  | reduce .[] as $chunk ([]; if length > 0 and .[-1] == $chunk then . else . + [$chunk] end)
  | .[]
' < "$json_file" 2>/dev/null > "$output_path" || true

if [[ ! -s "$output_path" ]]; then
  echo "(no response from claude)" > "$output_path"
fi

if [[ -n "$thread_id" ]]; then
  echo "session_id=$thread_id"
fi
echo "output_path=$output_path"
