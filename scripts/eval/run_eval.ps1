# run_eval.ps1 — automated regression evaluation for code-agent.
#
# Runs the data-driven intent-routing + coreference regression suite (and a
# build/vet gate) so routing or coref changes cannot silently regress.
#
# Usage:
#   pwsh scripts/eval/run_eval.ps1            # run all eval checks
#   pwsh scripts/eval/run_eval.ps1 -OnlyTest  # skip build/vet (faster iteration)
#
# Exit code is non-zero when any check fails (CI-friendly).

param(
    [switch]$OnlyTest
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path

function Step([string]$name) {
    Write-Host ""
    Write-Host "==> $name" -ForegroundColor Cyan
}

$ok = $true

if (-not $OnlyTest) {
    Step "go build ./..."
    Push-Location $root
    try {
        go build ./... 2>&1 | ForEach-Object { Write-Host $_ }
        if ($LASTEXITCODE -ne 0) { $ok = $false }
    } finally { Pop-Location }

    Step "go vet ./internal/domain/intent/..."
    Push-Location $root
    try {
        go vet ./internal/domain/intent/... 2>&1 | ForEach-Object { Write-Host $_ }
        if ($LASTEXITCODE -ne 0) { $ok = $false }
    } finally { Pop-Location }
}

Step "go test - intent regression suite"
Push-Location $root
try {
    go test ./internal/domain/intent/... -run 'TestRegression' -v 2>&1 | ForEach-Object { Write-Host $_ }
    if ($LASTEXITCODE -ne 0) { $ok = $false }
} finally { Pop-Location }

if ($ok) {
    Write-Host ""
    Write-Host "EVAL PASSED" -ForegroundColor Green
    exit 0
} else {
    Write-Host ""
    Write-Host "EVAL FAILED" -ForegroundColor Red
    exit 1
}
