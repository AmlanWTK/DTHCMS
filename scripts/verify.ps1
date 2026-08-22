<#
.SYNOPSIS
  Run every check that CI runs. If this passes locally, CI should pass too.
#>
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path -Parent $PSScriptRoot)
$failed = @()

function Step($name, $block) {
  Write-Host ''
  Write-Host "== $name" -ForegroundColor Cyan
  try {
    & $block
    if ($LASTEXITCODE -ne 0) { throw "exit code $LASTEXITCODE" }
    Write-Host "   PASS" -ForegroundColor Green
  } catch {
    Write-Host "   FAIL: $_" -ForegroundColor Red
    $script:failed += $name
  }
  $global:LASTEXITCODE = 0
}

Step 'Prettier (format check)' { pnpm run format:check }
Step 'ESLint'                  { pnpm run lint }
Step 'TypeScript typecheck'    { pnpm run typecheck }
Step 'TypeScript tests'        { pnpm run test }

Step 'gofmt' {
  Push-Location backend
  $unformatted = (gofmt -l .) -join "`n"
  Pop-Location
  if ($unformatted) { Write-Host $unformatted; throw 'files are not gofmt-formatted' }
}
# The race detector needs cgo and a 64-bit C toolchain, which a Windows workstation
# often does not have. CI runs the race detector on Linux; locally we disable cgo so
# the tests run everywhere. If your gcc is 64-bit you can drop CGO_ENABLED to use -race.
$env:CGO_ENABLED = '0'
Step 'go vet'   { Push-Location backend; go vet ./...;    Pop-Location }
Step 'go build' { Push-Location backend; go build ./...;  Pop-Location }
Step 'go test'  { Push-Location backend; go test ./...;   Pop-Location }
Step 'Architecture and PHI guardrails' {
  Push-Location backend
  go run ./tools/dthclint all
  Pop-Location
}

$python = if (Get-Command python -ErrorAction SilentlyContinue) { 'python' } else { 'python3' }
Step 'Blueprint custody' { & $python scripts/check_custody.py }

Write-Host ''
if ($failed.Count -gt 0) {
  Write-Host ("FAILED: " + ($failed -join ', ')) -ForegroundColor Red
  exit 1
}
Write-Host 'All checks passed.' -ForegroundColor Green
