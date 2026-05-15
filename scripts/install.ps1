param(
  [string]$RepoSlug,
  [string]$Version,
  [string]$BinDir,
  [switch]$NoPathUpdate
)

$ErrorActionPreference = "Stop"

function Fail($Message) {
  Write-Error "whale install: $Message"
  exit 1
}

function Resolve-RepoSlug {
  if (-not [string]::IsNullOrWhiteSpace($script:RepoSlug)) {
    return $script:RepoSlug.Trim()
  }
  if (-not [string]::IsNullOrWhiteSpace($env:REPO_SLUG)) {
    return $env:REPO_SLUG.Trim()
  }
  if (-not [string]::IsNullOrWhiteSpace($env:OWNER) -and -not [string]::IsNullOrWhiteSpace($env:REPO)) {
    return "$($env:OWNER.Trim())/$($env:REPO.Trim())"
  }
  return "usewhale/whale"
}

function Resolve-Version($ResolvedRepoSlug) {
  $configured = $script:Version
  if ([string]::IsNullOrWhiteSpace($configured)) {
    $configured = $env:VERSION
  }
  if (-not [string]::IsNullOrWhiteSpace($configured) -and $configured.Trim() -ne "latest") {
    return $configured.Trim()
  }
  Write-Host "Resolving latest whale release..."
  $apiUrl = "https://api.github.com/repos/$ResolvedRepoSlug/releases/latest"
  $release = Invoke-RestMethod -Uri $apiUrl -Headers @{ "User-Agent" = "whale-install" }
  if ([string]::IsNullOrWhiteSpace($release.tag_name)) {
    Fail "failed to resolve latest release tag from $apiUrl"
  }
  return [string]$release.tag_name
}

function Resolve-Arch {
  $arch = $env:PROCESSOR_ARCHITEW6432
  if ([string]::IsNullOrWhiteSpace($arch)) {
    $arch = $env:PROCESSOR_ARCHITECTURE
  }
  switch -Regex ($arch) {
    "^(AMD64|x86_64)$" { return "amd64" }
    "^(ARM64|AARCH64)$" { Fail "windows arm64 release asset is not available yet" }
    default { Fail "unsupported architecture: $arch" }
  }
}

function Resolve-BinDir {
  if (-not [string]::IsNullOrWhiteSpace($script:BinDir)) {
    return $script:BinDir.Trim()
  }
  if (-not [string]::IsNullOrWhiteSpace($env:BIN_DIR)) {
    return $env:BIN_DIR.Trim()
  }
  if ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
    Fail "LOCALAPPDATA is not set; pass -BinDir to choose an install directory"
  }
  return Join-Path $env:LOCALAPPDATA "Programs\whale\bin"
}

function Download-File($Url, $Destination) {
  Invoke-WebRequest -Uri $Url -OutFile $Destination -UseBasicParsing -Headers @{ "User-Agent" = "whale-install" }
}

function Find-Checksum($ManifestPath, $AssetName) {
  foreach ($line in Get-Content -Path $ManifestPath) {
    $trimmed = $line.Trim()
    if ($trimmed -eq "") {
      continue
    }
    $parts = $trimmed -split "\s+", 2
    if ($parts.Length -ne 2) {
      continue
    }
    $name = $parts[1].Trim()
    if ($name -eq $AssetName -or $name.EndsWith("/$AssetName")) {
      return $parts[0].ToLowerInvariant()
    }
  }
  return ""
}

function Verify-Checksum($FilePath, $ExpectedHash) {
  $actual = (Get-FileHash -Path $FilePath -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($actual -ne $ExpectedHash) {
    Fail "checksum mismatch for $(Split-Path -Leaf $FilePath); expected $ExpectedHash, got $actual"
  }
}

function Normalize-PathEntry($PathEntry) {
  $trimChars = [char[]]"\/"
  $trimmed = $PathEntry.Trim().TrimEnd($trimChars)
  if ($trimmed -eq "") {
    return ""
  }
  try {
    return [System.IO.Path]::GetFullPath($trimmed).TrimEnd($trimChars)
  } catch {
    return $trimmed
  }
}

function Test-PathContains($PathValue, $Directory) {
  $wanted = Normalize-PathEntry $Directory
  foreach ($entry in ($PathValue -split ";")) {
    if ((Normalize-PathEntry $entry) -ieq $wanted) {
      return $true
    }
  }
  return $false
}

function Ensure-UserPath($Directory) {
  if ($NoPathUpdate) {
    Write-Host "Skipping PATH update because -NoPathUpdate was set."
    return
  }
  if (Test-PathContains $env:Path $Directory) {
    Write-Host "PATH already contains $Directory"
    return
  }

  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  if ([string]::IsNullOrWhiteSpace($userPath)) {
    $newUserPath = $Directory
  } elseif (Test-PathContains $userPath $Directory) {
    $newUserPath = $userPath
  } else {
    $newUserPath = "$userPath;$Directory"
  }
  [Environment]::SetEnvironmentVariable("Path", $newUserPath, "User")
  $env:Path = "$env:Path;$Directory"
  Write-Host "Added $Directory to your user PATH."
  Write-Host "Open a new terminal if 'whale' is not found in the current one."
}

if ($env:OS -ne "Windows_NT") {
  Fail "scripts/install.ps1 supports native Windows only"
}

$resolvedRepoSlug = Resolve-RepoSlug
$resolvedVersion = Resolve-Version $resolvedRepoSlug
$arch = Resolve-Arch
$installDir = Resolve-BinDir

$assetName = "whale-windows-$arch"
$archiveName = "$assetName.zip"
$baseUrl = "https://github.com/$resolvedRepoSlug/releases/download/$resolvedVersion"
$tempDir = Join-Path ([System.IO.Path]::GetTempPath()) "whale-install-$([System.Guid]::NewGuid().ToString('N'))"

New-Item -ItemType Directory -Path $tempDir -Force | Out-Null
try {
  $archivePath = Join-Path $tempDir $archiveName
  $checksumsPath = Join-Path $tempDir "checksums.txt"
  $extractDir = Join-Path $tempDir "extract"

  Write-Host "Installing whale $resolvedVersion for windows/$arch"
  Write-Host "Downloading $archiveName..."
  try {
    Download-File "$baseUrl/$archiveName" $archivePath
  } catch {
    Fail "could not download $archiveName from $baseUrl; this release may not include a Windows zip yet. Build from source with: go build -o whale.exe ./cmd/whale"
  }

  Write-Host "Downloading checksums.txt..."
  try {
    Download-File "$baseUrl/checksums.txt" $checksumsPath
  } catch {
    Fail "could not download checksums.txt from $baseUrl"
  }

  $expected = Find-Checksum $checksumsPath $archiveName
  if ([string]::IsNullOrWhiteSpace($expected)) {
    Fail "could not find checksum for $archiveName"
  }

  Write-Host "Verifying checksum..."
  Verify-Checksum $archivePath $expected

  Write-Host "Extracting $archiveName..."
  New-Item -ItemType Directory -Path $extractDir -Force | Out-Null
  Expand-Archive -Path $archivePath -DestinationPath $extractDir -Force

  $source = Join-Path $extractDir "$assetName.exe"
  if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
    $matches = @(Get-ChildItem -Path $extractDir -Filter "$assetName.exe" -Recurse -File)
    if ($matches.Count -ne 1) {
      Fail "archive did not contain $assetName.exe"
    }
    $source = $matches[0].FullName
  }

  New-Item -ItemType Directory -Path $installDir -Force | Out-Null
  $target = Join-Path $installDir "whale.exe"
  Copy-Item -LiteralPath $source -Destination $target -Force

  Ensure-UserPath $installDir

  Write-Host "Installed whale $resolvedVersion to $target"
  & $target --version
} finally {
  Remove-Item -LiteralPath $tempDir -Recurse -Force -ErrorAction SilentlyContinue
}
