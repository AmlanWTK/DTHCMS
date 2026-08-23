<#
.SYNOPSIS
  Install workspace dependencies for DTHCMS.
#>
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path -Parent $PSScriptRoot)

Write-Host 'Checking pnpm...' -ForegroundColor Cyan
if (Get-Command pnpm -ErrorAction SilentlyContinue) {
  Write-Host ("  pnpm " + (pnpm --version) + " already installed")
} else {
  Write-Host '  pnpm not found - enabling corepack'
  corepack enable
  if ($LASTEXITCODE -ne 0) {
    Write-Host '  corepack could not write to the Node install directory.' -ForegroundColor Yellow
    Write-Host '  Install pnpm directly instead:  npm install -g pnpm' -ForegroundColor Yellow
    exit 1
  }
  $global:LASTEXITCODE = 0
}

Write-Host 'Installing TypeScript workspace dependencies...' -ForegroundColor Cyan
pnpm install

Write-Host 'Downloading Go module dependencies...' -ForegroundColor Cyan
Push-Location backend
go mod download
Pop-Location

Write-Host ''
Write-Host 'Bootstrap complete. Run .\scripts\verify.ps1 next.' -ForegroundColor Green
