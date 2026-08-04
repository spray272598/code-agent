# One-shot local try: mock LLM + server + optional interactive CLI
#   powershell -File scripts/try_cli.ps1
#   powershell -File scripts/try_cli.ps1 -RealLLM   # needs LLM_API_KEY
#   powershell -File scripts/try_cli.ps1 -SmokeOnly # non-interactive health + one chat

param(
  [switch]$RealLLM,
  [switch]$SmokeOnly,
  [string]$Config = "configs/config.yaml",
  [string]$Key = "dev-key",
  [string]$Base = "http://127.0.0.1:8080"
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $Root

if ($RealLLM) {
  if (-not $env:LLM_API_KEY) {
    Write-Host "FAIL: set LLM_API_KEY for -RealLLM" -ForegroundColor Red
    exit 2
  }
  $env:LLM_USE_MOCK = "false"
  if (-not $env:LLM_API_BASE -and $env:LLM_BASE_URL) { $env:LLM_API_BASE = $env:LLM_BASE_URL }
} else {
  $env:LLM_USE_MOCK = "true"
  $env:DB_TYPE = if ($env:DB_TYPE) { $env:DB_TYPE } else { "memory" }
  $env:REDIS_ENABLED = if ($env:REDIS_ENABLED) { $env:REDIS_ENABLED } else { "false" }
}

New-Item -ItemType Directory -Force -Path bin | Out-Null
Write-Host "building server + cli ..." -ForegroundColor Cyan
go build -o bin/server.exe ./cmd/server
go build -o bin/cli.exe ./cmd/cli

$needStart = $true
try {
  $null = Invoke-RestMethod -Uri "$Base/health" -Headers @{ "X-API-Key" = $Key } -TimeoutSec 2
  Write-Host "server already up at $Base"
  $needStart = $false
} catch {}

$proc = $null
if ($needStart) {
  Write-Host "starting server (config=$Config mock=$($env:LLM_USE_MOCK)) ..." -ForegroundColor Yellow
  $proc = Start-Process -FilePath ".\bin\server.exe" -ArgumentList "-config",$Config -PassThru -WindowStyle Hidden
  $ok = $false
  for ($i = 0; $i -lt 30; $i++) {
    Start-Sleep -Milliseconds 400
    try {
      $null = Invoke-RestMethod -Uri "$Base/health" -Headers @{ "X-API-Key" = $Key } -TimeoutSec 1
      $ok = $true
      break
    } catch {}
  }
  if (-not $ok) {
    Write-Host "server failed to become healthy" -ForegroundColor Red
    if ($proc) { Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue }
    exit 1
  }
  Write-Host "server ready" -ForegroundColor Green
}

if ($SmokeOnly) {
  Write-Host "=== smoke chat ===" -ForegroundColor Cyan
  $h = @{ "X-API-Key" = $Key; "Content-Type" = "application/json" }
  $sess = Invoke-RestMethod -Uri "$Base/api/v1/session" -Method POST -Headers $h -Body (@{ userId = "smoke"; title = "smoke" } | ConvertTo-Json)
  $sid = $sess.data.sessionId
  $chat = Invoke-RestMethod -Uri "$Base/api/v1/chat" -Method POST -Headers $h -Body (@{
    sessionId = $sid; userId = "smoke"; message = "say hi in one short sentence"; autoApprove = $true
  } | ConvertTo-Json)
  Write-Host "session=$sid"
  Write-Host "response=$($chat.data.response)"
  if ($proc) { Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue }
  Write-Host "smoke OK" -ForegroundColor Green
  exit 0
}

Write-Host "launching CLI (Ctrl+C to exit; /help for commands)" -ForegroundColor Cyan
try {
  & .\bin\cli.exe --base $Base --key $Key
} finally {
  if ($proc) {
    Write-Host "stopping server pid=$($proc.Id)"
    Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
  }
}
