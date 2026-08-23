<#
.SYNOPSIS
  Run every check that CI runs. If this passes locally, CI should pass too.
.PARAMETER SkipGoLint
  Skip golangci-lint. It is the slowest check on a cold cache; skipping it means CI may
  find something you did not.
#>
param([switch]$SkipGoLint)

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

# A .ps1 that does not parse is a script nobody can run, and that failure only appears at
# the moment someone needs it. PowerShell can parse a file without executing it, so every
# script in the repo is checked here before anything else runs.
#
# The BOM check is not cosmetic. Windows PowerShell 5.1 reads a file with no byte-order
# mark using the system ANSI code page, so a UTF-8 em-dash arrives as three CP1252
# characters, the last of which is a smart quote - and PowerShell treats smart quotes as
# string delimiters. Inside a double-quoted string that terminates it early and the rest
# of the file parses as nonsense, with an error pointing at a line that is entirely fine.
Step 'PowerShell scripts parse' {
  $problems = @()

  Get-ChildItem -Path $PSScriptRoot -Filter *.ps1 | ForEach-Object {
    $bytes = [IO.File]::ReadAllBytes($_.FullName)
    $hasBom = $bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF
    if (-not $hasBom) {
      $problems += "$($_.Name): no UTF-8 BOM - Windows PowerShell will misread every non-ASCII character"
    }

    $errors = $null
    [void][System.Management.Automation.Language.Parser]::ParseFile($_.FullName, [ref]$null, [ref]$errors)
    foreach ($e in $errors) {
      $problems += "$($_.Name):$($e.Extent.StartLineNumber) $($e.Message)"
    }
  }

  if ($problems.Count -gt 0) {
    $problems | ForEach-Object { Write-Host "   $_" -ForegroundColor Red }
    throw "$($problems.Count) problem(s) in PowerShell scripts"
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

# The database tests skip themselves without this, which would quietly turn the
# append-only guarantee into an untested claim. Point it at the local stack unless the
# caller has already chosen a server.
if (-not $env:DTHCMS_TEST_POSTGRES_URL) {
  $pgPort = if ($env:POSTGRES_PORT) { $env:POSTGRES_PORT } else { '5433' }
  $env:DTHCMS_TEST_POSTGRES_URL =
    "postgres://dthcms:dthcms_local_only@127.0.0.1:$pgPort/postgres?sslmode=disable"
}

Step 'go vet'   { Push-Location backend; go vet ./...;    Pop-Location }
Step 'go build' { Push-Location backend; go build ./...;  Pop-Location }
Step 'go test'  { Push-Location backend; go test ./...;   Pop-Location }
Step 'Architecture and PHI guardrails' {
  Push-Location backend
  go run ./tools/dthclint all
  Pop-Location
}

# sqlc is a downloaded binary rather than a Go module, so it may not be installed. CI
# always runs this check; locally a missing sqlc is reported as a skip rather than a
# failure, because it is not worth blocking a formatting fix on.
if (Get-Command sqlc -ErrorAction SilentlyContinue) {
  Step 'sqlc (generated code is current)' {
    Push-Location backend
    sqlc diff
    Pop-Location
  }
} else {
  Write-Host ''
  Write-Host '== sqlc (generated code is current)' -ForegroundColor Cyan
  Write-Host '   SKIPPED: sqlc is not installed. CI runs this check.' -ForegroundColor Yellow
  Write-Host '   Install: https://docs.sqlc.dev/en/latest/overview/install.html'
}

# The same linter and version CI runs. The first run downloads and builds it, which takes
# a minute; afterwards it is cached. Skip with:  .\scripts\verify.ps1 -SkipGoLint
if (-not $SkipGoLint) {
  Step 'golangci-lint' {
    Push-Location backend
    # v1.64 or newer is required. Earlier releases are built with Go 1.23 and their type
    # checker rejects the //go:build go1.24 files inside golang.org/x/net and x/sys, which
    # the OpenTelemetry dependency tree pulls in — reporting them as errors in our build.
    go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8 run ./...
    Pop-Location
  }
}

# Observability is checked separately by .\scripts\check-observability.ps1, which needs
# the local stack running and is therefore not part of the CI-equivalent run.

$python = if (Get-Command python -ErrorAction SilentlyContinue) { 'python' } else { 'python3' }
Step 'Blueprint custody' { & $python scripts/check_custody.py }

Write-Host ''
if ($failed -contains 'go test') {
  Write-Host 'Note: the database tests need the local stack running (.\scripts\dev.ps1 up).' -ForegroundColor Yellow
}
if ($failed.Count -gt 0) {
  Write-Host ("FAILED: " + ($failed -join ', ')) -ForegroundColor Red
  exit 1
}
Write-Host 'All checks passed.' -ForegroundColor Green
