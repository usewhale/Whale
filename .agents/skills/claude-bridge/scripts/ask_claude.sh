#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage:
  ask_claude.sh <task> [options]
  ask_claude.sh -t <task> [options]

Task input:
  <task>                       First positional argument is the task text
  -t, --task <text>            Alias for positional task
  (stdin)                      Pipe task text via stdin if no arg/flag given

File context (optional, repeatable):
  -f, --file <path>            Priority file path

Options:
  -w, --workspace <path>       Workspace directory (default: current directory)
      --model <name>           Model override
      --effort <level>         Effort: low, medium, high, max (default: max)
      --permission-mode <mode> Claude permission mode (default: auto)
      --dangerously-skip-permissions is always passed to Claude
      --read-only              Analysis only; disables file mutation tools
  -o, --output <path>          Output file path
  -h, --help                   Show this help

Output (on success):
  output_path=<file>           Path to response markdown
USAGE
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "[ERROR] Missing required command: $1" >&2
    exit 1
  fi
}

trim_whitespace() {
  awk 'BEGIN { RS=""; ORS="" } { gsub(/^[ \t\r\n]+|[ \t\r\n]+$/, ""); print }' <<<"$1"
}

to_abs_if_exists() {
  local target="$1"
  if [[ -e "$target" ]]; then
    local dir
    dir="$(cd "$(dirname "$target")" && pwd)"
    echo "$dir/$(basename "$target")"
    return
  fi
  echo "$target"
}

resolve_file_ref() {
  local workspace="$1" raw="$2" cleaned
  cleaned="$(trim_whitespace "$raw")"
  [[ -z "$cleaned" ]] && { echo ""; return; }
  if [[ "$cleaned" =~ ^(.+)#L[0-9]+$ ]]; then cleaned="${BASH_REMATCH[1]}"; fi
  if [[ "$cleaned" =~ ^(.+):[0-9]+(-[0-9]+)?$ ]]; then cleaned="${BASH_REMATCH[1]}"; fi
  if [[ "$cleaned" != /* ]]; then cleaned="$workspace/$cleaned"; fi
  to_abs_if_exists "$cleaned"
}

append_file_refs() {
  local raw="$1" item
  IFS=',' read -r -a items <<< "$raw"
  for item in "${items[@]}"; do
    local trimmed
    trimmed="$(trim_whitespace "$item")"
    [[ -n "$trimmed" ]] && file_refs+=("$trimmed")
  done
}

workspace="${PWD}"
task_text=""
model=""
effort="max"
permission_mode="auto"
read_only=false
output_path=""
file_refs=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    -w|--workspace) workspace="${2:-}"; shift 2 ;;
    -t|--task) task_text="${2:-}"; shift 2 ;;
    -f|--file|--focus) append_file_refs "${2:-}"; shift 2 ;;
    --model) model="${2:-}"; shift 2 ;;
    --effort|--reasoning) effort="${2:-}"; shift 2 ;;
    --permission-mode) permission_mode="${2:-}"; shift 2 ;;
    --read-only) read_only=true; shift ;;
    -o|--output) output_path="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    -*) echo "[ERROR] Unknown option: $1" >&2; usage >&2; exit 1 ;;
    *) if [[ -z "$task_text" ]]; then task_text="$1"; shift; else echo "[ERROR] Unexpected argument: $1" >&2; usage >&2; exit 1; fi ;;
  esac
done

require_cmd claude
require_cmd jq

if [[ ! -d "$workspace" ]]; then
  echo "[ERROR] Workspace does not exist: $workspace" >&2
  exit 1
fi
workspace="$(cd "$workspace" && pwd)"

if [[ -z "$task_text" && ! -t 0 ]]; then
  task_text="$(cat)"
fi
task_text="$(trim_whitespace "$task_text")"

if [[ -z "$task_text" ]]; then
  echo "[ERROR] Request text is empty. Pass a positional arg, --task, or stdin." >&2
  exit 1
fi

if [[ -z "$output_path" ]]; then
  timestamp="$(date -u +"%Y%m%d-%H%M%S")"
  skill_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
  output_path="$skill_dir/.runtime/${timestamp}.md"
fi
mkdir -p "$(dirname "$output_path")"
artifact_base="${output_path%.*}"
json_artifact="${artifact_base}.jsonl"
stderr_artifact="${artifact_base}.stderr"

file_block=""
if (( ${#file_refs[@]} > 0 )); then
  file_block=$'\nPriority files (read these first before making changes):'
  for ref in "${file_refs[@]}"; do
    resolved="$(resolve_file_ref "$workspace" "$ref")"
    [[ -z "$resolved" ]] && continue
    exists_tag="missing"
    [[ -e "$resolved" ]] && exists_tag="exists"
    file_block+=$'\n- '"${resolved} (${exists_tag})"
  done
fi

prompt="$task_text"
if [[ -n "$file_block" ]]; then
  prompt+=$'\n'"$file_block"
fi
if [[ "$read_only" == true ]]; then
  prompt+=$'\n\nRead-only mode: analyze and report only. Do not modify files, create files, delete files, or run commands that mutate the workspace.'
fi

cmd=(claude -p --verbose --output-format stream-json --effort "$effort" --permission-mode "$permission_mode" --dangerously-skip-permissions)
[[ -n "$model" ]] && cmd+=(--model "$model")
if [[ "$read_only" == true ]]; then
  cmd+=(--tools "Read,Grep,Glob,LS")
fi

jq_stderr_artifact="${artifact_base}.jq.stderr"
stderr_file="$stderr_artifact"
json_file="$json_artifact"
prompt_file="$(mktemp)"
trap 'rm -f "$prompt_file"' EXIT
: > "$stderr_file"
: > "$json_file"
rm -f "$jq_stderr_artifact"

printf "%s" "$prompt" > "$prompt_file"

run_claude() {
  (cd "$workspace" && "${cmd[@]}" < "$prompt_file" 2>"$stderr_file")
}

print_progress() {
  local line="$1" text tool
  case "$line" in
    *'"type":"assistant"'*)
      text="$(printf '%s' "$line" | jq -r '[.message.content[]? | select(.type == "text") | .text] | join("\n")' 2>/dev/null | head -1 | cut -c1-120)"
      [[ -n "$text" ]] && echo "[claude] $text" >&2
      ;;
    *'"type":"tool_use"'*|*'"tool_use"'*)
      tool="$(printf '%s' "$line" | jq -r '.. | objects | select(.type? == "tool_use") | select((.name? // "") as $name | ["Read", "Grep", "Glob", "LS"] | index($name) | not) | .name? // empty' 2>/dev/null | head -1 | cut -c1-80)"
      [[ -n "$tool" ]] && echo "[claude] tool $tool" >&2
      ;;
  esac
}

claude_error_summary() {
  [[ -s "$json_file" ]] || return 0
  jq -sr '
    def as_text:
      if . == null then ""
      elif type == "string" then .
      else tojson end;

    .[]
    | select(.type == "system" or .type == "result")
    | [
        (.subtype? // .error? // .error.type? // empty),
        (.message? // .content? // .result? // .error.message? // .error? // empty | as_text)
      ]
    | select((.[0] // "") != "" or (.[1] // "") != "")
    | @tsv
  ' < "$json_file" 2>/dev/null | head -20
}

save_failure_artifacts() {
  local reason="$1" summary
  echo "[ERROR] $reason" >&2
  if [[ -s "$json_file" ]]; then
    echo "[ERROR] Claude raw JSON saved to $json_artifact" >&2
  fi
  if [[ -s "$stderr_file" ]]; then
    echo "[ERROR] Claude stderr saved to $stderr_artifact" >&2
  fi
  if [[ -s "$jq_stderr_artifact" ]]; then
    echo "[ERROR] jq stderr saved to $jq_stderr_artifact" >&2
  fi
  summary="$(claude_error_summary || true)"
  if [[ -n "$summary" ]]; then
    echo "[ERROR] Claude stream error summary:" >&2
    echo "$summary" >&2
  fi
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

set +e
jq -sr '
  def assistant_chunks:
    .[]
    | select(.type == "assistant")
    | .message.content[]?
    | if .type == "text" and (.text // "") != "" then
        .text
      elif .type == "tool_use" and (.name // "") != "" then
        .name as $tool_name
        | if ["Read", "Grep", "Glob", "LS"] | index($tool_name) then
          empty
        else
          "### Tool: `" + $tool_name + "`"
        end
      else empty end;

  def result_chunks:
    .[]
    | select(.type == "result" and (.result // "") != "")
    | .result;

  [assistant_chunks, result_chunks]
  | reduce .[] as $chunk ([]; if length > 0 and .[-1] == $chunk then . else . + [$chunk] end)
  | .[]
' < "$json_file" 2>"$jq_stderr_artifact" > "$output_path"
jq_exit=$?
set -e

if [[ "$jq_exit" -ne 0 ]]; then
  {
    echo "(failed to parse claude response)"
    echo
    echo "Claude exited with code: $exit_code"
    echo "jq exited with code: $jq_exit"
    echo
    echo "Diagnostics:"
    [[ -s "$json_artifact" ]] && echo "- Raw JSON: $json_artifact"
    [[ -s "$stderr_artifact" ]] && echo "- Claude stderr: $stderr_artifact"
    [[ -s "$jq_stderr_artifact" ]] && echo "- jq stderr: $jq_stderr_artifact"
  } > "$output_path"
  save_failure_artifacts "Failed to parse Claude stream JSON with jq"
  exit "$jq_exit"
fi

if [[ ! -s "$output_path" ]]; then
  {
    echo "(no response from claude)"
    echo
    echo "Claude exited with code: $exit_code"
    summary="$(claude_error_summary || true)"
    if [[ -n "$summary" ]]; then
      echo
      echo "Claude stream error summary:"
      echo "$summary"
    fi
    echo
    echo "Diagnostics:"
    [[ -s "$json_artifact" ]] && echo "- Raw JSON: $json_artifact"
    [[ -s "$stderr_artifact" ]] && echo "- Claude stderr: $stderr_artifact"
    [[ -s "$jq_stderr_artifact" ]] && echo "- jq stderr: $jq_stderr_artifact"
  } > "$output_path"
  save_failure_artifacts "Claude produced no readable assistant/result response"
  if [[ "$exit_code" -ne 0 ]]; then
    exit "$exit_code"
  fi
  exit 1
fi

if [[ "$exit_code" -ne 0 ]]; then
  save_failure_artifacts "Claude exited with code $exit_code"
  exit "$exit_code"
fi

echo "output_path=$output_path"
