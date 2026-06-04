$ErrorActionPreference = "Stop"

$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$Verifier = Join-Path $ScriptDir "verify_html_mockup.py"

$python = Get-Command python -ErrorAction SilentlyContinue
if ($python) {
  & $python.Source $Verifier @args
  exit $LASTEXITCODE
}

$py = Get-Command py -ErrorAction SilentlyContinue
if ($py) {
  & $py.Source -3 $Verifier @args
  exit $LASTEXITCODE
}

Write-Error 'Python 3 was not found. Install Python or make python/py -3 available on PATH.'
exit 1
