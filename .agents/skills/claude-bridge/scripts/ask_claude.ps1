#!/usr/bin/env powershell
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [string]$Task,

    [Alias('t')]
    [string]$TaskText,

    [Alias('w')]
    [string]$Workspace = (Get-Location).Path,

    [Alias('f')]
    [string[]]$File,

    [string]$Model,

    [ValidateSet('low', 'medium', 'high', 'max')]
    [string]$Effort = 'max',

    [string]$PermissionMode = 'auto',

    [switch]$ReadOnly,

    [Alias('o')]
    [string]$Output,

    [switch]$Help
)

$ErrorActionPreference = 'Stop'

function Show-Usage {
    @'
Usage:
  ask_claude.ps1 <task> [options]
  ask_claude.ps1 -Task <task> [options]

Task input:
  <task>                       First positional argument is the task text
  -Task, -t <text>             Alias for positional task

File context (optional, repeatable):
  -File, -f <path>             Priority file path

Options:
  -Workspace, -w <path>        Workspace directory (default: current directory)
  -Model <name>                Model override
  -Effort <level>              Effort: low, medium, high, max (default: max)
  -PermissionMode <mode>       Claude permission mode (default: auto)
                                --dangerously-skip-permissions is always passed to Claude
  -ReadOnly                    Analysis only; disables file mutation tools
  -Output, -o <path>           Output file path
  -Help                        Show this help

Output (on success):
  output_path=<file>           Path to response markdown
'@
}

function Test-Command {
    param([string]$Name)
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        Write-Error "[ERROR] Missing required command: $Name"
        exit 1
    }
}

function Trim-Whitespace {
    param([string]$Text)
    if ([string]::IsNullOrEmpty($Text)) { return '' }
    return $Text.Trim()
}

function Resolve-FileRef {
    param([string]$Workspace, [string]$RawPath)
    $cleaned = Trim-Whitespace $RawPath
    if ([string]::IsNullOrWhiteSpace($cleaned)) { return '' }
    $cleaned = $cleaned -replace '#L\d+$', ''
    $cleaned = $cleaned -replace ':\d+(-\d+)?$', ''
    if (-not [System.IO.Path]::IsPathRooted($cleaned)) {
        $cleaned = Join-Path $Workspace $cleaned
    }
    if (Test-Path $cleaned) {
        return (Resolve-Path $cleaned -ErrorAction SilentlyContinue).Path
    }
    return $cleaned
}

function Write-File-NoBOM {
    param([string]$Path, [string]$Content)
    $utf8NoBom = New-Object System.Text.UTF8Encoding $false
    [System.IO.File]::WriteAllText($Path, $Content, $utf8NoBom)
}

if ($Help) {
    Show-Usage
    exit 0
}

Test-Command 'claude'

if ([string]::IsNullOrEmpty($Task) -and -not [string]::IsNullOrEmpty($TaskText)) {
    $Task = $TaskText
}

if (-not (Test-Path $Workspace -PathType Container)) {
    Write-Error "[ERROR] Workspace does not exist: $Workspace"
    exit 1
}
$Workspace = (Resolve-Path $Workspace).Path

$Task = Trim-Whitespace $Task
if ([string]::IsNullOrEmpty($Task)) {
    Write-Error "[ERROR] Request text is empty. Pass a positional arg or -Task."
    exit 1
}

if ($ReadOnly) {
    $Task += "`n`nRead-only mode: analyze and report only. Do not modify files, create files, delete files, or run commands that mutate the workspace."
}

if ([string]::IsNullOrEmpty($Output)) {
    $timestamp = (Get-Date).ToUniversalTime().ToString('yyyyMMdd-HHmmss')
    $skillDir = Split-Path $PSScriptRoot -Parent
    $runtimeDir = Join-Path $skillDir '.runtime'
    if (-not (Test-Path $runtimeDir)) {
        New-Item -ItemType Directory -Path $runtimeDir -Force | Out-Null
    }
    $Output = Join-Path $runtimeDir "$timestamp.md"
}

$fileBlock = ''
if ($File -and $File.Count -gt 0) {
    $fileBlock = "`nPriority files (read these first before making changes):"
    foreach ($ref in $File) {
        $resolved = Resolve-FileRef -Workspace $Workspace -RawPath $ref
        if (-not [string]::IsNullOrEmpty($resolved)) {
            $existsTag = if (Test-Path $resolved) { 'exists' } else { 'missing' }
            $fileBlock += "`n- $resolved ($existsTag)"
        }
    }
}

$prompt = $Task
if (-not [string]::IsNullOrEmpty($fileBlock)) {
    $prompt += $fileBlock
}

$claudeArgs = @('-p', '--verbose', '--output-format', 'stream-json', '--effort', $Effort, '--permission-mode', $PermissionMode, '--dangerously-skip-permissions')
if (-not [string]::IsNullOrEmpty($Model)) {
    $claudeArgs += '--model', $Model
}
if ($ReadOnly) {
    $claudeArgs += '--tools', 'Read,Grep,Glob,LS'
}

$tempDir = [System.IO.Path]::GetTempPath()
$guid = [guid]::NewGuid().ToString()
$stderrFile = Join-Path $tempDir "claude_stderr_$guid.txt"
$jsonFile = Join-Path $tempDir "claude_json_$guid.txt"
$outputDirForArtifacts = Split-Path $Output -Parent
$outputBaseName = [System.IO.Path]::GetFileNameWithoutExtension($Output)
if ([string]::IsNullOrEmpty($outputBaseName)) {
    $outputBaseName = [System.IO.Path]::GetFileName($Output)
}
$artifactBase = if ([string]::IsNullOrEmpty($outputDirForArtifacts)) { $outputBaseName } else { Join-Path $outputDirForArtifacts $outputBaseName }
$jsonArtifact = "$artifactBase.jsonl"
$stderrArtifact = "$artifactBase.stderr"

function ConvertTo-CompactText {
    param($Value)
    if ($null -eq $Value) { return '' }
    if ($Value -is [string]) { return $Value }
    try {
        return ($Value | ConvertTo-Json -Compress -Depth 12)
    } catch {
        return [string]$Value
    }
}

function Get-ClaudeErrorSummary {
    param([string]$JsonText)
    if ([string]::IsNullOrWhiteSpace($JsonText)) { return @() }
    $summaries = @()
    $jsonLines = $JsonText -split "`n" | Where-Object { $_.Trim() -and $_.TrimStart().StartsWith('{') }
    foreach ($line in $jsonLines) {
        if ($summaries.Count -ge 20) { break }
        try {
            $obj = $line | ConvertFrom-Json -ErrorAction SilentlyContinue
            if (-not $obj) { continue }
            if ($obj.type -ne 'system' -and $obj.type -ne 'result') { continue }

            $kind = ''
            if ($obj.PSObject.Properties.Name -contains 'subtype') { $kind = ConvertTo-CompactText $obj.subtype }
            elseif ($obj.PSObject.Properties.Name -contains 'error') { $kind = ConvertTo-CompactText $obj.error }

            $message = ''
            foreach ($name in @('message', 'content', 'result')) {
                if ($obj.PSObject.Properties.Name -contains $name) {
                    $message = ConvertTo-CompactText $obj.$name
                    if (-not [string]::IsNullOrWhiteSpace($message)) { break }
                }
            }
            if ([string]::IsNullOrWhiteSpace($message) -and $obj.PSObject.Properties.Name -contains 'error') {
                $err = $obj.error
                if ($err -and ($err.PSObject.Properties.Name -contains 'message')) {
                    $message = ConvertTo-CompactText $err.message
                }
            }

            if (-not [string]::IsNullOrWhiteSpace($kind) -or -not [string]::IsNullOrWhiteSpace($message)) {
                $summaries += "$kind`t$message"
            }
        } catch {}
    }
    return $summaries
}

function Save-FailureArtifacts {
    param([string]$Reason, [string]$JsonText, [string]$StderrText)
    [Console]::Error.WriteLine("[ERROR] $Reason")
    if (-not [string]::IsNullOrWhiteSpace($JsonText)) {
        Write-File-NoBOM -Path $jsonArtifact -Content $JsonText
        [Console]::Error.WriteLine("[ERROR] Claude raw JSON saved to $jsonArtifact")
    }
    if (-not [string]::IsNullOrWhiteSpace($StderrText)) {
        Write-File-NoBOM -Path $stderrArtifact -Content $StderrText
        [Console]::Error.WriteLine("[ERROR] Claude stderr saved to $stderrArtifact")
    }
    $summary = @(Get-ClaudeErrorSummary $JsonText)
    if ($summary.Count -gt 0) {
        [Console]::Error.WriteLine("[ERROR] Claude stream error summary:")
        foreach ($line in $summary) {
            [Console]::Error.WriteLine($line)
        }
    }
}

try {
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    if ($IsWindows -or $PSVersionTable.PSVersion.Major -le 5) {
        $psi.FileName = 'cmd.exe'
        $psi.Arguments = '/c claude ' + ($claudeArgs -join ' ')
    } else {
        $psi.FileName = 'claude'
        $psi.Arguments = $claudeArgs -join ' '
    }
    $psi.WorkingDirectory = $Workspace
    $psi.UseShellExecute = $false
    $psi.RedirectStandardInput = $true
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.CreateNoWindow = $true
    $psi.StandardOutputEncoding = [System.Text.Encoding]::UTF8
    $psi.StandardErrorEncoding = [System.Text.Encoding]::UTF8

    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $psi

    $jsonOutput = New-Object System.Text.StringBuilder
    $stderrOutput = New-Object System.Text.StringBuilder

    $stdOutAction = {
        param([object]$sender, [System.Diagnostics.DataReceivedEventArgs]$e)
        if ($e.Data) {
            $line = $e.Data -replace "`r", ''
            $line = $line -replace [char]4, ''
            if ($line.TrimStart().StartsWith('{')) {
                [System.Threading.Monitor]::Enter($Event.MessageData)
                try { $Event.MessageData.AppendLine($line) | Out-Null } finally { [System.Threading.Monitor]::Exit($Event.MessageData) }
                try {
                    $json = $line | ConvertFrom-Json -ErrorAction SilentlyContinue
                    if ($json.type -eq 'assistant' -and $json.message.content) {
                        $texts = @($json.message.content | Where-Object { $_.type -eq 'text' } | ForEach-Object { $_.text })
                        if ($texts.Count -gt 0) {
                            $preview = ($texts -join "`n").Split("`n")[0]
                            if ($preview.Length -gt 120) { $preview = $preview.Substring(0, 120) }
                            Write-Host "[claude] $preview" -ForegroundColor Gray
                        }
                    }
                } catch {}
            }
        }
    }

    $stdErrAction = {
        param([object]$sender, [System.Diagnostics.DataReceivedEventArgs]$e)
        if ($e.Data) {
            [System.Threading.Monitor]::Enter($Event.MessageData)
            try { $Event.MessageData.AppendLine($e.Data) | Out-Null } finally { [System.Threading.Monitor]::Exit($Event.MessageData) }
            Write-Host $e.Data -ForegroundColor Yellow
        }
    }

    $stdOutEvent = Register-ObjectEvent -InputObject $process -EventName OutputDataReceived -Action $stdOutAction -MessageData $jsonOutput
    $stdErrEvent = Register-ObjectEvent -InputObject $process -EventName ErrorDataReceived -Action $stdErrAction -MessageData $stderrOutput

    try {
        $process.Start() | Out-Null
        $process.BeginOutputReadLine()
        $process.BeginErrorReadLine()
        $process.StandardInput.Write($prompt)
        $process.StandardInput.Close()
        $process.WaitForExit()
        $exitCode = $process.ExitCode
    } finally {
        Unregister-Event -SourceIdentifier $stdOutEvent.Name -ErrorAction SilentlyContinue
        Unregister-Event -SourceIdentifier $stdErrEvent.Name -ErrorAction SilentlyContinue
        $process.Dispose()
    }

    $jsonText = $jsonOutput.ToString()
    $stderrText = $stderrOutput.ToString()
    Write-File-NoBOM -Path $jsonFile -Content $jsonText
    if (-not [string]::IsNullOrWhiteSpace($stderrText)) {
        Write-Host $stderrText -ForegroundColor Yellow
    }
    $outputContent = @()
    $jsonLines = $jsonText -split "`n" | Where-Object { $_.Trim() -and $_.TrimStart().StartsWith('{') }
    foreach ($line in $jsonLines) {
        try {
            $obj = $line | ConvertFrom-Json -ErrorAction SilentlyContinue
            if (-not $obj) { continue }
            if ($obj.type -eq 'assistant' -and $obj.message.content) {
                foreach ($content in $obj.message.content) {
                    if ($content.type -eq 'text' -and $content.text) {
                        $outputContent += $content.text
                    }
                    if ($content.type -eq 'tool_use' -and $content.name) {
                        if (@('Read', 'Grep', 'Glob', 'LS') -contains $content.name) {
                            continue
                        }
                        $outputContent += "### Tool: ``$($content.name)``"
                    }
                }
            }
            if ($obj.type -eq 'result' -and $obj.result) {
                if ($outputContent.Count -gt 0 -and $outputContent[-1].Trim() -eq $obj.result.Trim()) {
                    continue
                }
                $outputContent += $obj.result
            }
        } catch {}
    }

    $outputDir = Split-Path $Output -Parent
    if (-not (Test-Path $outputDir)) {
        New-Item -ItemType Directory -Path $outputDir -Force | Out-Null
    }
    if ($outputContent.Count -gt 0) {
        Write-File-NoBOM -Path $Output -Content (($outputContent | Where-Object { $_ }) -join "`n")
    } else {
        $summary = @(Get-ClaudeErrorSummary $jsonText)
        $noResponseContent = "(no response from claude)"
        if ($summary.Count -gt 0) {
            $noResponseContent += "`n`nClaude stream error summary:`n"
            $noResponseContent += ($summary -join "`n")
        }
        Write-File-NoBOM -Path $Output -Content $noResponseContent
        Save-FailureArtifacts -Reason "Claude produced no readable assistant/result response" -JsonText $jsonText -StderrText $stderrText
        if ($exitCode -ne 0) {
            exit $exitCode
        }
        exit 1
    }

    if ($exitCode -ne 0) {
        Save-FailureArtifacts -Reason "Claude exited with code $exitCode" -JsonText $jsonText -StderrText $stderrText
        exit $exitCode
    }

    Write-Output "output_path=$Output"
} finally {
    Remove-Item -Path $stderrFile -Force -ErrorAction SilentlyContinue
    Remove-Item -Path $jsonFile -Force -ErrorAction SilentlyContinue
}
