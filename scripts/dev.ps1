<#
.SYNOPSIS
  Control the DTHCMS local development stack.
.DESCRIPTION
  The Windows equivalent of the Makefile targets. Everything the stack needs runs in
  Docker: Postgres, Redis, MinIO, the mock AI/OCR service and mail capture.
.PARAMETER Command
  up       start the stack and wait until every service is healthy
  down     stop the stack, keeping data
  reset    stop the stack and DELETE all local data, then start fresh
  status   show what is running
  logs     follow logs (optionally for one service)
  observability  re-provision dashboards and alert rules, then verify them
  migrate  apply migrations, verify invariants, create the local database roles
  migrate-status  show which migrations have been applied
  migrate-verify  check checksums and invariants without applying anything
  psql     open a psql shell on the local database
  redis    open a redis-cli shell
  urls     print the local service addresses
.EXAMPLE
  .\scripts\dev.ps1 up
  .\scripts\dev.ps1 logs postgres
  .\scripts\dev.ps1 reset
#>
param(
  [Parameter(Position = 0)]
  [ValidateSet('up', 'down', 'reset', 'status', 'logs', 'migrate', 'migrate-status',
    'migrate-verify', 'observability', 'psql', 'redis', 'urls')]
  [string]$Command = 'up',

  [Parameter(Position = 1)]
  [string]$Service
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $repoRoot

function Require-Go {
  if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host ''
    Write-Host 'Go is not installed or not on PATH.' -ForegroundColor Red
    Write-Host 'Install Go 1.23 or newer: https://go.dev/dl/'
    exit 1
  }
}

function Require-Docker {
  if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    Write-Host ''
    Write-Host 'Docker is not installed or not on PATH.' -ForegroundColor Red
    Write-Host 'Install Docker Desktop with the WSL 2 backend: https://docs.docker.com/desktop/install/windows-install/'
    exit 1
  }
  docker info 2>&1 | Out-Null
  if ($LASTEXITCODE -ne 0) {
    Write-Host ''
    Write-Host 'Docker is installed but the engine is not running. Start Docker Desktop and try again.' -ForegroundColor Red
    exit 1
  }
  $global:LASTEXITCODE = 0
}

function Show-Urls {
  Write-Host ''
  Write-Host 'Local services' -ForegroundColor Cyan
  Write-Host '--------------'
  Write-Host '  Postgres        127.0.0.1:5433   user dthcms / password dthcms_local_only / db dthcms'
  Write-Host '  Redis           127.0.0.1:6380'
  Write-Host '  MinIO API       http://localhost:9000'
  Write-Host '  MinIO console   http://localhost:9001   (dthcms / dthcms_local_only)'
  Write-Host '  Mock AI + OCR   http://localhost:8090/healthz'
  Write-Host '  Grafana         http://localhost:3001   (traces, dashboards, alerts — DTHCMS folder)'
  Write-Host '  OTLP intake     127.0.0.1:4318 (HTTP) / 127.0.0.1:4317 (gRPC)'
  Write-Host '  Mailpit inbox   http://localhost:8025   (alert email lands here)'
  Write-Host ''
  Write-Host '  Ports are overridable in .env — see .env.example.' -ForegroundColor DarkGray
  Write-Host ''
}

switch ($Command) {

  'up' {
    Require-Docker

    Write-Host ''
    Write-Host 'Starting the DTHCMS local stack...' -ForegroundColor Cyan
    Write-Host 'First run pulls images and builds the mock service; later runs take seconds.' -ForegroundColor DarkGray

    # Long-running services only. The bucket-creation task is a separate step below,
    # because --wait treats a container that exits as a failure even when it succeeded.
    docker compose up -d --wait
    if ($LASTEXITCODE -ne 0) {
      Write-Host ''
      Write-Host 'The stack did not come up cleanly.' -ForegroundColor Red
      docker compose ps
      Write-Host ''
      Write-Host 'Inspect a specific service with:' -ForegroundColor Yellow
      Write-Host '  .\scripts\dev.ps1 logs postgres'
      exit 1
    }

    Write-Host ''
    Write-Host 'Creating object-storage buckets...' -ForegroundColor Cyan
    docker compose --profile init run --rm -T minio-init
    if ($LASTEXITCODE -ne 0) {
      Write-Host ''
      Write-Host 'Services are up, but bucket creation failed.' -ForegroundColor Red
      Write-Host 'Retry with:  docker compose --profile init run --rm minio-init'
      exit 1
    }

    Write-Host ''
    Write-Host 'Provisioning dashboards and alert rules...' -ForegroundColor Cyan
    docker compose --profile init run --rm -T grafana-init
    if ($LASTEXITCODE -ne 0) {
      # This used to be a warning that execution continued past. It was missed, and the
      # stack ran for a day with no dashboards while reporting that it was ready.
      # "Ready" has to mean ready, or it stops meaning anything.
      Write-Host ''
      Write-Host 'Services are up, but observability provisioning FAILED.' -ForegroundColor Red
      Write-Host 'There are no dashboards and no alert rules. The API itself is unaffected.'
      Write-Host ''
      Write-Host 'See what went wrong:' -ForegroundColor Yellow
      Write-Host '  docker compose --profile init run --rm grafana-init'
      exit 1
    }

    Write-Host ''
    Write-Host 'All services are healthy.' -ForegroundColor Green
    Show-Urls
  }

  'down' {
    Require-Docker
    docker compose down
    Write-Host 'Stack stopped. Data is preserved — use "reset" to erase it.' -ForegroundColor Green
  }

  'reset' {
    Require-Docker
    Write-Host ''
    Write-Host 'This deletes all local database, Redis and object-storage data.' -ForegroundColor Yellow
    $answer = Read-Host 'Type "reset" to confirm'
    if ($answer -ne 'reset') {
      Write-Host 'Cancelled.' -ForegroundColor Yellow
      exit 0
    }
    docker compose down -v
    docker compose up -d --wait
    docker compose --profile init run --rm -T minio-init
    docker compose --profile init run --rm -T grafana-init
    Write-Host ''
    Write-Host 'Stack reset and running with empty data.' -ForegroundColor Green
  }

  'status' {
    Require-Docker
    docker compose ps
  }

  'logs' {
    Require-Docker
    if ($Service) { docker compose logs -f $Service } else { docker compose logs -f }
  }

  'observability' {
    Require-Docker
    docker compose --profile init run --rm -T grafana-init
  }

  'migrate' {
    Require-Go
    Write-Host 'Applying migrations...' -ForegroundColor Cyan
    Push-Location (Join-Path $repoRoot 'backend')
    try {
      go run ./cmd/migrate up
      if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

      # The application connects as dthcms_app_local, not as the database owner, so a
      # write the ledger forbids fails here rather than in staging (docs/database.md).
      go run ./cmd/migrate dev-roles
      if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    }
    finally { Pop-Location }
    Write-Host ''
    Write-Host 'Schema applied and local roles created.' -ForegroundColor Green
  }

  'migrate-status' {
    Require-Go
    Push-Location (Join-Path $repoRoot 'backend')
    try { go run ./cmd/migrate status } finally { Pop-Location }
  }

  'migrate-verify' {
    Require-Go
    Push-Location (Join-Path $repoRoot 'backend')
    try { go run ./cmd/migrate verify } finally { Pop-Location }
  }

  'psql' {
    Require-Docker
    docker compose exec postgres psql -U dthcms -d dthcms
  }

  'redis' {
    Require-Docker
    docker compose exec redis redis-cli
  }

  'urls' { Show-Urls }
}
