param(
  [string]$Workspace = (Get-Location).Path,
  [Alias("t")]
  [string]$Task,
  [Alias("p")]
  [string]$Plan,
  [ValidateSet("plan", "file")]
  [string]$InputKind = "plan",
  [string[]]$Target = @(),
  [string]$Verification,
  [string]$VerificationFile,
  [int]$Round = 1,
  [string]$Session,
  [string]$Model = "",
  [string]$Effort = "max",
  [string]$PermissionMode = "auto",
  [Alias("o")]
  [string]$Output,
  [switch]$Help
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Show-Usage {
  @"
Usage:
  ask_claude_consensus.ps1 -InputKind <plan|file> [options]

First round required:
  -Task, -t <text>             Original user request or requirement
  -Plan, -p <text>             Initial Codex plan or file review instructions

Consensus:
  -InputKind <kind>            Internal input kind: plan or file (default: plan)
  -Round <n>                   Review round number (default: 1)
  -Session <id>                Resume the Claude session for this same requirement

Options:
  -Workspace <path>            Workspace directory (default: current directory)
  -Target <path>               Target file/path for file input (repeatable)
  -Verification <text>         Optional verification commands/results
  -VerificationFile <path>     Read optional verification commands/results from file
  -Model <name>                Claude model (default: use Claude CLI default)
  -Effort <level>              Effort: low, medium, high, max (default: max)
  -PermissionMode <mode>       Claude permission mode for new sessions (default: auto);
                               Claude mutation tools are disabled
  -Output, -o <path>           Output markdown path (default: .runtime/<timestamp>.md)
  -Help                        Show this help

Output (on success):
  session_id=<session_id>      Keep only inside this consensus subagent/request
  output_path=<file>           Path to Claude response markdown
"@
}

function Fail([string]$Message) {
  [Console]::Error.WriteLine("[ERROR] $Message")
  exit 1
}

function Require-Command([string]$Name) {
  if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
    Fail "Missing required command: $Name"
  }
}

function Trim-Text([AllowNull()][string]$Value) {
  if ($null -eq $Value) { return "" }
  return $Value.Trim()
}

if ($Help) {
  Show-Usage
  exit 0
}

if ([string]::IsNullOrWhiteSpace($Workspace)) {
  Fail "Workspace path is empty."
}
if (-not (Test-Path -LiteralPath $Workspace -PathType Container)) {
  Fail "Workspace does not exist: $Workspace"
}
$Workspace = (Resolve-Path -LiteralPath $Workspace).Path

$Task = Trim-Text $Task
$Plan = Trim-Text $Plan
if ($Round -lt 1) {
  Fail "-Round must be a positive integer."
}
if (($InputKind -eq "file") -and ($Target.Count -eq 0)) {
  Fail "-Target is required for -InputKind file."
}
if ([string]::IsNullOrWhiteSpace($Session)) {
  if ([string]::IsNullOrWhiteSpace($Task)) {
    Fail "Missing required -Task."
  }
  if ([string]::IsNullOrWhiteSpace($Plan)) {
    Fail "Missing required -Plan."
  }
} elseif ($InputKind -eq "plan") {
  if ([string]::IsNullOrWhiteSpace($Plan)) {
    Fail "Missing required -Plan for resumed plan review."
  }
}
if (-not [string]::IsNullOrWhiteSpace($VerificationFile)) {
  if (-not (Test-Path -LiteralPath $VerificationFile -PathType Leaf)) {
    Fail "Verification file does not exist: $VerificationFile"
  }
  $Verification = Get-Content -LiteralPath $VerificationFile -Raw
}

Require-Command "claude"
Require-Command "jq"

if ([string]::IsNullOrWhiteSpace($Output)) {
  $timestamp = (Get-Date).ToUniversalTime().ToString("yyyyMMdd-HHmmss")
  $scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
  $skillDir = Split-Path -Parent $scriptDir
  $Output = Join-Path $skillDir ".runtime/$timestamp.md"
}
$outputDir = Split-Path -Parent $Output
if (-not [string]::IsNullOrWhiteSpace($outputDir)) {
  New-Item -ItemType Directory -Force -Path $outputDir | Out-Null
}

function Resolve-TargetPath([string]$Path) {
  if (-not (Test-Path -LiteralPath $Path)) {
    Fail "Target path does not exist: $Path"
  }
  return (Resolve-Path -LiteralPath $Path).Path
}

$targetSection = "(none)"
if ($Target.Count -gt 0) {
  $targetSection = ($Target | ForEach-Object { "- $(Resolve-TargetPath $_)" }) -join "`n"
}

$statusContract = @"
Review contract:
- The first non-empty line of your response must be exactly one of: APPROVED, APPROVED_WITH_NOTES, REVISE, BLOCKED.
- Do not wrap the status token in markdown, headings, punctuation, prefixes, or suffixes.
- Review priorities, in order:
  1. Architecture design correctness and architecture option selection.
  2. Execution reliability and verification sufficiency.
- Before deep architecture review, classify the submitted plan or target file changes as trivial, small, medium, or large based on actual scope and risk, not line count.
- Classification guide:
  - trivial: localized text, comments, documentation wording, typo fixes, formatting-only changes, or one-line behavior-preserving edits with no interface or data-flow impact.
  - small: localized implementation change inside one existing module or file, following established patterns, with no public API, dependency, state ownership, cross-module, or compatibility impact.
  - medium: changes that touch multiple files or modules, modify internal interfaces, change non-trivial behavior, affect validation or testing strategy, or introduce meaningful maintenance tradeoffs.
  - large: changes that affect public APIs, dependency direction, shared state ownership, persistence or wire formats, cross-module flow, major abstractions, rollout compatibility, or broad architectural direction.
- For trivial or small changes, perform a lightweight architecture check only:
  1. Confirm the change belongs in the touched module or file.
  2. Confirm it follows existing local patterns.
  3. Confirm it does not alter public APIs, dependency direction, state ownership, cross-module data flow, compatibility, or test isolation.
  4. Do not require architecture alternatives unless one of those boundaries is touched.
- For medium or large changes, review architecture in this order:
  1. Scope and existing constraints: confirm the task boundary, existing architectural constraints, and compatibility limits.
  2. Module responsibilities and boundaries: check placement, ownership, and boundary clarity.
  3. Data flow and state ownership: check data sources, destinations, state ownership, transformations, and control flow.
  4. Interfaces, dependencies, and compatibility: check public API changes, dependency direction, coupling, compatibility risk, and test isolation risk.
  5. Abstraction fit: check consistency with the existing abstraction style and avoid premature abstraction, duplicate abstraction, and public interface pollution.
  6. Maintainability and extensibility consequences: check long-term maintenance or extension risk.
  7. In-scope architecture alternatives: check for an architecture that still serves the user request, stays inside the current task scope, and is better overall on complexity, consistency, maintenance cost, compatibility risk, verification difficulty, public API exposure, and dependency expansion.
- If a clearly better in-scope architecture exists and its benefit is enough to justify the change cost, require the Codex subagent to adopt it instead of approving a merely executable approach.
- If a trivial or small change touches or risks touching module boundaries, public interfaces, dependencies, state ownership, data flow, compatibility, or test isolation, escalate to the full medium or large architecture review.
- Do not escalate a trivial or small change into full architecture review solely to find optional improvements.
- Only after the appropriate lightweight or full architecture review is acceptable, review execution steps, implementation detail, validation coverage, and rollout risk.
- After the status token, immediately output:
  Risk classification: <trivial|small|medium|large>
  Classification reason: <one concise sentence>
  Architecture review mode: <lightweight|full>
- The Classification reason must briefly justify the chosen risk level and review mode.
- For full review, the Classification reason or the next brief sentence must explicitly name the main boundary risk source such as API, dependency, state ownership, data flow, compatibility, or test isolation.
- For lightweight review, the Classification reason should say the change is localized and does not touch architecture boundaries.
- Return APPROVED only when a trivial or small change passes the lightweight architecture check and important execution and verification risks are covered, or when a medium or large change has a sound architecture direction, no clearly better in-scope architecture alternative should be adopted, and the important execution and verification risks are covered. Keep APPROVED concise; for trivial or small changes, do not expand into long architecture analysis beyond the minimal auditable block and a brief rationale unless the change required full review.
- Return APPROVED_WITH_NOTES only for low-risk architecture caveats, architecture improvements that can be explicitly deferred, or execution-level cleanup that the Codex subagent should incorporate into the plan or target files when appropriate, or explicitly defer before another review round. Do not use APPROVED_WITH_NOTES for purely optional suggestions that require no follow-up. For plan input, the subagent will send the complete updated plan in the next round.
- Return REVISE if a trivial or small change is in the wrong module, violates existing local patterns, actually touches architecture boundaries without accounting for them, or has an execution/verification issue that the Codex subagent can fix without asking the user. For medium or large changes, return REVISE if there is an architecture design problem, a clearly better in-scope architecture alternative that should be adopted, or an execution/verification issue that the Codex subagent can fix without asking the user. Architecture issues take priority even when the current execution steps are complete. REVISE feedback must be actionable. For file or document review, identify the affected location, the problem, and the expected result. If the reason for REVISE is that a seemingly trivial or small change actually triggered full review, state which boundary risk caused the escalation. For plan input, the subagent will send the complete updated plan in the next round.
- Return BLOCKED if architecture judgment or reliable execution needs a missing user decision, inaccessible required context, or resolution of a contradiction. Do not only say that information is insufficient; identify the specific missing decision, inaccessible context, or contradiction and explain why it blocks a reliable judgment.
- Separate blocking concerns from non-blocking notes.
- Make REVISE and APPROVED_WITH_NOTES feedback concrete enough for a Codex subagent to turn into edits, deferrals, or verification steps.
- Do not edit files. Do not propose broad refactors or unrelated improvements.
"@

$verificationSection = ""
if (-not [string]::IsNullOrWhiteSpace($Verification)) {
  $verificationSection = @"

Verification:
$Verification
"@
}

if (-not [string]::IsNullOrWhiteSpace($Session) -and $InputKind -eq "plan") {
  $prompt = @"
You are Claude reviewing Codex work in read-only mode.

$statusContract

Consensus round:
$Round

Updated plan:
$Plan
"@
} elseif (-not [string]::IsNullOrWhiteSpace($Session) -and $InputKind -eq "file") {
  $prompt = @"
You are Claude reviewing Codex work in read-only mode.

$statusContract

Consensus round:
$Round

Target files/paths:
$targetSection

The target files have changed. Based on the current file contents and the prior session context, review them again.
"@
} else {
  $prompt = @"
You are Claude reviewing Codex work in read-only mode.

$statusContract
- Use the resumed session context from prior rounds when it already contains the needed code, configuration, tests, and documentation.
- On round 1, build enough workspace context to review reliably.
- For plan input, review the current Codex plan.
- For file input, the target files/paths are the source of truth; inspect them as needed and identify the affected location, problem, and expected result when requesting changes.
- Choose any additional relevant paths yourself from the user request, input kind, targets, and current plan.

Workspace:
$Workspace

Consensus round:
$Round

Input kind:
$InputKind

Target files/paths:
$targetSection

Original user request:
$Task

Initial Codex plan or file review instructions:
$Plan$verificationSection
"@
}

$claudeArgs = @("-p", "--verbose", "--output-format", "stream-json", "--effort", $Effort, "--tools", "Read,Grep,Glob,LS")
if ([string]::IsNullOrWhiteSpace($Session)) {
  $claudeArgs += @("--permission-mode", $PermissionMode)
} else {
  $claudeArgs += @("--resume", $Session)
}
if (-not [string]::IsNullOrWhiteSpace($Model)) {
  $claudeArgs += @("--model", $Model)
}

$stderrFile = [System.IO.Path]::GetTempFileName()
$jsonFile = [System.IO.Path]::GetTempFileName()
$promptFile = [System.IO.Path]::GetTempFileName()

try {
  Set-Content -LiteralPath $promptFile -Value $prompt -NoNewline
  Push-Location $Workspace
  try {
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = "claude"
    foreach ($arg in $claudeArgs) { [void]$psi.ArgumentList.Add($arg) }
    $psi.RedirectStandardInput = $true
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.UseShellExecute = $false
    $process = [System.Diagnostics.Process]::Start($psi)
    $process.StandardInput.Write($prompt)
    $process.StandardInput.Close()

    while (-not $process.StandardOutput.EndOfStream) {
      $line = $process.StandardOutput.ReadLine()
      if ([string]::IsNullOrWhiteSpace($line)) { continue }
      $cleaned = $line.Replace("`r", "").Replace([char]4, "")
      if (-not $cleaned.StartsWith("{")) { continue }
      Add-Content -LiteralPath $jsonFile -Value $cleaned
      if ($cleaned -like '*"type":"system"*' -and $cleaned -like '*"session_id"*') {
        $sid = $cleaned | jq -r '.session_id // empty' 2>$null
        if (-not [string]::IsNullOrWhiteSpace($sid)) {
          [Console]::Error.WriteLine("[claude] session $sid")
        }
      }
    }

    $stderrText = $process.StandardError.ReadToEnd()
    $process.WaitForExit()
    if (-not [string]::IsNullOrWhiteSpace($stderrText)) {
      [Console]::Error.Write($stderrText)
    }
    if ($process.ExitCode -ne 0 -and -not ((Test-Path -LiteralPath $jsonFile) -and ((Get-Item -LiteralPath $jsonFile).Length -gt 0))) {
      Fail "Claude exited with code $($process.ExitCode)"
    }
  } finally {
    Pop-Location
  }

  $threadId = Get-Content -LiteralPath $jsonFile | jq -sr '[.[] | .session_id? // empty] | .[0] // empty' 2>$null

  Get-Content -LiteralPath $jsonFile | jq -sr '
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
            "### Tool: `" + $name + "`"
          end
        else empty end;

    def result_chunks:
      .[]
      | select(.type == "result" and (.result // "") != "")
      | .result;

    [assistant_chunks, result_chunks]
    | reduce .[] as $chunk ([]; if length > 0 and .[-1] == $chunk then . else . + [$chunk] end)
    | .[]
  ' 2>$null | Set-Content -LiteralPath $Output

  if (-not (Test-Path -LiteralPath $Output) -or (Get-Item -LiteralPath $Output).Length -eq 0) {
    Set-Content -LiteralPath $Output -Value "(no response from claude)"
  }

  if (-not [string]::IsNullOrWhiteSpace($threadId)) {
    "session_id=$threadId"
  }
  "output_path=$Output"
} finally {
  Remove-Item -LiteralPath $stderrFile, $jsonFile, $promptFile -Force -ErrorAction SilentlyContinue
}
