# Real-model long-task stress + comparison report for code-agent.
# Secrets: pass via env only — never commit keys.
#
#   $env:LLM_API_KEY="sk-..."
#   $env:LLM_BASE_URL="https://api.siliconflow.cn/v1"   # or LLM_API_BASE
#   $env:LLM_MODEL="Qwen/Qwen2.5-32B-Instruct"
#   $env:LLM_USE_MOCK="false"
#   powershell -File scripts/llm_stress.ps1
#
# Output: reports/llm-stress-latest.json + .md

param(
  [string]$Base = "http://127.0.0.1:8080",
  [string]$Key = "dev-key",
  [string]$Config = "configs/config.host.yaml",
  [string]$OutFile = "reports/llm-stress-latest.json",
  [int]$TimeoutSec = 240,
  [switch]$NoStartServer
)

$ErrorActionPreference = "Continue"
$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $Root

if (-not $env:LLM_API_KEY) {
  Write-Host "FAIL: set LLM_API_KEY (do not hardcode in repo)" -ForegroundColor Red
  exit 2
}
if ($env:LLM_BASE_URL -and -not $env:LLM_API_BASE) {
  $env:LLM_API_BASE = $env:LLM_BASE_URL
}
$env:LLM_USE_MOCK = "false"
if (-not $env:LLM_MODEL) { $env:LLM_MODEL = "Qwen/Qwen2.5-32B-Instruct" }
# index + tools should see this repo during stress
if (-not $env:WORKSPACE_ROOT) { $env:WORKSPACE_ROOT = $Root }
if (-not $env:AGENT_TIMEOUT_SEC) { $env:AGENT_TIMEOUT_SEC = "300" }
if (-not $env:AGENT_MAX_STEPS) { $env:AGENT_MAX_STEPS = "24" }
# memory db — no mysql required
if (-not $env:DB_TYPE) { $env:DB_TYPE = "memory" }
if (-not $env:REDIS_ENABLED) { $env:REDIS_ENABLED = "false" }

$h = @{ "X-API-Key" = $Key; "Content-Type" = "application/json" }
$results = New-Object System.Collections.Generic.List[object]
$pass = 0; $fail = 0
$serverProc = $null
$started = Get-Date

function Add-R($name, $ok, $detail, $ms, $extra) {
  $o = [pscustomobject]@{ name=$name; ok=[bool]$ok; detail="$detail"; latency_ms=[int]$ms; extra=$extra }
  $results.Add($o) | Out-Null
  if ($ok) { $script:pass++; Write-Host ("  PASS {0,-32} {1}ms  {2}" -f $name,$ms,$detail) -ForegroundColor Green }
  else { $script:fail++; Write-Host ("  FAIL {0,-32} {1}ms  {2}" -f $name,$ms,$detail) -ForegroundColor Red }
}

function Run-Case($name, [scriptblock]$body) {
  $sw = [System.Diagnostics.Stopwatch]::StartNew()
  try {
    $detail = & $body
    $sw.Stop()
    if ($null -eq $detail) { $detail = "ok" }
    if ($detail -is [hashtable]) {
      Add-R $name $true ($detail.detail) $sw.ElapsedMilliseconds $detail
    } else {
      Add-R $name $true "$detail" $sw.ElapsedMilliseconds $null
    }
  } catch {
    $sw.Stop()
    Add-R $name $false $_.Exception.Message $sw.ElapsedMilliseconds $null
  }
}

# --- start server if needed ---
if (-not $NoStartServer) {
  try {
    $null = Invoke-RestMethod -Uri "$Base/health" -Headers $h -TimeoutSec 2
    Write-Host "server already up"
  } catch {
    Write-Host "building + starting server with real LLM ..." -ForegroundColor Yellow
    New-Item -ItemType Directory -Force -Path bin | Out-Null
    go build -o bin/server.exe ./cmd/server
    if ($LASTEXITCODE -ne 0) { exit 1 }
    $serverProc = Start-Process -FilePath ".\bin\server.exe" -ArgumentList @("-config", $Config) `
      -WorkingDirectory $Root -PassThru -WindowStyle Hidden
    $ok = $false
    for ($i=0; $i -lt 40; $i++) {
      try {
        $hh = Invoke-RestMethod -Uri "$Base/health" -Headers $h -TimeoutSec 2
        if ($hh.status -eq "ok") { $ok = $true; break }
      } catch { Start-Sleep -Milliseconds 500 }
    }
    if (-not $ok) {
      Write-Host "server health failed" -ForegroundColor Red
      if ($serverProc) { Stop-Process -Id $serverProc.Id -Force -ErrorAction SilentlyContinue }
      exit 1
    }
  }
}

Write-Host "=== LLM stress report ===" -ForegroundColor Cyan
Write-Host "base=$Base model=$($env:LLM_MODEL) base_url=$($env:LLM_API_BASE)"
Write-Host ""

# 1) index
Run-Case "index_rebuild" {
  $r = Invoke-RestMethod -Uri "$Base/api/v1/index/rebuild" -Method POST -Headers $h -TimeoutSec 60
  if (-not $r.data.files) { throw "no files" }
  "files=$($r.data.files) tokens=$($r.data.tokens)"
}

Run-Case "index_search_checkpoint" {
  $r = Invoke-RestMethod -Uri "$Base/api/v1/index/search?q=checkpoint" -Headers $h
  $n = 0
  if ($r.data.hits) { $n = @($r.data.hits).Count }
  if ($n -lt 1) { throw "no hits" }
  "hits=$n top=$($r.data.hits[0].path)"
}

# 2) tools include code_search
Run-Case "tools_have_code_search" {
  $r = Invoke-RestMethod -Uri "$Base/api/v1/tools" -Headers $h
  $names = @($r.data | ForEach-Object { $_.name })
  if ($names -notcontains "code_search") { throw "missing code_search" }
  if ($names -notcontains "code_index") { throw "missing code_index" }
  "ok"
}

# 3) session + short real chat
$sessionId = $null
Run-Case "session_create" {
  $r = Invoke-RestMethod -Uri "$Base/api/v1/session" -Method POST -Headers $h -Body '{"userId":"stress","title":"llm-stress"}'
  if (-not $r.data.sessionId) { throw "no sid" }
  $script:sessionId = $r.data.sessionId
  "sid=$script:sessionId"
}

Run-Case "chat_real_short" {
  if (-not $script:sessionId) { throw "no session" }
  $body = @{
    sessionId = $script:sessionId
    userId = "stress"
    message = "Reply with exactly one word: pong. Do not call tools."
    autoApprove = $true
  } | ConvertTo-Json
  $r = Invoke-RestMethod -Uri "$Base/api/v1/chat" -Method POST -Headers $h -Body $body -TimeoutSec $TimeoutSec
  if (-not $r.data.response) { throw "empty response class=$($r.data.errorClass)" }
  $txt = "$($r.data.response)"
  if ($txt -match "LLM init error|eino generate|mock") { throw "bad: $txt" }
  @{ detail = "len=$($txt.Length) tools=$($r.data.toolCalls) class=$($r.data.errorClass)"; response = $txt.Substring(0, [Math]::Min(200, $txt.Length)) }
}

# 4) long coding task (tools)
Run-Case "chat_long_code_search" {
  if (-not $script:sessionId) { throw "no session" }
  $msg = @"
You are stress-testing code_search. Use the code_search tool with query 'RunRegistry' or 'checkpoint'.
Then briefly report: (1) top file path (2) one sentence what it does. Max 5 tool calls. auto tools ok.
"@
  $body = @{
    sessionId = $script:sessionId
    userId = "stress"
    message = $msg
    autoApprove = $true
  } | ConvertTo-Json
  $r = Invoke-RestMethod -Uri "$Base/api/v1/chat" -Method POST -Headers $h -Body $body -TimeoutSec $TimeoutSec
  if (-not $r.data.response) { throw "empty class=$($r.data.errorClass)" }
  $txt = "$($r.data.response)"
  @{ detail = "tools=$($r.data.toolCalls) tokens~$($r.data.tokenUsed) class=$($r.data.errorClass)"; response = $txt.Substring(0, [Math]::Min(300, $txt.Length)); toolCalls = $r.data.toolCalls; tokenUsed = $r.data.tokenUsed }
}

# 5) deep vs teams (may be slow; still attempt)
Run-Case "chat_team_mode" {
  $body = @{
    userId = "stress"
    message = "/team Summarize in 3 bullets how security Guard layers work (read_file/grep only if needed). Be short."
    autoApprove = $true
  } | ConvertTo-Json
  $r = Invoke-RestMethod -Uri "$Base/api/v1/chat" -Method POST -Headers $h -Body $body -TimeoutSec $TimeoutSec
  if (-not $r.data.response) { throw "empty class=$($r.data.errorClass)" }
  $txt = "$($r.data.response)"
  if ($txt.Length -lt 20) { throw "too short" }
  @{ detail = "len=$($txt.Length) tools=$($r.data.toolCalls)"; response = $txt.Substring(0, [Math]::Min(240, $txt.Length)) }
}

Run-Case "chat_deep_mode" {
  $body = @{
    userId = "stress"
    message = "/deep In workspace, find where code_search tool is registered and quote the package path. Do not edit files. Short answer."
    autoApprove = $true
  } | ConvertTo-Json
  $r = Invoke-RestMethod -Uri "$Base/api/v1/chat" -Method POST -Headers $h -Body $body -TimeoutSec $TimeoutSec
  if (-not $r.data.response) { throw "empty class=$($r.data.errorClass)" }
  $txt = "$($r.data.response)"
  @{ detail = "len=$($txt.Length) tools=$($r.data.toolCalls)"; response = $txt.Substring(0, [Math]::Min(240, $txt.Length)) }
}

# 6) checkpoint after a confirm path (optional soft)
Run-Case "checkpoint_api" {
  # write-file without approve may create interrupt depending on path; use autoApprove chat already completed
  $list = Invoke-RestMethod -Uri "$Base/api/v1/session/checkpoints" -Headers $h
  $n = 0
  if ($list.data) { $n = @($list.data).Count }
  "checkpoints=$n"
  if ($script:sessionId) {
    try {
      $ck = Invoke-RestMethod -Uri "$Base/api/v1/session/checkpoint?sessionId=$script:sessionId" -Headers $h
      "status=$($ck.data.status)"
    } catch {
      "no snap yet (ok)"
    }
  }
}

Run-Case "cancel_idle" {
  $body = @{ sessionId = "nonexistent-idle"; reason = "stress" } | ConvertTo-Json
  $r = Invoke-RestMethod -Uri "$Base/api/v1/session/cancel" -Method POST -Headers $h -Body $body
  # cancelled=false expected for idle
  "cancelled=$($r.data.cancelled)"
}

Run-Case "slash_compare" {
  $body = @{ userId = "stress"; message = "/compare-agents"; autoApprove = $true } | ConvertTo-Json
  $r = Invoke-RestMethod -Uri "$Base/api/v1/chat" -Method POST -Headers $h -Body $body
  if ("$($r.data.response)" -notmatch "DeepAgent") { throw "missing comparison" }
  "slash=$($r.data.slash)"
}

# score
$total = $pass + $fail
$rate = if ($total -gt 0) { [math]::Round($pass / $total, 4) } else { 0 }
$elapsed = [int]((Get-Date) - $started).TotalMilliseconds

$report = [pscustomobject]@{
  generated_at = (Get-Date).ToString("o")
  base = $Base
  model = $env:LLM_MODEL
  api_base = $env:LLM_API_BASE
  provider = $env:LLM_PROVIDER
  total = $total
  pass = $pass
  fail = $fail
  pass_rate = $rate
  elapsed_ms = $elapsed
  cases = $results
  note = "API key never written to report files."
}

$dir = Split-Path -Parent $OutFile
if ($dir -and -not (Test-Path $dir)) { New-Item -ItemType Directory -Force -Path $dir | Out-Null }
$report | ConvertTo-Json -Depth 8 | Set-Content -Path $OutFile -Encoding UTF8

$mdPath = [System.IO.Path]::ChangeExtension($OutFile, ".md")
$md = @()
$md += "# Code-Agent LLM Stress Report"
$md += ""
$md += "- **time**: $($report.generated_at)"
$md += "- **model**: ``$($env:LLM_MODEL)``"
$md += "- **api_base**: ``$($env:LLM_API_BASE)``"
$md += "- **score**: **$pass / $total** (pass_rate=**$rate**)"
$md += "- **elapsed_ms**: $elapsed"
$md += ""
$md += "| Case | Result | ms | Detail |"
$md += "|------|--------|----|--------|"
foreach ($c in $results) {
  $st = if ($c.ok) { "PASS" } else { "FAIL" }
  $md += "| $($c.name) | $st | $($c.latency_ms) | $($c.detail -replace '\|','/') |"
}
$md += ""
$md += "## DeepAgent vs Teams"
$md += ""
$md += "See docs/deepagent-vs-teams.md. Stress cases ``chat_team_mode`` and ``chat_deep_mode`` exercise both routes under the real model."
$md += ""
$md += $(if ($rate -ge 0.7) { "**Overall: PASS** (threshold 0.7 for real LLM)" } else { "**Overall: FAIL**" })
$md -join "`n" | Set-Content -Path $mdPath -Encoding UTF8

Write-Host ""
Write-Host ("SCORE {0}/{1} pass_rate={2} elapsed={3}ms" -f $pass,$total,$rate,$elapsed) -ForegroundColor Cyan
Write-Host "JSON: $OutFile"
Write-Host "MD:   $mdPath"

if ($serverProc -and -not $serverProc.HasExited) {
  Stop-Process -Id $serverProc.Id -Force -ErrorAction SilentlyContinue
  Write-Host "stopped server"
}

if ($rate -ge 0.7) { exit 0 } else { exit 1 }
