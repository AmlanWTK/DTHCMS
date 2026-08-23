<#
.SYNOPSIS
  Verify the observability stack from the terminal: dashboards, alert rules, and whether
  the API's traces and metrics are actually arriving.
.DESCRIPTION
  Everything here is also visible in Grafana at http://localhost:3001. This exists because
  "are the metrics arriving?" is a yes/no question, and answering it by reading a chart
  means knowing what an empty chart looks like versus a broken one.

  Read-only. It changes nothing.
.PARAMETER GrafanaUrl
  Where Grafana is. Defaults to the local stack.
.PARAMETER Detailed
  Also print the route templates and status codes actually recorded, which is how you see
  that metrics are labelled by route rather than by raw path.
.PARAMETER Raw
  Print what Grafana actually returned for alert rules and contact points. For settling a
  disagreement between this script and reality with evidence rather than a theory.
.EXAMPLE
  .\scripts\check-observability.ps1
  .\scripts\check-observability.ps1 -Detailed
#>
param(
  [string]$GrafanaUrl = 'http://localhost:3001',
  [switch]$Detailed,
  [switch]$Raw
)

$ErrorActionPreference = 'Stop'
$results = @()

# Windows PowerShell 5.1 has no -SkipHttpErrorCheck and no -Authentication, so basic auth
# is built by hand and every call is wrapped.
$auth = @{
  Authorization = 'Basic ' + [Convert]::ToBase64String([Text.Encoding]::ASCII.GetBytes('admin:admin'))
}

function Get-Json($path) {
  $response = Invoke-RestMethod -Uri ($GrafanaUrl + $path) -Headers $auth -TimeoutSec 15

  # Windows PowerShell hands a JSON array back as a single collection object rather than
  # enumerating it. Everything downstream then misbehaves in a way that looks like data
  # rather than plumbing: Where-Object tests the whole collection as one item, `-eq`
  # against an array returns the matching elements instead of a boolean, so the filter
  # passes, and $result[0].someProperty member-enumerates across every element. That is
  # how "delivered to" came to read as three addresses joined together while the check
  # reported a single contact point.
  #
  # Writing each element to the pipeline explicitly makes callers behave as written.
  if ($response -is [System.Collections.IEnumerable] -and $response -isnot [string]) {
    foreach ($item in $response) { $item }
  }
  else {
    $response
  }
}

function Add-Result($name, $ok, $detail) {
  $script:results += [pscustomobject]@{ Check = $name; Result = $(if ($ok) { 'PASS' } else { 'FAIL' }); Detail = $detail }
}

function Write-Result($r) {
  $colour = if ($r.Result -eq 'PASS') { 'Green' } else { 'Red' }
  Write-Host ('  {0,-6}' -f $r.Result) -ForegroundColor $colour -NoNewline
  Write-Host ('{0,-34}' -f $r.Check) -NoNewline
  Write-Host $r.Detail -ForegroundColor DarkGray
}

Write-Host ''
Write-Host "Checking observability at $GrafanaUrl" -ForegroundColor Cyan
Write-Host ''

# ---------------------------------------------------------------------------
# 1. Grafana itself
# ---------------------------------------------------------------------------

try {
  $health = Get-Json '/api/health'
  Add-Result 'Grafana is running' $true "version $($health.version)"
}
catch {
  Write-Host '  FAIL  Grafana is not reachable.' -ForegroundColor Red
  Write-Host ''
  Write-Host '  The observability container is probably not running. Start it with:' -ForegroundColor Yellow
  Write-Host '    .\scripts\dev.ps1 up'
  Write-Host ''
  Write-Host '  If it is running, check its logs:' -ForegroundColor Yellow
  Write-Host '    docker compose logs observability'
  Write-Host ''
  exit 1
}

# ---------------------------------------------------------------------------
# 2. Dashboards
# ---------------------------------------------------------------------------

$wantDashboards = @{
  'dthcms-latency'    = 'Latency'
  'dthcms-errors'     = 'Errors'
  'dthcms-saturation' = 'Saturation'
}

foreach ($uid in $wantDashboards.Keys) {
  try {
    $d = Get-Json "/api/dashboards/uid/$uid"
    Add-Result "Dashboard: $($wantDashboards[$uid])" $true "$($d.dashboard.panels.Count) panels - $GrafanaUrl$($d.meta.url)"
  }
  catch {
    Add-Result "Dashboard: $($wantDashboards[$uid])" $false 'not installed - run .\scripts\dev.ps1 observability'
  }
}

# ---------------------------------------------------------------------------
# 3. Alert rules and where they go
# ---------------------------------------------------------------------------

$wantRules = @('dthcms-error-rate', 'dthcms-latency', 'dthcms-db-pool', 'dthcms-no-telemetry')
try {
  $rules = @(Get-Json '/api/v1/provisioning/alert-rules' | Where-Object { $_.folderUID -eq 'dthcms' })
  $haveUids = @($rules | ForEach-Object { $_.uid })
  if ($Raw) { Write-Host "  raw alert-rule uids: $($haveUids -join ', ')" -ForegroundColor DarkGray }
  $missing = @($wantRules | Where-Object { $haveUids -notcontains $_ })

  $found = @($wantRules | Where-Object { $haveUids -contains $_ })
  if ($missing.Count -eq 0) {
    Add-Result 'Alert rules' $true "$($found.Count) of $($wantRules.Count) installed"
  }
  else {
    # Naming them matters: "1 of 4" sends you looking at all four.
    Add-Result 'Alert rules' $false "missing: $($missing -join ', ')"
  }
}
catch {
  Add-Result 'Alert rules' $false 'could not read them from Grafana'
}

try {
  $contact = @(Get-Json '/api/v1/provisioning/contact-points' | Where-Object { $_.name -eq 'dthcms-oncall' })

  if ($contact.Count -eq 0) {
    Add-Result 'Alerts have a recipient' $false 'no dthcms-oncall contact point'
  }
  elseif ($contact.Count -gt 1) {
    # Two contact points with the same name means provisioning ran twice and created
    # rather than replaced. Grafana will use one of them; which one is not obvious.
    $all = ($contact | ForEach-Object { $_.settings.addresses }) -join ' | '
    Add-Result 'Alerts have a recipient' $false "$($contact.Count) contact points named dthcms-oncall: $all"
  }
  else {
    # -join guards against the value arriving as a collection: PowerShell would otherwise
    # interpolate several addresses into one space-separated string that looks like a
    # single malformed address.
    $address = $contact[0].settings.addresses
    Add-Result 'Alerts have a recipient' $true "delivered to $address"
  }

  if ($Raw) {
    Write-Host '  raw contact points:' -ForegroundColor DarkGray
    Get-Json '/api/v1/provisioning/contact-points' | ConvertTo-Json -Depth 6 | Write-Host -ForegroundColor DarkGray
  }
}
catch {
  Add-Result 'Alerts have a recipient' $false 'contact point missing'
}

# ---------------------------------------------------------------------------
# 4. Are metrics actually arriving?
#
# Queried through Grafana's datasource proxy, so this needs no extra port published and
# uses exactly the path the dashboards use.
# ---------------------------------------------------------------------------

function Invoke-PromQL($expr) {
  $encoded = [uri]::EscapeDataString($expr)
  $path = "/api/datasources/proxy/uid/dthcms-prometheus/api/v1/query?query=$encoded"
  # Callers must wrap this in @(). PowerShell unrolls a single-element array when a
  # function returns it, so wrapping here would achieve nothing: a PromQL sum() returns
  # exactly one result, which arrives at the caller as a bare object with no .Count, and
  # `$r.Count -gt 0` is then false. That misread is what made this script report "no
  # requests recorded" while its own detailed output listed thirty-six of them.
  (Get-Json $path).data.result
}

$requests = $null
try {
  $requests = @(Invoke-PromQL 'sum(http_server_request_duration_seconds_count)')
}
catch {
  Add-Result 'Metrics are arriving' $false 'the Prometheus datasource did not answer'
}

if ($null -ne $requests) {
  $total = 0
  if ($requests.Count -gt 0) { $total = [double]$requests[0].value[1] }

  if ($total -gt 0) {
    Add-Result 'Metrics are arriving' $true "$([int]$total) requests recorded"
  }
  else {
    Add-Result 'Metrics are arriving' $false 'no requests recorded yet - is the API running, and has 30s passed?'
  }
}

# ---------------------------------------------------------------------------
# 5. Are the labels safe?
#
# The check that matters most and is the least visible: metric labels must be route
# templates, never raw paths. A raw path would carry patient identifiers and would create
# one time series per patient.
# ---------------------------------------------------------------------------

try {
  $routes = @(Invoke-PromQL 'sum by (http_route) (http_server_request_duration_seconds_count)')
  $names = @($routes | ForEach-Object { $_.metric.http_route } | Where-Object { $_ })

  if ($names.Count -eq 0) {
    Add-Result 'Labels use route templates' $false 'no route labels recorded yet'
  }
  else {
    # A path segment that looks like an id - a UUID, or a long opaque string - means the
    # raw path is being used as a label somewhere.
    $suspicious = @($names | Where-Object {
        $_ -match '[0-9a-fA-F]{8}-[0-9a-fA-F]{4}' -or $_ -match '/\d{3,}'
      })

    if ($suspicious.Count -gt 0) {
      Add-Result 'Labels use route templates' $false "raw ids in labels: $($suspicious -join ', ')"
    }
    else {
      Add-Result 'Labels use route templates' $true "$($names.Count) distinct: $($names -join ', ')"
    }
  }
}
catch {
  Add-Result 'Labels use route templates' $false 'could not query route labels'
}

# ---------------------------------------------------------------------------
# 6. Traces
#
# Tempo's search API differs between versions, so a failure here is reported as "check by
# hand" rather than as a broken system - the dashboards above are the load-bearing part.
# ---------------------------------------------------------------------------

try {
  $datasources = Get-Json '/api/datasources'
  $tempo = @($datasources | Where-Object { $_.type -eq 'tempo' })

  if ($tempo.Count -eq 0) {
    Add-Result 'Traces' $false 'no Tempo datasource found'
  }
  else {
    $uid = $tempo[0].uid
    $query = [uri]::EscapeDataString('{ resource.service.name = "api" }')
    $found = (Get-Json "/api/datasources/proxy/uid/$uid/api/search?q=$query&limit=5").traces

    if ($found -and @($found).Count -gt 0) {
      Add-Result 'Traces are arriving' $true "$(@($found).Count) recent traces from the api service"
    }
    else {
      Add-Result 'Traces are arriving' $false 'none found - make a request, then wait ~10s'
    }
  }
}
catch {
  Add-Result 'Traces are arriving' $false "could not query Tempo automatically - check Explore in Grafana"
}

# ---------------------------------------------------------------------------
# Report
# ---------------------------------------------------------------------------

foreach ($r in $results) { Write-Result $r }

if ($Detailed) {
  Write-Host ''
  Write-Host 'Requests recorded, by status code' -ForegroundColor Cyan
  try {
    Invoke-PromQL 'sum by (http_response_status_code, http_route) (http_server_request_duration_seconds_count)' |
      ForEach-Object {
        Write-Host ('  {0,-6} {1,-30} {2}' -f $_.metric.http_response_status_code, $_.metric.http_route, [int][double]$_.value[1])
      }
  }
  catch {
    Write-Host '  (unavailable)' -ForegroundColor DarkGray
  }
}

$failed = @($results | Where-Object { $_.Result -eq 'FAIL' })

Write-Host ''
if ($failed.Count -gt 0) {
  Write-Host "$($failed.Count) check(s) failed." -ForegroundColor Red
  Write-Host ''
  Write-Host 'Most common causes:' -ForegroundColor Yellow
  Write-Host '  Dashboards missing    .\scripts\dev.ps1 observability'
  Write-Host '  No metrics or traces  the API is not running, or fewer than 30 seconds have passed'
  Write-Host '                        (metrics push every 15s; check the API log for'
  Write-Host '                         "telemetry export failed")'
  exit 1
}

Write-Host 'Observability is working.' -ForegroundColor Green
Write-Host ''
Write-Host "  Dashboards   $GrafanaUrl/dashboards" -ForegroundColor DarkGray
Write-Host "  Traces       $GrafanaUrl/explore  (select Tempo)" -ForegroundColor DarkGray
Write-Host '  Alert email  http://localhost:8025' -ForegroundColor DarkGray
Write-Host ''
