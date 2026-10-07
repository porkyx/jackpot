param([string]$NodePath='node')
$ErrorActionPreference='Stop'
$projectRoot=[IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
Push-Location $projectRoot
try {
    Push-Location (Join-Path $projectRoot 'frontend')
    try {
        & $NodePath node_modules/typescript/bin/tsc --noEmit
        if($LASTEXITCODE -ne 0){throw 'Product TypeScript failed'}
        & $NodePath scripts/policy.mjs
        if($LASTEXITCODE -ne 0){throw 'Product import policy failed'}
        & $NodePath node_modules/vite/bin/vite.js build
        if($LASTEXITCODE -ne 0){throw 'Product frontend build failed'}
    } finally {Pop-Location}
    & go build -tags production -o .task/producte2e.exe ./cmd/producte2e
    if($LASTEXITCODE -ne 0){throw 'Product native verification host build failed'}
    & $NodePath frontend/tests/native/product.driver.mjs
    if($LASTEXITCODE -ne 0){throw 'Actual product native E2E failed'}
    $report=Get-Content -LiteralPath .task/product-e2e-report.json -Raw -Encoding UTF8 | ConvertFrom-Json
    if($report.status -ne 'passed' -or $report.checks.Count -lt 15 -or $report.cleanup -ne 'isolated process/profile/DB removed'){throw 'Product native E2E evidence/cleanup incomplete'}
    Write-Host 'Actual isolated Wails product E2E passed. HTTP delivery, clipboard/browser and save selection boundaries remain explicitly documented.'
} finally {Pop-Location}