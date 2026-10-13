<#
.SYNOPSIS
    Automated Runner Script for GelatoFlow Python E2E Integration Tests.

.DESCRIPTION
    Validates Python environment, checks or starts Docker services, waits for API Gateway
    healthiness, executes pytest test suites, and reports execution summary.

.PARAMETER StartContainers
    If specified, launches Docker Compose stack (compose.yml + compose.dev.yml) before running tests.

.PARAMETER StopContainers
    If specified, tears down Docker Compose containers after tests complete.

.PARAMETER PytestArgs
    Additional arguments passed directly to pytest (e.g. "-k uc01", "-m nfr02", "-v").

.EXAMPLE
    .\tests\run_tests.ps1
    .\tests\run_tests.ps1 -StartContainers
    .\tests\run_tests.ps1 -PytestArgs "-k test_happy_path"
#>

[CmdletBinding()]
param(
    [switch]$StartContainers,
    [switch]$StopContainers,
    [string]$GatewayUrl = "http://localhost:8080",
    [string]$PytestArgs = ""
)

$ErrorActionPreference = "Stop"

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "  GelatoFlow Python E2E Integration Testing Suite Runner  " -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

# 1. Check Python
try {
    $pythonVersion = python --version 2>&1
    Write-Host "[1/5] Python runtime detected: $pythonVersion" -ForegroundColor Green
} catch {
    Write-Error "Python is not installed or not available in PATH. Please install Python 3.10+."
    exit 1
}

# 2. Check and Install requirements
Write-Host "[2/5] Checking Python test requirements..." -ForegroundColor Yellow
python -m pip install -r tests/requirements.txt --quiet --disable-pip-version-check
if ($LASTEXITCODE -ne 0) {
    Write-Warning "Failed to install some requirements. Attempting to proceed with existing environment."
} else {
    Write-Host "      Requirements verified." -ForegroundColor Green
}

# 3. Optional: Start Docker containers
if ($StartContainers) {
    Write-Host "[3/5] Starting GelatoFlow Docker Compose stack..." -ForegroundColor Yellow
    docker compose --env-file infra/.env.example -f infra/docker/compose.yml -f infra/docker/compose.dev.yml up -d
    if ($LASTEXITCODE -ne 0) {
        Write-Error "Docker Compose failed to start services."
        exit 1
    }
} else {
    Write-Host "[3/5] Skipping Docker startup (use -StartContainers to boot stack automatically)." -ForegroundColor Gray
}

# 4. Wait for API Gateway Health
Write-Host "[4/5] Checking API Gateway availability at $GatewayUrl/health..." -ForegroundColor Yellow
$maxAttempts = 15
$attempt = 0
$gatewayReady = $false

while ($attempt -lt $maxAttempts) {
    $attempt++
    try {
        $response = Invoke-RestMethod -Uri "$GatewayUrl/health" -Method Get -TimeoutSec 2 -ErrorAction SilentlyContinue
        if ($response.status -eq "ok" -or $response.message -like "*running*") {
            $gatewayReady = $true
            Write-Host "      API Gateway is healthy and responsive! (Attempt $attempt)" -ForegroundColor Green
            break
        }
    } catch {
        # Retry
    }
    Start-Sleep -Seconds 1
}

if (-not $gatewayReady) {
    Write-Warning "API Gateway at $GatewayUrl is not reachable. Tests requiring live containers will be skipped gracefully."
}

# 5. Execute Pytest
Write-Host "[5/5] Executing Pytest test suite..." -ForegroundColor Yellow
$env:GATEWAY_URL = $GatewayUrl

$command = "python -m pytest tests/ -v $PytestArgs"
Write-Host "Running: $command" -ForegroundColor Cyan
Invoke-Expression $command
$testExitCode = $LASTEXITCODE

# 6. Optional: Stop containers
if ($StopContainers) {
    Write-Host "Stopping Docker Compose stack..." -ForegroundColor Yellow
    docker compose --env-file infra/.env.example -f infra/docker/compose.yml -f infra/docker/compose.dev.yml down
}

Write-Host "==========================================================" -ForegroundColor Cyan
if ($testExitCode -eq 0) {
    Write-Host "  ALL TESTS PASSED SUCCESSFULLY!                          " -ForegroundColor Green
} else {
    Write-Host "  TEST EXECUTION COMPLETED WITH CODE: $testExitCode       " -ForegroundColor Red
}
Write-Host "==========================================================" -ForegroundColor Cyan

exit $testExitCode
