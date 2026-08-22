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
  [ValidateSet('up', 'down', 'reset', 'status', 'logs', 'psql', 'redis', 'urls')]
  [string]$Command = 'up',

  [Parameter(Position = 1)]
  [string]$Service
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $repoRoot

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
  Write-Host '  Postgres        localhost:5432    user dthcms / password dthcms_local_only / db dthcms'
  Write-Host '  Redis           localhost:6379'
  Write-Host '  MinIO API       http://localhost:9000'
  Write-Host '  MinIO console   http://localhost:9001   (dthcms / dthcms_local_only)'
  Write-Host '  Mock AI + OCR   http://localhost:8090/healthz'
  Write-Host '  Mailpit inbox   http://localhost:8025'
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
