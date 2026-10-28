# load-tests/run_load_tests.ps1
# Automated runner script for GelatoFlow k6 load tests.

param (
    [ValidateSet("all", "average", "spike")]
    [string]$Scenario = "all",
    [string]$GatewayUrl = "http://localhost:8080"
)

$ErrorActionPreference = "Stop"

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "  GelatoFlow Backend k6 Load Testing Suite" -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

# 1. Locate k6 executable
$k6Cmd = Get-Command "k6" -ErrorAction SilentlyContinue
if (-not $k6Cmd) {
    if (Test-Path "C:\Program Files\k6\k6.exe") {
        $k6Path = "C:\Program Files\k6\k6.exe"
    } else {
        Write-Error "k6 executable not found. Please ensure k6 is installed."
        exit 1
    }
} else {
    $k6Path = $k6Cmd.Source
}

Write-Host "Using k6: $k6Path" -ForegroundColor Gray

# 2. Verify API Gateway connectivity
Write-Host "Checking API Gateway at $GatewayUrl/health ..." -NoNewline
try {
    $health = Invoke-RestMethod -Uri "$GatewayUrl/health" -Method Get -TimeoutSec 5 -ErrorAction Stop
    Write-Host " [ONLINE]" -ForegroundColor Green
} catch {
    Write-Host " [FAILED]" -ForegroundColor Red
    Write-Warning "API Gateway at $GatewayUrl is not reachable. Ensure docker compose is up."
    exit 1
}

# Ensure reports directory exists
$reportsDir = Join-Path $PSScriptRoot "reports"
if (-not (Test-Path $reportsDir)) {
    New-Item -ItemType Directory -Path $reportsDir | Out-Null
}

$env:GATEWAY_URL = $GatewayUrl

function Run-Scenario {
    param (
        [string]$Name,
        [string]$ScriptPath,
        [string]$ReportFile
    )
    Write-Host "`n>>> Running: $Name ($ScriptPath) ..." -ForegroundColor Yellow
    $fullScriptPath = Join-Path $PSScriptRoot $ScriptPath
    
    & $k6Path run $fullScriptPath
    $exitCode = $LASTEXITCODE

    $fullReportPath = Join-Path $reportsDir $ReportFile
    if (Test-Path $fullReportPath) {
        Write-Host "Saved JSON report: $fullReportPath" -ForegroundColor Green
    } else {
        Write-Host "Warning: JSON report was not generated at $fullReportPath" -ForegroundColor DarkYellow
    }

    return $exitCode
}

$exitCodes = @()

if ($Scenario -eq "all" -or $Scenario -eq "average") {
    $code = Run-Scenario -Name "Type 1: Average Load (Catalog & Availability Read)" `
                         -ScriptPath "scenarios/01_average_load_read.js" `
                         -ReportFile "summary-average-load.json"
    $exitCodes += $code
}

if ($Scenario -eq "all" -or $Scenario -eq "spike") {
    $code = Run-Scenario -Name "Type 2: Spike / Stress (Preorder Checkout Contention)" `
                         -ScriptPath "scenarios/02_spike_stress_order.js" `
                         -ReportFile "summary-spike-stress.json"
    $exitCodes += $code
}

Write-Host "`n==========================================================" -ForegroundColor Cyan
Write-Host "  Load Testing Completed" -ForegroundColor Cyan
Write-Host "  Reports stored in: $reportsDir" -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

$failedRuns = ($exitCodes | Where-Object { $_ -ne 0 }).Count
if ($failedRuns -gt 0) {
    Write-Host "One or more scenarios violated configured SLA thresholds." -ForegroundColor Yellow
} else {
    Write-Host "All scenarios met configured SLA thresholds!" -ForegroundColor Green
}
