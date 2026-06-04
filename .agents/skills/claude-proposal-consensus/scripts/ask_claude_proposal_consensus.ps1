param(
  [string]$Workspace = (Get-Location).Path,
  [Alias("t")]
  [string]$Task,
  [string]$FeedbackFile,
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
  ask_claude_proposal_consensus.ps1 [options]

Round 1 required:
  -Workspace <path>            Workspace directory to inspect
  -Task, -t <text>             Original user problem
  -Round <n>                   Proposal round number

Resumed rounds required:
  -Workspace <path>            Workspace directory to inspect
  -FeedbackFile <path>         OpenAI agent review feedback markdown
  -Round <n>                   Proposal round number
  -Session <id>                Resume the Claude session for this same requirement

Options:
  -Model <name>                Claude model (default: use Claude CLI default)
  -Effort <level>              Effort: low, medium, high, max (default: max)
  -PermissionMode <mode>       Claude permission mode for new sessions (default: auto);
                               Claude mutation tools are disabled
  -Output, -o <path>           Output markdown path (default: .runtime/<timestamp>.md)
  -Help                        Show this help

Output (on success):
  session_id=<session_id>      Keep only inside this consensus subagent/request
  output_path=<file>           Path to Claude proposal markdown
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
$Session = Trim-Text $Session
$FeedbackFile = Trim-Text $FeedbackFile

if ($Round -lt 1) {
  Fail "-Round must be a positive integer."
}

$feedbackText = ""
if ([string]::IsNullOrWhiteSpace($Session)) {
  if ([string]::IsNullOrWhiteSpace($Task)) {
    Fail "Missing required -Task for round 1."
  }
  if (-not [string]::IsNullOrWhiteSpace($FeedbackFile)) {
    Fail "-FeedbackFile requires -Session."
  }
} else {
  if ([string]::IsNullOrWhiteSpace($FeedbackFile)) {
    Fail "Missing required -FeedbackFile for resumed rounds."
  }
  if (-not (Test-Path -LiteralPath $FeedbackFile -PathType Leaf)) {
    Fail "-FeedbackFile file does not exist: $FeedbackFile"
  }
  $feedbackText = Get-Content -LiteralPath $FeedbackFile -Raw
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

$proposalContract = @"
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
"@

if (-not [string]::IsNullOrWhiteSpace($Session)) {
  $prompt = @"
$proposalContract

Consensus round:
$Round

OpenAI agent review feedback:
$feedbackText

Revise your proposal to address the feedback. Output the complete revised plan.
"@
} else {
  $prompt = @"
$proposalContract

Workspace:
$Workspace

Consensus round:
$Round

Original user problem:
$Task

Inspect the workspace as needed and output the complete initial proposal.
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
