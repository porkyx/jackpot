param([switch]$SkipBuild)
$ErrorActionPreference='Stop'
$workspace=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$node=Join-Path $env:LOCALAPPDATA 'Volta/tools/image/node/22.16.0/node.exe'
$executable=Join-Path $workspace '.task/product-reservation-restart.exe'
$driver=Join-Path $workspace 'frontend/tests/native/reservationrestart.driver.mjs'
Push-Location $workspace
try{
 if(-not(Test-Path -LiteralPath $node)){throw 'Pinned Node22.16.0 runtime missing'}
 & $node --test (Join-Path $workspace 'frontend/tests/native/reservationrestart.invariants.test.mjs')
 if($LASTEXITCODE -ne 0){throw 'Reservation restart independent invariant guards failed'}
 & $node --check $driver
 if($LASTEXITCODE -ne 0){throw 'Reservation restart driver syntax failed'}
 if(-not $SkipBuild){& go build -tags production -o $executable ./cmd/producte2e;if($LASTEXITCODE -ne 0){throw 'Independent native reservation host build failed'}}
 & $node $driver
 if($LASTEXITCODE -ne 0){throw 'Native reservation scheduled-Go-restart E2E failed'}
 $report=[IO.File]::ReadAllText((Join-Path $workspace '.task/reservation-restart-report.json'))|ConvertFrom-Json
 if($report.status -ne 'passed' -or $report.checks.Count -ne 8 -or $report.runs.Count -ne 2 -or $report.ownedProcessesExited -ne $true -or $report.dbProfileExclusiveAndRemoved -ne $true -or $report.forcedKillClaim -ne $false){throw 'Native reservation restart evidence/cleanup incomplete'}
 Write-Output 'Native reservation restart PASS: collect -> local scheduled -> actual Go exit -> same DB restart -> UTC delayed due1 -> direct distinct Rerun; owned PID/profile/DB cleanup'
}finally{Pop-Location}