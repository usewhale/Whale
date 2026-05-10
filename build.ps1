# Build script for Whale on Windows
# Usage: .\build.ps1 [-Target <target>] [-Version <version>] [-Bin <path>]
#
# Targets: build, fmt-check, vet, test, test-evals, test-tui, run, clean
# Default: build

param(
    [string]$Target = "build",
    [string]$Version = "dev",
    [string]$Bin = "bin/whale.exe"
)

$ErrorActionPreference = "Stop"
$GoCache = Join-Path $PSScriptRoot ".gocache"
$LdFlags = "-X github.com/usewhale/whale/internal/build.Version=$Version"

function Build {
    $binDir = Split-Path $Bin -Parent
    if ($binDir -and !(Test-Path $binDir)) { New-Item -ItemType Directory -Path $binDir -Force | Out-Null }
    if (!(Test-Path $GoCache)) { New-Item -ItemType Directory -Path $GoCache -Force | Out-Null }
    $env:GOCACHE = $GoCache
    go build -ldflags $LdFlags -o $Bin ./cmd/whale
    Write-Host "Built $Bin"
}

function FmtCheck {
    $files = Get-ChildItem -Recurse -Filter "*.go" -Exclude ".gocache" | Where-Object { $_.FullName -notmatch "\\.gocache\\" }
    $unformatted = @()
    foreach ($f in $files) {
        $content = Get-Content $f.FullName -Raw
        $formatted = ($content | gofmt -s 2>&1)
        if ($LASTEXITCODE -ne 0 -or $content -ne $formatted) {
            $unformatted += $f.FullName
        }
    }
    if ($unformatted.Count -gt 0) {
        Write-Host "gofmt needs to be run on:"
        $unformatted | ForEach-Object { Write-Host $_ }
        exit 1
    }
    Write-Host "All Go files properly formatted"
}

function Vet {
    if (!(Test-Path $GoCache)) { New-Item -ItemType Directory -Path $GoCache -Force | Out-Null }
    $env:GOCACHE = $GoCache
    go vet ./...
}

function Test {
    if (!(Test-Path $GoCache)) { New-Item -ItemType Directory -Path $GoCache -Force | Out-Null }
    $env:GOCACHE = $GoCache
    go test ./...
}

function TestEvals {
    if (!(Test-Path $GoCache)) { New-Item -ItemType Directory -Path $GoCache -Force | Out-Null }
    $env:GOCACHE = $GoCache
    go test ./internal/evals
}

function TestTui {
    if (!(Test-Path $GoCache)) { New-Item -ItemType Directory -Path $GoCache -Force | Out-Null }
    $env:GOCACHE = $GoCache
    go test ./internal/tui ./internal/tui/render
}

function Run {
    Build
    & $Bin
}

function Clean {
    if (Test-Path "bin") { Remove-Item -Recurse -Force "bin" }
    if (Test-Path ".gocache") { Remove-Item -Recurse -Force ".gocache" }
    Write-Host "Cleaned"
}

switch ($Target) {
    "build"       { Build }
    "fmt-check"   { FmtCheck }
    "vet"         { Vet }
    "test"        { Test }
    "test-evals"  { TestEvals }
    "test-tui"    { TestTui }
    "run"         { Run }
    "clean"       { Clean }
    "help"        {
        Write-Host "Targets:"
        Write-Host "  build       Build $Bin"
        Write-Host "  fmt-check   Check Go formatting with gofmt"
        Write-Host "  vet         Run go vet"
        Write-Host "  test        Run all offline Go tests"
        Write-Host "  test-evals  Run the eval-focused subset"
        Write-Host "  test-tui    Run the TUI-focused subset"
        Write-Host "  run         Build and run the TUI"
        Write-Host "  clean       Remove build output and local Go cache"
        Write-Host ""
        Write-Host "Variables:"
        Write-Host "  -Version v0.1.0   Inject version into the binary"
        Write-Host "  -Bin path          Override output binary path"
    }
    default {
        Write-Host "Unknown target: $Target. Use 'help' to see available targets."
        exit 1
    }
}
