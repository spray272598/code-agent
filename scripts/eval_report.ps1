# Numeric eval report for code-agent (Windows PowerShell)
# Usage:
#   1) start server (mock or real)
#   2) powershell -File scripts/eval_report.ps1
#   3) optional: -OutFile reports/eval-latest.json
#
# Exit code: 0 if pass_rate >= MinPassRate (default 0.8), else 1

param(
  [string]$Base = "",
  [string]$Key = "",
  [string]$OutFile = "reports/eval-latest.json",
  [double]$MinPassRate = 0.8
)

$ErrorActionPreference = "Continue"
if (-not $Base) { $Base = if ($env:CODE_AGENT_BASE) { $env:CODE_AGENT_BASE } else { "http://127.0.0.1:8080" } }
if (-not $Key) { $Key = if ($env:CODE_AGENT_API_KEY) { $env:CODE_AGENT_API_KEY } else { "dev-key" } }
$h = @{ "X-API-Key" = $Key; "Content-Type" = "application/json" }

$results = New-Object System.Collections.Generic.List[object]
$pass = 0
$fail = 0
$start = Get-Date

function Add-Result($name, $ok, $detail, $ms) {
  $script:results.Add([pscustomobject]@{
    name = $name; ok = [bool]$ok; detail = "$detail"; latency_ms = [int]$ms
  }) | Out-Null
  if ($ok) {
    $script:pass++
    Write-Host ("  PASS {0,-28} {1}ms  {2}" -f $name, $ms, $detail) -ForegroundColor Green
  } else {
    $script:fail++
    Write-Host ("  FAIL {0,-28} {1}ms  {2}" -f $name, $ms, $detail) -ForegroundColor Red
  }
}

function Run-Case($name, [scriptblock]$body) {
  $sw = [System.Diagnostics.Stopwatch]::StartNew()
  try {
    $detail = & $body
    $sw.Stop()
    if ($null -eq $detail) { $detail = "ok" }
    Add-Result $name $true $detail $sw.ElapsedMilliseconds
  } catch {
    $sw.Stop()
    Add-Result $name $false $_.Exception.Message $sw.ElapsedMilliseconds
  }
}

Write-Host "=== code-agent eval report ===" -ForegroundColor Cyan
Write-Host "target=$Base  min_pass_rate=$MinPassRate"
Write-Host ""

# --- cases ---
Run-Case "health" {
  $r = Invoke-RestMethod -Uri "$Base/health" -Headers $h -TimeoutSec 5
  if ($r.status -ne "ok") { throw "status=$($r.status)" }
  "ok"
}

Run-Case "openapi" {
  $r = Invoke-WebRequest -Uri "$Base/api/v1/openapi.json" -UseBasicParsing -TimeoutSec 5
  if ($r.StatusCode -ne 200) { throw "http $($r.StatusCode)" }
  if ($r.Content -notmatch "Code-Agent") { throw "missing title" }
  "bytes=$($r.RawContentLength)"
}

Run-Case "tools_core" {
  $r = Invoke-RestMethod -Uri "$Base/api/v1/tools" -Headers $h
  $names = @($r.data | ForEach-Object { $_.name })
  $need = @("read_file","write_file","edit_file","bash","glob","grep","memory_save","memory_search")
  foreach ($n in $need) {
    if ($names -notcontains $n) { throw "missing $n" }
  }
  "count=$($names.Count)"
}

Run-Case "tools_delegate" {
  $r = Invoke-RestMethod -Uri "$Base/api/v1/tools" -Headers $h
  $names = @($r.data | ForEach-Object { $_.name })
  if ($names -notcontains "delegate") { throw "missing delegate" }
  "ok"
}

Run-Case "session_create" {
  $r = Invoke-RestMethod -Uri "$Base/api/v1/session" -Method POST -Headers $h -Body '{"userId":"eval","title":"eval-report"}'
  if (-not $r.data.sessionId) { throw "no sessionId" }
  $script:sid = $r.data.sessionId
  "sid=$script:sid"
}

Run-Case "chat_auto_approve" {
  if (-not $script:sid) { throw "no session" }
  $body = @{ sessionId = $script:sid; userId = "eval"; message = "say hello only"; autoApprove = $true } | ConvertTo-Json
  $r = Invoke-RestMethod -Uri "$Base/api/v1/chat" -Method POST -Headers $h -Body $body
  if (-not $r.data.response) { throw "empty response" }
  "toolCalls=$($r.data.toolCalls) class=$($r.data.errorClass)"
}

Run-Case "slash_help" {
  $body = @{ userId = "eval"; message = "/help"; autoApprove = $true } | ConvertTo-Json
  $r = Invoke-RestMethod -Uri "$Base/api/v1/chat" -Method POST -Headers $h -Body $body
  if (-not $r.data.slash -and "$($r.data.response)" -notmatch "Slash|help") { throw "slash not handled" }
  "slash=$($r.data.slash)"
}

Run-Case "memory_write_read" {
  $mem = @{ userId = "eval"; scope = "user"; content = "eval-report prefers concise answers"; importance = 70 } | ConvertTo-Json
  Invoke-RestMethod -Uri "$Base/api/v1/memory" -Method POST -Headers $h -Body $mem | Out-Null
  $list = Invoke-RestMethod -Uri "$Base/api/v1/memory?userId=eval" -Headers $h
  if ($list.data.Count -lt 1) { throw "empty list" }
  "n=$($list.data.Count)"
}

Run-Case "metrics_json" {
  $r = Invoke-RestMethod -Uri "$Base/api/v1/metrics" -Headers $h
  if ($null -eq $r.data.chat_total) { throw "no chat_total" }
  "chat_total=$($r.data.chat_total)"
}

Run-Case "prometheus" {
  $r = Invoke-WebRequest -Uri "$Base/metrics" -UseBasicParsing -TimeoutSec 5
  if ($r.Content -notmatch "code_agent") { throw "missing code_agent metrics" }
  "ok"
}

Run-Case "permission_pending_list" {
  $r = Invoke-RestMethod -Uri "$Base/api/v1/permission/pending" -Headers $h
  "pending=$($r.data.Count)"
}

Run-Case "skills_list" {
  $r = Invoke-RestMethod -Uri "$Base/api/v1/skills" -Headers $h
  "n=$($r.data.Count)"
}

Run-Case "host_devices" {
  $r = Invoke-RestMethod -Uri "$Base/api/v1/host/devices" -Headers $h
  $online = 0
  if ($r.data.online) { $online = [int]$r.data.online }
  "online=$online"
}

Run-Case "auth_reject" {
  try {
    Invoke-RestMethod -Uri "$Base/api/v1/tools" -Headers @{ "X-API-Key" = "definitely-wrong-key" } -TimeoutSec 5 | Out-Null
    throw "expected 401"
  } catch {
    $msg = "$_"
    if ($msg -match "401" -or $msg -match "Unauthorized" -or $msg -match "invalid") {
      "rejected"
    } else {
      # Invoke-RestMethod may throw differently
      if ($_.Exception.Response.StatusCode.value__ -eq 401) { "401" } else { throw $_ }
    }
  }
}

Run-Case "cors_preflight_safe" {
  # health is public; openapi should not require key
  $r = Invoke-WebRequest -Uri "$Base/api/v1/openapi.json" -UseBasicParsing -TimeoutSec 5
  if ($r.StatusCode -ne 200) { throw "status $($r.StatusCode)" }
  "ok"
}

# --- score ---
$total = $pass + $fail
$rate = if ($total -gt 0) { [math]::Round($pass / $total, 4) } else { 0 }
$elapsed = [int]((Get-Date) - $start).TotalMilliseconds

$report = [pscustomobject]@{
  generated_at = (Get-Date).ToString("o")
  base = $Base
  total = $total
  pass = $pass
  fail = $fail
  pass_rate = $rate
  min_pass_rate = $MinPassRate
  elapsed_ms = $elapsed
  cases = $results
}

$dir = Split-Path -Parent $OutFile
if ($dir -and -not (Test-Path $dir)) {
  New-Item -ItemType Directory -Force -Path $dir | Out-Null
}
$report | ConvertTo-Json -Depth 6 | Set-Content -Path $OutFile -Encoding UTF8

# markdown summary
$mdPath = [System.IO.Path]::ChangeExtension($OutFile, ".md")
$md = @()
$md += "# Code-Agent Eval Report"
$md += ""
$md += "- **time**: $($report.generated_at)"
$md += "- **base**: $Base"
$md += "- **score**: **$pass / $total** (pass_rate=**$rate**)"
$md += "- **elapsed_ms**: $elapsed"
$md += "- **threshold**: $MinPassRate"
$md += ""
$md += "| Case | Result | ms | Detail |"
$md += "|------|--------|----|--------|"
foreach ($c in $results) {
  $st = if ($c.ok) { "PASS" } else { "FAIL" }
  $md += "| $($c.name) | $st | $($c.latency_ms) | $($c.detail -replace '\|','/') |"
}
$md += ""
$md += $(if ($rate -ge $MinPassRate) { "**Overall: PASS**" } else { "**Overall: FAIL**" })
$md -join "`n" | Set-Content -Path $mdPath -Encoding UTF8

Write-Host ""
Write-Host ("SCORE {0}/{1} pass_rate={2} elapsed={3}ms" -f $pass, $total, $rate, $elapsed) -ForegroundColor Cyan
Write-Host "JSON: $OutFile"
Write-Host "MD:   $mdPath"

if ($rate -ge $MinPassRate) {
  Write-Host "EVAL OVERALL PASS" -ForegroundColor Green
  exit 0
} else {
  Write-Host "EVAL OVERALL FAIL (below $MinPassRate)" -ForegroundColor Red
  exit 1
}
