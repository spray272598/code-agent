# Mock-mode stress / evidence pack (no LLM API key required).
# Produces reports/mock-stress-latest.json + .md for design.md "压测证据" baseline.
#
#   powershell -File scripts/mock_stress.ps1
#   powershell -File scripts/mock_stress.ps1 -NoStartServer

param(
  [string]$Base = "http://127.0.0.1:8080",
  [string]$Key = "dev-key",
  [string]$Config = "configs/config.yaml",
  [string]$OutFile = "reports/mock-stress-latest.json",
  [int]$Rounds = 5,
  [switch]$NoStartServer
)

$ErrorActionPreference = "Continue"
$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $Root
New-Item -ItemType Directory -Force -Path reports, bin | Out-Null

$env:LLM_USE_MOCK = "true"
if (-not $env:DB_TYPE) { $env:DB_TYPE = "memory" }
if (-not $env:REDIS_ENABLED) { $env:REDIS_ENABLED = "false" }

$h = @{ "X-API-Key" = $Key; "Content-Type" = "application/json" }
$results = New-Object System.Collections.Generic.List[object]
$pass = 0; $fail = 0
$started = Get-Date
$proc = $null

function Add-R($name, $ok, $detail, $ms, $extra) {
  $results.Add([pscustomobject]@{ name=$name; ok=[bool]$ok; detail="$detail"; latency_ms=[int]$ms; extra=$extra }) | Out-Null
  if ($ok) { $script:pass++; Write-Host ("  PASS {0,-28} {1}ms  {2}" -f $name,$ms,$detail) -ForegroundColor Green }
  else { $script:fail++; Write-Host ("  FAIL {0,-28} {1}ms  {2}" -f $name,$ms,$detail) -ForegroundColor Red }
}

function Run-Case($name, [scriptblock]$body) {
  $sw = [System.Diagnostics.Stopwatch]::StartNew()
  try {
    $detail = & $body
    $sw.Stop()
    if ($null -eq $detail) { $detail = "ok" }
    if ($detail -is [hashtable]) { Add-R $name $true $detail.detail $sw.ElapsedMilliseconds $detail }
    else { Add-R $name $true "$detail" $sw.ElapsedMilliseconds $null }
  } catch {
    $sw.Stop()
    Add-R $name $false $_.Exception.Message $sw.ElapsedMilliseconds $null
  }
}

if (-not $NoStartServer) {
  try { $null = Invoke-RestMethod -Uri "$Base/health" -Headers $h -TimeoutSec 2 }
  catch {
    Write-Host "building + starting mock server ..." -ForegroundColor Yellow
    go build -o bin/server.exe ./cmd/server
    $proc = Start-Process -FilePath ".\bin\server.exe" -ArgumentList "-config",$Config -PassThru -WindowStyle Hidden
    $ok = $false
    for ($i=0; $i -lt 40; $i++) {
      Start-Sleep -Milliseconds 300
      try { $null = Invoke-RestMethod -Uri "$Base/health" -Headers $h -TimeoutSec 1; $ok=$true; break } catch {}
    }
    if (-not $ok) { Write-Host "server not healthy" -ForegroundColor Red; exit 1 }
  }
}

Write-Host "=== mock stress / evidence ===" -ForegroundColor Cyan
Write-Host "base=$Base rounds=$Rounds"

Run-Case "health" {
  $r = Invoke-RestMethod -Uri "$Base/health" -Headers $h -TimeoutSec 5
  if ($r.status -ne "ok" -and -not $r.ok) { "status=$($r.status)" } else { "ok" }
}

Run-Case "tools_list" {
  $r = Invoke-RestMethod -Uri "$Base/api/v1/tools" -Headers $h -TimeoutSec 10
  $n = 0
  if ($r.data -is [array]) { $n = $r.data.Count }
  elseif ($r.data.tools) { $n = @($r.data.tools).Count }
  if ($n -lt 4) { throw "tools=$n" }
  "tools=$n"
}

Run-Case "session_create" {
  $r = Invoke-RestMethod -Uri "$Base/api/v1/session" -Method POST -Headers $h -Body (@{ userId="stress"; title="mock-stress" } | ConvertTo-Json)
  $script:sid = $r.data.sessionId
  if (-not $script:sid) { throw "no sessionId" }
  "session=$($script:sid)"
}

for ($i=1; $i -le $Rounds; $i++) {
  Run-Case ("chat_round_" + $i) {
    $body = @{
      sessionId = $script:sid; userId = "stress"
      message = "list tools you have and answer briefly round $i"
      autoApprove = $true
    } | ConvertTo-Json
    $r = Invoke-RestMethod -Uri "$Base/api/v1/chat" -Method POST -Headers $h -Body $body -TimeoutSec 60
    $resp = "$($r.data.response)"
    if ($resp.Length -lt 1) { throw "empty response" }
    @{ detail = ("len=" + $resp.Length); tokens = $r.data.tokenUsed; needPerm = $r.data.needPermission }
  }
}

Run-Case "metrics" {
  $r = Invoke-RestMethod -Uri "$Base/api/v1/metrics" -Headers $h -TimeoutSec 10
  $d = $r.data
  "chat_total=$($d.chat_total) tool_calls=$($d.tool_calls) tokens=$($d.tokens_total)"
}

Run-Case "permission_pending_empty" {
  $r = Invoke-RestMethod -Uri "$Base/api/v1/permission/pending?sessionId=$($script:sid)" -Headers $h -TimeoutSec 10
  "ok"
}

$elapsed = [int]((Get-Date) - $started).TotalMilliseconds
$total = $pass + $fail
$rate = if ($total -gt 0) { [math]::Round($pass / $total, 4) } else { 0 }

$report = [ordered]@{
  kind = "mock-stress"
  generated_at = (Get-Date).ToString("o")
  base = $Base
  mock = $true
  rounds = $Rounds
  pass = $pass
  fail = $fail
  pass_rate = $rate
  elapsed_ms = $elapsed
  results = $results
}
$jsonPath = $OutFile
$mdPath = [IO.Path]::ChangeExtension($OutFile, ".md")
($report | ConvertTo-Json -Depth 8) | Set-Content -Path $jsonPath -Encoding UTF8

$md = @"
# Mock stress evidence

| field | value |
|-------|-------|
| generated | $($report.generated_at) |
| base | $Base |
| mock | true |
| rounds | $Rounds |
| pass / fail | $pass / $fail |
| pass_rate | $rate |
| elapsed_ms | $elapsed |

## Cases

| name | ok | latency_ms | detail |
|------|----|------------|--------|
"@
foreach ($r in $results) {
  $md += "| $($r.name) | $($r.ok) | $($r.latency_ms) | $($r.detail) |`n"
}
$md += @"

## Notes

- This is **offline/mock** evidence for CI and design.md acceptance without an API key.
- Real-model long-task evidence: ``powershell -File scripts/llm_stress.ps1`` (requires ``LLM_API_KEY``).
- Eval pack: ``powershell -File scripts/eval_report.ps1``.
"@
$md | Set-Content -Path $mdPath -Encoding UTF8

Write-Host ""
Write-Host "pass_rate=$rate  ($pass/$total)  elapsed=${elapsed}ms" -ForegroundColor Cyan
Write-Host "wrote $jsonPath"
Write-Host "wrote $mdPath"

if ($proc) { Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue }
if ($rate -lt 0.8) { exit 1 }
exit 0
