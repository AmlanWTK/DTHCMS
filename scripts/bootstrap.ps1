<#
.SYNOPSIS
  Install workspace dependencies for DTHCMS.
#>
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path -Parent $PSScriptRoot)

Write-Host 'Enabling corepack (pnpm)...' -ForegroundColor Cyan
corepack enable

Write-Host 'Installing TypeScript workspace dependencies...' -ForegroundColor Cyan
pnpm install

Write-Host 'Downloading Go module dependencies...' -ForegroundColor Cyan
Push-Location backend
go mod download
Pop-Location

Write-Host ''
Write-Host 'Bootstrap complete. Run .\scripts\verify.ps1 next.' -ForegroundColor Green
