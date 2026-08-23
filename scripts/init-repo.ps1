<#
.SYNOPSIS
  CP01 — initialise the DTHCMS git repository.
.DESCRIPTION
  Creates the local git repository, installs the project's git hooks, records the
  blueprint custody hashes, and makes the initial commit. Safe to re-run: it will
  not re-initialise an existing repository or duplicate the initial commit.
#>

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $repoRoot

Write-Host ''
Write-Host 'DTHCMS — repository initialisation (CP01)' -ForegroundColor Cyan
Write-Host '========================================='

# --- 1. Toolchain check ------------------------------------------------------
$missing = @()
foreach ($tool in 'git', 'go', 'node') {
  if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) { $missing += $tool }
}
if ($missing.Count -gt 0) {
  Write-Host ''
  Write-Host ("Missing required tools: " + ($missing -join ', ')) -ForegroundColor Red
  Write-Host 'Install them and run this script again. See README.md > Prerequisites.'
  exit 1
}
Write-Host ("git  : " + (git --version))
Write-Host ("go   : " + (go version))
Write-Host ("node : " + (node --version))

# --- 2. Initialise the repository -------------------------------------------
if (Test-Path (Join-Path $repoRoot '.git')) {
  Write-Host ''
  Write-Host 'Repository already initialised — skipping git init.' -ForegroundColor Yellow
} else {
  git init --initial-branch=main | Out-Null
  Write-Host ''
  Write-Host 'Initialised empty git repository on branch main.' -ForegroundColor Green
}

# --- 3. Identity -------------------------------------------------------------
$userName  = git config user.name
$userEmail = git config user.email
if (-not $userName -or -not $userEmail) {
  Write-Host ''
  Write-Host 'Git identity is not configured. Set it once, globally:' -ForegroundColor Yellow
  Write-Host '  git config --global user.name  "Your Name"'
  Write-Host '  git config --global user.email "you@example.com"'
  Write-Host 'Then run this script again.'
  exit 1
}
Write-Host ("identity: $userName <$userEmail>")

# --- 4. Hooks ----------------------------------------------------------------
git config core.hooksPath .githooks
Write-Host 'Git hooks enabled (.githooks): commit-msg, pre-commit.' -ForegroundColor Green

# --- 5. Install the build and CI files the file bridge cannot write ----------
$ciMarker = Join-Path $repoRoot '.github\workflows\ci.yml'
if (-not (Test-Path $ciMarker)) {
  Write-Host ''
  Write-Host 'Installing build and CI files...' -ForegroundColor Cyan
  & (Join-Path $PSScriptRoot 'install-ci-files.ps1')
} else {
  Write-Host 'Build and CI files already present.' -ForegroundColor Green
}

# --- 6. Remove the stale plan copy, if present -------------------------------
$stale = Join-Path $repoRoot 'docs\DTHCMS_Implementation_Plan_v1.0.md'
if (Test-Path $stale) {
  Remove-Item $stale
  Write-Host 'Removed the earlier plan copy; docs/implementation-plan.md is canonical.' -ForegroundColor Yellow
}

# --- 7. Custody hashes -------------------------------------------------------
$python = if (Get-Command python -ErrorAction SilentlyContinue) { 'python' }
          elseif (Get-Command python3 -ErrorAction SilentlyContinue) { 'python3' }
          else { $null }
if ($python) {
  & $python scripts/check_custody.py --write
  Write-Host 'Blueprint custody hashes recorded in docs/CUSTODY.md.' -ForegroundColor Green
} else {
  Write-Host 'Python not found — skipping custody hashes. Run scripts/check_custody.py --write later.' -ForegroundColor Yellow
}

# --- 8. Initial commit -------------------------------------------------------
$hasCommits = $true
try { git rev-parse HEAD 2>$null | Out-Null } catch { $hasCommits = $false }
if ($LASTEXITCODE -ne 0) { $hasCommits = $false }
$global:LASTEXITCODE = 0

if ($hasCommits) {
  Write-Host ''
  Write-Host 'Repository already has commits — not creating another initial commit.' -ForegroundColor Yellow
} else {
  git add -A
  git commit -m "chore(cp01): establish DTHCMS repository, tooling and CI skeleton" | Out-Null
  Write-Host ''
  Write-Host 'Initial commit created.' -ForegroundColor Green
}

Write-Host ''
Write-Host 'Done. Next steps:' -ForegroundColor Cyan
Write-Host '  1. .\scripts\bootstrap.ps1     install dependencies'
Write-Host '  2. .\scripts\verify.ps1        run the same checks CI runs'
Write-Host '  3. Add the remote when the GitHub repository exists:'
Write-Host '       git remote add origin <url>'
Write-Host '       git push -u origin main'
Write-Host ''
