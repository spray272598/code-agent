# One-click local Coding Agent: Server (prefer_host) + Host-Agent on your repo.
#
# Usage (from repo root):
#   powershell -File scripts/dev_local.ps1
#   powershell -File scripts/dev_local.ps1 -Workspace D:\your\project -RealLLM
#   powershell -File scripts/dev_local.ps1 -NoHost   # server only
#
# Env: CODE_AGENT_API_KEY, LLM_API_KEY, LLM_API_BASE, LLM_MODEL

param(
  [string]$Workspace = "",
  [string]$Config = "configs/config.host.yaml",
  [string]$Base = "http://127.0.0.1:8080",
  [string]$Token = "",
  [switch]$RealLLM,
  [switch]$NoHost,
  [switch]$NoBuild
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $Root

if (-not $Token) {
  $Token = if ($env:CODE_AGENT_API_KEY) { $env:CODE_AGENT_API_KEY } else { "dev-key" }
}
if (-not $Workspace) {
  $Workspace = Join-Path $Root "workspace"
  if (-not (Test-Path $Workspace)) { New-Item -ItemType Directory -Path $Workspace | Out-Null }
}
$Workspace = (Resolve-Path $Workspace).Path

Write-Host "=== code-agent local demo ===" -ForegroundColor Cyan
Write-Host "root=$Root"
Write-Host "workspace=$Workspace"
Write-Host "config=$Config prefer_host=true"

if (-not $NoBuild) {
  Write-Host "building server + host-agent + cli ..." -ForegroundColor Yellow
  New-Item -ItemType Directory -Force -Path (Join-Path $Root "bin") | Out-Null
  go build -o bin/server.exe ./cmd/server
  go build -o bin/host-agent.exe ./cmd/host-agent
  go build -o bin/cli.exe ./cmd/cli
}

if ($RealLLM) {
  if (-not $env:LLM_API_KEY) {
    Write-Host "WARN: -RealLLM but LLM_API_KEY empty; Native Loop will run with mock LLM" -ForegroundColor Yellow
  } else {
    $env:LLM_USE_MOCK = "false"
    Write-Host "LLM: real key set, orchestrator=eino expected" -ForegroundColor Green
  }
}

# free port check
try {
  $null = Invoke-WebRequest -Uri "$Base/health" -UseBasicParsing -TimeoutSec 1 -ErrorAction SilentlyContinue
  Write-Host "server already up at $Base" -ForegroundColor Yellow
  $serverJob = $null
} catch {
  Write-Host "starting server ..." -ForegroundColor Yellow
  $serverJob = Start-Process -FilePath (Join-Path $Root "bin\server.exe") `
    -ArgumentList @("-config", $Config) `
    -WorkingDirectory $Root `
    -PassThru -WindowStyle Minimized
  Start-Sleep -Seconds 2
}

# wait health
$ok = $false
for ($i = 0; $i -lt 20; $i++) {
  try {
    $h = Invoke-RestMethod -Uri "$Base/health" -Headers @{ "X-API-Key" = $Token } -TimeoutSec 2
    if ($h.status -eq "ok") { $ok = $true; break }
  } catch { Start-Sleep -Milliseconds 500 }
}
if (-not $ok) {
  Write-Host "FAIL: server health not ready" -ForegroundColor Red
  if ($serverJob) { Stop-Process -Id $serverJob.Id -Force -ErrorAction SilentlyContinue }
  exit 1
}
Write-Host "PASS server health" -ForegroundColor Green

$hostJob = $null
if (-not $NoHost) {
  Write-Host "starting host-agent workspace=$Workspace ..." -ForegroundColor Yellow
  $hostJob = Start-Process -FilePath (Join-Path $Root "bin\host-agent.exe") `
    -ArgumentList @(
      "--server", "ws://127.0.0.1:8080/ws/host",
      "--token", $Token,
      "--workspace", $Workspace,
      "--device", "local-dev",
      "--reconnect"
    ) `
    -WorkingDirectory $Root `
    -PassThru -WindowStyle Minimized
  Start-Sleep -Seconds 2
  try {
    $dev = Invoke-RestMethod -Uri "$Base/api/v1/host/devices" -Headers @{ "X-API-Key" = $Token }
    $n = 0
    if ($dev.data.online) { $n = [int]$dev.data.online }
    Write-Host "host devices online=$n" -ForegroundColor $(if ($n -gt 0) { "Green" } else { "Yellow" })
    if ($n -eq 0) {
      Write-Host "WARN: host not online yet; tools will fallback to server workspace" -ForegroundColor Yellow
    }
  } catch {
    Write-Host "WARN: cannot query host devices: $_" -ForegroundColor Yellow
  }
}

Write-Host ""
Write-Host "=== ready ===" -ForegroundColor Cyan
Write-Host "  CLI:  .\bin\cli.exe --base $Base --key $Token"
Write-Host "  Eval: powershell -File scripts/eval_report.ps1"
Write-Host "  Devices: curl -H `"X-API-Key: $Token`" $Base/api/v1/host/devices"
Write-Host "  Docs: docs/local-demo.md"
Write-Host ""
Write-Host "Press Ctrl+C to stop children, or close this window and kill server/host-agent PIDs." -ForegroundColor DarkGray

# keep script alive so user can Ctrl+C to cleanup
try {
  while ($true) {
    Start-Sleep -Seconds 5
    if ($serverJob -and $serverJob.HasExited) {
      Write-Host "server exited code=$($serverJob.ExitCode)" -ForegroundColor Red
      break
    }
  }
} finally {
  if ($hostJob -and -not $hostJob.HasExited) {
    Stop-Process -Id $hostJob.Id -Force -ErrorAction SilentlyContinue
    Write-Host "stopped host-agent"
  }
  if ($serverJob -and -not $serverJob.HasExited) {
    Stop-Process -Id $serverJob.Id -Force -ErrorAction SilentlyContinue
    Write-Host "stopped server"
  }
}
