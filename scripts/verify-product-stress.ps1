param([switch]$SkipBuild,[switch]$PrepareOnly,[ValidateRange(1,8)][int]$DrawSamples=3,[ValidateRange(1,100)][int]$Iterations=20,[ValidateSet(100,200)][int]$FixtureComments=200)
$ErrorActionPreference='Stop'
if($FixtureComments -eq 100 -and $DrawSamples -gt 3){throw '100-comment fixture supports at most three reruns'}
$priorDrawSamples=$env:JACKPOT_STRESS_DRAW_SAMPLES
$priorIterations=$env:JACKPOT_STRESS_ITERATIONS
$priorFixtureComments=$env:JACKPOT_STRESS_FIXTURE_COMMENTS
$projectRoot=[IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
$taskRoot=Join-Path $projectRoot '.task'
$node=Join-Path $env:LOCALAPPDATA 'Volta/tools/image/node/22.16.0/node.exe'
$executable=Join-Path $taskRoot 'product-stress.exe'
$driver=Join-Path $projectRoot 'frontend/tests/native/product.stress.mjs'
if(-not(Test-Path -LiteralPath $node)){throw 'Pinned Node22.16.0 runtime not found'}
if(-not(Test-Path -LiteralPath $taskRoot)){New-Item -ItemType Directory -Path $taskRoot|Out-Null}
Push-Location $projectRoot
try {
 $env:JACKPOT_STRESS_ITERATIONS=[string]$Iterations
 $env:JACKPOT_STRESS_DRAW_SAMPLES=[string]$DrawSamples
 $env:JACKPOT_STRESS_FIXTURE_COMMENTS=[string]$FixtureComments
 & $node --check $driver
 if($LASTEXITCODE -ne 0){throw 'Stress driver syntax failed'}
 & $node --test (Join-Path $projectRoot 'frontend/tests/native/stress.metrics.test.mjs') (Join-Path $projectRoot 'frontend/tests/native/stress.trace.test.mjs') (Join-Path $projectRoot 'frontend/tests/native/stress.media.test.mjs') (Join-Path $projectRoot 'frontend/tests/native/stress.preview.test.mjs') (Join-Path $projectRoot 'frontend/tests/native/stress.noimage.test.mjs') (Join-Path $projectRoot 'frontend/tests/native/stress.workload.test.mjs')
 if($LASTEXITCODE -ne 0){throw 'Stress metric invariant tests failed'}
 if(-not $SkipBuild) {
  & go test ./cmd/producte2e -run 'TestLocal|TestLarge|TestFixtureNetwork|TestFixturePage2|TestFixtureBlocked|TestProductE2EFixture' -count=1
  if($LASTEXITCODE -ne 0){throw 'Production-parser fixture tests failed'}
  & go build -tags production -o $executable ./cmd/producte2e
  if($LASTEXITCODE -ne 0){throw 'Stress host build failed'}
 }
 if(-not(Test-Path -LiteralPath $executable)){throw 'Stress host executable missing'}
 if($PrepareOnly){Write-Output 'Stress host and driver ready; native timed run was not started';return}
 if(-not(Test-Path -LiteralPath (Join-Path $projectRoot 'frontend/dist/index.html'))){throw 'Build production frontend/dist first'}
 $evidence=Get-CimInstance Win32_ComputerSystem
 $cpu=Get-CimInstance Win32_Processor|Select-Object Name,NumberOfCores,NumberOfLogicalProcessors
 $disk=Get-PhysicalDisk -ErrorAction SilentlyContinue|Select-Object MediaType,Size
 [IO.File]::WriteAllText((Join-Path $taskRoot 'product-stress-device.json'),(@{timestamp=[DateTime]::UtcNow.ToString('o');ramBytes=$evidence.TotalPhysicalMemory;processor=$cpu;disk=$disk;os=[Environment]::OSVersion.VersionString}|ConvertTo-Json -Depth 5),[Text.UTF8Encoding]::new($false))
 & $node $driver
 if($LASTEXITCODE -ne 0){throw 'Actual native stress failed; inspect .task/product-stress-report.json'}
 $report=[IO.File]::ReadAllText((Join-Path $taskRoot 'product-stress-report.json'))|ConvertFrom-Json
 if($report.status -ne 'passed' -or $report.iterations -ne $Iterations -or $report.ownedProcessesExited -ne $true){throw 'Stress success/resource gate missing'}
 Write-Output ('Native local functional PASS; memoryLimitMiB='+$report.targets.memoryLimitMiB+'; peakWithinMemoryGoal='+$report.targets.peakWithinMemoryGoal+'; historicalPeakUnder500MiB='+$report.targets.peakUnder500MiB+'; peakMiB='+$report.peakWorkingSetMiB+'; searchP95Ms='+$report.searchP95Ms+'; observedDrawP95Ms='+$report.drawP95Ms)
} finally {
 $env:JACKPOT_STRESS_DRAW_SAMPLES=$priorDrawSamples
 $env:JACKPOT_STRESS_ITERATIONS=$priorIterations
 $env:JACKPOT_STRESS_FIXTURE_COMMENTS=$priorFixtureComments
 Pop-Location
}
