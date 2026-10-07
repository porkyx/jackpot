param([switch]$SkipBuild,[switch]$PrepareOnly)
$ErrorActionPreference='Stop'
$projectRoot=[IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
$taskRoot=Join-Path $projectRoot '.task'
$node=Join-Path $env:LOCALAPPDATA 'Volta/tools/image/node/22.16.0/node.exe'
$driver=Join-Path $projectRoot 'frontend/tests/native/webview-recovery.driver.mjs'
$executable=Join-Path $taskRoot 'product-webview-recovery.exe'
Push-Location $projectRoot
try {
 if(-not(Test-Path -LiteralPath $node)){throw 'Pinned Node22.16.0 runtime not found'}
 & $node --test (Join-Path $projectRoot 'frontend/tests/native/webview-recovery.guards.test.mjs')
 if($LASTEXITCODE -ne 0){throw 'Native ownership/recovery invariants failed'}
 & $node --check $driver
 if($LASTEXITCODE -ne 0){throw 'Recovery driver syntax failed'}
 if(-not $SkipBuild){& go build -tags production -o $executable ./cmd/producte2e;if($LASTEXITCODE -ne 0){throw 'Actual product host build failed'}}
 if(-not(Test-Path -LiteralPath $executable)){throw 'Recovery host executable missing'}
 if($PrepareOnly){Write-Output 'Recovery host/helper ready; no native failure injected';return}
 & $node $driver
 if($LASTEXITCODE -ne 0){throw 'Actual WebView recovery failed; inspect .task/webview-recovery-report.json'}
 $report=[IO.File]::ReadAllText((Join-Path $taskRoot 'webview-recovery-report.json'))|ConvertFrom-Json
 if($report.status -ne 'passed' -or $report.recoveries.Count -ne 4 -or $report.ownedProcessesExited -ne $true -or $report.profileExclusiveOpenAndRemoved -ne $true){throw 'Native recovery/cleanup gate incomplete'}
 Write-Output 'Native WebView-only PASS:3 owned renderer failures +1 owned browser controller replacement; same Go session/draft/results/authority; owned process/profile cleanup'
} finally {Pop-Location}