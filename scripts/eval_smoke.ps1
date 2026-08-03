# Phase 6 smoke eval for code-agent (Windows PowerShell)
# Usage: start server first, then:
#   powershell -File scripts/eval_smoke.ps1

$ErrorActionPreference = "Stop"
$base = if ($env:CODE_AGENT_BASE) { $env:CODE_AGENT_BASE } else { "http://127.0.0.1:8080" }
$key = if ($env:CODE_AGENT_API_KEY) { $env:CODE_AGENT_API_KEY } else { "dev-key" }
$h = @{ "X-API-Key" = $key; "Content-Type" = "application/json" }

function Pass($name) { Write-Host "PASS $name" -ForegroundColor Green }
function Fail($name, $msg) { Write-Host "FAIL $name : $msg" -ForegroundColor Red; exit 1 }

Write-Host "eval against $base"

try {
  $health = Invoke-RestMethod -Uri "$base/health" -Headers $h
  if ($health.status -ne "ok") { Fail "health" $health }
  Pass "health"
} catch { Fail "health" $_ }

$tools = Invoke-RestMethod -Uri "$base/api/v1/tools" -Headers $h
$names = @($tools.data | ForEach-Object { $_.name })
foreach ($need in @("read_file","write_file","edit_file","bash","glob","grep","memory_save","memory_search","delegate")) {
  if ($names -notcontains $need) { Fail "tools" "missing $need" }
}
Pass "tools($($names.Count))"

$skills = Invoke-RestMethod -Uri "$base/api/v1/skills" -Headers $h
Pass "skills($($skills.data.Count))"

$sess = Invoke-RestMethod -Uri "$base/api/v1/session" -Method POST -Headers $h -Body '{"userId":"eval","title":"eval"}'
$sid = $sess.data.sessionId
if (-not $sid) { Fail "session" "no id" }
Pass "session"

$body = @{ sessionId = $sid; userId = "eval"; message = "list files"; autoApprove = $true } | ConvertTo-Json
$chat = Invoke-RestMethod -Uri "$base/api/v1/chat" -Method POST -Headers $h -Body $body
if (-not $chat.data.response) { Fail "chat" "empty response" }
Pass "chat(toolCalls=$($chat.data.toolCalls))"

$mem = @{ userId = "eval"; scope = "user"; content = "eval prefers mock"; importance = 70 } | ConvertTo-Json
Invoke-RestMethod -Uri "$base/api/v1/memory" -Method POST -Headers $h -Body $mem | Out-Null
$list = Invoke-RestMethod -Uri "$base/api/v1/memory?userId=eval" -Headers $h
if ($list.data.Count -lt 1) { Fail "memory" "empty" }
Pass "memory"

$metrics = Invoke-RestMethod -Uri "$base/api/v1/metrics" -Headers $h
Pass "metrics(chat=$($metrics.data.chat_total))"

try {
  $prom = Invoke-WebRequest -Uri "$base/metrics" -UseBasicParsing
  if ($prom.Content -notmatch "code_agent_chat_total") { Fail "prometheus" "missing metric" }
  Pass "prometheus"
} catch { Fail "prometheus" $_ }

$slash = Invoke-RestMethod -Uri "$base/api/v1/chat" -Method POST -Headers $h -Body (@{userId="eval";message="/help";autoApprove=$true} | ConvertTo-Json)
if (-not $slash.data.slash) { Fail "slash" "not handled" }
Pass "slash"

Write-Host "ALL EVAL PASSED" -ForegroundColor Cyan
