$ErrorActionPreference='Stop'
$projectRoot=[IO.Path]::GetFullPath((Split-Path $PSScriptRoot -Parent))
$taskRoot=Join-Path $projectRoot '.task'
$runRoot=Join-Path $taskRoot ('wake-mutations-'+[Guid]::NewGuid().ToString('N'))
$sourcePath=Join-Path $projectRoot 'internal/scheduler/scheduler.go'
$testPath=Join-Path $projectRoot 'internal/scheduler/wake_test.go'
$sourceHash=(Get-FileHash -LiteralPath $sourcePath -Algorithm SHA256).Hash
$pollTestPath=Join-Path $projectRoot 'internal/scheduler/timer_poll_test.go'
$pollTestHash=(Get-FileHash -LiteralPath $pollTestPath -Algorithm SHA256).Hash
$testHash=(Get-FileHash -LiteralPath $testPath -Algorithm SHA256).Hash
$original=[IO.File]::ReadAllText($sourcePath)
$cases=@(
 @{name='native-wake-ignored';old="func waitForWake(ctx context.Context, clock appclock.Clock, wake <-chan struct{}) (wakeReason, error) {";new="func waitForWake(ctx context.Context, clock appclock.Clock, wake <-chan struct{}) (wakeReason, error) {`n wake = nil";test='TestOSWake'},
  @{name='startup-must-use-full';old='reason := wakeSignal';new='reason := wakeTimer';test='TestTimerPollStartup'},
 @{name='timer-routing-must-check-reason';old='if timerPolling && reason == wakeTimer {';new='if timerPolling {';test='TestTimerPollStartup'},
 @{name='supported-timer-must-use-poll';old='if timerPolling && reason == wakeTimer {';new='if timerPolling && reason == wakeTimer && false {';test='TestTimerPollStartup'},
 @{name='native-signal-remains-full';old='return wakeSignal, ctx.Err()';new='return wakeTimer, ctx.Err()';test='TestTimerPollStartup'},
 @{name='timer-signal-remains-poll';old="case <-channel:`n`t`treturn wakeTimer, ctx.Err()";new="case <-channel:`n`t`treturn wakeSignal, ctx.Err()";test='TestTimerPollStartup'},
 @{name='coalesced-capacity';old='wake := make(chan struct{}, 1)';new='wake := make(chan struct{}, 2)';test='TestWakeSignal'},
 @{name='closed-wake-fatal';old="if reason == wakeClosed {`n`t`t`twake = nil";new="if reason == wakeClosed {`n`t`t`treturn appclock.ErrInvalidTimer";test='TestClosedWake'},
 @{name='timer-leak';old='defer timer.Stop()';new='// removed timer stop';test='Test(OSWake|SchedulerReports)'},
 @{name='cancel-before-timer';old="if err := ctx.Err(); err != nil {`n`t`treturn wakeTimer, err";new="if false {`n`t`treturn wakeTimer, ctx.Err()";test='TestSchedulerCancellation'}
)
$results=New-Object 'System.Collections.Generic.List[object]'
Push-Location $projectRoot
try {
 New-Item -ItemType Directory -Path $runRoot|Out-Null
 & go test ./internal/scheduler -run 'Test(OSWake|Wake|ClosedWake|Scheduler|TimerPoll)' -race -count=1 '-coverprofile=.task/scheduler-wake.cover'
 if($LASTEXITCODE -ne 0){throw 'Wake mutation baseline failed'}
 foreach($case in $cases){
  $normal=$original.Replace("`r`n","`n")
  if(([regex]::Matches($normal,[regex]::Escape($case.old))).Count -ne 1){throw ('Wake mutation source marker must occur once: '+$case.name)}
  $mutant=Join-Path $runRoot ($case.name+'.go')
  [IO.File]::WriteAllText($mutant,$normal.Replace($case.old,$case.new),[Text.UTF8Encoding]::new($false))
  $overlay=Join-Path $runRoot 'overlay.json'
  $replace=@{};$replace[$sourcePath]=$mutant
  [IO.File]::WriteAllText($overlay,(@{Replace=$replace}|ConvertTo-Json -Depth 3 -Compress),[Text.UTF8Encoding]::new($false))
  $output=& go test -overlay $overlay ./internal/scheduler -run $case.test -count=1 -timeout 15s 2>&1
  $exit=$LASTEXITCODE;$text=$output -join "`n"
  if($exit -eq 0 -or $text -notmatch '--- FAIL:' -or $text -match 'build failed|undefined:|timed out|panic:|fatal error:'){throw ('Wake mutant did not produce an independent assertion failure: '+$case.name+"`n"+$text)}
  $results.Add(@{name=$case.name;status='assertion-killed'})
  Write-Host ('ASSERTION KILL '+$case.name)
 }
 & go tool cover '-func=.task/scheduler-wake.cover'
 if($LASTEXITCODE -ne 0){throw 'Wake coverage analysis failed'}
} finally {
 try {
  if((Get-FileHash -LiteralPath $sourcePath -Algorithm SHA256).Hash -ne $sourceHash -or (Get-FileHash -LiteralPath $testPath -Algorithm SHA256).Hash -ne $testHash -or (Get-FileHash -LiteralPath $pollTestPath -Algorithm SHA256).Hash -ne $pollTestHash){throw 'Wake source or regression test changed during overlay harness'}
  if(Test-Path -LiteralPath $runRoot){
   $target=[IO.Path]::GetFullPath($runRoot);$prefix=[IO.Path]::GetFullPath($taskRoot).TrimEnd([char[]]'\/')+[IO.Path]::DirectorySeparatorChar
   if(-not $target.StartsWith($prefix,[StringComparison]::OrdinalIgnoreCase) -or -not [IO.Path]::GetFileName($target).StartsWith('wake-mutations-')){throw 'Wake mutant cleanup escaped owned directory'}
   foreach($item in Get-ChildItem -LiteralPath $target -Recurse -Force){if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw 'Wake mutant cleanup found reparse point'}}
   Remove-Item -LiteralPath $target -Recurse -Force
  }
  [IO.File]::WriteAllText((Join-Path $taskRoot 'scheduler-wake-mutations.json'),(@{results=$results.ToArray();sourceSHA256=$sourceHash;testSHA256=$testHash;pollTestSHA256=$pollTestHash;sourceUnchanged=$true;temporaryRemoved=-not(Test-Path -LiteralPath $runRoot)}|ConvertTo-Json -Depth 5),[Text.UTF8Encoding]::new($false))
 } finally {Pop-Location}
}